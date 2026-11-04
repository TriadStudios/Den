package compiler

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
)

// Context: provide(v) and use[T](). A component's provide(v) makes v what
// use[T] finds, for T the type of v, in its own later statements and in
// everything its markup builds, through the render tree: markup passed as
// children or a slot sees the context of the component that renders it.
//
// It is all compile time. In a program that provides something, every
// component's function takes the context as one more parameter, $x (an
// object, or undefined at the root), and every component call passes the
// caller's. provide(v) is const $xN = {__proto__: <context>, K: v}, where K
// is the number of v's type, so that a lookup walks the prototype chain to
// the nearest provider; use[T]() is (<context>?.[K] ?? zero T). ?? is the
// same as asking whether K is there: a den value is null only when T's zero
// value is null too (pointers, slices, funcs), and never undefined. Each
// provide makes a new const, which the code after it uses, so a use sees
// the provides written above it, wherever and whenever it runs. Markup
// passed to a den.Children prop is a closure ($svg, $x) => node, and
// {children} passes the context of the component rendering it. A program
// without provide compiles as it did before context existed.

// contextInfo is what emit needs to know about a program's context.
type contextInfo struct {
	// keys holds each provide and use call of den's: the key of its type,
	// or -1 for a use of a type that nothing provides, which is always the
	// zero value.
	keys map[*ast.CallExpr]int
	on   bool // something is provided: components take and pass the context
}

// contextCall returns "provide" or "use" when call calls den's own provide
// or use, and "" otherwise.
func contextCall(info *types.Info, fset *token.FileSet, call *ast.CallExpr) string {
	id, ok := callee(info, ast.Unparen(call.Fun)).(*ast.Ident)
	if !ok {
		return ""
	}
	f, ok := info.Uses[id].(*types.Func)
	if !ok || f.Signature().Recv() != nil || fset.Position(f.Pos()).Filename != preludeFile {
		return ""
	}
	if f.Name() == "provide" || f.Name() == "use" {
		return f.Name()
	}
	return ""
}

// contextType returns the type that a provide or use call finds its value
// by, and where to report it: the explicit type argument if there is one,
// else for provide its argument.
func contextType(info *types.Info, call *ast.CallExpr, fn string) (types.Type, token.Pos) {
	at := call.Pos()
	switch ix := ast.Unparen(call.Fun).(type) {
	case *ast.IndexExpr:
		at = ix.Index.Pos()
	case *ast.IndexListExpr:
		at = ix.Indices[0].Pos()
	default:
		if fn == "provide" && len(call.Args) > 0 {
			at = call.Args[0].Pos()
		}
	}
	sig, _ := info.TypeOf(call.Fun).(*types.Signature)
	switch {
	case sig == nil:
		return nil, at
	case fn == "provide" && sig.Params().Len() == 1:
		return sig.Params().At(0).Type(), at
	case fn == "use" && sig.Results().Len() == 1:
		return sig.Results().At(0).Type(), at
	}
	return nil, at
}

// checkContext reports provide and use where den cannot find a value by
// their type or in a place: a type that is not a named type of the
// program's own, a provide that is not a statement of its own at the top
// level of a component's statements, and a use outside a component. It
// numbers the types that are provided, in source order, one key for each
// distinct type (instances of a generic type are distinct types).
func checkContext(file *ast.File, info *types.Info, pkg *types.Package, fset *token.FileSet, byName map[string]*Component, errAt func(token.Pos, string)) contextInfo {
	type found struct {
		call *ast.CallExpr
		fn   string
		t    types.Type
	}
	var calls []found
	// The prelude's own functions call neither, so every declaration is
	// walked: a component's func may be placed in the prelude file when
	// nothing of the program's comes before it.
	for _, d := range file.Decls {
		fd, isFunc := d.(*ast.FuncDecl)
		comp := isFunc && fd.Recv == nil && byName[fd.Name.Name] != nil
		// The component's own statements end at the denMark() gen wrote
		// before the Go for its markup.
		top := map[*ast.CallExpr]bool{}
		if comp && fd.Body != nil {
			body := fd.Body.List
			for i := len(body) - 1; i >= 0; i-- {
				if isMark(body[i]) {
					body = body[:i]
					break
				}
			}
			for _, s := range body {
				if es, ok := s.(*ast.ExprStmt); ok {
					if c, ok := ast.Unparen(es.X).(*ast.CallExpr); ok {
						top[c] = true
					}
				}
			}
		}
		ast.Inspect(d, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn := contextCall(info, fset, call)
			switch {
			case fn == "":
				return true
			case fn == "provide" && !top[call]:
				errAt(call.Pos(), "provide must be a statement of its own at the top level of a component")
				return true
			case fn == "use" && !comp:
				errAt(call.Pos(), "use can only be called in a component")
				return true
			}
			t, at := contextType(info, call, fn)
			if t == nil {
				return true // go/types has reported it
			}
			named, ok := types.Unalias(t).(*types.Named)
			switch {
			case !ok:
				errAt(at, fmt.Sprintf("context values are found by their type, so %s needs a named type: declare one, as in type Theme struct{...}", fn))
			case named.Obj().Pkg() != pkg || fset.Position(named.Obj().Pos()).Filename == preludeFile:
				errAt(at, errorf("context values are found by their type, so %s needs a type this program declares, not %s: declare one, as in type Theme struct{...}", fn, t))
			default:
				calls = append(calls, found{call, fn, named})
			}
			return true
		})
	}
	ctx := contextInfo{keys: map[*ast.CallExpr]int{}}
	var provided []types.Type
	key := func(t types.Type) int {
		for i, p := range provided {
			if types.Identical(p, t) {
				return i
			}
		}
		return -1
	}
	for _, c := range calls {
		if c.fn == "provide" && key(c.t) < 0 {
			provided = append(provided, c.t)
		}
	}
	for _, c := range calls {
		ctx.keys[c.call] = key(c.t)
	}
	ctx.on = len(provided) > 0
	return ctx
}

// isProvide reports whether s is a provide statement of den's.
func (e *emitter) isProvide(s *ast.ExprStmt) (*ast.CallExpr, bool) {
	call, ok := ast.Unparen(s.X).(*ast.CallExpr)
	return call, ok && contextCall(e.ck.info, e.ck.fset, call) == "provide"
}

// provide emits provide(v): a new context whose prototype is the one in
// effect, holding v under its type's key. The code after it uses the new
// one.
func (e *emitter) provide(call *ast.CallExpr) {
	v := e.expr(call.Args[0])
	name := e.name("$x")
	e.line("const %s = {__proto__: %s, %d: %s};", name, e.ctx, e.ck.ctx.keys[call], v)
	e.ctx = name
}

// use returns the JS for use[T](): the value of the nearest provider of a
// T, or the zero T.
func (e *emitter) use(call *ast.CallExpr) string {
	zero := zeroValue(e.ck.info.TypeOf(call))
	k, ok := e.ck.ctx.keys[call]
	if !ok || k < 0 || e.ctx == "" {
		return zero
	}
	return fmt.Sprintf("(%s?.[%d] ?? %s)", e.ctx, k, zero)
}

// ctxArgs returns args, the arguments of a call that renders den.Children
// or a component, with the context in effect added when there is one.
func (e *emitter) ctxArgs(args string, render bool) string {
	if e.ctx == "" {
		return args
	}
	if render && args == "" {
		args = "0" // the namespace argument: HTML
	}
	if args != "" {
		args += ", "
	}
	return args + e.ctx
}
