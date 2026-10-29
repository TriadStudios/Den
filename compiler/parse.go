// Package compiler turns .den component files into small JavaScript.
//
// A .den file holds one or more components:
//
//	component Counter(label string, start int) {
//	    count := signal(start)
//	    <button onclick={count.Set(count.Get() + 1)}>{label}: {count.Get()}</button>
//	}
//
// The Go statements at the top of a component body are real Go, checked by
// go/types. The markup below them is parsed here. Plain Go declarations
// (types, constants and functions) may sit between components.
package compiler

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"html"
	"sort"
	"strings"
	"unicode"
)

// Pos is a 1-based line/column inside a .den file.
type Pos struct{ Line, Col int }

// Error is a compile error pointing into a .den file.
type Error struct {
	File string
	Pos  Pos
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Pos.Line, e.Pos.Col, e.Msg)
}

// ErrorList collects every error from one compile.
type ErrorList []*Error

func (l ErrorList) Error() string {
	var b strings.Builder
	for i, e := range l {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.Error())
	}
	return b.String()
}

func (l ErrorList) sorted() ErrorList {
	sort.SliceStable(l, func(i, j int) bool {
		if l[i].File != l[j].File {
			return l[i].File < l[j].File
		}
		if l[i].Pos.Line != l[j].Pos.Line {
			return l[i].Pos.Line < l[j].Pos.Line
		}
		return l[i].Pos.Col < l[j].Pos.Col
	})
	return l
}

// File is one parsed .den file.
type File struct {
	Name       string
	Components []*Component
	Decls      []*GoDecl
}

// GoDecl is plain Go source between components.
type GoDecl struct {
	Src string
	Pos Pos
}

// Component is one `component Name(params) { ... }` block.
type Component struct {
	File      string
	Name      string
	NamePos   Pos
	ParamsSrc string
	ParamsPos Pos
	Params    []Param
	StmtsSrc  string
	StmtsPos  Pos
	Root      *Element
	Style     *Style // the component's style block, if it has one
}

// Style is the CSS of a component's `style { ... }` block, between its
// braces. It is cut out of StmtsSrc (blanked, keeping every newline), so
// Go positions stay exact.
type Style struct {
	Src string
	Pos Pos // the position of Src[0]
}

// Param is one component parameter (prop). Children is set by the checker
// when its type is den.Children: the parameter named children takes the
// element's default content, and any other is a slot filled by <:name>.
type Param struct {
	Name, Type string
	Children   bool
	typ        ast.Expr
	hidden     bool // its type is den.Children, but the program has its own den
}

// Node is a markup node: *Element, *Text, *Interp, *If or *For.
type Node interface{ pos() Pos }

// Element is an HTML element, a component use (capitalised tag) or a
// <:name> slot. For a component use, Children is the default content and
// Slots holds the <:name> elements given directly inside it.
type Element struct {
	Tag      string
	Pos      Pos
	Attrs    []*Attr
	Children []Node
	Slots    []*Element
}

// AttrKind says how an attribute's value was written.
type AttrKind int

const (
	AttrStatic AttrKind = iota // name="text"
	AttrExpr                   // name={expr}
	AttrBool                   // name
)

// Attr is one attribute on an element.
type Attr struct {
	Name     string
	Pos      Pos
	Kind     AttrKind
	Value    string
	ValuePos Pos
}

// Text is literal text between tags.
type Text struct {
	Value string
	Pos_  Pos
}

// Interp is a `{expr}` inside markup.
type Interp struct {
	Src  string
	Pos_ Pos
}

// If is `{if cond { ... } else { ... }}` in markup. An `else if` is stored
// as an Else holding a single *If.
type If struct {
	Pos_    Pos
	Cond    string
	CondPos Pos
	Then    []Node
	Else    []Node
}

// For is `{for i, x := range xs key x.ID { ... }}` in markup.
type For struct {
	Pos_      Pos
	Header    string // "i, x := range xs"
	HeaderPos Pos
	Key       string
	KeyPos    Pos
	Body      []Node
}

func (e *Element) pos() Pos { return e.Pos }
func (t *Text) pos() Pos    { return t.Pos_ }
func (i *Interp) pos() Pos  { return i.Pos_ }
func (n *If) pos() Pos      { return n.Pos_ }
func (n *For) pos() Pos     { return n.Pos_ }

// IsComponent reports whether the tag names a component rather than HTML.
func (e *Element) IsComponent() bool {
	r := []rune(e.Tag)
	return len(r) > 0 && unicode.IsUpper(r[0])
}

// IsSlot reports whether the element is a <:name> slot.
func (e *Element) IsSlot() bool { return strings.HasPrefix(e.Tag, ":") }

// slotContent returns the markup a component use gives for its den.Children
// parameter p, and whether it gives any.
func slotContent(el *Element, p Param) ([]Node, bool) {
	if p.Name == "children" {
		return el.Children, len(el.Children) > 0
	}
	for _, s := range el.Slots {
		if s.Tag[1:] == p.Name {
			return s.Children, true
		}
	}
	return nil, false
}

type parser_ struct {
	file  string
	src   string
	off   int
	lines []int
}

type bailout struct{ err *Error }

// Parse parses a .den source file.
func Parse(name, src string) (f *File, err error) {
	p := &parser_{file: name, src: src}
	p.lines = append(p.lines, 0)
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			p.lines = append(p.lines, i+1)
		}
	}
	defer func() {
		if r := recover(); r != nil {
			b, ok := r.(bailout)
			if !ok {
				panic(r)
			}
			f, err = nil, ErrorList{b.err}
		}
	}()
	f = &File{Name: name}
	seen := map[string]bool{}
	for {
		p.skipSpaceAndComments()
		if p.eof() {
			break
		}
		if !p.atWord("component") {
			f.Decls = append(f.Decls, p.parseGoDecls())
			continue
		}
		c := p.parseComponent()
		if seen[c.Name] {
			p.failAt(c.NamePos, "component %s is declared twice", c.Name)
		}
		seen[c.Name] = true
		f.Components = append(f.Components, c)
	}
	return f, nil
}

func (p *parser_) posAt(off int) Pos {
	i := sort.Search(len(p.lines), func(i int) bool { return p.lines[i] > off }) - 1
	return Pos{Line: i + 1, Col: off - p.lines[i] + 1}
}

func (p *parser_) failAt(pos Pos, format string, args ...any) {
	panic(bailout{&Error{File: p.file, Pos: pos, Msg: fmt.Sprintf(format, args...)}})
}

func (p *parser_) fail(format string, args ...any) {
	p.failAt(p.posAt(p.off), format, args...)
}

func (p *parser_) eof() bool { return p.off >= len(p.src) }

func (p *parser_) peek() byte {
	if p.eof() {
		return 0
	}
	return p.src[p.off]
}

func (p *parser_) peekAt(n int) byte {
	if p.off+n >= len(p.src) {
		return 0
	}
	return p.src[p.off+n]
}

// atWord reports whether the identifier w starts at p.off.
func (p *parser_) atWord(w string) bool {
	if !strings.HasPrefix(p.src[p.off:], w) {
		return false
	}
	c := p.peekAt(len(w))
	return !isLetter(c) && !isDigit(c)
}

func (p *parser_) expect(s string) {
	if !strings.HasPrefix(p.src[p.off:], s) {
		p.fail("expected %q", s)
	}
	p.off += len(s)
}

func (p *parser_) skipSpace() {
	for !p.eof() && isSpace(p.src[p.off]) {
		p.off++
	}
}

func (p *parser_) skipSpaceAndComments() {
	for {
		p.skipSpace()
		rest := p.src[p.off:]
		switch {
		case strings.HasPrefix(rest, "//"):
			if i := strings.IndexByte(rest, '\n'); i >= 0 {
				p.off += i + 1
			} else {
				p.off = len(p.src)
			}
		case strings.HasPrefix(rest, "/*"):
			i := strings.Index(rest[2:], "*/")
			if i < 0 {
				p.fail("unterminated comment")
			}
			p.off += i + 4
		default:
			return
		}
	}
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func isLetter(c byte) bool {
	return c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

func (p *parser_) readIdent() string {
	start := p.off
	for !p.eof() && (isLetter(p.peek()) || (p.off > start && isDigit(p.peek()))) {
		p.off++
	}
	return p.src[start:p.off]
}

// readName reads a tag or attribute name: letters, digits, '-', ':', '.'.
func (p *parser_) readName() string {
	start := p.off
	for !p.eof() {
		c := p.peek()
		if isLetter(c) || (p.off > start && (isDigit(c) || c == '-' || c == ':' || c == '.')) {
			p.off++
			continue
		}
		break
	}
	return p.src[start:p.off]
}

// parseGoDecls reads plain Go declarations up to the next `component` at the
// start of a line. go/parser and go/types check them later.
func (p *parser_) parseGoDecls() *GoDecl {
	start := p.off
	depth := 0
	lineStart := false
	for !p.eof() {
		c := p.src[p.off]
		switch {
		case c == '\n':
			lineStart = true
			p.off++
			continue
		case c == ' ' || c == '\t' || c == '\r':
			p.off++
			continue
		case lineStart && depth == 0 && p.atWord("component"):
			return &GoDecl{Src: p.src[start:p.off], Pos: p.posAt(start)}
		case p.atLiteral():
			p.skipLiteral()
			lineStart = false
			continue
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		}
		lineStart = false
		p.off++
	}
	return &GoDecl{Src: p.src[start:], Pos: p.posAt(start)}
}

// atLiteral reports whether a Go string, rune or comment starts at p.off.
func (p *parser_) atLiteral() bool {
	c := p.peek()
	return c == '"' || c == '\'' || c == '`' || (c == '/' && (p.peekAt(1) == '/' || p.peekAt(1) == '*'))
}

// skipLiteral moves past the Go string, rune or comment at p.off.
func (p *parser_) skipLiteral() {
	start := p.posAt(p.off)
	rest := p.src[p.off:]
	switch {
	case strings.HasPrefix(rest, "//"):
		if i := strings.IndexByte(rest, '\n'); i >= 0 {
			p.off += i
		} else {
			p.off = len(p.src)
		}
		return
	case strings.HasPrefix(rest, "/*"):
		i := strings.Index(rest[2:], "*/")
		if i < 0 {
			p.failAt(start, "unterminated comment")
		}
		p.off += i + 4
		return
	}
	q := rest[0]
	for i := 1; i < len(rest); i++ {
		switch {
		case rest[i] == '\\' && q != '`':
			i++
		case rest[i] == q:
			p.off += i + 1
			return
		case rest[i] == '\n' && q != '`':
			p.failAt(start, "unterminated literal")
		}
	}
	p.failAt(start, "unterminated literal")
}

func (p *parser_) parseComponent() *Component {
	if !p.atWord("component") {
		p.fail("expected 'component' declaration")
	}
	p.off += len("component")
	p.skipSpace()
	c := &Component{File: p.file, NamePos: p.posAt(p.off)}
	c.Name = p.readIdent()
	if c.Name == "" {
		p.fail("expected component name")
	}
	if !unicode.IsUpper(rune(c.Name[0])) {
		p.failAt(c.NamePos, "component name %q must start with an uppercase letter", c.Name)
	}
	p.skipSpace()
	p.expect("(")
	start := p.off
	depth := 1
	for depth > 0 {
		if p.eof() {
			p.fail("unterminated parameter list")
		}
		switch p.src[p.off] {
		case '(':
			depth++
		case ')':
			depth--
		}
		p.off++
	}
	c.ParamsSrc = p.src[start : p.off-1]
	c.ParamsPos = p.posAt(start)
	c.Params = p.parseParams(c)
	p.skipSpace()
	p.expect("{")
	bodyStart := p.off
	markup, styles := p.findMarkupStart(bodyStart)
	if markup < 0 {
		p.failAt(c.NamePos, "component %s has no markup; end it with one root element", c.Name)
	}
	stmts := []byte(p.src[bodyStart:markup])
	for i, s := range styles {
		if i > 0 {
			p.failAt(p.posAt(s.kw), "component %s has a second style block; put all of its CSS in one", c.Name)
		}
		c.Style = &Style{Src: p.src[s.open+1 : s.end-1], Pos: p.posAt(s.open + 1)}
		for j := s.kw; j < s.end; j++ {
			if stmts[j-bodyStart] != '\n' {
				stmts[j-bodyStart] = ' '
			}
		}
	}
	c.StmtsSrc = string(stmts)
	c.StmtsPos = p.posAt(bodyStart)
	p.off = markup
	c.Root = p.parseElement(false)
	p.skipSpaceAndComments()
	if p.peek() == '<' {
		p.fail("component %s must have exactly one root element; wrap these in a <div>", c.Name)
	}
	if p.styleAt(p.off) {
		p.fail("the style block of %s must come before its markup", c.Name)
	}
	p.expect("}")
	return c
}

// parseParams reuses go/parser so prop lists are real Go parameter lists.
func (p *parser_) parseParams(c *Component) []Param {
	src := "package p; func _(" + c.ParamsSrc + ") {}"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		p.failAt(c.ParamsPos, "invalid parameter list: %v", stripPos(err.Error()))
	}
	fn := f.Decls[0].(*ast.FuncDecl)
	var out []Param
	for _, field := range fn.Type.Params.List {
		typ := src[fset.Position(field.Type.Pos()).Offset:fset.Position(field.Type.End()).Offset]
		if len(field.Names) == 0 {
			p.failAt(c.ParamsPos, "every parameter of %s needs a name", c.Name)
		}
		for _, n := range field.Names {
			out = append(out, Param{Name: n.Name, Type: typ, typ: field.Type})
		}
	}
	return out
}

func stripPos(msg string) string {
	// "1:23: expected ..." -> "expected ..."
	parts := strings.SplitN(msg, ": ", 2)
	if len(parts) == 2 && strings.Contains(parts[0], ":") {
		return parts[1]
	}
	return msg
}

// styleSpan is a style block in a component body: the offsets of its
// style keyword and its '{', and the offset just past its '}'.
type styleSpan struct{ kw, open, end int }

// findMarkupStart finds the first line in the body that starts with `<tag`.
// Everything before it is Go statements, apart from the style blocks it
// also returns: a line that starts a statement at the top of the body with
// `style {` starts one, which no Go statement can. Their CSS is skipped
// with CSS rules for strings and comments, never Go's.
func (p *parser_) findMarkupStart(from int) (int, []styleSpan) {
	i := from
	lineStart := true
	inStr := byte(0)
	depth := 0
	last := byte(';') // the last byte of Go code, outside comments
	var styles []styleSpan
	for i < len(p.src) {
		c := p.src[i]
		if inStr != 0 {
			if c == '\\' && inStr != '`' {
				i += 2
				continue
			}
			if c == inStr {
				inStr = 0
				last = c
			}
			i++
			continue
		}
		switch {
		case c == '\n':
			lineStart = true
			i++
			continue
		case c == ' ' || c == '\t' || c == '\r':
			i++
			continue
		case lineStart && c == '<' && i+1 < len(p.src) && isLetter(p.src[i+1]):
			return i, styles
		case lineStart && depth == 0 && endsStmt(last) && p.styleAt(i):
			s := p.scanStyle(i)
			styles = append(styles, s)
			i, last, lineStart = s.end, '}', false
			continue
		case c == '"' || c == '`' || c == '\'':
			inStr = c
		case c == '/' && i+1 < len(p.src) && p.src[i+1] == '/':
			for i < len(p.src) && p.src[i] != '\n' {
				i++
			}
			continue
		case c == '/' && i+1 < len(p.src) && p.src[i+1] == '*':
			if end := strings.Index(p.src[i+2:], "*/"); end >= 0 {
				i += end + 4
				lineStart = false
				continue
			}
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		}
		last = c
		if (c == '+' || c == '-') && i > from && p.src[i-1] == c {
			last = ';' // x++ and x-- end a statement
		}
		lineStart = false
		i++
	}
	return -1, styles
}

// endsStmt reports whether a line whose last byte of code is c ends a Go
// statement there, as Go's semicolon rule has it.
func endsStmt(c byte) bool {
	return isLetter(c) || isDigit(c) || strings.IndexByte(";)]}\"'`", c) >= 0
}

// styleAt reports whether `style {` starts at off.
func (p *parser_) styleAt(off int) bool {
	if !strings.HasPrefix(p.src[off:], "style") {
		return false
	}
	i := off + len("style")
	for i < len(p.src) && (p.src[i] == ' ' || p.src[i] == '\t') {
		i++
	}
	return i < len(p.src) && p.src[i] == '{'
}

// scanStyle finds the end of the style block at kw. The CSS in it is
// read as CSS: strings, comments and url(...) may hold anything. A line
// that starts with `<tag` is markup, which CSS never has, so a block that
// reaches one was not closed.
func (p *parser_) scanStyle(kw int) styleSpan {
	open := strings.IndexByte(p.src[kw:], '{') + kw
	depth := 0
	lineStart := false
	for i := open; i < len(p.src); {
		switch c := p.src[i]; {
		case lineStart && c == '<' && i+1 < len(p.src) && isLetter(p.src[i+1]):
			p.failAt(p.posAt(kw), "unterminated style block")
		case c == '\n':
			lineStart = true
		case c != ' ' && c != '\t' && c != '\r':
			lineStart = false
		}
		next, err := cssAtom(p.src, i)
		if err != "" {
			p.failAt(p.posAt(i), "%s", err)
		}
		switch p.src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return styleSpan{kw, open, i + 1}
			}
		}
		i = next
	}
	p.failAt(p.posAt(kw), "unterminated style block")
	panic("unreachable")
}

func (p *parser_) parseElement(inSVG bool) *Element {
	el := &Element{Pos: p.posAt(p.off)}
	p.expect("<")
	el.Tag = p.readName()
	if el.Tag == "" {
		p.fail("expected tag name")
	}
	for {
		p.skipSpace()
		switch {
		case p.eof():
			p.failAt(el.Pos, "unclosed <%s>", el.Tag)
		case strings.HasPrefix(p.src[p.off:], "/>"):
			p.off += 2
			return el
		case p.peek() == '>':
			p.off++
			p.parseChildren(el)
			if el.IsComponent() {
				p.splitSlots(el)
			}
			return el
		default:
			el.Attrs = append(el.Attrs, p.parseAttr(el))
		}
	}
}

// parseSlot parses <:name>...</:name>, which fills the slot parameter name
// of the component element parent.
func (p *parser_) parseSlot(parent *Element) *Element {
	el := &Element{Pos: p.posAt(p.off)}
	p.off += 2
	name := p.readIdent()
	if name == "" {
		p.fail("expected a slot name after <:")
	}
	el.Tag = ":" + name
	if parent == nil || !parent.IsComponent() {
		p.failAt(el.Pos, "<:%s> must be directly inside a component element", name)
	}
	if name == "children" {
		p.failAt(el.Pos, "<:children> is not a slot: put default content directly between <%s> and </%s>", parent.Tag, parent.Tag)
	}
	p.skipSpace()
	switch {
	case p.eof():
		p.failAt(el.Pos, "unclosed <%s>", el.Tag)
	case strings.HasPrefix(p.src[p.off:], "/>"):
		p.off += 2
	case p.peek() == '>':
		p.off++
		p.parseChildren(el)
		el.Children = trimBlank(el.Children)
	default:
		p.fail("slot <%s> takes no attributes", el.Tag)
	}
	return el
}

// splitSlots moves the <:name> slots of a component element out of its
// default content. Blank text at the edges of the content is layout.
func (p *parser_) splitSlots(el *Element) {
	var kids []Node
	for _, n := range el.Children {
		s, ok := n.(*Element)
		if !ok || !s.IsSlot() {
			kids = append(kids, n)
			continue
		}
		for _, prev := range el.Slots {
			if prev.Tag == s.Tag {
				p.failAt(s.Pos, "<%s> slot %q given twice", el.Tag, s.Tag[1:])
			}
		}
		el.Slots = append(el.Slots, s)
	}
	el.Children = trimBlank(kids)
}

func trimBlank(ns []Node) []Node {
	blank := func(n Node) bool {
		t, ok := n.(*Text)
		return ok && strings.Trim(t.Value, " \t\r\n") == ""
	}
	for len(ns) > 0 && blank(ns[0]) {
		ns = ns[1:]
	}
	for len(ns) > 0 && blank(ns[len(ns)-1]) {
		ns = ns[:len(ns)-1]
	}
	return ns
}

func (p *parser_) parseAttr(el *Element) *Attr {
	a := &Attr{Pos: p.posAt(p.off)}
	a.Name = p.readName()
	if a.Name == "" {
		p.fail("unexpected %q in <%s>", string(p.peek()), el.Tag)
	}
	for _, other := range el.Attrs {
		if other.Name == a.Name {
			p.failAt(a.Pos, "duplicate attribute %q", a.Name)
		}
	}
	p.skipSpace()
	if p.peek() != '=' {
		a.Kind = AttrBool
		return a
	}
	p.off++
	p.skipSpace()
	switch q := p.peek(); q {
	case '"', '\'':
		p.off++
		a.ValuePos = p.posAt(p.off)
		end := strings.IndexByte(p.src[p.off:], q)
		if end < 0 {
			p.fail("unterminated attribute value")
		}
		a.Kind = AttrStatic
		a.Value = html.UnescapeString(p.src[p.off : p.off+end])
		p.off += end + 1
	case '{':
		a.Kind = AttrExpr
		a.Value, a.ValuePos = p.readBraced()
		if strings.TrimSpace(a.Value) == "" {
			p.failAt(a.Pos, "attribute %q has an empty {}", a.Name)
		}
	default:
		p.fail("attribute values must be \"text\" or {expr}")
	}
	return a
}

func (p *parser_) parseChildren(el *Element) {
	el.Children = p.parseNodes(el, el.Pos)
}

// parseNodes parses markup up to the closing tag of el or, when el is nil, up
// to the '}' that ends a control-flow block opened at open.
func (p *parser_) parseNodes(el *Element, open Pos) []Node {
	var nodes []Node
	for {
		if p.eof() {
			if el != nil {
				p.failAt(el.Pos, "unclosed <%s>", el.Tag)
			}
			p.failAt(open, "unclosed {")
		}
		rest := p.src[p.off:]
		switch {
		case el == nil && rest[0] == '}':
			p.off++
			return nodes
		case strings.HasPrefix(rest, "</"):
			closePos := p.posAt(p.off)
			p.off += 2
			var name string
			if p.peek() == ':' {
				p.off++
				name = ":" + p.readIdent()
			} else {
				name = p.readName()
			}
			if el == nil {
				p.failAt(closePos, "</%s> has no matching open tag inside this block", name)
			}
			if name != el.Tag {
				p.failAt(closePos, "</%s> does not match <%s> opened at line %d", name, el.Tag, el.Pos.Line)
			}
			p.skipSpace()
			p.expect(">")
			return nodes
		case strings.HasPrefix(rest, "<!--"):
			end := strings.Index(rest, "-->")
			if end < 0 {
				p.fail("unterminated <!-- comment")
			}
			p.off += end + 3
		case strings.HasPrefix(rest, "<:"):
			nodes = append(nodes, p.parseSlot(el))
		case rest[0] == '<':
			nodes = append(nodes, p.parseElement(false))
		case rest[0] == '{' && p.controlWord() != "":
			nodes = append(nodes, p.parseControl())
		case rest[0] == '{':
			src, srcPos := p.readBraced()
			trimmed := strings.TrimSpace(src)
			if trimmed == "" || (strings.HasPrefix(trimmed, "/*") && strings.HasSuffix(trimmed, "*/")) {
				continue
			}
			nodes = append(nodes, &Interp{Src: src, Pos_: srcPos})
		default:
			start := p.off
			for !p.eof() && p.peek() != '<' && p.peek() != '{' && (el != nil || p.peek() != '}') {
				p.off++
			}
			t := normalizeText(p.src[start:p.off])
			if el == nil && len(nodes) == 0 {
				t = strings.TrimLeft(t, " ") // space just inside a block's braces is layout
			}
			if el == nil && p.peek() == '}' {
				t = strings.TrimRight(t, " ")
			}
			if t != "" {
				nodes = append(nodes, &Text{Value: html.UnescapeString(t), Pos_: p.posAt(start)})
			}
		}
	}
}

// controlWord returns "if" or "for" when the '{' at p.off opens a
// control-flow block.
func (p *parser_) controlWord() string {
	save := p.off
	defer func() { p.off = save }()
	p.off++
	p.skipSpace()
	for _, w := range []string{"if", "for"} {
		if p.atWord(w) {
			return w
		}
	}
	return ""
}

func (p *parser_) parseControl() Node {
	open := p.posAt(p.off)
	kw := p.controlWord()
	p.off++
	p.skipSpace()
	var n Node
	if kw == "if" {
		n = p.parseIf()
	} else {
		n = p.parseFor()
	}
	p.skipSpace()
	if p.peek() != '}' {
		p.fail("expected } to close the {%s opened at line %d", kw, open.Line)
	}
	p.off++
	return n
}

func (p *parser_) parseIf() *If {
	n := &If{Pos_: p.posAt(p.off)}
	p.off += len("if")
	var off int
	n.Cond, off = p.readHeader("if", n.Pos_)
	n.CondPos = p.posAt(off)
	n.Then = p.parseNodes(nil, n.Pos_)
	p.skipSpace()
	if !p.atWord("else") {
		return n
	}
	p.off += len("else")
	p.skipSpace()
	if p.atWord("if") {
		n.Else = []Node{p.parseIf()}
		return n
	}
	if p.peek() != '{' {
		p.fail("expected { or if after else")
	}
	p.off++
	n.Else = p.parseNodes(nil, n.Pos_)
	return n
}

func (p *parser_) parseFor() *For {
	n := &For{Pos_: p.posAt(p.off)}
	p.off += len("for")
	header, off := p.readHeader("for", n.Pos_)
	head, key, keyOff := splitKey(header)
	if key == "" {
		p.failAt(n.Pos_, "for in markup needs a key so rows can move instead of being rebuilt: {for _, x := range xs key x.ID { ... }}")
	}
	n.Header, n.HeaderPos = head, p.posAt(off)
	n.Key, n.KeyPos = key, p.posAt(off+keyOff)
	n.Body = p.parseNodes(nil, n.Pos_)
	return n
}

// readHeader reads the Go source of an if or for header up to the '{' that
// opens its body, consumes the '{' and returns the source and its offset.
// As in Go, a '{' after a slice, array or map type starts a composite
// literal, and a literal of a named type must be wrapped in parentheses.
func (p *parser_) readHeader(kw string, kwPos Pos) (string, int) {
	p.skipSpace()
	start := p.off
	depth := 0
	for {
		if p.eof() {
			p.failAt(kwPos, "unterminated {%s", kw)
		}
		if p.atLiteral() {
			p.skipLiteral()
			continue
		}
		switch c := p.src[p.off]; {
		case c == '(' || c == '[' || (c == '{' && afterTypeLit(p.src[start:p.off])):
			depth++
		case c == ')' || c == ']' || (c == '}' && depth > 0):
			depth--
		case c == '{' && depth == 0:
			src := strings.TrimRight(p.src[start:p.off], " \t\r\n")
			if src == "" {
				p.failAt(kwPos, "%s needs a condition before {", kw)
			}
			p.off++
			return src, start
		case c == '}' && depth == 0:
			p.fail("expected { to start the %s body", kw)
		}
		p.off++
	}
}

// afterTypeLit reports whether src ends in a slice, array or map type such
// as []T, [3]*T or map[K]V, so that a following '{' starts a literal.
func afterTypeLit(src string) bool {
	i := len(src)
	for i > 0 && (isLetter(src[i-1]) || isDigit(src[i-1])) {
		i--
	}
	if i == len(src) {
		return false
	}
	for i > 0 && src[i-1] == '*' {
		i--
	}
	return i > 0 && src[i-1] == ']'
}

// splitKey splits "i, x := range xs key x.ID" into the range clause and the
// key expression. `key` is not a Go keyword, so it is the identifier "key"
// that sits between the end of one expression and the start of another,
// which no valid Go expression contains.
func splitKey(src string) (head, key string, keyOff int) {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, []byte(src), nil, 0)
	type tok struct {
		t     token.Token
		off   int
		lit   string
		depth int
	}
	var toks []tok
	depth := 0
	for {
		pos, t, lit := s.Scan()
		if t == token.EOF {
			break
		}
		if t == token.SEMICOLON && lit == "\n" {
			continue
		}
		switch t {
		case token.RPAREN, token.RBRACK, token.RBRACE:
			depth--
		}
		toks = append(toks, tok{t, file.Offset(pos), lit, depth})
		switch t {
		case token.LPAREN, token.LBRACK, token.LBRACE:
			depth++
		}
	}
	for i := 1; i+1 < len(toks); i++ {
		k := toks[i]
		if k.depth == 0 && k.t == token.IDENT && k.lit == "key" && endsExpr(toks[i-1].t) && startsExpr(toks[i+1].t) {
			return strings.TrimSpace(src[:k.off]), strings.TrimSpace(src[toks[i+1].off:]), toks[i+1].off
		}
	}
	return src, "", 0
}

func endsExpr(t token.Token) bool {
	switch t {
	case token.IDENT, token.INT, token.FLOAT, token.IMAG, token.CHAR, token.STRING,
		token.RPAREN, token.RBRACK, token.RBRACE, token.INC, token.DEC:
		return true
	}
	return false
}

func startsExpr(t token.Token) bool {
	switch t {
	case token.IDENT, token.INT, token.FLOAT, token.IMAG, token.CHAR, token.STRING,
		token.LPAREN, token.LBRACK, token.NOT, token.SUB, token.ADD, token.XOR,
		token.MUL, token.AND, token.ARROW, token.FUNC, token.STRUCT, token.MAP:
		return true
	}
	return false
}

// readBraced reads `{...}` with nesting and Go string literals; returns the inner text.
func (p *parser_) readBraced() (string, Pos) {
	open := p.posAt(p.off)
	p.expect("{")
	start := p.off
	depth := 1
	for {
		if p.eof() {
			p.failAt(open, "unterminated {")
		}
		c := p.src[p.off]
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				inner := p.src[start:p.off]
				p.off++
				return inner, p.posAt(start)
			}
		case '"', '\'', '`':
			p.off++
			for !p.eof() && p.src[p.off] != c {
				if p.src[p.off] == '\\' && c != '`' {
					p.off++
				}
				p.off++
			}
		}
		p.off++
	}
}

// normalizeText applies JSX-style whitespace rules: whitespace that contains a
// newline is dropped, other runs collapse to one space.
func normalizeText(s string) string {
	if !strings.Contains(s, "\n") {
		return collapse(s)
	}
	lines := strings.Split(s, "\n")
	var kept []string
	for i, l := range lines {
		if i > 0 {
			l = strings.TrimLeft(l, " \t\r")
		}
		if i < len(lines)-1 {
			l = strings.TrimRight(l, " \t\r")
		}
		if strings.TrimSpace(l) != "" {
			kept = append(kept, l)
		}
	}
	return collapse(strings.Join(kept, " "))
}

func collapse(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}
