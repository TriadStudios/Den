package compiler

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"regexp"
	"strings"
)

//go:embed runtime.js
var runtimeJS string

// Result is a compiled program.
type Result struct {
	JS         string   // complete, self-contained app.js
	Components []string // component names, in source order
}

// Source is one input file.
type Source struct {
	Name string
	Code string
}

// Compile parses, type-checks and emits JavaScript for a set of .den files.
// The component named App is mounted into #app.
func Compile(sources []Source) (*Result, error) {
	var files []*File
	var errs ErrorList
	for _, s := range sources {
		f, err := Parse(s.Name, s.Code)
		if err != nil {
			errs = append(errs, err.(ErrorList)...)
			continue
		}
		files = append(files, f)
	}
	if len(errs) > 0 {
		return nil, errs.sorted()
	}
	ck, err := check(files)
	if err != nil {
		return nil, err
	}
	var body strings.Builder
	e := &emitter{ck: ck, w: &body}
	for _, fd := range ck.funcs {
		e.funcDecl(fd)
	}
	var app *Component
	var names []string
	for _, cc := range ck.comps {
		e.component(cc)
		names = append(names, cc.c.Name)
		if cc.c.Name == "App" {
			app = cc.c
		}
	}
	if app == nil {
		file := "app.den"
		if len(sources) > 0 {
			file = sources[0].Name
		}
		e.errs = append(e.errs, &Error{File: file, Pos: Pos{1, 1}, Msg: "no App component: the root component must be named App"})
	} else if len(app.Params) > 0 {
		e.errs = append(e.errs, &Error{File: app.File, Pos: app.ParamsPos, Msg: "App is the root component and cannot take props"})
	}
	if len(e.errs) > 0 {
		return nil, e.errs.sorted()
	}
	body.WriteString("$mount(App, document.getElementById(\"app\"));\n")
	var out strings.Builder
	out.WriteString("// Built by Den. Do not edit.\n(() => {\n\"use strict\";\n")
	out.WriteString(runtimeFor(body.String()))
	out.WriteString(body.String())
	out.WriteString("})();\n")
	return &Result{JS: out.String(), Components: names}, nil
}

var (
	runtimeDecl = regexp.MustCompile(`^(?:function|const|let)\s+(\$\w+)`)
	runtimeRef  = regexp.MustCompile(`\$\w+`)
)

// runtimeFor returns the top-level runtime declarations that code uses,
// directly or through other runtime declarations, in runtime.js order.
func runtimeFor(code string) string {
	type decl struct {
		name, src string
		used      bool
	}
	var decls []*decl
	byName := map[string]*decl{}
	for _, line := range strings.Split(runtimeJS, "\n") {
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "//") {
			continue
		}
		if m := runtimeDecl.FindStringSubmatch(line); m != nil {
			d := &decl{name: m[1]}
			decls = append(decls, d)
			byName[d.name] = d
		}
		if len(decls) > 0 {
			decls[len(decls)-1].src += line + "\n"
		}
	}
	work := runtimeRef.FindAllString(code, -1)
	for len(work) > 0 {
		d := byName[work[len(work)-1]]
		work = work[:len(work)-1]
		if d == nil || d.used {
			continue
		}
		d.used = true
		work = append(work, runtimeRef.FindAllString(d.src, -1)...)
	}
	var b strings.Builder
	for _, d := range decls {
		if d.used {
			b.WriteString(d.src)
		}
	}
	return b.String()
}

type emitter struct {
	ck      *checked
	w       *strings.Builder
	ind     string
	errs    ErrorList
	n       int // counter for generated names
	cc      *checkedComponent
	results []string // named results of the function being emitted
}

func (e *emitter) errAt(n ast.Node, format string, args ...any) {
	p := e.ck.fset.Position(n.Pos())
	e.errs = append(e.errs, &Error{File: p.Filename, Pos: Pos{p.Line, p.Column}, Msg: fmt.Sprintf(format, args...)})
}

// line writes one line of JS at the current indent.
func (e *emitter) line(format string, args ...any) {
	e.w.WriteString(e.ind)
	fmt.Fprintf(e.w, format, args...)
	e.w.WriteByte('\n')
}

// capture runs fn with output going to a new buffer at indent ind.
func (e *emitter) capture(ind string, fn func()) string {
	w, i := e.w, e.ind
	var b strings.Builder
	e.w, e.ind = &b, ind
	fn()
	e.w, e.ind = w, i
	return b.String()
}

func (e *emitter) indented(fn func()) {
	e.ind += "  "
	fn()
	e.ind = e.ind[:len(e.ind)-2]
}

func (e *emitter) name(prefix string) string {
	e.n++
	return fmt.Sprintf("%s%d", prefix, e.n-1)
}

func (e *emitter) component(cc *checkedComponent) {
	e.cc, e.n, e.ind, e.results = cc, 0, "  ", nil
	var params []string
	for _, p := range cc.c.Params {
		params = append(params, jsIdent(p.Name))
	}
	fmt.Fprintf(e.w, "function %s(%s) {\n", jsIdent(cc.c.Name), strings.Join(params, ", "))
	e.stmts(cc.stmts)
	root := e.element(cc.c.Root, false)
	e.line("return %s;", root)
	e.w.WriteString("}\n")
}

func (e *emitter) funcDecl(fd *ast.FuncDecl) {
	e.n, e.ind = 0, "  "
	fmt.Fprintf(e.w, "function %s(%s) {\n", jsIdent(fd.Name.Name), e.params(fd.Type.Params))
	e.funcBody(fd.Type, fd.Body)
	e.w.WriteString("}\n")
}

func (e *emitter) params(fl *ast.FieldList) string {
	var out []string
	for _, f := range fl.List {
		names := f.Names
		if len(names) == 0 {
			names = []*ast.Ident{{Name: "_"}}
		}
		for _, n := range names {
			p := jsIdent(n.Name)
			if n.Name == "_" {
				p = e.name("$p")
			}
			if _, ok := f.Type.(*ast.Ellipsis); ok {
				p = "..." + p
			}
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}

func (e *emitter) funcBody(ft *ast.FuncType, body *ast.BlockStmt) {
	saved := e.results
	e.results = nil
	if ft.Results != nil {
		for _, f := range ft.Results.List {
			for _, n := range f.Names {
				e.line("let %s = %s;", jsIdent(n.Name), zeroValue(e.ck.info.Defs[n].Type()))
				e.results = append(e.results, jsIdent(n.Name))
			}
		}
	}
	e.stmts(body.List)
	e.results = saved
}

// element emits code that builds el and returns the JS expression for it.
func (e *emitter) element(el *Element, svg bool) string {
	if el.IsComponent() {
		pt := e.cc.parts[el]
		var args []string
		for _, a := range pt.args {
			args = append(args, e.expr(a))
		}
		return jsIdent(el.Tag) + "(" + strings.Join(args, ", ") + ")"
	}
	svg = svg || el.Tag == "svg"
	v := e.name("$e")
	maker := "$h"
	if svg {
		maker = "$hs"
	}
	e.line("const %s = %s(%s);", v, maker, jsString(el.Tag))
	for _, a := range el.Attrs {
		switch a.Kind {
		case AttrStatic:
			e.line("%s.setAttribute(%s, %s);", v, jsString(a.Name), jsString(a.Value))
		case AttrBool:
			e.line("%s.setAttribute(%s, \"\");", v, jsString(a.Name))
		case AttrExpr:
			pt := e.cc.attrs[a]
			if pt.kind == partHandler {
				body := e.capture(e.ind+"  ", func() { e.stmts(pt.stmts) })
				e.line("$on(%s, %s, (e) => {\n%s%s});", v, jsString(a.Name[2:]), body, e.ind)
				continue
			}
			x := e.expr(pt.expr)
			if e.ck.reactive(pt.expr) {
				e.line("$attr(%s, %s, () => %s);", v, jsString(a.Name), x)
			} else {
				e.line("$set(%s, %s, %s);", v, jsString(a.Name), x)
			}
		}
	}
	e.children(v, el.Children, svg && el.Tag != "foreignObject")
	return v
}

// children emits code that appends nodes to the DOM node named parent.
func (e *emitter) children(parent string, nodes []Node, svg bool) {
	for _, ch := range nodes {
		switch ch := ch.(type) {
		case *Text:
			e.line("%s.append(%s);", parent, jsString(ch.Value))
		case *Interp:
			pt := e.cc.parts[ch]
			if tv := e.ck.info.Types[pt.expr]; tv.Value != nil {
				e.line("%s.append(%s);", parent, jsString(constString(tv.Value)))
			} else if e.ck.reactive(pt.expr) {
				e.line("$text(%s, () => %s);", parent, e.expr(pt.expr))
			} else {
				e.line("%s.append(String(%s));", parent, e.expr(pt.expr))
			}
		case *Element:
			e.line("%s.append(%s);", parent, e.element(ch, svg))
		case *If:
			e.ifNode(parent, ch, svg)
		case *For:
			e.forNode(parent, ch, svg)
		}
	}
}

// block emits a function that builds nodes into a fresh fragment.
func (e *emitter) block(params string, nodes []Node, svg bool) string {
	f := e.name("$f")
	body := e.capture(e.ind+"  ", func() {
		e.line("const %s = $frag();", f)
		e.children(f, nodes, svg)
		e.line("return %s;", f)
	})
	return "(" + params + ") => {\n" + body + e.ind + "}"
}

// ifNode emits {if}. A condition that reads no signal can never change, so
// it compiles to a plain JS if; otherwise $if swaps the branches.
func (e *emitter) ifNode(parent string, n *If, svg bool) {
	pt := e.cc.parts[n]
	cond := e.expr(pt.expr)
	if !e.ck.reactive(pt.expr) {
		e.line("if (%s) {", cond)
		e.indented(func() { e.children(parent, n.Then, svg) })
		if len(n.Else) > 0 {
			e.line("} else {")
			e.indented(func() { e.children(parent, n.Else, svg) })
		}
		e.line("}")
		return
	}
	els := "null"
	if len(n.Else) > 0 {
		els = e.block("", n.Else, svg)
	}
	e.line("$if(%s, () => %s, %s, %s);", parent, cond, e.block("", n.Then, svg), els)
}

// forNode emits {for}. A list that reads no signal compiles to a plain JS
// loop; otherwise $for keeps the rows in sync by key.
func (e *emitter) forNode(parent string, n *For, svg bool) {
	pt := e.cc.parts[n]
	rs := pt.rng
	k, v := loopVar(rs.Key), loopVar(rs.Value)
	b, isInt := e.ck.info.TypeOf(rs.X).Underlying().(*types.Basic)
	isInt = isInt && b.Info()&types.IsInteger != 0
	list := e.expr(rs.X)
	if !e.ck.reactive(rs.X) {
		l := e.name("$l")
		e.line("const %s = %s;", l, list)
		switch {
		case isInt:
			i := or(k, e.name("$i"))
			e.line("for (let %s = 0; %s < %s; %s++) {", i, i, l, i)
		case k != "" && v != "":
			e.line("for (const [%s, %s] of %s.entries()) {", k, v, l)
		case k != "":
			e.line("for (const %s of %s.keys()) {", k, l)
		default:
			e.line("for (const %s of %s) {", or(v, e.name("$v")), l)
		}
		e.indented(func() { e.children(parent, n.Body, svg) })
		e.line("}")
		return
	}
	var params string
	useIndex := false
	if isInt {
		list = "$seq(" + list + ")"
		params = or(k, "$i")
	} else {
		params = or(v, "$v")
		if k != "" {
			params += ", " + k
			useIndex = true
		}
	}
	e.line("$for(%s, () => %s, (%s) => %s, %s, %t);", parent, list, params, e.expr(pt.expr), e.block(params, n.Body, svg), useIndex)
}

// loopVar returns the JS name of a range variable, or "" if there is none.
func loopVar(x ast.Expr) string {
	id, ok := x.(*ast.Ident)
	if !ok || id.Name == "_" {
		return ""
	}
	return jsIdent(id.Name)
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (e *emitter) stmts(list []ast.Stmt) {
	for _, s := range list {
		e.stmt(s)
	}
}

func (e *emitter) stmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.ExprStmt:
		e.line("%s;", e.expr(s.X))
	case *ast.AssignStmt:
		e.assign(s)
	case *ast.IncDecStmt:
		if id, ok := ast.Unparen(s.X).(*ast.Ident); ok {
			e.line("%s%s;", jsIdent(id.Name), s.Tok)
			return
		}
		op := token.ADD
		if s.Tok == token.DEC {
			op = token.SUB
		}
		e.store(s.X, e.binary(op, s.X, nil, "1", e.ck.info.TypeOf(s.X)))
	case *ast.DeclStmt:
		gd, ok := s.Decl.(*ast.GenDecl)
		if !ok {
			e.errAt(s, "this declaration is not supported here")
			return
		}
		if gd.Tok != token.VAR {
			return // constants are folded and types need no code
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, n := range vs.Names {
				var init string
				if i < len(vs.Values) {
					init = e.expr(vs.Values[i])
				} else {
					init = zeroValue(e.ck.info.Defs[n].Type())
				}
				e.line("let %s = %s;", jsIdent(n.Name), init)
			}
		}
	case *ast.IfStmt:
		e.ifStmt(s, "")
	case *ast.ForStmt:
		var init, post string
		if s.Init != nil {
			init = e.inline(s.Init)
		}
		if s.Post != nil {
			post = e.inline(s.Post)
		}
		cond := ""
		if s.Cond != nil {
			cond = e.expr(s.Cond)
		}
		e.line("for (%s; %s; %s) {", init, cond, post)
		e.indented(func() { e.stmts(s.Body.List) })
		e.line("}")
	case *ast.RangeStmt:
		e.rangeStmt(s)
	case *ast.BranchStmt:
		if s.Label != nil || (s.Tok != token.BREAK && s.Tok != token.CONTINUE) {
			e.errAt(s, "%s is not supported yet", s.Tok)
			return
		}
		e.line("%s;", s.Tok)
	case *ast.ReturnStmt:
		switch {
		case len(s.Results) == 1:
			e.line("return %s;", e.expr(s.Results[0]))
		case len(s.Results) > 1 || len(e.results) > 1:
			e.errAt(s, "multiple return values are not supported yet")
		case len(e.results) == 1:
			e.line("return %s;", e.results[0])
		default:
			e.line("return;")
		}
	case *ast.BlockStmt:
		e.line("{")
		e.indented(func() { e.stmts(s.List) })
		e.line("}")
	case *ast.EmptyStmt:
	default:
		e.errAt(s, "this statement is not supported yet")
	}
}

// inline emits a simple statement for use in a for clause.
func (e *emitter) inline(s ast.Stmt) string {
	out := e.capture("", func() { e.stmt(s) })
	if strings.Count(out, "\n") > 1 {
		e.errAt(s, "this statement is too complex for a for clause")
	}
	return strings.TrimSuffix(strings.TrimSpace(out), ";")
}

func (e *emitter) ifStmt(s *ast.IfStmt, prefix string) {
	if s.Init != nil {
		e.errAt(s, "if with an init statement is not supported yet")
		return
	}
	e.line("%sif (%s) {", prefix, e.expr(s.Cond))
	e.indented(func() { e.stmts(s.Body.List) })
	switch el := s.Else.(type) {
	case *ast.IfStmt:
		e.ifStmt(el, "} else ")
		return
	case *ast.BlockStmt:
		e.line("} else {")
		e.indented(func() { e.stmts(el.List) })
	}
	e.line("}")
}

// rangeStmt emits a range loop. Go evaluates the range expression once and
// gives each iteration its own copies of the variables, so the loop runs on
// hidden counters and assigns the variables per iteration.
func (e *emitter) rangeStmt(s *ast.RangeStmt) {
	if s.Tok == token.ASSIGN {
		e.errAt(s, "range with = is not supported yet; use :=")
		return
	}
	k, v := loopVar(s.Key), loopVar(s.Value)
	l, i := e.name("$l"), e.name("$k")
	e.line("const %s = %s;", l, e.expr(s.X))
	switch t := e.ck.info.TypeOf(s.X).Underlying().(type) {
	case *types.Basic:
		if t.Info()&types.IsInteger == 0 {
			e.errAt(s.X, "ranging over %s is not supported yet", t)
			return
		}
		e.line("for (let %s = 0; %s < %s; %s++) {", i, i, l, i)
		if k != "" {
			e.line("  let %s = %s;", k, i)
		}
	case *types.Slice, *types.Array:
		e.line("for (let %s = 0; %s < %s.length; %s++) {", i, i, l, i)
		var vars []string
		if k != "" {
			vars = append(vars, k+" = "+i)
		}
		if v != "" {
			vars = append(vars, v+" = "+l+"["+i+"]")
		}
		if len(vars) > 0 {
			e.line("  let %s;", strings.Join(vars, ", "))
		}
	default:
		e.errAt(s.X, "ranging over %s is not supported yet", e.ck.info.TypeOf(s.X))
		return
	}
	e.indented(func() { e.stmts(s.Body.List) })
	e.line("}")
}

var assignOps = map[token.Token]token.Token{
	token.ADD_ASSIGN: token.ADD, token.SUB_ASSIGN: token.SUB, token.MUL_ASSIGN: token.MUL,
	token.QUO_ASSIGN: token.QUO, token.REM_ASSIGN: token.REM, token.AND_ASSIGN: token.AND,
	token.OR_ASSIGN: token.OR, token.XOR_ASSIGN: token.XOR, token.SHL_ASSIGN: token.SHL,
	token.SHR_ASSIGN: token.SHR,
}

func (e *emitter) assign(s *ast.AssignStmt) {
	if len(s.Lhs) != 1 || len(s.Rhs) != 1 {
		e.errAt(s, "multiple assignment is not supported yet")
		return
	}
	lhs, rhs := s.Lhs[0], s.Rhs[0]
	switch s.Tok {
	case token.DEFINE:
		id := lhs.(*ast.Ident)
		if id.Name == "_" {
			e.line("%s;", e.expr(rhs))
			return
		}
		e.line("let %s = %s;", jsIdent(id.Name), e.expr(rhs))
	case token.ASSIGN:
		e.store(lhs, e.expr(rhs))
	default:
		op, ok := assignOps[s.Tok]
		if !ok {
			e.errAt(s, "%s is not supported yet", s.Tok)
			return
		}
		if id, ok := lhs.(*ast.Ident); ok && op != token.QUO {
			e.line("%s %s %s;", jsIdent(id.Name), s.Tok, e.expr(rhs))
			return
		}
		e.store(lhs, e.binary(op, lhs, rhs, "", e.ck.info.TypeOf(lhs)))
	}
}

// store assigns val to lhs. Structs and arrays are values in Go, so den never
// changes one in place: assigning to a field or array element builds a new
// value and assigns it to the variable at the root. Two variables can then
// share a JS object without either seeing the other's writes.
func (e *emitter) store(lhs ast.Expr, val string) {
	switch x := ast.Unparen(lhs).(type) {
	case *ast.Ident:
		if x.Name == "_" {
			e.line("%s;", val)
			return
		}
		e.line("%s = %s;", jsIdent(x.Name), val)
	case *ast.SelectorExpr:
		sel := e.ck.info.Selections[x]
		if sel == nil || sel.Kind() != types.FieldVal {
			e.errAt(x, "cannot assign to %s", x.Sel.Name)
			return
		}
		if _, ok := sel.Recv().Underlying().(*types.Pointer); ok {
			e.errAt(x, "assigning through pointers is not supported")
			return
		}
		e.store(x.X, withField(e.expr(x.X), fieldPath(sel), val))
	case *ast.IndexExpr:
		if _, ok := e.ck.info.TypeOf(x.X).Underlying().(*types.Array); !ok {
			e.errAt(x, "slice elements are read-only in den for now; build a new slice instead (for example with append)")
			return
		}
		e.store(x.X, e.expr(x.X)+".with("+e.expr(x.Index)+", "+val+")")
	default:
		e.errAt(lhs, "cannot assign to this expression")
	}
}

func withField(obj string, path []string, val string) string {
	if len(path) > 1 {
		val = withField(obj+"."+path[0], path[1:], val)
	}
	return "({..." + obj + ", " + path[0] + ": " + val + "})"
}

// fieldPath returns the JS property names that reach a selected field,
// including the embedded structs a promoted field goes through.
func fieldPath(sel *types.Selection) []string {
	t := sel.Recv()
	var path []string
	for _, i := range sel.Index() {
		if p, ok := t.Underlying().(*types.Pointer); ok {
			t = p.Elem()
		}
		f := t.Underlying().(*types.Struct).Field(i)
		path = append(path, fieldName(f.Name()))
		t = f.Type()
	}
	return path
}

func fieldName(name string) string {
	if name == "__proto__" {
		return name + "$"
	}
	return name
}

func isInt(t types.Type) bool {
	if t == nil {
		return false
	}
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsInteger != 0
}

var binOps = map[token.Token]string{
	token.ADD: "+", token.SUB: "-", token.MUL: "*", token.QUO: "/", token.REM: "%",
	token.EQL: "===", token.NEQ: "!==", token.LSS: "<", token.GTR: ">", token.LEQ: "<=", token.GEQ: ">=",
	token.LAND: "&&", token.LOR: "||", token.AND: "&", token.OR: "|", token.XOR: "^",
	token.SHL: "<<", token.SHR: ">>",
}

// binary emits x op y. y is either an expression or, when nil, the JS ys.
func (e *emitter) binary(op token.Token, x, y ast.Expr, ys string, result types.Type) string {
	js, ok := binOps[op]
	if !ok {
		e.errAt(x, "operator %s is not supported yet", op)
		return "undefined"
	}
	xs := e.expr(x)
	if y != nil {
		ys = e.expr(y)
	}
	if op == token.EQL || op == token.NEQ {
		switch t := e.ck.info.TypeOf(x).Underlying().(type) {
		case *types.Struct, *types.Array:
			if op == token.NEQ {
				return "!$eq(" + xs + ", " + ys + ")"
			}
			return "$eq(" + xs + ", " + ys + ")"
		case *types.Slice:
			e.errAt(x, "comparing a %s with nil is not supported; use len(...) == 0", t)
		}
	}
	s := xs + " " + js + " " + ys
	if op == token.QUO && isInt(result) {
		return "Math.trunc(" + s + ")"
	}
	return "(" + s + ")"
}

func (e *emitter) expr(x ast.Expr) string {
	if tv, ok := e.ck.info.Types[x]; ok && tv.Value != nil {
		return constJS(tv.Value)
	}
	switch x := x.(type) {
	case *ast.Ident:
		switch x.Name {
		case "true", "false":
			return x.Name
		case "nil":
			if t := e.ck.nilTypes[x]; t != nil && isSlice(t) {
				return "[]" // a nil slice behaves like an empty one
			}
			return "null"
		}
		return jsIdent(x.Name)
	case *ast.ParenExpr:
		return "(" + e.expr(x.X) + ")"
	case *ast.BinaryExpr:
		return e.binary(x.Op, x.X, x.Y, "", e.ck.info.TypeOf(x))
	case *ast.UnaryExpr:
		switch x.Op {
		case token.NOT, token.SUB, token.ADD:
			return x.Op.String() + e.expr(x.X)
		case token.XOR:
			return "~" + e.expr(x.X)
		case token.AND:
			e.errAt(x, "pointers are not supported yet")
			return "undefined"
		}
		e.errAt(x, "operator %s is not supported", x.Op)
		return "undefined"
	case *ast.CallExpr:
		return e.call(x)
	case *ast.FuncLit:
		params := e.params(x.Type.Params)
		body := e.capture(e.ind+"  ", func() { e.funcBody(x.Type, x.Body) })
		return "((" + params + ") => {\n" + body + e.ind + "})"
	case *ast.CompositeLit:
		return e.compositeLit(x)
	case *ast.SelectorExpr:
		sel := e.ck.info.Selections[x]
		if sel == nil || sel.Kind() != types.FieldVal || e.ck.isPrelude(sel.Obj()) {
			e.errAt(x.Sel, "%s is not supported here", x.Sel.Name)
			return "undefined"
		}
		return e.expr(x.X) + "." + strings.Join(fieldPath(sel), ".")
	case *ast.IndexExpr:
		switch e.ck.info.TypeOf(x.X).Underlying().(type) {
		case *types.Slice, *types.Array:
			return e.expr(x.X) + "[" + e.expr(x.Index) + "]"
		case *types.Signature:
			return e.expr(x.X) // explicit instantiation of a generic function
		}
		e.errAt(x, "indexing %s is not supported yet", e.ck.info.TypeOf(x.X))
		return "undefined"
	case *ast.IndexListExpr:
		return e.expr(x.X)
	case *ast.SliceExpr:
		switch e.ck.info.TypeOf(x.X).Underlying().(type) {
		case *types.Slice, *types.Array:
		default:
			e.errAt(x, "slicing %s is not supported yet", e.ck.info.TypeOf(x.X))
			return "undefined"
		}
		if x.Slice3 {
			e.errAt(x, "3-index slices are not supported")
			return "undefined"
		}
		lo, hi := "0", ""
		if x.Low != nil {
			lo = e.expr(x.Low)
		}
		if x.High != nil {
			hi = ", " + e.expr(x.High)
		}
		return e.expr(x.X) + ".slice(" + lo + hi + ")"
	}
	e.errAt(x, "this expression is not supported yet")
	return "undefined"
}

func (e *emitter) compositeLit(x *ast.CompositeLit) string {
	t := e.ck.info.TypeOf(x)
	var parts []string
	switch u := t.Underlying().(type) {
	case *types.Struct:
		vals := map[string]string{}
		for i, el := range x.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				vals[kv.Key.(*ast.Ident).Name] = e.expr(kv.Value)
			} else {
				vals[u.Field(i).Name()] = e.expr(el)
			}
		}
		for i := 0; i < u.NumFields(); i++ {
			f := u.Field(i)
			v, ok := vals[f.Name()]
			if !ok {
				v = zeroValue(f.Type())
			}
			parts = append(parts, fieldName(f.Name())+": "+v)
		}
		return "({" + strings.Join(parts, ", ") + "})"
	case *types.Slice, *types.Array:
		for _, el := range x.Elts {
			if _, ok := el.(*ast.KeyValueExpr); ok {
				e.errAt(el, "indexed elements in literals are not supported yet")
				return "undefined"
			}
			parts = append(parts, e.expr(el))
		}
		if a, ok := u.(*types.Array); ok {
			for int64(len(parts)) < a.Len() {
				parts = append(parts, zeroValue(a.Elem()))
			}
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	e.errAt(x, "%s literals are not supported yet", t)
	return "undefined"
}

func (e *emitter) call(x *ast.CallExpr) string {
	args := func() string {
		var out []string
		for i, a := range x.Args {
			s := e.expr(a)
			if x.Ellipsis.IsValid() && i == len(x.Args)-1 {
				s = "..." + s
			}
			out = append(out, s)
		}
		return strings.Join(out, ", ")
	}
	// Type conversions: int(x), float64(x), string(x), named types...
	if tv, ok := e.ck.info.Types[x.Fun]; ok && tv.IsType() {
		if len(x.Args) != 1 {
			return "undefined"
		}
		arg := e.expr(x.Args[0])
		from := e.ck.info.TypeOf(x.Args[0])
		if types.Identical(tv.Type.Underlying(), from.Underlying()) {
			return arg
		}
		if b, ok := tv.Type.Underlying().(*types.Basic); ok {
			switch {
			case b.Info()&types.IsInteger != 0 && !isInt(from):
				return "Math.trunc(" + arg + ")"
			case b.Info()&(types.IsInteger|types.IsFloat) != 0:
				return arg
			case b.Info()&types.IsString != 0 && isInt(from):
				return "String.fromCodePoint(" + arg + ")"
			case b.Info()&types.IsString != 0:
				return arg
			}
		}
		e.errAt(x, "this conversion is not supported yet")
		return "undefined"
	}
	switch fun := ast.Unparen(x.Fun).(type) {
	case *ast.SelectorExpr:
		recv := e.ck.recvName(fun)
		obj := e.expr(fun.X)
		switch recv + "." + fun.Sel.Name {
		case "Signal.Get", "Computed.Get":
			return obj + ".get()"
		case "Signal.Set":
			return obj + ".set(" + args() + ")"
		case "Signal.Update":
			return obj + ".update(" + args() + ")"
		case "Event.Value":
			return obj + ".target.value"
		case "Event.Checked":
			return obj + ".target.checked"
		case "Event.Key":
			return obj + ".key"
		case "Event.PreventDefault":
			return obj + ".preventDefault()"
		}
		if sel := e.ck.info.Selections[fun]; sel != nil && sel.Kind() == types.FieldVal {
			return e.expr(fun) + "(" + args() + ")" // a func-typed field
		}
		e.errAt(x, "method %s is not supported yet", fun.Sel.Name)
		return "undefined"
	case *ast.Ident:
		switch o := e.ck.info.Uses[fun].(type) {
		case *types.Builtin:
			return e.builtin(o.Name(), x)
		case *types.Func:
			if e.ck.components[o.Name()] != nil {
				e.errAt(x, "components are used as <%s/> markup, not called", o.Name())
				return "undefined"
			}
			if e.ck.isPrelude(o) {
				switch o.Name() {
				case "signal":
					return "$signal(" + args() + ")"
				case "computed":
					return "$computed(() => " + args() + ")"
				case "itoa":
					return "String(" + args() + ")"
				}
				e.errAt(x, "%s is internal to den", o.Name())
				return "undefined"
			}
		}
		return jsIdent(fun.Name) + "(" + args() + ")"
	case *ast.FuncLit, *ast.IndexExpr, *ast.IndexListExpr:
		return e.expr(fun) + "(" + args() + ")"
	}
	e.errAt(x, "this call is not supported yet")
	return "undefined"
}

func (e *emitter) builtin(name string, x *ast.CallExpr) string {
	arg := func(i int) string { return e.expr(x.Args[i]) }
	switch name {
	case "len", "cap":
		if b, ok := e.ck.info.TypeOf(x.Args[0]).Underlying().(*types.Basic); ok && b.Info()&types.IsString != 0 {
			return "$len(" + arg(0) + ")"
		}
		return arg(0) + ".length"
	case "append":
		out := []string{"..." + arg(0)}
		for i := 1; i < len(x.Args); i++ {
			if x.Ellipsis.IsValid() && i == len(x.Args)-1 {
				out = append(out, "..."+arg(i))
			} else {
				out = append(out, arg(i))
			}
		}
		return "[" + strings.Join(out, ", ") + "]"
	case "make":
		if s, ok := e.ck.info.TypeOf(x).Underlying().(*types.Slice); ok {
			return "Array(" + arg(1) + ").fill(" + zeroValue(s.Elem()) + ")"
		}
	case "min", "max":
		if isNumeric(e.ck.info.TypeOf(x)) {
			var out []string
			for i := range x.Args {
				out = append(out, arg(i))
			}
			return "Math." + name + "(" + strings.Join(out, ", ") + ")"
		}
	}
	e.errAt(x, "%s is not supported here yet", name)
	return "undefined"
}

func isSlice(t types.Type) bool {
	_, ok := t.Underlying().(*types.Slice)
	return ok
}

func isNumeric(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsNumeric != 0
}

func constJS(v constant.Value) string {
	switch v.Kind() {
	case constant.String:
		return jsString(constant.StringVal(v))
	case constant.Bool:
		if constant.BoolVal(v) {
			return "true"
		}
		return "false"
	case constant.Int:
		return v.ExactString()
	case constant.Float:
		f, _ := constant.Float64Val(v)
		return fmt.Sprint(f)
	}
	return "undefined"
}

func constString(v constant.Value) string {
	if v.Kind() == constant.String {
		return constant.StringVal(v)
	}
	if v.Kind() == constant.Float {
		f, _ := constant.Float64Val(v)
		return fmt.Sprint(f)
	}
	return v.ExactString()
}

func zeroValue(t types.Type) string {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsString != 0:
			return `""`
		case u.Info()&types.IsBoolean != 0:
			return "false"
		case u.Info()&types.IsNumeric != 0:
			return "0"
		}
	case *types.Struct:
		var parts []string
		for i := 0; i < u.NumFields(); i++ {
			parts = append(parts, fieldName(u.Field(i).Name())+": "+zeroValue(u.Field(i).Type()))
		}
		return "({" + strings.Join(parts, ", ") + "})"
	case *types.Array:
		return fmt.Sprintf("Array(%d).fill(%s)", u.Len(), zeroValue(u.Elem()))
	case *types.Slice:
		return "[]"
	}
	return "null"
}

func jsString(s string) string {
	b, _ := json.Marshal(s) // escapes <, > and & too
	return string(b)
}

var jsReserved = map[string]bool{
	"arguments": true, "await": true, "class": true, "catch": true, "delete": true, "do": true,
	"enum": true, "eval": true, "export": true, "extends": true, "finally": true, "function": true,
	"implements": true, "in": true, "instanceof": true, "let": true, "new": true, "null": true,
	"private": true, "protected": true, "public": true, "static": true, "super": true, "this": true,
	"throw": true, "try": true, "typeof": true, "void": true, "while": true, "with": true,
	"yield": true, "undefined": true, "NaN": true, "Infinity": true,
	"document": true, "window": true, "queueMicrotask": true, "console": true,
	// globals the runtime and generated code use
	"Object": true, "String": true, "Number": true, "Boolean": true, "Array": true, "Math": true,
	"Set": true, "Map": true, "JSON": true, "Error": true, "Symbol": true, "Promise": true,
}

// jsIdent renames Go identifiers that would clash with JS. Go identifiers
// cannot contain '$', so runtime names like $signal can never collide.
func jsIdent(name string) string {
	if jsReserved[name] {
		return name + "$"
	}
	return name
}
