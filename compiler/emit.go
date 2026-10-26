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
	"slices"
	"strconv"
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
	e := &emitter{ck: ck, w: &body, tpls: map[string]string{}}
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
	code := e.tplOut.String() + body.String()
	var out strings.Builder
	out.WriteString("// Built by Den. Do not edit.\n(() => {\n\"use strict\";\n")
	out.WriteString(runtimeFor(code))
	out.WriteString(code)
	out.WriteString("})();\n")
	return &Result{JS: out.String(), Components: names}, nil
}

var (
	runtimeDecl = regexp.MustCompile(`^(?:function|const|let|class)\s+(\$\w+)`)
	runtimeRef  = regexp.MustCompile(`\$\w+`)
)

// runtimeFor returns the top-level declarations of runtime.js and stdlib.js
// that code uses, directly or through other declarations, in source order.
func runtimeFor(code string) string {
	type decl struct {
		name, src string
		used      bool
	}
	var decls []*decl
	byName := map[string]*decl{}
	for _, line := range strings.Split(runtimeJS+"\n"+stdlibJS, "\n") {
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
	return minifyJS(b.String())
}

type emitter struct {
	ck      *checked
	tpls    map[string]string // template code -> name
	tplOut  strings.Builder
	w       *strings.Builder
	ind     string
	errs    ErrorList
	n       int // counter for generated names
	cc      *checkedComponent
	results []string // named results of the function being emitted

	loopVars  map[types.Object]bool // variables of the markup loops being emitted
	row       *rowCtx               // the innermost keyed list row, if any
	delegated map[*Attr]bool        // handlers found through the row's table
}

// rowCtx is a keyed list being emitted. Handlers and bindings in its rows
// that use no loop variable but the row's own item are hoisted out of the
// row: written once, before the list, as functions of the item, so each row
// stores the item instead of a closure.
//
// When a row is a single element, hoisted bubbling handlers go further: they
// are kept in a table on the list, and the element gets a static attribute,
// den-click="0", naming its handler. The dispatcher finds the row through
// the row's element, so rows hold no handler state at all.
type rowCtx struct {
	obj      types.Object // the row's item variable
	name     string       // its JS name
	hoist    strings.Builder
	rootEl   bool     // each row is a single element
	handlers []string // the table of delegated handlers
	types    []string // the events they handle
}

// delegate reports whether handler a can go in the row's handler table,
// and if so puts it there and returns its index.
func (e *emitter) delegate(a *Attr) (int, bool) {
	pt := e.cc.attrs[a]
	if pt.kind != partHandler || e.row == nil || !e.row.rootEl || !bubbles[a.Name[2:]] || e.row.name == "e" {
		return 0, false
	}
	var stmts []ast.Node
	for _, st := range pt.stmts {
		stmts = append(stmts, st)
	}
	if !e.hoistable(stmts...) {
		return 0, false
	}
	body := e.capture(e.ind+"  ", func() { e.stmts(pt.stmts) })
	e.row.handlers = append(e.row.handlers, e.hoisted("(e, "+e.row.name+") => {\n"+body+e.ind+"}"))
	if !slices.Contains(e.row.types, a.Name[2:]) {
		e.row.types = append(e.row.types, a.Name[2:])
	}
	if e.delegated == nil {
		e.delegated = map[*Attr]bool{}
	}
	e.delegated[a] = true
	return len(e.row.handlers) - 1, true
}

// hoistable reports whether the code in nodes can be hoisted out of the
// current row.
func (e *emitter) hoistable(nodes ...ast.Node) bool {
	if e.row == nil {
		return false
	}
	ok := true
	for _, n := range nodes {
		ast.Inspect(n, func(x ast.Node) bool {
			if id, isID := x.(*ast.Ident); isID {
				if obj := e.ck.info.Uses[id]; obj != nil && e.loopVars[obj] && obj != e.row.obj {
					ok = false
				}
			}
			return ok
		})
	}
	return ok
}

// hoisted writes def (a function taking the row's item) before the list
// and returns its name.
func (e *emitter) hoisted(def string) string {
	name := e.name("$b")
	fmt.Fprintf(&e.row.hoist, "%sconst %s = %s;\n", e.ind, name, def)
	return name
}

func (e *emitter) errAt(n ast.Node, format string, args ...any) {
	p := e.ck.fset.Position(n.Pos())
	e.errs = append(e.errs, &Error{File: p.Filename, Pos: Pos{p.Line, p.Column}, Msg: errorf(format, args...)})
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
	e.line("return %s;", e.instantiate([]Node{cc.c.Root}, false))
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

// A tnode is one node of a static template. Each component root and each
// block body is built once with DOM calls and then copied with cloneNode,
// which is much faster than building every instance node by node. Dynamic
// parts get a placeholder: an empty text node for {expr}, a comment for a
// component, and comment anchors for {if} and {for}.
type tnode struct {
	kind     byte // 'e' element, 't' text, 'c' comment
	tag      string
	svg      bool // element is SVG, or (for anchors) content goes inside SVG
	text     string
	attrs    []*Attr // static attributes
	children []*tnode
	src      Node   // the markup node that needs code after cloning
	end      *tnode // reactive {if} or {for}: the anchor after its content
	need     bool   // code needs a reference to this node
	v        string // that reference
}

func (t *tnode) needed() bool {
	if t.need {
		return true
	}
	for _, c := range t.children {
		if c.needed() {
			return true
		}
	}
	return false
}

func (e *emitter) tnodes(nodes []Node, svg bool) []*tnode {
	var out []*tnode
	for _, n := range nodes {
		switch n := n.(type) {
		case *Text:
			out = append(out, &tnode{kind: 't', text: n.Value})
		case *Interp:
			if tv := e.ck.info.Types[e.cc.parts[n].expr]; tv.Value != nil {
				out = append(out, &tnode{kind: 't', text: constString(tv.Value)})
			} else {
				out = append(out, &tnode{kind: 't', src: n, need: true})
			}
		case *Element:
			if n.IsComponent() {
				out = append(out, &tnode{kind: 'c', src: n, need: true})
				continue
			}
			t := &tnode{kind: 'e', tag: n.Tag, svg: svg || n.Tag == "svg", src: n}
			for _, a := range n.Attrs {
				if a.Kind == AttrExpr {
					if i, ok := e.delegate(a); ok {
						t.attrs = append(t.attrs, &Attr{Name: "den-" + a.Name[2:], Kind: AttrStatic, Value: strconv.Itoa(i)})
						continue
					}
				}
				if a.Kind == AttrExpr || a.Name == "autofocus" {
					t.need = true
				}
				if a.Kind != AttrExpr {
					t.attrs = append(t.attrs, a)
				}
			}
			t.children = e.tnodes(n.Children, t.svg && n.Tag != "foreignObject")
			out = append(out, t)
		case *If, *For:
			start := &tnode{kind: 'c', svg: svg, src: n, need: true}
			out = append(out, start)
			if e.reactiveNode(n) {
				start.end = &tnode{kind: 'c', need: true}
				out = append(out, start.end)
			}
		}
		// Merge static text, including folded constants, into one node.
		if k := len(out); k > 1 && out[k-1].kind == 't' && out[k-2].kind == 't' && !out[k-1].need && !out[k-2].need {
			out[k-2].text += out[k-1].text
			out = out[:k-1]
		}
	}
	return out
}

func (e *emitter) reactiveNode(n Node) bool {
	pt := e.cc.parts[n]
	if pt.kind == partFor {
		return e.ck.reactive(pt.rng.X)
	}
	return e.ck.reactive(pt.expr)
}

// template returns the name of a function that clones the static DOM for
// ts. Identical templates are shared.
func (e *emitter) template(ts []*tnode) string {
	var b strings.Builder
	n := 0
	var build func(t *tnode) string
	build = func(t *tnode) string {
		switch t.kind {
		case 't':
			return jsString(t.text)
		case 'c':
			return "$cm()"
		}
		v := fmt.Sprintf("a%d", n)
		n++
		maker := "$h"
		if t.svg {
			maker = "$hs"
		}
		fmt.Fprintf(&b, "  const %s = %s(%s);\n", v, maker, jsString(t.tag))
		for _, a := range t.attrs {
			fmt.Fprintf(&b, "  %s.setAttribute(%s, %s);\n", v, jsString(a.Name), jsString(a.Value))
		}
		var kids []string
		for _, c := range t.children {
			kids = append(kids, build(c))
		}
		if len(kids) > 0 {
			fmt.Fprintf(&b, "  %s.append(%s);\n", v, strings.Join(kids, ", "))
		}
		return v
	}
	root := ""
	if len(ts) == 1 && ts[0].kind == 'e' {
		root = build(ts[0])
	} else {
		var kids []string
		for _, t := range ts {
			kids = append(kids, build(t))
		}
		root = "f"
		fmt.Fprintf(&b, "  const f = $frag();\n  f.append(%s);\n", strings.Join(kids, ", "))
	}
	code := b.String() + "  return " + root + ";\n"
	if name, ok := e.tpls[code]; ok {
		return name
	}
	name := fmt.Sprintf("$t%d", len(e.tpls))
	e.tpls[code] = name
	fmt.Fprintf(&e.tplOut, "const %s = $tpl(() => {\n%s});\n", name, code)
	return name
}

// instantiate emits code that clones the template for nodes, wires up its
// dynamic parts and returns the JS expression for the result: an element,
// or a fragment when there is more than one top-level node.
func (e *emitter) instantiate(nodes []Node, svg bool) string {
	if len(nodes) == 0 {
		return "$cm()" // a placeholder, so a list row is never empty
	}
	if len(nodes) == 1 {
		if el, ok := nodes[0].(*Element); ok && el.IsComponent() {
			return e.componentCall(el)
		}
	}
	ts := e.tnodes(nodes, svg)
	root := e.name("$e")
	e.line("const %s = %s();", root, e.template(ts))
	// Take every reference before any binding changes the tree.
	if len(ts) == 1 && ts[0].kind == 'e' {
		ts[0].v = root
		e.walk(root, ts[0].children)
	} else {
		e.walk(root, ts)
	}
	e.bind(ts)
	return root
}

func (e *emitter) walk(parent string, ts []*tnode) {
	prev, prevIdx := "", -1
	for i, t := range ts {
		if !t.needed() {
			continue
		}
		if prev == "" {
			prev = parent + ".firstChild" + strings.Repeat(".nextSibling", i)
		} else {
			prev += strings.Repeat(".nextSibling", i-prevIdx)
		}
		t.v = e.name("$e")
		e.line("const %s = %s;", t.v, prev)
		prev, prevIdx = t.v, i
		e.walk(t.v, t.children)
	}
}

// bind emits the code for the dynamic parts of a cloned template.
func (e *emitter) bind(ts []*tnode) {
	for _, t := range ts {
		switch n := t.src.(type) {
		case *Interp:
			pt := e.cc.parts[n]
			if e.ck.reactive(pt.expr) {
				if e.hoistable(pt.expr) {
					e.line("$text(%s, %s, %s);", t.v, e.hoisted("("+e.row.name+") => "+e.expr(pt.expr)), e.row.name)
				} else {
					e.line("$text(%s, () => %s);", t.v, e.expr(pt.expr))
				}
			} else {
				e.line("%s.data = String(%s);", t.v, e.expr(pt.expr))
			}
		case *Element:
			if n.IsComponent() {
				e.line("%s.replaceWith(%s);", t.v, e.componentCall(n))
				continue
			}
			for _, a := range n.Attrs {
				if a.Name == "autofocus" && a.Kind != AttrExpr {
					// Browsers only autofocus on page load; den also focuses
					// elements it adds later, once they are in the document.
					e.line("queueMicrotask(() => %s.focus());", t.v)
				}
				if a.Kind != AttrExpr || e.delegated[a] {
					continue
				}
				pt := e.cc.attrs[a]
				if pt.kind == partHandler {
					body := e.capture(e.ind+"  ", func() { e.stmts(pt.stmts) })
					switch {
					case bubbles[a.Name[2:]]:
						e.line("$on(%s, %s, (e) => {\n%s%s});", t.v, jsString(a.Name[2:]), body, e.ind)
					default:
						e.line("%s.addEventListener(%s, (e) => {\n%s%s});", t.v, jsString(a.Name[2:]), body, e.ind)
					}
					continue
				}
				x := e.expr(pt.expr)
				if urlAttrs[strings.ToLower(a.Name)] {
					x = "$url(" + x + ")"
				}
				if e.ck.reactive(pt.expr) {
					if e.hoistable(pt.expr) {
						e.line("$attr(%s, %s, %s, %s);", t.v, jsString(a.Name), e.hoisted("("+e.row.name+") => "+x), e.row.name)
					} else {
						e.line("$attr(%s, %s, () => %s);", t.v, jsString(a.Name), x)
					}
				} else {
					e.line("$set(%s, %s, %s);", t.v, jsString(a.Name), x)
				}
			}
			e.bind(t.children)
		case *If:
			e.ifNode(t, n)
		case *For:
			e.forNode(t, n)
		}
	}
}

// bubbles lists the events that bubble and so can share one delegated
// listener on the document. Other events get a listener on the element.
var bubbles = map[string]bool{
	"click": true, "dblclick": true, "auxclick": true, "contextmenu": true,
	"input": true, "change": true, "beforeinput": true, "submit": true, "reset": true,
	"keydown": true, "keyup": true,
	"pointerdown": true, "pointerup": true, "pointermove": true, "pointerover": true, "pointerout": true,
	"mousedown": true, "mouseup": true, "mousemove": true, "mouseover": true, "mouseout": true,
	"touchstart": true, "touchend": true, "touchmove": true,
	"focusin": true, "focusout": true, "wheel": true, "dragstart": true, "drag": true, "dragend": true,
	"dragenter": true, "dragover": true, "dragleave": true, "drop": true, "copy": true, "cut": true, "paste": true,
}

func (e *emitter) componentCall(el *Element) string {
	var args []string
	for _, a := range e.cc.parts[el].args {
		args = append(args, e.expr(a))
	}
	return jsIdent(el.Tag) + "(" + strings.Join(args, ", ") + ")"
}

// block emits a function that builds nodes and returns them.
func (e *emitter) block(params string, nodes []Node, svg bool) string {
	body := e.capture(e.ind+"  ", func() { e.line("return %s;", e.instantiate(nodes, svg)) })
	return "(" + params + ") => {\n" + body + e.ind + "}"
}

// ifNode emits {if}. A condition that reads no signal can never change, so
// it compiles to a plain JS if that fills in its placeholder once;
// otherwise $if swaps the branches between two anchors.
func (e *emitter) ifNode(t *tnode, n *If) {
	pt := e.cc.parts[n]
	cond := e.expr(pt.expr)
	if t.end == nil {
		branch := func(nodes []Node) {
			if len(nodes) > 0 {
				e.indented(func() { e.line("%s.replaceWith(%s);", t.v, e.instantiate(nodes, t.svg)) })
			}
		}
		e.line("if (%s) {", cond)
		branch(n.Then)
		if len(n.Else) > 0 {
			e.line("} else {")
			branch(n.Else)
		}
		e.line("}")
		return
	}
	els := "null"
	if len(n.Else) > 0 {
		els = e.block("", n.Else, t.svg)
	}
	e.line("$if(%s, %s, () => %s, %s, %s);", t.v, t.end.v, cond, e.block("", n.Then, t.svg), els)
}

// forNode emits {for}. A list that reads no signal compiles to a plain JS
// loop; otherwise $for keeps the rows between its anchors in sync by key.
func (e *emitter) forNode(t *tnode, n *For) {
	pt := e.cc.parts[n]
	rs := pt.rng
	k, v := loopVar(rs.Key), loopVar(rs.Value)
	b, isInt := e.ck.info.TypeOf(rs.X).Underlying().(*types.Basic)
	isInt = isInt && b.Info()&types.IsInteger != 0
	list := e.expr(rs.X)
	if isSlice(e.ck.info.TypeOf(rs.X)) {
		list = "$arr(" + list + ")"
	}
	var keyObj, valObj types.Object
	if id, ok := rs.Key.(*ast.Ident); ok {
		keyObj = e.ck.info.Defs[id]
	}
	if id, ok := rs.Value.(*ast.Ident); ok {
		valObj = e.ck.info.Defs[id]
	}
	if e.loopVars == nil {
		e.loopVars = map[types.Object]bool{}
	}
	for _, o := range []types.Object{keyObj, valObj} {
		if o != nil {
			e.loopVars[o] = true
			defer delete(e.loopVars, o)
		}
	}
	if t.end == nil {
		l := e.name("$l")
		e.line("const %s = %s;", l, list)
		switch {
		case isMap(e.ck.info.TypeOf(rs.X)):
			e.line("for (const [%s, %s] of %s) {", or(k, e.name("$k")), or(v, e.name("$v")), l)
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
		e.indented(func() { e.line("%s.before(%s);", t.v, e.instantiate(n.Body, t.svg)) })
		e.line("}")
		return
	}
	var params string
	useIndex := false
	if isInt {
		list = "Array.from({ length: " + list + " }, (_, i) => i)"
		params = or(k, "$i")
	} else if isMap(e.ck.info.TypeOf(rs.X)) {
		list = "$entries(" + list + ")" // values, with the map keys as indexes
		params = or(v, "$v") + ", " + or(k, "$k")
		useIndex = true
	} else {
		params = or(v, "$v")
		if k != "" {
			params += ", " + k
			useIndex = true
		}
	}
	// The row's item: the int itself when ranging over an int, otherwise the
	// element or map value.
	outer := e.row
	e.row = nil
	if isInt && keyObj != nil {
		e.row = &rowCtx{obj: keyObj, name: k}
	} else if !isInt && valObj != nil {
		e.row = &rowCtx{obj: valObj, name: v}
	}
	if e.row != nil && len(n.Body) == 1 {
		el, ok := n.Body[0].(*Element)
		e.row.rootEl = ok && !el.IsComponent()
	}
	block := e.block(params, n.Body, t.svg)
	table := ""
	if e.row != nil {
		e.w.WriteString(e.row.hoist.String())
		if len(e.row.handlers) > 0 {
			table = ", [" + strings.Join(e.row.handlers, ", ") + "]"
		}
		for _, typ := range e.row.types {
			e.line("$listen(%s);", jsString(typ))
		}
	}
	e.row = outer
	e.line("$for(%s, %s, () => %s, (%s) => %s, %s, %t%s);", t.v, t.end.v, list, params, e.expr(pt.expr), block, useIndex, table)
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
			if len(vs.Names) > 1 && len(vs.Values) == 1 {
				t := e.name("$r")
				e.line("const %s = %s;", t, e.expr(vs.Values[0]))
				for i, n := range vs.Names {
					e.line("let %s = %s[%d];", jsIdent(n.Name), t, i)
				}
				continue
			}
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
		case len(s.Results) > 1:
			// Multiple results travel as an array.
			var rs []string
			for _, r := range s.Results {
				rs = append(rs, e.expr(r))
			}
			e.line("return [%s];", strings.Join(rs, ", "))
		case len(e.results) == 1:
			e.line("return %s;", e.results[0])
		case len(e.results) > 1:
			e.line("return [%s];", strings.Join(e.results, ", "))
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
		// The init statement's variables are scoped to the if and its else.
		e.line("%s{", prefix)
		e.indented(func() {
			e.stmt(s.Init)
			e.ifStmt(&ast.IfStmt{If: s.If, Cond: s.Cond, Body: s.Body, Else: s.Else}, "")
		})
		e.line("}")
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
		elem := l + "[" + i + "]"
		if _, ok := t.(*types.Slice); ok {
			e.line("for (let %s = 0; %s < $slen(%s); %s++) {", i, i, l, i)
			elem = l + ".a[" + l + ".o + " + i + "]"
		} else {
			e.line("for (let %s = 0; %s < %s.length; %s++) {", i, i, l, i)
		}
		var vars []string
		if k != "" {
			vars = append(vars, k+" = "+i)
		}
		if v != "" {
			vars = append(vars, v+" = "+elem)
		}
		if len(vars) > 0 {
			e.line("  let %s;", strings.Join(vars, ", "))
		}
	case *types.Map:
		if v != "" {
			e.line("for (let [%s, %s] of %s) {", or(k, i), v, l)
		} else {
			e.line("for (let %s of %s.keys()) {", or(k, i), l)
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
	if ix, ok := commaOk(e, s); ok {
		// v, ok := m[k]
		m, k := e.name("$m"), e.name("$k")
		e.line("const %s = %s, %s = %s;", m, e.expr(ix.X), k, e.expr(ix.Index))
		has := m + ".has(" + k + ")"
		val := has + " ? " + m + ".get(" + k + ") : " + zeroValue(e.ck.info.TypeOf(ix.X).Underlying().(*types.Map).Elem())
		if s.Tok == token.DEFINE {
			var decls []string
			if n := loopVar(s.Lhs[0]); n != "" {
				decls = append(decls, n+" = "+val)
			}
			if n := loopVar(s.Lhs[1]); n != "" {
				decls = append(decls, n+" = "+has)
			}
			if len(decls) > 0 {
				e.line("let %s;", strings.Join(decls, ", "))
			}
			return
		}
		e.store(s.Lhs[0], val)
		e.store(s.Lhs[1], has)
		return
	}
	if len(s.Lhs) > 1 {
		// a, b = b, a and a, b := f(): evaluate everything, then assign.
		var vals []string
		if len(s.Rhs) == 1 {
			t := e.name("$r")
			e.line("const %s = %s;", t, e.expr(s.Rhs[0]))
			for i := range s.Lhs {
				vals = append(vals, fmt.Sprintf("%s[%d]", t, i))
			}
		} else {
			var decls []string
			for _, r := range s.Rhs {
				t := e.name("$r")
				decls = append(decls, t+" = "+e.expr(r))
				vals = append(vals, t)
			}
			e.line("const %s;", strings.Join(decls, ", "))
		}
		for i, l := range s.Lhs {
			if id, ok := l.(*ast.Ident); ok && s.Tok == token.DEFINE && e.ck.info.Defs[id] != nil {
				e.line("let %s = %s;", jsIdent(id.Name), vals[i])
			} else if !isBlank(l) {
				e.store(l, vals[i])
			}
		}
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
		if _, ok := e.ck.info.TypeOf(x.X).Underlying().(*types.Map); ok {
			e.line("%s.set(%s, %s);", e.expr(x.X), e.expr(x.Index), val)
			return
		}
		if isSlice(e.ck.info.TypeOf(x.X)) {
			e.line("$put(%s, %s, %s);", e.expr(x.X), e.expr(x.Index), val)
			return
		}
		e.store(x.X, e.expr(x.X)+".with("+e.expr(x.Index)+", "+val+")")
	default:
		e.errAt(lhs, "cannot assign to this expression")
	}
}

func isNilIdent(x ast.Expr) bool {
	id, ok := ast.Unparen(x).(*ast.Ident)
	return ok && id.Name == "nil"
}

func isBlank(x ast.Expr) bool {
	id, ok := x.(*ast.Ident)
	return ok && id.Name == "_"
}

// commaOk reports whether s is `v, ok := m[k]` (or =) on a map.
func commaOk(e *emitter, s *ast.AssignStmt) (*ast.IndexExpr, bool) {
	if len(s.Lhs) != 2 || len(s.Rhs) != 1 {
		return nil, false
	}
	ix, ok := ast.Unparen(s.Rhs[0]).(*ast.IndexExpr)
	if !ok {
		return nil, false
	}
	_, isMap := e.ck.info.TypeOf(ix.X).Underlying().(*types.Map)
	return ix, isMap
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
		case *types.Map:
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
			if t := e.ck.nilTypes[x]; t != nil && isMap(t) {
				return "new Map()" // a nil map behaves like an empty one
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
		case *types.Slice:
			return "$at(" + e.expr(x.X) + ", " + e.expr(x.Index) + ")"
		case *types.Array:
			return e.expr(x.X) + "[" + e.expr(x.Index) + "]"
		case *types.Map:
			return "(" + e.expr(x.X) + ".get(" + e.expr(x.Index) + ") ?? " + zeroValue(e.ck.info.TypeOf(x)) + ")"
		case *types.Signature:
			return e.expr(x.X) // explicit instantiation of a generic function
		}
		e.errAt(x, "indexing %s is not supported yet", e.ck.info.TypeOf(x.X))
		return "undefined"
	case *ast.IndexListExpr:
		return e.expr(x.X)
	case *ast.SliceExpr:
		if !isSlice(e.ck.info.TypeOf(x.X)) {
			e.errAt(x, "slicing %s is not supported yet", e.ck.info.TypeOf(x.X))
			return "undefined"
		}
		args := []string{e.expr(x.X), "0", "undefined"}
		if x.Low != nil {
			args[1] = e.expr(x.Low)
		}
		if x.High != nil {
			args[2] = e.expr(x.High)
		}
		if x.Max != nil {
			args = append(args, e.expr(x.Max))
		} else if x.High == nil {
			args = args[:2]
		}
		return "$slice(" + strings.Join(args, ", ") + ")"
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
			return "[" + strings.Join(parts, ", ") + "]"
		}
		return "$sl([" + strings.Join(parts, ", ") + "])"
	case *types.Map:
		for _, el := range x.Elts {
			kv := el.(*ast.KeyValueExpr)
			parts = append(parts, "["+e.expr(kv.Key)+", "+e.expr(kv.Value)+"]")
		}
		return "new Map([" + strings.Join(parts, ", ") + "])"
	}
	e.errAt(x, "%s literals are not supported yet", t)
	return "undefined"
}

func (e *emitter) call(x *ast.CallExpr) string {
	args := func() string {
		var out []string
		for _, a := range x.Args {
			out = append(out, e.expr(a))
		}
		if len(x.Args) == 1 {
			if tup, ok := e.ck.info.TypeOf(x.Args[0]).(*types.Tuple); ok && tup.Len() > 1 {
				return "..." + out[0] // f(g()) where g returns several values
			}
		}
		// A variadic function takes its extra arguments as one slice, as in Go.
		if sig, ok := e.ck.info.TypeOf(x.Fun).(*types.Signature); ok && sig.Variadic() && !x.Ellipsis.IsValid() {
			n := sig.Params().Len() - 1
			if n <= len(out) {
				out = append(out[:n:n], "$sl(["+strings.Join(out[n:], ", ")+"])")
			}
		}
		return strings.Join(out, ", ")
	}
	// Type conversions: int(x), float64(x), string(x), named types...
	if tv, ok := e.ck.info.Types[x.Fun]; ok && tv.IsType() {
		if len(x.Args) != 1 {
			return "undefined"
		}
		if isNilIdent(x.Args[0]) {
			return zeroValue(tv.Type) // []T(nil), map[K]V(nil)
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
		if id, ok := fun.X.(*ast.Ident); ok {
			if pkg, ok := e.ck.info.Uses[id].(*types.PkgName); ok {
				name := pkg.Imported().Path() + "." + fun.Sel.Name
				if f := stdCalls[name]; f != nil {
					var as []string
					for _, a := range x.Args {
						as = append(as, e.expr(a))
					}
					return f(as)
				}
				e.errAt(x, "%s is not supported yet", name)
				return "undefined"
			}
		}
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
		case "error.Error":
			return obj + ".message"
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
				case "stored":
					t := e.ck.info.TypeOf(x).(*types.Pointer).Elem().(*types.Named).TypeArgs().At(0)
					dec, ok := e.decoder(t, "j")
					if !ok {
						e.errAt(x, "cannot store %s in localStorage", t)
						return "undefined"
					}
					return "$stored(" + args() + ", (j) => " + dec + ")"
				case "hash":
					return "$hash()"
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

// stdCalls implements the functions of the importable packages.
var stdCalls = map[string]func(a []string) string{
	"strings.Contains":   func(a []string) string { return "(" + a[0] + ").includes(" + a[1] + ")" },
	"strings.HasPrefix":  func(a []string) string { return "(" + a[0] + ").startsWith(" + a[1] + ")" },
	"strings.HasSuffix":  func(a []string) string { return "(" + a[0] + ").endsWith(" + a[1] + ")" },
	"strings.ToLower":    func(a []string) string { return "(" + a[0] + ").toLowerCase()" },
	"strings.ToUpper":    func(a []string) string { return "(" + a[0] + ").toUpperCase()" },
	"strings.Repeat":     func(a []string) string { return "(" + a[0] + ").repeat(" + a[1] + ")" },
	"strings.Join":       func(a []string) string { return "$arr(" + a[0] + ").join(" + a[1] + ")" },
	"strings.TrimSpace":  func(a []string) string { return "$trimSpace(" + a[0] + ")" },
	"strings.TrimPrefix": func(a []string) string { return "$trimPrefix(" + a[0] + ", " + a[1] + ")" },
	"strings.TrimSuffix": func(a []string) string { return "$trimSuffix(" + a[0] + ", " + a[1] + ")" },
	"strings.Split":      func(a []string) string { return "$split(" + a[0] + ", " + a[1] + ")" },
	"strings.ReplaceAll": func(a []string) string { return "$replaceAll(" + a[0] + ", " + a[1] + ", " + a[2] + ")" },
	"strings.Fields":     func(a []string) string { return "$fields(" + a[0] + ")" },
	"strconv.Itoa":       func(a []string) string { return "String(" + a[0] + ")" },
	"strconv.FormatBool": func(a []string) string { return "String(" + a[0] + ")" },
	"strconv.Atoi":       func(a []string) string { return "$atoi(" + a[0] + ")" },
}

// decoder returns JS that turns the parsed JSON in x back into a den value
// of type t, throwing if it does not fit. Fields missing from the JSON get
// their zero value, as with encoding/json.
func (e *emitter) decoder(t types.Type, x string) (string, bool) {
	v := e.name("v")
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsString != 0:
			return "$want(" + x + `, "string")`, true
		case u.Info()&types.IsBoolean != 0:
			return "$want(" + x + `, "boolean")`, true
		case u.Info()&types.IsNumeric != 0:
			return "$want(" + x + `, "number")`, true
		}
	case *types.Slice:
		dec, ok := e.decoder(u.Elem(), v)
		return "(" + x + " === null ? null : $sl($list(" + x + ").map((" + v + ") => " + dec + ")))", ok
	case *types.Array:
		dec, ok := e.decoder(u.Elem(), v)
		return "$list(" + x + ").map((" + v + ") => " + dec + ")", ok
	case *types.Struct:
		o := e.name("o")
		var fields []string
		for i := 0; i < u.NumFields(); i++ {
			f := u.Field(i)
			fx := o + "." + fieldName(f.Name())
			dec, ok := e.decoder(f.Type(), fx)
			if !ok {
				return "", false
			}
			fields = append(fields, fieldName(f.Name())+": "+fx+" === undefined ? "+zeroValue(f.Type())+" : "+dec)
		}
		return "((" + o + ") => ({" + strings.Join(fields, ", ") + "}))($obj(" + x + "))", true
	case *types.Map:
		k := e.name("k")
		key := k
		switch b := u.Key().Underlying().(*types.Basic); {
		case b.Info()&types.IsNumeric != 0:
			key = "Number(" + k + ")"
		case b.Info()&types.IsBoolean != 0:
			key = k + ` === "true"`
		}
		dec, ok := e.decoder(u.Elem(), v)
		return "new Map(Object.entries($obj(" + x + ")).map(([" + k + ", " + v + "]) => [" + key + ", " + dec + "]))", ok
	}
	return "", false
}

func (e *emitter) builtin(name string, x *ast.CallExpr) string {
	arg := func(i int) string { return e.expr(x.Args[i]) }
	switch name {
	case "len", "cap":
		switch t := e.ck.info.TypeOf(x.Args[0]).Underlying().(type) {
		case *types.Basic:
			if t.Info()&types.IsString != 0 {
				return "$len(" + arg(0) + ")"
			}
		case *types.Map:
			return arg(0) + ".size"
		case *types.Slice:
			return "$s" + name + "(" + arg(0) + ")"
		}
		return arg(0) + ".length"
	case "delete":
		return arg(0) + ".delete(" + arg(1) + ")"
	case "clear":
		switch t := e.ck.info.TypeOf(x.Args[0]).Underlying().(type) {
		case *types.Map:
			return arg(0) + ".clear()"
		case *types.Slice:
			s := e.name("$s")
			return "((" + s + ") => " + s + "?.a.fill(" + zeroValue(t.Elem()) + ", " + s + ".o, " + s + ".o + " + s + ".n))(" + arg(0) + ")"
		}
	case "append":
		if x.Ellipsis.IsValid() {
			if isString(e.ck.info.TypeOf(x.Args[1])) {
				break
			}
			return "$append(" + arg(0) + ", $arr(" + arg(1) + "))"
		}
		var out []string
		for i := 1; i < len(x.Args); i++ {
			out = append(out, arg(i))
		}
		return "$append(" + arg(0) + ", [" + strings.Join(out, ", ") + "])"
	case "copy":
		if isSlice(e.ck.info.TypeOf(x.Args[1])) {
			return "$copy(" + arg(0) + ", " + arg(1) + ")"
		}
	case "make":
		switch t := e.ck.info.TypeOf(x).Underlying().(type) {
		case *types.Slice:
			c := arg(1)
			if len(x.Args) > 2 {
				c = arg(2)
			}
			return "$make(" + arg(1) + ", " + c + ", " + zeroValue(t.Elem()) + ")"
		case *types.Map:
			return "new Map()"
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

func isString(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}

func isMap(t types.Type) bool {
	_, ok := t.Underlying().(*types.Map)
	return ok
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
	case *types.Map:
		return "new Map()"
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
