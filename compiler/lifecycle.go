package compiler

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"
)

// Lifecycle: onMount, onCleanup, after and every belong to the component
// instance whose Go calls them, which emit gives a record ($c = $life())
// that they register with; ref() makes a *Ref that ref={r} fills. The
// checks that need types are here; gen and the pairer in check.go check
// the ref= attribute's form and place.

// lifeCtx is Go that den evaluates again, or once per row or element,
// rather than once per component: where says what it is, and why the
// rest of the message.
type lifeCtx struct{ where, why string }

const (
	whyRecomputed = "it would run again each time the value is recomputed"
	whyMarkup     = "den evaluates it each time it builds that markup, and again when a signal it reads changes"
	whyElement    = "den evaluates it each time it makes the element"
)

// lifeChecker reports den's lifecycle functions called where they cannot
// be: onMount, onCleanup, after and every outside a component, which has
// no instance for them; and any of them, or ref, in Go that den may
// evaluate many times for one instance, where each evaluation would add
// another timer, cleanup or ref.
type lifeChecker struct {
	info   *types.Info
	fset   *token.FileSet
	byName map[string]*Component
	errAt  func(token.Pos, string)
	comp   bool // walking a component
}

// checkLifecycle checks every call of den's lifecycle functions and ref,
// and that den's Ref is only ever used as a *Ref.
func checkLifecycle(file *ast.File, info *types.Info, fset *token.FileSet, byName map[string]*Component, errAt func(token.Pos, string)) {
	lc := &lifeChecker{info: info, fset: fset, byName: byName, errAt: errAt}
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil || fset.Position(fd.Pos()).Filename == preludeFile && byName[fd.Name.Name] == nil {
			continue
		}
		lc.comp = fd.Recv == nil && byName[fd.Name.Name] != nil
		if !lc.comp {
			lc.walk(fd.Body, nil)
			continue
		}
		// The component's own statements come before gen's denMark(), and
		// the Go gen wrote for its markup after it.
		body := fd.Body.List
		mark := -1
		for i := len(body) - 1; i >= 0 && mark < 0; i-- {
			if isMark(body[i]) {
				mark = i
			}
		}
		if mark < 0 {
			lc.walk(fd.Body, nil)
			continue
		}
		for _, s := range body[:mark] {
			lc.walk(s, nil)
		}
		lc.markup(body[mark+1:], nil)
	}
	// A Ref value, as opposed to a *Ref, could never hold an element: no
	// ref= takes one, and Go would call its methods through its address,
	// which den has no way to take.
	for x, tv := range info.Types {
		if _, paren := x.(*ast.ParenExpr); paren || !tv.IsValue() || !lc.isRef(tv.Type) {
			continue
		}
		errAt(x.Pos(), "den's Ref is used only as a *Ref, made by ref(): a Ref value never holds an element")
	}
}

// isRef reports whether t is den's Ref (not a *Ref).
func (lc *lifeChecker) isRef(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Name() == "Ref" && lc.fset.Position(n.Obj().Pos()).Filename == preludeFile && isOpaque(t)
}

// prelude returns the name of den's function that call calls, or "".
func (lc *lifeChecker) prelude(call *ast.CallExpr) string {
	id, ok := callee(lc.info, ast.Unparen(call.Fun)).(*ast.Ident)
	if !ok {
		return ""
	}
	f, ok := lc.info.Uses[id].(*types.Func)
	if !ok || f.Signature().Recv() != nil || lc.byName[f.Name()] != nil || lc.fset.Position(f.Pos()).Filename != preludeFile {
		return ""
	}
	return f.Name()
}

// walk checks the calls in n, which den evaluates in ctx (nil: once per
// component instance, or when an event or timer calls it).
func (lc *lifeChecker) walk(n ast.Node, ctx *lifeCtx) {
	if n == nil {
		return
	}
	ast.Inspect(n, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch name := lc.prelude(call); name {
		case "onMount", "onCleanup", "after", "every", "ref":
			switch {
			case ctx != nil:
				lc.errAt(call.Fun.Pos(), fmt.Sprintf("%s cannot be called in %s: %s", name, ctx.where, ctx.why))
				return false
			case !lc.comp && name != "ref":
				lc.errAt(call.Fun.Pos(), fmt.Sprintf("%s can only be called in a component: in its statements, handlers or func literals", name))
				return false
			}
		case "computed", "spring":
			if ctx == nil {
				for _, a := range call.Args {
					lc.walk(a, &lifeCtx{name + "(...)", whyRecomputed})
				}
				return false
			}
		}
		return true
	})
}

// markup checks stmts, the Go that gen wrote for markup (see gen.node),
// first of which is denKey(x) when key says what x is.
func (lc *lifeChecker) markup(stmts []ast.Stmt, key *lifeCtx) {
	for i, s := range stmts {
		switch s := s.(type) {
		case *ast.ExprStmt:
			call, ok := s.X.(*ast.CallExpr)
			if !ok {
				lc.walk(s, &lifeCtx{"markup {...}", whyMarkup})
				continue
			}
			fn := ""
			if id, ok := call.Fun.(*ast.Ident); ok {
				fn = id.Name
			}
			switch fn {
			case "denHandler":
				lc.walk(call, nil) // a handler's statements run on its events
			case "denText", "denAttr":
				lc.walk(call, &lifeCtx{"markup {...}", whyMarkup})
			case "denKey":
				if i == 0 && key != nil {
					lc.walk(call, key)
				} else {
					lc.walk(call, &lifeCtx{"markup {...}", whyMarkup})
				}
			case "denEnter", "denExit", "denAnimate":
				lc.walk(call, &lifeCtx{strings.ToLower(fn[3:4]) + fn[4:] + "=", whyElement})
			case "denRef":
				lc.walk(call, &lifeCtx{"ref=", whyElement})
			case "denAny":
				lc.walk(call, &lifeCtx{"an attribute's value", whyElement})
			default: // the use of a component
				for _, a := range call.Args {
					if slot, ok := a.(*ast.CallExpr); ok {
						if id, ok := slot.Fun.(*ast.Ident); ok && id.Name == "denSlot" && len(slot.Args) == 1 {
							if fl, ok := slot.Args[0].(*ast.FuncLit); ok {
								lc.markup(fl.Body.List, nil)
							}
							continue
						}
					}
					// A func literal that is the whole prop is only made
					// when the prop is evaluated: its body runs when the
					// component given it calls it, as a func among the
					// statements passed as a prop would.
					if fl, ok := ast.Unparen(a).(*ast.FuncLit); ok {
						lc.walk(fl.Body, nil)
						continue
					}
					lc.walk(a, &lifeCtx{"a prop", "den evaluates props each time it builds the component"})
				}
			}
		case *ast.IfStmt:
			ctx := &lifeCtx{"an {if} condition", whyMarkup}
			lc.walk(s.Init, ctx)
			lc.walk(s.Cond, ctx)
			lc.markup(s.Body.List, nil)
			if els, ok := s.Else.(*ast.BlockStmt); ok {
				lc.markup(els.List, nil)
			} else {
				lc.walk(s.Else, ctx)
			}
		case *ast.RangeStmt:
			lc.walk(s.X, &lifeCtx{"a {for} clause", whyMarkup})
			lc.markup(s.Body.List, &lifeCtx{"a list key", "den evaluates it for each row, again whenever the list changes"})
		case *ast.BlockStmt:
			lc.markup(s.List, &lifeCtx{"a {key} value", whyMarkup})
		default:
			lc.walk(s, &lifeCtx{"markup {...}", whyMarkup})
		}
	}
}

// usesLife reports whether the Go of component fd (its statements and its
// markup's, handlers and slot markup included) calls onMount, onCleanup,
// after or every, or resource, whose request in flight is aborted when the
// component goes, so its JS function needs its lifecycle record.
func (ck *checked) usesLife(fd *ast.FuncDecl) bool {
	found := false
	if fd == nil {
		return false
	}
	ast.Inspect(fd, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && !found {
			if id, ok := callee(ck.info, ast.Unparen(call.Fun)).(*ast.Ident); ok && ck.preludeFunc(ck.info.Uses[id], append(lifeNames[:len(lifeNames):len(lifeNames)], "resource")...) {
				found = true
			}
		}
		return !found
	})
	return found
}
