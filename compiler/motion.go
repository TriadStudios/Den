package compiler

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Motion: enter={fade()}, exit={slide("left").For(Fast)} and
// animate={layout()} on elements that an {if} or {for} adds, and
// spring(target). The checks that need types are here; gen and the pairer
// in check.go check the attributes' form and place.

// defaultMS is how long a transition takes without .For.
const defaultMS = 200

// slideDirs are the directions slide takes.
var slideDirs = []string{"left", "right", "up", "down"}

// motionProps are the element properties stdlib.js reads the values of
// the motion attributes from.
var motionProps = map[string]string{"enter": "$in", "exit": "$out", "animate": "$lay"}

// checkMotion checks the arguments that motion needs as constants: the
// direction of slide(dir) and the duration of t.For(d). It returns each
// For call's duration in milliseconds.
func checkMotion(file *ast.File, info *types.Info, fset *token.FileSet, errAt func(token.Pos, string)) map[*ast.CallExpr]float64 {
	prelude := func(obj types.Object) bool {
		return obj != nil && fset.Position(obj.Pos()).Filename == preludeFile
	}
	out := map[*ast.CallExpr]float64{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		arg := call.Args[0]
		tv := info.Types[arg]
		switch fun := ast.Unparen(call.Fun).(type) {
		case *ast.Ident:
			if obj, ok := info.Uses[fun].(*types.Func); !ok || !prelude(obj) || obj.Name() != "slide" {
				return true
			}
			if tv.Value == nil || tv.Value.Kind() != constant.String {
				errAt(arg.Pos(), `slide needs a constant direction: "left", "right", "up" or "down"`)
			} else if dir := constant.StringVal(tv.Value); !slices.Contains(slideDirs, dir) {
				errAt(arg.Pos(), fmt.Sprintf(`slide takes "left", "right", "up" or "down", not %q`, dir))
			}
		case *ast.SelectorExpr:
			sel := info.Selections[fun]
			if sel == nil || sel.Kind() != types.MethodVal || !prelude(sel.Obj()) || sel.Obj().Name() != "For" {
				return true
			}
			if tv.Value == nil || tv.Value.Kind() != constant.String {
				errAt(arg.Pos(), "For needs a constant duration, like 150ms or a den.Duration const")
				return true
			}
			ms, msg := durationMS(constant.StringVal(tv.Value))
			if msg != "" {
				errAt(arg.Pos(), msg)
				return true
			}
			out[call] = ms
		}
		return true
	})
	return out
}

// durationMS reads a duration as design tokens write it, a number of ms or
// s (1e3ms and 1.5E-1s too), and returns it in milliseconds.
func durationMS(v string) (float64, string) {
	s := trimCSSSpace(v)
	if strings.HasPrefix(s, "-") {
		return 0, fmt.Sprintf("a transition cannot take a negative time: %q", v)
	}
	if !isDuration(s) {
		return 0, fmt.Sprintf("%q is not a duration (want a number of ms or s, like 150ms)", v)
	}
	s = strings.TrimPrefix(s, "+")
	scale := 1.0
	if num, ok := strings.CutSuffix(s, "ms"); ok {
		s = num
	} else {
		s, scale = strings.TrimSuffix(s, "s"), 1000
	}
	f, err := strconv.ParseFloat(s, 64)
	if f *= scale; err != nil || math.IsInf(f, 0) {
		return 0, fmt.Sprintf("%q is too long for a transition", v)
	}
	return f, ""
}

// isOpaque reports whether t is one of den's opaque values, a Transition
// or a Layout, whose zero value is null.
func isOpaque(t types.Type) bool {
	s, ok := t.Underlying().(*types.Struct)
	if !ok || s.NumFields() != 1 {
		return false
	}
	n, ok := s.Field(0).Type().(*types.Named)
	return ok && n.Obj().Name() == "denOpaque" && n.Obj().Parent() == n.Obj().Pkg().Scope()
}

// motionIn reports whether an element at the top level of nodes, or at the
// top level of a block there (and so on), has enter= or exit=, or with
// layout set, animate=. Those elements are what a block adds and removes,
// so the block needs the motion variant of its helper ($ifm, $form).
func motionIn(nodes []Node, layout bool) bool {
	for _, n := range nodes {
		switch n := n.(type) {
		case *Element:
			if n.IsComponent() {
				continue
			}
			for _, a := range n.Attrs {
				if a.Kind == AttrExpr && (a.Name == "enter" || a.Name == "exit" || layout && a.Name == "animate") {
					return true
				}
			}
		case *If:
			if motionIn(n.Then, layout) || motionIn(n.Else, layout) {
				return true
			}
		case *For:
			if motionIn(n.Body, layout) {
				return true
			}
		}
	}
	return false
}

// motionValue returns the JS for x, the value of a motion attribute. A
// transition made of den's functions and constants alone, the usual
// case, is built once, before the components, and shared by every
// element that uses it.
func (e *emitter) motionValue(x ast.Expr) string {
	js := e.expr(x)
	if !e.motionStatic(x) || !strings.Contains(js, "$") { // not null or 1
		return js
	}
	if name, ok := e.motions[js]; ok {
		return name
	}
	name := fmt.Sprintf("$m%d", len(e.motions))
	if e.motions == nil {
		e.motions = map[string]string{}
	}
	e.motions[js] = name
	fmt.Fprintf(&e.tplOut, "const %s = %s;\n", name, js)
	return name
}

// motionStatic reports whether x uses nothing but constants, types and
// den's motion functions, so that it has the same value wherever it is
// evaluated.
func (e *emitter) motionStatic(x ast.Expr) bool {
	ok := true
	ast.Inspect(x, func(n ast.Node) bool {
		if id, isID := n.(*ast.Ident); isID && ok {
			switch o := e.ck.info.Uses[id].(type) {
			case *types.Const, *types.TypeName, *types.PkgName, *types.Nil:
			default:
				ok = e.ck.preludeFunc(o, "fade", "scale", "slide", "For", "layout")
			}
		}
		return ok
	})
	return ok
}

// transition returns the JS for x lasting ms when x is fade(), scale() or
// slide(dir), with or without .For: t.For(d) then builds its transition
// with d instead of copying t's.
func (e *emitter) transition(x ast.Expr, ms string) (string, bool) {
	c, ok := ast.Unparen(x).(*ast.CallExpr)
	if !ok {
		return "", false
	}
	switch fun := ast.Unparen(c.Fun).(type) {
	case *ast.Ident:
		switch o := e.ck.info.Uses[fun]; {
		case e.ck.preludeFunc(o, "fade", "scale"):
			return "$" + o.Name() + "(" + ms + ")", true
		case e.ck.preludeFunc(o, "slide"):
			return "$slide(" + e.expr(c.Args[0]) + ", " + ms + ")", true
		}
	case *ast.SelectorExpr:
		if sel := e.ck.info.Selections[fun]; sel != nil && sel.Kind() == types.MethodVal && e.ck.preludeFunc(sel.Obj(), "For") {
			return e.transition(fun.X, ms)
		}
	}
	return "", false
}

// preludeFunc reports whether o is den's own function or method with one
// of the names. (A component's name may be placed in the prelude file.)
func (ck *checked) preludeFunc(o types.Object, names ...string) bool {
	f, ok := o.(*types.Func)
	return ok && slices.Contains(names, f.Name()) && ck.fset.Position(f.Pos()).Filename == preludeFile &&
		(f.Signature().Recv() != nil || ck.components[f.Name()] == nil)
}

// handlerTypes returns the JS declaring $evs: every event type the
// program has a handler for (onclick, onmouseenter, ...). A leaving
// element stops all of them, delegated or not, before they reach a
// handler. Den takes any on<name> attribute, so the list comes from the
// program itself rather than from a table that could fall behind it.
func handlerTypes(ck *checked) string {
	seen := map[string]bool{}
	for _, cc := range ck.comps {
		for a, pt := range cc.attrs {
			if pt.kind == partHandler {
				seen[a.Name[2:]] = true
			}
		}
	}
	var types []string
	for t := range seen {
		types = append(types, jsString(t))
	}
	slices.Sort(types)
	return "const $evs = [" + strings.Join(types, ", ") + "];\n"
}

// jsNumber writes a float64 as a JS number literal.
func jsNumber(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
