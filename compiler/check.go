package compiler

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"sort"
	"strconv"
	"strings"
)

// prelude declares the built-in den API as ordinary Go so go/types can check
// user code against it. None of this code is ever executed.
const prelude = `package app

type Signal[T any] struct{ v T }

func (s *Signal[T]) Get() T             { return s.v }
func (s *Signal[T]) Set(v T)            {}
func (s *Signal[T]) Update(f func(T) T) {}

type Computed[T any] struct{ v T }

func (c *Computed[T]) Get() T { return c.v }

func signal[T any](v T) *Signal[T]     { return &Signal[T]{v: v} }
func computed[T any](v T) *Computed[T] { return &Computed[T]{v: v} }
func itoa(n int) string                { return "" }

type Event struct{}

func (Event) Value() string   { return "" }
func (Event) Checked() bool   { return false }
func (Event) Key() string     { return "" }
func (Event) PreventDefault() {}

type renderable interface {
	~string | ~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~float32 | ~float64 | ~bool
}

func denText[T renderable | den.Children](v T) {}
func denAttr[T renderable](v T)                {}
func denHandler(f func(e Event))               {}
func denKey[T comparable](v T)                 {}
func denMark()                                 {}

// denSlot stands in for the markup a component use passes to a
// den.Children parameter; f holds it, checked in the caller's scope.
func denSlot(f func()) den.Children { return den.Children{} }

// stored is a signal kept in localStorage under key, as JSON.
func stored[T any](key string, initial T) *Signal[T] { return &Signal[T]{v: initial} }

// hash is a signal holding location.hash, for simple routing.
func hash() *Signal[string] { return nil }
`

const preludeFile = "den_prelude.go"

// denPackage is the built-in package den, imported into every program.
// Children is a struct that holds a func, so Go rejects calling it,
// comparing it (even with nil) and converting to it; outside den only its
// zero value can be written.
const denPackage = `package den

// Children is markup passed to a component: its default content, or a
// named slot. Markup renders it with {x}.
type Children struct{ _ func() }
`

// isChildren reports whether t is den.Children.
func isChildren(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Name() == "Children" && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "den"
}

// partKind identifies a dynamic piece of markup.
type partKind int

const (
	partText partKind = iota
	partAttr
	partHandler
	partComponent
	partIf
	partFor
)

// part links a markup node to the Go AST that the type checker produced for it.
type part struct {
	kind  partKind
	expr  ast.Expr       // partText, partAttr, partIf (condition), partFor (key)
	stmts []ast.Stmt     // partHandler
	args  []ast.Expr     // partComponent, in param order
	slots [][]Node       // partComponent: markup for each den.Children param
	rng   *ast.RangeStmt // partFor
}

// checked is the result of type checking a whole program.
type checked struct {
	fset        *token.FileSet
	info        *types.Info
	comps       []*checkedComponent
	funcs       []*ast.FuncDecl // top-level Go functions
	components  map[string]*Component
	readsSignal map[types.Object]bool // functions that may read a signal
	nilTypes    map[*ast.Ident]types.Type
	svgSlots    map[string]bool // "Comp.param": the den.Children param is rendered inside SVG
}

type checkedComponent struct {
	c     *Component
	decl  *ast.FuncDecl
	stmts []ast.Stmt
	parts map[Node]*part // keyed by *Interp, *If, *For or *Element (component use)
	attrs map[*Attr]*part
}

func lineDirective(file string, p Pos) string {
	return fmt.Sprintf("/*line %s:%d:%d*/", file, p.Line, p.Col)
}

// check builds one Go file from every component and type-checks it.
func check(files []*File) (*checked, error) {
	var errs ErrorList
	byName := map[string]*Component{}
	for _, f := range files {
		for _, c := range f.Components {
			if prev, dup := byName[c.Name]; dup {
				errs = append(errs, &Error{File: c.File, Pos: c.NamePos,
					Msg: fmt.Sprintf("component %s already declared in %s", c.Name, prev.File)})
				continue
			}
			byName[c.Name] = c
		}
	}

	// Imports must come first in the generated file, so they are lifted out
	// of the declarations (keeping positions) and written after the package
	// clause.
	var declSrc strings.Builder
	imports := map[string]bool{}
	var importSrc strings.Builder
	for _, f := range files {
		for _, d := range f.Decls {
			code, imps, err := screenDecl(f.Name, d)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			for _, im := range imps {
				if !imports[im.spec] {
					imports[im.spec] = true
					fmt.Fprintf(&importSrc, "import %s%s\n", lineDirective(f.Name, im.pos), im.spec)
				}
			}
			declSrc.WriteString("\n" + lineDirective(f.Name, d.Pos) + code + "\n")
		}
	}
	var src strings.Builder
	src.WriteString("package app\n\nimport /*line " + preludeFile + ":1:1*/\"den\"\n" + importSrc.String())
	// The import directives would otherwise carry on into the prelude.
	src.WriteString("/*line " + preludeFile + ":2:1*/" + strings.TrimPrefix(prelude, "package app\n"))
	src.WriteString(declSrc.String())
	g := &gen{src: &src, byName: byName, errs: &errs}
	for _, f := range files {
		for _, c := range f.Components {
			g.file = c.File
			fmt.Fprintf(&src, "\nfunc %s%s(%s) {", c.Name, lineDirective(c.File, c.ParamsPos), c.ParamsSrc)
			src.WriteString(lineDirective(c.File, c.StmtsPos))
			src.WriteString(c.StmtsSrc)
			src.WriteString("\n\tdenMark()\n")
			g.node(c.Root)
			src.WriteString("}\n")
		}
	}
	// Keep going after markup errors so the type checker can report the rest.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, preludeFile, src.String(), parser.SkipObjectResolution)
	if err != nil {
		if list, ok := err.(scanner.ErrorList); ok {
			for _, e := range list {
				errs = append(errs, &Error{File: e.Pos.Filename, Pos: Pos{e.Pos.Line, e.Pos.Column}, Msg: e.Msg})
			}
			return nil, errs.sorted()
		}
		return nil, err
	}

	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := types.Config{
		Importer: stdImporter{fset: fset, pkgs: map[string]*types.Package{}},
		Error: func(err error) {
			te := err.(types.Error)
			p := fset.Position(te.Pos)
			msg := te.Msg
			if i := strings.Index(msg, " does not satisfy renderable"); i >= 0 {
				msg = "cannot render " + msg[:i] + ": only strings, numbers and bools can be shown in markup"
			}
			errs = append(errs, &Error{File: p.Filename, Pos: Pos{p.Line, p.Column}, Msg: msg})
		},
	}
	conf.Check("app", fset, []*ast.File{file}, info)
	// Map keys become JS Map keys, which compare like Go only for basic types.
	badKey := map[token.Pos]bool{}
	for x, tv := range info.Types {
		if m, ok := tv.Type.(*types.Map); ok && tv.IsType() && !badKey[x.Pos()] {
			if _, basic := m.Key().Underlying().(*types.Basic); !basic {
				badKey[x.Pos()] = true
				p := fset.Position(x.Pos())
				errs = append(errs, &Error{File: p.Filename, Pos: Pos{p.Line, p.Column},
					Msg: errorf("map keys must be strings, numbers or bools, not %s", m.Key())})
			}
		}
	}
	if len(errs) > 0 {
		return nil, errs.sorted()
	}

	out := &checked{fset: fset, info: info, components: byName, readsSignal: map[types.Object]bool{}, nilTypes: nilTargets(file, info)}
	decls := map[string]*ast.FuncDecl{}
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv != nil {
			continue
		}
		switch {
		case byName[fd.Name.Name] != nil:
			decls[fd.Name.Name] = fd
		case fset.Position(fd.Pos()).Filename != preludeFile:
			out.funcs = append(out.funcs, fd)
		}
	}
	// A function reads a signal if its body does, directly or through
	// another function. Iterate until nothing changes.
	for changed := true; changed; {
		changed = false
		for _, fd := range out.funcs {
			if obj := info.Defs[fd.Name]; !out.readsSignal[obj] && out.reactive(fd.Body) {
				out.readsSignal[obj] = true
				changed = true
			}
		}
	}
	for _, f := range files {
		for _, c := range f.Components {
			fd := decls[c.Name]
			cc := &checkedComponent{c: c, decl: fd, parts: map[Node]*part{}, attrs: map[*Attr]*part{}}
			mark := -1
			for i, s := range fd.Body.List {
				if isMark(s) {
					mark = i
					break
				}
			}
			cc.stmts = fd.Body.List[:mark]
			pr := &pairer{ck: out, cc: cc}
			pr.node(c.Root, fd.Body.List[mark+1:])
			errs = append(errs, pr.errs...)
			out.comps = append(out.comps, cc)
		}
	}
	if len(errs) > 0 {
		return nil, errs.sorted()
	}
	out.findSVGSlots()
	return out, nil
}

// findSVGSlots works out which den.Children parameters each component
// renders inside SVG, directly or by passing them on to another component,
// so that callers build the markup they pass there as SVG elements.
func (ck *checked) findSVGSlots() {
	ck.svgSlots = map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, cc := range ck.comps {
			params := map[types.Object]string{}
			for _, f := range cc.decl.Type.Params.List {
				for _, n := range f.Names {
					params[ck.info.Defs[n]] = n.Name
				}
			}
			var walk func(ns []Node, svg bool)
			walk = func(ns []Node, svg bool) {
				for _, n := range ns {
					switch n := n.(type) {
					case *Interp:
						id, ok := ast.Unparen(cc.parts[n].expr).(*ast.Ident)
						if !ok || !svg || !isChildren(ck.info.TypeOf(id)) {
							continue
						}
						if name, ok := params[ck.info.Uses[id]]; ok && !ck.svgSlots[cc.c.Name+"."+name] {
							ck.svgSlots[cc.c.Name+"."+name] = true
							changed = true
						}
					case *If:
						walk(n.Then, svg)
						walk(n.Else, svg)
					case *For:
						walk(n.Body, svg)
					case *Element:
						if n.IsComponent() {
							for i, p := range ck.components[n.Tag].Params {
								if p.Children {
									walk(cc.parts[n].slots[i], svg || ck.svgSlots[n.Tag+"."+p.Name])
								}
							}
							continue
						}
						svg := svg || n.Tag == "svg"
						walk(n.Children, svg && n.Tag != "foreignObject")
					}
				}
			}
			walk([]Node{cc.c.Root}, false)
		}
	}
}

// nilTargets finds the type each nil is converted to. go/types records nil
// as untyped, but den needs to know which nils are slices.
func nilTargets(file *ast.File, info *types.Info) map[*ast.Ident]types.Type {
	out := map[*ast.Ident]types.Type{}
	set := func(x ast.Expr, t types.Type) {
		if id, ok := ast.Unparen(x).(*ast.Ident); ok && id.Name == "nil" && t != nil {
			out[id] = t
		}
	}
	var stack []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		switch n := n.(type) {
		case *ast.AssignStmt:
			if len(n.Lhs) == len(n.Rhs) {
				for i, r := range n.Rhs {
					set(r, info.TypeOf(n.Lhs[i]))
				}
			}
		case *ast.ValueSpec:
			for i, v := range n.Values {
				if i < len(n.Names) {
					set(v, info.TypeOf(n.Names[i]))
				}
			}
		case *ast.CallExpr:
			sig, ok := info.TypeOf(n.Fun).(*types.Signature)
			if !ok {
				if tv := info.Types[n.Fun]; tv.IsType() && len(n.Args) == 1 {
					set(n.Args[0], tv.Type) // conversion
				}
				return true
			}
			for i, a := range n.Args {
				ps := sig.Params()
				switch {
				case sig.Variadic() && i >= ps.Len()-1 && !n.Ellipsis.IsValid():
					set(a, ps.At(ps.Len()-1).Type().(*types.Slice).Elem())
				case i < ps.Len():
					set(a, ps.At(i).Type())
				}
			}
		case *ast.ReturnStmt:
			for i := len(stack) - 1; i >= 0; i-- {
				var sig *types.Signature
				switch f := stack[i].(type) {
				case *ast.FuncLit:
					sig, _ = info.TypeOf(f).(*types.Signature)
				case *ast.FuncDecl:
					if obj := info.Defs[f.Name]; obj != nil {
						sig, _ = obj.Type().(*types.Signature)
					}
				default:
					continue
				}
				if sig != nil && sig.Results().Len() == len(n.Results) {
					for j, r := range n.Results {
						set(r, sig.Results().At(j).Type())
					}
				}
				break
			}
		case *ast.CompositeLit:
			switch u := info.TypeOf(n).Underlying().(type) {
			case *types.Slice:
				for _, el := range n.Elts {
					set(el, u.Elem())
				}
			case *types.Array:
				for _, el := range n.Elts {
					set(el, u.Elem())
				}
			case *types.Struct:
				for i, el := range n.Elts {
					if kv, ok := el.(*ast.KeyValueExpr); ok {
						for j := 0; j < u.NumFields(); j++ {
							if u.Field(j).Name() == kv.Key.(*ast.Ident).Name {
								set(kv.Value, u.Field(j).Type())
							}
						}
					} else if i < u.NumFields() {
						set(el, u.Field(i).Type())
					}
				}
			}
		}
		return true
	})
	return out
}

type importSpec struct {
	spec string // `"strings"` or `name "strings"`
	pos  Pos
}

// screenDecl rejects top-level Go that den does not support before go/parser
// sees it, so the errors are about den rather than about the generated file.
// It returns the declarations with their imports blanked out (so positions
// stay put) and the imports themselves.
func screenDecl(file string, d *GoDecl) (string, []importSpec, *Error) {
	fset := token.NewFileSet()
	f := fset.AddFile("", fset.Base(), len(d.Src))
	var s scanner.Scanner
	s.Init(f, []byte(d.Src), nil, 0)
	depth := 0
	prev := token.SEMICOLON
	code := []byte(d.Src)
	var imps []importSpec
	posOf := func(pos token.Pos) Pos {
		p := f.Position(pos)
		col := p.Column
		if p.Line == 1 {
			col += d.Pos.Col - 1
		}
		return Pos{d.Pos.Line + p.Line - 1, col}
	}
	blank := func(from, to int) {
		for i := from; i < to; i++ {
			if code[i] != '\n' {
				code[i] = ' '
			}
		}
	}
	for {
		pos, t, lit := s.Scan()
		atStart := prev == token.SEMICOLON
		prev = t
		at := func(msg string) (string, []importSpec, *Error) {
			return "", nil, &Error{File: file, Pos: posOf(pos), Msg: msg}
		}
		switch t {
		case token.EOF:
			return string(code), imps, nil
		case token.LPAREN, token.LBRACK, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACK, token.RBRACE:
			depth--
		case token.IMPORT:
			start := f.Offset(pos)
			grouped := false
			for {
				p2, t2, lit2 := s.Scan()
				switch {
				case t2 == token.LPAREN && !grouped:
					grouped = true
					continue
				case t2 == token.SEMICOLON:
					continue
				case t2 == token.IDENT || t2 == token.PERIOD:
					name := lit2
					if t2 == token.PERIOD || name == "_" {
						return "", nil, &Error{File: file, Pos: posOf(p2), Msg: "dot and blank imports are not supported"}
					}
					p3, t3, lit3 := s.Scan()
					if t3 != token.STRING {
						return "", nil, &Error{File: file, Pos: posOf(p3), Msg: "expected import path"}
					}
					if err := checkImport(file, lit3, posOf(p3)); err != nil {
						return "", nil, err
					}
					imps = append(imps, importSpec{name + " " + lit3, posOf(p2)})
				case t2 == token.STRING:
					if err := checkImport(file, lit2, posOf(p2)); err != nil {
						return "", nil, err
					}
					imps = append(imps, importSpec{lit2, posOf(p2)})
				case t2 == token.RPAREN && grouped:
					grouped = false
				default:
					return "", nil, &Error{File: file, Pos: posOf(p2), Msg: "expected import path"}
				}
				if !grouped {
					p4, _, _ := s.Scan() // the semicolon that ends the import
					blank(start, f.Offset(p4))
					break
				}
			}
			prev = token.SEMICOLON
		case token.PACKAGE:
			return at(".den files have no package clause")
		case token.VAR:
			if depth == 0 {
				return at("package-level variables are not supported yet; keep state in a component")
			}
		case token.FUNC:
			if depth == 0 && atStart {
				if _, next, _ := s.Scan(); next == token.LPAREN {
					return at("methods are not supported yet; use a function that takes the value")
				}
			}
		}
		_ = lit
	}
}

func checkImport(file, lit string, pos Pos) *Error {
	path, _ := strconv.Unquote(lit)
	if stdPackages[path] == "" {
		var names []string
		for p := range stdPackages {
			names = append(names, p)
		}
		sort.Strings(names)
		return &Error{File: file, Pos: pos, Msg: fmt.Sprintf("package %q is not available in den; you can import %s", path, strings.Join(names, ", "))}
	}
	return nil
}

// gen writes the Go that stands in for markup: every {expr} becomes a typed
// call, every handler a func(e Event), every <Card .../> a call to Card, and
// {if} and {for} become real Go if and for statements so their scopes and
// types are Go's own.
type gen struct {
	src    *strings.Builder
	file   string
	byName map[string]*Component
	errs   *ErrorList
}

func (g *gen) d(p Pos) string { return lineDirective(g.file, p) }

func (g *gen) nodes(ns []Node) {
	for _, n := range ns {
		g.node(n)
	}
}

func (g *gen) node(n Node) {
	switch n := n.(type) {
	case *Interp:
		fmt.Fprintf(g.src, "\t%sdenText(%s%s)\n", g.d(n.Pos_), g.d(n.Pos_), n.Src)
	case *If:
		fmt.Fprintf(g.src, "\t%sif %s%s {\n", g.d(n.Pos_), g.d(n.CondPos), n.Cond)
		g.nodes(n.Then)
		if len(n.Else) > 0 {
			g.src.WriteString("\t} else {\n")
			g.nodes(n.Else)
		}
		g.src.WriteString("\t}\n")
	case *For:
		fmt.Fprintf(g.src, "\t%sfor %s%s {\n", g.d(n.Pos_), g.d(n.HeaderPos), n.Header)
		fmt.Fprintf(g.src, "\t%sdenKey(%s%s)\n", g.d(n.KeyPos), g.d(n.KeyPos), n.Key)
		g.nodes(n.Body)
		g.src.WriteString("\t}\n")
	case *Element:
		if n.IsComponent() {
			if err := g.component(n); err != nil {
				*g.errs = append(*g.errs, err)
			}
			return
		}
		switch n.Tag {
		case "script", "iframe", "object", "embed", "frame":
			*g.errs = append(*g.errs, &Error{File: g.file, Pos: n.Pos, Msg: fmt.Sprintf("<%s> is not allowed in components", n.Tag)})
			return
		}
		for _, a := range n.Attrs {
			if err := validateAttr(g.file, n, a); err != nil {
				*g.errs = append(*g.errs, err)
				continue
			}
			if a.Kind != AttrExpr {
				continue
			}
			d := g.d(a.ValuePos)
			if isEventAttr(a.Name) {
				fmt.Fprintf(g.src, "\t%sdenHandler(func(e Event) {%s%s\n\t})\n", g.d(a.Pos), d, a.Value)
			} else {
				fmt.Fprintf(g.src, "\t%sdenAttr(%s%s)\n", d, d, a.Value)
			}
		}
		g.nodes(n.Children)
	}
}

// pairer walks markup in the same order as gen and links each node to the
// checked Go statements that gen wrote for it.
type pairer struct {
	ck   *checked
	cc   *checkedComponent
	errs ErrorList
}

func (pr *pairer) errAt(n ast.Node, format string, args ...any) {
	p := pr.ck.fset.Position(n.Pos())
	pr.errs = append(pr.errs, &Error{File: p.Filename, Pos: Pos{p.Line, p.Column}, Msg: errorf(format, args...)})
}

// errorf is fmt.Sprintf that prints types the way the user wrote them,
// without the generated package's name.
func errorf(format string, args ...any) string {
	for i, a := range args {
		if t, ok := a.(types.Type); ok {
			args[i] = types.TypeString(t, func(p *types.Package) string {
				if p.Path() == "den" {
					return "den"
				}
				return ""
			})
		}
	}
	return fmt.Sprintf(format, args...)
}

func callArgs(s ast.Stmt) []ast.Expr { return s.(*ast.ExprStmt).X.(*ast.CallExpr).Args }

func (pr *pairer) nodes(ns []Node, stmts []ast.Stmt) []ast.Stmt {
	for _, n := range ns {
		stmts = pr.node(n, stmts)
	}
	return stmts
}

func (pr *pairer) node(n Node, stmts []ast.Stmt) []ast.Stmt {
	switch n := n.(type) {
	case *Interp:
		x := callArgs(stmts[0])[0]
		if isChildren(pr.ck.info.TypeOf(x)) && pr.ck.reactive(x) {
			pr.errAt(x, "den.Children shown in markup cannot depend on a signal; choose between them with {if} instead")
		}
		pr.cc.parts[n] = &part{kind: partText, expr: x}
		return stmts[1:]
	case *If:
		is := stmts[0].(*ast.IfStmt)
		pr.cc.parts[n] = &part{kind: partIf, expr: is.Cond}
		pr.nodes(n.Then, is.Body.List)
		if len(n.Else) > 0 {
			pr.nodes(n.Else, is.Else.(*ast.BlockStmt).List)
		}
		return stmts[1:]
	case *For:
		rs, ok := stmts[0].(*ast.RangeStmt)
		if !ok {
			pr.errAt(stmts[0], "for in markup must range over a slice, array, map or int: {for i, x := range xs key x.ID { ... }}")
			return stmts[1:]
		}
		if rs.Tok == token.ASSIGN {
			pr.errAt(rs, "for in markup must declare its variables with :=")
		}
		switch t := pr.ck.info.TypeOf(rs.X).Underlying().(type) {
		case *types.Slice, *types.Array, *types.Map:
		case *types.Basic:
			if t.Info()&types.IsInteger == 0 {
				pr.errAt(rs.X, "for in markup can range over slices, arrays, maps and ints, not %s", t)
			}
		default:
			pr.errAt(rs.X, "for in markup can range over slices, arrays, maps and ints, not %s", pr.ck.info.TypeOf(rs.X))
		}
		key := callArgs(rs.Body.List[0])[0]
		if _, ok := pr.ck.info.TypeOf(key).Underlying().(*types.Basic); !ok {
			pr.errAt(key, "list keys must be strings, numbers or bools, not %s", pr.ck.info.TypeOf(key))
		}
		pr.cc.parts[n] = &part{kind: partFor, expr: key, rng: rs}
		pr.nodes(n.Body, rs.Body.List[1:])
		return stmts[1:]
	case *Element:
		if n.IsComponent() {
			pt := &part{kind: partComponent, args: callArgs(stmts[0])}
			for i, p := range pr.ck.components[n.Tag].Params {
				pt.slots = append(pt.slots, nil)
				if !p.Children {
					continue
				}
				if nodes, ok := slotContent(n, p); ok {
					// gen wrote denSlot(func() { ... }) around the markup.
					pt.slots[i] = nodes
					fn := pt.args[i].(*ast.CallExpr).Args[0].(*ast.FuncLit)
					pr.nodes(nodes, fn.Body.List)
				}
			}
			pr.cc.parts[n] = pt
			return stmts[1:]
		}
		for _, a := range n.Attrs {
			if a.Kind != AttrExpr {
				continue
			}
			arg := callArgs(stmts[0])[0]
			if isEventAttr(a.Name) {
				pr.cc.attrs[a] = &part{kind: partHandler, stmts: arg.(*ast.FuncLit).Body.List}
			} else {
				pr.cc.attrs[a] = &part{kind: partAttr, expr: arg}
			}
			stmts = stmts[1:]
		}
		return pr.nodes(n.Children, stmts)
	}
	return stmts
}

func isMark(s ast.Stmt) bool {
	es, ok := s.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == "denMark"
}

// reactive reports whether evaluating n may read a signal, i.e. whether a DOM
// binding for it needs an effect. Decided at compile time from types. Calls
// through function values are assumed to read signals.
func (ck *checked) reactive(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := ast.Unparen(call.Fun).(type) {
		case *ast.SelectorExpr:
			if fun.Sel.Name == "Get" {
				if recv := ck.recvName(fun); recv == "Signal" || recv == "Computed" {
					found = true
				}
			}
		case *ast.Ident:
			switch o := ck.info.Uses[fun].(type) {
			case *types.Var:
				found = true
			case *types.Func:
				found = ck.readsSignal[o]
			}
		}
		return true
	})
	return found
}

// recvName returns the type name of a method call's receiver.
func (ck *checked) recvName(sel *ast.SelectorExpr) string {
	s := ck.info.Selections[sel]
	if s == nil || s.Kind() != types.MethodVal {
		return ""
	}
	t := s.Recv()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := t.(*types.Named); ok {
		return n.Obj().Name()
	}
	return ""
}

// isPrelude reports whether obj is declared by den's built-in prelude.
func (ck *checked) isPrelude(obj types.Object) bool {
	return ck.components[obj.Name()] == nil && ck.fset.Position(obj.Pos()).Filename == preludeFile
}

func isEventAttr(name string) bool {
	return len(name) > 2 && strings.HasPrefix(name, "on") && name[2] >= 'a' && name[2] <= 'z'
}

var urlAttrs = map[string]bool{"href": true, "src": true, "action": true, "formaction": true, "poster": true, "cite": true, "xlink:href": true}

// validateAttr enforces the security rules that can be decided at compile time.
func validateAttr(file string, el *Element, a *Attr) *Error {
	bad := func(format string, args ...any) *Error {
		return &Error{File: file, Pos: a.Pos, Msg: fmt.Sprintf(format, args...)}
	}
	lname := strings.ToLower(a.Name)
	switch {
	case lname == "innerhtml" || lname == "outerhtml" || lname == "srcdoc" || strings.HasPrefix(lname, "dangerously"):
		return bad("%q is not allowed: den never injects raw HTML", a.Name)
	case isEventAttr(a.Name) && a.Kind != AttrExpr:
		return bad("event handler %q must be Go code in {...}; inline JS strings are not allowed", a.Name)
	case strings.HasPrefix(lname, "on") && !isEventAttr(a.Name):
		return bad("unknown event attribute %q (use lowercase, e.g. onclick)", a.Name)
	case urlAttrs[lname] && a.Kind == AttrStatic:
		if !safeStaticURL(a.Value) {
			return bad("unsafe URL in %s: only http, https, mailto, tel and relative URLs are allowed", a.Name)
		}
	}
	return nil
}

func safeStaticURL(u string) bool {
	s := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, u)
	s = strings.TrimLeft(s, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x0b\x0c\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f ")
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return true
	}
	scheme := strings.ToLower(s[:i])
	for _, r := range scheme {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.') {
			return true // ':' is not part of a scheme, e.g. "/path?a=b:c"
		}
	}
	switch scheme {
	case "http", "https", "mailto", "tel":
		return true
	}
	return false
}

// component turns <Counter label="x" start={1} /> into Counter("x", 1)
// with arguments in parameter order, so go/types checks every prop. The
// markup for a den.Children parameter becomes denSlot(func() { ... }), so
// it is checked in the caller's scope; an unfilled one is denSlot(nil).
func (g *gen) component(el *Element) *Error {
	bad := func(p Pos, format string, args ...any) *Error {
		return &Error{File: g.file, Pos: p, Msg: fmt.Sprintf(format, args...)}
	}
	target, ok := g.byName[el.Tag]
	if !ok {
		return bad(el.Pos, "unknown component <%s>", el.Tag)
	}
	param := func(name string) (Param, bool) {
		for _, p := range target.Params {
			if p.Name == name {
				return p, true
			}
		}
		return Param{}, false
	}
	if p, ok := param("children"); len(el.Children) > 0 && !(ok && p.Children) {
		return bad(el.Pos, "<%s> does not take children: it has no children den.Children parameter", el.Tag)
	}
	for _, s := range el.Slots {
		if p, ok := param(s.Tag[1:]); !ok || !p.Children {
			return bad(s.Pos, "<%s> has no slot %q", el.Tag, s.Tag[1:])
		}
	}
	given := map[string]*Attr{}
	for _, a := range el.Attrs {
		p, ok := param(a.Name)
		switch {
		case !ok:
			return bad(a.Pos, "<%s> has no prop %q", el.Tag, a.Name)
		case p.Children && p.Name == "children":
			return bad(a.Pos, "children of <%s> go between <%s> and </%s>, not in an attribute", el.Tag, el.Tag, el.Tag)
		case p.Children:
			return bad(a.Pos, "slot %q of <%s> is filled with <:%s>...</:%s> between the tags, not with an attribute", a.Name, el.Tag, a.Name, a.Name)
		}
		given[a.Name] = a
	}
	for _, p := range target.Params {
		if _, ok := given[p.Name]; !ok && !p.Children {
			return bad(el.Pos, "<%s> is missing prop %q (%s)", el.Tag, p.Name, p.Type)
		}
	}
	fmt.Fprintf(g.src, "\t%s%s(", g.d(el.Pos), el.Tag)
	for i, p := range target.Params {
		if i > 0 {
			g.src.WriteString(", ")
		}
		if p.Children {
			nodes, ok := slotContent(el, p)
			if !ok {
				g.src.WriteString("denSlot(nil)")
				continue
			}
			g.src.WriteString("denSlot(func() {\n")
			g.nodes(nodes)
			g.src.WriteString("\t})")
			continue
		}
		a := given[p.Name]
		switch a.Kind {
		case AttrStatic:
			g.src.WriteString(g.d(a.ValuePos) + strconv.Quote(a.Value))
		case AttrBool:
			g.src.WriteString(g.d(a.Pos) + "true")
		case AttrExpr:
			g.src.WriteString(g.d(a.ValuePos) + a.Value)
		}
	}
	g.src.WriteString(")\n")
	return nil
}
