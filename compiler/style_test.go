package compiler

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
)

// scopeBlock compiles the CSS of one style block as a component scoped
// d-x would, without tokens.
func scopeBlock(src string) (string, ErrorList) {
	var errs ErrorList
	s := &styler{c: &Component{File: "app.den", Style: &Style{Src: src, Pos: Pos{1, 1}}}, src: src, attr: "d-x", vars: map[string]*types.Const{}, errs: &errs}
	return s.css(), errs
}

func TestScopeSelectors(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`.a { color: red }`, `.a[d-x]{color:red}`},
		{`a b{color:red}`, `a[d-x] b[d-x]{color:red}`},
		{`a>b{color:red}`, `a[d-x]>b[d-x]{color:red}`},
		{"a \n >\t b { color: red }", `a[d-x]>b[d-x]{color:red}`},
		{`a+b, a ~ b {color:red}`, `a[d-x]+b[d-x],a[d-x]~b[d-x]{color:red}`},
		{`ul.list > li:hover a.on{color:red}`, `ul.list[d-x]>li:hover[d-x] a.on[d-x]{color:red}`},
		{`a::before{color:red}`, `a[d-x]::before{color:red}`},
		{`a:hover::after{color:red}`, `a:hover[d-x]::after{color:red}`},
		{`p:BEFORE, p:after, p:first-line, p:first-letter{color:red}`, `p[d-x]:BEFORE,p[d-x]:after,p[d-x]:first-line,p[d-x]:first-letter{color:red}`},
		{`::selection{color:red}`, `[d-x]::selection{color:red}`},
		{`input::-webkit-input-placeholder{color:red}`, `input[d-x]::-webkit-input-placeholder{color:red}`},
		{`*{margin:0}`, `*[d-x]{margin:0}`},
		{`.a > *{margin:0}`, `.a[d-x]>*[d-x]{margin:0}`},
		{`:not(.a .b) > :is(h1, h2 > a){margin:0}`, `:not(.a .b)[d-x]>:is(h1, h2 > a)[d-x]{margin:0}`},
		{`li:nth-child(2n + 1):has(> img){margin:0}`, `li:nth-child(2n + 1):has(> img)[d-x]{margin:0}`},
		{`[data-x="a b, c"] , [title='}{ x']{margin:0}`, `[data-x="a b, c"][d-x],[title='}{ x'][d-x]{margin:0}`},
		{`a[href$=".pdf" i]{margin:0}`, `a[href$=".pdf" i][d-x]{margin:0}`},
		{`a /* one */ > /* two */ b /* three */ {margin:0}`, `a[d-x]>b[d-x]{margin:0}`},
		{`.a/* x */.b, a/**/b, a/**//**/b, :is(a/* x */b, / /**/ *)::before{margin:0}`, `.a/**/.b[d-x],a/**/b[d-x],a/**/b[d-x],:is(a/**/b, / *)[d-x]::before{margin:0}`},
		{`[title="a\"]"], [x='\'']::after{margin:0}`, `[title="a\"]"][d-x],[x='\''][d-x]::after{margin:0}`},
		{`[class~="sm:p-4"]::after { content: "\201C" }`, `[class~="sm:p-4"][d-x]::after{content:"\201C"}`},
		{"p::after { content: \"a\\\r\nb\\41\r\nc\\\fd\"; }", "p[d-x]::after{content:\"a\\\r\nb\\41\r\nc\\\fd\"}"},
		{`p { content: "} { /* not a comment */"; margin : 0 ; }`, `p[d-x]{content:"} { /* not a comment */";margin:0}`},
		{`p { font: 16px / 1.5 a , b; margin: calc( 1px + 2px ) auto !important }`, `p[d-x]{font:16px / 1.5 a,b;margin:calc(1px + 2px) auto !important}`},
		{`p { background: url( "a b.png" ) , URL(img/x.png); }`, `p[d-x]{background:url("a b.png"),url(img/x.png)}`},
		{`p { --gap : 4px ; }`, `p[d-x]{--gap:4px}`},
		{`p { background-image: image-set("a.png" 1x, url(b.png) 2x, "https://x.test/c.avif" type("image/avif")); }`, `p[d-x]{background-image:image-set("a.png" 1x,url(b.png) 2x,"https://x.test/c.avif" type("image/avif"))}`},
		{`[href^="javascript:"]::after { content: "a javascript: tutorial, expression(1)" }`, `[href^="javascript:"][d-x]::after{content:"a javascript: tutorial, expression(1)"}`},
		{`p { background: url(data:image/png;base64,iVBORw0KGgo=), url("DATA:IMAGE/SVG+XML,<svg xmlns='http://www.w3.org/2000/svg'/>"); }`, `p[d-x]{background:url(data:image/png;base64,iVBORw0KGgo=),url("DATA:IMAGE/SVG+XML,<svg xmlns='http://www.w3.org/2000/svg'/>")}`},
		{`p {} a { }`, ``},
		{`@media (min-width: 40rem) and (max-width: 60rem) { .a, .b { margin: 0 } }`, `@media (min-width: 40rem) and (max-width: 60rem){.a[d-x],.b[d-x]{margin:0}}`},
		{`@MEDIA screen { @supports (display: grid) { @container (width > 1px) { a b { margin: 0 } } } }`, `@media screen{@supports (display: grid){@container (width > 1px){a[d-x] b[d-x]{margin:0}}}}`},
		{"/* a comment\n<div> with ' in it */ p { margin: 0 }\na { margin: 0 }", "p[d-x]{margin:0}\na[d-x]{margin:0}"},
	} {
		got, errs := scopeBlock(c.src)
		if len(errs) > 0 {
			t.Errorf("%s: %v", c.src, errs)
			continue
		}
		want := c.want
		if want != "" {
			want += "\n"
		}
		if got != want {
			t.Errorf("%s:\n got: %q\nwant: %q", c.src, got, c.want)
		}
	}
}

func TestKeyframes(t *testing.T) {
	src := `
a { animation: spin 1s linear infinite, other 2s; }
@keyframes spin { from { transform: rotate(0) } 50%, 75.5% { opacity: .5 } TO { transform: rotate(1turn) } }
b { animation-name: spin-slow, spin; -webkit-animation: 1s spin; transition: spin 1s; }
@media (prefers-reduced-motion: no-preference) { @keyframes spin-slow { to { opacity: 1 } } }
c { animation: var(--spin) 1s; content: "spin"; }
d { animation: spin 1s ease-in-out infinite alternate both, ease 2s; }
`
	want := `a[d-x]{animation:spin-d-x 1s linear infinite,other 2s}
@keyframes spin-d-x{from{transform:rotate(0)}50%,75.5%{opacity:.5}to{transform:rotate(1turn)}}
b[d-x]{animation-name:spin-slow-d-x,spin-d-x;-webkit-animation:1s spin-d-x;transition:spin 1s}
@media (prefers-reduced-motion: no-preference){@keyframes spin-slow-d-x{to{opacity:1}}}
c[d-x]{animation:var(--spin) 1s;content:"spin"}
d[d-x]{animation:spin-d-x 1s ease-in-out infinite alternate both,ease 2s}
`
	got, errs := scopeBlock(src)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Two components never share a scope, and each keeps its own from one
// build to the next.
func TestScopeNames(t *testing.T) {
	seen := map[string]string{}
	var a, b string
	for i := 0; i < 1_000_000 && a == ""; i++ {
		name := fmt.Sprintf("C%d", i)
		scope := scopeNames([]*Component{{Name: name}})
		for _, s := range scope {
			if prev, ok := seen[s]; ok {
				a, b = prev, name
			}
			seen[s] = name
		}
	}
	if a == "" {
		t.Fatal("no two names hash alike; the collision path is untested")
	}
	ca, cb := &Component{Name: a}, &Component{Name: b}
	for _, order := range [][]*Component{{ca, cb}, {cb, ca}} {
		got := scopeNames(order)
		if got[ca] == got[cb] {
			t.Fatalf("%s and %s share %s", a, b, got[ca])
		}
		if want := scopeNames([]*Component{ca})[ca]; got[ca] != want {
			t.Errorf("%s: %s, want %s (the first in name order keeps its name)", a, got[ca], want)
		}
		if len(got[ca]) != 7 || len(got[cb]) != 7 || !strings.HasPrefix(got[cb], "d-") {
			t.Errorf("bad scope names %q %q", got[ca], got[cb])
		}
	}
}

func TestCheckToken(t *testing.T) {
	ok := map[string][]string{
		"Color": {"#fff", "#FFFA", "#a0b1c2", "#a0b1c2ff", "red", "RebeccaPurple", "transparent", "currentColor",
			"rgb(0 0 0 / 0.1)", "rgba(0,0,0,.5)", "hsl(200 50% 50%)", "oklch(25% 0.02 260)", "color-mix(in oklch, red 40%, blue)", "color(display-p3 1 0 0)"},
		"Length":   {"0", "16px", "-4px", "+1.5rem", ".5em", "1e3px", "1.5E-1rem", "-2e+1em", "1ex", "100%", "50vw", "10dvh", "2cqi", "calc(100% - 2rem)", "clamp(1rem, 2vw, 3rem)", "min(10px, 1em)"},
		"Duration": {"150ms", "1s", ".2s", "0s", "-1s", "1e3ms", "1.5E-1S"},
		"Font":     {"system-ui", "system-ui, sans-serif", `ui-sans-serif, -apple-system, "Segoe UI", sans-serif`, "'Fira Code', monospace", "Times New Roman"},
		"Token":    {"0 1px 2px rgb(0 0 0 / 0.1)", "1.5", "bold", `"a b"`},
	}
	bad := map[string][]string{
		"Color":    {"#ff", "#ggg", "blu", "rgb(0 0 0", "rgb(0 0 0) red", "evil(1)", "var(--x)", ""},
		"Length":   {"16", "px", "1.5.5px", "16 px", "--4px", "calc(1px", "calc(1px) 2px", "auto", "1e3", "e3px", "1e+px", "1.px"},
		"Duration": {"150", "ms", "1 s", "fast", "1es", "1e", "1e3"},
		"Font":     {"a,,b", `"unclosed`, `"a"b"`, "1font", "a, "},
		"Token":    {"'a\fb'", "a\tb", "\x00", "a\x7f", "a\x1b", "JAVA script:x", "a;b", `"a;b"`, "a{", "}", "<x", `a\b`, "url(x)", "EXPRESSION(1)", "a /* b", "a\nb", "(a", `"a`, "  "},
	}
	for kind, vs := range ok {
		for _, v := range vs {
			if msg := checkToken(kind, v); msg != "" {
				t.Errorf("%s %q: %s", kind, v, msg)
			}
		}
	}
	for kind, vs := range bad {
		for _, v := range vs {
			if checkToken(kind, v) == "" {
				t.Errorf("%s %q passed", kind, v)
			}
		}
	}
}

func TestKebab(t *testing.T) {
	for _, name := range []string{"Space4", "space4", "RadiusLg", "Surface", "AccentInk", "H1Size", "Shadow12"} {
		k := kebab(name)
		camel, ok := tokenCamel(k)
		if !ok || !strings.EqualFold(camel, name) || camel[1:] != name[1:] {
			t.Errorf("%s -> %s -> %s", name, k, camel)
		}
	}
	if k := kebab("RadiusLg"); k != "radius-lg" {
		t.Errorf("kebab(RadiusLg) = %s", k)
	}
}

func TestTokens(t *testing.T) {
	res, err := compileOne(`
const (
    Surface  den.Color    = " #ffffff "
    Ink      den.Color    = "oklch(25% 0.02 260)"
    Space4   den.Length   = "16px"
    RadiusLg den.Length   = "12px"
    Fast     den.Duration = "150ms"
    Body     den.Font     = "system-ui, sans-serif"
    Shadow1  den.Token    = "0 1px 2px rgb(0 0 0 / 0.1)"
    Unused   den.Color    = "red"
)

type Brand = den.Color

const accent Brand = "#00758f"

component Card(title string, children den.Children) {
    style {
        .card { padding: $space-4; border-radius: $radius-lg; background: $surface; box-shadow: $shadow-1; }
        .card h2 { color: $ink; font-family: $body; transition: color $fast; border: 1px solid $accent; }
        @media (min-width: 40rem) { .card { padding: calc($Space-4 * 2); margin: $space4; } }
    }
    <section class="card"><h2>{title}</h2>{children}</section>
}

component App() {
    <Card title="x">y</Card>
}`)
	if err != nil {
		t.Fatal(err)
	}
	card := scopeNames([]*Component{{Name: "Card"}})
	var scope string
	for _, s := range card {
		scope = s
	}
	want := `:root{--accent:#00758f;--body:system-ui, sans-serif;--fast:150ms;--ink:oklch(25% 0.02 260);--radius-lg:12px;--shadow-1:0 1px 2px rgb(0 0 0 / 0.1);--space-4:16px;--surface:#ffffff}
.card[d-x]{padding:var(--space-4);border-radius:var(--radius-lg);background:var(--surface);box-shadow:var(--shadow-1)}
.card[d-x] h2[d-x]{color:var(--ink);font-family:var(--body);transition:color var(--fast);border:1px solid var(--accent)}
@media (min-width: 40rem){.card[d-x]{padding:calc(var(--space-4) * 2);margin:var(--space-4)}}
`
	if want = strings.ReplaceAll(want, "d-x", scope); res.CSS != want {
		t.Errorf("got:\n%s\nwant:\n%s", res.CSS, want)
	}
	// Tokens alone, without a style block, make no CSS.
	res, err = compileOne(`
const Ink den.Color = "#000"
component App() {
    <p>{string(Ink)}</p>
}`)
	if err != nil || res.CSS != "" {
		t.Errorf("CSS %q, err %v", res.CSS, err)
	}
}

// A style block is cut out of the Go statements without moving them, so
// Go errors after it are still reported where they are.
func TestStyleBlockKeepsPositions(t *testing.T) {
	_, err := compileOne(`
component App() {
    a := 1
    style {
        p { color: red; }
        a::after { content: "it's"; }
    }
    b := nope
    <p>{a}{b}</p>
}`)
	if err == nil || !strings.Contains(err.Error(), "app.den:8:10: undefined: nope") {
		t.Fatalf("got %v", err)
	}
}

// style is still an ordinary Go name: only a statement that starts with
// `style {` starts a style block.
func TestGoNamedStyle(t *testing.T) {
	got := runApp(t, `
type style struct {
    n int
}

func get(s style) int { return s.n }

component App() {
    n :=
        style{n: 5}.n
    style := style{n: 1}
    style.n++
    k :=
        get(
            style)
    m := get(style) +
    2
    x := struct{ style int }{
        style: 3,
    }
    <p>{n} {style.n} {k} {m} {x.style}</p>
}`, `log(html());`)
	expectLines(t, got, `<p>5 2 2 4 3</p>`)
}

// A statement line that starts `style {` opens a style block, even where
// Go would read a composite literal of a type named style; in parentheses
// the literal is Go again. The error says so only when the program has
// its own style.
func TestStyleLiteralStatements(t *testing.T) {
	got := runApp(t, `
type style []int
type look struct{ F func() int }

func two() int { return 2 }

component App() {
    s := style{1, 2}
    (style{1, 2})[0] = 5
    n := (look{F: two}).F()
    (look{F: two}).F()
    <p>{s[0]} {n} {len(style{3})}</p>
}`, `log(html());`)
	expectLines(t, got, `<p>1 2 1</p>`)
	_, err := compileOne(`
component App() {
    style{
        1, // it's
        2,
    }[0] = 5
    <p>x</p>
}`)
	if err == nil || !strings.Contains(err.Error(), "app.den:4:17: unterminated string in style block") || strings.Contains(err.Error(), literalHint) {
		t.Errorf("got %v", err)
	}
	// The program's own style may be in another file, or an import.
	for _, other := range []string{"type style []int", `import style "strings"
func f() string { return style.ToUpper("x") }`} {
		_, err = Compile([]Source{{Name: "a.den", Code: other + "\n"}, {Name: "b.den", Code: `
component App() {
    style{ 1, "x }
    <p>x</p>
}`}})
		if err == nil || !strings.Contains(err.Error(), "b.den:3:15: unterminated string in style block; "+literalHint) {
			t.Errorf("%s: got %v", other, err)
		}
	}
}

// Elements from a component's own markup carry its scope; the elements
// of the components it uses carry theirs, or none; markup it passes as
// children carries its scope, wherever it is rendered.
func TestRunScopedStyles(t *testing.T) {
	src := `
component App() {
    on := signal(true)
    items := signal([]int{1, 2})
    cls := signal("a")
    style { p { margin: 0; } }
    <div class={cls.Get()}>
        <p>top</p>
        <Card><i>slot {len(items.Get())}</i></Card>
        <Plain><i>plain</i></Plain>
        {if on.Get() { <b>if</b> }}
        <ul>{for _, x := range items.Get() key x { <li onclick={items.Set([]int{x})}>{x}</li> }}</ul>
        <button onclick={cls.Set("b")}>class</button>
    </div>
}

component Card(children den.Children) {
    style { section { margin: 0; } }
    <section><h2>card</h2>{children}<svg><circle r="1"/></svg></section>
}

component Plain(children den.Children) {
    <span>{children}</span>
}`
	res, err := compileOne(src)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.CSS, "span") || !strings.Contains(res.CSS, "section[") {
		t.Errorf("CSS:\n%s", res.CSS)
	}
	scopes := scopeNames([]*Component{{Name: "App"}, {Name: "Card"}})
	var app, card string
	for c, s := range scopes {
		if c.Name == "App" {
			app = s
		} else {
			card = s
		}
	}
	got := runApp(t, src, `
log(html());
click(find("button", "class")); await tick();
click(find("li", "2")); await tick();
log(html());`)
	got = strings.ReplaceAll(strings.ReplaceAll(got, app, "APP"), card, "CARD")
	expectLines(t, got,
		`<div APP="" class="a"><p APP="">top</p><section CARD=""><h2 CARD="">card</h2><i APP="">slot 2</i><svg CARD=""><circle r="1" CARD=""></circle></svg></section><span><i APP="">plain</i></span><b APP="">if</b><ul APP=""><li den-click="0" APP="">1</li><li den-click="0" APP="">2</li></ul><button APP="">class</button></div>`,
		`<div APP="" class="b"><p APP="">top</p><section CARD=""><h2 CARD="">card</h2><i APP="">slot 1</i><svg CARD=""><circle r="1" CARD=""></circle></svg></section><span><i APP="">plain</i></span><b APP="">if</b><ul APP=""><li den-click="0" APP="">2</li></ul><button APP="">class</button></div>`)
}

// The CSS of a style block must never change how the CSS after it reads.
// styleCase compiles a component whose style block holds body, with the
// design tokens $t, $c, $l, $d and $f (a den.Token, Color, Length,
// Duration and Font) set to toks. A compile error is fine. Otherwise the
// CSS must have passed the build's own check (an internal error is a
// failure: den's scanner and the CSS tokenizer disagreed), and with a
// sentinel rule after it, as the app's style.css would be, the tokenizer
// must read the sentinel as a rule of its own, with every rule before it
// narrowed to the component. It returns whether the block compiled.
func styleCase(t testing.TB, body string, toks [5]string) bool {
	t.Helper()
	src := "component App() {\n\tstyle {\n" + body + "\n\t}\n\t<p>x</p>\n}\n"
	f, err := Parse("app.den", src)
	if err != nil {
		return false
	}
	den := types.NewPackage("den", "den")
	pkg := types.NewPackage("main", "main")
	info := &types.Info{Defs: map[*ast.Ident]types.Object{}}
	for i, kind := range []string{"Token", "Color", "Length", "Duration", "Font"} {
		typ := types.NewNamed(types.NewTypeName(token.NoPos, den, kind, nil), types.Typ[types.String], nil)
		name := kind[:1]
		c := types.NewConst(token.NoPos, pkg, name, typ, constant.MakeString(toks[i]))
		pkg.Scope().Insert(c)
		info.Defs[ast.NewIdent(name)] = c
	}
	ck := &checked{fset: token.NewFileSet(), info: info, pkg: pkg}
	for _, c := range f.Components {
		ck.comps = append(ck.comps, &checkedComponent{c: c})
	}
	scopes, css, errs := styles(ck)
	for _, e := range errs {
		if strings.Contains(e.Msg, "internal error") {
			t.Fatalf("%v\nbody: %q\ntokens: %q", e, body, toks)
		}
	}
	if len(errs) > 0 {
		return false
	}
	if msg := sentinelCheck(css, scopes[f.Components[0]]); msg != "" {
		t.Fatalf("%s\nbody: %q\ntokens: %q\nCSS: %q", msg, body, toks, css)
	}
	return true
}

// sentinelCheck reads css, with a sentinel rule after it, as a browser
// would, and returns what is wrong.
func sentinelCheck(css, scope string) string {
	items, problem := readCSS(css)
	if problem != "" {
		return "the CSS does not read as written: " + problem
	}
	const sentinel = ".sentinel{color:red}"
	all, problem := readCSS(css + sentinel)
	if problem != "" {
		return "the CSS and the sentinel do not read as written: " + problem
	}
	if last := all[len(all)-1]; len(all) != len(items)+1 || !last.rule || last.text != ".sentinel" ||
		len(last.items) != 1 || last.items[0].text != "color:red" {
		return "the sentinel rule is not read as a rule of its own"
	}
	var scoped func(items []cssItem, top bool) string
	scoped = func(items []cssItem, top bool) string {
		for _, it := range items {
			switch {
			case !it.rule:
				return "a declaration outside every rule: " + it.text
			case strings.HasPrefix(it.text, "@keyframes "):
			case strings.HasPrefix(it.text, "@"):
				if msg := scoped(it.items, false); msg != "" {
					return msg
				}
			case top && it.text == ":root":
			case !strings.Contains(it.text, "["+scope+"]"):
				return "a rule that is not narrowed to the component: " + it.text
			}
		}
		return ""
	}
	return scoped(items, true)
}

// Pieces of style blocks: mostly CSS, some of it broken.
var (
	fuzzSelectors = []string{".a", ".b", "p", "a", "li", "*", "#i", "ul > li", "a + b", "a ~ b", "a b", ".a.b",
		"::before", ":after", ":hover", "::selection", ":not(.x)", ":is(.a, .b)", ":where(p)", ":has(> img)",
		":nth-child(2n + 1)", "[x]", `[title="a b"]`, `[x='}{']`, `[href$=".pdf" i]`, `[x="\"]"]`, ":first-line",
		"input::-webkit-input-placeholder", ".a/**/.b", "a /* c */ b", "_x", "-x", "é", "/", "*", "/**/", "a//**/*",
		":is(//**/*)", "u/**/rl(x)", `[href^="javascript:"]`}
	fuzzProps = []string{"color", "margin", "padding", "content", "background", "background-image", "animation",
		"animation-name", "font-family", "font", "transition", "--x", "--y", "width", "border-color", "box-shadow", "quotes"}
	fuzzValues = []string{"red", "1px", "0", "auto", "solid", "1px solid red", `"s"`, `'s'`, `"a;b}"`, `"\"" `, `'\''`,
		"\"a\\\nb\"", `"\41 "`, "url(a.png)", `url("b.png")`, "url( c.png )", `image-set("a.png" 1x, url(b.png) 2x)`,
		`src("x.woff")`, "calc(1px + 2px)", "var(--x)", "#fff", "rgb(0 0 0 / .5)", "!important", ",", "/", "attr(x)",
		"counter(c)", "$t", "$t", "$t", "$c", "$l", "spin 1s linear", "fade", "a/**/b", "u/**/rl(x)", "#url(x)",
		"@url(x)", "_url(x)", "-url(x)", "@url(a/*b)", "#url(a/*b)", "<!--", "<!--url(a/*b)", "[x]", "1e3", "-1.5e-2px", "50%", "a-->b", "0 0 1px $c", "\"<!--\"", "\"/*\"",
		"'}'", "\"\\\r\n\"", "\"\\41\r\nx\"", "a\fb", "a\r\nb", "\"é😀\"", "é", "\u2028", "--a", "-->",
		"url(data:image/png;base64,iVBORw0KGgo=)", `url("DATA:IMAGE/SVG+XML,<svg xmlns='http://www.w3.org/2000/svg'/>")`,
		`image-set("data:image/gif;base64,R0lGOD" 1x)`, "url(data:text/html,x)", `"a javascript: tutorial"`,
		`'expression(1)'`, `url("javascript:x")`, "{}"}
	fuzzNoise = []string{"'", "\"", "(", ")", "[", "]", "{", "}", ";", ":", ",", "\\", "/*", "*/", "/", "*", "<!--",
		"-->", "<", ">", "@", "@media", "@keyframes x", "$", "$t", "$d", "$f", "url(", "URL(", "u", "rl(", "\f", "\r",
		"\n", "\r\n", "\t", " ", "\x00", "\x01", "\x0b", "\x7f", "é", "\u2028", "\u00a0", "😀", "\xff", "#", "%", "-",
		"_", "&", "!", "e", "1", ".", "\\\n", "\\31 ", "\"\\", "'\\\r\n'", "/**/", "\"\n", "}{", "){", "javascript:",
		"expression(", "image-set(", "src(", "\\)", "\\]", "\\}", "\\{"}
	// Token values that pass checkToken, then ones that do not.
	fuzzTokens = [5][]string{
		{"0 1px 2px red", "1.5", "bold", `"a b"`, "'x'", "fade", `"x.png"`, "'a' 'b'", `"a'b"`, "a-->b", "é", "rgb(0 0 0)"},
		{"red", "#fff", "rgb(0 0 0 / .1)", "oklch(25% 0.02 260)", "transparent", "currentColor"},
		{"0", "16px", "-1.5rem", "calc(100% - 2px)", "clamp(1rem, 2vw, 3rem)", "1e3px", "1.5E-1rem"},
		{"150ms", "1s", "-.5s", "1e3ms", "2E-1S"},
		{"system-ui", "system-ui, sans-serif", `"Segoe UI", serif`, "'a b'"},
	}
	fuzzBadTokens = []string{"", "a;b", "a}", `"a`, "(a", "a)", "x\fy", "url(x)", "<x", `\`, "a/*b", "nope", "#ff",
		"rgb((0)", "16", "calc(1px", "fast", "1 s", "a,,b", "1x", "'\"'", "[a", "javascript:x"}
)

func pick(r *rand.Rand, xs []string) string { return xs[r.Intn(len(xs))] }

// fuzzBlock writes a style block body from the grammar, then breaks it
// with noise.
func fuzzBlock(r *rand.Rand) string {
	var b strings.Builder
	decls := func() {
		for n := r.Intn(4); n > 0; n-- {
			b.WriteString(pick(r, fuzzProps) + ": ")
			for m := 1 + r.Intn(3); m > 0; m-- {
				b.WriteString(pick(r, fuzzValues) + " ")
			}
			b.WriteString("; ")
		}
	}
	var rule func(depth int)
	rule = func(depth int) {
		switch k := r.Intn(10); {
		case k < 6 || depth > 1:
			for n := 1 + r.Intn(2); n > 0; n-- {
				b.WriteString(pick(r, fuzzSelectors))
				if n > 1 {
					b.WriteString(pick(r, []string{", ", " ", " > ", ""}))
				}
			}
			b.WriteString(" { ")
			decls()
			b.WriteString("}\n")
		case k < 8:
			b.WriteString(pick(r, []string{"@media (min-width: 1px)", "@supports (display: grid)", "@container (width > 1px)", "@MEDIA screen and (color)"}) + " { ")
			for n := 1 + r.Intn(2); n > 0; n-- {
				rule(depth + 1)
			}
			b.WriteString("}\n")
		default:
			b.WriteString("@keyframes " + pick(r, []string{"spin", "fade", "auto", "a_b"}) + " { from { ")
			decls()
			b.WriteString("} 50% { opacity: .5 } to { ")
			decls()
			b.WriteString("} }\n")
		}
	}
	for n := 1 + r.Intn(3); n > 0; n-- {
		rule(0)
	}
	s := b.String()
	for n := max(0, r.Intn(6)-2); n > 0; n-- {
		at := r.Intn(len(s) + 1)
		cut := at
		if r.Intn(3) == 0 {
			cut = min(len(s), at+r.Intn(4))
		}
		s = s[:at] + pick(r, fuzzNoise) + s[cut:]
	}
	return s
}

func fuzzToks(r *rand.Rand) [5]string {
	var toks [5]string
	for i, vs := range fuzzTokens {
		toks[i] = pick(r, vs)
	}
	// Every token is checked, used or not, so one bad value is an error.
	switch r.Intn(6) {
	case 0:
		toks[r.Intn(5)] = pick(r, fuzzBadTokens)
	case 1:
		toks[0] += pick(r, fuzzNoise)
	}
	return toks
}

// TestStyleProperty runs styleCase on blocks made from a grammar and
// broken with noise. DEN_STYLE_CASES=n runs n of them; FuzzStyle runs
// more under go test -fuzz.
func TestStyleProperty(t *testing.T) {
	n := 25_000
	if testing.Short() {
		n = 2_000
	}
	if s := os.Getenv("DEN_STYLE_CASES"); s != "" {
		n, _ = strconv.Atoi(s)
	}
	r := rand.New(rand.NewSource(1))
	compiled := 0
	for i := 0; i < n; i++ {
		if styleCase(t, fuzzBlock(r), fuzzToks(r)) {
			compiled++
		}
	}
	// Most cases are errors; enough must compile for the test to mean
	// something.
	if compiled < n/10 {
		t.Errorf("only %d of %d cases compiled", compiled, n)
	}
	t.Logf("%d cases, %d compiled", n, compiled)
}

// readCSS must read CSS as a browser does: it is what catches den's own
// mistakes.
func TestReadCSS(t *testing.T) {
	good := []struct {
		css   string
		rules []cssRule
	}{
		{"@media (a: b){.a[d-x]{b:c;d:e}.f[d-x]{g:h}}\n.i[d-x]{j:\"k;}\"}\n", []cssRule{
			{prelude: "@media (a: b)", rules: []cssRule{{prelude: ".a[d-x]", decls: []string{"b:c", "d:e"}}, {prelude: ".f[d-x]", decls: []string{"g:h"}}}},
			{prelude: ".i[d-x]", decls: []string{`j:"k;}"`}}}},
		{".a[d-x]{b:f(;{});c:[x]}\n", []cssRule{{prelude: ".a[d-x]", decls: []string{"b:f(;{})", "c:[x]"}}}},
		{".a[d-x]{content:\"a\\\nb\\41\r\nc\\\r\nd\";x:url(a/*b)}\n", []cssRule{{prelude: ".a[d-x]", decls: []string{"content:\"a\\\nb\\41\r\nc\\\r\nd\"", "x:url(a/*b)"}}}},
		{"@keyframes a-d-x{from{}to{b:c}}\n:root{--a:;--b:x}\n", []cssRule{
			{prelude: "@keyframes a-d-x", rules: []cssRule{{prelude: "from"}, {prelude: "to", decls: []string{"b:c"}}}},
			{prelude: ":root", decls: []string{"--a:", "--b:x"}}}},
		{".a/**/.b[d-x],a[d-x]>b[d-x]{c:-->}\n", []cssRule{{prelude: ".a/**/.b[d-x],a[d-x]>b[d-x]", decls: []string{"c:-->"}}}},
	}
	for _, c := range good {
		if items, problem := readCSS(c.css); problem != "" || !sameRules(c.rules, items) {
			t.Errorf("%q: %s %+v", c.css, problem, items)
		}
	}
	// CSS that breaks the CSS after it, among it what den once wrote for
	// blocks with backslash escapes.
	bad := []string{
		".a[x\\][d-x]{color:red}\n.z[d-x]{color:blue}\n",
		":is(.a\\)[d-x] .b[d-x]{color:red}\n.z[d-x]{color:blue}\n",
		"@media (min-width: 1px){.a[d-x]::after\\{color:red}}\n.z[d-x]{color:blue}\n",
		".a[d-x]::before\\{color:red}\n.z[d-x]{color:blue}\n",
		":root{--t:'a\fb'}\n",
		".a[d-x]{content:\"a\\41\"}\n.b[d-x]{content:\"\n\"}\n",
		".a[d-x]{b:url(a b)}\n",
		".a[d-x]{b:u\\72l(a\"b)}\n",
		".a[d-x]{b:url(a\\\n)}\n",
		"<!--.a[d-x]{b:c}\n",
		"-->.a[d-x]{b:c}\n",
		".a[d-x]{b:c}}\n",
		".a[d-x]{b:c\n",
		".a[d-x]{b:(]}\n",
		".a[d-x]{b:c}/*\n",
		".a[d-x]{b:\"c}\n",
	}
	for _, css := range bad {
		if msg := sentinelCheck(css, "d-x"); msg == "" {
			t.Errorf("%q passed", css)
		}
	}
}

// The blocks that broke style.css before, each in one of the ways found.
var styleRegressions = []string{
	`.a[x\] { color: red; }`,
	`.a[title=x\] { color: red; }`,
	`.a::before\, .b { color: red; }`,
	`.\31 23::before, .a\:b { color: red; }`,
	`:is(.a\) .b { color: red; }`,
	`@media (min-width: 1px) { .a::after\{ color: red; } }`,
	`.a:not(.b\) { color: red; }`,
	`.a::before\{ color: red; }`,
	`.a:after\{ color: red; }`,
	".a::after { content: \"x\fy\"; }",
	".a::after { content: \"x\\\fy\"; }",
	".a\\\f.b { color: red; }",
	`@keyframes auto { to { opacity: 1 } } .a { animation: auto 1s; }`,
	`.a { background-image: image-set($t 1x); }`,
	`.a { quotes: $t; }`,
	`.a//**/* { color: red; }`,
	`:is(//**/*) { color: red; }`,
	`.a { background: @url(a/*b); }`,
	`.a { background: url(a b); }`,
	`.x[(]) ] { color: red; }`,
}

func TestStyleRegressions(t *testing.T) {
	compiled := 0
	defer func() {
		if compiled == 0 {
			t.Error("no case compiled, so none was read back")
		}
	}()
	for _, body := range styleRegressions {
		for _, tok := range []string{`"x.png"`, "'a\fb'", "'a'"} {
			body := body + "\n.z { color: blue; }"
			styleCase(t, body, [5]string{tok, "red", "1px", "1s", "serif"})
			// And through the whole compiler.
			res, err := compileOne("const T den.Token = " + strconv.Quote(tok) + "\ncomponent App() {\n\tstyle {\n" + body + "\n\t}\n\t<p>x</p>\n}\n")
			switch {
			case err != nil && strings.Contains(err.Error(), "internal error"):
				t.Errorf("%q: %v", body, err)
			case err == nil:
				compiled++
				app := &Component{Name: "App"}
				if msg := sentinelCheck(res.CSS, scopeNames([]*Component{app})[app]); msg != "" {
					t.Errorf("%q: %s", body, msg)
				}
			}
		}
	}
}

func FuzzStyle(f *testing.F) {
	for _, body := range styleRegressions {
		f.Add(body, `"x"`)
	}
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 50; i++ {
		f.Add(fuzzBlock(r), fuzzToks(r)[0])
	}
	f.Fuzz(func(t *testing.T, body, tok string) {
		styleCase(t, body, [5]string{tok, "red", "1px", "1s", "serif"})
	})
}
