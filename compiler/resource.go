package compiler

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Async data: resource(fetchJSON[T](url)). check.go declares resource,
// Resource and fetchJSON in the prelude, each giving way to a name of the
// program's own (resourceNames), and checkResources checks where they are
// used and that den can decode JSON into each T. Emit writes a resource as
// $resource with the arguments of the call it takes split from that
// call's requester (resourceCall), and a table of JSON descriptors that
// $jdec in stdlib.js reads to decode as encoding/json does (jsonDesc).

// resourceNames returns, for each of den's resource, Resource and
// fetchJSON that the program's own names take away, the program's name
// that does. resource and Resource go together, since resource returns a
// *Resource; fetchJSON, only ever resource's argument, goes with them, or
// alone.
func resourceNames(own map[string]string) map[string]string {
	by := map[string]string{}
	for _, name := range []string{"resource", "Resource"} {
		if own[name] != "" {
			by["resource"], by["Resource"], by["fetchJSON"] = name, name, name
		}
	}
	if own["fetchJSON"] != "" {
		by["fetchJSON"] = "fetchJSON"
	}
	return by
}

// checkResources checks the uses of den's resource and fetchJSON once Go
// accepts the program. resource is called only in a component's
// statements, which run once, when the component is made: not in a func
// literal, markup, a handler, a prop or the argument of computed or spring,
// which den may run again or later, nor in a top-level function. Its
// argument is a call of fetchJSON, which appears nowhere else. And den
// must be able to decode JSON into each fetchJSON's T.
func checkResources(file *ast.File, info *types.Info, pkg *types.Package, fset *token.FileSet, byName map[string]*Component, own map[string]string, errAt func(token.Pos, string)) {
	prelude := func(name string) types.Object {
		o := pkg.Scope().Lookup(name)
		if o == nil || fset.Position(o.Pos()).Filename != preludeFile {
			return nil
		}
		return o
	}
	resourceFn, fetchFn := prelude("resource"), prelude("fetchJSON")
	computedFn, springFn := prelude("computed"), prelude("spring")
	if resourceFn == nil && fetchFn == nil {
		return
	}
	// of returns what a call calls, through f[T](...), and the name it
	// calls it by.
	of := func(call *ast.CallExpr) (types.Object, *ast.Ident) {
		id, ok := callee(info, ast.Unparen(call.Fun)).(*ast.Ident)
		if !ok {
			return nil, nil
		}
		return info.Uses[id], id
	}
	// The resource calls in components' statements, before the denMark()
	// that gen writes ahead of the markup's Go.
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
					case obj == resourceFn:
						allowed[n] = true
						return false // its argument is evaluated again and again
					case obj == computedFn || obj == springFn:
						return false
					}
				}
				return true
			})
		}
	}
	placed := map[*ast.CallExpr]bool{} // fetchJSON calls that are resource's argument
	var fetches []*ast.CallExpr
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || fset.Position(call.Pos()).Filename == preludeFile {
			return true
		}
		obj, id := of(call)
		switch {
		case obj == nil:
		case obj == fetchFn:
			fetches = append(fetches, call)
		case obj == resourceFn:
			if !allowed[call] {
				errAt(id.Pos(), "resource can only be called in a component's statements")
			}
			var inner *ast.CallExpr
			if len(call.Args) == 1 {
				inner, _ = ast.Unparen(call.Args[0]).(*ast.CallExpr)
			}
			if inner != nil {
				if f, _ := of(inner); f != nil && f == fetchFn {
					placed[inner] = true
					break
				}
			}
			msg := "resource takes a call of fetchJSON, as in resource(fetchJSON[T](url))"
			if inner != nil {
				if _, name := of(inner); name != nil && name.Name == "fetchJSON" && own["fetchJSON"] != "" {
					msg = "resource takes a call of den's fetchJSON, which is not available because this program " + own["fetchJSON"] + "; rename it"
				}
			}
			errAt(call.Args[0].Pos(), msg)
		}
		return true
	})
	jc := &jsonChecker{fset: fset, errAt: errAt, tags: map[string]bool{}}
	for _, call := range fetches {
		_, id := of(call)
		if !placed[call] {
			errAt(id.Pos(), "fetchJSON can only be used as resource's argument: resource(fetchJSON[T](url))")
			continue
		}
		// T is always explicit: Go cannot infer it from url.
		if ix, ok := ast.Unparen(call.Fun).(*ast.IndexExpr); ok && info.Types[ix.Index].IsType() {
			jc.check(info.Types[ix.Index].Type, ix.Index.Pos())
		}
	}
}

// jsonChecker checks the types fetchJSON decodes JSON into.
type jsonChecker struct {
	fset  *token.FileSet
	errAt func(token.Pos, string)
	tags  map[string]bool // the tag options reported, by field and option
}

// check reports the first type reached from t, as encoding/json reaches
// types, that den cannot decode JSON into, at pos, the T of fetchJSON[T].
// It also reports json tag options other than omitempty and omitzero on
// the fields it decodes, at the field, once each.
func (jc *jsonChecker) check(t types.Type, pos token.Pos) {
	var seen []types.Type
	if bad, where, why := jc.walk(t, "", &seen); bad != nil {
		msg := errorf("fetchJSON cannot decode JSON into %s", bad)
		if where != "" {
			msg += " (" + where + ")"
		}
		jc.errAt(pos, msg+": "+why)
	}
}

// walk returns the first type den cannot decode JSON into in t, which is
// where says, and why; or nil.
func (jc *jsonChecker) walk(written types.Type, where string, seen *[]types.Type) (types.Type, string, string) {
	t := types.Unalias(written)
	if slices.ContainsFunc(*seen, func(s types.Type) bool { return types.Identical(s, t) }) {
		return nil, "", ""
	}
	*seen = append(*seen, t)
	const only = "den decodes JSON into strings, numbers, bools, and structs, arrays, slices and maps of them"
	if n, ok := t.(*types.Named); ok && (jc.fset.Position(n.Obj().Pos()).Filename == preludeFile || isChildren(t)) || isOpaque(t) {
		return written, where, "it is one of den's own types"
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		if u.Info()&(types.IsString|types.IsBoolean|types.IsInteger|types.IsFloat) != 0 && u.Kind() != types.UnsafePointer {
			return nil, "", ""
		}
	case *types.Slice:
		return jc.walk(u.Elem(), errorf("the elements of %s", written), seen)
	case *types.Array:
		return jc.walk(u.Elem(), errorf("the elements of %s", written), seen)
	case *types.Map:
		if k, ok := u.Key().Underlying().(*types.Basic); !ok || k.Info()&(types.IsString|types.IsInteger) == 0 {
			return written, where, "the keys of a JSON object decode only into strings and integers"
		}
		return jc.walk(u.Elem(), errorf("the values of %s", written), seen)
	case *types.Struct:
		fields, badNames := jsonFields(t)
		for _, f := range badNames {
			key := jc.fset.Position(f.v.Pos()).String() + " name"
			if !jc.tags[key] {
				jc.tags[key] = true
				jc.errAt(f.v.Pos(), errorf("fetchJSON does not support the json tag name %q (on field %s of %s): versions of Go's encoding/json read it differently; use letters, digits, spaces and ASCII punctuation other than quotes, backquotes and backslashes", f.name, f.v.Name(), written))
			}
		}
		for _, f := range fields {
			at := errorf("field %s of %s", f.v.Name(), written)
			if f.ptr != nil {
				return f.ptr, errorf("embedded in %s", written), "den does not support pointers yet"
			}
			for _, o := range strings.Split(f.opts, ",") {
				key := jc.fset.Position(f.v.Pos()).String() + " " + o
				if o != "" && o != "omitempty" && o != "omitzero" && !jc.tags[key] {
					jc.tags[key] = true
					jc.errAt(f.v.Pos(), fmt.Sprintf("fetchJSON does not support the json tag option %q yet (on %s): only omitempty and omitzero, which change nothing in decoding", o, at))
				}
			}
			if bad, w, why := jc.walk(f.v.Type(), at, seen); bad != nil {
				return bad, w, why
			}
		}
		return nil, "", ""
	case *types.Pointer:
		return written, where, "den does not support pointers yet"
	case *types.Interface:
		return written, where, "den does not decode JSON into interfaces"
	}
	return written, where, only
}

// jsonField is a struct field that encoding/json decodes, found as its
// typeFields finds them: exported fields, and the fields of embedded
// structs (exported or not), promoted; named by their json tag, or else
// by their own name; json:"-" leaves a field out.
type jsonField struct {
	name  string     // the key it takes
	tag   bool       // name comes from the json tag
	index []int      // the field, through the embedded structs it is promoted from
	id    int        // its place in breadth-first order (level by level)
	v     *types.Var // the field
	opts  string     // the tag's options, after the name
	ptr   types.Type // an embedded pointer it is promoted through, if any
}

// jsonFields returns the fields of struct type t that encoding/json
// decodes, in field order. Like Go's typeFields, it explores embedded
// structs level by level, then keeps, of the fields with one name, the
// least deep, a tagged one before others at its depth, and none if two at
// that depth are both tagged or both not. It also returns the fields whose
// json tag names a key that older versions of encoding/json take as no
// name (isValidTag) and newer ones take as is, with that name.
func jsonFields(t types.Type) (fields, badNames []jsonField) {
	type level struct {
		typ   types.Type
		index []int
		ptr   types.Type
	}
	var current, next []level
	next = []level{{typ: t}}
	var count, nextCount typeCount
	var visited []types.Type
	for len(next) > 0 {
		current, next = next, nil
		count, nextCount = nextCount, nil
		for _, f := range current {
			if slices.ContainsFunc(visited, func(v types.Type) bool { return types.Identical(v, f.typ) }) {
				continue
			}
			visited = append(visited, f.typ)
			st := f.typ.Underlying().(*types.Struct)
			for i := 0; i < st.NumFields(); i++ {
				sf := st.Field(i)
				ft, ptr := types.Unalias(sf.Type()), f.ptr
				if p, ok := ft.(*types.Pointer); ok {
					ft = types.Unalias(p.Elem())
					if sf.Embedded() {
						ptr = sf.Type()
					}
				}
				_, isStruct := ft.Underlying().(*types.Struct)
				if sf.Embedded() && !sf.Exported() && !isStruct || !sf.Embedded() && !sf.Exported() {
					continue
				}
				tag := reflect.StructTag(st.Tag(i)).Get("json")
				if tag == "-" {
					continue
				}
				name, opts, _ := strings.Cut(tag, ",")
				if !validTag(name) {
					if name != "" {
						badNames = append(badNames, jsonField{name: name, v: sf})
					}
					name = ""
				}
				index := append(slices.Clone(f.index), i)
				if name != "" || !sf.Embedded() || !isStruct {
					jf := jsonField{name: name, tag: name != "", index: index, id: len(fields), v: sf, opts: opts, ptr: f.ptr}
					if name == "" {
						jf.name = sf.Name()
					}
					fields = append(fields, jf)
					if count.get(f.typ) > 1 {
						// Two instances of one embedded struct at one depth:
						// a second copy makes the names conflict below.
						fields = append(fields, jf)
					}
					continue
				}
				if nextCount.add(ft) == 1 {
					next = append(next, level{ft, index, ptr})
				}
			}
		}
	}
	slices.SortFunc(fields, func(a, b jsonField) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		if c := len(a.index) - len(b.index); c != 0 {
			return c
		}
		if a.tag != b.tag {
			if a.tag {
				return -1
			}
			return 1
		}
		return slices.Compare(a.index, b.index)
	})
	var out []jsonField
	for i := 0; i < len(fields); {
		j := i + 1
		for j < len(fields) && fields[j].name == fields[i].name {
			j++
		}
		if j-i == 1 || len(fields[i].index) != len(fields[i+1].index) || fields[i].tag != fields[i+1].tag {
			out = append(out, fields[i])
		}
		i = j
	}
	slices.SortFunc(out, func(a, b jsonField) int { return slices.Compare(a.index, b.index) })
	return out, badNames
}

// typeCount counts types, telling them apart as Go's reflect does.
type typeCount []struct {
	t types.Type
	n int
}

func (c typeCount) get(t types.Type) int {
	for _, e := range c {
		if types.Identical(e.t, t) {
			return e.n
		}
	}
	return 0
}

func (c *typeCount) add(t types.Type) int {
	for i := range *c {
		if types.Identical((*c)[i].t, t) {
			(*c)[i].n++
			return (*c)[i].n
		}
	}
	*c = append(*c, struct {
		t types.Type
		n int
	}{t, 1})
	return 1
}

// validTag is encoding/json's isValidTag: a json tag name made of
// letters, digits and punctuation other than quotes and backslashes.
func validTag(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
}

// foldName is encoding/json's foldName, by which keys match field names
// ignoring case: ASCII letters in upper case, and every other rune as the
// smallest rune of its simple-folding orbit.
func foldName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < utf8.RuneSelf {
			if 'a' <= r && r <= 'z' {
				r -= 'a' - 'A'
			}
		} else {
			r = foldRune(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

func foldRune(r rune) rune {
	for {
		r2 := unicode.SimpleFold(r)
		if r2 <= r {
			return r2
		}
		r = r2
	}
}

// Runes that $jfold folds by itself: the only ones outside ASCII whose
// orbits hold ASCII letters (ſ with s and S, the Kelvin sign with k and K).
const (
	longS  = 'ſ'
	kelvin = 'K'
)

// foldTable adds to F, for each rune of name outside ASCII, the runes of
// its orbit that fold to another, each with the rune it folds to. $jfold
// folds ASCII itself, and a rune no field's orbit holds can match no
// field, so it is left as it is.
func foldTable(name string, F map[rune]rune) {
	for _, r := range name {
		if r < utf8.RuneSelf || r == longS || r == kelvin {
			continue
		}
		to := foldRune(r)
		for m := unicode.SimpleFold(r); ; m = unicode.SimpleFold(m) {
			if m != to && m >= utf8.RuneSelf {
				F[m] = to
			}
			if m == r {
				break
			}
		}
	}
}

// resourceCall emits resource(f(args)) as $resource(args, req, zero): the
// call split in two. args is a function returning the values of f's
// arguments, which $resource evaluates in an effect, as computed does, so
// that a change to a signal they read starts a new request; req is f's
// requester (see requester). zero is T's zero value, Data's until a
// request succeeds.
func (e *emitter) resourceCall(x *ast.CallExpr) string {
	f := ast.Unparen(x.Args[0]).(*ast.CallExpr) // checkResources made sure
	t := e.ck.info.TypeOf(x).(*types.Pointer).Elem().(*types.Named).TypeArgs().At(0)
	return "$resource(() => [" + e.callArgs(f) + "], " + e.requester(f, t) + ", " + zeroValue(t) + ")"
}

// requester returns the JS of the function that makes the requests of a
// resource whose argument is the call f, returning a T: called with the
// values of f's arguments, in an array, and an AbortSignal, it returns a
// promise of f's result, or of its error. fetchJSON[T] is the only
// requester today; Phase 3's typed server functions are to be others.
func (e *emitter) requester(f *ast.CallExpr, t types.Type) string {
	if id, ok := callee(e.ck.info, ast.Unparen(f.Fun)).(*ast.Ident); ok && e.ck.preludeFunc(e.ck.info.Uses[id], "fetchJSON") {
		return "$fetchJSON($jt, " + strconv.Itoa(e.jsonDesc(t)) + ")"
	}
	e.errAt(f, "internal error: resource has no requester for this call; please report")
	return "undefined"
}

// jsonTable holds the program's JSON descriptors, which emit writes as
// the JS array $jt: one for each type that fetchJSON decodes into or that
// such a type holds. $jdec decodes JSON into the type that $jt[i]
// describes. A descriptor is an object with k, its kind: "s" string, "b"
// bool, "i" signed and "u" unsigned integer and "f" float of b bits; "l"
// slice and "a" array (of n) of the type $jt[e]; "m" map from $jt[K] to
// $jt[e]; "o" struct, with s, its name, z, a function returning its zero
// value, and f, its fields as [key, folded key, JS path, type, Go path
// when promoted], in breadth-first order; and F, when its keys have
// letters outside ASCII, the table $jfold folds them by (foldTable). t is
// the type as den names it in errors.
type jsonTable struct {
	types []types.Type
	js    []string
}

// jsonDesc returns the index in $jt of the descriptor of t, adding it
// and the descriptors it refers to when they are not there yet.
func (e *emitter) jsonDesc(t types.Type) int {
	t = types.Unalias(t)
	for i, s := range e.json.types {
		if types.Identical(s, t) {
			return i
		}
	}
	i := len(e.json.types)
	e.json.types = append(e.json.types, t) // first, for the types that hold t
	e.json.js = append(e.json.js, "")
	e.json.js[i] = e.jsonDescJS(t)
	return i
}

// jsonBits are the sizes of Go's number kinds, as encoding/json checks
// them: int, uint and uintptr are 64 bits, as on 64-bit platforms.
var jsonBits = map[types.BasicKind]int{
	types.Int: 64, types.Int8: 8, types.Int16: 16, types.Int32: 32, types.Int64: 64,
	types.Uint: 64, types.Uint8: 8, types.Uint16: 16, types.Uint32: 32, types.Uint64: 64, types.Uintptr: 64,
	types.Float32: 32, types.Float64: 64,
}

func (e *emitter) jsonDescJS(t types.Type) string {
	ts := ", t: " + jsString(errorf("%s", t))
	switch u := t.Underlying().(type) {
	case *types.Basic:
		info := u.Info()
		switch {
		case info&types.IsString != 0:
			return `{k: "s"` + ts + `}`
		case info&types.IsBoolean != 0:
			return `{k: "b"` + ts + `}`
		case info&types.IsFloat != 0:
			return fmt.Sprintf(`{k: "f"%s, b: %d}`, ts, jsonBits[u.Kind()])
		case info&types.IsUnsigned != 0:
			return fmt.Sprintf(`{k: "u"%s, b: %d}`, ts, jsonBits[u.Kind()])
		default:
			return fmt.Sprintf(`{k: "i"%s, b: %d}`, ts, jsonBits[u.Kind()])
		}
	case *types.Slice:
		return fmt.Sprintf(`{k: "l"%s, e: %d}`, ts, e.jsonDesc(u.Elem()))
	case *types.Array:
		return fmt.Sprintf(`{k: "a"%s, e: %d, n: %d}`, ts, e.jsonDesc(u.Elem()), u.Len())
	case *types.Map:
		return fmt.Sprintf(`{k: "m"%s, K: %d, e: %d}`, ts, e.jsonDesc(u.Key()), e.jsonDesc(u.Elem()))
	}
	// A struct, the only other type checkResources lets through.
	name := ""
	if _, ok := t.(*types.Named); ok {
		name = errorf("%s", t)
	}
	F := map[rune]rune{}
	var fields []string
	// $jdec takes the first field whose key matches ignoring case: the
	// first in breadth-first order, as Go's encoding/json does since it is
	// built on json/v2 (before, the first in field order).
	jfs, _ := jsonFields(t)
	slices.SortFunc(jfs, func(a, b jsonField) int { return a.id - b.id })
	for _, f := range jfs {
		var path, goPath []string
		in := t
		for _, i := range f.index {
			fv := in.Underlying().(*types.Struct).Field(i)
			path = append(path, jsString(fieldName(fv.Name())))
			goPath = append(goPath, fv.Name())
			in = fv.Type()
		}
		entry := fmt.Sprintf("[%s, %s, [%s], %d", jsString(f.name), jsString(foldName(f.name)), strings.Join(path, ", "), e.jsonDesc(f.v.Type()))
		if len(goPath) > 1 {
			// Go names a promoted field in errors by the embedded fields
			// it is reached through, then its key.
			entry += ", " + jsString(strings.Join(append(goPath[:len(goPath)-1], f.name), "."))
		}
		fields = append(fields, entry+"]")
		foldTable(f.name, F)
	}
	s := fmt.Sprintf(`{k: "o"%s, s: %s, z: () => %s, f: [%s]`, ts, jsString(name), zeroValue(t), strings.Join(fields, ", "))
	if len(F) > 0 {
		var fs []string
		for from, to := range F {
			fs = append(fs, jsString(string(from))+": "+jsString(string(to)))
		}
		sort.Strings(fs)
		s += ", F: {" + strings.Join(fs, ", ") + "}"
	}
	return s + "}"
}

// out returns the JS declaring $jt, or "" when the program decodes no
// JSON.
func (j *jsonTable) out() string {
	if len(j.js) == 0 {
		return ""
	}
	return "const $jt = [\n  " + strings.Join(j.js, ",\n  ") + ",\n];\n"
}
