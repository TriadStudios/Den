package compiler

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strconv"
)

// A server-sent event source: events(url), then on[T](s, name, func(T)).
// check.go declares events, Stream and on in the prelude, each giving way
// to a name of the program's own (eventNames), and checkEvents checks
// where they are used and that den can decode JSON into each T. Emit
// writes events as $events (eventsCall) and on as $onEv (onCall). The URL
// is read in an effect, so a signal in it reconnects; the event name is
// read once, untracked. Connected and Err read the source's signals.

// eventNames returns, for each of den's events, Stream and on that the
// program's own names take away, the program's name that does. events and
// Stream go together, since events returns a *Stream; on goes with them,
// or alone.
func eventNames(own map[string]string) map[string]string {
	by := map[string]string{}
	for _, name := range []string{"events", "Stream"} {
		if own[name] != "" {
			by["events"], by["Stream"], by["on"] = name, name, name
		}
	}
	if own["on"] != "" {
		by["on"] = "on"
	}
	return by
}

// checkEvents checks the uses of den's events and on once Go accepts the
// program. Both are called only in a component's statements, like resource
// and action: not in a func literal, markup, a handler, a prop or the
// argument of computed, spring, resource or action, which den may run
// again or later, nor in a top-level function. on's first argument is
// den's *Stream and its fn is func(T), which the prelude signature
// requires, and den must be able to decode JSON into T.
func checkEvents(file *ast.File, info *types.Info, pkg *types.Package, fset *token.FileSet, byName map[string]*Component, errAt func(token.Pos, string)) {
	prelude := func(name string) types.Object {
		o := pkg.Scope().Lookup(name)
		if o == nil || fset.Position(o.Pos()).Filename != preludeFile {
			return nil
		}
		return o
	}
	eventsFn, onFn := prelude("events"), prelude("on")
	computedFn, springFn, resourceFn, actionFn := prelude("computed"), prelude("spring"), prelude("resource"), prelude("action")
	if eventsFn == nil && onFn == nil {
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
					case obj == eventsFn || obj == onFn:
						allowed[n] = true
					case obj == computedFn || obj == springFn || obj == resourceFn || obj == actionFn:
						return false
					}
				}
				return true
			})
		}
	}
	jc := &jsonChecker{fset: fset, errAt: errAt, tags: map[string]bool{}, who: "on"}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || fset.Position(call.Pos()).Filename == preludeFile {
			return true
		}
		obj, id := of(call)
		switch {
		case obj == nil:
		case obj == eventsFn:
			if !allowed[call] {
				errAt(id.Pos(), "events can only be called in a component's statements")
			}
		case obj == onFn:
			if len(call.Args) >= 2 {
				if v := info.Types[call.Args[1]].Value; v != nil && v.Kind() == constant.String {
					switch name := constant.StringVal(v); name {
					case "error", "open":
						errAt(call.Args[1].Pos(), fmt.Sprintf("event name %q is reserved", name))
					}
				}
			}
			if !allowed[call] {
				errAt(id.Pos(), "on can only be called in a component's statements")
				break
			}
			if t, pos := onType(info, call); t != nil {
				jc.check(t, pos)
			}
		}
		return true
	})
}

// onType returns the T of on[T], from the explicit type argument or, when
// Go inferred it, from fn's parameter, and where an error about T points.
func onType(info *types.Info, call *ast.CallExpr) (types.Type, token.Pos) {
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.IndexExpr:
		if info.Types[fun.Index].IsType() {
			return info.Types[fun.Index].Type, fun.Index.Pos()
		}
	}
	if len(call.Args) >= 3 {
		if sig, ok := info.TypeOf(call.Args[2]).Underlying().(*types.Signature); ok && sig.Params().Len() == 1 {
			return sig.Params().At(0).Type(), call.Args[2].Pos()
		}
	}
	return nil, token.NoPos
}

// eventsCall emits events(url) as $events($c, () => url). The thunk is
// read in an effect, so a signal it reads reconnects. $c is the
// component's lifecycle record (usesLife counts events).
func (e *emitter) eventsCall(x *ast.CallExpr) string {
	return "$events($c, () => " + e.expr(x.Args[0]) + ")"
}

// onCall emits on[T](s, name, fn) as $onEv($c, s, nameOnce, $jt, desc, fn).
// $c is the caller's lifecycle record, so the registration goes away with
// the component that called on, which may not be the one that called events.
// nameOnce reads the name once, untracked. desc is T's JSON descriptor,
// the same table fetchJSON uses.
func (e *emitter) onCall(x *ast.CallExpr) string {
	t, _ := onType(e.ck.info, x)
	return "$onEv($c, " + e.expr(x.Args[0]) + ", $scope(() => " + e.expr(x.Args[1]) + ")[0], $jt, " +
		strconv.Itoa(e.jsonDesc(t)) + ", " + e.expr(x.Args[2]) + ")"
}
