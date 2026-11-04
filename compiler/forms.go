package compiler

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"
)

// Forms: bind={x} on <input>, <textarea> and <select>, which keeps the
// element and a *Signal[T] or *Field[T] in step both ways, and
// field(initial, check), a value with a check. gen (bindAttr) checks what
// it can before types, the pairer (bind) checks x and T, and emit (bindJS)
// writes the calls of the helpers at the end of stdlib.js.

// preludeField declares Field and field, unless the program declares its
// own field or Field: field returns a *Field[T], so the two go together.
const preludeField = `
// Field is a value with a check, for forms: field(initial, check).
type Field[T any] struct{ v T }

func (f *Field[T]) Get() T        { return f.v }
func (f *Field[T]) Set(v T)       {}
func (f *Field[T]) Error() error  { return nil }
func (f *Field[T]) Touched() bool { return false }
func (f *Field[T]) Touch()        {}
func (f *Field[T]) Reset()        {}

// field is a value with a check, which may be nil.
func field[T any](initial T, check func(T) error) *Field[T] { return &Field[T]{v: initial} }
`

// preludeBind declares the call that stands for bind={x}. The pairer
// checks x itself, so that the errors say what bind takes.
const preludeBind = `
func denBind(x any) {}
`

// bindKind is what a bound element is. It picks the property bind writes,
// the event it listens to and how the element's text becomes a T.
type bindKind int

const (
	bindText     bindKind = iota // <input> of a text type: value / input
	bindArea                     // <textarea>: value / input
	bindNumber                   // type="number" or "range": value / input, parsed
	bindCheckbox                 // checked / change
	bindRadio                    // checked while x == its value / change sets x to it
	bindSelect                   // value / change, the option's value parsed
)

// bindInfo is what the pairer found out about a bind={x}.
type bindInfo struct {
	kind  bindKind
	field bool       // x is a *Field
	t     types.Type // the T of x
	value *Attr      // a radio's value attribute
}

// textTypes are the input types whose value is text that bind takes as a
// string, as written in lower case ("" when there is no type).
var textTypes = []string{"", "text", "search", "email", "url", "tel", "password", "hidden", "color",
	"date", "time", "datetime-local", "month", "week"}

// valuelessTypes are the input types bind cannot work with.
var valuelessTypes = []string{"file", "button", "submit", "reset", "image"}

// attrNamed returns el's attribute named name in any ASCII case, as HTML
// reads names, or nil.
func attrNamed(el *Element, name string) *Attr {
	for _, a := range el.Attrs {
		if asciiLower(a.Name) == name {
			return a
		}
	}
	return nil
}

// bindKindOf returns the kind of el, an element with bind, or why bind
// cannot work on it and the attribute to blame (nil: bind itself).
func bindKindOf(el *Element) (bindKind, *Attr, string) {
	switch asciiLower(el.Tag) {
	case "textarea":
		return bindArea, nil, ""
	case "select":
		if m := attrNamed(el, "multiple"); m != nil {
			return 0, m, "bind does not work on <select multiple>: it binds one value, and such a select has several"
		}
		return bindSelect, nil, ""
	case "input":
	default:
		return 0, nil, fmt.Sprintf("bind works on <input>, <textarea> and <select>, not on <%s>", el.Tag)
	}
	typ := ""
	if a := attrNamed(el, "type"); a != nil {
		switch a.Kind {
		case AttrExpr:
			return 0, a, `bind needs the type of its <input> written out, as in type="number": with type={...} den cannot tell what bind should set`
		case AttrStatic:
			typ = asciiLower(a.Value)
		}
	}
	switch {
	case slices.Contains(textTypes, typ):
		return bindText, nil, ""
	case typ == "number" || typ == "range":
		return bindNumber, nil, ""
	case typ == "checkbox":
		return bindCheckbox, nil, ""
	case typ == "radio":
		return bindRadio, nil, ""
	case slices.Contains(valuelessTypes, typ):
		return 0, nil, fmt.Sprintf(`bind does not work on <input type=%q>, which has no value to bind`, typ)
	}
	return 0, nil, fmt.Sprintf(`bind does not know <input type=%q>; it works with text types, "number", "range", "checkbox" and "radio"`, typ)
}

// bindAttr checks a bind attribute of el before types: that its value is
// Go, that the element can be bound, and that nothing else on it sets
// what bind sets. It writes denBind(x), or denAny(x) when it reports an
// error, so that the variables x uses still count as used.
func (g *gen) bindAttr(el *Element, a *Attr) *Error {
	bad := func(at *Attr, format string, args ...any) *Error {
		if at == nil {
			at = a
		}
		return &Error{File: g.file, Pos: at.Pos, Msg: fmt.Sprintf(format, args...)}
	}
	if a.Kind != AttrExpr {
		return bad(nil, "bind takes a signal or a field in braces, like bind={name}")
	}
	var err *Error
	kind, at, msg := bindKindOf(el)
	if msg != "" {
		err = bad(at, "%s", msg)
	} else {
		prop := "value"
		if kind == bindCheckbox || kind == bindRadio {
			prop = "checked"
		}
		switch both := attrNamed(el, prop); {
		case both != nil:
			err = bad(both, "%s and bind would both set %s: bind sets it, so leave %s out", both.Name, prop, both.Name)
		case kind == bindRadio && attrNamed(el, "value") == nil:
			err = bad(nil, `a radio with bind needs a value, the one bind sets when it is checked: value="pro" or value={v}`)
		case kind == bindArea && len(el.Children) > 0:
			err = bad(nil, "a <textarea> with bind shows the value of bind, so it cannot have content of its own")
		}
	}
	fn := "denBind"
	if err != nil {
		fn = "denAny"
	}
	g.call(g.d(a.ValuePos), fn, a.ValuePos, a.Value, "this {...}")
	return err
}

// bindTarget returns T when t is den's *Signal[T] or *Field[T], and the
// name of den's type t points to (Signal, Field or Computed), if any.
func (ck *checked) bindTarget(t types.Type) (types.Type, string) {
	p, ok := types.Unalias(t).(*types.Pointer)
	if !ok {
		return nil, ""
	}
	n, ok := types.Unalias(p.Elem()).(*types.Named)
	if !ok || n.TypeArgs().Len() != 1 || ck.fset.Position(n.Obj().Pos()).Filename != preludeFile {
		return nil, ""
	}
	switch name := n.Obj().Name(); name {
	case "Signal", "Field":
		return n.TypeArgs().At(0), name
	case "Computed":
		return nil, name
	}
	return nil, ""
}

// bind checks bind={x} on el against the types, and returns what emit
// needs to know, or nil after an error. The value attribute of a radio has
// been paired already.
func (pr *pairer) bind(el *Element, x ast.Expr) *bindInfo {
	t := pr.ck.info.TypeOf(x)
	elem, name := pr.ck.bindTarget(t)
	switch {
	case name == "Computed":
		pr.errAt(x, "bind cannot set a %s: it takes a *Signal or a *Field", t)
		return nil
	case elem == nil:
		pr.errAt(x, "bind takes a *Signal or a *Field, like bind={name}, not %s", t)
		return nil
	case pr.ck.reactive(x):
		pr.errAt(x, "bind cannot depend on a signal: den reads it once, when it makes the element, and binds that")
		return nil
	}
	kind, _, _ := bindKindOf(el)
	if msg := bindFits(kind, el, elem); msg != "" {
		pr.errAt(x, "%s", msg)
		return nil
	}
	b := &bindInfo{kind: kind, field: name == "Field", t: elem}
	if kind != bindRadio {
		return b
	}
	b.value = attrNamed(el, "value")
	if b.value.Kind != AttrExpr {
		if !isString(elem) {
			pr.errAt(x, "a radio bound to %s needs value={...} of type %s: value=%q is a string", article(elem), elem, b.value.Value)
			return nil
		}
		return b
	}
	// The value must be one bind can set x to, by Go's rules: x.Set(v).
	v := pr.cc.attrs[b.value].expr
	set := &ast.SelectorExpr{X: x, Sel: ast.NewIdent("Set")}
	err := types.CheckExpr(pr.ck.fset, pr.ck.pkg, x.Pos(), &ast.CallExpr{Fun: set, Lparen: v.Pos(), Args: []ast.Expr{v}, Rparen: v.End()}, nil)
	var te types.Error
	if errors.As(err, &te) {
		msg := strings.Replace(te.Msg, " in argument to "+types.ExprString(set), " in value=", 1)
		pr.errAt(posNode(te.Pos), "%s, the value of a radio bound to %s", msg, article(elem))
		return nil
	}
	return b
}

// posNode is a node at a position, for errAt.
type posNode token.Pos

func (p posNode) Pos() token.Pos { return token.Pos(p) }
func (p posNode) End() token.Pos { return token.Pos(p) }

// bindFits returns why an element of kind cannot be bound to a T, or "".
func bindFits(kind bindKind, el *Element, t types.Type) string {
	var info types.BasicInfo
	if b, ok := t.Underlying().(*types.Basic); ok {
		info = b.Info()
	}
	str, num, boolean := info&types.IsString != 0, info&(types.IsInteger|types.IsFloat) != 0, info&types.IsBoolean != 0
	switch {
	case !str && !num && !boolean:
		return errorf("bind works with strings, numbers and bools, not %s", t)
	case kind == bindRadio,
		kind == bindText && str, kind == bindArea && str,
		kind == bindNumber && num, kind == bindCheckbox && boolean,
		kind == bindSelect && (str || num):
		return ""
	}
	where := "a text <input> or a <textarea>"
	switch {
	case num:
		where = `type="number" or "range"`
	case boolean:
		where = `type="checkbox"`
	}
	msg := "bind to " + article(t) + " needs " + where
	switch kind {
	case bindArea:
		msg += ", not a <textarea>"
	case bindSelect:
		msg += ", not a <select>"
	case bindNumber, bindCheckbox:
		msg += fmt.Sprintf(", not type=%q", attrNamed(el, "type").Value)
	}
	return msg
}

// article returns t with "a" or "an" before it.
func article(t types.Type) string {
	s := errorf("%s", t)
	if s != "" && strings.ContainsRune("aeioAEIO", rune(s[0])) {
		return "an " + s
	}
	return "a " + s
}

// isFieldMethod reports whether sel calls a method of den's own Field
// (not one promoted from a field of the program's own type).
func (ck *checked) isFieldMethod(sel *ast.SelectorExpr) bool {
	s := ck.info.Selections[sel]
	return s != nil && s.Kind() == types.MethodVal && len(s.Index()) == 1 && ck.recvName(sel) == "Field" &&
		ck.fset.Position(s.Obj().Pos()).Filename == preludeFile
}

// bindEvents returns the events a bind listens to, for $evs.
func bindEvents(b *bindInfo) []string {
	ev := "input"
	if b.kind == bindCheckbox || b.kind == bindRadio || b.kind == bindSelect {
		ev = "change"
	}
	if b.field {
		return []string{ev, "focusout"}
	}
	return []string{ev}
}

// boundRadio reports whether el is a radio with bind, whose value
// attribute bindJS writes.
func (e *emitter) boundRadio(el *Element) bool {
	for _, a := range el.Attrs {
		if pt := e.cc.attrs[a]; pt != nil && pt.kind == partBind && pt.bind.kind == bindRadio {
			return true
		}
	}
	return false
}

// bindJS writes the JS for bind={x} on the element el names. Each helper
// returns x, which $touch then takes for a field, so x is evaluated once.
func (e *emitter) bindJS(el string, pt *part) {
	b, x := pt.bind, e.expr(pt.expr)
	var js string
	switch b.kind {
	case bindCheckbox:
		js = "$check(" + el + ", " + x + ")"
	case bindRadio:
		v := jsString(b.value.Value)
		if b.value.Kind == AttrExpr {
			// The value is evaluated once (or, when it reads signals, by
			// one computed), and the attribute and the radio both use it.
			vx := e.cc.attrs[b.value].expr
			v = e.name("$v")
			if e.ck.reactive(vx) {
				e.line("const %s = $computed(() => %s);", v, e.expr(vx))
				e.line("$attr(%s, \"value\", () => %s.get());", el, v)
			} else {
				e.line("const %s = %s;", v, e.expr(vx))
				e.line("$set(%s, \"value\", %s);", el, v)
			}
		}
		js = "$radio(" + el + ", " + x + ", " + v + ")"
	default:
		ev := "input"
		if b.kind == bindSelect {
			ev = "change"
		}
		js = "$bind(" + el + ", " + x + ", " + jsString(ev) + ", " + textParser(b.t) + ")"
	}
	if b.field {
		js = "$touch(" + el + ", " + js + ")"
	}
	e.line("%s;", js)
}

// textParser returns the JS function that reads an element's text as a t:
// as it is for strings, as strconv.Atoi does for ints (within the range of
// the kind), and as strconv.ParseFloat does a decimal number for floats.
func textParser(t types.Type) string {
	u := t.Underlying().(*types.Basic)
	switch {
	case u.Info()&types.IsString != 0:
		return "$str"
	case u.Info()&types.IsFloat != 0:
		return "$float"
	}
	switch u.Kind() {
	case types.Int8:
		return "$int(-128, 127)"
	case types.Int16:
		return "$int(-32768, 32767)"
	case types.Int32:
		return "$int(-2147483648, 2147483647)"
	case types.Uint8:
		return "$int(0, 255)"
	case types.Uint16:
		return "$int(0, 65535)"
	case types.Uint32:
		return "$int(0, 4294967295)"
	case types.Uint, types.Uint64, types.Uintptr:
		return "$int(0, 9007199254740991)"
	}
	return "$atoi" // int and int64: a safe integer, which $atoi checks
}
