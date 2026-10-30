package compiler

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/constant"
	"go/format"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// den fmt: one canonical layout for .den files, as gofmt has for Go.
//
// Formatting changes only whitespace that the compiler ignores, so a
// formatted program compiles to exactly the same JavaScript and CSS. Go
// code is formatted by go/format and go/printer. Markup is indented with
// tabs, and its line breaks move only where den's text rules make them
// meaningless: whitespace with a newline in it at the edge of a text run is
// dropped, and inside one it is a single space, so such whitespace can
// become any other whitespace with a newline. A space without a newline
// next to a tag or a {...} is text and stays a space. Style blocks get one
// declaration per line, with whitespace runs collapsed to one space.
//
// Format checks its own work: it parses what it wrote and compares it with
// what it read, and returns an error rather than a changed program.

const (
	fmtWidth    = 100 // the line width markup aims for
	fmtCSSWidth = 80  // a selector list longer than this gets a line per selector
	fmtTab      = 4   // columns a tab counts for when measuring lines
)

// normalizeNumbers is go/format's printer mode that canonicalizes number
// literal prefixes and exponents (0X1F becomes 0x1F), as gofmt does.
const normalizeNumbers printer.Mode = 1 << 30

var goPrinter = printer.Config{Mode: printer.UseSpaces | printer.TabIndent | normalizeNumbers, Tabwidth: 8}

// Format formats the .den source src, the contents of the file filename,
// and returns the result, with LF line endings. A file that does not parse
// gets the parser's errors, with their positions.
func Format(filename string, src []byte) (_ []byte, err error) {
	s := string(src)
	orig, err := Parse(filename, s)
	if err != nil {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			err = ErrorList{{File: filename, Pos: Pos{1, 1}, Msg: fmt.Sprintf("internal error: den fmt failed: %v; please report", r)}}
		}
	}()
	f := &formatter{file: filename, src: s, p: newParser(filename, s), cache: map[fragKey]string{}}
	out, err := f.format()
	if err != nil {
		return nil, err
	}
	if err := sameMeaning(filename, orig, out); err != nil {
		return nil, err
	}
	return []byte(out), nil
}

type formatter struct {
	file  string
	src   string
	p     *parser_
	errs  ErrorList
	cache map[fragKey]string
}

type fragKey struct {
	kind   byte
	off    int
	indent int
}

func (f *formatter) errAt(off int, msg string) {
	pos := f.p.posAt(min(off, len(f.src)))
	for _, e := range f.errs {
		if e.Pos == pos && e.Msg == msg {
			return
		}
	}
	f.errs = append(f.errs, &Error{File: f.file, Pos: pos, Msg: msg})
}

// piece is a component or the Go between components, formatted, with the
// offsets of what it covers of the source.
type piece struct {
	start, end int
	text       string
	comp       bool
}

func (f *formatter) format() (string, error) {
	p := f.p
	var pieces []piece
	region := 0
	for {
		p.skipSpaceAndComments()
		if p.eof() {
			break
		}
		if !p.atWord("component") {
			p.parseGoDecls()
			continue
		}
		pieces = f.goRegion(pieces, region, p.off)
		c := (&lparser{p}).component()
		pieces = append(pieces, piece{c.off, c.end, f.component(c), true})
		region = c.end
	}
	pieces = f.goRegion(pieces, region, len(f.src))
	if len(f.errs) > 0 {
		return "", f.errs.sorted()
	}
	var b strings.Builder
	for i, pc := range pieces {
		if i > 0 {
			gap := f.src[pieces[i-1].end:pc.start]
			nl := strings.Count(gap, "\n")
			switch {
			case nl == 0 && pieces[i-1].comp && !pc.comp && strings.HasPrefix(pc.text, "/"):
				b.WriteString(" ") // a comment after a component's closing brace
			case nl >= 2:
				b.WriteString("\n\n")
			default:
				b.WriteString("\n")
			}
		}
		b.WriteString(pc.text)
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	return b.String(), nil
}

// goRegion formats the Go declarations and comments in src[start:end], if
// there are any.
func (f *formatter) goRegion(pieces []piece, start, end int) []piece {
	for start < end && isSpace(f.src[start]) {
		start++
	}
	for end > start && isSpace(f.src[end-1]) {
		end--
	}
	if start == end {
		return pieces
	}
	var s synth
	s.add("package p\n")
	s.src(f.src[start:end], start)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", s.String(), parser.ParseComments|parser.SkipObjectResolution)
	if err = withoutImportOrder(err); err != nil {
		f.goErrors(err, &s)
		return pieces
	}
	var b bytes.Buffer
	if err := format.Node(&b, fset, file); err != nil {
		// format.Node sorts imports by printing and parsing the file
		// again, which fails when an import follows other declarations.
		b.Reset()
		if err := goPrinter.Fprint(&b, fset, file); err != nil {
			f.errAt(start, err.Error())
			return pieces
		}
	}
	text := strings.Trim(strings.TrimPrefix(b.String(), "package p\n"), "\n")
	return append(pieces, piece{start, end, text, false})
}

// withoutImportOrder drops go/parser's complaint about imports after other
// declarations: den lifts every import to the top of the program, so they
// may come anywhere.
func withoutImportOrder(err error) error {
	list, ok := err.(scanner.ErrorList)
	if !ok {
		return err
	}
	var out scanner.ErrorList
	for _, e := range list {
		if e.Msg != "imports must appear before other declarations" {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// synth builds Go source around fragments of the .den file and maps
// offsets in it back to the file, for error positions. The fragments go in
// with LF line endings: Go ignores the CR of a CRLF, but gofmt formats some
// comments differently with it.
type synth struct {
	b    strings.Builder
	segs []synthSeg
}

type synthSeg struct {
	at, off int
	orig    string
}

func (s *synth) add(x string) { s.b.WriteString(x) }

func (s *synth) src(x string, off int) {
	s.segs = append(s.segs, synthSeg{s.b.Len(), off, x})
	s.b.WriteString(lf(x))
}

func (s *synth) String() string { return s.b.String() }

// denOff returns the offset in the .den file of offset at of the built
// source; built text that is not from the file maps to the nearest edge.
func (s *synth) denOff(at int) int {
	prev := -1
	for _, g := range s.segs {
		if at < g.at {
			if prev < 0 {
				return g.off
			}
			return prev
		}
		k := at - g.at // in the fragment as written, with the CRs of CRLFs
		for i := 0; i < len(g.orig) && i <= k; i++ {
			if g.orig[i] == '\r' && i+1 < len(g.orig) && g.orig[i+1] == '\n' {
				k++
			}
		}
		if k < len(g.orig) {
			return g.off + k
		}
		prev = g.off + len(g.orig)
	}
	return max(prev, 0)
}

func (f *formatter) goErrors(err error, s *synth) {
	if list, ok := err.(scanner.ErrorList); ok {
		for _, e := range list {
			f.errAt(s.denOff(e.Pos.Offset), e.Msg)
		}
		return
	}
	f.errAt(s.denOff(0), err.Error())
}

func lf(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func tabs(n int) string { return strings.Repeat("\t", n) }

// width is the number of columns s takes on a line.
func width(s string) int {
	n := 0
	for _, r := range s {
		if r == '\t' {
			n += fmtTab
		} else {
			n++
		}
	}
	return n
}

func onlySpace(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isSpace(s[i]) {
			return false
		}
	}
	return true
}

// ---- components ------------------------------------------------------------

// mComponent is a component as written, with the offsets of its parts.
type mComponent struct {
	off, end  int
	name      string
	params    string
	paramsOff int
	body      int        // just past the body's {
	markup    int        // the < of the root element
	style     *styleSpan // its style block, if any
	root      *mElem
	tail      int // just past the root element
	close     int // the component's closing }
}

func (f *formatter) component(c *mComponent) string {
	var b strings.Builder
	b.WriteString("component " + c.name + f.params(c) + " {\n")
	if body := f.body(c); body != "" {
		b.WriteString(body + "\n")
		region := f.src[c.body:c.markup]
		trail := region[len(strings.TrimRight(region, " \t\r\n")):]
		if strings.Count(trail, "\n") >= 2 {
			b.WriteString("\n")
		}
	}
	b.WriteString("\t")
	m := &mprinter{f: f, b: &b, col: fmtTab, depth: 1}
	m.element(c.root)
	b.WriteString(f.tailComments(f.src[c.tail:c.close]))
	b.WriteString("\n}")
	return b.String()
}

// params formats the parameter list of c, parentheses included.
func (f *formatter) params(c *mComponent) string {
	var s synth
	s.add("package p\nfunc _(")
	s.src(c.params, c.paramsOff)
	s.add(") {}\n")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", s.String(), parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		f.goErrors(err, &s)
		return "(" + lf(c.params) + ")"
	}
	fd := file.Decls[0].(*ast.FuncDecl)
	var b bytes.Buffer
	if err := goPrinter.Fprint(&b, fset, &printer.CommentedNode{Node: fd.Type, Comments: file.Comments}); err != nil {
		f.errAt(c.paramsOff, err.Error())
		return "(" + lf(c.params) + ")"
	}
	return strings.TrimPrefix(b.String(), "func")
}

// body formats the Go statements and the style block before the markup,
// tab-indented, or returns "" when there are none. The style block stands
// in for a call of a placeholder while go/format formats the Go.
func (f *formatter) body(c *mComponent) string {
	region := f.src[c.body:c.markup]
	if onlySpace(region) {
		return ""
	}
	ph := "denStyleBlock"
	for strings.Contains(region, ph) {
		ph += "_"
	}
	var s synth
	s.add("package p\nfunc _() {\n")
	if c.style != nil {
		s.src(f.src[c.body:c.style.kw], c.body)
		s.add(ph + "();")
		s.src(f.src[c.style.end:c.markup], c.style.end)
	} else {
		s.src(region, c.body)
	}
	s.add("\n}\n")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", s.String(), parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		f.goErrors(err, &s)
		return ""
	}
	var b bytes.Buffer
	if err := format.Node(&b, fset, file); err != nil {
		f.errAt(c.body, err.Error())
		return ""
	}
	out := b.String()
	const head = "func _() {\n"
	i := strings.Index(out, head)
	if i < 0 || !strings.HasSuffix(out, "\n}\n") {
		f.errAt(c.body, "internal error: unexpected go/format output; please report")
		return ""
	}
	lines := strings.Split(out[i+len(head):len(out)-len("\n}\n")], "\n")
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if c.style != nil {
		for i, l := range lines {
			t := strings.TrimLeft(l, "\t")
			if !strings.HasPrefix(t, ph+"()") {
				continue
			}
			ind := len(l) - len(t)
			css := f.src[c.style.open+1 : c.style.end-1]
			lines[i] = tabs(ind) + styleBlock(css, ind) + t[len(ph)+2:]
			break
		}
	}
	return strings.Join(lines, "\n")
}

// styleBlock formats a style block whose CSS is css at indent depth.
func styleBlock(css string, depth int) string {
	items, ok := parseCSS(css)
	if !ok {
		return "style {" + lf(css) + "}"
	}
	if len(items) == 0 {
		return "style {}"
	}
	var b strings.Builder
	b.WriteString("style {\n")
	printCSS(&b, items, depth+1)
	b.WriteString("\n" + tabs(depth) + "}")
	return b.String()
}

// tailComments formats the Go comments between a component's root element
// and its closing brace.
func (f *formatter) tailComments(src string) string {
	var b strings.Builder
	nl := 0
	for i := 0; i < len(src); {
		switch {
		case isSpace(src[i]):
			if src[i] == '\n' {
				nl++
			}
			i++
			continue
		case strings.HasPrefix(src[i:], "//"):
			j := strings.IndexByte(src[i:], '\n')
			if j < 0 {
				j = len(src) - i
			}
			writeComment(&b, nl, strings.TrimRight(src[i:i+j], " \t\r"))
			i += j
		default: // a /* */ comment: the parser allows nothing else here
			j := strings.Index(src[i+2:], "*/") + i + 4
			writeComment(&b, nl, lf(src[i:j]))
			i = j
		}
		nl = 0
	}
	return b.String()
}

func writeComment(b *strings.Builder, nl int, c string) {
	switch {
	case nl == 0:
		b.WriteString(" ")
	case nl >= 2:
		b.WriteString("\n\n\t")
	default:
		b.WriteString("\n\t")
	}
	b.WriteString(c)
}

// ---- Go fragments in markup ------------------------------------------------

// printNode prints n at indent and returns it without the indent of its
// first line, or false when it has comments go/printer would not print.
func printNode(fset *token.FileSet, file *ast.File, n ast.Node, indent int) (string, bool) {
	for _, cg := range file.Comments {
		if cg.Pos() < n.Pos() || cg.End() > n.End() {
			return "", false
		}
	}
	cfg := goPrinter
	cfg.Indent = indent
	var b bytes.Buffer
	if err := cfg.Fprint(&b, fset, &printer.CommentedNode{Node: n, Comments: file.Comments}); err != nil {
		return "", false
	}
	return strings.TrimPrefix(b.String(), tabs(indent)), true
}

// goExpr formats the Go expression src at offset off, for a line at indent.
// Den checks such an expression as the argument of a call, denText(x), so
// it is formatted as one: comments around it stay inside the call.
func (f *formatter) goExpr(src string, off, indent int) string {
	key := fragKey{'x', off, indent}
	if out, ok := f.cache[key]; ok {
		return out
	}
	out := lf(src)
	var s synth
	s.add("package p\nvar _ = f(")
	s.src(src, off)
	s.add(")\n")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", s.String(), parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		f.goErrors(err, &s)
	} else if vs := file.Decls[0].(*ast.GenDecl).Specs[0].(*ast.ValueSpec); len(file.Decls) == 1 && len(vs.Values) == 1 {
		if x, ok := printNode(fset, file, vs.Values[0], indent); ok && strings.HasPrefix(x, "f(") && strings.HasSuffix(x, ")") {
			out = x[2 : len(x)-1]
		}
	}
	f.cache[key] = out
	return out
}

// endsInLineComment reports whether Go source src ends with a // comment.
func endsInLineComment(src string) bool {
	fset := token.NewFileSet()
	file := fset.AddFile("", -1, len(src))
	var s scanner.Scanner
	s.Init(file, []byte(src), func(token.Position, string) {}, scanner.ScanComments)
	last := ""
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			return strings.HasPrefix(last, "//")
		}
		if tok == token.SEMICOLON && lit == "\n" {
			continue
		}
		last = lit
	}
}

// goHandler formats src, the statements of an event handler at offset off,
// for a line at indent. A handler is formatted as the body of a function
// literal, so go/printer keeps it on one line when it was written on one
// line and fits, as gofmt does for small functions.
func (f *formatter) goHandler(src string, off, indent int) string {
	key := fragKey{'h', off, indent}
	if out, ok := f.cache[key]; ok {
		return out
	}
	out := lf(src)
	var s synth
	const head = "func(e Event) "
	s.add("package p\nvar _ = " + head + "{")
	s.src(src, off)
	if endsInLineComment(src) && !strings.HasSuffix(strings.TrimRight(src, " \t\r"), "\n") {
		s.add("\n") // as den has it when it checks the handler
	}
	s.add("}\n")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", s.String(), parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		f.goErrors(err, &s)
	} else if vs := file.Decls[0].(*ast.GenDecl).Specs[0].(*ast.ValueSpec); len(file.Decls) == 1 && len(vs.Values) == 1 {
		if x, ok := printNode(fset, file, vs.Values[0], indent); ok && strings.HasPrefix(x, head+"{") && strings.HasSuffix(x, "}") {
			x = x[len(head)+1 : len(x)-1]
			if !strings.Contains(x, "\n") {
				x = strings.TrimSuffix(strings.TrimPrefix(x, " "), " ")
			}
			if strings.TrimSpace(x) != "" {
				out = x
			}
		}
	}
	f.cache[key] = out
	return out
}

// goHeader formats the header of an if or for block, src at offset off,
// for a line at indent.
func (f *formatter) goHeader(kw, src string, off, indent int) string {
	key := fragKey{kw[0], off, indent}
	if out, ok := f.cache[key]; ok {
		return out
	}
	out := lf(src)
	var s synth
	s.add("package p\nfunc _() {\n" + kw + " ")
	s.src(src, off)
	s.add(" {\n}\n}\n")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", s.String(), parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		f.goErrors(err, &s)
	} else if body := file.Decls[0].(*ast.FuncDecl).Body; len(file.Decls) == 1 && len(body.List) == 1 {
		end := " {\n" + tabs(indent) + "}"
		if x, ok := printNode(fset, file, body.List[0], indent); ok && strings.HasPrefix(x, kw+" ") && strings.HasSuffix(x, end) {
			out = x[len(kw)+1 : len(x)-len(end)]
		}
	}
	f.cache[key] = out
	return out
}

// ---- markup, as written ----------------------------------------------------

// An mNode is markup as written: *mText, *mInterp, *mComment, *mElem, *mIf,
// *mFor, or *mWord once text is split.
type mNode any

type mText struct {
	raw string
	off int
}

// mInterp is {...} in markup: an expression, or nothing but a comment.
type mInterp struct {
	raw string
	off int // of the text inside the braces
}

type mComment struct{ raw string } // <!-- ... -->

type mElem struct {
	tag      string
	off, end int
	attrs    []*mAttr
	self     bool // closed with />
	open     int  // the > or /> that ends the start tag
	closeAt  int  // the </ of the end tag
	kids     []mNode
}

type mAttr struct {
	name     string
	kind     AttrKind
	raw      string // between the quotes or braces
	quote    byte
	off, end int
	valOff   int
}

type mIf struct {
	off, end int // of the whole block, for the first if of a chain
	ifAt     int
	cond     string
	condOff  int
	thenAt   int // the { of the body
	then     []mNode
	elseAt   int // the else, if any
	elseIf   *mIf
	els      []mNode
	hasElse  bool
}

type mFor struct {
	off, end  int
	forAt     int
	header    string
	headerOff int
	key       string
	keyOff    int
	body      []mNode
}

type mWord struct{ s string }

func (e *mElem) isComponent() bool { return (&Element{Tag: e.tag}).IsComponent() }
func (e *mElem) isSlot() bool      { return strings.HasPrefix(e.tag, ":") }

// lparser reads markup losslessly, the way parser_ reads it. Format runs it
// only on source that Parse accepted.
type lparser struct{ *parser_ }

func newParser(file, src string) *parser_ {
	p := &parser_{file: file, src: src}
	p.lines = append(p.lines, 0)
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			p.lines = append(p.lines, i+1)
		}
	}
	return p
}

func (p *lparser) component() *mComponent {
	c := &mComponent{off: p.off}
	p.off += len("component")
	p.skipSpace()
	c.name = p.readIdent()
	p.skipSpace()
	p.expect("(")
	c.paramsOff = p.off
	for depth := 1; depth > 0; p.off++ {
		switch p.src[p.off] {
		case '(':
			depth++
		case ')':
			depth--
		}
	}
	c.params = p.src[c.paramsOff : p.off-1]
	p.skipSpace()
	p.expect("{")
	c.body = p.off
	markup, styles := p.findMarkupStart(c.body)
	c.markup = markup
	if len(styles) > 0 {
		c.style = &styles[0]
	}
	p.off = markup
	c.root = p.element()
	c.tail = p.off
	p.skipSpaceAndComments()
	c.close = p.off
	p.expect("}")
	c.end = p.off
	return c
}

func (p *lparser) element() *mElem {
	el := &mElem{off: p.off}
	p.expect("<")
	el.tag = p.readName()
	for {
		p.skipSpace()
		el.open = p.off
		switch {
		case strings.HasPrefix(p.src[p.off:], "/>"):
			p.off += 2
			el.self = true
			el.end = p.off
			return el
		case p.peek() == '>':
			p.off++
			el.kids, el.closeAt = p.nodes(true)
			el.end = p.off
			return el
		default:
			el.attrs = append(el.attrs, p.attr())
		}
	}
}

func (p *lparser) slot() *mElem {
	el := &mElem{off: p.off}
	p.off += 2
	el.tag = ":" + p.readIdent()
	p.skipSpace()
	el.open = p.off
	if strings.HasPrefix(p.src[p.off:], "/>") {
		p.off += 2
		el.self = true
	} else {
		p.off++
		el.kids, el.closeAt = p.nodes(true)
	}
	el.end = p.off
	return el
}

func (p *lparser) attr() *mAttr {
	a := &mAttr{off: p.off}
	a.name = p.readName()
	if p.skipSpace(); p.peek() != '=' {
		a.kind, a.end = AttrBool, a.off+len(a.name)
		return a
	}
	p.off++
	p.skipSpace()
	switch q := p.peek(); q {
	case '"', '\'':
		p.off++
		end := strings.IndexByte(p.src[p.off:], q)
		a.kind, a.raw, a.quote, a.valOff = AttrStatic, p.src[p.off:p.off+end], q, p.off
		p.off += end + 1
	default:
		a.kind, a.valOff = AttrExpr, p.off+1
		a.raw, _ = p.readBraced()
	}
	a.end = p.off
	return a
}

// nodes reads markup up to the closing tag of an element, or up to the }
// that ends a block, and moves past it. It also returns where that starts.
func (p *lparser) nodes(inElem bool) ([]mNode, int) {
	var out []mNode
	for {
		rest := p.src[p.off:]
		switch {
		case !inElem && rest[0] == '}':
			p.off++
			return out, p.off - 1
		case strings.HasPrefix(rest, "</"):
			end := p.off
			p.off += 2
			if p.peek() == ':' {
				p.off++
				p.readIdent()
			} else {
				p.readName()
			}
			p.skipSpace()
			p.expect(">")
			return out, end
		case strings.HasPrefix(rest, "<!--"):
			end := strings.Index(rest, "-->") + 3
			out = append(out, &mComment{rest[:end]})
			p.off += end
		case strings.HasPrefix(rest, "<:"):
			out = append(out, p.slot())
		case rest[0] == '<':
			out = append(out, p.element())
		case rest[0] == '{' && p.controlWord() != "":
			out = append(out, p.control())
		case rest[0] == '{':
			off := p.off + 1
			raw, _ := p.readBraced()
			out = append(out, &mInterp{raw, off})
		default:
			start := p.off
			for !p.eof() && p.peek() != '<' && p.peek() != '{' && (inElem || p.peek() != '}') {
				p.off++
			}
			out = append(out, &mText{p.src[start:p.off], start})
		}
	}
}

func (p *lparser) control() mNode {
	off := p.off
	kw := p.controlWord()
	p.off++
	p.skipSpace()
	var n mNode
	if kw == "if" {
		n = p.ifBlock()
	} else {
		n = p.forBlock()
	}
	p.skipSpace()
	p.expect("}")
	switch n := n.(type) {
	case *mIf:
		n.off, n.end = off, p.off
	case *mFor:
		n.off, n.end = off, p.off
	}
	return n
}

func (p *lparser) ifBlock() *mIf {
	n := &mIf{ifAt: p.off, elseAt: -1}
	p.off += len("if")
	n.cond, n.condOff = p.readHeader("if", Pos{})
	n.thenAt = p.off - 1
	n.then, _ = p.nodes(false)
	p.skipSpace()
	if !p.atWord("else") {
		return n
	}
	n.elseAt = p.off
	p.off += len("else")
	p.skipSpace()
	if p.atWord("if") {
		n.elseIf = p.ifBlock()
		return n
	}
	p.off++
	n.hasElse = true
	n.els, _ = p.nodes(false)
	return n
}

func (p *lparser) forBlock() *mFor {
	n := &mFor{forAt: p.off}
	p.off += len("for")
	header, off := p.readHeader("for", Pos{})
	head, key, keyOff := splitKey(header)
	n.header, n.headerOff = head, off
	n.key, n.keyOff = key, off+keyOff
	n.body, _ = p.nodes(false)
	return n
}

// ---- markup layout ---------------------------------------------------------

// A content list (the children of an element, or the body of a block) is
// laid out as atoms with gaps of whitespace between them, and at its two
// edges. Atoms are the words of text and every other node.
type mseq struct {
	atoms []mNode
	gaps  []mgap // len(atoms)+1
}

type gapKind int

const (
	gapEdge  gapKind = iota // nothing, or a newline: whitespace with a newline at the edge of text (or none)
	gapWords                // one space, or a newline: whitespace between two words
	gapSpace                // one space and nothing else: text the page shows
	gapAny                  // anything: whitespace den trims away at the edge of a block
)

type mgap struct {
	raw    string
	nl     int
	kind   gapKind
	frozen bool // kept exactly as written
}

type seqCtx int

const (
	ctxElem  seqCtx = iota // an HTML or SVG element
	ctxComp                // a component element or a slot, whose blank edge text is dropped
	ctxBlock               // an {if} or {for} body, whose edge spaces are trimmed
)

// fragile reports whether a word is made only of characters that
// strings.TrimSpace removes: alone on a line of text, den drops it, so
// text around it must keep its lines.
func fragile(w string) bool {
	for _, r := range w {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func isMarkupSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

func newSeq(nodes []mNode, ctx seqCtx, frozen bool) *mseq {
	s := &mseq{}
	pending := ""
	push := func(a mNode, words bool) {
		s.gaps = append(s.gaps, mgap{raw: pending})
		if words {
			s.gaps[len(s.gaps)-1].kind = gapWords
		}
		s.atoms = append(s.atoms, a)
		pending = ""
	}
	var stuck [][2]int // the gaps around and inside each text run with a fragile word
	for _, n := range nodes {
		t, ok := n.(*mText)
		if !ok {
			push(n, false)
			continue
		}
		first := len(s.gaps)
		inRun, broken := false, false
		for i := 0; i < len(t.raw); {
			if isMarkupSpace(t.raw[i]) {
				pending += t.raw[i : i+1]
				i++
				continue
			}
			j := i
			for j < len(t.raw) && !isMarkupSpace(t.raw[j]) {
				j++
			}
			w := t.raw[i:j]
			broken = broken || fragile(w)
			push(&mWord{w}, inRun)
			inRun = true
			i = j
		}
		if broken {
			stuck = append(stuck, [2]int{first, len(s.gaps)}) // up to the gap after the run
		}
	}
	s.gaps = append(s.gaps, mgap{raw: pending})
	for _, r := range stuck {
		for k := r[0]; k <= r[1]; k++ {
			s.gaps[k].frozen = true
		}
	}
	n := len(s.atoms)
	for i := range s.gaps {
		g := &s.gaps[i]
		g.nl = strings.Count(g.raw, "\n")
		if frozen {
			g.frozen = true
		}
		if g.kind == gapWords {
			continue
		}
		if g.nl > 0 || g.raw == "" {
			g.kind = gapEdge
			continue
		}
		g.kind = gapSpace
		switch {
		case ctx == ctxBlock && (i == 0 || i == n):
			g.kind = gapAny
		case ctx == ctxComp && i == 0 && (n == 0 || !isWord(s.atoms[0])):
			g.kind = gapAny
		case ctx == ctxComp && i == n && (n == 0 || !isWord(s.atoms[n-1])):
			g.kind = gapAny
		}
	}
	return s
}

func isWord(n mNode) bool { _, ok := n.(*mWord); return ok }

// blockAtom reports whether n is markup that may start a line of its own
// where it was glued to its neighbour.
func blockAtom(n mNode) bool {
	switch n.(type) {
	case *mElem, *mIf, *mFor, *mComment:
		return true
	}
	return false
}

// inlineOnly reports whether the list holds only text and {expr}s.
func (s *mseq) inlineOnly() bool {
	for _, a := range s.atoms {
		switch a.(type) {
		case *mWord, *mInterp:
		default:
			return false
		}
	}
	return true
}

type mprinter struct {
	f     *formatter
	b     *strings.Builder
	col   int // the column after what is written so far
	depth int // the indent of the current line
}

func (m *mprinter) write(s string) {
	m.b.WriteString(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		m.col = width(s[i+1:])
		m.depth = len(s[i+1:]) - len(strings.TrimLeft(s[i+1:], "\t"))
		return
	}
	m.col += width(s)
}

func (m *mprinter) newline(depth int, blank bool) {
	if blank {
		m.b.WriteString("\n")
	}
	m.b.WriteString("\n" + tabs(depth))
	m.col, m.depth = depth*fmtTab, depth
}

func (m *mprinter) oneLine(off, end int) bool {
	return !strings.Contains(m.f.src[off:end], "\n")
}

func (m *mprinter) fits(s string) bool { return m.col+width(s) <= fmtWidth }

func isFrozenTag(tag string) bool {
	t := strings.ToLower(tag)
	return t == "pre" || t == "textarea"
}

func (m *mprinter) ctx(e *mElem) seqCtx {
	if e.isComponent() || e.isSlot() {
		return ctxComp
	}
	return ctxElem
}

// attr formats an attribute for a line at indent.
func (m *mprinter) attr(e *mElem, a *mAttr, indent int) string {
	switch a.kind {
	case AttrBool:
		return a.name
	case AttrStatic:
		q := a.quote
		if !strings.Contains(a.raw, `"`) {
			q = '"'
		}
		// A CR is part of the value: written as a reference, the file can
		// have LF line endings without changing it.
		return a.name + "=" + string(q) + strings.ReplaceAll(a.raw, "\r", "&#13;") + string(q)
	}
	if !e.isComponent() && isEventAttr(a.name) {
		return a.name + "={" + m.f.goHandler(a.raw, a.valOff, indent) + "}"
	}
	return a.name + "={" + m.f.goExpr(a.raw, a.valOff, indent) + "}"
}

// startTag returns the start tag of e on one line, and false if it cannot
// be on one line.
func (m *mprinter) startTag(e *mElem, end string) (string, bool) {
	var b strings.Builder
	b.WriteString("<" + e.tag)
	for _, a := range e.attrs {
		s := m.attr(e, a, m.depth+1)
		if strings.Contains(s, "\n") {
			return "", false
		}
		b.WriteString(" " + s)
	}
	b.WriteString(end)
	return b.String(), true
}

func (m *mprinter) writeStartTag(e *mElem, end string) {
	d := m.depth
	if s, ok := m.startTag(e, end); ok && (len(e.attrs) == 0 || m.fits(s)) {
		m.write(s)
		return
	}
	m.write("<" + e.tag)
	for _, a := range e.attrs {
		m.newline(d+1, false)
		m.write(m.attr(e, a, d+1))
	}
	m.write(end)
	m.depth = d + 1
}

func (m *mprinter) element(e *mElem) {
	d := m.depth
	frozen := isFrozenTag(e.tag)
	if e.self {
		m.writeStartTag(e, " />")
		m.depth = d
		return
	}
	s := newSeq(e.kids, m.ctx(e), frozen)
	if (s.inlineOnly() || m.oneLine(e.off, e.end)) && !frozen {
		if flat, ok := m.flat(e); ok && m.fits(flat) {
			m.write(flat)
			return
		}
	}
	m.writeStartTag(e, ">")
	m.block(s, d, frozen)
	m.write("</" + e.tag + ">")
}

// block writes the atoms of s and the gaps around them for content whose
// opening line is at indent d: its lines go at d+1, and the line of the
// closing tag or brace at d.
func (m *mprinter) block(s *mseq, d int, frozen bool) {
	n := len(s.atoms)
	for i := 0; i <= n; i++ {
		g := s.gaps[i]
		edge := i == 0 || i == n
		switch {
		case g.frozen:
			m.write(lf(g.raw))
		case g.kind == gapSpace:
			m.write(" ")
		case g.kind == gapWords:
			if g.nl > 0 {
				m.newline(d+1, g.nl > 1)
			} else {
				m.write(" ")
			}
		case n == 0:
			// empty content stays empty
		case i == n:
			m.newline(d, false)
		case edge:
			m.newline(d+1, false)
		case g.nl > 0:
			m.newline(d+1, g.nl > 1)
		case blockAtom(s.atoms[i-1]) && blockAtom(s.atoms[i]):
			m.newline(d+1, false)
		}
		if i < n {
			m.atom(s.atoms[i], frozen)
		}
	}
	m.depth = d
}

func (m *mprinter) atom(a mNode, frozen bool) {
	switch a := a.(type) {
	case *mWord:
		m.write(a.s)
	case *mComment:
		m.write(lf(a.raw))
	case *mInterp:
		m.write(m.interp(a))
	case *mElem:
		if frozen && !isFrozenTag(a.tag) {
			// Inside <pre> and <textarea>, so is all whitespace.
			m.frozenElement(a)
			return
		}
		m.element(a)
	case *mIf, *mFor:
		m.control(a)
	}
}

// frozenElement writes an element inside <pre> or <textarea>: its tags are
// formatted, its content's whitespace kept.
func (m *mprinter) frozenElement(e *mElem) {
	d := m.depth
	if e.self {
		m.writeStartTag(e, " />")
		m.depth = d
		return
	}
	m.writeStartTag(e, ">")
	m.block(newSeq(e.kids, m.ctx(e), true), d, true)
	m.write("</" + e.tag + ">")
}

func (m *mprinter) interp(a *mInterp) string {
	t := strings.TrimSpace(a.raw)
	if t == "" || strings.HasPrefix(t, "/*") && strings.HasSuffix(t, "*/") {
		return "{" + lf(t) + "}" // the parser drops these
	}
	return "{" + m.f.goExpr(a.raw, a.off, m.depth) + "}"
}

func (m *mprinter) control(n mNode) {
	d := m.depth
	switch n := n.(type) {
	case *mIf:
		if m.oneLine(n.off, n.end) {
			if flat, ok := m.flat(n); ok && m.fits(flat) {
				m.write(flat)
				return
			}
		}
		m.write("{")
		for x := n; ; x = x.elseIf {
			m.write("if " + m.f.goHeader("if", x.cond, x.condOff, d) + " {")
			m.block(newSeq(x.then, ctxBlock, false), d, false)
			if x.elseIf != nil {
				m.write("} else ")
				continue
			}
			if x.hasElse {
				m.write("} else {")
				m.block(newSeq(x.els, ctxBlock, false), d, false)
			}
			break
		}
		m.write("}}")
	case *mFor:
		if m.oneLine(n.off, n.end) {
			if flat, ok := m.flat(n); ok && m.fits(flat) {
				m.write(flat)
				return
			}
		}
		m.write("{for " + m.f.goHeader("for", n.header, n.headerOff, d) + " key " + m.f.goExpr(n.key, n.keyOff, d) + " {")
		m.block(newSeq(n.body, ctxBlock, false), d, false)
		m.write("}}")
	}
}

// flat returns n written on one line, or false if it cannot be.
func (m *mprinter) flat(n mNode) (string, bool) {
	one := func(s string) (string, bool) { return s, !strings.Contains(s, "\n") }
	switch n := n.(type) {
	case *mWord:
		return n.s, true
	case *mComment:
		return one(n.raw)
	case *mInterp:
		return one(m.interp(n))
	case *mElem:
		if n.self {
			return m.startTag(n, " />")
		}
		if isFrozenTag(n.tag) {
			return "", false
		}
		open, ok := m.startTag(n, ">")
		if !ok {
			return "", false
		}
		kids, ok := m.flatSeq(newSeq(n.kids, m.ctx(n), false))
		return open + kids + "</" + n.tag + ">", ok
	case *mIf:
		var b strings.Builder
		b.WriteString("{")
		for x := n; ; x = x.elseIf {
			cond, ok := one(m.f.goHeader("if", x.cond, x.condOff, m.depth))
			then, ok2 := m.flatSeq(newSeq(x.then, ctxBlock, false))
			if !ok || !ok2 {
				return "", false
			}
			b.WriteString("if " + cond + " {" + then)
			if x.elseIf != nil {
				b.WriteString("} else ")
				continue
			}
			if x.hasElse {
				els, ok := m.flatSeq(newSeq(x.els, ctxBlock, false))
				if !ok {
					return "", false
				}
				b.WriteString("} else {" + els)
			}
			break
		}
		b.WriteString("}}")
		return b.String(), true
	case *mFor:
		header, ok := one(m.f.goHeader("for", n.header, n.headerOff, m.depth))
		key, ok2 := one(m.f.goExpr(n.key, n.keyOff, m.depth))
		body, ok3 := m.flatSeq(newSeq(n.body, ctxBlock, false))
		return "{for " + header + " key " + key + " {" + body + "}}", ok && ok2 && ok3
	}
	return "", false
}

func (m *mprinter) flatSeq(s *mseq) (string, bool) {
	var b strings.Builder
	for i, g := range s.gaps {
		switch {
		case g.frozen:
			if g.nl > 0 {
				return "", false
			}
			b.WriteString(g.raw)
		case g.kind == gapWords || g.kind == gapSpace:
			b.WriteString(" ")
		}
		if i < len(s.atoms) {
			a, ok := m.flat(s.atoms[i])
			if !ok {
				return "", false
			}
			b.WriteString(a)
		}
	}
	return b.String(), true
}

// ---- style blocks -----------------------------------------------------------

// cssNode is an item of a style block: a comment, a declaration, a rule or
// an at-rule.
type cssNode struct {
	comment string     // a comment, as written
	same    bool       // a comment on the line of the item before it
	blank   bool       // a blank line before it
	text    string     // a declaration, a statement or an at-rule's prelude
	sels    []string   // a rule's selectors
	block   bool       // it has a { } block, holding kids
	kids    []*cssNode //
	raw     string     // an at-rule den does not know, as written
}

// parseCSS reads the CSS of a style block for formatting, or returns false
// if it has errors that make its structure unclear.
func parseCSS(src string) ([]*cssNode, bool) {
	var errs ErrorList
	s := &styler{src: src, c: &Component{Style: &Style{}}, errs: &errs}
	s.lex()
	if len(errs) > 0 {
		return nil, false
	}
	return cssItems(s, 0, len(src), false)
}

func cssItems(s *styler, i, end int, decls bool) ([]*cssNode, bool) {
	src := s.src
	var out []*cssNode
	first, nl := true, 0
	for {
		for i < end {
			if c := src[i]; isCSSSpace(c) {
				if c == '\n' {
					nl++
				}
				i++
				continue
			}
			if !strings.HasPrefix(src[i:end], "/*") {
				break
			}
			j, _ := cssAtom(src, i)
			out = append(out, &cssNode{comment: lf(src[i:j]), same: !first && nl == 0, blank: !first && nl >= 2})
			first, nl, i = false, 0, j
		}
		if i >= end {
			return out, true
		}
		n := &cssNode{blank: !first && nl >= 2}
		first, nl = false, 0
		if decls {
			stop := s.find(i, end, ";{")
			if stop < end && src[stop] == '{' {
				return nil, false
			}
			if stop > i {
				n.text = cssDecl(s, i, stop)
				out = append(out, n)
			}
			i = stop + 1
			continue
		}
		stop := s.preludeEnd(i, end)
		if stop >= end || src[stop] == ';' {
			n.text = collapseCSSSpace(src[i:min(stop, end)])
			out = append(out, n)
			i = stop + 1
			continue
		}
		close := s.matchBrace(stop, end)
		n.block = true
		ok := true
		if src[i] == '@' {
			nameEnd, name := s.atName(i, stop)
			switch name {
			case "media", "supports", "container", "keyframes":
				n.text = src[i:nameEnd]
				if p := collapseCSSSpace(src[nameEnd:stop]); p != "" {
					n.text += " " + p
				}
				n.kids, ok = cssItems(s, stop+1, close, false)
			default:
				n.block, n.raw = false, lf(src[i:close+1])
			}
		} else {
			for _, part := range s.splitTop(i, stop) {
				n.sels = append(n.sels, collapseCSSSpace(src[part[0]:part[1]]))
			}
			n.kids, ok = cssItems(s, stop+1, close, true)
		}
		if !ok {
			return nil, false
		}
		out = append(out, n)
		i = close + 1
	}
}

// cssDecl formats the declaration in src[i:end] as `name: value`.
func cssDecl(s *styler, i, end int) string {
	colon := s.find(i, end, ":")
	if colon >= end {
		return collapseCSSSpace(s.src[i:end])
	}
	d := strings.TrimRight(s.src[i:colon], " \t\r\n\f") + ":"
	if v := collapseCSSSpace(s.src[colon+1 : end]); v != "" {
		d += " " + v
	}
	return d
}

// collapseCSSSpace trims src and collapses each run of whitespace in it to
// one space, leaving strings, comments and url(...) as they are written.
// Den reads CSS whitespace as one space wherever it matters at all.
func collapseCSSSpace(src string) string {
	var b strings.Builder
	space := false
	for i := 0; i < len(src); {
		if isCSSSpace(src[i]) {
			space = true
			i++
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		j, _ := cssAtom(src, i)
		if strings.HasPrefix(src[i:], "/*") {
			b.WriteString(lf(src[i:j]))
		} else {
			// A string keeps even the CR of an escaped CRLF: den writes
			// strings into den.css as they are.
			b.WriteString(src[i:j])
		}
		i = j
	}
	return b.String()
}

func printCSS(b *strings.Builder, items []*cssNode, depth int) {
	for i, it := range items {
		if it.comment != "" && it.same {
			b.WriteString(" " + it.comment)
			continue
		}
		if i > 0 {
			b.WriteString("\n")
			if it.blank {
				b.WriteString("\n")
			}
		}
		ind := tabs(depth)
		b.WriteString(ind)
		switch {
		case it.comment != "":
			b.WriteString(it.comment)
		case it.raw != "":
			b.WriteString(it.raw)
		case it.block:
			if it.sels == nil {
				b.WriteString(it.text + " {")
			} else if one := strings.Join(it.sels, ", ") + " {"; len(it.sels) == 1 || width(ind+one) <= fmtCSSWidth {
				b.WriteString(one)
			} else {
				b.WriteString(strings.Join(it.sels, ",\n"+ind) + " {")
			}
			if len(it.kids) == 0 {
				b.WriteString("}")
				continue
			}
			b.WriteString("\n")
			printCSS(b, it.kids, depth+1)
			b.WriteString("\n" + ind + "}")
		default:
			b.WriteString(it.text + ";")
		}
	}
}

// cssShape describes CSS as den reads it, without its layout.
func cssShape(src string) string {
	items, ok := parseCSS(src)
	if !ok {
		return "?" + lf(src)
	}
	var b strings.Builder
	var walk func([]*cssNode)
	walk = func(items []*cssNode) {
		for _, it := range items {
			switch {
			case it.comment != "":
				b.WriteString("C" + it.comment)
			case it.raw != "":
				b.WriteString("R" + it.raw)
			case it.block:
				b.WriteString("B" + it.text + strings.Join(it.sels, "\x00") + "{")
				walk(it.kids)
				b.WriteString("}")
			default:
				b.WriteString("D" + it.text + ";")
			}
		}
	}
	walk(items)
	return b.String()
}

// ---- checking the result ---------------------------------------------------

// sameMeaning parses out and compares it with orig, the parse of the
// source that was formatted: the markup must be the same, text for text,
// the Go the same tokens and the CSS the same rules. Formatting is wrong
// if they differ, so that is reported rather than the formatted file.
func sameMeaning(file string, orig *File, out string) error {
	bad := func(p Pos) error {
		return ErrorList{{File: file, Pos: p, Msg: "internal error: den fmt would change what this file means here; please report"}}
	}
	f, err := Parse(file, out)
	if err != nil {
		return bad(Pos{1, 1})
	}
	if len(f.Decls) != len(orig.Decls) || len(f.Components) != len(orig.Components) {
		return bad(Pos{1, 1})
	}
	for i, d := range orig.Decls {
		if !sameGo('d', d.Src, f.Decls[i].Src) {
			return bad(d.Pos)
		}
	}
	for i, c := range orig.Components {
		g := f.Components[i]
		switch {
		case c.Name != g.Name || !sameGo('p', c.ParamsSrc, g.ParamsSrc):
			return bad(c.NamePos)
		case !sameGo('s', c.StmtsSrc, g.StmtsSrc):
			return bad(c.StmtsPos)
		case (c.Style == nil) != (g.Style == nil):
			return bad(c.StmtsPos)
		case c.Style != nil && cssShape(c.Style.Src) != cssShape(g.Style.Src):
			return bad(c.Style.Pos)
		}
		if p, ok := sameNodes([]Node{c.Root}, []Node{g.Root}); !ok {
			return bad(p)
		}
	}
	return nil
}

func sameNodes(a, b []Node) (Pos, bool) {
	for i, n := range a {
		if i >= len(b) {
			return n.pos(), false
		}
		if p, ok := sameNode(n, b[i]); !ok {
			return p, false
		}
	}
	if len(b) > len(a) {
		if len(a) > 0 {
			return a[len(a)-1].pos(), false
		}
		return b[0].pos(), false
	}
	return Pos{}, true
}

func sameNode(a, b Node) (Pos, bool) {
	switch a := a.(type) {
	case *Text:
		if b, ok := b.(*Text); ok && a.Value == b.Value {
			return Pos{}, true
		}
	case *Interp:
		if b, ok := b.(*Interp); ok && sameGo('x', a.Src, b.Src) {
			return Pos{}, true
		}
	case *If:
		if b, ok := b.(*If); ok && sameGo('i', a.Cond, b.Cond) {
			if p, ok := sameNodes(a.Then, b.Then); !ok {
				return p, false
			}
			return sameNodes(a.Else, b.Else)
		}
	case *For:
		if b, ok := b.(*For); ok && sameGo('f', a.Header, b.Header) && sameGo('x', a.Key, b.Key) {
			return sameNodes(a.Body, b.Body)
		}
	case *Element:
		b, ok := b.(*Element)
		if !ok || a.Tag != b.Tag || len(a.Attrs) != len(b.Attrs) || len(a.Slots) != len(b.Slots) {
			break
		}
		for i, x := range a.Attrs {
			y := b.Attrs[i]
			kind := byte('x')
			if !a.IsComponent() && isEventAttr(x.Name) {
				kind = 'h'
			}
			if x.Name != y.Name || x.Kind != y.Kind || x.Kind == AttrStatic && x.Value != y.Value || x.Kind == AttrExpr && !sameGo(kind, x.Value, y.Value) {
				return x.Pos, false
			}
		}
		for i, s := range a.Slots {
			if p, ok := sameNode(s, b.Slots[i]); !ok {
				return p, false
			}
		}
		return sameNodes(a.Children, b.Children)
	}
	return a.pos(), false
}

// goShape describes Go source src of the given kind ('d' declarations,
// 'p' parameters, 's' statements, 'x' an expression, 'h' a handler, 'i'
// and 'f' if and for headers) as its syntax tree, without positions,
// comments and empty statements. Parentheses count, except where gofmt
// drops them and den compiles the same without them (see unparenSlots).
// Numbers are described by their values and imports as a set, since gofmt
// sorts them. It returns false if src does not parse.
func goShape(kind byte, src string) (string, bool) {
	var full string
	switch kind {
	case 'd':
		full = "package p\n" + src
	case 'p':
		full = "package p\nfunc _(" + src + ") {}\n"
	case 's':
		full = "package p\nfunc _() {\n" + src + "\n}\n"
	case 'x':
		full = "package p\nvar _ = f(" + src + ")\n"
	case 'h':
		full = "package p\nvar _ = func(e Event) {" + src + "\n}\n"
	case 'i':
		full = "package p\nfunc _() {\nif " + src + " {\n}\n}\n"
	case 'f':
		full = "package p\nfunc _() {\nfor " + src + " {\n}\n}\n"
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", lf(full), parser.SkipObjectResolution)
	if withoutImportOrder(err) != nil {
		return "", false
	}
	unparenSlots(file)
	var b strings.Builder
	dumpGo(&b, reflect.ValueOf(file.Decls))
	return b.String(), true
}

// unparenSlots removes from file the parentheses and empty statements
// that go/printer drops: parentheses around if and for conditions and
// range expressions (stripParens; emit drops these too, in header), and
// around parameter, result and type parameter types (stripParensAlways;
// types make no JS). Nested parentheses go/printer prints as one pair, as
// emit does; dumpGo folds those.
func unparenSlots(file *ast.File) {
	types := func(fls ...*ast.FieldList) {
		for _, fl := range fls {
			if fl != nil {
				for _, f := range fl.List {
					f.Type = ast.Unparen(f.Type)
				}
			}
		}
	}
	empty := func(s ast.Stmt) bool { _, ok := s.(*ast.EmptyStmt); return ok }
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.IfStmt:
			n.Cond = ast.Unparen(n.Cond)
		case *ast.ForStmt:
			n.Cond = ast.Unparen(n.Cond)
		case *ast.SwitchStmt:
			n.Tag = ast.Unparen(n.Tag)
		case *ast.RangeStmt:
			n.X = ast.Unparen(n.X)
		case *ast.FuncType:
			types(n.TypeParams, n.Params, n.Results)
		case *ast.TypeSpec:
			types(n.TypeParams)
		case *ast.BlockStmt:
			n.List = slices.DeleteFunc(n.List, empty)
		case *ast.CaseClause:
			n.Body = slices.DeleteFunc(n.Body, empty)
		case *ast.CommClause:
			n.Body = slices.DeleteFunc(n.Body, empty)
		}
		return true
	})
}

var (
	posType     = reflect.TypeFor[token.Pos]()
	skipInShape = map[reflect.Type]bool{
		reflect.TypeFor[*ast.Object]():       true,
		reflect.TypeFor[*ast.Scope]():        true,
		reflect.TypeFor[*ast.CommentGroup](): true,
	}
)

func dumpGo(b *strings.Builder, v reflect.Value) {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			b.WriteString("nil;")
			return
		}
		dumpGo(b, v.Elem())
	case reflect.Pointer:
		if skipInShape[v.Type()] {
			return
		}
		if v.IsNil() {
			b.WriteString("nil;")
			return
		}
		switch x := v.Interface().(type) {
		case *ast.ParenExpr:
			if inner, ok := x.X.(*ast.ParenExpr); ok {
				dumpGo(b, reflect.ValueOf(inner)) // ((x)) is printed, and compiled, as (x)
				return
			}
		case *ast.BasicLit:
			val := x.Value
			if x.Kind == token.INT || x.Kind == token.FLOAT || x.Kind == token.IMAG {
				val = constant.MakeFromLiteral(x.Value, x.Kind, 0).ExactString()
			}
			b.WriteString("lit(" + x.Kind.String() + " " + val + ");")
			return
		case *ast.CallExpr:
			if x.Ellipsis.IsValid() {
				b.WriteString("...")
			}
		case *ast.GenDecl:
			if x.Tok == token.IMPORT {
				var specs []string
				for _, s := range x.Specs {
					var sb strings.Builder
					dumpGo(&sb, reflect.ValueOf(s))
					if !slices.Contains(specs, sb.String()) {
						specs = append(specs, sb.String())
					}
				}
				slices.Sort(specs)
				b.WriteString("import[" + strings.Join(specs, "") + "]")
				return
			}
		}
		dumpGo(b, v.Elem())
	case reflect.Struct:
		b.WriteString(v.Type().Name() + "{")
		for i := 0; i < v.NumField(); i++ {
			if f := v.Type().Field(i); f.IsExported() && f.Type != posType {
				dumpGo(b, v.Field(i))
			}
		}
		b.WriteString("}")
	case reflect.Slice:
		b.WriteString("[")
		for i := 0; i < v.Len(); i++ {
			dumpGo(b, v.Index(i))
		}
		b.WriteString("]")
	case reflect.String:
		b.WriteString(strconv.Quote(v.String()) + ";")
	case reflect.Bool, reflect.Int:
		fmt.Fprint(b, v.Interface(), ";")
	}
}

// sameGo reports whether a and b, Go source of the given kind (see
// goShape), are the same Go but for layout and comments.
func sameGo(kind byte, a, b string) bool {
	x, ok := goShape(kind, a)
	y, ok2 := goShape(kind, b)
	return ok && ok2 && x == y
}
