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
// go/types. The markup below them is parsed here.
package compiler

import (
	"fmt"
	"go/ast"
	"go/parser"
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
}

// Param is one component parameter (prop).
type Param struct{ Name, Type string }

// Node is a markup node: *Element, *Text or *Interp.
type Node interface{ pos() Pos }

// Element is an HTML element or a component use (capitalised tag).
type Element struct {
	Tag      string
	Pos      Pos
	Attrs    []*Attr
	Children []Node
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

func (e *Element) pos() Pos { return e.Pos }
func (t *Text) pos() Pos    { return t.Pos_ }
func (i *Interp) pos() Pos  { return i.Pos_ }

// IsComponent reports whether the tag names a component rather than HTML.
func (e *Element) IsComponent() bool {
	r := []rune(e.Tag)
	return len(r) > 0 && unicode.IsUpper(r[0])
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

func (p *parser_) parseComponent() *Component {
	if !strings.HasPrefix(p.src[p.off:], "component") || isLetter(p.peekAt(len("component"))) {
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
	markup := p.findMarkupStart(bodyStart)
	if markup < 0 {
		p.failAt(c.NamePos, "component %s has no markup; end it with one root element", c.Name)
	}
	c.StmtsSrc = p.src[bodyStart:markup]
	c.StmtsPos = p.posAt(bodyStart)
	p.off = markup
	c.Root = p.parseElement(false)
	p.skipSpaceAndComments()
	if p.peek() == '<' {
		p.fail("component %s must have exactly one root element; wrap these in a <div>", c.Name)
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
			out = append(out, Param{Name: n.Name, Type: typ})
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

// findMarkupStart finds the first line in the body that starts with `<tag`.
// Everything before it is Go statements.
func (p *parser_) findMarkupStart(from int) int {
	i := from
	lineStart := true
	inStr := byte(0)
	for i < len(p.src) {
		c := p.src[i]
		if inStr != 0 {
			if c == '\\' && inStr != '`' {
				i += 2
				continue
			}
			if c == inStr {
				inStr = 0
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
			return i
		case c == '"' || c == '`' || c == '\'':
			inStr = c
		case c == '/' && i+1 < len(p.src) && p.src[i+1] == '/':
			for i < len(p.src) && p.src[i] != '\n' {
				i++
			}
			continue
		}
		lineStart = false
		i++
	}
	return -1
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
			return el
		default:
			el.Attrs = append(el.Attrs, p.parseAttr(el))
		}
	}
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
	for {
		if p.eof() {
			p.failAt(el.Pos, "unclosed <%s>", el.Tag)
		}
		rest := p.src[p.off:]
		switch {
		case strings.HasPrefix(rest, "</"):
			closePos := p.posAt(p.off)
			p.off += 2
			name := p.readName()
			if name != el.Tag {
				p.failAt(closePos, "</%s> does not match <%s> opened at line %d", name, el.Tag, el.Pos.Line)
			}
			p.skipSpace()
			p.expect(">")
			return
		case strings.HasPrefix(rest, "<!--"):
			end := strings.Index(rest, "-->")
			if end < 0 {
				p.fail("unterminated <!-- comment")
			}
			p.off += end + 3
		case rest[0] == '<':
			el.Children = append(el.Children, p.parseElement(false))
		case rest[0] == '{':
			pos := p.posAt(p.off)
			src, srcPos := p.readBraced()
			trimmed := strings.TrimSpace(src)
			if trimmed == "" || (strings.HasPrefix(trimmed, "/*") && strings.HasSuffix(trimmed, "*/")) {
				continue
			}
			_ = pos
			el.Children = append(el.Children, &Interp{Src: src, Pos_: srcPos})
		default:
			start := p.off
			for !p.eof() && p.peek() != '<' && p.peek() != '{' {
				p.off++
			}
			if t := normalizeText(p.src[start:p.off]); t != "" {
				el.Children = append(el.Children, &Text{Value: html.UnescapeString(t), Pos_: p.posAt(start)})
			}
		}
	}
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
