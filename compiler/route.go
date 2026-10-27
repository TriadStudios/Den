package compiler

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strings"
	"unicode"
)

// routeSeg is one segment of a route pattern: static text, a param (":x")
// or a catch-all ("*x").
type routeSeg struct {
	kind byte   // 0 for static text, ':' or '*'
	text string // the text, or the param's name
}

// parseRoute splits a route pattern into segments, ignoring one trailing
// slash (except in "/"), or says what is wrong with it.
func parseRoute(pat string) ([]routeSeg, string) {
	if !strings.HasPrefix(pat, "/") {
		return nil, fmt.Sprintf("route pattern %q must start with /", pat)
	}
	parts := strings.Split(strings.TrimSuffix(pat[1:], "/"), "/") // "/" is [""]
	var segs []routeSeg
	for i, s := range parts {
		if s == "" || s[0] != ':' && s[0] != '*' {
			segs = append(segs, routeSeg{text: s})
			continue
		}
		name := s[1:]
		switch {
		case name == "":
			return nil, fmt.Sprintf("route pattern %q has a param with no name", pat)
		case name == "_":
			return nil, fmt.Sprintf("route pattern %q: a param cannot be named _", pat)
		case !paramName(name):
			return nil, fmt.Sprintf("route pattern %q: param name %q is not a Go identifier", pat, name)
		case s[0] == '*' && i < len(parts)-1:
			return nil, fmt.Sprintf("route pattern %q: *%s must be the last segment", pat, name)
		}
		for _, prev := range segs {
			if prev.kind != 0 && strings.EqualFold(prev.text, name) {
				return nil, fmt.Sprintf("route pattern %q has param %s twice (param names ignore case)", pat, name)
			}
		}
		segs = append(segs, routeSeg{kind: s[0], text: name})
	}
	return segs, ""
}

// paramName reports whether s is a Go identifier. Keywords are fine, since
// a param names a field (":type" fills Type).
func paramName(s string) bool {
	for i, r := range s {
		if !(r == '_' || unicode.IsLetter(r) || i > 0 && unicode.IsDigit(r)) {
			return false
		}
	}
	return s != ""
}

// checkRoutes checks every call of the prelude's route, route[T](pattern),
// and returns for each the pattern den's runtime matches: the pattern with
// its params renamed to the JS names of the fields of T they fill.
func checkRoutes(file *ast.File, info *types.Info, pkg *types.Package, fset *token.FileSet, errAt func(token.Pos, string)) map[*ast.CallExpr]string {
	prelude := func(name string) types.Object {
		o := pkg.Scope().Lookup(name)
		if o == nil || fset.Position(o.Pos()).Filename != preludeFile {
			return nil
		}
		return o
	}
	routeFn, pathFn := prelude("route"), prelude("path")
	specs := map[*ast.CallExpr]string{}
	called := map[*ast.Ident]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		fun := ast.Unparen(call.Fun)
		ix, _ := fun.(*ast.IndexExpr)
		if ix != nil {
			fun = ast.Unparen(ix.X)
		}
		id, ok := fun.(*ast.Ident)
		if !ok {
			return true
		}
		switch obj := info.Uses[id]; {
		case obj == nil:
		case obj == pathFn:
			called[id] = true
		case obj == routeFn:
			called[id] = true
			if spec, ok := routeSpec(call, ix, info, errAt); ok {
				specs[call] = spec
			}
		}
		return true
	})
	// The checks above need to see the call, so route and path cannot be
	// passed around as values.
	for id, obj := range info.Uses {
		if obj != nil && (obj == routeFn || obj == pathFn) && !called[id] {
			errAt(id.Pos(), fmt.Sprintf("%s can only be called, not used as a value", id.Name))
		}
	}
	return specs
}

// routeSpec checks one call route[T](pattern): the pattern must be a
// constant, and each param must fill exactly one field of the struct T,
// matched ignoring case, with every field filled and each a string or an
// int (a catch-all only a string).
func routeSpec(call *ast.CallExpr, ix *ast.IndexExpr, info *types.Info, errAt func(token.Pos, string)) (string, bool) {
	if ix == nil || len(call.Args) != 1 {
		return "", false // go/types reported it
	}
	argPos, typePos := call.Args[0].Pos(), ix.Index.Pos()
	v := info.Types[call.Args[0]].Value
	if v == nil || v.Kind() != constant.String {
		errAt(argPos, "route pattern must be a constant string")
		return "", false
	}
	pat := constant.StringVal(v)
	segs, msg := parseRoute(pat)
	if msg != "" {
		errAt(argPos, msg)
		return "", false
	}
	t := info.TypeOf(call).(*types.Pointer).Elem().(*types.Named).TypeArgs().At(0)
	st, ok := t.Underlying().(*types.Struct)
	if !ok {
		errAt(typePos, errorf("route params must be a struct type, not %s", t))
		return "", false
	}
	field := make([]int, len(segs)) // the field each param fills
	used := make([]bool, st.NumFields())
	for i, s := range segs {
		if s.kind == 0 {
			continue
		}
		field[i] = -1
		for j := 0; j < st.NumFields(); j++ {
			if !strings.EqualFold(s.text, st.Field(j).Name()) {
				continue
			}
			if field[i] >= 0 {
				errAt(argPos, errorf("route pattern %q: param %s matches both %s and %s of %s", pat, s.text, st.Field(field[i]).Name(), st.Field(j).Name(), t))
				return "", false
			}
			field[i] = j
		}
		if field[i] < 0 {
			errAt(argPos, errorf("route pattern %q: param %s matches no field of %s", pat, s.text, t))
			return "", false
		}
		used[field[i]] = true
	}
	for j := 0; j < st.NumFields(); j++ {
		f := st.Field(j)
		if !used[j] {
			errAt(typePos, errorf("field %s of %s has no param in route pattern %q", f.Name(), t, pat))
			return "", false
		}
		if b, ok := f.Type().Underlying().(*types.Basic); !ok || b.Kind() != types.String && b.Kind() != types.Int {
			errAt(typePos, errorf("field %s of %s is %s; route params can be strings or ints", f.Name(), t, f.Type()))
			return "", false
		}
	}
	var b strings.Builder
	for i, s := range segs {
		b.WriteByte('/')
		if s.kind == 0 {
			b.WriteString(s.text)
			continue
		}
		f := st.Field(field[i])
		if s.kind == '*' && !isString(f.Type()) {
			errAt(typePos, errorf("field %s of %s is %s, but the catch-all *%s needs a string", f.Name(), t, f.Type(), s.text))
			return "", false
		}
		b.WriteByte(s.kind)
		b.WriteString(fieldName(f.Name()))
	}
	return b.String(), true
}
