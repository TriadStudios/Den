package compiler

import (
	"flag"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files of TestFormatGolden")

// program is a set of .den files that compile together.
type program struct {
	name  string
	files []Source
}

func readSources(t *testing.T, paths ...string) []Source {
	t.Helper()
	var out []Source
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, Source{Name: filepath.Base(p), Code: string(b)})
	}
	return out
}

// repoPrograms returns the examples, the benchmark app, the conformance
// programs (each with an App), the golden inputs, and every program in
// the compiler's tests that compiles on its own.
func repoPrograms(t *testing.T) []program {
	t.Helper()
	var out []program
	dirs, _ := filepath.Glob("../examples/*")
	for _, dir := range append(dirs, "../bench/den") {
		files, _ := filepath.Glob(filepath.Join(dir, "*.den"))
		if len(files) > 0 {
			out = append(out, program{dir, readSources(t, files...)})
		}
	}
	conform, _ := filepath.Glob("testdata/conform/*.den")
	for _, f := range conform {
		srcs := readSources(t, f)
		srcs = append(srcs, Source{Name: "app.den", Code: "component App() {\n\t<p>{run()}</p>\n}\n"})
		out = append(out, program{f, srcs})
	}
	inputs, _ := filepath.Glob("testdata/format/*.input")
	for _, f := range inputs {
		out = append(out, program{f, readSources(t, f)})
	}
	for i, src := range formatRepros {
		out = append(out, program{"formatRepros[" + strconv.Itoa(i) + "]", []Source{{Name: "app.den", Code: src}}})
	}
	if len(dirs) < 5 || len(conform) == 0 || len(inputs) == 0 {
		t.Fatalf("found %d examples, %d conformance programs and %d golden inputs", len(dirs), len(conform), len(inputs))
	}
	return append(out, inlinePrograms(t)...)
}

// formatRepros are programs that once compiled differently after den fmt:
// gofmt dropped the parentheses of their if, for and range headers, which
// den used to write into the JS.
var formatRepros = []string{
	"component App() {\n\tx := 1\n\tif (x > 0) {\n\t\tx++\n\t}\n\t<p>{if (x > 1) {<b>y</b>}}</p>\n}\n",
	"func g() int {\n\ts := 0\n\tfor i := 0; (i < 3); i++ {\n\t\ts += i\n\t}\n\treturn s\n}\n\n" +
		"component App() {\n\txs := []int{1}\n\tfor _, v := range (xs) {\n\t\txs[0] += v\n\t}\n" +
		"\t<ul>{for _, v := range (xs) key v {<li>{v}{g()}</li>}}</ul>\n}\n",
}

// inlinePrograms returns the string literals in the compiler's tests that
// hold a program that compiles by itself.
func inlinePrograms(t *testing.T) []program {
	t.Helper()
	files, _ := filepath.Glob("*_test.go")
	seen := map[string]bool{}
	var out []program
	for _, file := range files {
		if file == "format_test.go" {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			src, err := strconv.Unquote(lit.Value)
			if err != nil || !strings.Contains(src, "component ") || seen[src] {
				return true
			}
			seen[src] = true
			if _, err := compileOne(src); err == nil {
				out = append(out, program{fset.Position(lit.Pos()).String(), []Source{{Name: "app.den", Code: src}}})
			}
			return true
		})
	}
	if len(out) < 50 {
		t.Fatalf("found only %d programs in the tests", len(out))
	}
	return out
}

func formatAll(t *testing.T, p program) []Source {
	t.Helper()
	var out []Source
	for _, s := range p.files {
		b, err := Format(s.Name, []byte(s.Code))
		if err != nil {
			t.Fatalf("%s: %s: %v", p.name, s.Name, err)
		}
		out = append(out, Source{Name: s.Name, Code: string(b)})
	}
	return out
}

func mustCompile(t *testing.T, name string, srcs []Source) *Result {
	t.Helper()
	res, err := Compile(srcs)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

// TestFormatInvariant holds den fmt to its promise on every program in
// the repository: the formatted program compiles to the same JavaScript
// and CSS, byte for byte, and formatting it again changes nothing.
func TestFormatInvariant(t *testing.T) {
	progs := repoPrograms(t)
	files := 0
	for _, p := range progs {
		want := mustCompile(t, p.name, p.files)
		formatted := formatAll(t, p)
		for i, s := range formatted {
			files++
			again, err := Format(s.Name, []byte(s.Code))
			if err != nil {
				t.Fatalf("%s: %s formatted does not format: %v\n%s", p.name, s.Name, err, s.Code)
			}
			if string(again) != s.Code {
				t.Errorf("%s: %s: formatting is not idempotent\nonce:\n%s\ntwice:\n%s", p.name, p.files[i].Name, s.Code, again)
			}
		}
		got := mustCompile(t, p.name+" (formatted)", formatted)
		if got.JS != want.JS || got.CSS != want.CSS {
			t.Errorf("%s: the formatted program compiles differently\n%s", p.name, sourcesText(formatted))
		}
	}
	t.Logf("%d programs, %d files", len(progs), files)
}

func sourcesText(srcs []Source) string {
	var b strings.Builder
	for _, s := range srcs {
		b.WriteString("--- " + s.Name + "\n" + s.Code)
	}
	return b.String()
}

// TestFormatCRLF: a file with CRLF line endings formats exactly as the
// same file with LF ones.
func TestFormatCRLF(t *testing.T) {
	n := 0
	for _, p := range repoPrograms(t) {
		for _, s := range p.files {
			code := strings.ReplaceAll(s.Code, "\r\n", "\n")
			want, err := Format(s.Name, []byte(code))
			if err != nil {
				t.Fatal(err)
			}
			got, err := Format(s.Name, []byte(strings.ReplaceAll(code, "\n", "\r\n")))
			if err != nil {
				t.Fatalf("%s: %s with CRLF: %v", p.name, s.Name, err)
			}
			if string(got) != string(want) {
				t.Errorf("%s: %s formats differently with CRLF:\n%s", p.name, s.Name, got)
			}
			n++
		}
	}
	t.Logf("%d files", n)
}

// TestFormatGolden formats each testdata/format/NAME.input and compares
// the result with NAME.golden. go test -run FormatGolden -update rewrites
// the golden files.
func TestFormatGolden(t *testing.T) {
	inputs, _ := filepath.Glob("testdata/format/*.input")
	if len(inputs) == 0 {
		t.Fatal("no golden inputs")
	}
	for _, in := range inputs {
		t.Run(filepath.Base(in), func(t *testing.T) {
			src, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Format(filepath.Base(in), src)
			if err != nil {
				t.Fatal(err)
			}
			golden := strings.TrimSuffix(in, ".input") + ".golden"
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != lf(string(want)) {
				t.Errorf("formatted %s differs from %s:\n%s", in, golden, got)
			}
			again, err := Format(filepath.Base(golden), got)
			if err != nil || string(again) != string(got) {
				t.Errorf("formatting %s again: %v\n%s", golden, err, again)
			}
		})
	}
}

// TestFormatErrors: a file that does not parse gets the parser's errors,
// and Go that does not parse gets go/parser's, at their places in the
// .den file.
func TestFormatErrors(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"unclosed tag", "component App() {\n    <div>\n        <p>hi</p>\n}\n", "app.den:2:5: unclosed <div>"},
		{"mismatched tag", "component App() {\n    <div></span>\n}\n", "app.den:2:10: </span> does not match <div> opened at line 2"},
		{"no markup", "component App() {\n    x := 1\n}\n", "app.den:1:11: component App has no markup; end it with one root element"},
		{"two style blocks", "component App() {\n    style { p { color: red; } }\n    style { }\n    <p />\n}\n", "app.den:3:5: component App has a second style block; put all of its CSS in one"},
		{"go declaration", "type T struct {\n    A int\n\nfunc f() {}\n\ncomponent App() {\n    <p />\n}\n", "app.den:4:1: expected '}', found 'func'"},
		{"go statement", "component App() {\n    x := (1\n    <p>{x}</p>\n}\n", "app.den:2:12: expected ')', found newline"},
		{"expression", "component App() {\n    <p>{1 +}</p>\n}\n", "app.den:2:12: expected operand, found ')'"},
		{"attribute", "component App() {\n    <p class={f(}></p>\n}\n", "app.den:2:17: missing ',' before newline in argument list"},
		{"handler", "component App() {\n    <button onclick={x := }>b</button>\n}\n", "app.den:2:27: expected operand, found '}'"},
		{"if header", "component App() {\n    <div>{if x == {\n        <p />\n    }}</div>\n}\n", "app.den:2:18: expected operand, found '{'"},
		{"params", "component App(a int b) {\n    <p />\n}\n", "app.den:1:15: invalid parameter list: missing ',' in parameter list"},
		{"go after a component", "component App() {\n    <p />\n}\n\nfunc f(\n", "app.den:5:8: expected ')', found 'EOF'"},
		{"crlf", "type T struct {\r\n    A int\r\n\r\nfunc f() {}\r\n\r\ncomponent App() {\r\n    <p />\r\n}\r\n", "app.den:4:1: expected '}', found 'func'"},
		{"crlf statement", "component App() {\r\n    // a\r\n    // b\r\n    x := (1\r\n    <p>{x}</p>\r\n}\r\n", "app.den:4:13: expected ')', found newline"},
	} {
		_, err := Format("app.den", []byte(c.src))
		if err == nil {
			t.Errorf("%s: no error", c.name)
			continue
		}
		if got := strings.Split(err.Error(), "\n")[0]; got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

// TestSameGo pins down what Format's self-check counts as the same Go:
// only what gofmt changes and den compiles the same either way.
func TestSameGo(t *testing.T) {
	for _, c := range []struct {
		kind byte
		a, b string
		same bool
	}{
		{'s', "x := (1)", "x := 1", false},
		{'s', "x := (y)", "x := y", false},
		{'s', "x := ((y))", "x := (y)", true},
		{'s', "x := ((y))", "x := y", false},
		{'s', "if (x > 0) {\n}", "if x > 0 {\n}", true},
		{'s', "if x := (1); x > 0 {\n}", "if x := 1; x > 0 {\n}", false},
		{'s', "for i := 0; (i < 3); i++ {\n}", "for i := 0; i < 3; i++ {\n}", true},
		{'s', "for i := (0); i < 3; i++ {\n}", "for i := 0; i < 3; i++ {\n}", false},
		{'s', "for (x) {\n}", "for x {\n}", true},
		{'s', "for range (xs) {\n}", "for range xs {\n}", true},
		{'s', "a();;\n;\nb()", "a()\nb()", true},
		{'s', "a()", "b()", false},
		{'d', "func f(a (int)) (int) { return a }", "func f(a int) int { return a }", true},
		{'d', "func f() int { return (1) }", "func f() int { return 1 }", false},
		{'d', "import (\n\"b\"\n\"a\"\n\"a\"\n)", "import (\n\"a\"\n\"b\"\n)", true},
		{'i', "(x > 0)", "x > 0", true},
		{'f', "_, v := range (xs)", "_, v := range xs", true},
		{'x', "(a)", "a", false},
		{'x', "f(xs...)", "f(xs)", false},
		{'x', "0X1F", "31", true},
		{'x', "1", "2", false},
		{'x', "f(a, /* c */ b,)", "f(a, b)", true},
	} {
		if got := sameGo(c.kind, c.a, c.b); got != c.same {
			t.Errorf("sameGo(%c, %q, %q) = %t, want %t", c.kind, c.a, c.b, got, c.same)
		}
	}
}

// TestCommentOnlyInterps: a {...} in markup holding only comments and
// whitespace (what strings.TrimSpace trims, U+00A0 and \f included) is a
// comment and compiles to nothing, as it did before den fmt; one holding
// anything else is Go. den fmt agrees, formats each the same twice, and
// never changes what it compiles to.
func TestCommentOnlyInterps(t *testing.T) {
	prog := func(inner string) string {
		return "component App() {\n\tx := \"hi\"\n\t<p>[{" + inner + "}]{x}</p>\n}\n"
	}
	js := func(src string) string {
		t.Helper()
		res, err := compileOne(src)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		return res.JS
	}
	none, x, s := js("component App() {\n\tx := \"hi\"\n\t<p>[]{x}</p>\n}\n"), js(prog("x")), js(prog(`"s"`))
	for _, c := range []struct{ inner, want string }{
		{"", none}, {" ", none}, {"/* a */", none}, {" /* a */ ", none},
		{"/* a */ /* b */", none}, {"/* a *//* b */", none}, {"// a\n", none},
		{"\n// a\n// b\n", none}, {"/**/", none}, {"/* a */ // b\n", none},
		{" ", none}, {"\f", none}, {"/* a */ ", none}, {"\n", none},
		{"/* a */\n", none}, {"\t\n\t", none}, {" ", none}, {`/* "}" */`, none},
		{"\v", none}, {"// a ", none},
		{"/* a */ x /* b */", x}, {"/* a */ x", x}, {"x /* b */", x}, {"/*a*/x/*b*/", x},
		{`/* a */ "s" /* b */`, s},
		{"/* unterminated ", ""}, {"/* a */ ;", ""}, {"x // c\n", ""}, {"/* } */", ""}, {"/* a */ + x", ""},
	} {
		src := prog(c.inner)
		res, err := compileOne(src)
		switch {
		case c.want == "" && err == nil:
			t.Errorf("{%q} compiles, want an error", c.inner)
		case c.want != "" && err != nil:
			t.Errorf("{%q}: %v", c.inner, err)
		case c.want != "" && res.JS != c.want:
			t.Errorf("{%q} compiles to different JS", c.inner)
		}
		if err != nil {
			continue
		}
		once, err := Format("app.den", []byte(src))
		if err != nil {
			t.Errorf("{%q}: den fmt: %v", c.inner, err)
			continue
		}
		if twice, err := Format("app.den", once); err != nil || string(twice) != string(once) {
			t.Errorf("{%q}: den fmt is not idempotent: %v\n%s\n%s", c.inner, err, once, twice)
		}
		if got := js(string(once)); got != res.JS {
			t.Errorf("{%q}: formatted, it compiles differently:\n%s", c.inner, once)
		}
	}
}

// TestRepoFormatted keeps every .den file in the repository formatted, so
// that go test is the CI check for it.
func TestRepoFormatted(t *testing.T) {
	n := 0
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != ".." && (strings.HasPrefix(name, ".") || name == "dist" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".den") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		n++
		got, err := Format(path, b)
		if err != nil {
			t.Errorf("%s: %v", path, err)
		} else if string(got) != lf(string(b)) {
			t.Errorf("%s is not formatted; run den fmt -w %s", path, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 10 {
		t.Fatalf("found only %d .den files", n)
	}
}

// ---- perturbation ------------------------------------------------------------

// wsSlot is whitespace in formatted source that can change without
// changing what den fmt makes of it: 'l' a run with newlines, which can
// get other indentation, trailing blanks and (if it has a blank line
// already) more blank lines; 's' a run of blanks, which can be other
// blanks; 'z' a place where blanks can be added.
type wsSlot struct {
	at, end int
	kind    byte
}

type slotter struct {
	src   string
	slots []wsSlot
}

func (s *slotter) ws(at, end int) {
	if at > end || strings.Trim(s.src[at:end], " \t\n") != "" {
		panic("not whitespace: " + strconv.Quote(s.src[at:end]))
	}
	kind := byte('z')
	switch w := s.src[at:end]; {
	case strings.Contains(w, "\n"):
		kind = 'l'
	case w != "":
		kind = 's'
	}
	s.slots = append(s.slots, wsSlot{at, end, kind})
}

// existing adds the whitespace in src[at:end], if there is any.
func (s *slotter) existing(at, end int) {
	if at < end {
		s.ws(at, end)
	}
}

func (s *slotter) goCode(at, end int) {
	fset := token.NewFileSet()
	file := fset.AddFile("", -1, end-at)
	var sc scanner.Scanner
	sc.Init(file, []byte(s.src[at:end]), nil, scanner.ScanComments)
	prev := at
	for {
		pos, tok, lit := sc.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.SEMICOLON && lit == "\n" {
			continue
		}
		text := lit
		if text == "" {
			text = tok.String()
		}
		off := at + file.Offset(pos)
		// go/printer re-indents the lines of a /* */ comment from the
		// column it starts at (stripCommonPrefix), so that column counts.
		if tok != token.COMMENT || !strings.Contains(text, "\n") {
			s.existing(prev, off)
		}
		prev = off + len(text)
	}
	s.existing(prev, end)
}

func (s *slotter) css(at, end int) {
	for i := at; i < end; {
		if isCSSSpace(s.src[i]) {
			j := i
			for j < end && isCSSSpace(s.src[j]) {
				j++
			}
			s.existing(i, j)
			i = j
			continue
		}
		i, _ = cssAtom(s.src[:end], i)
	}
}

func (s *slotter) nodes(ns []mNode, frozen bool) {
	for _, n := range ns {
		switch n := n.(type) {
		case *mText:
			if frozen || slices.ContainsFunc(strings.FieldsFunc(n.raw, func(r rune) bool { return r < 0x80 && isMarkupSpace(byte(r)) }), fragile) {
				continue
			}
			for i := 0; i < len(n.raw); {
				j := i
				for j < len(n.raw) && isMarkupSpace(n.raw[j]) {
					j++
				}
				s.existing(n.off+i, n.off+j)
				for j < len(n.raw) && !isMarkupSpace(n.raw[j]) {
					j++
				}
				i = j
			}
		case *mInterp:
			if !commentOnly(n.raw) {
				s.goCode(n.off, n.off+len(n.raw))
			}
		case *mElem:
			s.elem(n, frozen)
		case *mIf:
			s.ws(n.off+1, n.ifAt)
			s.ifBlock(n)
			s.ws(n.end-1, n.end-1)
		case *mFor:
			s.ws(n.off+1, n.forAt)
			s.existing(n.forAt+3, n.headerOff)
			hEnd := n.headerOff + len(n.header)
			s.goCode(n.headerOff, hEnd)
			kw := hEnd + strings.Index(s.src[hEnd:n.keyOff], "key")
			s.existing(hEnd, kw)
			s.existing(kw+3, n.keyOff)
			kEnd := n.keyOff + len(n.key)
			s.goCode(n.keyOff, kEnd)
			s.existing(kEnd, kEnd+strings.IndexByte(s.src[kEnd:], '{'))
			s.nodes(n.body, false)
			s.ws(n.end-1, n.end-1)
		}
	}
}

func (s *slotter) ifBlock(n *mIf) {
	s.existing(n.ifAt+2, n.condOff)
	cEnd := n.condOff + len(n.cond)
	s.goCode(n.condOff, cEnd)
	s.existing(cEnd, n.thenAt)
	s.nodes(n.then, false)
	if n.elseAt < 0 {
		return
	}
	k := n.elseAt
	for isSpace(s.src[k-1]) {
		k--
	}
	s.existing(k, n.elseAt)
	if n.elseIf != nil {
		s.existing(n.elseAt+4, n.elseIf.ifAt)
		s.ifBlock(n.elseIf)
		return
	}
	s.existing(n.elseAt+4, n.elseAt+4+strings.IndexByte(s.src[n.elseAt+4:], '{'))
	s.nodes(n.els, false)
}

func (s *slotter) elem(e *mElem, frozen bool) {
	prev := e.off + 1 + len(e.tag)
	for _, a := range e.attrs {
		s.existing(prev, a.off)
		if a.kind != AttrBool {
			eq := a.off + len(a.name)
			s.ws(eq, eq)
			s.ws(eq+1, eq+1)
			if a.kind == AttrExpr {
				s.goCode(a.valOff, a.valOff+len(a.raw))
			}
		}
		prev = a.end
	}
	s.ws(prev, e.open)
	if e.self {
		return
	}
	s.nodes(e.kids, frozen || isFrozenTag(e.tag))
	name := e.closeAt + 2 + len(e.tag)
	s.ws(name, name)
}

// wsSlots returns the whitespace slots of formatted source src.
func wsSlots(src string) []wsSlot {
	s := &slotter{src: src}
	p := newParser("x.den", src)
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
		s.goCode(region, p.off)
		c := (&lparser{p}).component()
		s.existing(c.off+len("component"), c.off+len("component "))
		name := c.off + len("component ") + len(c.name)
		s.ws(name, name)
		s.goCode(c.paramsOff, c.paramsOff+len(c.params))
		s.existing(c.paramsOff+len(c.params)+1, c.body-1)
		if c.style != nil {
			s.goCode(c.body, c.style.kw)
			s.existing(c.style.kw+len("style"), c.style.open)
			s.css(c.style.open+1, c.style.end-1)
			s.goCode(c.style.end, c.markup)
		} else {
			s.goCode(c.body, c.markup)
		}
		s.elem(c.root, false)
		s.goCode(c.tail, c.close)
		region = c.end
	}
	s.goCode(region, len(src))
	return s.slots
}

func blanks(rng *rand.Rand, lo, hi int) string {
	var b strings.Builder
	for range lo + rng.Intn(hi-lo+1) {
		b.WriteByte(" \t"[rng.Intn(2)])
	}
	return b.String()
}

// perturb changes about half of the whitespace slots of src at random.
func perturb(src string, rng *rand.Rand) string {
	var b strings.Builder
	prev := 0
	for _, sl := range wsSlots(src) {
		if sl.at < prev {
			continue
		}
		b.WriteString(src[prev:sl.at])
		prev = sl.end
		w := src[sl.at:sl.end]
		if rng.Intn(2) == 0 {
			b.WriteString(w)
			continue
		}
		switch sl.kind {
		case 'z':
			b.WriteString(blanks(rng, 0, 2))
		case 's':
			b.WriteString(blanks(rng, 1, 3))
		case 'l':
			nl := strings.Count(w, "\n")
			if nl >= 2 {
				nl += rng.Intn(3)
			}
			for range nl {
				b.WriteString(blanks(rng, 0, 2) + "\n")
			}
			b.WriteString(blanks(rng, 0, 8))
		}
	}
	b.WriteString(src[prev:])
	return b.String()
}

// TestFormatPerturb changes insignificant whitespace in formatted programs
// at random (indentation, blank lines, trailing blanks, blanks inside Go
// code, tags and CSS) and checks that each still formats to the same
// text and compiles to the same output.
func TestFormatPerturb(t *testing.T) {
	seed := fmtSeed(t, 1)
	rng := rand.New(rand.NewSource(seed))
	start := time.Now()
	cases, changed := 0, 0
	for _, p := range repoPrograms(t) {
		want := mustCompile(t, p.name, p.files)
		formatted := formatAll(t, p)
		rounds := 6
		if raceEnabled {
			rounds = 1
		}
		if strings.Contains(p.name, "_test.go") {
			rounds = 1 // the tests' many small programs
		}
		for range rounds {
			perturbed := make([]Source, len(formatted))
			for i, s := range formatted {
				perturbed[i] = Source{Name: s.Name, Code: perturb(s.Code, rng)}
				if perturbed[i].Code != s.Code {
					changed++
				}
				got, err := Format(s.Name, []byte(perturbed[i].Code))
				if err != nil {
					t.Fatalf("-fmtseed=%d: %s: %s perturbed: %v\n%s", seed, p.name, s.Name, err, perturbed[i].Code)
				}
				if string(got) != s.Code {
					t.Fatalf("-fmtseed=%d: %s: %s perturbed formats differently\nperturbed:\n%s\nformatted:\n%s\nwant:\n%s", seed, p.name, s.Name, perturbed[i].Code, got, s.Code)
				}
			}
			got := mustCompile(t, p.name+" (perturbed)", perturbed)
			if got.JS != want.JS || got.CSS != want.CSS {
				t.Fatalf("-fmtseed=%d: %s perturbed compiles differently\n%s", seed, p.name, sourcesText(perturbed))
			}
			cases++
		}
	}
	t.Logf("%d perturbed programs (%d files changed) in %v, -fmtseed=%d", cases, changed, time.Since(start).Round(time.Millisecond), seed)
}

var fmtSeedFlag = flag.String("fmtseed", "", "seed of TestFormatPerturb and TestFormatMutants: a number, or random for one from the clock (default: each test's fixed seed)")

// fmtSeed returns the seed a randomized format test runs with: its fixed
// seed, so that go test gives the same result on every run and in CI, or
// the one -fmtseed asks for. To look for new failures, run
// go test ./compiler -run 'FormatPerturb|FormatMutants' -fmtseed=random -count=100;
// a failure names the seed that reproduces it.
func fmtSeed(t *testing.T, fixed int64) int64 {
	t.Helper()
	switch *fmtSeedFlag {
	case "":
		return fixed
	case "random":
		return time.Now().UnixNano()
	}
	n, err := strconv.ParseInt(*fmtSeedFlag, 10, 64)
	if err != nil {
		t.Fatalf("-fmtseed=%s: want a number or random", *fmtSeedFlag)
	}
	return n
}

// mutate changes about a quarter of the whitespace slots of src to
// whitespace of any kind, newlines and CRLFs included, or to nothing.
func mutate(src string, rng *rand.Rand) string {
	choices := []string{"", " ", "  ", "\n", "\n\n", "\n\t\t", " \n", "\t", "\r\n"}
	var b strings.Builder
	prev := 0
	for _, sl := range wsSlots(src) {
		if sl.at < prev {
			continue
		}
		b.WriteString(src[prev:sl.at])
		prev = sl.end
		if rng.Intn(4) == 0 {
			b.WriteString(choices[rng.Intn(len(choices))])
		} else {
			b.WriteString(src[sl.at:sl.end])
		}
	}
	b.WriteString(src[prev:])
	return b.String()
}

// TestFormatMutants holds the invariant on programs with arbitrary layouts:
// whitespace in the repository's programs is changed at random, which
// often changes what they mean or breaks them, and every one that still
// compiles must compile the same once formatted, and format idempotently.
func TestFormatMutants(t *testing.T) {
	seed := fmtSeed(t, 2)
	rng := rand.New(rand.NewSource(seed))
	rounds := 8
	if raceEnabled {
		rounds = 1
	}
	tried, kept := 0, 0
	progs := repoPrograms(t)
	for range rounds {
		for _, p := range progs {
			var mut []Source
			for _, s := range formatAll(t, p) {
				mut = append(mut, Source{Name: s.Name, Code: mutate(s.Code, rng)})
			}
			tried++
			want, err := Compile(mut)
			if err != nil {
				continue
			}
			kept++
			var out []Source
			for _, s := range mut {
				got, err := Format(s.Name, []byte(s.Code))
				if err != nil {
					t.Fatalf("-fmtseed=%d: %s: %v\n%s", seed, p.name, err, s.Code)
				}
				if again, err := Format(s.Name, got); err != nil || string(again) != string(got) {
					t.Fatalf("-fmtseed=%d: %s: not idempotent: %v\nmutant:\n%q\nonce:\n%s\ntwice:\n%s", seed, p.name, err, s.Code, got, again)
				}
				out = append(out, Source{Name: s.Name, Code: string(got)})
			}
			if res, err := Compile(out); err != nil || res.JS != want.JS || res.CSS != want.CSS {
				t.Fatalf("-fmtseed=%d: %s compiles differently once formatted: %v\nmutant:\n%s\nformatted:\n%s", seed, p.name, err, sourcesText(mut), sourcesText(out))
			}
		}
	}
	t.Logf("%d mutants, %d of which compile, -fmtseed=%d", tried, kept, seed)
}
