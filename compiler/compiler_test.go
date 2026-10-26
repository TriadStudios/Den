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
		{"calling children", `
component Card(children den.Children) {
    <div>{children()}</div>
}
component App() {
    <Card>x</Card>
}`, `app.den:3:11: invalid operation: cannot call children`},
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
