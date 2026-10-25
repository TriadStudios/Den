package compiler

import (
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
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
		{"import", `
import "strings"
component App() {
    <p>x</p>
}`, `app.den:2:1: imports are not supported yet`},
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
$scope(() => $for(parent, () => list.get(), (x) => x, (x) => { const f = $frag(), li = $h("li"); li.append(String(x)); f.append(li); return f; }, false));
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

// TestExampleBudgets compiles every example and holds it to the size budget
// in docs/PLAN.md: an app the size of TodoMVC ships under 10 KB gzipped.
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
		var buf bytes.Buffer
		w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		w.Write([]byte(res.JS))
		w.Close()
		t.Logf("%s: %d bytes, %d gzipped", filepath.Base(dir), len(res.JS), buf.Len())
		if buf.Len() > 10*1024 {
			t.Errorf("%s is %d bytes gzipped, over the 10 KB budget", dir, buf.Len())
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
