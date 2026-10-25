package compiler

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
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
	e := &emitter{ck: ck}
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
	var out strings.Builder
	out.WriteString("// Built by Den. Do not edit.\n(() => {\n\"use strict\";\n")
	for _, line := range strings.Split(runtimeJS, "\n") {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "//") {
			out.WriteString(line + "\n")
		}
	}
	out.WriteString(e.out.String())
	out.WriteString("$mount(App, document.getElementById(\"app\"));\n})();\n")
	return &Result{JS: out.String(), Components: names}, nil
}

type emitter struct {
	ck   *checked
	out  strings.Builder
	errs ErrorList
	n    int // element counter within a component
	cc   *checkedComponent
}

func (e *emitter) errAt(n ast.Node, format string, args ...any) {
	p := e.ck.fset.Position(n.Pos())
	e.errs = append(e.errs, &Error{File: p.Filename, Pos: Pos{p.Line, p.Column}, Msg: fmt.Sprintf(format, args...)})
}

func (e *emitter) component(cc *checkedComponent) {
	e.cc, e.n = cc, 0
	var params []string
	for _, p := range cc.c.Params {
		params = append(params, jsIdent(p.Name))
	}
	fmt.Fprintf(&e.out, "function %s(%s) {\n", cc.c.Name, strings.Join(params, ", "))
	for _, s := range cc.stmts {
		e.stmt(&e.out, s, "  ")
	}
	root := e.element(cc.c.Root, false)
	fmt.Fprintf(&e.out, "  return %s;\n}\n", root)
}

// element emits code that builds el and returns the JS expression for it.
func (e *emitter) element(el *Element, inSVG bool) string {
	if el.IsComponent() {
		pt := e.cc.parts[el]
		var args []string
		for _, a := range pt.args {
			args = append(args, e.expr(a))
		}
		return el.Tag + "(" + strings.Join(args, ", ") + ")"
	}
	svg := inSVG || el.Tag == "svg"
	v := fmt.Sprintf("$e%d", e.n)
	e.n++
	maker := "$h"
	if svg {
		maker = "$hs"
	}
	fmt.Fprintf(&e.out, "  const %s = %s(%s);\n", v, maker, jsString(el.Tag))
	for _, a := range el.Attrs {
		switch a.Kind {
		case AttrStatic:
			fmt.Fprintf(&e.out, "  %s.setAttribute(%s, %s);\n", v, jsString(a.Name), jsString(a.Value))
		case AttrBool:
			fmt.Fprintf(&e.out, "  %s.setAttribute(%s, \"\");\n", v, jsString(a.Name))
		case AttrExpr:
			pt := e.cc.parts[a]
			if pt.kind == partHandler {
				var body strings.Builder
				for _, s := range pt.stmts {
					e.stmt(&body, s, "    ")
				}
				fmt.Fprintf(&e.out, "  $on(%s, %s, (e) => {\n%s  });\n", v, jsString(a.Name[2:]), body.String())
				continue
			}
			x := e.expr(pt.expr)
			if e.reactive(pt.expr) {
				fmt.Fprintf(&e.out, "  $attr(%s, %s, () => %s);\n", v, jsString(a.Name), x)
			} else {
				fmt.Fprintf(&e.out, "  $set(%s, %s, %s);\n", v, jsString(a.Name), x)
			}
		}
	}
	for _, ch := range el.Children {
		switch ch := ch.(type) {
		case *Text:
			fmt.Fprintf(&e.out, "  %s.append(%s);\n", v, jsString(ch.Value))
		case *Interp:
			pt := e.cc.parts[ch]
			if tv := e.ck.info.Types[pt.expr]; tv.Value != nil {
				fmt.Fprintf(&e.out, "  %s.append(%s);\n", v, jsString(constString(tv.Value)))
			} else if e.reactive(pt.expr) {
				fmt.Fprintf(&e.out, "  $text(%s, () => %s);\n", v, e.expr(pt.expr))
			} else {
				fmt.Fprintf(&e.out, "  %s.append(String(%s));\n", v, e.expr(pt.expr))
			}
		case *Element:
			child := e.element(ch, svg && ch.Tag != "foreignObject")
			fmt.Fprintf(&e.out, "  %s.append(%s);\n", v, child)
		}
	}
	return v
}

// reactive reports whether evaluating x reads a signal, i.e. whether the DOM
// binding needs an effect. Decided at compile time from types.
func (e *emitter) reactive(x ast.Expr) bool {
	found := false
	ast.Inspect(x, func(n ast.Node) bool {
		if found {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Get" {
				if recv := e.recvName(sel); recv == "Signal" || recv == "Computed" {
					found = true
				}
			}
		}
		return true
	})
	return found
}

func (e *emitter) recvName(sel *ast.SelectorExpr) string {
	s := e.ck.info.Selections[sel]
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

func (e *emitter) stmt(b *strings.Builder, s ast.Stmt, ind string) {
	switch s := s.(type) {
	case *ast.ExprStmt:
		fmt.Fprintf(b, "%s%s;\n", ind, e.expr(s.X))
	case *ast.AssignStmt:
		if len(s.Lhs) != 1 || len(s.Rhs) != 1 {
			e.errAt(s, "multiple assignment is not supported yet")
			return
		}
		lhs, ok := s.Lhs[0].(*ast.Ident)
		if !ok {
			e.errAt(s, "only assignment to local variables is supported")
			return
		}
		switch s.Tok {
		case token.DEFINE:
			if lhs.Name == "_" {
				fmt.Fprintf(b, "%s%s;\n", ind, e.expr(s.Rhs[0]))
				return
			}
			fmt.Fprintf(b, "%slet %s = %s;\n", ind, jsIdent(lhs.Name), e.expr(s.Rhs[0]))
		case token.ASSIGN:
			if lhs.Name == "_" {
				fmt.Fprintf(b, "%s%s;\n", ind, e.expr(s.Rhs[0]))
				return
			}
			fmt.Fprintf(b, "%s%s = %s;\n", ind, jsIdent(lhs.Name), e.expr(s.Rhs[0]))
		case token.ADD_ASSIGN, token.SUB_ASSIGN, token.MUL_ASSIGN, token.REM_ASSIGN:
			fmt.Fprintf(b, "%s%s %s %s;\n", ind, jsIdent(lhs.Name), s.Tok.String(), e.expr(s.Rhs[0]))
		case token.QUO_ASSIGN:
			if e.isInt(lhs) {
				fmt.Fprintf(b, "%s%s = Math.trunc(%s / (%s));\n", ind, jsIdent(lhs.Name), jsIdent(lhs.Name), e.expr(s.Rhs[0]))
			} else {
				fmt.Fprintf(b, "%s%s /= %s;\n", ind, jsIdent(lhs.Name), e.expr(s.Rhs[0]))
			}
		default:
			e.errAt(s, "%s is not supported yet", s.Tok)
		}
	case *ast.IncDecStmt:
		fmt.Fprintf(b, "%s%s%s;\n", ind, e.expr(s.X), s.Tok)
	case *ast.DeclStmt:
		gd, ok := s.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			e.errAt(s, "only var declarations are supported here")
			return
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
				fmt.Fprintf(b, "%slet %s = %s;\n", ind, jsIdent(n.Name), init)
			}
		}
	case *ast.IfStmt:
		if s.Init != nil {
			e.errAt(s, "if with an init statement is not supported yet")
			return
		}
		e.ifStmt(b, s, ind)
		b.WriteString("\n")
	case *ast.ReturnStmt:
		switch len(s.Results) {
		case 0:
			fmt.Fprintf(b, "%sreturn;\n", ind)
		case 1:
			fmt.Fprintf(b, "%sreturn %s;\n", ind, e.expr(s.Results[0]))
		default:
			e.errAt(s, "multiple return values are not supported yet")
		}
	case *ast.BlockStmt:
		fmt.Fprintf(b, "%s{\n", ind)
		for _, st := range s.List {
			e.stmt(b, st, ind+"  ")
		}
		fmt.Fprintf(b, "%s}\n", ind)
	case *ast.EmptyStmt:
	default:
		e.errAt(s, "this statement is not supported yet")
	}
}

func (e *emitter) ifStmt(b *strings.Builder, s *ast.IfStmt, ind string) {
	fmt.Fprintf(b, "%sif (%s) {\n", ind, e.expr(s.Cond))
	for _, st := range s.Body.List {
		e.stmt(b, st, ind+"  ")
	}
	fmt.Fprintf(b, "%s}", ind)
	switch el := s.Else.(type) {
	case *ast.IfStmt:
		b.WriteString(" else ")
		var inner strings.Builder
		e.ifStmt(&inner, el, ind)
		b.WriteString(strings.TrimPrefix(inner.String(), ind))
	case *ast.BlockStmt:
		b.WriteString(" else {\n")
		for _, st := range el.List {
			e.stmt(b, st, ind+"  ")
		}
		fmt.Fprintf(b, "%s}", ind)
	}
}

func (e *emitter) isInt(x ast.Expr) bool {
	t := e.ck.info.TypeOf(x)
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
			return "null"
		}
		return jsIdent(x.Name)
	case *ast.ParenExpr:
		return "(" + e.expr(x.X) + ")"
	case *ast.BinaryExpr:
		op, ok := binOps[x.Op]
		if !ok {
			e.errAt(x, "operator %s is not supported yet", x.Op)
			return "undefined"
		}
		s := e.expr(x.X) + " " + op + " " + e.expr(x.Y)
		if x.Op == token.QUO && e.isInt(x) {
			return "Math.trunc(" + s + ")"
		}
		return "(" + s + ")"
	case *ast.UnaryExpr:
		switch x.Op {
		case token.NOT, token.SUB, token.ADD:
			return x.Op.String() + e.expr(x.X)
		case token.XOR:
			return "~" + e.expr(x.X)
		}
		e.errAt(x, "operator %s is not supported", x.Op)
		return "undefined"
	case *ast.CallExpr:
		return e.call(x)
	case *ast.FuncLit:
		var params []string
		for _, f := range x.Type.Params.List {
			for _, n := range f.Names {
				params = append(params, jsIdent(n.Name))
			}
		}
		var body strings.Builder
		for _, s := range x.Body.List {
			e.stmt(&body, s, "      ")
		}
		return "((" + strings.Join(params, ", ") + ") => {\n" + body.String() + "    })"
	case *ast.SelectorExpr:
		e.errAt(x, "field access is not supported yet")
		return "undefined"
	}
	e.errAt(x, "this expression is not supported yet")
	return "undefined"
}

func (e *emitter) call(x *ast.CallExpr) string {
	args := func() []string {
		var out []string
		for _, a := range x.Args {
			out = append(out, e.expr(a))
		}
		return out
	}
	// Type conversions: int(x), float64(x), string(x)...
	if tv, ok := e.ck.info.Types[x.Fun]; ok && tv.IsType() {
		if len(x.Args) != 1 {
			return "undefined"
		}
		arg := e.expr(x.Args[0])
		if b, ok := tv.Type.Underlying().(*types.Basic); ok {
			switch {
			case b.Info()&types.IsInteger != 0 && !e.isInt(x.Args[0]):
				return "Math.trunc(" + arg + ")"
			case b.Info()&(types.IsInteger|types.IsFloat) != 0:
				return arg
			case b.Info()&types.IsString != 0 && e.isInt(x.Args[0]):
				return "String.fromCodePoint(" + arg + ")"
			case b.Info()&types.IsString != 0:
				return arg
			}
		}
		e.errAt(x, "this conversion is not supported yet")
		return "undefined"
	}
	switch fun := x.Fun.(type) {
	case *ast.SelectorExpr:
		recv := e.recvName(fun)
		obj := e.expr(fun.X)
		switch recv + "." + fun.Sel.Name {
		case "Signal.Get", "Computed.Get":
			return obj + ".get()"
		case "Signal.Set":
			return obj + ".set(" + args()[0] + ")"
		case "Signal.Update":
			return obj + ".update(" + args()[0] + ")"
		case "Event.Value":
			return obj + ".target.value"
		case "Event.Checked":
			return obj + ".target.checked"
		case "Event.Key":
			return obj + ".key"
		case "Event.PreventDefault":
			return obj + ".preventDefault()"
		}
		e.errAt(x, "method %s is not supported yet", fun.Sel.Name)
		return "undefined"
	case *ast.Ident:
		switch o := e.ck.info.Uses[fun].(type) {
		case *types.Builtin:
			if o.Name() == "len" {
				return "(" + args()[0] + ").length"
			}
			e.errAt(x, "builtin %s is not supported yet", o.Name())
			return "undefined"
		case *types.Func:
			if o.Parent() == o.Pkg().Scope() {
				switch o.Name() {
				case "signal":
					return "$signal(" + args()[0] + ")"
				case "computed":
					return "$computed(() => " + args()[0] + ")"
				case "itoa":
					return "String(" + args()[0] + ")"
				}
				e.errAt(x, "components are used as <%s/> markup, not called", o.Name())
				return "undefined"
			}
		}
		return jsIdent(fun.Name) + "(" + strings.Join(args(), ", ") + ")"
	case *ast.FuncLit, *ast.ParenExpr:
		return e.expr(fun) + "(" + strings.Join(args(), ", ") + ")"
	}
	e.errAt(x, "this call is not supported yet")
	return "undefined"
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
	if b, ok := t.Underlying().(*types.Basic); ok {
		switch {
		case b.Info()&types.IsString != 0:
			return `""`
		case b.Info()&types.IsBoolean != 0:
			return "false"
		case b.Info()&types.IsNumeric != 0:
			return "0"
		}
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
	"yield": true, "undefined": true, "NaN": true, "Infinity": true, "Object": true, "String": true,
	"Math": true, "document": true, "window": true,
}

// jsIdent renames Go identifiers that would clash with JS. Go identifiers
// cannot contain '$', so runtime names like $signal can never collide.
func jsIdent(name string) string {
	if jsReserved[name] {
		return name + "$"
	}
	return name
}
