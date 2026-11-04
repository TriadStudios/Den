package compiler

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// prelude declares the built-in den API as ordinary Go so go/types can check
// user code against it. None of this code is ever executed.
const prelude = `package app

type Signal[T any] struct{ v T }

func (s *Signal[T]) Get() T             { return s.v }
func (s *Signal[T]) Set(v T)            {}
func (s *Signal[T]) Update(f func(T) T) {}

type Computed[T any] struct{ v T }

func (c *Computed[T]) Get() T { return c.v }

func signal[T any](v T) *Signal[T]     { return &Signal[T]{v: v} }
func computed[T any](v T) *Computed[T] { return &Computed[T]{v: v} }
func itoa(n int) string                { return "" }

type Event struct{}

func (Event) Value() string   { return "" }
func (Event) Checked() bool   { return false }
func (Event) Key() string     { return "" }
func (Event) PreventDefault() {}

type renderable interface {
	~string | ~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~float32 | ~float64 | ~bool
}

func denAttr[T renderable](v T)  {}
func denHandler(f func(e Event)) {}
func denKey[T comparable](v T)   {}
func denMark()                   {}
func denAny(v any)               {}

// stored is a signal kept in localStorage under key, as JSON.
func stored[T any](key string, initial T) *Signal[T] { return &Signal[T]{v: initial} }

// hash is a signal holding location.hash, for simple routing.
func hash() *Signal[string] { return nil }

// denOpaque is the field of den's opaque values (a Transition, a Layout):
// Go cannot compare them, and their zero value is null in JS.
type denOpaque func()
`

// preludeRoute declares the router's typed routes, unless the program
// declares its own route or Route, which then keep their Go meaning.
const preludeRoute = `
// Route is a URL pattern whose params fill a T.
type Route[T any] struct{ p T }

func (r *Route[T]) Matches() bool   { return false }
func (r *Route[T]) Params() T       { return r.p }
func (r *Route[T]) Link(p T) string { return "" }

// route is a typed URL pattern, like route[UserParams]("/users/:id").
func route[T any](pattern string) *Route[T] { return nil }
`

// preludePath declares path, unless the program declares its own.
const preludePath = `
// path is a signal holding location.pathname; Set navigates.
func path() *Signal[string] { return nil }
`

// preludeTransition declares the transitions of enter= and exit=, unless
// the program declares its own Transition. The zero Transition plays
// nothing. For takes a den.Duration, or a string in a program with its
// own den.
const preludeTransition = `
// Transition is how an element enters or leaves: fade(), scale() or
// slide(dir), for 200ms unless .For says otherwise.
type Transition struct{ _ denOpaque }

// For is the transition taking d, a constant like 150ms.
func (t Transition) For(d den.Duration) Transition { return t }

func denEnter(t Transition) {}
func denExit(t Transition)  {}
`

// preludeFade, preludeScale and preludeSlide declare the transitions,
// each unless the program has a name of its own for it.
const (
	preludeFade  = "\nfunc fade() Transition { return Transition{} }\n"
	preludeScale = "\nfunc scale() Transition { return Transition{} }\n"
	preludeSlide = "\nfunc slide(dir string) Transition { return Transition{} }\n"
)

// preludeLayout declares Layout, the value of animate=, unless the program
// declares its own; preludeLayoutFn declares layout() unless it has one.
const preludeLayout = `
// Layout makes a list row's element glide to its new place.
type Layout struct{ _ denOpaque }

func denAnimate(l Layout) {}
`

const preludeLayoutFn = "\nfunc layout() Layout { return Layout{} }\n"

// preludeSpring declares spring, unless the program declares its own.
const preludeSpring = `
// spring follows target with spring physics.
func spring(target float64) *Computed[float64] { return nil }
`

// preludeLife declares den's lifecycle functions and timers, each unless
// the program has a name of its own for it. after and every take the
// Duration of den's package time, which the prelude imports as denTime
// when it declares either (as denTime_ and so on in a program that has a
// denTime of its own, which check writes in their place).
var preludeLife = [][2]string{
	{"onMount", "\n// onMount runs f once the component's nodes are in the document.\nfunc onMount(f func()) {}\n"},
	{"onCleanup", "\n// onCleanup runs f when the component goes away.\nfunc onCleanup(f func()) {}\n"},
	{"after", "\n// after runs f once, d from now, unless stopped first.\nfunc after(d denTime.Duration, f func()) (stop func()) { return nil }\n"},
	{"every", "\n// every runs f every d until stopped.\nfunc every(d denTime.Duration, f func()) (stop func()) { return nil }\n"},
}

// preludeRef declares element refs, unless the program declares its own
// ref or Ref, which then go together, as route and Route do. A Ref is
// used as a *Ref; its own field type makes comparing two Refs an error
// den can word on its own.
const preludeRef = `
// Ref holds the element that ref={r} is on, while it exists.
type Ref struct{ _ denOpaqueRef }

type denOpaqueRef func()

func (r *Ref) Focus()          {}
func (r *Ref) Blur()           {}
func (r *Ref) Select()         {}
func (r *Ref) ScrollIntoView() {}

// ref is a new *Ref, holding no element.
func ref() *Ref { return nil }

func denRef(r *Ref) {}
`

// lifeNames are den's functions that need the component instance calling
// them: they exist only in components, never in top-level functions.
var lifeNames = []string{"onMount", "onCleanup", "after", "every"}

// Context: preludeProvide and preludeUse declare provide and use, each
// unless the program has a name of its own for it. They are checked in
// context.go once Go accepts the program.
const (
	preludeProvide = `
// provide makes v what use[T] finds, for T the type of v, in the rest of
// the component and in everything its markup builds.
func provide[T any](v T) {}
`
	preludeUse = `
// use returns the value of the nearest provide of a T among the
// components that build this one, or the zero T if there is none.
func use[T any]() (v T) { return }
`
)

// preludeResource declares resource and Resource, unless the program
// declares its own resource or Resource: resource returns a *Resource[T],
// so the two come and go together.
const preludeResource = `
// Resource is data loaded asynchronously: resource(fetchJSON[T](url)).
type Resource[T any] struct{ v T }

func (r *Resource[T]) Data() T       { return r.v }
func (r *Resource[T]) Err() error    { return nil }
func (r *Resource[T]) Loading() bool { return false }
func (r *Resource[T]) Reload()       {}

// resource takes a call that returns (T, error), resource(f(args)), and
// makes the request again whenever the signals args read change.
func resource[T any](v T, err error) *Resource[T] { return nil }
`

// preludeFetchJSON declares fetchJSON, unless the program declares its own
// or has neither resource nor Resource of den's, whose argument it is.
const preludeFetchJSON = `
// fetchJSON is a GET of url on the page's origin, decoding the JSON
// response into a T; only as resource's argument.
func fetchJSON[T any](url string) (T, error) { var v T; return v, nil }
`

// preludeDen ends the prelude of a program that imports the built-in
// package den, which is every program that does not declare its own den.
const preludeDen = `
func denText[T renderable | den.Children](v T) {}

// denSlot stands in for the markup a component use passes to a
// den.Children parameter; f holds it, checked in the caller's scope.
func denSlot(f func()) den.Children { return den.Children{} }
`

// preludePlain ends the prelude of a program that declares or imports its
// own den. Such a program cannot use den.Children.
const preludePlain = `
func denText[T renderable](v T) {}
`

const preludeFile = "den_prelude.go"

// denPackage is the built-in package den. Children is a struct that holds
// a func, so Go rejects calling it, comparing it (even with nil) and
// converting to it; outside den only its zero value can be written.
const denPackage = `package den

// Children is markup passed to a component: its default content, or a
// named slot. Markup renders it with {x}.
type Children struct{ _ func() }

// Design token types. A package-level constant of one of them is a token
// that style blocks use as $name: const RadiusLg den.Length = "12px" is
// $radius-lg.
type (
	Color    string
	Length   string
	Duration string
	Font     string
	Token    string
)
`

// isChildren reports whether t is den.Children.
func isChildren(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Name() == "Children" && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "den"
}

// basedOnChildren reports whether t has den.Children's underlying type.
func basedOnChildren(t types.Type) bool {
	s, ok := t.Underlying().(*types.Struct)
	return ok && s.NumFields() == 1 && s.Field(0).Pkg() != nil && s.Field(0).Pkg().Path() == "den"
}

// topLevel returns the names the program's Go declarations declare at the
// top level, and the right-hand sides of its plain (non-generic) aliases.
func topLevel(src string) (map[string]bool, map[string]ast.Expr) {
	names, aliases := map[string]bool{}, map[string]ast.Expr{}
	f, _ := parser.ParseFile(token.NewFileSet(), "", "package app\n"+src, parser.SkipObjectResolution)
	if f == nil {
		return names, aliases // the full parse reports the syntax error
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				names[d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					names[s.Name.Name] = true
					if s.Assign.IsValid() && s.TypeParams == nil {
						aliases[s.Name.Name] = s.Type
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						names[n.Name] = true
					}
				}
			}
		}
	}
	return names, aliases
}

// childrenType reports whether the type expression x is den.Children,
// directly or through the plain aliases. go/types confirms it afterwards.
func childrenType(x ast.Expr, aliases map[string]ast.Expr, depth int) bool {
	switch x := x.(type) {
	case *ast.ParenExpr:
		return childrenType(x.X, aliases, depth)
	case *ast.SelectorExpr:
		pkg, ok := x.X.(*ast.Ident)
		return ok && pkg.Name == "den" && x.Sel.Name == "Children"
	case *ast.Ident:
		if a := aliases[x.Name]; a != nil && depth < len(aliases) {
			return childrenType(a, aliases, depth+1)
		}
	}
	return false
}

// partKind identifies a dynamic piece of markup.
type partKind int

const (
	partText partKind = iota
	partAttr
	partHandler
	partComponent
	partIf
	partFor
	partMotion // enter=, exit= and animate=
	partKey    // {key x { ... }}
	partRef    // ref={r}
	partBind   // bind={x}
)

// part links a markup node to the Go AST that the type checker produced for it.
type part struct {
	kind  partKind
	expr  ast.Expr       // partText, partAttr, partIf (condition), partFor and partKey (key), partBind
	stmts []ast.Stmt     // partHandler
	args  []ast.Expr     // partComponent, in param order
	slots [][]Node       // partComponent: markup for each den.Children param
	rng   *ast.RangeStmt // partFor
	bind  *bindInfo      // partBind
}

// checked is the result of type checking a whole program.
type checked struct {
	fset        *token.FileSet
	info        *types.Info
	pkg         *types.Package
	comps       []*checkedComponent
	funcs       []*ast.FuncDecl // top-level Go functions
	components  map[string]*Component
	readsSignal map[types.Object]bool // functions that may read a signal
	nilTypes    map[*ast.Ident]types.Type
	routes      map[*ast.CallExpr]string  // route[T](...) calls: the pattern to match
	durations   map[*ast.CallExpr]float64 // Transition.For(d) calls: d in milliseconds
	ctx         contextInfo               // provide and use (context.go)
}

type checkedComponent struct {
	c     *Component
	decl  *ast.FuncDecl
	stmts []ast.Stmt
	parts map[Node]*part // keyed by *Interp, *If, *For, *Key or *Element (component use)
	attrs map[*Attr]*part
}

func lineDirective(file string, p Pos) string {
	return fmt.Sprintf("/*line %s:%d:%d*/", file, p.Line, p.Col)
}

// check builds one Go file from every component and type-checks it.
func check(files []*File) (*checked, error) {
	var errs ErrorList
	byName := map[string]*Component{}
	for _, f := range files {
		for _, c := range f.Components {
			if prev, dup := byName[c.Name]; dup {
				errs = append(errs, &Error{File: c.File, Pos: c.NamePos,
					Msg: fmt.Sprintf("component %s already declared in %s", c.Name, prev.File)})
				continue
			}
			byName[c.Name] = c
		}
	}

	// Imports must come first in the generated file, so they are lifted out
	// of the declarations (keeping positions) and written after the package
	// clause.
	var declSrc strings.Builder
	var declFrags []fragment // at offsets in declSrc
	imports := map[string]bool{}
	var importSrc strings.Builder
	for _, f := range files {
		for _, d := range f.Decls {
			code, imps, err := screenDecl(f.Name, d)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			for _, im := range imps {
				if !imports[im.spec] {
					imports[im.spec] = true
					fmt.Fprintf(&importSrc, "import %s%s\n", lineDirective(f.Name, im.pos), im.spec)
				}
			}
			declSrc.WriteString("\n" + lineDirective(f.Name, d.Pos))
			declFrags = append(declFrags, fragment{start: declSrc.Len(), end: declSrc.Len() + len(code), last: -1,
				src: code, file: f.Name, pos: d.Pos, what: "this Go declaration", must: "complete"})
			declSrc.WriteString(code + "\n")
		}
	}
	// own holds the names the program gives a meaning of its own at the top
	// level, through a declaration, a component or an import, and says how.
	// Den's built-in den, route, Route and path give way to them, so they
	// keep meaning what they mean in Go.
	top, aliases := topLevel(declSrc.String())
	own := map[string]string{}
	for name := range top {
		own[name] = "declares its own " + name
	}
	for name := range byName {
		own[name] = "has a component named " + name
	}
	for spec := range imports {
		name, path := importName(spec)
		own[name] = "imports " + path + " as " + name
	}
	denTaken := own["den"] != ""
	// route returns a *Route[T], so the two come and go together.
	routeTaken := own["route"] != "" || own["Route"] != ""
	// fade, scale and slide return a Transition and layout a Layout, so
	// they go when the program has its own; otherwise each goes alone.
	// motionBy says which name of the program's took each one away.
	motionBy := map[string]string{}
	for _, name := range []string{"Transition", "fade", "scale", "slide", "Layout", "layout", "spring"} {
		if own[name] != "" {
			motionBy[name] = name
		}
	}
	for _, name := range []string{"fade", "scale", "slide"} {
		if motionBy["Transition"] != "" {
			motionBy[name] = "Transition"
		}
	}
	if motionBy["Layout"] != "" {
		motionBy["layout"] = "Layout"
	}
	// ref returns a *Ref, so the two come and go together, like route and
	// Route; refBy names the program's name that took them, if any.
	refBy := ""
	for _, name := range []string{"Ref", "ref"} {
		if own[name] != "" {
			refBy = name
		}
	}
	// timeName is what the prelude imports den's package time as, for after
	// and every: a name the program does not have at the top level.
	needTime := own["after"] == "" || own["every"] == ""
	timeName := "denTime"
	for own[timeName] != "" {
		timeName += "_"
	}
	// field returns a *Field[T], so the two come and go together.
	fieldTaken := own["field"] != "" || own["Field"] != ""
	resourceBy := resourceNames(own)
	// A prop is den.Children when its type is, directly or through aliases,
	// so gen knows which props take markup before the types are checked.
	for _, f := range files {
		for _, c := range f.Components {
			for i := range c.Params {
				p := &c.Params[i]
				p.Children = !denTaken && childrenType(p.typ, aliases, 0)
				p.hidden = denTaken && childrenType(p.typ, aliases, 0)
			}
		}
	}
	var src strings.Builder
	src.WriteString("package app\n\n")
	if !denTaken {
		src.WriteString("import /*line " + preludeFile + ":1:1*/\"den\"\n")
	}
	if needTime {
		src.WriteString("import /*line " + preludeFile + ":1:1*/" + timeName + " \"time\"\n")
	}
	src.WriteString(importSrc.String())
	// The import directives would otherwise carry on into the prelude.
	src.WriteString("/*line " + preludeFile + ":2:1*/" + strings.TrimPrefix(prelude, "package app\n"))
	if !routeTaken {
		src.WriteString(preludeRoute)
	}
	if own["path"] == "" {
		src.WriteString(preludePath)
	}
	transition := preludeTransition
	if denTaken {
		transition = strings.Replace(transition, "den.Duration", "string", 1)
	}
	for _, d := range [][2]string{{"Transition", transition}, {"fade", preludeFade},
		{"scale", preludeScale}, {"slide", preludeSlide}, {"Layout", preludeLayout},
		{"layout", preludeLayoutFn}, {"spring", preludeSpring}} {
		if motionBy[d[0]] == "" {
			src.WriteString(d[1])
		}
	}
	for _, d := range preludeLife {
		if own[d[0]] == "" {
			src.WriteString(strings.ReplaceAll(d[1], "denTime.", timeName+"."))
		}
	}
	if refBy == "" {
		src.WriteString(preludeRef)
	}
	// Context.
	if own["provide"] == "" {
		src.WriteString(preludeProvide)
	}
	if own["use"] == "" {
		src.WriteString(preludeUse)
	}
	// Forms: field and Field, and bind={x}.
	if !fieldTaken {
		src.WriteString(preludeField)
	}
	src.WriteString(preludeBind)
	if resourceBy["Resource"] == "" {
		src.WriteString(preludeResource)
	}
	if resourceBy["fetchJSON"] == "" {
		src.WriteString(preludeFetchJSON)
	}
	if denTaken {
		src.WriteString(preludePlain)
	} else {
		src.WriteString(preludeDen)
	}
	g := &gen{src: &src, byName: byName, errs: &errs, own: own, motionBy: motionBy, refBy: refBy}
	for _, fr := range declFrags {
		fr.start += src.Len()
		fr.end += src.Len()
		g.frags = append(g.frags, fr)
	}
	src.WriteString(declSrc.String())
	for _, f := range files {
		for _, c := range f.Components {
			g.function(c)
		}
	}
	// Keep going after markup errors so the type checker can report the rest.
	code := src.String()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, preludeFile, code, parser.SkipObjectResolution)
	if err != nil {
		if list, ok := err.(scanner.ErrorList); ok {
			for _, e := range list {
				errs = append(errs, &Error{File: e.Pos.Filename, Pos: Pos{e.Pos.Line, e.Pos.Column}, Msg: e.Msg})
			}
			return nil, errs.sorted()
		}
		return nil, err
	}
	// Go must read what gen wrote around the program's Go as gen wrote it,
	// or the pairer would pair the wrong statements; and that Go would be
	// type-checked as something else.
	if err := g.checkShapes(code, fset, file); err != nil {
		return nil, append(errs, err).sorted()
	}

	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	var typeErrs []types.Error
	conf := types.Config{
		Importer: stdImporter{fset: fset, pkgs: map[string]*types.Package{}},
		Error:    func(err error) { typeErrs = append(typeErrs, err.(types.Error)) },
	}
	pkg, _ := conf.Check("app", fset, []*ast.File{file}, info)
	errAt := func(pos token.Pos, msg string) {
		p := fset.Position(pos)
		errs = append(errs, &Error{File: p.Filename, Pos: Pos{p.Line, p.Column}, Msg: msg})
	}
	where := func(pos token.Pos) string {
		p := fset.Position(pos)
		return fmt.Sprintf("%s:%d:%d", p.Filename, p.Line, p.Column)
	}
	// In a program with its own den, den.Children names that den. Say so
	// instead of Go's error about it.
	var replaced [][2]token.Pos
	if denTaken && len(typeErrs) > 0 {
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Children" {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || id.Name != "den" {
				return true
			}
			switch o := info.Uses[id].(type) {
			case *types.PkgName:
				errAt(sel.Pos(), fmt.Sprintf("den.Children is not available because this program imports %q as den at %s; rename that import", o.Imported().Path(), where(o.Pos())))
			case types.Object:
				if o.Parent() != pkg.Scope() {
					return true // a local den
				}
				errAt(sel.Pos(), fmt.Sprintf("den.Children is not available because this program declares its own den at %s; rename that declaration", where(o.Pos())))
			default:
				return true
			}
			replaced = append(replaced, [2]token.Pos{sel.Pos(), sel.End()})
			return true
		})
	}
	// The prelude's import of package time is in the same file as the
	// program, but not the program's: Go would not know the name. Say so,
	// instead of any error Go has about what the program selects from it.
	timeUses := map[*ast.Ident]bool{}
	for id, obj := range info.Uses {
		if pn, ok := obj.(*types.PkgName); ok && pn.Name() == timeName && fset.Position(pn.Pos()).Filename == preludeFile && fset.Position(id.Pos()).Filename != preludeFile {
			timeUses[id] = true
			errAt(id.Pos(), "undefined: "+timeName)
			replaced = append(replaced, [2]token.Pos{id.Pos(), id.End()})
		}
	}
	if len(timeUses) > 0 {
		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				for id := range timeUses {
					if sel.Pos() == id.Pos() {
						replaced = append(replaced, [2]token.Pos{sel.Pos(), sel.End()})
					}
				}
			}
			return true
		})
	}
next:
	for _, te := range typeErrs {
		for _, r := range replaced {
			if te.Pos >= r[0] && te.Pos < r[1] {
				continue next
			}
		}
		msg := te.Msg
		if i := strings.Index(msg, " does not satisfy renderable"); i >= 0 {
			msg = "cannot render " + msg[:i] + ": only strings, numbers and bools can be shown in markup"
		}
		// A program with its own route or Route has neither of den's. Say why.
		if name, ok := strings.CutPrefix(msg, "undefined: "); ok && routeTaken && (name == "route" || name == "Route") {
			by := "route"
			if own[by] == "" {
				by = "Route"
			}
			msg = fmt.Sprintf("%s is not available because this program %s; rename it", name, own[by])
		}
		if name, ok := strings.CutPrefix(msg, "undefined: "); ok && motionBy[name] != "" {
			msg = fmt.Sprintf("%s is not available because this program %s; rename it", name, own[motionBy[name]])
		}
		if name, ok := strings.CutPrefix(msg, "undefined: "); ok && refBy != "" && (name == "ref" || name == "Ref") {
			msg = fmt.Sprintf("%s is not available because this program %s; rename it", name, own[refBy])
		}
		// Forms: likewise for field and Field, and the value of bind=.
		if name, ok := strings.CutPrefix(msg, "undefined: "); ok && fieldTaken && (name == "field" || name == "Field") {
			by := "field"
			if own[by] == "" {
				by = "Field"
			}
			msg = fmt.Sprintf("%s is not available because this program %s; rename it", name, own[by])
		}
		msg = strings.Replace(msg, " in call to denBind", " in bind=", 1)
		if name, ok := strings.CutPrefix(msg, "undefined: "); ok && resourceBy[name] != "" {
			msg = fmt.Sprintf("%s is not available because this program %s; rename it", name, own[resourceBy[name]])
		}
		msg = preludeInfer(msg)
		msg = strings.Replace(msg, "struct containing denOpaque cannot be compared", "a Transition or Layout cannot be compared", 1)
		msg = strings.Replace(msg, "struct containing denOpaqueRef cannot be compared", "a Ref cannot be compared", 1)
		msg = strings.Replace(msg, "struct containing time.opaque cannot be compared", "den cannot compare times; compare t.UnixMilli() values, or test t.Sub(u) == 0", 1)
		// Go's message would point into den's prelude.
		if own["use"] == "" && strings.HasPrefix(msg, "in call to use, cannot infer T") {
			msg = "use needs the type of the value it finds, as in use[Theme]()"
		}
		// The value of enter= is checked as an argument to denEnter, and
		// that of ref= as one to denRef.
		for _, a := range append(motionAttrs[:len(motionAttrs):len(motionAttrs)], "ref") {
			msg = strings.Replace(msg, " in argument to den"+strings.ToUpper(a[:1])+a[1:], " in "+a+"=", 1)
		}
		errAt(te.Pos, msg)
	}
	// gen took a prop for den.Children by reading its type; go/types must
	// agree, or the markup passed to it would be handled the wrong way.
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || byName[fd.Name.Name] == nil {
			continue
		}
		params := byName[fd.Name.Name].Params
		i := 0
		for _, field := range fd.Type.Params.List {
			for _, n := range field.Names {
				obj := info.Defs[n]
				if obj != nil && i < len(params) && isChildren(obj.Type()) != params[i].Children && obj.Type() != types.Typ[types.Invalid] {
					errAt(n.Pos(), fmt.Sprintf("den cannot tell whether %s is a den.Children prop; write its type as den.Children or as a plain alias of it (type X = den.Children)", n.Name))
				}
				i++
			}
		}
	}
	// A new type made from den.Children would be neither markup nor data.
	for id, obj := range info.Defs {
		if tn, ok := obj.(*types.TypeName); ok && !tn.IsAlias() && basedOnChildren(tn.Type()) {
			errAt(id.Pos(), fmt.Sprintf("type %s is based on den.Children, which den does not support; declare an alias instead: type %s = den.Children", id.Name, id.Name))
		}
	}
	// Route patterns and params, and the constants motion needs, are
	// checked once Go accepts the program.
	var routes map[*ast.CallExpr]string
	var durations map[*ast.CallExpr]float64
	var ctx contextInfo
	if len(typeErrs) == 0 {
		routes = checkRoutes(file, info, pkg, fset, errAt)
		durations = checkMotion(file, info, fset, errAt)
		checkLifecycle(file, info, fset, byName, errAt)
		preludeValues(file, info, fset, byName, errAt)
		ctx = checkContext(file, info, pkg, fset, byName, errAt)
		checkResources(file, info, pkg, fset, byName, own, errAt)
	}
	// Map keys become JS Map keys, which compare like Go only for basic types.
	badKey := map[token.Pos]bool{}
	for x, tv := range info.Types {
		if m, ok := tv.Type.(*types.Map); ok && tv.IsType() && !badKey[x.Pos()] {
			if _, basic := m.Key().Underlying().(*types.Basic); !basic {
				badKey[x.Pos()] = true
				p := fset.Position(x.Pos())
				errs = append(errs, &Error{File: p.Filename, Pos: Pos{p.Line, p.Column},
					Msg: errorf("map keys must be strings, numbers or bools, not %s", m.Key())})
			}
		}
	}
	if len(errs) > 0 {
		return nil, errs.sorted()
	}

	out := &checked{fset: fset, info: info, pkg: pkg, components: byName, readsSignal: map[types.Object]bool{}, nilTypes: nilTargets(file, info), routes: routes, durations: durations, ctx: ctx}
	decls := map[string]*ast.FuncDecl{}
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv != nil {
			continue
		}
		switch {
		case byName[fd.Name.Name] != nil:
			decls[fd.Name.Name] = fd
		case fset.Position(fd.Pos()).Filename != preludeFile:
			out.funcs = append(out.funcs, fd)
		}
	}
	// A function reads a signal if its body does, directly or through
	// another function. Iterate until nothing changes.
	for changed := true; changed; {
		changed = false
		for _, fd := range out.funcs {
			if obj := info.Defs[fd.Name]; !out.readsSignal[obj] && out.reactive(fd.Body) {
				out.readsSignal[obj] = true
				changed = true
			}
		}
	}
	for _, f := range files {
		for _, c := range f.Components {
			fd := decls[c.Name]
			cc := &checkedComponent{c: c, decl: fd, parts: map[Node]*part{}, attrs: map[*Attr]*part{}}
			pr := &pairer{ck: out, cc: cc}
			// The markup's statements follow the denMark() that gen writes
			// after the component's own statements, which may call it too.
			var body []ast.Stmt
			if fd != nil && fd.Body != nil {
				body = fd.Body.List
			}
			mark := -1
			for i := len(body) - 1; i >= 0; i-- {
				if isMark(body[i]) {
					mark = i
					break
				}
			}
			if mark < 0 {
				pr.internal(c.NamePos, "the Go den wrote for component %s has no denMark()", c.Name)
			} else {
				cc.stmts = body[:mark]
				pr.done(c.Root.Pos, pr.node(c.Root, body[mark+1:]))
			}
			errs = append(errs, pr.errs...)
			out.comps = append(out.comps, cc)
		}
	}
	if len(errs) > 0 {
		return nil, errs.sorted()
	}
	return out, nil
}

// preludeValues reports den's built-in functions used other than by
// calling them, as in f := slide. They exist only as calls (each call of
// slide has its direction checked, and signal(v) is $signal(v) in JS).
func preludeValues(file *ast.File, info *types.Info, fset *token.FileSet, byName map[string]*Component, errAt func(token.Pos, string)) {
	called := map[*ast.Ident]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if id, ok := callee(info, ast.Unparen(call.Fun)).(*ast.Ident); ok {
				called[id] = true
			}
		}
		return true
	})
	for id, obj := range info.Uses {
		f, ok := obj.(*types.Func)
		if !ok || called[id] || f.Signature().Recv() != nil || byName[f.Name()] != nil ||
			fset.Position(f.Pos()).Filename != preludeFile || fset.Position(id.Pos()).Filename == preludeFile {
			continue
		}
		errAt(id.Pos(), fmt.Sprintf("%s can only be called, not used as a value", id.Name))
	}
}

// nilTargets finds the type each nil is converted to. go/types records nil
// as untyped, but den needs to know which nils are slices.
func nilTargets(file *ast.File, info *types.Info) map[*ast.Ident]types.Type {
	out := map[*ast.Ident]types.Type{}
	set := func(x ast.Expr, t types.Type) {
		if id, ok := ast.Unparen(x).(*ast.Ident); ok && id.Name == "nil" && t != nil {
			out[id] = t
		}
	}
	var stack []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		switch n := n.(type) {
		case *ast.AssignStmt:
			if len(n.Lhs) == len(n.Rhs) {
				for i, r := range n.Rhs {
					set(r, info.TypeOf(n.Lhs[i]))
				}
			}
		case *ast.ValueSpec:
			for i, v := range n.Values {
				if i < len(n.Names) {
					set(v, info.TypeOf(n.Names[i]))
				}
			}
		case *ast.CallExpr:
			sig, ok := info.TypeOf(n.Fun).(*types.Signature)
			if !ok {
				if tv := info.Types[n.Fun]; tv.IsType() && len(n.Args) == 1 {
					set(n.Args[0], tv.Type) // conversion
				}
				return true
			}
			for i, a := range n.Args {
				ps := sig.Params()
				switch {
				case sig.Variadic() && i >= ps.Len()-1 && !n.Ellipsis.IsValid():
					set(a, ps.At(ps.Len()-1).Type().(*types.Slice).Elem())
				case i < ps.Len():
					set(a, ps.At(i).Type())
				}
			}
		case *ast.ReturnStmt:
			for i := len(stack) - 1; i >= 0; i-- {
				var sig *types.Signature
				switch f := stack[i].(type) {
				case *ast.FuncLit:
					sig, _ = info.TypeOf(f).(*types.Signature)
				case *ast.FuncDecl:
					if obj := info.Defs[f.Name]; obj != nil {
						sig, _ = obj.Type().(*types.Signature)
					}
				default:
					continue
				}
				if sig != nil && sig.Results().Len() == len(n.Results) {
					for j, r := range n.Results {
						set(r, sig.Results().At(j).Type())
					}
				}
				break
			}
		case *ast.CompositeLit:
			switch u := info.TypeOf(n).Underlying().(type) {
			case *types.Slice:
				for _, el := range n.Elts {
					set(el, u.Elem())
				}
			case *types.Array:
				for _, el := range n.Elts {
					set(el, u.Elem())
				}
			case *types.Struct:
				for i, el := range n.Elts {
					if kv, ok := el.(*ast.KeyValueExpr); ok {
						for j := 0; j < u.NumFields(); j++ {
							if u.Field(j).Name() == kv.Key.(*ast.Ident).Name {
								set(kv.Value, u.Field(j).Type())
							}
						}
					} else if i < u.NumFields() {
						set(el, u.Field(i).Type())
					}
				}
			}
		}
		return true
	})
	return out
}

type importSpec struct {
	spec string // `"strings"` or `name "strings"`
	pos  Pos
}

// screenDecl rejects top-level Go that den does not support before go/parser
// sees it, so the errors are about den rather than about the generated file.
// It returns the declarations with their imports blanked out (so positions
// stay put) and the imports themselves.
func screenDecl(file string, d *GoDecl) (string, []importSpec, *Error) {
	fset := token.NewFileSet()
	f := fset.AddFile("", fset.Base(), len(d.Src))
	var s scanner.Scanner
	s.Init(f, []byte(d.Src), nil, 0)
	depth := 0
	prev := token.SEMICOLON
	code := []byte(d.Src)
	var imps []importSpec
	posOf := func(pos token.Pos) Pos {
		p := f.Position(pos)
		col := p.Column
		if p.Line == 1 {
			col += d.Pos.Col - 1
		}
		return Pos{d.Pos.Line + p.Line - 1, col}
	}
	blank := func(from, to int) {
		for i := from; i < to; i++ {
			if code[i] != '\n' {
				code[i] = ' '
			}
		}
	}
	for {
		pos, t, lit := s.Scan()
		atStart := prev == token.SEMICOLON
		prev = t
		at := func(msg string) (string, []importSpec, *Error) {
			return "", nil, &Error{File: file, Pos: posOf(pos), Msg: msg}
		}
		switch t {
		case token.EOF:
			return string(code), imps, nil
		case token.LPAREN, token.LBRACK, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACK, token.RBRACE:
			depth--
		case token.IMPORT:
			start := f.Offset(pos)
			grouped := false
			for {
				p2, t2, lit2 := s.Scan()
				switch {
				case t2 == token.LPAREN && !grouped:
					grouped = true
					continue
				case t2 == token.SEMICOLON:
					continue
				case t2 == token.IDENT || t2 == token.PERIOD:
					name := lit2
					if t2 == token.PERIOD || name == "_" {
						return "", nil, &Error{File: file, Pos: posOf(p2), Msg: "dot and blank imports are not supported"}
					}
					p3, t3, lit3 := s.Scan()
					if t3 != token.STRING {
						return "", nil, &Error{File: file, Pos: posOf(p3), Msg: "expected import path"}
					}
					if err := checkImport(file, lit3, posOf(p3)); err != nil {
						return "", nil, err
					}
					imps = append(imps, importSpec{name + " " + lit3, posOf(p2)})
				case t2 == token.STRING:
					if err := checkImport(file, lit2, posOf(p2)); err != nil {
						return "", nil, err
					}
					imps = append(imps, importSpec{lit2, posOf(p2)})
				case t2 == token.RPAREN && grouped:
					grouped = false
				default:
					return "", nil, &Error{File: file, Pos: posOf(p2), Msg: "expected import path"}
				}
				if !grouped {
					p4, _, _ := s.Scan() // the semicolon that ends the import
					blank(start, f.Offset(p4))
					break
				}
			}
			prev = token.SEMICOLON
		case token.PACKAGE:
			return at(".den files have no package clause")
		case token.VAR:
			if depth == 0 {
				return at("package-level variables are not supported yet; keep state in a component")
			}
		case token.FUNC:
			if depth == 0 && atStart {
				if _, next, _ := s.Scan(); next == token.LPAREN {
					return at("methods are not supported yet; use a function that takes the value")
				}
			}
		}
		_ = lit
	}
}

// declaresStyle reports whether the program gives the name style a meaning
// at the top level, through a declaration or an import, as check's own
// map records it. Components never take it: their names are capitalized.
func declaresStyle(files []*File) bool {
	var src strings.Builder
	for _, f := range files {
		for _, d := range f.Decls {
			code, imps, err := screenDecl(f.Name, d)
			if err != nil {
				continue
			}
			for _, im := range imps {
				if name, _ := importName(im.spec); name == "style" {
					return true
				}
			}
			src.WriteString(code + "\n")
		}
	}
	top, _ := topLevel(src.String())
	return top["style"]
}

// importName returns the name an import spec (`"strings"` or `s "strings"`)
// declares, and its quoted path.
func importName(spec string) (name, path string) {
	if name, path, ok := strings.Cut(spec, " "); ok {
		return name, path
	}
	p, _ := strconv.Unquote(spec)
	return p[strings.LastIndex(p, "/")+1:], spec
}

func checkImport(file, lit string, pos Pos) *Error {
	path, _ := strconv.Unquote(lit)
	if stdPackages[path] == "" {
		var names []string
		for p := range stdPackages {
			names = append(names, p)
		}
		sort.Strings(names)
		return &Error{File: file, Pos: pos, Msg: fmt.Sprintf("package %q is not available in den; you can import %s", path, strings.Join(names, ", "))}
	}
	return nil
}

// gen writes the Go that stands in for markup: every {expr} becomes a typed
// call, every handler a func(e Event), every <Card .../> a call to Card, and
// {if} and {for} become real Go if and for statements so their scopes and
// types are Go's own. A {key x { ... }} becomes a block holding denKey(x),
// typed like a {for} key, and then its content.
type gen struct {
	src      *strings.Builder
	file     string
	byName   map[string]*Component
	errs     *ErrorList
	own      map[string]string // check's own: the names the program claims, and how
	motionBy map[string]string // den's motion names the program's own names take away
	refBy    string            // the program's own name that takes ref and Ref away, if any
	chain    []Node            // the {if}, {for} and {key} blocks whose top level is being written
	rows     int               // how many {for} rows the markup being written is in
	frags    []fragment        // the program's Go that gen copied into src, in order
	shapes   []shape           // what gen wrote around it, in order
	funcs    []int             // the offsets of the components' funcs
}

func (g *gen) d(p Pos) string { return lineDirective(g.file, p) }

// A fragment is a piece of the program's own Go that gen copies into the
// file it writes: an {expr}, an attribute value, an if or for header, a
// list key, the parameters or statements of a component, a declaration.
// What gen writes around it must stay as gen wrote it: Go must read the
// fragment as ending where it ends, never closing a bracket gen opened or
// running on, as a comment or literal, into gen's code (checkShapes).
type fragment struct {
	start, end int // its bytes in the file
	last       int // where the last token that starts in it starts, once scanned
	src        string
	file       string
	pos        Pos    // where src starts in file
	what, must string // for errors: "<what> must be <must>"
}

// frag writes src, the program's Go at pos in g.file, after a line
// directive for it, and records it as a fragment for checkShapes.
func (g *gen) frag(pos Pos, src, what, must string) int {
	g.src.WriteString(g.d(pos))
	g.frags = append(g.frags, fragment{start: g.src.Len(), end: g.src.Len() + len(src), last: -1,
		src: src, file: g.file, pos: pos, what: what, must: must})
	g.src.WriteString(src)
	return len(g.frags) - 1
}

// A shape is Go that gen wrote around fragments, from offset at: a call
// such as denText(x), an if or for statement, a {key} block, the call of a
// component, a component's func. check returns nil if go/parser read it as
// gen wrote it, by the offsets of gen's own tokens, which line directives
// in the program's Go do not change, and otherwise what to report.
type shape struct {
	at    int
	check func(ix *goIndex) *blame
}

// A blame is what checkShapes reports for a shape: fragment frag (-1: the
// last one before the shape) at offset off in the file (-1: where frag
// starts), and why, which follows the fragment's name in the message (by
// default, that it must be what it must be and Go reads it otherwise).
type blame struct {
	frag, off int
	why       string
}

// expect adds the shape that starts at offset at and is checked by check.
func (g *gen) expect(at int, check func(ix *goIndex) *blame) {
	g.shapes = append(g.shapes, shape{at, check})
}

// argsBlame is the blame for a fragment that is one argument of a call gen
// wrote, of which Go read n arguments in its place (more when it holds a
// top-level comma), the second of them at offset second, or a ... at
// offset dots (-1: none) after it.
func (g *gen) argsBlame(f, n, second, dots int) *blame {
	switch {
	case n == 0 && commentOnly(g.frags[f].src):
		return &blame{f, -1, "has only comments in it; it must be one Go expression"}
	case n > 1:
		return &blame{f, second, "must be one Go expression, not several"}
	case dots >= 0:
		return &blame{f, dots, "must be one Go expression; ... passes the elements of a slice, which only a variadic prop takes"}
	}
	return &blame{f, -1, ""}
}

// call writes fn(x) as a statement after the line directive d, where x is
// the Go src at pos, a fragment that is what and must be one expression,
// and returns the fragment.
func (g *gen) call(d, fn string, pos Pos, src, what string) int {
	g.src.WriteString("\t" + d)
	at := g.src.Len()
	g.src.WriteString(fn + "(")
	f := g.frag(pos, src, what, "one Go expression")
	rp := g.src.Len()
	g.src.WriteString(")\n")
	g.expect(at, func(ix *goIndex) *blame {
		c := ix.call(at, fn, rp)
		switch {
		case c == nil:
			return &blame{f, -1, ""}
		case len(c.Args) > 1:
			return g.argsBlame(f, len(c.Args), ix.off(c.Args[1].Pos()), -1)
		case len(c.Args) == 0 || c.Ellipsis.IsValid():
			return g.argsBlame(f, len(c.Args), -1, ix.off(c.Ellipsis))
		}
		return nil
	})
	return f
}

// function writes the Go function that stands for component c: its
// parameters, its statements, denMark() and the Go for its markup.
func (g *gen) function(c *Component) {
	g.file = c.File
	g.src.WriteString("\n")
	at := g.src.Len()
	g.funcs = append(g.funcs, at)
	g.src.WriteString("func " + c.Name + "(")
	lp := g.src.Len() - 1
	params := g.frag(c.ParamsPos, c.ParamsSrc, "the parameter list of "+c.Name, "Go parameters")
	rp := g.src.Len()
	g.src.WriteString(") {")
	stmts := g.frag(c.StmtsPos, c.StmtsSrc, "the statements of "+c.Name, "complete Go statements")
	g.src.WriteString("\n\t")
	mark := g.src.Len()
	g.src.WriteString("denMark()\n")
	rb := -1
	g.expect(at, func(ix *goIndex) *blame {
		fd := ix.funcs[at]
		if fd == nil || fd.Recv != nil || fd.Type.TypeParams != nil || fd.Type.Results != nil ||
			ix.off(fd.Type.Params.Opening) != lp || ix.off(fd.Type.Params.Closing) != rp {
			return &blame{params, -1, ""}
		}
		if fd.Body == nil || ix.off(fd.Body.Lbrace) != rp+len(") ") || ix.off(fd.Body.Rbrace) != rb {
			return &blame{stmts, -1, ""}
		}
		// The markup's Go is written after denMark(), which must be a
		// statement of its own: not the end of the component's last one,
		// as in defer denMark() or L: denMark(). Blame that statement.
		last := g.frags[stmts].last
		for _, s := range fd.Body.List {
			if es, ok := s.(*ast.ExprStmt); ok {
				if c, ok := es.X.(*ast.CallExpr); ok && c == ix.call(mark, "denMark", mark+len("denMark(")) && len(c.Args) == 0 {
					return nil
				}
			}
			if off := ix.off(s.Pos()); off < mark {
				last = off
			}
		}
		return &blame{stmts, last, ""}
	})
	g.node(c.Root)
	rb = g.src.Len()
	g.src.WriteString("}\n")
}

// checkShapes makes sure that go/parser read src, the file gen wrote (and
// parsed as file), as gen wrote it around the program's Go. It returns the
// error for the first fragment that Go reads otherwise: one that ends gen's
// code early, by closing a bracket gen opened or with Go such as a label
// that takes gen's next statement in, or that runs on into gen's code as a
// comment, a literal or an open bracket. Parse rejects such Go in markup
// with a better message (fragmentEscape), but the pairer and everything
// after it rely on this check, not on that one.
//
// It goes by the offsets of tokens in src, which line directives in the
// program's Go do not change. First every token: a bracket must be closed
// by a bracket of the code it was opened in (the same fragment, or gen's
// own), and the first token gen wrote after a fragment must be read as a
// token where gen wrote it, not as part of a comment or literal that the
// fragment started. Then the shapes: the calls, statements and functions
// gen wrote must be where gen wrote them, with gen's own brackets.
func (g *gen) checkShapes(src string, fset *token.FileSet, file *ast.File) *Error {
	tf := token.NewFileSet().AddFile("", -1, len(src))
	var s scanner.Scanner
	s.Init(tf, []byte(src), nil, scanner.ScanComments)
	type opened struct {
		tok       token.Token
		frag, off int
	}
	var open []opened
	in, due := 0, 0 // the first fragment not ended before the token; the first whose next token is due
	for {
		pos, tok, _ := s.Scan()
		off := tf.Offset(pos)
		for ; due < len(g.frags); due++ {
			fr := &g.frags[due]
			next := fr.end + len(src[fr.end:]) - len(strings.TrimLeft(src[fr.end:], " \t\r\n"))
			if next > off {
				break
			}
			if next < off {
				return g.escape(due, fr.last, "")
			}
		}
		if tok == token.EOF {
			break
		}
		for in < len(g.frags) && g.frags[in].end <= off {
			in++
		}
		frag := -1
		if in < len(g.frags) && g.frags[in].start <= off {
			frag = in
			g.frags[in].last = off
		}
		switch tok {
		case token.LPAREN, token.LBRACK, token.LBRACE:
			open = append(open, opened{tok, frag, off})
		case token.RPAREN, token.RBRACK, token.RBRACE:
			if len(open) == 0 {
				return g.escapeNear(frag, off)
			}
			top := open[len(open)-1]
			open = open[:len(open)-1]
			if top.frag >= 0 && (top.frag != frag || top.tok != openerOf[tok]) {
				return g.escape(top.frag, top.off, "")
			}
			if top.frag != frag || top.tok != openerOf[tok] {
				return g.escapeNear(frag, off)
			}
		}
	}
	if len(open) > 0 {
		return g.escapeNear(open[0].frag, open[0].off)
	}
	ix := newGoIndex(fset, file, g.funcs)
	for _, sh := range g.shapes {
		if b := sh.check(ix); b != nil {
			if b.frag < 0 {
				return g.escapeNear(-1, sh.at)
			}
			return g.escape(b.frag, b.off, b.why)
		}
	}
	return nil
}

// escapeNear is the error for fragment frag, or if that is -1, for the last
// fragment that starts before offset off.
func (g *gen) escapeNear(frag, off int) *Error {
	if frag < 0 {
		frag = sort.Search(len(g.frags), func(i int) bool { return g.frags[i].start >= off }) - 1
	}
	if frag < 0 {
		return &Error{File: preludeFile, Pos: Pos{1, 1}, Msg: "internal error: den could not read back the Go it wrote; please report"}
	}
	return g.escape(frag, off, "")
}

// escape is the error for fragment frag, at offset off in the file if that
// is in the fragment, or else at its start: by default, that Go reads it
// as going past its end; or why, which follows the fragment's name.
func (g *gen) escape(frag, off int, why string) *Error {
	fr := g.frags[frag]
	pos := fr.pos
	if fr.start <= off && off <= fr.end {
		before := fr.src[:off-fr.start]
		if nl := strings.LastIndexByte(before, '\n'); nl >= 0 {
			pos = Pos{Line: pos.Line + strings.Count(before, "\n"), Col: len(before) - nl}
		} else {
			pos.Col += len(before)
		}
	}
	if why == "" {
		why = "must be " + fr.must + "; Go would read this together with the code den writes around it"
	}
	return &Error{File: fr.file, Pos: pos, Msg: fr.what + " " + why}
}

// goIndex finds the nodes of the components' funcs in a parsed file by the
// offsets of their tokens.
type goIndex struct {
	tf    *token.File
	funcs map[int]*ast.FuncDecl  // by the offset of func
	calls map[int]*ast.CallExpr  // by the offset of (
	ifs   map[int]*ast.IfStmt    // by the offset of if
	fors  map[int]*ast.BlockStmt // the body of a for statement, by the offset of for
	// blocks holds the blocks that are statements of a block, not the
	// body of an if, for or func, by the offset of {.
	blocks map[int]*ast.BlockStmt
}

func newGoIndex(fset *token.FileSet, file *ast.File, funcs []int) *goIndex {
	ix := &goIndex{tf: fset.File(file.Pos()), funcs: map[int]*ast.FuncDecl{}, calls: map[int]*ast.CallExpr{},
		ifs: map[int]*ast.IfStmt{}, fors: map[int]*ast.BlockStmt{}, blocks: map[int]*ast.BlockStmt{}}
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && slices.Contains(funcs, ix.off(fd.Type.Func)) {
			ix.funcs[ix.off(fd.Type.Func)] = fd
			ast.Inspect(fd, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.BlockStmt:
					for _, s := range n.List {
						if b, ok := s.(*ast.BlockStmt); ok {
							ix.blocks[ix.off(b.Lbrace)] = b
						}
					}
				case *ast.CallExpr:
					ix.calls[ix.off(n.Lparen)] = n
				case *ast.IfStmt:
					ix.ifs[ix.off(n.If)] = n
				case *ast.ForStmt:
					ix.fors[ix.off(n.For)] = n.Body
				case *ast.RangeStmt:
					ix.fors[ix.off(n.For)] = n.Body
				}
				return true
			})
		}
	}
	return ix
}

// off returns the offset of p in the file, or -1 for no position.
func (ix *goIndex) off(p token.Pos) int {
	if !p.IsValid() {
		return -1
	}
	return ix.tf.Offset(p)
}

// call returns the call that Go read at offset at as a call of fn, with
// its ( right after fn and its ) at offset rp, or nil if there is none.
func (ix *goIndex) call(at int, fn string, rp int) *ast.CallExpr {
	c := ix.calls[at+len(fn)]
	if c == nil || ix.off(c.Rparen) != rp {
		return nil
	}
	if id, ok := c.Fun.(*ast.Ident); !ok || id.Name != fn || ix.off(id.Pos()) != at {
		return nil
	}
	return c
}

func (g *gen) nodes(ns []Node) {
	for _, n := range ns {
		g.node(n)
	}
}

// body writes the nodes of a body of block b, whose top level they are.
func (g *gen) body(b Node, ns []Node) {
	defer func(c []Node) { g.chain = c }(g.chain)
	g.chain = append(g.chain[:len(g.chain):len(g.chain)], b)
	g.nodes(ns)
}

// inside writes nodes that are at the top level of no block, such as the
// children of an element.
func (g *gen) inside(ns []Node) {
	defer func(c []Node) { g.chain = c }(g.chain)
	g.chain = nil
	g.nodes(ns)
}

func (g *gen) node(n Node) {
	switch n := n.(type) {
	case *Interp:
		g.call(g.d(n.Pos_), "denText", n.Pos_, n.Src, "this {...}")
	case *If:
		g.src.WriteString("\t" + g.d(n.Pos_))
		at := g.src.Len()
		g.src.WriteString("if ")
		cond := g.frag(n.CondPos, n.Cond, "this {if} condition", "one Go expression")
		lb := g.src.Len() + len(" ")
		g.src.WriteString(" {\n")
		rb, elseLb, elseRb := -1, -1, -1
		g.expect(at, func(ix *goIndex) *blame {
			s := ix.ifs[at]
			switch {
			case s == nil || s.Body == nil:
				return &blame{cond, -1, ""}
			case ix.off(s.Body.Lbrace) != lb:
				return &blame{cond, ix.off(s.Body.Lbrace), ""}
			case ix.off(s.Body.Rbrace) != rb:
				return &blame{cond, -1, ""}
			case elseLb < 0 && s.Else == nil:
				return nil
			}
			if e, ok := s.Else.(*ast.BlockStmt); ok && elseLb >= 0 && ix.off(e.Lbrace) == elseLb && ix.off(e.Rbrace) == elseRb {
				return nil
			}
			return &blame{cond, -1, ""}
		})
		g.body(n, n.Then)
		g.src.WriteString("\t")
		rb = g.src.Len()
		if len(n.Else) > 0 {
			g.src.WriteString("} else {\n")
			elseLb = rb + len("} else ")
			g.body(n, n.Else)
			g.src.WriteString("\t")
			elseRb = g.src.Len()
		}
		g.src.WriteString("}\n")
	case *For:
		g.src.WriteString("\t" + g.d(n.Pos_))
		at := g.src.Len()
		g.src.WriteString("for ")
		header := g.frag(n.HeaderPos, n.Header, "this {for} clause", "a Go range clause")
		lb := g.src.Len() + len(" ")
		g.src.WriteString(" {\n")
		rb := -1
		g.expect(at, func(ix *goIndex) *blame {
			body := ix.fors[at]
			switch {
			case body == nil:
				return &blame{header, -1, ""}
			case ix.off(body.Lbrace) != lb:
				return &blame{header, ix.off(body.Lbrace), ""}
			case ix.off(body.Rbrace) != rb:
				return &blame{header, -1, ""}
			}
			return nil
		})
		g.call(g.d(n.KeyPos), "denKey", n.KeyPos, n.Key, "this list key")
		g.rows++
		g.body(n, n.Body)
		g.rows--
		g.src.WriteString("\t")
		rb = g.src.Len()
		g.src.WriteString("}\n")
	case *Key:
		// A block of its own, statement of the block around it: { denKey(x); ... }.
		g.src.WriteString("\t" + g.d(n.Pos_))
		at := g.src.Len()
		g.src.WriteString("{\n")
		rb, key := -1, -1
		g.expect(at, func(ix *goIndex) *blame {
			if b := ix.blocks[at]; b != nil && ix.off(b.Rbrace) == rb {
				return nil
			}
			return &blame{key, -1, ""}
		})
		key = g.call(g.d(n.KeyPos), "denKey", n.KeyPos, n.Key, "this {key} value")
		g.body(n, n.Body)
		g.src.WriteString("\t")
		rb = g.src.Len()
		g.src.WriteString("}\n")
	case *Element:
		if n.IsComponent() {
			if err := g.component(n); err != nil {
				*g.errs = append(*g.errs, err)
			}
			return
		}
		switch n.Tag {
		case "script", "iframe", "object", "embed", "frame":
			*g.errs = append(*g.errs, &Error{File: g.file, Pos: n.Pos, Msg: fmt.Sprintf("<%s> is not allowed in components", n.Tag)})
			return
		}
		for _, a := range n.Attrs {
			if err := validateAttr(g.file, n, a); err != nil {
				*g.errs = append(*g.errs, err)
				continue
			}
			if isMotionAttr(a.Name) {
				if err := g.motionAttr(a); err != nil {
					*g.errs = append(*g.errs, err)
				}
				continue
			}
			if a.Name == "ref" {
				if err := g.refAttr(a); err != nil {
					*g.errs = append(*g.errs, err)
				}
				continue
			}
			if asciiLower(a.Name) == "bind" { // Bind= too, to say why it is wrong
				if err := g.bindAttr(n, a); err != nil {
					*g.errs = append(*g.errs, err)
				}
				continue
			}
			if a.Kind != AttrExpr {
				continue
			}
			if isEventAttr(a.Name) {
				g.handler(a)
			} else {
				g.call(g.d(a.ValuePos), "denAttr", a.ValuePos, a.Value, "this {...}")
			}
		}
		g.inside(n.Children)
	}
}

// handler writes denHandler(func(e Event) { ... }) around the statements
// of the event handler a.
func (g *gen) handler(a *Attr) {
	g.src.WriteString("\t" + g.d(a.Pos))
	at := g.src.Len()
	g.src.WriteString("denHandler(func(e Event) {")
	fn := at + len("denHandler(")
	lb := g.src.Len() - 1
	f := g.frag(a.ValuePos, a.Value, "this {...}", "Go statements")
	g.src.WriteString("\n\t")
	rb := g.src.Len()
	g.src.WriteString("})\n")
	g.expect(at, func(ix *goIndex) *blame {
		if c := ix.call(at, "denHandler", rb+len("}")); c != nil && len(c.Args) == 1 && !c.Ellipsis.IsValid() {
			if fl, ok := c.Args[0].(*ast.FuncLit); ok && ix.off(fl.Type.Func) == fn && ix.off(fl.Body.Lbrace) == lb && ix.off(fl.Body.Rbrace) == rb {
				return nil
			}
		}
		return &blame{f, -1, ""}
	})
}

// motionAttrs are the attributes that animate an element: enter and exit
// take a Transition, animate a Layout.
var motionAttrs = []string{"enter", "exit", "animate"}

func isMotionAttr(name string) bool { return slices.Contains(motionAttrs, name) }

// motionAttr checks what can be checked of an enter, exit or animate
// attribute before types: that its value is Go, that den's Transition or
// Layout is there, and that the attribute is where it can play. It writes
// the call that type-checks the value, denEnter(x) for enter={x}, or
// denAny(x) when it reports an error: the value is still checked, and the
// variables it uses still count as used, so that error is the only one.
// (The pairer, which reads these calls, runs only when gen reported none.)
func (g *gen) motionAttr(a *Attr) *Error {
	bad := func(format string, args ...any) *Error {
		return &Error{File: g.file, Pos: a.Pos, Msg: fmt.Sprintf(format, args...)}
	}
	typ, want := "Transition", "a transition in braces, like "+a.Name+"={fade()}"
	if a.Name == "animate" {
		typ, want = "Layout", "{layout()}: animate={layout()}"
	}
	if a.Kind != AttrExpr {
		return bad("%s takes %s", a.Name, want)
	}
	var err *Error
	inFor := slices.ContainsFunc(g.chain, func(b Node) bool { _, ok := b.(*For); return ok })
	switch by := g.motionBy[typ]; {
	case by != "":
		err = bad("%s is not available because this program %s; rename it", a.Name, g.own[by])
	case a.Name == "animate" && !inFor:
		err = bad("animate works only on an element directly inside a {for} row, whose place it animates when the list changes")
	case len(g.chain) == 0:
		err = bad("%s works only on an element directly inside an {if} branch, a {for} row or a {key} block, which add and remove it", a.Name)
	}
	fn := "den" + strings.ToUpper(a.Name[:1]) + a.Name[1:]
	if err != nil {
		fn = "denAny"
	}
	g.call(g.d(a.ValuePos), fn, a.ValuePos, a.Value, "this {...}")
	return err
}

// refAttr checks what can be checked of ref={r} before types: that its
// value is Go, that den's Ref is there, and that the element is made once
// per component, not once per {for} row. It writes denRef(r), which
// checks that r is a *Ref, or denAny(r) when it reports an error, as
// motionAttr does.
func (g *gen) refAttr(a *Attr) *Error {
	bad := func(format string, args ...any) *Error {
		return &Error{File: g.file, Pos: a.Pos, Msg: fmt.Sprintf(format, args...)}
	}
	if a.Kind != AttrExpr {
		return bad("ref takes a *Ref in braces, like ref={box} where box := ref()")
	}
	var err *Error
	switch {
	case g.refBy != "":
		err = bad("ref is not available because this program %s; rename it", g.own[g.refBy])
	case g.rows > 0:
		err = bad("ref= inside a {for} row: every row would set the same ref")
	}
	fn := "denRef"
	if err != nil {
		fn = "denAny"
	}
	g.call(g.d(a.ValuePos), fn, a.ValuePos, a.Value, "this {...}")
	return err
}

// pairer walks markup in the same order as gen and links each node to the
// checked Go statements that gen wrote for it.
//
// Those statements have the shape gen gave them: check rejects the program
// when Go reads any of the program's Go in markup as ending the code gen
// writes around it, or running on into the code after it (checkShapes).
// The pairer still checks every shape it relies on, and that it pairs
// every statement gen wrote, and reports a mismatch as an internal error
// rather than panicking or pairing the wrong statements.
type pairer struct {
	ck    *checked
	cc    *checkedComponent
	errs  ErrorList
	chain []Node // as in gen
	bad   bool   // an internal error was reported: the rest is not paired
}

// internal reports that the statements gen wrote do not have the shape
// the pairer expects, at the markup at p. Only the first one is reported.
func (pr *pairer) internal(p Pos, format string, args ...any) {
	if !pr.bad {
		pr.bad = true
		pr.errs = append(pr.errs, &Error{File: pr.cc.c.File, Pos: p, Msg: "internal error: " + fmt.Sprintf(format, args...) + "; please report"})
	}
}

// done reports statements left once the nodes that gen wrote them for,
// the markup at p, have all been paired.
func (pr *pairer) done(p Pos, rest []ast.Stmt) {
	if len(rest) > 0 {
		pr.internal(p, "den wrote %d more statements for this markup than it paired", len(rest))
	}
}

// next returns the first of stmts, which gen wrote for the markup at p, as
// an S, and the statements after it. ok is false, with an internal error
// reported, when there is none or it is something else.
func next[S ast.Stmt](pr *pairer, p Pos, stmts []ast.Stmt) (s S, rest []ast.Stmt, ok bool) {
	if pr.bad {
		return s, nil, false
	}
	if len(stmts) == 0 {
		pr.internal(p, "den wrote no statement for this markup")
		return s, nil, false
	}
	if s, ok = stmts[0].(S); !ok {
		pr.internal(p, "den wrote a %T for this markup, not a %T", stmts[0], s)
		return s, nil, false
	}
	return s, stmts[1:], true
}

// call returns the arguments of the call that is the first of stmts, gen's
// fn(args) for the markup at p, where fn is one of fns, and the statements
// after it.
func (pr *pairer) call(p Pos, stmts []ast.Stmt, fns ...string) ([]ast.Expr, []ast.Stmt, bool) {
	es, rest, ok := next[*ast.ExprStmt](pr, p, stmts)
	if !ok {
		return nil, nil, false
	}
	return pr.callOf(p, es.X, fns...), rest, !pr.bad
}

// callOf returns the arguments of x, gen's call fn(args) for the markup at
// p, where fn is one of fns.
func (pr *pairer) callOf(p Pos, x ast.Expr, fns ...string) []ast.Expr {
	call, ok := x.(*ast.CallExpr)
	if !ok {
		pr.internal(p, "den wrote a %T for this markup, not a call of %s", x, fns[0])
		return nil
	}
	if id, ok := call.Fun.(*ast.Ident); !ok || !slices.Contains(fns, id.Name) {
		pr.internal(p, "den wrote a call of %s for this markup, not of %s", types.ExprString(call.Fun), fns[0])
		return nil
	}
	return call.Args
}

// arg returns the only argument of gen's call fn(x) for the markup at p.
func (pr *pairer) arg(p Pos, stmts []ast.Stmt, fns ...string) (ast.Expr, []ast.Stmt, bool) {
	args, rest, ok := pr.call(p, stmts, fns...)
	if !ok {
		return nil, nil, false
	}
	if len(args) != 1 {
		pr.internal(p, "den wrote %d arguments to %s for this markup, not 1", len(args), fns[0])
		return nil, nil, false
	}
	return args[0], rest, true
}

// body pairs the nodes of a body of block b, as gen.body wrote them, with
// all of stmts.
func (pr *pairer) body(b Node, ns []Node, stmts []ast.Stmt) {
	defer func(c []Node) { pr.chain = c }(pr.chain)
	pr.chain = append(pr.chain[:len(pr.chain):len(pr.chain)], b)
	pr.done(b.pos(), pr.nodes(ns, stmts))
}

// inside pairs nodes that are at the top level of no block, as gen.inside
// wrote them.
func (pr *pairer) inside(ns []Node, stmts []ast.Stmt) []ast.Stmt {
	defer func(c []Node) { pr.chain = c }(pr.chain)
	pr.chain = nil
	return pr.nodes(ns, stmts)
}

// motion checks the value of an enter, exit or animate attribute against
// the types, and that some block around the element can change.
func (pr *pairer) motion(el *Element, a *Attr, x ast.Expr) {
	if pr.ck.reactive(x) {
		pr.errAt(x, "%s cannot depend on a signal: den reads it once, when it makes the element", a.Name)
	}
	for _, b := range pr.chain {
		if _, isFor := b.(*For); (isFor || a.Name != "animate") && pr.ck.blockReactive(pr.cc.parts[b]) {
			return
		}
	}
	msg := fmt.Sprintf("%s never plays: the {if}, {for} or {key} around <%s> reads no signal, so it never adds or removes it", a.Name, el.Tag)
	if a.Name == "animate" {
		msg = fmt.Sprintf("animate never plays: the {for} around <%s> reads no signal, so its rows never move", el.Tag)
	}
	pr.errs = append(pr.errs, &Error{File: pr.cc.c.File, Pos: a.Pos, Msg: msg})
}

func (pr *pairer) errAt(n ast.Node, format string, args ...any) {
	p := pr.ck.fset.Position(n.Pos())
	pr.errs = append(pr.errs, &Error{File: p.Filename, Pos: Pos{p.Line, p.Column}, Msg: errorf(format, args...)})
}

// errorf is fmt.Sprintf that prints types the way the user wrote them,
// without the generated package's name.
func errorf(format string, args ...any) string {
	for i, a := range args {
		if t, ok := a.(types.Type); ok {
			args[i] = types.TypeString(t, func(p *types.Package) string {
				if p.Path() == "den" {
					return "den"
				}
				return ""
			})
		}
	}
	return fmt.Sprintf(format, args...)
}

// nodes pairs ns with the statements gen wrote for them at the start of
// stmts, and returns the statements after those.
func (pr *pairer) nodes(ns []Node, stmts []ast.Stmt) []ast.Stmt {
	for _, n := range ns {
		if pr.bad {
			return nil
		}
		stmts = pr.node(n, stmts)
	}
	return stmts
}

func (pr *pairer) node(n Node, stmts []ast.Stmt) []ast.Stmt {
	switch n := n.(type) {
	case *Interp:
		x, rest, ok := pr.arg(n.Pos_, stmts, "denText")
		if !ok {
			return nil
		}
		if isChildren(pr.ck.info.TypeOf(x)) && pr.ck.reactive(x) {
			pr.errAt(x, "den.Children shown in markup cannot depend on a signal; choose between them with {if} instead")
		}
		pr.cc.parts[n] = &part{kind: partText, expr: x}
		return rest
	case *If:
		is, rest, ok := next[*ast.IfStmt](pr, n.Pos_, stmts)
		if !ok {
			return nil
		}
		// Go would run it; den keeps only the condition of a markup if.
		if is.Init != nil {
			pr.errAt(is.Init, "if in markup cannot have an init statement; put it before the markup")
		}
		pr.cc.parts[n] = &part{kind: partIf, expr: is.Cond}
		pr.body(n, n.Then, is.Body.List)
		els, isBlock := is.Else.(*ast.BlockStmt)
		switch {
		case len(n.Else) > 0 && isBlock:
			pr.body(n, n.Else, els.List)
		case len(n.Else) > 0 || is.Else != nil:
			pr.internal(n.Pos_, "den wrote an else of %T for this {if}, which has %d else nodes", is.Else, len(n.Else))
		}
		return rest
	case *For:
		if len(stmts) > 0 {
			if fs, ok := stmts[0].(*ast.ForStmt); ok {
				pr.errAt(fs, "for in markup must range over a slice, array, map or int: {for i, x := range xs key x.ID { ... }}")
				return stmts[1:]
			}
		}
		rs, rest, ok := next[*ast.RangeStmt](pr, n.Pos_, stmts)
		if !ok {
			return nil
		}
		if rs.Tok == token.ASSIGN {
			pr.errAt(rs, "for in markup must declare its variables with :=")
		}
		switch t := pr.ck.info.TypeOf(rs.X).Underlying().(type) {
		case *types.Slice, *types.Array, *types.Map:
		case *types.Basic:
			if t.Info()&types.IsInteger == 0 {
				pr.errAt(rs.X, "for in markup can range over slices, arrays, maps and ints, not %s", t)
			}
		default:
			pr.errAt(rs.X, "for in markup can range over slices, arrays, maps and ints, not %s", pr.ck.info.TypeOf(rs.X))
		}
		key, body, ok := pr.arg(n.KeyPos, rs.Body.List, "denKey")
		if !ok {
			return nil
		}
		if _, ok := pr.ck.info.TypeOf(key).Underlying().(*types.Basic); !ok {
			pr.errAt(key, "list keys must be strings, numbers or bools, not %s", pr.ck.info.TypeOf(key))
		}
		pr.cc.parts[n] = &part{kind: partFor, expr: key, rng: rs}
		pr.body(n, n.Body, body)
		return rest
	case *Key:
		// gen wrote { denKey(x); content }.
		bs, rest, ok := next[*ast.BlockStmt](pr, n.Pos_, stmts)
		if !ok {
			return nil
		}
		key, body, ok := pr.arg(n.KeyPos, bs.List, "denKey")
		if !ok {
			return nil
		}
		if _, ok := pr.ck.info.TypeOf(key).Underlying().(*types.Basic); !ok {
			pr.errAt(key, "{key} values must be strings, numbers or bools, not %s", pr.ck.info.TypeOf(key))
		}
		pr.cc.parts[n] = &part{kind: partKey, expr: key}
		pr.body(n, n.Body, body)
		return rest
	case *Element:
		if n.IsComponent() {
			return pr.component(n, stmts)
		}
		for _, a := range n.Attrs {
			if a.Kind != AttrExpr {
				continue
			}
			fn := "denAttr"
			switch {
			case isEventAttr(a.Name):
				fn = "denHandler"
			case isMotionAttr(a.Name):
				fn = "den" + strings.ToUpper(a.Name[:1]) + a.Name[1:]
			case a.Name == "ref":
				fn = "denRef"
			case a.Name == "bind":
				fn = "denBind"
			}
			arg, rest, ok := pr.arg(a.Pos, stmts, fn)
			if !ok {
				return nil
			}
			stmts = rest
			switch {
			case isEventAttr(a.Name):
				fl, ok := arg.(*ast.FuncLit)
				if !ok {
					pr.internal(a.Pos, "den wrote a %T for this handler, not a function", arg)
					return nil
				}
				pr.cc.attrs[a] = &part{kind: partHandler, stmts: fl.Body.List}
			case isMotionAttr(a.Name):
				pr.cc.attrs[a] = &part{kind: partMotion, expr: arg}
				pr.motion(n, a, arg)
			case a.Name == "ref":
				pr.cc.attrs[a] = &part{kind: partRef, expr: arg}
				if pr.ck.reactive(arg) {
					pr.errAt(arg, "ref= cannot depend on a signal: den reads it once, when it makes the element")
				}
			case a.Name == "bind":
				pr.cc.attrs[a] = &part{kind: partBind, expr: arg}
			default:
				pr.cc.attrs[a] = &part{kind: partAttr, expr: arg}
			}
		}
		// bind is checked once the element's other attributes are paired:
		// a radio's value too.
		for _, a := range n.Attrs {
			if pt := pr.cc.attrs[a]; pt != nil && pt.kind == partBind {
				pt.bind = pr.bind(n, pt.expr)
			}
		}
		return pr.inside(n.Children, stmts)
	}
	return stmts
}

// component pairs the use of a component, el, with the call of it that gen
// wrote first in stmts, and the markup of its den.Children props with the
// closures gen passed for them.
func (pr *pairer) component(el *Element, stmts []ast.Stmt) []ast.Stmt {
	args, rest, ok := pr.call(el.Pos, stmts, el.Tag)
	if !ok {
		return nil
	}
	c := pr.ck.components[el.Tag]
	if c == nil {
		pr.internal(el.Pos, "den paired a call of %s, which is no component", el.Tag)
		return nil
	}
	params := c.Params
	if len(args) != len(params) {
		pr.internal(el.Pos, "den wrote %d arguments to %s, which has %d props", len(args), el.Tag, len(params))
		return nil
	}
	pt := &part{kind: partComponent, args: args}
	for i, p := range params {
		pt.slots = append(pt.slots, nil)
		if !p.Children {
			continue
		}
		slot := pr.callOf(el.Pos, args[i], "denSlot")
		nodes, filled := slotContent(el, p)
		if pr.bad || len(slot) != 1 {
			pr.internal(el.Pos, "den wrote %d arguments to denSlot for prop %s of %s, not 1", len(slot), p.Name, el.Tag)
			return nil
		}
		if !filled {
			continue // gen wrote denSlot(nil)
		}
		// gen wrote denSlot(func() { ... }) around the markup.
		fn, ok := slot[0].(*ast.FuncLit)
		if !ok {
			pr.internal(el.Pos, "den wrote a %T for the markup of prop %s of %s, not a function", slot[0], p.Name, el.Tag)
			return nil
		}
		pt.slots[i] = nodes
		pr.done(el.Pos, pr.inside(nodes, fn.Body.List))
	}
	pr.cc.parts[el] = pt
	return rest
}

// blockReactive reports whether the {if}, {for} or {key} with part pt
// reads a signal, so that its content can change.
func (ck *checked) blockReactive(pt *part) bool {
	if pt.kind == partFor {
		return ck.reactive(pt.rng.X)
	}
	return ck.reactive(pt.expr)
}

func isMark(s ast.Stmt) bool {
	es, ok := s.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == "denMark"
}

// reactive reports whether evaluating n may read a signal, i.e. whether a DOM
// binding for it needs an effect. Decided at compile time from types. Calls
// through function values are assumed to read signals.
func (ck *checked) reactive(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := callee(ck.info, ast.Unparen(call.Fun)).(type) {
		case *ast.SelectorExpr:
			// A route's Matches and Params read the path signal, and a
			// resource's Data, Err and Loading read its state's signals.
			switch recv := ck.recvName(fun); fun.Sel.Name {
			case "Get":
				found = recv == "Signal" || recv == "Computed"
			case "Matches", "Params":
				found = recv == "Route"
			case "Data", "Err", "Loading":
				found = recv == "Resource"
			}
			// A field's Get, Error and Touched read its signals.
			switch fun.Sel.Name {
			case "Get", "Error", "Touched":
				found = found || ck.isFieldMethod(fun)
			}
		case *ast.Ident:
			switch o := ck.info.Uses[fun].(type) {
			case *types.Var:
				found = true
			case *types.Func:
				found = ck.readsSignal[o]
			}
		}
		return true
	})
	return found
}

// recvName returns the type name of a method call's receiver.
func (ck *checked) recvName(sel *ast.SelectorExpr) string {
	s := ck.info.Selections[sel]
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

// isPrelude reports whether obj is declared by den's built-in prelude.
func (ck *checked) isPrelude(obj types.Object) bool {
	return ck.components[obj.Name()] == nil && ck.fset.Position(obj.Pos()).Filename == preludeFile
}

func isEventAttr(name string) bool {
	return len(name) > 2 && strings.HasPrefix(name, "on") && name[2] >= 'a' && name[2] <= 'z'
}

var urlAttrs = map[string]bool{"href": true, "src": true, "action": true, "formaction": true, "poster": true, "cite": true, "xlink:href": true}

// validateAttr enforces the security rules that can be decided at compile time.
func validateAttr(file string, el *Element, a *Attr) *Error {
	bad := func(format string, args ...any) *Error {
		return &Error{File: file, Pos: a.Pos, Msg: fmt.Sprintf(format, args...)}
	}
	lname := strings.ToLower(a.Name)
	switch {
	case lname == "innerhtml" || lname == "outerhtml" || lname == "srcdoc" || strings.HasPrefix(lname, "dangerously"):
		return bad("%q is not allowed: den never injects raw HTML", a.Name)
	case isEventAttr(a.Name) && a.Kind != AttrExpr:
		return bad("event handler %q must be Go code in {...}; inline JS strings are not allowed", a.Name)
	case strings.HasPrefix(lname, "on") && !isEventAttr(a.Name):
		return bad("unknown event attribute %q (use lowercase, e.g. onclick)", a.Name)
	case urlAttrs[lname] && a.Kind == AttrStatic:
		if !safeStaticURL(a.Value) {
			return bad("unsafe URL in %s: only http, https, mailto, tel and relative URLs are allowed", a.Name)
		}
	}
	return nil
}

func safeStaticURL(u string) bool {
	s := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, u)
	s = strings.TrimLeft(s, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x0b\x0c\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f ")
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return true
	}
	scheme := strings.ToLower(s[:i])
	for _, r := range scheme {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.') {
			return true // ':' is not part of a scheme, e.g. "/path?a=b:c"
		}
	}
	switch scheme {
	case "http", "https", "mailto", "tel":
		return true
	}
	return false
}

// component turns <Counter label="x" start={1} /> into Counter("x", 1)
// with arguments in parameter order, so go/types checks every prop. The
// markup for a den.Children parameter becomes denSlot(func() { ... }), so
// it is checked in the caller's scope; an unfilled one is denSlot(nil).
func (g *gen) component(el *Element) *Error {
	bad := func(p Pos, format string, args ...any) *Error {
		return &Error{File: g.file, Pos: p, Msg: fmt.Sprintf(format, args...)}
	}
	target, ok := g.byName[el.Tag]
	if !ok {
		return bad(el.Pos, "unknown component <%s>", el.Tag)
	}
	param := func(name string) (Param, bool) {
		for _, p := range target.Params {
			if p.Name == name {
				return p, true
			}
		}
		return Param{}, false
	}
	for _, p := range target.Params {
		if p.hidden {
			return bad(el.Pos, "<%s> cannot be used: its prop %q is den.Children, which this program's own den hides", el.Tag, p.Name)
		}
	}
	if p, ok := param("children"); len(el.Children) > 0 && !(ok && p.Children) {
		return bad(el.Pos, "<%s> does not take children: it has no children den.Children parameter", el.Tag)
	}
	for _, s := range el.Slots {
		if p, ok := param(s.Tag[1:]); !ok || !p.Children {
			return bad(s.Pos, "<%s> has no slot %q", el.Tag, s.Tag[1:])
		}
	}
	given := map[string]*Attr{}
	for _, a := range el.Attrs {
		p, ok := param(a.Name)
		switch {
		case !ok && isMotionAttr(a.Name):
			return bad(a.Pos, "%s works on HTML and SVG elements, not on components: <%s> has no prop %q", a.Name, el.Tag, a.Name)
		case !ok && a.Name == "ref":
			return bad(a.Pos, "ref works on HTML and SVG elements, not on components: <%s> has no prop %q; pass the *Ref as a prop and put ref= on an element inside", el.Tag, a.Name)
		case !ok && a.Name == "bind":
			return bad(a.Pos, "bind works on <input>, <textarea> and <select>, not on components: <%s> has no prop \"bind\"", el.Tag)
		case !ok:
			return bad(a.Pos, "<%s> has no prop %q", el.Tag, a.Name)
		case p.Children && p.Name == "children":
			return bad(a.Pos, "children of <%s> go between <%s> and </%s>, not in an attribute", el.Tag, el.Tag, el.Tag)
		case p.Children:
			return bad(a.Pos, "slot %q of <%s> is filled with <:%s>...</:%s> between the tags, not with an attribute", a.Name, el.Tag, a.Name, a.Name)
		}
		given[a.Name] = a
	}
	for _, p := range target.Params {
		if _, ok := given[p.Name]; !ok && !p.Children {
			return bad(el.Pos, "<%s> is missing prop %q (%s)", el.Tag, p.Name, p.Type)
		}
	}
	g.src.WriteString("\t" + g.d(el.Pos))
	at := g.src.Len()
	g.src.WriteString(el.Tag + "(")
	// Each argument is a fragment (frag >= 0) or Go of gen's own at off.
	type arg struct{ frag, off int }
	var args []arg
	rp := -1
	g.expect(at, func(ix *goIndex) *blame {
		// in returns the fragment of these arguments that holds offset
		// off, or -1.
		in := func(off int) int {
			for _, a := range args {
				if a.frag >= 0 && g.frags[a.frag].start <= off && off < g.frags[a.frag].end {
					return a.frag
				}
			}
			return -1
		}
		// near blames the fragment of argument i, or of the last one
		// before it that is a fragment.
		near := func(i, off int) *blame {
			for j := min(i, len(args)-1); j >= 0; j-- {
				if args[j].frag >= 0 {
					return &blame{args[j].frag, off, ""}
				}
			}
			return &blame{-1, -1, ""}
		}
		c := ix.call(at, el.Tag, rp)
		if c == nil {
			return near(len(args)-1, -1)
		}
		for i, a := range args {
			if i >= len(c.Args) {
				// Go read fewer: argument i has only comments.
				if a.frag >= 0 {
					return g.argsBlame(a.frag, 0, -1, -1)
				}
				return near(i, -1)
			}
			off := ix.off(c.Args[i].Pos())
			switch {
			case a.frag >= 0 && in(off) == a.frag, a.frag < 0 && off == a.off:
			case in(off) >= 0:
				// Argument i starts in an earlier prop, which holds two.
				return g.argsBlame(in(off), 2, off, -1)
			default:
				return near(i, -1)
			}
		}
		if len(c.Args) > len(args) {
			off := ix.off(c.Args[len(args)].Pos())
			if f := in(off); f >= 0 {
				return g.argsBlame(f, 2, off, -1)
			}
			return near(len(args)-1, -1)
		}
		// A variadic prop can take xs... as its value.
		if c.Ellipsis.IsValid() {
			if off := ix.off(c.Ellipsis); len(args) == 0 || in(off) < 0 || in(off) != args[len(args)-1].frag {
				return near(len(args)-1, off)
			}
		}
		return nil
	})
	for i, p := range target.Params {
		if i > 0 {
			g.src.WriteString(", ")
		}
		if p.Children {
			args = append(args, arg{-1, g.src.Len()})
			nodes, ok := slotContent(el, p)
			if !ok {
				g.src.WriteString("denSlot(nil)")
				continue
			}
			g.slot(nodes)
			continue
		}
		a := given[p.Name]
		switch a.Kind {
		case AttrStatic:
			g.src.WriteString(g.d(a.ValuePos))
			args = append(args, arg{-1, g.src.Len()})
			g.src.WriteString(strconv.Quote(a.Value))
		case AttrBool:
			g.src.WriteString(g.d(a.Pos))
			args = append(args, arg{-1, g.src.Len()})
			g.src.WriteString("true")
		case AttrExpr:
			args = append(args, arg{g.frag(a.ValuePos, a.Value, "this {...}", "one Go expression"), -1})
		}
	}
	rp = g.src.Len()
	g.src.WriteString(")\n")
	return nil
}

// slot writes denSlot(func() { ... }) around nodes, the markup passed to a
// den.Children prop.
func (g *gen) slot(nodes []Node) {
	at := g.src.Len()
	g.src.WriteString("denSlot(func() {\n")
	fn := at + len("denSlot(")
	lb := g.src.Len() - len("\n") - 1
	rb := -1
	g.expect(at, func(ix *goIndex) *blame {
		if c := ix.call(at, "denSlot", rb+len("}")); c != nil && len(c.Args) == 1 && !c.Ellipsis.IsValid() {
			if fl, ok := c.Args[0].(*ast.FuncLit); ok && ix.off(fl.Type.Func) == fn && ix.off(fl.Body.Lbrace) == lb && ix.off(fl.Body.Rbrace) == rb {
				return nil
			}
		}
		return &blame{-1, -1, ""}
	})
	g.inside(nodes)
	g.src.WriteString("\t")
	rb = g.src.Len()
	g.src.WriteString("})")
}
