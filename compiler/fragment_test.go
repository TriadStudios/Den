package compiler

import (
	"fmt"
	"go/parser"
	"go/token"
	"math/rand"
	"os"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fragmentSeed is a program with every kind of Go fragment den writes into
// the Go it type-checks: text, attribute, motion and handler values, a
// component's props, statements and parameters, if and for headers and a
// list key. Each @ marks a place, inside or at the edge of a fragment,
// where the property tests insert text.
const fragmentSeed = `type T struct{ v int }

func first(xs []int) int { return xs[0] }

component Badge(n int@) {
	<i>{n@}</i>
}

component Card(n int, children den.Children) {
	<div title={"card"@}>{@n}{children}</div>
}

component Pair(a string, b int) {
	<i>{a}{b}</i>
}

component App() {
	x := signal(1)@
	xs := signal([]int{1, 2})
	<div class="app">
		<button onclick={@x.Set(x.Get() + 1)@}>inc</button>
		<p title={@itoa(x.Get())@}>{@x.Get()@} and {@len(xs.Get())@}</p>
		{if @x.Get() > 0@ {<b>{@first(xs.Get())@}</b>} else {<i>none</i>}}
		{if @(T{@x.Get()@}).v > 1@ {<u enter={@fade()@}>big</u>}}
		<ul>{for @_, v := range xs.Get()@ key @v@ {<li animate={layout()@} onclick={@xs.Set(nil)@}>{v@}</li>}}</ul>
		<p>{key x.Get()@ + @1@ {<s enter={fade()}>{@x.Get() * 2@}</s>}}</p>
		<Badge n={@x.Get()@} />
		<Pair a={@"p"@} b={@2@} />
		<Card n={@x.Get()@}><span>{x.Get()@}</span></Card>
	</div>
}
`

// fragmentEscapes are changes to fragmentSeed, each made of edits (a piece
// of the seed, marks included, and what replaces it), that end the code
// den writes around a fragment and add Go statements of their own after
// it, or open a comment or literal in one fragment that runs on through
// den's code and ends in another. Without den's checks each one compiles
// (to statements no markup is paired with: dropped output, or a wrong
// pairing, or den's own Go shown as text) or panics, so each must be a
// compile error.
var fragmentEscapes = [][][2]string{
	{{"{@x.Get()@} and", "{x.Get()); denAny(1); denAny(0} and"}},
	{{"{@len(xs.Get())@}", "{len(xs.Get())); denAny(1); denAny(0}"}},
	{{"title={@itoa(x.Get())@}", `title={itoa(x.Get())); denAny(1); denAny("a"}`}},
	{{`title={"card"@}`, `title={"card"); denAny(1); denAny("a"}`}},
	{{"enter={@fade()@}", "enter={fade()); denAny(1); denEnter(fade()}"}},
	{{"animate={layout()@}", "animate={layout()); denAny(1); denAnimate(layout()}"}},
	{{"onclick={@x.Set(x.Get() + 1)@}", "onclick={x.Set(1) /* { */ }); denAny(1); denHandler(func(e Event) { /* } */}"}},
	{{"onclick={@xs.Set(nil)@}", "onclick={/* { */ }); denAny(1); denHandler(func(e Event) { /* } */}"}},
	{{"key @v@", "key v); denAny(1); denAny(0"}},
	{{"{if @x.Get() > 0@", "{if denAny(1); x.Get() > 0"}},
	{{"{if @x.Get() > 0@", "{if denAny(1)\n\t\tx.Get() > 0"}},
	{{"<Badge n={@x.Get()@} />", "<Badge n={x.Get()); denAny(1); denAny(0} />"}},
	{{"<Badge n={@x.Get()@} />", "<Badge n={x.Get()), Badge(1} />"}},
	{{"x := signal(1)@", "x := signal(1)\n\tdenMark()\n}\n\nfunc evil() {\n\tx := signal(1)"}},
	{{"{v@}</li>", "{v); denAny(1); denAny(0}</li>"}},
	{{"{x.Get()@}</span>", "{x.Get()); denAny(1); denAny(0}</span>"}},
	// den cuts a fragment at a }, counting quotes but not comments, so a
	// quote in a comment leaves a literal open in Go, to end in the next
	// fragment that has one.
	{{"{@x.Get()@} and", "{/* ` */ `} and"}, {"{@len(xs.Get())@}", "{`); denAny(len(xs.Get()) /* ` */}"}},
	{{"title={@itoa(x.Get())@}", "title={/* ` */ `}"}, {"{@x.Get()@} and", "{`); denAny(x.Get() /* ` */} and"}},
	{{`a={@"p"@}`, `a={/* " */ "}`}, {"b={@2@}", `b={", 2 /* " */}`}},
	{{"onclick={@x.Set(x.Get() + 1)@}", "onclick={_ = /* ` */ `}"}, {"title={@itoa(x.Get())@}", "title={`}); denAttr(itoa(x.Get()) /* ` */}"}},
	// //line after den's /*line*/ comment is a comment, not a directive.
	{{"{@x.Get()@} and", "{//line x:0\nx.Get()); denAny(1); denAny(0} and"}},
	{{"{@x.Get()@} and", "{//line :x\r\nx.Get()); denAny(1); denAny(0} and"}},
	{{"title={@itoa(x.Get())@}", "title={//line x:0\nitoa(x.Get())) //}"}},
	{{"onclick={@xs.Set(nil)@}", "onclick={//line x:0\n/* { */ }); denAny(1); denHandler(func(e Event) { /* } */}"}},
	{{"{@x.Get()@} and", "{//line x:0\n\"a\"); denAny(/* \" */ ` /* \" */} and"}, {"{@len(xs.Get())@}", "{//line x:0\n\" `); denAny(len(xs.Get()) /* \" */}"}},
	// A {key} value, written as denKey(x) in a block of its own.
	{{"@ + @1@ {<s", "); denAny(0); 2 {<s"}},
	{{"@ + @1@ {<s", " //line x:0\n); denAny(0); 2 {<s"}},
	{{"@ + @1@ {<s", " + 1 // c\n {<s"}},
	{{"@ + @1@ {<s", ") }; { denKey(0 {<s"}},
	// den reads "a\ and the newline as one string; Go reads a raw string
	// from the ` on, to the one in the {...} in the block.
	{{"{key x.Get()@ + @1@ {", "{key \"a\\\n` \" + \"c\" {"}, {"{@x.Get() * 2@}", "{`); denAny(x.Get() /* ` */}"}},
	// Statements that take den's next one in: defer denMark().
	{{"xs := signal([]int{1, 2})", "xs := signal([]int{1, 2})\n\tdefer"}},
	{{"xs := signal([]int{1, 2})", "xs := signal([]int{1, 2})\n\tgoto L\nL:"}},
}

// fragmentSpans are pairs of insertions at two marks, each given as the
// piece of fragmentSeed around its mark (the only @ in it) and the text to
// insert there, that open a literal in one fragment and end it in the
// next. Like fragmentEscapes, each must be a compile error.
var fragmentSpans = [][2][2]string{
	{{"x.Get()@} and", " + len(/* ` */ `"}, {"{@len(xs.Get())", "`)); denAny( /* ` */"}},
	{{`"p"@}`, ` + /* " */ "`}, {"b={@2", `", 2 /* " */ + `}},
	{{"{@x.Set(x.Get() + 1)", "_ = /* ` */ `; "}, {"title={@itoa", "`}); denAttr( /* ` */"}},
}

// fragmentMark returns the index of the mark in the piece ctx of
// fragmentSeed, which holds one.
func fragmentMark(t testing.TB, ctx string) int {
	t.Helper()
	at := strings.Index(fragmentSeed, ctx)
	if at < 0 || strings.Count(ctx, "@") != 1 || strings.Count(fragmentSeed, ctx) != 1 {
		t.Fatalf("%q is not a piece of fragmentSeed with one mark", ctx)
	}
	return strings.Count(fragmentSeed[:at+strings.Index(ctx, "@")], "@")
}

// fragmentBreaks is text that ends, or tries to end, what den writes
// around a fragment: closing brackets, statement ends, a comma, comments
// that hide a bracket or what follows, quotes. TestFragmentProperty puts
// each one alone at each mark.
var fragmentBreaks = []string{
	")", "]", "}", "{", ";", ",", "\n", "// c", "// c\n", "/* } */", "/* { */", "/* ) */", "/*",
	`")"`, "'}'", "); denAny(0", "}); denAny(0); denHandler(func(e Event) {", ", 1", "denMark()",
	"/* ` */ `", `/* " */ "`, "/* ' */ '", `"\`,
}

// fragmentNoise is all the text the property tests insert at the marks:
// the breaks, opening brackets, other comments, quotes, newlines, words
// of den's markup and Go's, bytes Go rejects, and the ends of literals
// that a break opened in another fragment.
var fragmentNoise = append(slices.Clone(fragmentBreaks),
	"(", "[", "\r\n", " ", "// )", "/* c */", "/* ( */", "*/", "/* \n */",
	`"`, "'", "`", "`}`", `"\"`, "); denAny(0); denAny(", ", denSlot(nil)",
	"...", "key", "else", "if", "for", "x", "+", ":=", "{}", "()", "\x00", "@",
	"`); denAny(0 /* ` */", "`)); denAny( /* ` */", `", 2 /* " */ + `, "`}); denAttr( /* ` */",
	"//line x:0\n", "//line :x\n", "//line x:0\r\n", "/*line x:0*/", "defer", "go", "L:",
)

// fragmentNeutral is text that changes no program when it goes between
// two tokens of Go.
var fragmentNeutral = []string{" ", "/* c */"}

// fragmentMarks returns the offsets of the marks in fragmentSeed, after
// they are taken out, and the seed without them.
func fragmentMarks() ([]int, string) {
	var marks []int
	var b strings.Builder
	for i := 0; i < len(fragmentSeed); i++ {
		if fragmentSeed[i] == '@' {
			marks = append(marks, b.Len())
			continue
		}
		b.WriteByte(fragmentSeed[i])
	}
	return marks, b.String()
}

// insertAt inserts the texts at the offsets in src, which are in order.
func insertAt(src string, at []int, texts []string) string {
	var b strings.Builder
	prev := 0
	for i, off := range at {
		b.WriteString(src[prev:off] + texts[i])
		prev = off
	}
	return b.String() + src[prev:]
}

// fragmentCase compiles src, which must not panic or report an internal
// error, and returns the compiled JS or the compile error. den fmt must
// not panic on it either; an error is fine, and so is den fmt declining
// to change what a file means (a /*\f*/ comment between two statements
// does that, on main too).
func fragmentCase(t testing.TB, src string) (js string, err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic: %v\n%s\nsource:\n%s", r, debug.Stack(), src)
		}
	}()
	res, err := compileOne(src)
	if err != nil && strings.Contains(err.Error(), "internal error") {
		t.Fatalf("%v\nsource:\n%s", err, src)
	}
	if _, ferr := Format("app.den", []byte(src)); ferr != nil && strings.Contains(ferr.Error(), "den fmt failed") {
		t.Fatalf("den fmt: %v\nsource:\n%s", ferr, src)
	}
	if err != nil {
		return "", err
	}
	return res.JS, nil
}

// TestFragmentProperty inserts text into the Go fragments of a program, at
// random marks and from noise that breaks Go, and requires that den never
// panics or reports an internal error (den fmt neither), that every escape
// (fragmentEscapes, fragmentSpans) is a compile error, and that text Go
// ignores changes nothing. The seed is fixed so CI is stable;
// DEN_FRAGMENT_CASES=n runs n random mutants, DEN_FRAGMENT_SEED=n another
// seed.
func TestFragmentProperty(t *testing.T) {
	marks, clean := fragmentMarks()
	want, err := fragmentCase(t, clean)
	if err != nil {
		t.Fatalf("the seed does not compile: %v", err)
	}
	for _, edits := range fragmentEscapes {
		src := fragmentSeed
		for _, e := range edits {
			if strings.Count(src, e[0]) != 1 {
				t.Fatalf("escape edit %q matches the seed %d times", e[0], strings.Count(src, e[0]))
			}
			src = strings.Replace(src, e[0], e[1], 1)
		}
		src = strings.ReplaceAll(src, "@", "")
		if _, err := fragmentCase(t, src); err == nil {
			t.Errorf("escape %q compiles:\n%s", edits, src)
		}
	}
	for _, sp := range fragmentSpans {
		a, b := fragmentMark(t, sp[0][0]), fragmentMark(t, sp[1][0])
		src := insertAt(clean, []int{marks[a], marks[b]}, []string{sp[0][1], sp[1][1]})
		if _, err := fragmentCase(t, src); err == nil {
			t.Errorf("span %q compiles:\n%s", sp, src)
		}
	}
	for i := range marks {
		for _, n := range fragmentNeutral {
			src := insertAt(clean, marks[i:i+1], []string{n})
			if js, err := fragmentCase(t, src); err != nil || js != want {
				t.Errorf("%q at mark %d changes the program: %v\n%s", n, i, err, src)
			}
		}
		for _, n := range fragmentBreaks {
			fragmentCase(t, insertAt(clean, marks[i:i+1], []string{n}))
		}
	}
	n := 1_000
	if testing.Short() || raceEnabled {
		n = 200
	}
	if s := os.Getenv("DEN_FRAGMENT_CASES"); s != "" {
		n, _ = strconv.Atoi(s)
	}
	seed := int64(1)
	if s := os.Getenv("DEN_FRAGMENT_SEED"); s != "" {
		seed, _ = strconv.ParseInt(s, 10, 64)
	}
	r := rand.New(rand.NewSource(seed))
	compiled := 0
	for range n {
		// Two or three marks, each with one or two pieces.
		k := 2 + r.Intn(2)
		at := r.Perm(len(marks))[:k]
		slices.Sort(at)
		offs, texts := make([]int, k), make([]string, k)
		for j, m := range at {
			offs[j] = marks[m]
			texts[j] = fragmentNoise[r.Intn(len(fragmentNoise))]
			if r.Intn(3) == 0 {
				texts[j] += fragmentNoise[r.Intn(len(fragmentNoise))]
			}
		}
		if _, err := fragmentCase(t, insertAt(clean, offs, texts)); err == nil {
			compiled++
		}
	}
	t.Logf("%d marks, %d escapes, %d spans, %d random mutants (seed %d), %d of which compile",
		len(marks), len(fragmentEscapes), len(fragmentSpans), n, seed, compiled)
}

// FuzzFragments: go test ./compiler -run X -fuzz FuzzFragments inserts any
// two texts at any two marks of fragmentSeed: a literal or comment opened
// in one fragment can only end in another.
func FuzzFragments(f *testing.F) {
	marks, clean := fragmentMarks()
	for i, n := range fragmentNoise {
		f.Add(uint(i), n, uint(i), "")
	}
	// Each escape's texts, at the marks its edits start at (or after).
	for _, edits := range fragmentEscapes {
		var ms [2]uint
		var ts [2]string
		for j, e := range edits[:min(2, len(edits))] {
			ms[j] = uint(strings.Count(fragmentSeed[:strings.Index(fragmentSeed, e[0])], "@"))
			ts[j] = e[1]
		}
		f.Add(ms[0], ts[0], ms[1], ts[1])
	}
	for _, sp := range fragmentSpans {
		f.Add(uint(fragmentMark(f, sp[0][0])), sp[0][1], uint(fragmentMark(f, sp[1][0])), sp[1][1])
	}
	f.Fuzz(func(t *testing.T, m1 uint, t1 string, m2 uint, t2 string) {
		a, b := marks[m1%uint(len(marks))], marks[m2%uint(len(marks))]
		if a > b {
			a, b, t1, t2 = b, a, t2, t1
		}
		fragmentCase(t, insertAt(clean, []int{a, b}, []string{t1, t2}))
	})
}

// fragmentSlot is a Go fragment of a parsed program, which gen copies
// into the Go it writes, and how gen writes it.
type fragmentSlot struct {
	src  *string
	kind string // params, stmts, expr (text, attribute, motion, list key), key ({key} value), prop, handler, if, for
}

// fragmentSlots returns the Go fragments of the components of f.
func fragmentSlots(f *File) []fragmentSlot {
	var out []fragmentSlot
	var walk func(ns []Node)
	walk = func(ns []Node) {
		for _, n := range ns {
			switch n := n.(type) {
			case *Interp:
				out = append(out, fragmentSlot{&n.Src, "expr"})
			case *If:
				out = append(out, fragmentSlot{&n.Cond, "if"})
				walk(n.Then)
				walk(n.Else)
			case *For:
				out = append(out, fragmentSlot{&n.Header, "for"}, fragmentSlot{&n.Key, "expr"})
				walk(n.Body)
			case *Key:
				out = append(out, fragmentSlot{&n.Key, "key"})
				walk(n.Body)
			case *Element:
				for _, a := range n.Attrs {
					switch {
					case a.Kind != AttrExpr:
					case n.IsComponent():
						out = append(out, fragmentSlot{&a.Value, "prop"})
					case isEventAttr(a.Name):
						out = append(out, fragmentSlot{&a.Value, "handler"})
					default:
						out = append(out, fragmentSlot{&a.Value, "expr"})
					}
				}
				walk(n.Children)
				for _, s := range n.Slots {
					walk(s.Children)
				}
			}
		}
	}
	for _, c := range f.Components {
		out = append(out, fragmentSlot{&c.ParamsSrc, "params"}, fragmentSlot{&c.StmtsSrc, "stmts"})
		walk([]Node{c.Root})
	}
	return out
}

// shapeEscapes are Go that ends, or runs on past, the code gen writes
// around a fragment of each kind; $ stands for the fragment's own Go.
var shapeEscapes = map[string][]string{
	"expr": {"$); denAny(0", "$); denAny(0); denAny(1", "$, 0", "$ //", "$ /*", "$ + /* ` */ `",
		"//line x:0\n$); denAny(0", "//line :x\n$); denAny(0", "//line x:0\r\n$); denAny(0",
		"//line x:0\n\"a\"); if true { denAny(0) }; denAny($"},
	"prop": {"$, 0", "$); denAny(0", "$ //", "$ /*", "//line x:0\n$); denAny(0", "//line x:0\n$, 0 //"},
	"key": {"$); denAny(0", "$, 0", "$ //", "$ /*", "$ + /* ` */ `", "//line x:0\n$); denAny(0",
		"$) }; { denKey(0", "$) }; denAny(0); { denKey(0", "$)\n\t}\n\tif true {\n\tdenKey(0"},
	"handler": {"$ }); denAny(0); denHandler(func(e Event) {", "$\n}); denAny(0); denHandler(func(e Event) {",
		"//line x:0\n$ }); denAny(0); denHandler(func(e Event) {", "$ /*", "$ + /* ` */ `"},
	"if":     {"$ { }; if true", "$ { denAny(0) }; if true", "$ //", "denAny(0); $", "//line x:0\n$ { }; if true", "$) {", "$ /*"},
	"for":    {"$ { }; for range 1", "$ //", "//line x:0\n$ { }; for range 1", "$ /*"},
	"params": {"$) {}; func evil(", "$ /*", "//line x:0\n$) {}; func evil(", "$ //"},
	"stmts": {"$\n\tdenMark()\n}\n\nfunc evil() {\n", "$\n\tdefer", "$\n\tgo", "$\n\tgoto L\nL:", "$\n}\nfunc evil() {",
		"//line x:0\n$\n}\nfunc evil() {", "$ /*", "$\n\tif true {} else"},
}

// shapeSpans are pairs of Go for the first two {expr}s of a component that
// open a literal in the first and end it in the second.
var shapeSpans = [][2]string{
	{"$ + len(/* ` */ `", "`)); denAny( /* ` */$"},
	{"//line x:0\n$); denAny(/* \" */ ` /* \" */", "//line x:0\n\" `); denAny($ /* \" */"},
	{"$ /* ` */ + `", "`); denAny($"},
}

// TestFragmentShapes puts Go straight into the fragments of a parsed
// program, past Parse and its checks (fragmentEscape), and requires that
// check rejects every escape all the same, never with an internal error or
// a panic, and that Go that changes nothing changes nothing. This is
// checkShapes on its own: what keeps the pairer from pairing the wrong Go.
func TestFragmentShapes(t *testing.T) {
	_, clean := fragmentMarks()
	// try checks the program with the fragments changed by edit, which
	// gets them in order, and returns check's error. A panic or an
	// internal error fails the test (and counts as an error here).
	try := func(edit func(i int, s fragmentSlot)) (err error) {
		f, perr := Parse("app.den", clean)
		if perr != nil {
			t.Fatal(perr)
		}
		var changed strings.Builder
		for i, s := range fragmentSlots(f) {
			was := *s.src
			edit(i, s)
			if *s.src != was {
				fmt.Fprintf(&changed, "%s fragment %d: %q\n", s.kind, i, *s.src)
			}
		}
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic: %v\n%s\n%s", r, debug.Stack(), &changed)
				err = fmt.Errorf("panic")
			}
		}()
		_, err = check([]*File{f})
		if err != nil && strings.Contains(err.Error(), "internal error") {
			t.Errorf("%v\n%s", err, &changed)
		}
		return err
	}
	with := func(at int, tmpl string) func(int, fragmentSlot) {
		return func(i int, s fragmentSlot) {
			if i == at {
				*s.src = strings.ReplaceAll(tmpl, "$", *s.src)
			}
		}
	}
	if err := try(func(int, fragmentSlot) {}); err != nil {
		t.Fatalf("the seed does not check: %v", err)
	}
	f, _ := Parse("app.den", clean)
	slots := fragmentSlots(f)
	kinds := map[string]int{}
	for i, s := range slots {
		kinds[s.kind]++
		for _, n := range []string{"$ ", "$/* c */", "/* c */$", "$\n"} {
			if n == "$\n" && s.kind != "stmts" && s.kind != "handler" {
				continue // in Go a newline ends the expression or header
			}
			if err := try(with(i, n)); err != nil {
				t.Errorf("%q in %s fragment %d (%q) fails: %v", n, s.kind, i, *s.src, err)
			}
		}
		for _, e := range shapeEscapes[s.kind] {
			if err := try(with(i, e)); err == nil {
				t.Errorf("%q in %s fragment %d (%q) checks", e, s.kind, i, *s.src)
			}
		}
		for _, n := range fragmentBreaks {
			try(with(i, "$"+n))
			try(with(i, n+"$"))
		}
	}
	for kind := range shapeEscapes {
		if kinds[kind] == 0 {
			t.Errorf("the seed has no %s fragment", kind)
		}
	}
	// Spans go across Pair's {a}{b}, and from the value of the {key}
	// block to the fragment after it, in its body.
	var pairs [][2]int
	for i, s := range slots[:len(slots)-1] {
		if s.kind == "expr" && *s.src == "a" && *slots[i+1].src == "b" || s.kind == "key" {
			pairs = append(pairs, [2]int{i, i + 1})
		}
	}
	if len(pairs) != 2 {
		t.Fatalf("the seed has %d pairs of fragments for spans, not 2", len(pairs))
	}
	for _, pair := range pairs {
		for _, sp := range shapeSpans {
			err := try(func(i int, s fragmentSlot) {
				for j, at := range pair {
					if i == at {
						*s.src = strings.ReplaceAll(sp[j], "$", *s.src)
					}
				}
			})
			if err == nil {
				t.Errorf("span %q across fragments %d checks", sp, pair)
			}
		}
	}
	// Noise before or after two or three fragments at random.
	var noise []string
	noise = append(noise, fragmentNoise...)
	for _, es := range shapeEscapes {
		for _, e := range es {
			noise = append(noise, strings.Split(e, "$")...)
		}
	}
	n := 1_000
	if testing.Short() || raceEnabled {
		n = 200
	}
	r := rand.New(rand.NewSource(1))
	for range n {
		edits := map[int]string{}
		for range 2 + r.Intn(2) {
			if r.Intn(2) == 0 {
				edits[r.Intn(len(slots))] = "$" + noise[r.Intn(len(noise))]
			} else {
				edits[r.Intn(len(slots))] = noise[r.Intn(len(noise))] + "$"
			}
		}
		try(func(i int, s fragmentSlot) {
			if e, ok := edits[i]; ok {
				*s.src = strings.ReplaceAll(e, "$", *s.src)
			}
		})
	}
}

// TestKeyBlockShape makes sure checkShapes holds gen's block for a {key}
// to its shape: a statement of its own, from gen's { to gen's }. Go in
// fragments cannot change that without failing a check that comes before
// it, so the test changes gen's own code: the block made the body of an
// if, with every offset kept, must fail.
func TestKeyBlockShape(t *testing.T) {
	f, err := Parse("app.den", "component App() {\n\tx := signal(1)\n\t<div>{key x.Get() {<p>{x.Get()}</p>}}</div>\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	g := &gen{src: &strings.Builder{}, errs: &ErrorList{}}
	g.src.WriteString("package app\n")
	g.function(f.Components[0])
	shapes := func(code string) *Error {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "", code, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%v\n%s", err, code)
		}
		return g.checkShapes(code, fset, file)
	}
	code := g.src.String()
	if err := shapes(code); err != nil {
		t.Fatalf("%v\n%s", err, code)
	}
	// The block's { follows its line directive.
	lb := strings.LastIndex(code[:strings.Index(code, "denKey(")], "{")
	d := strings.LastIndex(code[:lb], "/*line")
	iff := "if true" + strings.Repeat(" ", lb-d-len("if true"))
	if err := shapes(code[:d] + iff + code[lb:]); err == nil || strings.Contains(err.Error(), "internal error") {
		t.Errorf("the block as the body of an if: %v", err)
	}
}

// TestFragmentEscape pins what fragmentEscape leaves to Go and what it
// reports, and where.
func TestFragmentEscape(t *testing.T) {
	for _, c := range []struct {
		src  string
		k    fragKind
		at   int
		want string
	}{
		{"x)", fragExpr, 1, "unexpected ): there is no ( for it to close"},
		{"f(x])", fragExpr, -1, ""}, // the wrong kind: Go's error
		{"x}", fragExpr, -1, ""},
		{"x }", fragStmts, 2, "unexpected }: there is no { for it to close"},
		{"x)", fragStmts, -1, ""},
		{"a, b", fragProp, 1, "a prop takes one value; this , would pass another argument"},
		{"a, /* c */", fragProp, -1, ""},
		{"a, b", fragExpr, -1, ""},
		{"x // c", fragExpr, 2, "a // comment cannot end an expression in markup: it runs past the }; use /* */"},
		{"x // c\n", fragExpr, -1, ""},
		{"x // c", fragStmts, -1, ""},
		{"x /* c", fragExpr, 2, "comment not terminated before the } that ends this {...}"},
		{"x /* a\nb", fragExpr, 2, "comment not terminated before the } that ends this {...}"},
		{"x /* c", fragParams, 2, "comment not terminated before the ) that ends the parameter list"},
		{"x + `", fragHeader, 4, "raw string literal not terminated before the { that starts the body"},
		{"x); y", fragHeader, 1, "unexpected ): there is no ( for it to close"},
		{"/* ` */ `", fragExpr, 8, "raw string literal not terminated before the } that ends this {...}"},
		{"/* ` */ `\n", fragStmts, 8, "raw string literal not terminated before the } that ends this {...}"},
		{`/* " */ "`, fragProp, 8, "string literal not terminated before the } that ends this {...}"},
		{`x + "\`, fragProp, 4, "string literal not terminated before the } that ends this {...}"},
		{`"a` + "\nb", fragExpr, -1, ""}, // a newline ends it in den's file too
		// An error does not end the search: Go's scanner goes on after it.
		{`"\q" + "a`, fragExpr, 7, "string literal not terminated before the } that ends this {...}"},
		{"/* ' */ '", fragProp, 8, "rune literal not terminated before the } that ends this {...}"},
		{`'\`, fragExpr, 0, "rune literal not terminated before the } that ends this {...}"},
		{"'a\n'", fragExpr, 3, "rune literal not terminated before the } that ends this {...}"},
		{"'a\nb", fragExpr, -1, ""},
		{"1e + x)", fragExpr, 6, "unexpected ): there is no ( for it to close"},
		{"\x00 + `)` + x)", fragExpr, 11, "unexpected ): there is no ( for it to close"},
		// In den's file a fragment follows a /*line*/ comment on the same
		// line, so //line at its start is a comment, not a line directive
		// (alone it would be one, and an invalid one, an error).
		{"//line x:0\n1)", fragExpr, 12, "unexpected ): there is no ( for it to close"},
		{"//line :x\n1)", fragExpr, 11, "unexpected ): there is no ( for it to close"},
		{"//line x:0\r\n1)", fragExpr, 13, "unexpected ): there is no ( for it to close"},
		{"//line x:0\n\"t\") //", fragExpr, 14, "unexpected ): there is no ( for it to close"},
		{"//line x:0\n}", fragStmts, 11, "unexpected }: there is no { for it to close"},
		{"//line x:0\n1", fragExpr, -1, ""},
		{"x + //line x:0\n1", fragExpr, -1, ""},
		{"/*line x:0*/ 1)", fragExpr, 14, "unexpected ): there is no ( for it to close"}, // an error in den's file too
		{"'}' + `)` + \")\"", fragExpr, -1, ""},
		{"1e", fragExpr, -1, ""},
		{"x\x00", fragExpr, -1, ""},
	} {
		at, msg := fragmentEscape(c.src, c.k)
		if at != c.at || msg != c.want {
			t.Errorf("fragmentEscape(%q, %d) = %d, %q; want %d, %q", c.src, c.k, at, msg, c.at, c.want)
		}
	}
}
