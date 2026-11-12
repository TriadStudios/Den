package compiler

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strconv"
)

// A typed request made by hand: action(sendJSON[In, Out](method, url)).
// check.go declares action, Action and sendJSON in the prelude, each
// giving way to a name of the program's own (actionNames), and
// checkActions checks where they are used, that the method is one of the
// four verbs, and that den can encode In and decode Out. Emit writes an
// action as $action (actionCall): the method and URL are read when Do
// runs, not in an effect, and $sendJSON encodes In and decodes Out with
// the same JSON descriptors as fetchJSON.

// actionNames returns, for each of den's action, Action and sendJSON that
// the program's own names take away, the program's name that does. action
// and Action go together, since action returns an *Action; sendJSON, only
// ever action's argument, goes with them, or alone.
func actionNames(own map[string]string) map[string]string {
	by := map[string]string{}
	for _, name := range []string{"action", "Action"} {
		if own[name] != "" {
			by["action"], by["Action"], by["sendJSON"] = name, name, name
		}
	}
	if own["sendJSON"] != "" {
		by["sendJSON"] = "sendJSON"
	}
	return by
}

// checkActions checks the uses of den's action and sendJSON once Go
// accepts the program. action is called only in a component's statements,
// like resource: not in a func literal, markup, a handler, a prop or the
// argument of computed, spring or resource, which den may run again or
// later, nor in a top-level function. Its argument is a call of sendJSON,
// which appears nowhere else. The method is the constant POST, PUT, PATCH
// or DELETE. Do() is the call when In is struct{} (it sends {} , or no
// body when the method is DELETE); every other In is Do(v). And den must
// be able to encode In and decode Out.
func checkActions(file *ast.File, info *types.Info, pkg *types.Package, fset *token.FileSet, byName map[string]*Component, own map[string]string, errAt func(token.Pos, string)) {
	prelude := func(name string) types.Object {
		o := pkg.Scope().Lookup(name)
		if o == nil || fset.Position(o.Pos()).Filename != preludeFile {
			return nil
		}
		return o
	}
	actionFn, sendFn := prelude("action"), prelude("sendJSON")
	computedFn, springFn, resourceFn := prelude("computed"), prelude("spring"), prelude("resource")
	if actionFn == nil && sendFn == nil {
		return
	}
	of := func(call *ast.CallExpr) (types.Object, *ast.Ident) {
		id, ok := callee(info, ast.Unparen(call.Fun)).(*ast.Ident)
		if !ok {
			return nil, nil
		}
		return info.Uses[id], id
	}
	allowed := map[*ast.CallExpr]bool{}
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Body == nil || byName[fd.Name.Name] == nil {
			continue
		}
		body := fd.Body.List
		mark := len(body)
		for i := len(body) - 1; i >= 0; i-- {
			if isMark(body[i]) {
				mark = i
				break
			}
		}
		for _, s := range body[:mark] {
			ast.Inspect(s, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.FuncLit:
					return false
				case *ast.CallExpr:
					switch obj, _ := of(n); {
					case obj == nil:
					case obj == actionFn:
						allowed[n] = true
						return false
					case obj == computedFn || obj == springFn || obj == resourceFn:
						return false
					}
				}
				return true
			})
		}
	}
	placed := map[*ast.CallExpr]bool{}
	var sends []*ast.CallExpr
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || fset.Position(call.Pos()).Filename == preludeFile {
			return true
		}
		obj, id := of(call)
		switch {
		case obj == nil:
		case obj == sendFn:
			sends = append(sends, call)
		case obj == actionFn:
			if !allowed[call] {
				errAt(id.Pos(), "action can only be called in a component's statements")
			}
			var inner *ast.CallExpr
			if len(call.Args) == 1 {
				inner, _ = ast.Unparen(call.Args[0]).(*ast.CallExpr)
			}
			if inner != nil {
				if f, _ := of(inner); f != nil && f == sendFn {
					placed[inner] = true
					break
				}
			}
			msg := "action takes a call of sendJSON, as in action(sendJSON[In, Out](method, url))"
			if inner != nil {
				if _, name := of(inner); name != nil && name.Name == "sendJSON" && own["sendJSON"] != "" {
					msg = "action takes a call of den's sendJSON, which is not available because this program " + own["sendJSON"] + "; rename it"
				}
			}
			if len(call.Args) == 1 {
				errAt(call.Args[0].Pos(), msg)
			}
		}
		return true
	})
	enc := &jsonChecker{fset: fset, errAt: errAt, tags: map[string]bool{}, who: "sendJSON", encode: true}
	dec := &jsonChecker{fset: fset, errAt: errAt, tags: map[string]bool{}, who: "sendJSON"}
	for _, call := range sends {
		_, id := of(call)
		if !placed[call] {
			errAt(id.Pos(), "sendJSON can only be used as action's argument: action(sendJSON[In, Out](method, url))")
			continue
		}
		if len(call.Args) >= 1 {
			checkSendMethod(info, call.Args[0], errAt)
		}
		var idxs []ast.Expr
		switch fun := ast.Unparen(call.Fun).(type) {
		case *ast.IndexListExpr:
			idxs = fun.Indices
		case *ast.IndexExpr:
			idxs = []ast.Expr{fun.Index}
		}
		if len(idxs) == 2 {
			if info.Types[idxs[0]].IsType() {
				enc.check(info.Types[idxs[0]].Type, idxs[0].Pos())
			}
			if info.Types[idxs[1]].IsType() {
				dec.check(info.Types[idxs[1]].Type, idxs[1].Pos())
			}
		}
	}
	// Do's arity depends on In, which Go's variadic signature cannot say.
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || fset.Position(call.Pos()).Filename == preludeFile {
			return true
		}
		fun, ok := callee(info, ast.Unparen(call.Fun)).(*ast.SelectorExpr)
		if !ok {
			return true
		}
		sel := info.Selections[fun]
		if sel == nil || sel.Kind() != types.MethodVal {
			return true
		}
		fn, _ := sel.Obj().(*types.Func)
		if fn == nil || fn.Name() != "Do" || fset.Position(fn.Pos()).Filename != preludeFile {
			return true
		}
		recv := sel.Recv()
		if p, ok := recv.(*types.Pointer); ok {
			recv = p.Elem()
		}
		named, ok := recv.(*types.Named)
		if !ok || named.Obj().Name() != "Action" || named.TypeArgs().Len() < 1 {
			return true
		}
		in := named.TypeArgs().At(0)
		empty := isEmptyStruct(in)
		switch {
		case empty && (len(call.Args) != 0 || call.Ellipsis.IsValid()):
			errAt(fun.Sel.Pos(), "Do takes no value when In is struct{}: a.Do() sends {} (or no body, for DELETE)")
		case !empty && (len(call.Args) != 1 || call.Ellipsis.IsValid()):
			errAt(fun.Sel.Pos(), "Do takes the value to send, as in a.Do(v)")
		}
		return true
	})
}

// checkSendMethod reports when sendJSON's method is not one of the four
// verbs, as a string constant. A value computed at run time could not
// decide, at the call of Do(), whether struct{} sends {} or no body.
func checkSendMethod(info *types.Info, method ast.Expr, errAt func(token.Pos, string)) {
	tv := info.Types[method]
	if tv.Value == nil || tv.Value.Kind() != constant.String {
		errAt(method.Pos(), `sendJSON's method must be the constant "POST", "PUT", "PATCH" or "DELETE"`)
		return
	}
	switch constant.StringVal(tv.Value) {
	case "POST", "PUT", "PATCH", "DELETE":
	default:
		errAt(method.Pos(), fmt.Sprintf(`sendJSON's method must be "POST", "PUT", "PATCH" or "DELETE", not %q`, constant.StringVal(tv.Value)))
	}
}

// isEmptyStruct reports whether t is the unnamed struct{}, including an
// alias of it. A named empty struct is a value Do sends as {}, not the
// marker for Do().
func isEmptyStruct(t types.Type) bool {
	t = types.Unalias(t)
	s, ok := t.(*types.Struct)
	return ok && s.NumFields() == 0
}

// actionCall emits action(sendJSON[In, Out](method, url)) as
// $action($c, () => [method, url], $sendJSON($jt, in, out), zeroOut).
// The thunk is read when Do runs, inside $scope, so a signal it reads is
// not subscribed and a panic is reported rather than sent. $c is the
// component's lifecycle record (usesLife counts action), with which the
// request in flight is aborted when the component goes.
func (e *emitter) actionCall(x *ast.CallExpr) string {
	f := ast.Unparen(x.Args[0]).(*ast.CallExpr) // checkActions made sure
	named := e.ck.info.TypeOf(x).(*types.Pointer).Elem().(*types.Named)
	in := named.TypeArgs().At(0)
	out := named.TypeArgs().At(1)
	return "$action($c, () => [" + e.expr(f.Args[0]) + ", " + e.expr(f.Args[1]) + "], $sendJSON($jt, " +
		strconv.Itoa(e.jsonDescMode(in, true)) + ", " + strconv.Itoa(e.jsonDesc(out)) + "), " + zeroValue(out) + ")"
}
