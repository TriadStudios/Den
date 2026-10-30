package compiler

import (
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func compileOne(src string) (*Result, error) {
	return Compile([]Source{{Name: "app.den", Code: src}})
}

func TestCompilesCounter(t *testing.T) {
	res, err := compileOne(`
component App() {
    count := signal(0)
    <button onclick={count.Set(count.Get() + 1)}>Clicked {count.Get()} times</button>
}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`$signal(0)`, `$text($e1, () => count.get())`, `count.set((count.get() + 1))`, `a0.append("Clicked ", "", " times")`} {
		if !strings.Contains(res.JS, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

// Each case must fail with the given message at the given line:col of app.den.
func TestErrors(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"type mismatch in handler", `
component App() {
    count := signal(0)
    <button onclick={count.Set("hi")}>{count.Get()}</button>
}`, `app.den:4:32: cannot use "hi"`},
		{"undefined name in text", `
component App() {
    <p>{nope}</p>
}`, `app.den:3:9: undefined: nope`},
		{"struct is not renderable", `
component App() {
    s := signal(struct{ A int }{1})
    <p>{s.Get()}</p>
}`, `app.den:4:9: `},
		{"unused variable", `
component App() {
    unused := 5
    <p>hi</p>
}`, `app.den:3:5: declared and not used: unused`},
		{"wrong prop type", `
component App() {
    <Card title={42} />
}
component Card(title string) {
    <h2>{title}</h2>
}`, `app.den:3:18: cannot use 42`},
		{"missing prop", `
component App() {
    <Card />
}
component Card(title string) {
    <h2>{title}</h2>
}`, `app.den:3:5: <Card> is missing prop "title" (string)`},
		{"unknown prop", `
component App() {
    <Card title="a" colour="red" />
}
component Card(title string) {
    <h2>{title}</h2>
}`, `app.den:3:21: <Card> has no prop "colour"`},
		{"inline js handler", `
component App() {
    <button onclick="alert(1)">x</button>
}`, `app.den:3:13: event handler "onclick" must be Go code`},
		{"javascript url", `
component App() {
    <a href="javascript:alert(1)">x</a>
}`, `app.den:3:8: unsafe URL in href`},
		{"obfuscated javascript url", "\ncomponent App() {\n    <a href=\"java\tscript:alert(1)\">x</a>\n}", `app.den:3:8: unsafe URL in href`},
		{"raw html", `
component App() {
    <div innerHTML={"<b>x</b>"}></div>
}`, `app.den:3:10: "innerHTML" is not allowed`},
		{"script tag", `
component App() {
    <div><script>alert(1)</script></div>
}`, `app.den:3:10: <script> is not allowed`},
		{"mismatched tag", `
component App() {
    <div><p>x</div>
}`, `app.den:3:14: </div> does not match <p>`},
		{"for without key", `
component App() {
    xs := signal([]int{1})
    <ul>{for _, x := range xs.Get() { <li>{x}</li> }}</ul>
}`, `app.den:4:10: for in markup needs a key`},
		{"struct key", `
type P struct{ A int }
component App() {
    <ul>{for _, p := range []P{} key p { <li>{p.A}</li> }}</ul>
}`, `app.den:4:38: list keys must be strings, numbers or bools`},
		{"range over string in markup", `
component App() {
    <ul>{for _, r := range "abc" key r { <li>{r}</li> }}</ul>
}`, `app.den:3:28: for in markup can range over slices, arrays, maps and ints`},
		{"three-clause for in markup", `
component App() {
    <ul>{for i := 0; i < 3; i++ key i { <li>{i}</li> }}</ul>
}`, `app.den:3:10: for in markup must range`},
		{"unused loop variable", `
component App() {
    <ul>{for i, x := range []int{} key x { <li>{x}</li> }}</ul>
}`, `app.den:3:14: declared and not used: i`},
		{"if needs bool", `
component App() {
    <p>{if 1 { yes }}</p>
}`, `app.den:3:12: non-boolean condition in if statement`},
		{"method", `
type T struct{}
func (t T) Name() string { return "" }
component App() {
    <p>x</p>
}`, `app.den:3:1: methods are not supported yet`},
		{"package var", `
var count = 1
component App() {
    <p>x</p>
}`, `app.den:2:1: package-level variables are not supported yet`},
		{"import outside the curated set", `
import "fmt"
component App() {
    <p>x</p>
}`, `app.den:2:8: package "fmt" is not available in den; you can import strconv, strings`},
		{"unused import", `
import (
    "strings"
)
component App() {
    <p>x</p>
}`, `app.den:3:5: "strings" imported and not used`},
		{"value that cannot be stored", `
component App() {
    s := stored("k", signal(1))
    <p>{s.Get().Get()}</p>
}`, `app.den:3:10: cannot store *Signal[int] in localStorage`},
		{"struct map key", `
type P struct{ A int }
func f(m map[P]int) int { return len(m) }
component App() {
    <p>{f(nil)}</p>
}`, `app.den:3:10: map keys must be strings, numbers or bools, not P`},
		{"map compared with nil", `
func f(m map[string]int) bool { return m == nil }
component App() {
    <p>{f(nil)}</p>
}`, `app.den:2:40: comparing a map[string]int with nil is not supported`},
		{"script tag in a block", `
component App() {
    <div>{if true { <script>alert(1)</script> }}</div>
}`, `app.den:3:21: <script> is not allowed`},
		{"inline js in a list row", `
component App() {
    <ul>{for i := range 3 key i { <li onclick="alert(1)">{i}</li> }}</ul>
}`, `app.den:3:39: event handler "onclick" must be Go code`},
		{"no App", `
component Home() {
    <p>x</p>
}`, `no App component`},
		{"children for a component without a children param", `
component App() {
    <Card title="a"><p>x</p></Card>
}
component Card(title string) {
    <h2>{title}</h2>
}`, `app.den:3:5: <Card> does not take children: it has no children den.Children parameter`},
		{"unknown slot", `
component App() {
    <Card><:foot>x</:foot></Card>
}
component Card(head den.Children) {
    <h2>{head}</h2>
}`, `app.den:3:11: <Card> has no slot "foot"`},
		{"slot that is not den.Children", `
component App() {
    <Card title="a"><:title>x</:title></Card>
}
component Card(title string) {
    <h2>{title}</h2>
}`, `app.den:3:21: <Card> has no slot "title"`},
		{"slot given twice", `
component App() {
    <Card><:head>a</:head><:head>b</:head></Card>
}
component Card(head den.Children) {
    <h2>{head}</h2>
}`, `app.den:3:27: <Card> slot "head" given twice`},
		{"slot outside a component", `
component App() {
    <div><:head>x</:head></div>
}`, `app.den:3:10: <:head> must be directly inside a component element`},
		{"slot in a block inside a component", `
component App() {
    <Card>{if true { <:head>x</:head> }}</Card>
}
component Card(head den.Children) {
    <h2>{head}</h2>
}`, `app.den:3:22: <:head> must be directly inside a component element`},
		{"children slot", `
component App() {
    <Card><:children>x</:children></Card>
}
component Card(children den.Children) {
    <div>{children}</div>
}`, `app.den:3:11: <:children> is not a slot: put default content directly between <Card> and </Card>`},
		{"children passed as an attribute", `
component App() {
    <Card children={1} />
}
component Card(children den.Children) {
    <div>{children}</div>
}`, `app.den:3:11: children of <Card> go between <Card> and </Card>, not in an attribute`},
		{"slot passed as an attribute", `
component Outer(head den.Children) {
    <Card head={head} />
}
component App() {
    <Outer />
}
component Card(head den.Children) {
    <div>{head}</div>
}`, `app.den:3:11: slot "head" of <Card> is filled with <:head>...</:head> between the tags, not with an attribute`},
		{"attribute on a slot", `
component App() {
    <Card><:head class="x">a</:head></Card>
}
component Card(head den.Children) {
    <h2>{head}</h2>
}`, `app.den:3:18: slot <:head> takes no attributes`},
		// go/types words the rest differently from one Go release to the next.
		{"calling children", `
component Card(children den.Children) {
    <div>{children()}</div>
}
component App() {
    <Card>x</Card>
}`, `app.den:3:11: invalid operation: cannot call`},
		{"comparing children with nil", `
component Card(children den.Children) {
    <div>{if children == nil { x }}</div>
}
component App() {
    <Card>x</Card>
}`, `app.den:3:26: invalid operation: children == nil (mismatched types den.Children and untyped nil)`},
		{"comparing children", `
func same(a, b den.Children) bool { return a == b }
component App() {
    <p>x</p>
}`, `app.den:2:44: invalid operation: a == b (struct containing func() cannot be compared)`},
		{"children in an attribute", `
component Card(children den.Children) {
    <div title={children}>x</div>
}
component App() {
    <Card>x</Card>
}`, `app.den:3:17: cannot render den.Children: only strings, numbers and bools can be shown in markup`},
		{"children from a signal", `
component Card(children den.Children) {
    s := signal(children)
    <div>{s.Get()}</div>
}
component App() {
    <Card>x</Card>
}`, `app.den:4:11: den.Children shown in markup cannot depend on a signal; choose between them with {if} instead`},
		{"children converted from another type", `
func f() den.Children { return den.Children(func() {}) }
component App() {
    <p>x</p>
}`, `app.den:2:45: cannot convert (func() literal) (value of type func()) to type den.Children`},
		{"children stored", `
component Card(children den.Children) {
    s := stored("k", []den.Children{children})
    <div>{len(s.Get())}</div>
}
component App() {
    <Card>x</Card>
}`, `app.den:3:10: cannot store []den.Children in localStorage`},
		{"den.Children in a program with its own den", `
func den() int { return 1 }
component Card(children den.Children) {
    <div>{children}</div>
}
component App() {
    <Card>x</Card>
}`, `app.den:3:25: den.Children is not available because this program declares its own den at app.den:2:6; rename that declaration`},
		{"using a component whose den.Children prop is hidden", `
func den() int { return 1 }
component Card(children den.Children) {
    <div>{children}</div>
}
component App() {
    <Card>x</Card>
}`, `app.den:7:5: <Card> cannot be used: its prop "children" is den.Children, which this program's own den hides`},
		{"den.Children in a program that imports a package as den", `
import den "strings"
component Card(children den.Children) {
    <div>{children}{den.ToUpper("a")}</div>
}
component App() {
    <Card>x</Card>
}`, `app.den:3:25: den.Children is not available because this program imports "strings" as den at app.den:2:8; rename that import`},
		{"alias of den.Children passed as an attribute", `
type Kids = den.Children
component Card(children Kids) {
    <div>{children}</div>
}
component Outer(children den.Children) {
    <Card children={children} />
}
component App() {
    <Outer>x</Outer>
}`, `app.den:7:11: children of <Card> go between <Card> and </Card>, not in an attribute`},
		{"new type based on den.Children", `
type Kids den.Children
component App() {
    <p>x</p>
}`, `app.den:2:6: type Kids is based on den.Children, which den does not support; declare an alias instead: type Kids = den.Children`},
		{"den.Children through a generic alias", `
type Id[T any] = den.Children
component Card(children Id[int]) {
    <div>{children}</div>
}
component App() {
    <Card>x</Card>
}`, `app.den:3:16: den cannot tell whether children is a den.Children prop; write its type as den.Children or as a plain alias of it (type X = den.Children)`},
		{"undefined type in a prop", `
component App() {
    <Card title="x" />
}
component Card(title strin) {
    <h2>{title}</h2>
}`, `app.den:5:22: undefined: strin`},
		{"route pattern that is not a constant", `
component App() {
    p := "/x"
    r := route[struct{}](p)
    <p>{r.Matches()}</p>
}`, `app.den:4:26: route pattern must be a constant string`},
		{"route pattern without a leading slash", `
component App() {
    r := route[struct{}]("about")
    <p>{r.Matches()}</p>
}`, `app.den:3:26: route pattern "about" must start with /`},
		{"route pattern in a function", `
func users() *Route[struct{}] { return route[struct{}]("users") }
component App() {
    <p>{users().Matches()}</p>
}`, `app.den:2:56: route pattern "users" must start with /`},
		{"route param with no name", `
component App() {
    r := route[struct{}]("/users/:")
    <p>{r.Matches()}</p>
}`, `app.den:3:26: route pattern "/users/:" has a param with no name`},
		{"route catch-all with no name", `
component App() {
    r := route[struct{}]("/files/*")
    <p>{r.Matches()}</p>
}`, `app.den:3:26: route pattern "/files/*" has a param with no name`},
		{"route param named twice", `
type P struct{ ID int }
component App() {
    r := route[P]("/a/:id/:ID")
    <p>{r.Matches()}</p>
}`, `app.den:4:19: route pattern "/a/:id/:ID" has param ID twice (param names ignore case)`},
		{"route catch-all before the end", `
type P struct{ Rest string }
component App() {
    r := route[P]("/files/*rest/x")
    <p>{r.Matches()}</p>
}`, `app.den:4:19: route pattern "/files/*rest/x": *rest must be the last segment`},
		{"route param that is not an identifier", `
type P struct{ ID int }
component App() {
    r := route[P]("/users/:user-id")
    <p>{r.Matches()}</p>
}`, `app.den:4:19: route pattern "/users/:user-id": param name "user-id" is not a Go identifier`},
		{"route param that is blank", `
type P struct{ ID int }
component App() {
    r := route[P]("/users/:_")
    <p>{r.Matches()}</p>
}`, `app.den:4:19: route pattern "/users/:_": a param cannot be named _`},
		{"route params that are not a struct", `
component App() {
    r := route[int]("/x")
    <p>{r.Matches()}</p>
}`, `app.den:3:16: route params must be a struct type, not int`},
		{"route param with no field", `
component App() {
    r := route[struct{}]("/users/:id")
    <p>{r.Matches()}</p>
}`, `app.den:3:26: route pattern "/users/:id": param id matches no field of struct{}`},
		{"route param matching two fields", `
type P struct {
    ID int
    Id int
}
component App() {
    r := route[P]("/users/:id")
    <p>{r.Matches()}</p>
}`, `app.den:7:19: route pattern "/users/:id": param id matches both ID and Id of P`},
		{"route field with no param", `
type P struct {
    ID   int
    Name string
}
component App() {
    r := route[P]("/users/:id")
    <p>{r.Matches()}</p>
}`, `app.den:7:16: field Name of P has no param in route pattern "/users/:id"`},
		{"route field that is not a string or int", `
type P struct{ Score float64 }
component App() {
    r := route[P]("/s/:score")
    <p>{r.Matches()}</p>
}`, `app.den:4:16: field Score of P is float64; route params can be strings or ints`},
		{"route field of a struct literal", `
component App() {
    r := route[struct{ On bool }]("/:on")
    <p>{r.Matches()}</p>
}`, `app.den:3:16: field On of struct{On bool} is bool; route params can be strings or ints`},
		{"route catch-all that is not a string", `
type P struct{ Rest int }
component App() {
    r := route[P]("/f/*rest")
    <p>{r.Matches()}</p>
}`, `app.den:4:16: field Rest of P is int, but the catch-all *rest needs a string`},
		{"route used as a value", `
component App() {
    f := route[struct{}]
    r := f("/x")
    <p>{r.Matches()}</p>
}`, `app.den:3:10: route can only be called, not used as a value`},
		{"route hidden by a component named Route", `
component Route(name string) {
    <p>{name}</p>
}
component App() {
    r := route[struct{}]("/")
    <div>{if r.Matches() { <Route name="x" /> }}</div>
}`, `app.den:6:10: route is not available because this program has a component named Route; rename it`},
		{"Route hidden by an import named route", `
import route "strconv"
func f(r *Route[struct{}]) string { return route.Itoa(1) }
component App() {
    <p>{f(nil)}</p>
}`, `app.den:3:11: Route is not available because this program imports "strconv" as route; rename it`},
		{"path used as a value", `
component App() {
    f := path
    <p>{f().Get()}</p>
}`, `app.den:3:10: path can only be called, not used as a value`},
		{"second style block", `
component App() {
    style { p { color: red; } }
    style { a { color: blue; } }
    <p>x</p>
}`, `app.den:4:5: component App has a second style block; put all of its CSS in one`},
		{"style block after the markup", `
component App() {
    <p>x</p>
    style { p { color: red; } }
}`, `app.den:4:5: the style block of App must come before its markup`},
		{"unterminated style block", `
component App() {
    style { p { color: red; }
    <p>x</p>
}`, `app.den:3:5: unterminated style block`},
		{"unterminated string in a style block", `
component App() {
    style { p::after { content: "x; } }
    <p>x</p>
}`, `app.den:3:33: unterminated string in style block`},
		{"unknown token", `
component App() {
    style { p { color: $ink; } }
    <p>x</p>
}`, `app.den:3:24: unknown token $ink (want a const Ink of type den.Color, den.Length, den.Duration, den.Font or den.Token)`},
		{"unknown token on a later line", `
component App() {
    style {
        p {
            margin: 0;
            color: $nope;
        }
    }
    <p>x</p>
}`, `app.den:6:20: unknown token $nope`},
		{"ambiguous token", `
const ink den.Color = "#000"
const Ink den.Color = "#111"
component App() {
    style { p { color: $ink; } }
    <p>x</p>
}`, `app.den:5:24: design token $ink is ambiguous: both ink and Ink are constants`},
		{"token that is not a token type", `
const Ink string = "#000"
component App() {
    style { p { color: $ink; } }
    <p>x</p>
}`, `app.den:4:24: $ink is string, not a design token type`},
		{"local const is not a token", `
component App() {
    const ink den.Color = "#000"
    style { p { color: $ink; } }
    <p>{string(ink)}</p>
}`, `app.den:4:24: unknown token $ink`},
		{"bad color token", `
const Ink den.Color = "#12"
component App() {
    <p>x</p>
}`, `app.den:2:7: design token Ink: "#12" is not a color`},
		{"bad length token", `
const Gap den.Length = "16"
component App() {
    <p>x</p>
}`, `app.den:2:7: design token Gap: "16" is not a length`},
		{"bad duration token", `
const Fast den.Duration = "fast"
component App() {
    <p>x</p>
}`, `app.den:2:7: design token Fast: "fast" is not a duration`},
		{"bad font token", `
const Body den.Font = "serif,,sans-serif"
component App() {
    <p>x</p>
}`, `app.den:2:7: design token Body: "serif,,sans-serif" is not a list of font families`},
		{"token that breaks out of its declaration", `
const Shadow den.Token = "0 0 1px red; color: blue"
component App() {
    <p>x</p>
}`, `app.den:2:7: design token Shadow: a token cannot contain ";"`},
		{"color token that closes the rule", `
const (
    Ink den.Color = "red}*"
)
component App() {
    <p>x</p>
}`, `app.den:3:5: design token Ink: unexpected }`},
		{"token with an open string", `
const Quote den.Token = "\"x"
component App() {
    <p>x</p>
}`, `app.den:2:7: design token Quote: unterminated string`},
		// A token's text goes wherever var() takes it, so it cannot hold a
		// URL at all: den checks URLs where a style block writes them.
		{"token holding image-set", `
const U den.Token = "image-set(\"vbscript:x\" 1x)"
component App() {
    style { p { background-image: $u; } }
    <p>x</p>
}`, `app.den:2:7: design token U: a token cannot contain image-set(); write the URL in the style block`},
		{"token holding -webkit-image-set", `
const U den.Token = "-webkit-image-set(\"blob:https://x/y\" 1x)"
component App() {
    style { p { background-image: $u; } }
    <p>x</p>
}`, `app.den:2:7: design token U: a token cannot contain -webkit-image-set()`},
		{"token holding IMAGE-SET in capitals", `
const T den.Token = "IMAGE-SET('ftp://x/y' 2x)"
component App() {
    style { p { list-style-image: $t; } }
    <p>x</p>
}`, `app.den:2:7: design token T: a token cannot contain image-set()`},
		{"token holding a data:text/html image-set, used in content", `
const T den.Token = "image-set(\"data:text/html,x\" 1x)"
component App() {
    style { p::after { content: $t; } }
    <p>x</p>
}`, `app.den:2:7: design token T: a token cannot contain image-set()`},
		{"token reaching image-set through a custom property", `
const T den.Token = "image-set(\"vbscript:x\" 1x)"
component App() {
    style {
        .a { --q: $t; }
        .b { background-image: var(--q); }
    }
    <p class="a"><b class="b">x</b></p>
}`, `app.den:2:7: design token T: a token cannot contain image-set()`},
		{"length token hiding image-set in calc", `
const L den.Length = "calc(1px + image-set(\"vbscript:x\" 1x))"
component App() {
    style { p { background: $l; } }
    <p>x</p>
}`, `app.den:2:7: design token L: a token cannot contain image-set()`},
		{"token holding a safe url", `
const Img den.Token = "URL(a.png)"
component App() {
    <p>x</p>
}`, `app.den:2:7: design token Img: a token cannot contain url(); write the URL in the style block`},
		{"token holding cross-fade", `
const Img den.Token = "cross-fade(red, blue)"
component App() {
    <p>x</p>
}`, `app.den:2:7: design token Img: a token cannot contain cross-fade()`},
		{"token calling a custom function", `
const Img den.Token = "--img(\"x\")"
component App() {
    <p>x</p>
}`, `app.den:2:7: design token Img: custom CSS functions are not supported in design tokens`},
		{"token using another token", `
const Gap den.Token = "$space-4"
component App() {
    <p>x</p>
}`, `app.den:2:7: design token Gap: a token cannot contain $: tokens cannot use other tokens`},
		{"color token where a length goes", `
const Ink den.Color = "#000"
component App() {
    style { p { padding: $ink; } }
    <p>x</p>
}`, `app.den:4:26: $ink is a den.Color, but padding takes a length`},
		{"length token where a color goes", `
const Gap den.Length = "4px"
component App() {
    style { p { border-top-color: $gap; } }
    <p>x</p>
}`, `app.den:4:35: $gap is a den.Length, but border-top-color takes a color`},
		{"duration token outside a transition", `
const Fast den.Duration = "1s"
component App() {
    style { p { width: $fast; } }
    <p>x</p>
}`, `app.den:4:24: $fast is a den.Duration, which only transition and animation properties take`},
		{"font token outside font", `
const Body den.Font = "serif"
component App() {
    style { p { color: $body; } }
    <p>x</p>
}`, `app.den:4:24: $body is a den.Font, which only font-family and font take`},
		{"token in a media condition", `
const Wide den.Length = "40rem"
component App() {
    style { @media (min-width: $wide) { p { color: red; } } }
    <p>x</p>
}`, `app.den:4:32: design tokens cannot be used in @media conditions: CSS custom properties do not work there`},
		{"import in a style block", `
component App() {
    style { @import "x.css"; }
    <p>x</p>
}`, `app.den:3:13: @import is not allowed in a style block; put it in style.css`},
		{"font-face in a style block", `
component App() {
    style { @font-face { font-family: x; } }
    <p>x</p>
}`, `app.den:3:13: @font-face is not allowed in a style block; put it in style.css`},
		{":root in a style block", `
component App() {
    style { :root { color: red; } }
    <p>x</p>
}`, `app.den:3:13: :root is outside every component, so a component's style can never match it; put page-wide styles in style.css`},
		{"body in a style block", `
component App() {
    style { body p { color: red; } }
    <p>x</p>
}`, `app.den:3:13: body is outside every component`},
		{"html in a selector list", `
component App() {
    style { p, HTML > a { color: red; } }
    <p>x</p>
}`, `app.den:3:16: html is outside every component`},
		{"unsafe url scheme", `
component App() {
    style { p { background: url("vbscript:x"); } }
    <p>x</p>
}`, `app.den:3:29: unsafe URL in url(): only http, https, mailto, tel, data:image/ and relative URLs are allowed`},
		{"unsafe unquoted url", `
component App() {
    style { p { background: url(data:text/html,xx); } }
    <p>x</p>
}`, `app.den:3:29: unsafe URL in url()`},
		{"javascript in a style block", `
component App() {
    style { p::after { content: JavaScript:x; } }
    <p>x</p>
}`, `app.den:3:33: "javascript:" is not allowed in a style block`},
		{"javascript in a url string", `
component App() {
    style { p { background: url("JavaScript:alert(1)"); } }
    <p>x</p>
}`, `app.den:3:29: url() cannot contain "expression(" or "javascript:"`},
		{"data URL that is not an image", `
component App() {
    style { p { background-image: image-set("data:text/html,x" 1x); } }
    <p>x</p>
}`, `app.den:3:45: unsafe URL in image-set()`},
		{"block in a custom property", `
component App() {
    style { p { --x: {}; } }
    <p>x</p>
}`, `app.den:3:22: {} blocks in custom property values are not supported`},
		// Case is ASCII-only in CSS. Unicode lowering once changed byte
		// offsets (İ is two bytes, its lower case one), so this panicked or
		// let expression( through.
		{"expression after a dotted capital I", `
component App() {
    style { p::after { content: "İ"/**/expression(1); } }
    <p>x</p>
}`, `app.den:3:41: "expression(" is not allowed in a style block`},
		{"data URL whose type is not ASCII image", `
component App() {
    style { p { background: url("DATA:İMAGE/png,x"); } }
    <p>x</p>
}`, `app.den:3:29: unsafe URL in url()`},
		{"at-rule name with a dotted capital I", `
component App() {
    style { @MEDİA screen { p { color: red; } } }
    <p>x</p>
}`, `app.den:3:13: @medİa is not allowed in a style block`},
		// var(), env() and attr() would hand a URL function a value den
		// never checked.
		{"var in image-set", `
component App() {
    style { .k { background-image: image-set(var(--nope, "javascript:alert(2)") 1x); } }
    <p>x</p>
}`, `app.den:3:46: var() cannot be used in image-set(): its URLs are checked when the app builds`},
		{"var in a prefixed image-set, any case", `
component App() {
    style { .k { --u: "javascript:alert(1)"; background-image: -WEBKIT-IMAGE-SET(VAR(--u) 1x); } }
    <p>x</p>
}`, `app.den:3:82: var() cannot be used in -webkit-image-set()`},
		{"env in image", `
component App() {
    style { p { background-image: image(ENV(safe-area)); } }
    <p>x</p>
}`, `app.den:3:41: env() cannot be used in image()`},
		{"attr in src", `
component App() {
    style { p { background-image: image-set(src(attr(data-x)) 1x); } }
    <p>x</p>
}`, `app.den:3:49: attr() cannot be used in image-set()`},
		{"string in parentheses in image-set", `
component App() {
    style { p { background-image: image-set(("javascript:x") 1x); } }
    <p>x</p>
}`, `app.den:3:46: image-set() cannot contain "expression(" or "javascript:"`},
		{"expression in a style block", `
component App() {
    style { p { width: expression(1); } }
    <p>x</p>
}`, `app.den:3:24: "expression(" is not allowed in a style block`},
		{"behavior in a style block", `
component App() {
    style { p { behavior: url(x.htc); } }
    <p>x</p>
}`, `app.den:3:17: behavior is not allowed in a style block: it can run code`},
		{"stray dollar", `
component App() {
    style { p { margin: $ 1px; } }
    <p>x</p>
}`, `app.den:3:25: stray $: a design token is written $name, like $space-4`},
		{"dollar in a selector", `
component App() {
    style { p$x { margin: 0; } }
    <p>x</p>
}`, `app.den:3:14: design tokens cannot be used in selectors`},
		{"nested rule", `
component App() {
    style { p { margin: 0; a { color: red; } } }
    <p>x</p>
}`, `app.den:3:30: nested rules are not supported in a style block; write each rule with its full selector`},
		{"nesting selector", `
component App() {
    style { &.on { margin: 0; } }
    <p>x</p>
}`, `app.den:3:13: & (CSS nesting) is not supported in a style block`},
		{"selector ending in a combinator", `
component App() {
    style { p > { margin: 0; } }
    <p>x</p>
}`, `app.den:3:13: a selector cannot end with a combinator`},
		{"declaration without a colon", `
component App() {
    style { p { margin; } }
    <p>x</p>
}`, `app.den:3:17: expected property: value`},
		{"unclosed parenthesis in a value", `
component App() {
    style { p { color: rgb(0 0 0; margin: 0; } }
    <p>x</p>
}`, `app.den:3:27: unclosed (`},
		{"extra parenthesis in a value", `
component App() {
    style { p { width: calc(1px)); } }
    <p>x</p>
}`, `app.den:3:33: unexpected )`},
		{"unclosed parenthesis in a selector", `
component App() {
    style { a:not(.b { margin: 0; } }
    <p>x</p>
}`, `app.den:3:18: unclosed (`},
		// A form feed ends a CSS string, so a token holding one in quotes
		// would turn the rest of style.css into part of the :root rule.
		{"form feed in a token", `
const Odd den.Token = "'a\fb'"
component App() {
    style { q { quotes: $odd; } }
    <q>x</q>
}`, `app.den:2:7: design token Odd: a token cannot contain a control character (U+000C)`},
		{"form feed in a token made by concatenation", `
const ff = "\f"
const Odd den.Token = "'a" + ff + "b'"
component App() {
    style { q { quotes: $odd; } }
    <q>x</q>
}`, `app.den:3:7: design token Odd: a token cannot contain a control character (U+000C)`},
		{"carriage return in a token", `
const Gap den.Length = "1px\r"
component App() {
    <p>x</p>
}`, `app.den:2:7: design token Gap: a token cannot contain a control character (U+000D)`},
		{"NUL in a token", `
const Ink den.Color = "red\x00"
component App() {
    <p>x</p>
}`, `app.den:2:7: design token Ink: a token cannot contain a control character (U+0000)`},
		{"DEL in a token", `
const Body den.Font = "serif\x7f"
component App() {
    <p>x</p>
}`, `app.den:2:7: design token Body: a token cannot contain a control character (U+007F)`},
		{"javascript in a token", `
const Odd den.Token = "JavaScript :alert(1)"
component App() {
    <p>x</p>
}`, `app.den:2:7: design token Odd: a token cannot contain "javascript:"`},
		{"form feed in a style block string", "\ncomponent App() {\n    style { p::after { content: \"a\fb\"; } }\n    <p>x</p>\n}",
			`app.den:3:33: unterminated string in style block`},
		{"carriage return in a style block string", "\ncomponent App() {\n    style { p::after { content: \"a\rb\"; } }\n    <p>x</p>\n}",
			`app.den:3:33: unterminated string in style block`},
		// Outside strings a backslash is an error: escapes there are where
		// den and browsers once disagreed about where a selector ends.
		{"escaped line end in a selector", "\ncomponent App() {\n    style { .a\\\fb { margin: 0; } }\n    <p>x</p>\n}",
			`app.den:3:15: backslash escapes are only allowed inside strings in style blocks`},
		{"escaped bracket in an attribute selector", `
component App() {
    style { .a[x\] { color: red; } .z { color: blue; } }
    <p>x</p>
}`, `app.den:3:17: backslash escapes are only allowed inside strings in style blocks`},
		{"escaped colon in a class", `
component App() {
    style { .a\:b { color: red; } }
    <p>x</p>
}`, `app.den:3:15: backslash escapes are only allowed inside strings in style blocks`},
		{"escaped brace after a pseudo-element", `
component App() {
    style { .a::before\{ color: red; } .z { color: blue; } }
    <p>x</p>
}`, `app.den:3:23: backslash escapes are only allowed inside strings in style blocks`},
		{"backslash in a media condition", `
component App() {
    style { @media (min-width: 1px\)) { p { color: red; } } }
    <p>x</p>
}`, `app.den:3:35: backslash escapes are only allowed inside strings in style blocks`},
		{"backslash in a property name", `
component App() {
    style { p { col\or: red; } }
    <p>x</p>
}`, `app.den:3:20: backslash escapes are only allowed inside strings in style blocks`},
		{"backslash in an unquoted url", `
component App() {
    style { p { background: url(a\)b.png); } }
    <p>x</p>
}`, `app.den:3:34: backslash escapes are only allowed inside strings in style blocks`},
		{"backslash in a value", `
component App() {
    style { p { margin: 1px\9; } }
    <p>x</p>
}`, `app.den:3:28: backslash escapes are only allowed inside strings in style blocks`},
		{"control character in a style block", "\ncomponent App() {\n    style { p { margin: 1px\x01; } }\n    <p>x</p>\n}",
			`app.den:3:28: a style block cannot contain the control character U+0001`},
		{"bracket closed by a parenthesis", `
component App() {
    style { a[x=y) { color: red; } }
    <p>x</p>
}`, `app.den:3:14: unclosed [`},
		{"unquoted url with a space", `
component App() {
    style { p { background: url(a b.png); } }
    <p>x</p>
}`, `app.den:3:34: unquoted url() cannot contain spaces, quotes or (; put the URL in quotes`},
		{"url in a selector", `
component App() {
    style { :is(url(x)) { color: red; } }
    <p>x</p>
}`, `app.den:3:17: url() cannot be used in a selector`},
		{"HTML comment opener in a style block", `
component App() {
    style { p { background: <!--url(x); } }
    <p>x</p>
}`, `app.den:3:29: <!-- is not allowed in a style block`},
		{"less-than in a selector", `
component App() {
    style { a < b { color: red; } }
    <p>x</p>
}`, `app.den:3:15: unexpected < in selector`},
		{"keyframes named auto", `
component App() {
    style { @keyframes auto { to { opacity: 1; } } }
    <p>x</p>
}`, `app.den:3:24: "auto" cannot name @keyframes: animation reads it as a keyword; choose another name`},
		{"token in image-set", `
const Img den.Token = "\"x.png\""
component App() {
    style { p { background-image: image-set($img 1x); } }
    <p>x</p>
}`, `app.den:4:45: design tokens cannot be used in image-set(): its URLs are checked when the app builds`},
		{"token in src", `
const Img den.Token = "\"x.png\""
component App() {
    style { p { background: src(type("image/png") $img); } }
    <p>x</p>
}`, `app.den:4:51: design tokens cannot be used in src()`},
		{"keyframes named like an animation keyword", `
component App() {
    style { @keyframes ease { to { opacity: 1; } } }
    <p>x</p>
}`, `app.den:3:24: "ease" cannot name @keyframes: animation reads it as a keyword; choose another name`},
		{"keyframes named like a CSS-wide keyword", `
component App() {
    style { @keyframes  NONE { to { opacity: 1; } } }
    <p>x</p>
}`, `app.den:3:25: "NONE" cannot name @keyframes`},
		{"unsafe URL in image-set", `
component App() {
    style { p { background-image: image-set("vbscript:x" 1x, "a.png" 2x); } }
    <p>x</p>
}`, `app.den:3:45: unsafe URL in image-set(): only http, https, mailto, tel, data:image/ and relative URLs are allowed`},
		{"unsafe URL in src", `
component App() {
    style { p { background: src("vbscript:x"); } }
    <p>x</p>
}`, `app.den:3:33: unsafe URL in src()`},
		// An @function in the app's own style.css could wrap a string into
		// image-set(), where den would never see it as a URL.
		{"custom function call in a style block", `
component App() {
    style { p { background-image: --img("vbscript:x"); } }
    <p>x</p>
}`, `app.den:3:35: custom CSS functions are not supported in style blocks`},
		{"custom function call with _ in its name", `
component App() {
    style { p { width: calc(--Gap_x(2) * 1px); } }
    <p>x</p>
}`, `app.den:3:29: custom CSS functions are not supported in style blocks`},
		{"composite literal of type style, indexed", `
type style []int
component App() {
    style{1, 2}[0] = 5
    <p>x</p>
}`, `app.den:4:5: style { opens a style block; write (style{...}) for a composite literal of type style`},
		{"composite literal of type style, called", `
type style struct{ F func() }
func f() {}
component App() {
    style{F: f}.F()
    <p>x</p>
}`, `app.den:5:5: style { opens a style block; write (style{...}) for a composite literal of type style`},
		{"composite literal of type style that is not CSS", `
type style []int
component App() {
    style{
        1, // it's
        2,
    }[0] = 5
    <p>x</p>
}`, `app.den:5:17: unterminated string in style block; style { opens a style block; write (style{...}) for a composite literal of type style`},
		{"bad keyframe selector", `
component App() {
    style { @keyframes spin { start { opacity: 0; } } }
    <p>x</p>
}`, `app.den:3:31: keyframe selectors are from, to or a percentage, not "start"`},
		{"unterminated comment in statements", `
component App() {
    x := 1 /* oops
    <p>{x}</p>
}`, `app.den:3:12: comment not terminated`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := compileOne(c.src)
			if err == nil {
				t.Fatalf("expected error containing %q, got none", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("expected error containing %q\ngot: %v", c.want, err)
			}
		})
	}
}

func TestConstantsFoldAndIntDivision(t *testing.T) {
	res, err := compileOne(`
component App() {
    n := signal(7)
    <p>{7 / 2} {n.Get() / 2}</p>
}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.JS, `a0.append("3 ", "")`) {
		t.Error("constant 7/2 should fold to 3")
	}
	if !strings.Contains(res.JS, `Math.trunc(n.get() / 2)`) {
		t.Error("int division should truncate")
	}
}

func TestGoDeclsAndData(t *testing.T) {
	got := runApp(t, `
type Point struct {
    X, Y int
}

type Box struct {
    Point
    Tags [2]string
}

const scale = 10

func moved(b Box, dx int) Box {
    b.X += dx * scale
    b.Tags[1] = "moved"
    return b
}

func sum(xs ...int) (total int) {
    for _, x := range xs {
        total += x
    }
    return
}

component App() {
    a := Box{Point: Point{1, 2}}
    b := moved(a, 1)
    c := a
    c.Y = 7
    words := []string{"x", "y", "z"}
    <div>
        <p>{a.X},{a.Y} {a.Tags[1]}|{b.X},{b.Y} {b.Tags[1]}|{c.Y}</p>
        <p>{a == c} {a == Box{Point: Point{1, 2}}} {b != a}</p>
        <p>{len(words[1:])} {words[len(words)-1]} {sum(1, 2, 3)} {sum(words2len(words)...)}</p>
        <p>{len("héllo")} {min(3, 1, 2)}</p>
    </div>
}

func words2len(ws []string) []int {
    out := make([]int, 0)
    for i := range ws {
        out = append(out, i+1)
    }
    return out
}`, `log(findAll("p").map((p) => p.textContent).join("\n"));`)
	expectLines(t, got,
		`1,2 |11,2 moved|7`,
		`false true true`,
		`2 z 6 6`,
		`6 1`)
}

func TestRemovedRowsStopUpdating(t *testing.T) {
	got := runApp(t, `
component App() {
    n := signal(0)
    ids := signal([]int{1, 2, 3})
    <div>
        <ul>
            {for _, id := range ids.Get() key id {
                <li><Row id={id} n={n} /></li>
            }}
        </ul>
        <button onclick={n.Set(n.Get() + 1)}>inc</button>
        <button onclick={ids.Set([]int{3, 1})}>drop</button>
    </div>
}

component Row(id int, n *Signal[int]) {
    label := computed(itoa(id) + ":" + itoa(n.Get()))
    <span>{label.Get()}</span>
}`, `
const before = findAll("span");
click(find("button", "drop")); await tick();
click(find("button", "inc")); await tick();
log(findAll("span").map((s) => s.textContent).join(" "));
log(before.map((s) => s.textContent).join(" "));`)
	expectLines(t, got,
		`3:1 1:1`,
		`1:1 2:0 3:1`)
}

// TestReconcileFuzz drives $for directly with random edits and checks the
// DOM matches the list, and that rows whose item survived kept their node.
func TestReconcileFuzz(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	dom, err := os.ReadFile("testdata/dom.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(dom) + runtimeFor("$signal $scope $for $h $frag") + `
let seed = 7;
const rand = (n) => { seed = (seed * 1103515245 + 12345) % 2147483648; return seed % n; };
const parent = document.createElement("ul");
const list = $signal([]);
const start = document.createComment(""), end = document.createComment("");
parent.append(start, end);
$scope(() => $for(start, end, () => list.get(), (x) => x, (x) => {
  const li = $h("li"); li.append(String(x));
  if (x % 3) return li; // rows are single elements or fragments
  const f = $frag(); f.append(li, document.createTextNode("")); return f;
}, false));
let next = 0, prevNodes = new Map();
(async () => {
  for (let round = 0; round < 300; round++) {
    let xs = [...list.get()];
    const ops = 1 + rand(4);
    for (let i = 0; i < ops; i++) {
      const r = rand(10);
      if (r < 4 || !xs.length) xs.splice(rand(xs.length + 1), 0, next++);
      else if (r < 7) xs.splice(rand(xs.length), 1);
      else { const a = rand(xs.length), b = rand(xs.length); [xs[a], xs[b]] = [xs[b], xs[a]]; }
    }
    if (rand(20) === 0) xs = [];
    if (rand(10) === 0) xs.reverse();
    list.set(xs);
    await tick();
    const lis = parent.childNodes.filter((n) => n.tagName === "li");
    const got = lis.map((n) => n.textContent).join(",");
    if (got !== xs.join(",")) throw new Error("round " + round + ": got " + got + " want " + xs.join(","));
    const nodes = new Map(lis.map((n, i) => [xs[i], n]));
    for (const [k, n] of nodes) if (prevNodes.has(k) && prevNodes.get(k) !== n) throw new Error("row " + k + " was rebuilt");
    prevNodes = nodes;
  }
  console.log("ok");
})().catch((e) => { console.error(e); process.exit(1); });
`
	file := filepath.Join(t.TempDir(), "fuzz.js")
	if err := os.WriteFile(file, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, file).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("%v\n%s", err, out)
	}
}

func gzipLen(s string) int {
	var buf bytes.Buffer
	w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	w.Write([]byte(s))
	w.Close()
	return buf.Len()
}

// TestRuntimeBudget holds the whole runtime, every feature included, to its
// size budget: under 3 KB gzipped. Apps ship only what they use.
func TestRuntimeBudget(t *testing.T) {
	all := runtimeFor(runtimeJS)
	t.Logf("full runtime: %d bytes, %d gzipped", len(all), gzipLen(all))
	if n := gzipLen(all); n >= 3*1024 {
		t.Errorf("runtime is %d bytes gzipped, over the 3 KB budget", n)
	}
}

// TestRuntimeNames checks that runtime.js and stdlib.js never declare the
// same name: tree shaking finds declarations by name, so one would quietly
// replace the other.
func TestRuntimeNames(t *testing.T) {
	seen := map[string]bool{}
	for _, line := range strings.Split(runtimeJS+"\n"+stdlibJS, "\n") {
		if m := runtimeDecl.FindStringSubmatch(line); m != nil {
			if seen[m[1]] {
				t.Errorf("%s is declared twice", m[1])
			}
			seen[m[1]] = true
		}
	}
}

// An app that uses neither route nor path ships none of the router, and
// one that uses only path ships no route matching.
func TestRouterTreeShaking(t *testing.T) {
	src, err := os.ReadFile("../examples/counter/app.den")
	if err != nil {
		t.Fatal(err)
	}
	router := []string{"pushState", "popstate", "$path", "$route", "$match", "$linkTo"}
	for _, c := range []struct {
		name, src string
		router    bool
		ships     []string
	}{
		{"counter", string(src), false, nil},
		{"path only", `
component App() {
    p := path()
    <p>{p.Get()}</p>
}`, true, router[:3]},
		{"routes", `
type P struct{ ID int }
component App() {
    user := route[P]("/users/:id")
    <a href={user.Link(P{ID: 1})}>{user.Params().ID}</a>
}`, true, append(router, "$atoi")},
	} {
		res, err := compileOne(c.src)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if res.Router != c.router {
			t.Errorf("%s: Router is %t", c.name, res.Router)
		}
		for _, w := range router {
			if got, want := strings.Contains(res.JS, w), slices.Contains(c.ships, w); got != want {
				t.Errorf("%s: ships %s: %t, want %t", c.name, w, got, want)
			}
		}
	}
}

// TestExampleBudgets compiles every example and holds it to the size budget:
// an app the size of TodoMVC ships under 10 KB gzipped.
func TestExampleBudgets(t *testing.T) {
	dirs, _ := filepath.Glob("../examples/*")
	if len(dirs) == 0 {
		t.Fatal("no examples found")
	}
	for _, dir := range dirs {
		files, _ := filepath.Glob(filepath.Join(dir, "*.den"))
		var srcs []Source
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			srcs = append(srcs, Source{Name: f, Code: string(b)})
		}
		res, err := Compile(srcs)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		n := gzipLen(res.JS)
		t.Logf("%s: %d bytes, %d gzipped", filepath.Base(dir), len(res.JS), n)
		if n > 10*1024 {
			t.Errorf("%s is %d bytes gzipped, over the 10 KB budget", dir, n)
		}
	}
}

func TestHeaders(t *testing.T) {
	got := runApp(t, `
type P struct{ Done bool }

func Set(n int) int { return n }

component App() {
    ps := []P{{true}, {false}}
    <div>
        <p>{for _, x := range []int{3, 4} key x {{x}}}</p>
        <p>{if ps[0].Done{yes}}</p>
        <p>{if ps[1:][0].Done { yes } else { no }}</p>
        <p>{Set(5)} <Map /></p>
    </div>
}

component Map() {
    s := []int{1}
    <i>{len(s)}</i>
}`, `log(html());`)
	expectLines(t, got, `<div><p>34</p><p>yes</p><p>no</p><p>5 <i>1</i></p></div>`)
}
