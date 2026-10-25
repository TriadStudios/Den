package compiler

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
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

func denText[T renderable](v T)  {}
func denAttr[T renderable](v T)  {}
func denHandler(f func(e Event)) {}
func denMark()                   {}
`

// partKind identifies a dynamic piece of markup.
type partKind int

const (
	partText partKind = iota
	partAttr
	partHandler
	partComponent
)

// part links a markup node to the Go AST that the type checker produced for it.
type part struct {
	kind partKind
	attr *Attr
	elem *Element
	// filled after type checking:
	expr  ast.Expr   // partText, partAttr
	stmts []ast.Stmt // partHandler
	args  []ast.Expr // partComponent, in param order
}

// checked is the result of type checking a whole program.
type checked struct {
	fset  *token.FileSet
	info  *types.Info
	comps []*checkedComponent
}

type checkedComponent struct {
	c     *Component
	decl  *ast.FuncDecl
	stmts []ast.Stmt
	parts map[any]*part // keyed by *Interp, *Attr or *Element (component use)
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

	var src strings.Builder
	src.WriteString(prelude)
	type genComp struct {
		c     *Component
		parts []*part
	}
	var gens []genComp
	for _, f := range files {
		for _, c := range f.Components {
			var parts []*part
			fmt.Fprintf(&src, "\nfunc %s%s(%s) {", c.Name, lineDirective(c.File, c.ParamsPos), c.ParamsSrc)
			src.WriteString(lineDirective(c.File, c.StmtsPos))
			src.WriteString(c.StmtsSrc)
			src.WriteString("\n\tdenMark()\n")
			walkMarkup(c.Root, func(n Node) {
				switch n := n.(type) {
				case *Interp:
					d := lineDirective(c.File, n.Pos_)
					fmt.Fprintf(&src, "\t%sdenText(%s%s)\n", d, d, n.Src)
					parts = append(parts, &part{kind: partText})
				case *Element:
					if n.IsComponent() {
						call, err := componentCall(c.File, n, byName)
						if err != nil {
							errs = append(errs, err)
							return
						}
						src.WriteString("\t" + call + "\n")
						parts = append(parts, &part{kind: partComponent, elem: n})
						return
					}
					switch n.Tag {
					case "script", "iframe", "object", "embed", "frame":
						errs = append(errs, &Error{File: c.File, Pos: n.Pos, Msg: fmt.Sprintf("<%s> is not allowed in components", n.Tag)})
						return
					}
					for _, a := range n.Attrs {
						if err := validateAttr(c.File, n, a); err != nil {
							errs = append(errs, err)
							continue
						}
						if a.Kind != AttrExpr {
							continue
						}
						d := lineDirective(c.File, a.ValuePos)
						if isEventAttr(a.Name) {
							fmt.Fprintf(&src, "\t%sdenHandler(func(e Event) {%s%s\n\t})\n", lineDirective(c.File, a.Pos), d, a.Value)
							parts = append(parts, &part{kind: partHandler, attr: a})
						} else {
							fmt.Fprintf(&src, "\t%sdenAttr(%s%s)\n", d, d, a.Value)
							parts = append(parts, &part{kind: partAttr, attr: a})
						}
					}
				}
			})
			src.WriteString("}\n")
			gens = append(gens, genComp{c, parts})
		}
	}
	// Keep going after markup errors so the type checker can report the rest.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "den_prelude.go", src.String(), parser.SkipObjectResolution)
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
	if len(file.Imports) > 0 {
		p := fset.Position(file.Imports[0].Pos())
		errs = append(errs, &Error{File: p.Filename, Pos: Pos{p.Line, p.Column}, Msg: "imports are not supported yet"})
	}
	if len(errs) > 0 {
		return nil, errs.sorted()
	}

	out := &checked{fset: fset, info: info}
	decls := map[string]*ast.FuncDecl{}
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
			decls[fd.Name.Name] = fd
		}
	}
	for _, g := range gens {
		fd := decls[g.c.Name]
		cc := &checkedComponent{c: g.c, decl: fd, parts: map[any]*part{}}
		mark := -1
		for i, s := range fd.Body.List {
			if isMark(s) {
				mark = i
				break
			}
		}
		cc.stmts = fd.Body.List[:mark]
		rest := fd.Body.List[mark+1:]
		// Re-walk markup in the same order to pair nodes with their AST.
		i := 0
		walkMarkup(g.c.Root, func(n Node) {
			switch n := n.(type) {
			case *Interp:
				pt := g.parts[i]
				pt.expr = rest[i].(*ast.ExprStmt).X.(*ast.CallExpr).Args[0]
				cc.parts[n] = pt
				i++
			case *Element:
				if n.IsComponent() {
					pt := g.parts[i]
					pt.args = rest[i].(*ast.ExprStmt).X.(*ast.CallExpr).Args
					cc.parts[n] = pt
					i++
					return
				}
				for _, a := range n.Attrs {
					if a.Kind != AttrExpr {
						continue
					}
					pt := g.parts[i]
					call := rest[i].(*ast.ExprStmt).X.(*ast.CallExpr)
					if pt.kind == partHandler {
						pt.stmts = call.Args[0].(*ast.FuncLit).Body.List
					} else {
						pt.expr = call.Args[0]
					}
					cc.parts[a] = pt
					i++
				}
			}
		})
		out.comps = append(out.comps, cc)
	}
	return out, nil
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

// walkMarkup visits elements and interpolations in document order. Children of
// component uses are not visited (components take no children yet).
func walkMarkup(n Node, fn func(Node)) {
	fn(n)
	el, ok := n.(*Element)
	if !ok || el.IsComponent() {
		return
	}
	for _, ch := range el.Children {
		walkMarkup(ch, fn)
	}
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

// componentCall turns <Counter label="x" start={1} /> into Counter("x", 1)
// with arguments in parameter order, so go/types checks every prop.
func componentCall(file string, el *Element, byName map[string]*Component) (string, *Error) {
	target, ok := byName[el.Tag]
	if !ok {
		return "", &Error{File: file, Pos: el.Pos, Msg: fmt.Sprintf("unknown component <%s>", el.Tag)}
	}
	if len(el.Children) > 0 {
		return "", &Error{File: file, Pos: el.Pos, Msg: fmt.Sprintf("<%s> does not take children yet", el.Tag)}
	}
	given := map[string]*Attr{}
	for _, a := range el.Attrs {
		found := false
		for _, p := range target.Params {
			if p.Name == a.Name {
				found = true
			}
		}
		if !found {
			return "", &Error{File: file, Pos: a.Pos, Msg: fmt.Sprintf("<%s> has no prop %q", el.Tag, a.Name)}
		}
		given[a.Name] = a
	}
	var args []string
	for _, p := range target.Params {
		a, ok := given[p.Name]
		if !ok {
			return "", &Error{File: file, Pos: el.Pos, Msg: fmt.Sprintf("<%s> is missing prop %q (%s)", el.Tag, p.Name, p.Type)}
		}
		d := lineDirective(file, a.ValuePos)
		switch a.Kind {
		case AttrStatic:
			args = append(args, d+strconv.Quote(a.Value))
		case AttrBool:
			args = append(args, lineDirective(file, a.Pos)+"true")
		case AttrExpr:
			args = append(args, d+a.Value)
		}
	}
	return lineDirective(file, el.Pos) + el.Tag + "(" + strings.Join(args, ", ") + ")", nil
}
