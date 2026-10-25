package compiler

import (
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
	for _, want := range []string{`$signal(0)`, `$text($e0, () => count.get())`, `count.set((count.get() + 1))`} {
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
		{"no App", `
component Home() {
    <p>x</p>
}`, `no App component`},
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
	if !strings.Contains(res.JS, `$e0.append("3")`) {
		t.Error("constant 7/2 should fold to 3")
	}
	if !strings.Contains(res.JS, `Math.trunc(n.get() / 2)`) {
		t.Error("int division should truncate")
	}
}
