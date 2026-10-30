package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runApp compiles src, runs it under node against testdata/dom.js, then runs
// script (async JS using the helpers in dom.js) and returns what it logged.
func runApp(t *testing.T, src, script string) string {
	t.Helper()
	return runAppWith(t, src, "", script)
}

// runAppWith is runApp with JS that runs before the app starts.
func runAppWith(t *testing.T, src, before, script string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	res, err := compileOne(src)
	if err != nil {
		t.Fatal(err)
	}
	dom, err := os.ReadFile("testdata/dom.js")
	if err != nil {
		t.Fatal(err)
	}
	prog := string(dom) + "\n" + before + "\n" + res.JS + "\n(async () => {\n" + script +
		"\n})().catch((e) => { console.error(e); process.exit(1); });\n"
	file := filepath.Join(t.TempDir(), "app.js")
	if err := os.WriteFile(file, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, file).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s\n--- app.js ---\n%s", err, out, res.JS)
	}
	return strings.TrimSpace(string(out))
}

func expectLines(t *testing.T, got string, want ...string) {
	t.Helper()
	g := strings.Split(got, "\n")
	if len(g) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(g), len(want), got)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Errorf("line %d:\n got: %s\nwant: %s", i+1, g[i], want[i])
		}
	}
}

// A {...} in markup is a comment only when nothing but comments is in it:
// {/* a */ x /* b */} shows x.
func TestRunCommentInterp(t *testing.T) {
	got := runApp(t, `
component App() {
	x := "hi"
	<p>{/* a */ x /* b */}|{/* only a comment */}|{
		// a line comment
	}|{ }|{/* a */ /* b */}</p>
}`, `log(find("p").textContent);`)
	expectLines(t, got, `hi||||`)
}

func TestRunCounter(t *testing.T) {
	got := runApp(t, `
component App() {
    count := signal(0)
    <button onclick={count.Set(count.Get() + 1)}>Clicked {count.Get()} times</button>
}`, `
log(html());
click(find("button"));
await tick();
log(html());`)
	expectLines(t, got,
		`<button>Clicked 0 times</button>`,
		`<button>Clicked 1 times</button>`)
}

func TestRunIf(t *testing.T) {
	got := runApp(t, `
component App() {
    n := signal(0)
    <div>
        <button onclick={n.Set(n.Get() + 1)}>next</button>
        {if n.Get() == 0 {
            <p>zero</p>
        } else if n.Get() == 1 {
            <p>one</p><Badge n={n.Get()} />
        } else {
            many: {n.Get()}
        }}
        <span>end</span>
    </div>
}

component Badge(n int) {
    doubled := computed(n * 2)
    <b>{doubled.Get()}</b>
}`, `
log(html());
for (let i = 0; i < 3; i++) { click(find("button")); await tick(); log(html()); }`)
	expectLines(t, got,
		`<div><button>next</button><p>zero</p><span>end</span></div>`,
		`<div><button>next</button><p>one</p><b>2</b><span>end</span></div>`,
		`<div><button>next</button>many: 2<span>end</span></div>`,
		`<div><button>next</button>many: 3<span>end</span></div>`)
}

func TestRunStaticIfAndFor(t *testing.T) {
	got := runApp(t, `
component App() {
    <List items={[]string{"a", "b", "c"}} loud={true} />
}

component List(items []string, loud bool) {
    <ul>
        {for i, s := range items key s {
            <li>{i}:{s}{if loud { ! }}</li>
        }}
        {for i := range 2 key i {<hr/>}}
    </ul>
}`, `log(html());`)
	expectLines(t, got, `<ul><li>0:a!</li><li>1:b!</li><li>2:c!</li><hr></hr><hr></hr></ul>`)
	res, err := compileOne(`
component App() {
    <p>{if true {x}}</p>
}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.JS, "$if") || strings.Contains(res.JS, "function $for") {
		t.Error("static control flow should not use the reactive runtime")
	}
}

const todoSrc = `
type Todo struct {
    ID   int
    Text string
    Done bool
}

func toggle(ts []Todo, id int) []Todo {
    out := []Todo{}
    for _, t := range ts {
        if t.ID == id {
            t.Done = !t.Done
        }
        out = append(out, t)
    }
    return out
}

func remaining(ts []Todo) int {
    n := 0
    for _, t := range ts {
        if !t.Done {
            n++
        }
    }
    return n
}

component App() {
    todos := signal([]Todo{{1, "a", false}, {2, "b", false}, {3, "c", false}, {4, "d", false}})
    <div>
        <ul>
            {for _, t := range todos.Get() key t.ID {
                <li class={t.Done} onclick={todos.Set(toggle(todos.Get(), t.ID))}>{t.Text}</li>
            }}
        </ul>
        <p>{remaining(todos.Get())} left</p>
        <button onclick={todos.Update(func(ts []Todo) []Todo {
            out := []Todo{}
            for i := len(ts) - 1; i >= 0; i-- {
                out = append(out, ts[i])
            }
            return out
        })}>reverse</button>
        <button onclick={todos.Set(todos.Get()[1:])}>drop first</button>
        <button onclick={todos.Set(append(todos.Get(), Todo{ID: 9, Text: "z"}))}>add</button>
        <button onclick={todos.Set(nil)}>clear</button>
    </div>
}`

func TestRunKeyedList(t *testing.T) {
	got := runApp(t, todoSrc, `
const lis = () => findAll("li");
const ids = () => lis().map((li) => li.textContent).join("");
lis().forEach((li, i) => { li.mark = i; });
log(ids(), find("p").textContent);

click(find("li", "b")); await tick();
log(html(find("ul")), find("p").textContent);
log("b rebuilt, others kept:", lis().map((li) => li.mark ?? "new").join(","));

ops();
click(find("button", "reverse")); await tick();
log(ids(), "ops", ops(), "kept", lis().map((li) => li.mark ?? "new").join(","));

click(find("button", "drop first")); await tick();
click(find("button", "add")); await tick();
log(ids(), find("p").textContent);

click(find("button", "clear")); await tick();
log(html(find("ul")), find("p").textContent);
click(find("button", "add")); await tick();
log(html(find("ul")));`)
	expectLines(t, got,
		`abcd 4 left`,
		`<ul><li den-click="0">a</li><li den-click="0" class="">b</li><li den-click="0">c</li><li den-click="0">d</li></ul> 3 left`,
		`b rebuilt, others kept: 0,new,2,3`,
		// Reversing 4 rows keeps the longest run in place and moves the other 3.
		`dcba ops 3 kept 3,2,new,0`,
		`cbaz 3 left`,
		`<ul></ul> 0 left`,
		`<ul><li den-click="0">z</li></ul>`)
}

func TestRunAttributes(t *testing.T) {
	got := runApp(t, `
component App() {
    on := signal(true)
    <button aria-pressed={on.Get()} disabled={!on.Get()} data-n={7} onclick={on.Set(!on.Get())}>x</button>
}`, `
log(html());
click(find("button")); await tick();
log(html());`)
	expectLines(t, got,
		`<button aria-pressed="true" data-n="7">x</button>`,
		`<button aria-pressed="false" data-n="7" disabled="">x</button>`)
}

// runRuntime runs script against the runtime declarations it names, with no
// compiled app, and returns what it logged.
func runRuntime(t *testing.T, names, script string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	dom, err := os.ReadFile("testdata/dom.js")
	if err != nil {
		t.Fatal(err)
	}
	prog := string(dom) + runtimeFor(names) + "\n(async () => {\n" + script +
		"\n})().catch((e) => { console.error(e); process.exit(1); });\n"
	file := filepath.Join(t.TempDir(), "rt.js")
	if err := os.WriteFile(file, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, file).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestSignalGraph(t *testing.T) {
	got := runRuntime(t, "$signal $computed $effect $scope $free", `
let runs = 0;
const a = $signal(1);
const lazy = $computed(() => { runs++; return a.get() * 10; });
log("not read yet:", runs);
log(lazy.get(), lazy.get(), "computed once:", runs);

// A diamond: d depends on a twice, through b and c. The effect must run once
// per change and never see b and c out of step.
const b = $computed(() => a.get() * 2);
const c = $computed(() => a.get() + 1);
const d = $computed(() => b.get() + "/" + c.get());
const seen = [];
const [, scope] = $scope(() => $effect(() => seen.push(d.get())));
a.set(2);
a.set(3); // two writes in one tick: one effect run
await tick();
log("diamond:", seen.join(" "));

// A computed whose value does not change stops the update there.
let parityRuns = 0;
const parity = $computed(() => a.get() % 2);
$scope(() => $effect(() => { parity.get(); parityRuns++; }));
a.set(5); await tick();
log("parity effect ran", parityRuns, "time(s); lazy ran", runs, "times");
a.set(6); await tick();
log("after parity change:", parityRuns);

$free(scope);
a.set(7); await tick();
log("disposed effect saw:", seen.join(" "));`)
	expectLines(t, got,
		`not read yet: 0`,
		`10 10 computed once: 1`,
		`diamond: 2/2 6/4`,
		`parity effect ran 1 time(s); lazy ran 1 times`,
		`after parity change: 2`,
		`disposed effect saw: 2/2 6/4 10/6 12/7`)
}

func TestMaps(t *testing.T) {
	got := runApp(t, `
type Score struct {
    Name  string
    Total int
}

func tally(words []string) map[string]int {
    counts := make(map[string]int)
    for _, w := range words {
        counts[w]++
    }
    return counts
}

func describe(m map[string]int, k string) string {
    if n, ok := m[k]; ok {
        return k + "=" + itoa(n)
    } else if k == "" {
        return "empty"
    }
    return k + " missing"
}

component App() {
    scores := signal(map[string]int{"ann": 1, "bob": 2})
    var none map[string]Score
    <div>
        <ul>
            {for name, n := range scores.Get() key name {
                <li>{name}:{n}</li>
            }}
        </ul>
        <p>{describe(tally([]string{"a", "b", "a"}), "a")} {describe(scores.Get(), "zed")} {len(none)} {none["x"].Total}</p>
        <button onclick={scores.Update(func(m map[string]int) map[string]int {
            m["bob"] += 10
            m["cat"] = 3
            delete(m, "ann")
            return m
        })}>edit</button>
    </div>
}`, `
const lis = findAll("li"); lis.forEach((li, i) => { li.mark = i; });
log(html(find("ul")), find("p").textContent);
click(find("button")); await tick();
log(html(find("ul")), "kept:", findAll("li").map((li) => li.mark ?? "new").join(","));`)
	expectLines(t, got,
		`<ul><li>ann:1</li><li>bob:2</li></ul> a=2 zed missing 0 0`,
		`<ul><li>bob:12</li><li>cat:3</li></ul> kept: new,new`)
}

func TestBoundsCheck(t *testing.T) {
	got := runApp(t, `
component App() {
    xs := []int{1}
    <button onclick={xs[0] = xs[5]}>oob</button>
}`, `try { click(find("button")); } catch (e) { log(e.message); }`)
	expectLines(t, got, `den: index out of range`)
}

// TestGoConformance runs each testdata/conform/*.den file (plain Go
// declarations) with Go and with den and requires identical output. Each
// file declares func run() string.
func TestGoConformance(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	files, _ := filepath.Glob("testdata/conform/*.den")
	if len(files) == 0 {
		t.Fatal("no conformance files")
	}
	progs := map[string]string{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		progs[filepath.Base(f)] = string(src)
	}
	for name, src := range conformInline {
		progs[name] = src
	}
	for name, src := range progs {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			main := "package main\n\nimport \"fmt\"\n\n" + src +
				"\n\nfunc itoa(n int) string { return fmt.Sprint(n) }\n\nfunc main() { fmt.Println(run()) }\n"
			os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644)
			os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module conform\n\ngo 1.24\n"), 0o644)
			cmd := exec.Command(goTool, "run", ".")
			cmd.Dir = dir
			want, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go run: %v\n%s", err, want)
			}
			got := runApp(t, src+"\ncomponent App() {\n    <p>{run()}</p>\n}\n", `log(find("p").textContent);`)
			if got != strings.TrimSpace(string(want)) {
				t.Errorf("den and Go differ\n  go: %s\n den: %s", strings.TrimSpace(string(want)), got)
			}
		})
	}
}

// conformInline holds conformance programs that cannot be files in
// testdata/conform, because den fmt would rewrite what they test and
// TestRepoFormatted keeps every .den file formatted.
var conformInline = map[string]string{
	// Parentheses gofmt drops: around if and for conditions and range
	// expressions, and nested ones. Den drops them too, so formatting
	// never changes the JS.
	"parens": `
type P struct{ N int }

func run() string {
	out := ""
	x, xs := 2, []int{3, 4}
	if (x > 1) {
		out += "a"
	}
	if y := x * 2; ((y > 3)) {
		out += "b"
	} else if (y < 0) {
		out += "c"
	}
	for i := 0; (i < 2); i++ {
		out += itoa(i)
	}
	for (x < 4) {
		x++
	}
	for _, v := range (xs) {
		out += itoa(((v + 1)) * 2)
	}
	for i := range ((3)) {
		out += itoa(i)
	}
	if (P{1}) == (P{x - 3}) {
		out += "p"
	}
	return out + itoa(x)
}`,
}

// TestSliceGrowth pins den's own capacity growth for append: at least
// double, as in GopherJS. Go leaves growth to the implementation, so this
// is not part of the conformance suite.
func TestSliceGrowth(t *testing.T) {
	got := runApp(t, `
func caps() string {
    grow := []int{}
    out := ""
    for i := range 5 {
        grow = append(grow, i)
        out += itoa(cap(grow)) + ","
    }
    return out
}

component App() {
    <p>{caps()}</p>
}`, `log(find("p").textContent);`)
	expectLines(t, got, `1,2,4,4,8,`)
}

func TestRunDynamicURLs(t *testing.T) {
	got := runApp(t, `
component App() {
    u := signal("https://go.dev")
    <a href={u.Get()} onclick={u.Set("java\tscript:alert(1)")}>x</a>
}`, `
log(html());
click(find("a")); await tick();
log(html());`)
	expectLines(t, got,
		`<a href="https://go.dev">x</a>`,
		`<a href="about:blank#blocked">x</a>`)
}

const storedSrc = `
type Item struct {
    Name string
    Tags []string
    Done bool
}

component App() {
    items := stored("items", []Item{{Name: "first"}})
    counts := stored("counts", map[string]int{})
    route := hash()
    <div>
        <p>{route.Get()}: {for _, it := range items.Get() key it.Name {{it.Name}{len(it.Tags)}{it.Done} }}</p>
        <p>{counts.Get()["x"]}</p>
        <button onclick={
            items.Set(append(items.Get(), Item{Name: "second", Tags: []string{"a", "b"}}))
            counts.Update(func(m map[string]int) map[string]int { m["x"]++; return m })
            route.Set("#/done")
        }>add</button>
    </div>
}`

func TestStoredAndHash(t *testing.T) {
	got := runApp(t, storedSrc, `
log(findAll("p").map((p) => p.textContent).join(" | "), localStorage.getItem("items"));
click(find("button")); await tick(); await tick();
log(findAll("p").map((p) => p.textContent).join(" | "), location.hash);
log(localStorage.getItem("items"), localStorage.getItem("counts"));
location.hash = "#/all"; await tick(); await tick();
log(find("p").textContent);`)
	expectLines(t, got,
		`: first0false | 0 [{"Name":"first","Tags":null,"Done":false}]`,
		`#/done: first0falsesecond2false | 1 #/done`,
		`[{"Name":"first","Tags":null,"Done":false},{"Name":"second","Tags":["a","b"],"Done":false}] {"x":1}`,
		`#/all: first0falsesecond2false`)

	// Stored JSON is decoded back into den values; a missing field gets its
	// zero value, and JSON that does not fit the type falls back to the
	// initial value.
	got = runAppWith(t, storedSrc, `localStorage.setItem("items", '[{"Name":"saved","Tags":["t"]}]'); localStorage.setItem("counts", '{"x":5}');`,
		`log(findAll("p").map((p) => p.textContent).join(" | "));`)
	expectLines(t, got, `: saved1false | 5`)
	got = runAppWith(t, storedSrc, `localStorage.setItem("items", '[{"Name":3}]'); localStorage.setItem("counts", '[1]');`,
		`log(findAll("p").map((p) => p.textContent).join(" | "));`)
	expectLines(t, got, `: first0false | 0`)
}

// TestTodoMVC runs examples/todomvc through the main flows of the TodoMVC
// spec on the test DOM. examples/todomvc/e2e.js covers the full spec in
// Chromium.
func TestTodoMVC(t *testing.T) {
	src, err := os.ReadFile("../examples/todomvc/app.den")
	if err != nil {
		t.Fatal(err)
	}
	got := runApp(t, string(src), `
await tick();
const titles = () => findAll("label").filter((l) => l.parentNode.getAttribute("class") === "view").map((l) => l.textContent).join(",");
const count = () => find("span").textContent;
const add = async (s) => { const i = findAll("input").find((e) => e.getAttribute("class") === "new-todo"); input(i, s); key(i, "Enter"); await tick(); };
log("empty:", findAll("section").length, findAll("footer").length, document.activeElement?.getAttribute("class"));
await add("  a  "); await add(" "); await add("b"); await add("c");
log(titles(), "|", count());
const toggles = () => findAll("input").filter((e) => e.getAttribute("class") === "toggle");
check(toggles()[0], true); await tick();
log(count(), findAll("li")[0].getAttribute("class"));
check(findAll("input").find((e) => e.id === "toggle-all" || e.getAttribute("id") === "toggle-all"), true); await tick();
log(count());
check(findAll("input").find((e) => e.getAttribute("id") === "toggle-all"), false); await tick();
log(count());
findAll("label")[2].dispatch("dblclick"); await tick(); await tick();
const ed = findAll("input").find((e) => e.getAttribute("class") === "edit");
log("editing:", findAll("li")[1].getAttribute("class"), ed.value, document.activeElement === ed);
input(ed, " bee "); key(ed, "Enter"); await tick();
log(titles());
check(toggles()[2], true); await tick();
location.hash = "#/active"; await tick(); await tick();
log("active:", titles());
location.hash = "#/completed"; await tick(); await tick();
log("completed:", titles());
location.hash = "#/"; await tick(); await tick();
click(find("button", "Clear completed")); await tick();
log(titles(), "|", count(), "|", localStorage.getItem("todos-den"));`)
	expectLines(t, got,
		`empty: 1 1 new-todo`,
		`a,b,c | 3 items left`,
		`2 items left completed`,
		`0 items left`,
		`3 items left`,
		`editing: editing b true`,
		`a,bee,c`,
		`active: a,bee`,
		`completed: c`,
		`a,bee | 2 items left | [{"ID":1,"Title":"a","Completed":false},{"ID":2,"Title":"bee","Completed":false}]`)
}

// Children are markup written by the caller, reading the caller's state,
// and rendered by the callee where it puts {children}.
func TestRunChildren(t *testing.T) {
	got := runApp(t, `
component Box(children den.Children) {
    <div class="box">{children}</div>
}

component App() {
    n := signal(0)
    <main>
        <Box>Count: {n.Get()} <i>static</i> <button onclick={n.Set(n.Get() + 1)}>inc</button></Box>
    </main>
}`, `
log(html());
click(find("button")); await tick();
log(html());`)
	expectLines(t, got,
		`<main><div class="box">Count: 0 <i>static</i> <button>inc</button></div></main>`,
		`<main><div class="box">Count: 1 <i>static</i> <button>inc</button></div></main>`)
}

func TestRunSlots(t *testing.T) {
	got := runApp(t, `
component Card(title string, header den.Children, footer den.Children, children den.Children) {
    <section>
        <header>{header}</header>
        <h2>{title}</h2>
        {children}
        <footer>{footer}</footer>
    </section>
}

component App() {
    on := signal(false)
    <div>
        <Card title="A">
            <:header><b onclick={on.Set(!on.Get())}>top {on.Get()}</b></:header>
            <p>body</p>
        </Card>
        <Card title="B" />
        <Card title="C"><:footer>end</:footer></Card>
    </div>
}`, `
log(html());
click(find("b")); await tick();
log(html(find("header")));`)
	expectLines(t, got,
		`<div>`+
			`<section><header><b>top false</b></header><h2>A</h2><p>body</p><footer></footer></section>`+
			`<section><header></header><h2>B</h2><footer></footer></section>`+
			`<section><header></header><h2>C</h2><footer>end</footer></section>`+
			`</div>`,
		`<header><b>top true</b></header>`)
}

// The nodes children make belong to the scope that renders them: when the
// callee's {if} removes them, their bindings stop.
func TestRunChildrenInIf(t *testing.T) {
	got := runApp(t, `
component Toggle(open *Signal[bool], children den.Children) {
    <div>{if open.Get() { {children} }}</div>
}

component App() {
    open := signal(true)
    n := signal(0)
    <main>
        <Toggle open={open}><span>{n.Get()}</span></Toggle>
        <button onclick={open.Set(!open.Get())}>toggle</button>
        <button onclick={n.Set(n.Get() + 1)}>inc</button>
    </main>
}`, `
const div = () => html(find("div"));
const first = find("span");
log(div());
click(find("button", "toggle")); await tick();
log(div());
click(find("button", "inc")); await tick();
log(div(), first.textContent);
click(find("button", "toggle")); await tick();
log(div(), findAll("span").length, find("span") !== first);
click(find("button", "inc")); await tick();
log(div(), "removed copy:", first.textContent);`)
	expectLines(t, got,
		`<div><span>0</span></div>`,
		`<div></div>`,
		`<div></div> 0`,
		`<div><span>1</span></div> 1 true`,
		`<div><span>2</span></div> removed copy: 0`)
}

// Children inside a caller's keyed list use the row's item. The callee
// renders them inside its own list rows, whose delegated handlers must not
// be confused with the caller's.
func TestRunChildrenInRows(t *testing.T) {
	got := runApp(t, `
type Item struct {
    ID   int
    Name string
}

component Pair(trail *Signal[string], children den.Children) {
    count := signal(2)
    <div>
        {for i := range count.Get() key i {
            <span onclick={trail.Set(trail.Get() + itoa(i))}>{children}</span>
        }}
    </div>
}

component App() {
    items := signal([]Item{{1, "a"}, {2, "b"}, {3, "c"}})
    picked := signal("")
    trail := signal("")
    <div>
        <ul>
            {for _, it := range items.Get() key it.ID {
                <li><Pair trail={trail}><button class={picked.Get() == it.Name} onclick={picked.Set(it.Name)}>{it.Name}</button></Pair></li>
            }}
        </ul>
        <p>{picked.Get()}|{trail.Get()}</p>
        <button onclick={items.Update(func(xs []Item) []Item { return []Item{xs[2], xs[0]} })}>reorder</button>
        <button onclick={items.Update(func(xs []Item) []Item {
            xs[0].Name = "C"
            return xs
        })}>rename</button>
    </div>
}`, `
const row = () => findAll("li").map((li) => findAll("button", li).map((b) => b.textContent + (b.getAttribute("class") === "" ? "*" : "")).join("")).join(",");
const p = () => find("p").textContent;
const lis = findAll("li");
log(row(), p());
click(findAll("button", lis[1])[0]); await tick();
log(row(), p());
click(findAll("button", lis[1])[1]); await tick();
log(row(), p());
click(find("button", "reorder")); await tick();
log(row(), p(), "kept:", findAll("li")[0] === lis[2], findAll("li")[1] === lis[0]);
click(findAll("button", findAll("li")[0])[0]); await tick();
log(row(), p());
click(find("button", "rename")); await tick();
log(row(), p());
click(findAll("button", findAll("li")[0])[1]); await tick();
log(row(), p());`)
	expectLines(t, got,
		`aa,bb,cc |`,
		`aa,b*b*,cc b|0`,
		`aa,b*b*,cc b|01`,
		`cc,aa b|01 kept: true true`,
		`c*c*,aa c|010`,
		`CC,aa c|010`,
		`C*C*,aa C|0101`)
}

// Outer passes its own children and a slot on to Inner.
func TestRunChildrenForwarding(t *testing.T) {
	got := runApp(t, `
component Inner(head den.Children, children den.Children) {
    <div><h1>{head}</h1>{children}</div>
}

component Outer(title den.Children, children den.Children) {
    <section><Inner><:head>{title}</:head>{children}</Inner></section>
}

component App() {
    n := signal(1)
    <Outer>
        <:title><i>{n.Get()}</i></:title>
        <b>body</b>
        <button onclick={n.Set(n.Get() + 1)}>inc</button>
    </Outer>
}`, `
log(html());
click(find("button")); await tick();
log(html());`)
	expectLines(t, got,
		`<section><div><h1><i>1</i></h1><b>body</b><button>inc</button></div></section>`,
		`<section><div><h1><i>2</i></h1><b>body</b><button>inc</button></div></section>`)
}

// Each {children} makes a fresh copy, with its own bindings.
func TestRunChildrenTwice(t *testing.T) {
	got := runApp(t, `
component Twice(children den.Children) {
    <div>{children}|{children}</div>
}

component App() {
    n := signal(0)
    <Twice><b onclick={n.Set(n.Get() + 1)}>{n.Get()}</b></Twice>
}`, `
log(html(), findAll("b")[0] !== findAll("b")[1]);
click(findAll("b")[1]); await tick();
log(html());`)
	expectLines(t, got,
		`<div><b>0</b>|<b>0</b></div> true`,
		`<div><b>1</b>|<b>1</b></div>`)
}

func TestRunChildrenControlFlow(t *testing.T) {
	got := runApp(t, `
component Badge(n int) {
    <em>{n}</em>
}

component Box(children den.Children) {
    <div>{children}</div>
}

component App() {
    xs := signal([]int{1, 2})
    show := signal(true)
    <main>
        <Box>
            {if show.Get() { <span>shown</span> } else { hidden }}
            {for _, x := range xs.Get() key x { <Badge n={x} /> }}
        </Box>
        <button onclick={show.Set(!show.Get())}>toggle</button>
        <button onclick={xs.Set(append(xs.Get(), 3))}>add</button>
    </main>
}`, `
log(html(find("div")));
click(find("button", "toggle")); await tick();
log(html(find("div")));
click(find("button", "add")); await tick();
log(html(find("div")));`)
	expectLines(t, got,
		`<div><span>shown</span><em>1</em><em>2</em></div>`,
		`<div>hidden<em>1</em><em>2</em></div>`,
		`<div>hidden<em>1</em><em>2</em><em>3</em></div>`)
}

// Each copy of passed markup is built in the namespace of the place that
// renders it: through a local variable, a function's result or another
// component, and as HTML and SVG at once. Inside it, <svg> and
// <foreignObject> switch namespace as usual, blocks follow the copy they
// are in, and markup passed on to a component follows that component.
func TestRunChildrenSVG(t *testing.T) {
	got := runApp(t, `
func pick(a den.Children, b den.Children, first bool) den.Children {
    if first {
        return a
    }
    return b
}

component Icon(children den.Children) {
    c := children
    <svg viewBox="0 0 10 10">{c}</svg>
}

component Both(children den.Children) {
    <div><p>{children}</p><svg>{children}</svg></div>
}

component Picked(a den.Children, b den.Children) {
    <svg>{pick(a, b, false)}</svg>
}

component Frame(children den.Children) {
    <div><Icon>{children}</Icon></div>
}

component Box(children den.Children) {
    <div>{children}</div>
}

component App() {
    on := signal(false)
    <main>
        <Icon><circle r="5" /></Icon>
        <Both>
            <g>{if on.Get() { <rect /> }}{for i := range 2 key i { <line /> }}</g>
            <foreignObject><em>html</em></foreignObject>
            <svg><path /></svg>
            <Box><ellipse /></Box>
        </Both>
        <Picked><:a><polygon /></:a><:b><polyline /></:b></Picked>
        <Frame><text>t</text></Frame>
        <button onclick={on.Set(!on.Get())}>on</button>
    </main>
}`, `
const ns = (t) => findAll(t).map((e) => (e.namespaceURI === "html" ? "h" : "s")).join("");
const all = () => ["circle", "g", "rect", "line", "foreignObject", "em", "path", "ellipse", "polygon", "polyline", "text"].map((t) => t + ":" + ns(t)).join(" ");
log(all());
click(find("button")); await tick();
log(all());`)
	expectLines(t, got,
		`circle:s g:hs rect: line:hhss foreignObject:hs em:hh path:ss ellipse:hh polygon: polyline:s text:s`,
		`circle:s g:hs rect:hs line:hhss foreignObject:hs em:hh path:ss ellipse:hh polygon: polyline:s text:s`)
}

// A prop whose type is an alias of den.Children is one.
func TestRunChildrenAliases(t *testing.T) {
	got := runApp(t, `
type Kids = den.Children
type More = Kids

component Card(head Kids, children More) {
    <section><h1>{head}</h1>{children}</section>
}

component App() {
    <Card><:head>top</:head><p>body</p></Card>
}`, `log(html());`)
	expectLines(t, got, `<section><h1>top</h1><p>body</p></section>`)
}

// A program may still declare or import its own den, as on main; it then
// keeps meaning what it means in Go.
func TestRunOwnDen(t *testing.T) {
	for _, c := range []struct{ src, want string }{{`
func den() int { return 1 }

component App() {
    <p>{den()}</p>
}`, `<p>1</p>`}, {`
type den struct{ X int }

component App() {
    d := den{X: 2}
    <p>{d.X}</p>
}`, `<p>2</p>`}, {`
import den "strconv"

component App() {
    <p>{den.Itoa(3)}</p>
}`, `<p>3</p>`}, {`
type P struct{ Children int }

component Card(children den.Children) {
    den := P{4}
    <p>{den.Children}{children}</p>
}

component App() {
    <Card>!</Card>
}`, `<p>4!</p>`}} {
		expectLines(t, runApp(t, c.src, `log(html());`), c.want)
	}
}

const routeTypes = `
type UserParams struct {
    ID int
}

type FileParams struct {
    Dir  string
    Rest string
}

type TagParams struct {
    Name string
}

func yes(b bool) string {
    if b {
        return "Y"
    }
    return "."
}
`

// A route matches a path segment by segment: one trailing slash is ignored,
// params are decoded, a :x param needs a non-empty segment, ints parse as
// strconv.Atoi parses, and a catch-all takes the rest of the path, which
// may be empty.
func TestRouteMatching(t *testing.T) {
	got := runApp(t, routeTypes+`
component App() {
    home := route[struct{}]("/")
    about := route[struct{}]("/about/")
    user := route[UserParams]("/users/:id")
    files := route[FileParams]("/files/:dir/*rest")
    tag := route[TagParams]("/tags/:name")
    <p>{yes(home.Matches())} {yes(about.Matches())} {yes(user.Matches())}:{user.Params().ID} {yes(files.Matches())}:{files.Params().Dir}:{files.Params().Rest} {yes(tag.Matches())}:{tag.Params().Name}</p>
}`, `
log("start", find("p").textContent);
for (const u of ["/about", "/about/", "/about//", "/users/7", "/users/+8", "/users/x", "/users/99999999999999999999",
  "/users", "/users/7/x", "/files/docs", "/files/docs/", "/files/docs//", "/files//x", "/files/docs/a/b%20c/", "/files/%ZZ/a", "/files/a/%ZZ",
  "/tags/caf%C3%A9", "/tags/a%2Fb", "/tags/%E0%A4%A", "/nowhere", "/"]) {
  visit(u); await tick();
  log(u, find("p").textContent);
}`)
	expectLines(t, got,
		`start Y . .:0 .:: .:`,
		`/about . Y .:0 .:: .:`,
		`/about/ . Y .:0 .:: .:`,
		`/about// . . .:0 .:: .:`,
		`/users/7 . . Y:7 .:: .:`,
		`/users/+8 . . Y:8 .:: .:`,
		`/users/x . . .:0 .:: .:`,
		`/users/99999999999999999999 . . .:0 .:: .:`,
		`/users . . .:0 .:: .:`,
		`/users/7/x . . .:0 .:: .:`,
		`/files/docs . . .:0 Y:docs: .:`,
		`/files/docs/ . . .:0 Y:docs: .:`,
		`/files/docs// . . .:0 Y:docs: .:`,
		`/files//x . . .:0 .:: .:`,
		`/files/docs/a/b%20c/ . . .:0 Y:docs:a/b c .:`,
		`/files/%ZZ/a . . .:0 .:: .:`,
		`/files/a/%ZZ . . .:0 .:: .:`,
		`/tags/caf%C3%A9 . . .:0 .:: Y:café`,
		`/tags/a%2Fb . . .:0 .:: Y:a/b`,
		`/tags/%E0%A4%A . . .:0 .:: .:`,
		`/nowhere . . .:0 .:: .:`,
		`/ Y . .:0 .:: .:`)
}

// Link encodes each param, and its result passes den's URL check and leads
// back through the route to the same params, except for a :x param that is
// "", "." or "..": an empty param never matches, and browsers resolve dot
// segments (encoded or not) to another path.
func TestRouteLink(t *testing.T) {
	got := runApp(t, routeTypes+`
type PairParams struct {
    A string
    B string
}

const tagPattern = "/tags/:name"

component App() {
    home := route[struct{}]("/")
    user := route[UserParams]("/users/:id")
    files := route[FileParams]("/files/:dir/*rest")
    tag := route[TagParams](tagPattern)
    pair := route[PairParams]("/:a/:b")
    <div>
        <a href={home.Link(struct{}{})}>home</a>
        <a href={user.Link(UserParams{ID: -7})}>user</a>
        <a href={tag.Link(TagParams{Name: "a b/c?#%é"})}>tag</a>
        <a href={tag.Link(TagParams{Name: "javascript:alert(1)"})}>js</a>
        <a href={files.Link(FileParams{Dir: "d", Rest: "x y/z"})}>files</a>
        <a href={files.Link(FileParams{Dir: "d"})}>files root</a>
        <a href={tag.Link(TagParams{Name: ""})}>empty</a>
        <a href={tag.Link(TagParams{Name: ".."})}>dots</a>
        <a href={pair.Link(PairParams{B: "evil.com"})}>pair</a>
        <p>{yes(home.Matches())} {user.Params().ID} {tag.Params().Name} {files.Params().Dir}:{files.Params().Rest} {yes(pair.Matches())}:{pair.Params().A}:{pair.Params().B}</p>
    </div>
}`, `
for (const a of findAll("a")) {
  log(a.textContent, a.getAttribute("href"), a.dispatch("click").defaultPrevented, location.pathname);
  await tick();
  log("  " + find("p").textContent);
}`)
	expectLines(t, got,
		`home / true /`,
		`  Y 0  : .::`,
		`user /users/-7 true /users/-7`,
		`  . -7  : Y:users:-7`,
		`tag /tags/a%20b%2Fc%3F%23%25%C3%A9 true /tags/a%20b%2Fc%3F%23%25%C3%A9`,
		`  . 0 a b/c?#%é : Y:tags:a b/c?#%é`,
		`js /tags/javascript%3Aalert(1) true /tags/javascript%3Aalert(1)`,
		`  . 0 javascript:alert(1) : Y:tags:javascript:alert(1)`,
		`files /files/d/x%20y/z true /files/d/x%20y/z`,
		`  . 0  d:x y/z .::`,
		`files root /files/d/ true /files/d/`,
		`  . 0  d: Y:files:d`,
		`empty /tags/ true /tags/`,
		`  . 0  : .::`,
		`dots /tags/.. true /`,
		`  Y 0  : .::`,
		// An empty first param would make //evil.com, a link to that host.
		`pair /.//evil.com true //evil.com`,
		`  . 0  : .::`)
}

// Browsers resolve dot segments away before den sees a path. Should one
// get through, no param takes it. A catch-all value is "/"-separated parts
// (Link writes each part as a segment), so no part of the decoded value is
// "." or "..", even when the raw segment hid the "/" as %2F. A :x param is
// one segment whatever it holds, so only the whole value is checked.
func TestRouteMatchDots(t *testing.T) {
	got := runRuntime(t, "$match $linkTo", `
const tag = ["tags", ":Name"], rest = ["f", "*Rest"], all = ["*Rest"];
for (const p of ["/tags/x", "/tags/.", "/tags/..", "/tags/%2e", "/tags/%2E%2e", "/tags/...", "/tags/.x", "/tags/..%2F"]) log(p, JSON.stringify($match(tag, { Name: "" }, p)));
for (const p of ["/f/a/b", "/f/a/../b", "/f/./b", "/f/", "/f/a%2F..%2Fb", "/f/a%2F...", "/f/.%2Fb"]) log(p, JSON.stringify($match(rest, { Rest: "" }, p)));
for (const p of ["/docs/a%2F..%2F..%2Fadmin", "/docs/a%2F.", "/docs/%2E%2E%2Fx", "/docs/a%2F%2E", "/docs/a%2Fb"]) log(p, JSON.stringify($match(all, { Rest: "" }, p)));
for (const [s, v, z] of [[tag, { Name: "../" }, { Name: "" }], [all, { Rest: "docs/a/b" }, { Rest: "" }]]) {
  const u = $linkTo({ s }, v);
  log("link", u, JSON.stringify($match(s, z, u)));
}`)
	expectLines(t, got,
		`/tags/x {"Name":"x"}`,
		`/tags/. null`,
		`/tags/.. null`,
		`/tags/%2e null`,
		`/tags/%2E%2e null`,
		`/tags/... {"Name":"..."}`,
		`/tags/.x {"Name":".x"}`,
		`/tags/..%2F {"Name":"../"}`,
		`/f/a/b {"Rest":"a/b"}`,
		`/f/a/../b null`,
		`/f/./b null`,
		`/f/ {"Rest":""}`,
		`/f/a%2F..%2Fb null`,
		`/f/a%2F... {"Rest":"a/..."}`,
		`/f/.%2Fb null`,
		`/docs/a%2F..%2F..%2Fadmin null`,
		`/docs/a%2F. null`,
		`/docs/%2E%2E%2Fx null`,
		`/docs/a%2F%2E null`,
		`/docs/a%2Fb {"Rest":"docs/a/b"}`,
		`link /tags/..%2F {"Name":"../"}`,
		`link /docs/a/b {"Rest":"docs/a/b"}`)
}

// path() is the URL path: Set navigates, back and forward follow history,
// and clicks on links to this origin are followed without loading a page,
// unless the browser should handle them.
func TestRouteNavigation(t *testing.T) {
	got := runApp(t, routeTypes+`
component App() {
    p := path()
    user := route[UserParams]("/users/:id")
    <div>
        <nav>
            <a href="/users/1">one</a>
            <a href="../users/2">relative</a>
            <a href="/users/3" target="_blank">blank</a>
            <a href="/users/4" target="_self"><b>self</b></a>
            <a href="/users/5" download>download</a>
            <a href="https://example.com/users/6">external</a>
            <a href="/users/7" onclick={e.PreventDefault()}>prevented</a>
            <a href="#top">anchor</a>
            <a href="mailto:a@example.com">mail</a>
            <a>no href</a>
        </nav>
        <button onclick={p.Set("/users/9?tab=a#x")}>go</button>
        <p>{p.Get()} {yes(user.Matches())} {user.Params().ID}</p>
    </div>
}`, `
const p = () => find("p").textContent;
const follow = (text, extra) => find("a", text).dispatch("click", extra).defaultPrevented;
log(p(), entries.length);
log("one", follow("one"), location.pathname, scrolls); await tick(); log(p());
log("one again", follow("one"), entries.length, scrolls);
log("relative", follow("relative"), location.pathname); await tick(); log(p());
for (const [name, extra] of [["ctrl", { ctrlKey: true }], ["meta", { metaKey: true }], ["shift", { shiftKey: true }], ["alt", { altKey: true }], ["middle", { button: 1 }]]) {
  log(name, follow("one", extra), location.pathname);
}
for (const name of ["blank", "download", "external", "prevented", "anchor", "mail", "no href"]) log(name, follow(name), location.pathname);
log("self", find("b").dispatch("click").defaultPrevented, location.pathname); await tick(); log(p(), scrolls);
click(find("button")); await tick(); log(p(), location.href, scrolls, entries.length);
back(); await tick(); log(p(), location.href);
back(); await tick(); log(p());
forward(); forward(); await tick(); log(p(), location.href);`)
	expectLines(t, got,
		`/ . 0 1`,
		`one true /users/1 1`,
		`/users/1 Y 1`,
		`one again true 2 2`,
		`relative true /users/2`,
		`/users/2 Y 2`,
		`ctrl false /users/2`,
		`meta false /users/2`,
		`shift false /users/2`,
		`alt false /users/2`,
		`middle false /users/2`,
		`blank false /users/2`,
		`download false /users/2`,
		`external false /users/2`,
		`prevented true /users/2`,
		`anchor false /users/2`,
		`mail false /users/2`,
		`no href false /users/2`,
		`self true /users/4`,
		`/users/4 Y 4 4`,
		`/users/9 Y 9 http://localhost/users/9?tab=a#x 4 5`,
		`/users/4 Y 4 http://localhost/users/4`,
		`/users/2 Y 2`,
		`/users/9 Y 9 http://localhost/users/9?tab=a#x`)
}

// Routes choose branches of an {if}: a branch stays while its route still
// matches, and the branch it leaves is freed, with any route made in it.
func TestRouteBranches(t *testing.T) {
	got := runApp(t, routeTypes+`
component User(user *Route[UserParams]) {
    <p>user {user.Params().ID}</p>
}

component Other() {
    here := route[struct{}]("/here")
    p := path()
    <p>other {yes(here.Matches())} {p.Get()}</p>
}

component App() {
    home := route[struct{}]("/")
    user := route[UserParams]("/users/:id")
    <main>
        {if home.Matches() {
            <p>home</p>
        } else if user.Matches() {
            <User user={user} />
        } else {
            <Other />
        }}
    </main>
}`, `
const main = () => html(find("main"));
log(main());
visit("/users/7"); await tick(); const first = find("p"); log(main());
visit("/users/8"); await tick(); log(main(), find("p") === first);
visit("/here"); await tick(); const other = find("p"); log(main(), first.textContent);
visit("/there"); await tick(); log(main(), find("p") === other);
visit("/users/9"); await tick(); log(main(), "|", first.textContent, "|", other.textContent);`)
	expectLines(t, got,
		`<main><p>home</p></main>`,
		`<main><p>user 7</p></main>`,
		`<main><p>user 8</p></main> true`,
		`<main><p>other Y /here</p></main> user 8`,
		`<main><p>other . /there</p></main> true`,
		`<main><p>user 9</p></main> | user 8 | other . /there`)
}

// TestRouterExample walks through examples/router on the test DOM.
func TestRouterExample(t *testing.T) {
	// The example's files (app.den and tokens.den) as one source.
	files, _ := filepath.Glob("../examples/router/*.den")
	var src []byte
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src = append(append(src, b...), '\n')
	}
	got := runApp(t, string(src), `
const page = async () => { await tick(); return find("nav").childNodes.filter((a) => a.getAttribute?.("class") === "active").map((a) => a.textContent).join("") + " | " + find("main").textContent + " | " + location.pathname; };
const go = async (text) => { find("a", text).dispatch("click"); return page(); };
log(await page());
log(await go("People"));
log(await go("Ken Thompson"));
log(await go("Next id"));
click(find("button", "All people")); log(await page());
log(await go("A broken link"));
click(find("button", "Go home")); log(await page());
back(); log(await page());
back(); back(); log(await page(), findAll("a").find((a) => a.textContent === "Next id").getAttribute("href"));`)
	expectLines(t, got,
		`Home | Routing in denThe URL path picks the page. Links within the site change it without reloading, and back and forward work as usual.Every page has its own address, like this profile. | /`,
		`People | PeopleAda Lovelace Wrote the first programGrace Hopper Built the first compilerKen Thompson Co-created Unix and Go | /people`,
		`People | Ken ThompsonCo-created Unix and GoNext idAll people | /people/7`,
		`People | No person 8Next idAll people | /people/8`,
		`People | PeopleAda Lovelace Wrote the first programGrace Hopper Built the first compilerKen Thompson Co-created Unix and Go | /people`,
		` | Not foundNothing lives at /nowhere.Go home | /nowhere`,
		`Home | Routing in denThe URL path picks the page. Links within the site change it without reloading, and back and forward work as usual.Every page has its own address, like this profile. | /`,
		` | Not foundNothing lives at /nowhere.Go home | /nowhere`,
		`People | No person 8Next idAll people | /people/8 /people/9`)
}

// A program that has its own path, route or Route, as a declaration, a
// component or an import, keeps it, as on main; route and Route give way
// together, path on its own. Explicitly instantiated generics, den's and
// the program's, are called like any function.
func TestRunOwnRouterNames(t *testing.T) {
	for _, c := range []struct{ src, want string }{{`
func path() string { return "mine" }

type Route struct{ N int }

func first[T any](xs []T) T { return xs[0] }

component App() {
    r := Route{N: 2}
    n := signal[int](3)
    <p>{path()} {r.N} {n.Get()} {first[string]([]string{"a"})}</p>
}`, `<p>mine 2 3 a</p>`}, {`
component Route(name string) {
    <p>{name}</p>
}

component App() {
    <Route name="x" />
}`, `<p>x</p>`}, {`
import path "strings"

component App() {
    <p>{path.ToUpper("x")}</p>
}`, `<p>X</p>`}, {`
import route "strconv"

component App() {
    p := path()
    <p>{route.Itoa(4)}{p.Get()}</p>
}`, `<p>4/</p>`}} {
		expectLines(t, runApp(t, c.src, `log(html());`), c.want)
	}
}

// logAnims logs the animations started since it last ran (anims, from
// dom.js) as tag:text keyframes options.
const logAnims = `
function anim(a) { return a.el.tagName + ":" + a.el.textContent + " " + JSON.stringify(a.k) + " " + JSON.stringify(a.o); }
let seen = 0;
function logNew() { anims.slice(seen).forEach((a) => log(anim(a))); seen = anims.length; }
`

// An {if} whose branches have transitions: nothing animates on the first
// render; then the new branch enters while the old one leaves, frozen and
// inert, and its nodes go only when its exit transition ends. Toggling
// faster than that leaves several branches on their way out at once.
func TestMotionIf(t *testing.T) {
	got := runApp(t, `
component App() {
    open := signal(false)
    n := signal(0)
    <div>
        <button onclick={open.Set(!open.Get())}>t</button>
        <button onclick={n.Set(n.Get() + 1)}>n</button>
        {if open.Get() {
            <p enter={fade()} exit={scale().For("50ms")}>{n.Get()}</p>
            text
        } else {
            <i exit={fade()}>shut</i>
        }}
    </div>
}`, logAnims+`
const [t, n] = findAll("button");
log(html()); logNew();
click(t); await tick();
log(html(), find("i").inert, find("p").inert); logNew();
click(n); await tick();
log(html());
click(t); await tick();
click(n); await tick();
log(html(), running().join(" "));
click(t); await tick();
log(html(), running().join(" "));
finishAll();
log(html(), running().length);
logNew();
click(t); await tick();
log(html(), running().join(" "));
finishAll();
log(html());`)
	expectLines(t, got,
		`<div><button>t</button><button>n</button><i>shut</i></div>`,
		`<div><button>t</button><button>n</button><i>shut</i><p>0</p>text</div> true false`,
		`i:shut {"opacity":0} {"duration":200,"easing":"ease-in","fill":"forwards"}`,
		`p:0 {"opacity":0,"offset":0} {"duration":200,"easing":"ease-out","fill":"none"}`,
		`<div><button>t</button><button>n</button><i>shut</i><p>1</p>text</div>`,
		// The first <i> and <p> are still leaving (<p>'s enter, too).
		`<div><button>t</button><button>n</button><i>shut</i><p>1</p><i>shut</i></div> i:shut p:1 p:1`,
		`<div><button>t</button><button>n</button><i>shut</i><p>1</p><i>shut</i><p>2</p>text</div> i:shut p:1 p:1 i:shut p:2`,
		`<div><button>t</button><button>n</button><p>2</p>text</div> 0`,
		`p:1 {"opacity":0,"transform":"scale(.95)"} {"duration":50,"easing":"ease-in","fill":"forwards"}`,
		`i:shut {"opacity":0} {"duration":200,"easing":"ease-in","fill":"forwards"}`,
		`p:2 {"opacity":0,"offset":0} {"duration":200,"easing":"ease-out","fill":"none"}`,
		`<div><button>t</button><button>n</button><p>2</p><i>shut</i></div> p:2`,
		`<div><button>t</button><button>n</button><i>shut</i></div>`)
}

// An else if chain is one block to the eye: each of its branches enters
// and leaves, though the inner {if} is built anew when the outer one
// switches to it. A nested block disposed while its elements leave
// cancels their transitions and removes them.
func TestMotionNested(t *testing.T) {
	got := runApp(t, `
component App() {
    n := signal(0)
    outer := signal(true)
    inner := signal(true)
    <div>
        <button onclick={n.Set((n.Get() + 1) % 3)}>n</button>
        <button onclick={outer.Set(!outer.Get())}>outer</button>
        <button onclick={inner.Set(!inner.Get())}>inner</button>
        {if n.Get() == 0 {
            <b enter={fade()} exit={fade()}>zero</b>
        } else if n.Get() == 1 {
            <i enter={fade()} exit={fade()}>one</i>
        }}
        {if outer.Get() {
            <section>{if inner.Get() { <p exit={fade()}>p</p> }}</section>
        }}
    </div>
}`, `
const [n, outer, inner] = findAll("button");
click(n); await tick();
log(html(), running().join(" "));
finishAll();
click(n); await tick();
log(html(), running().join(" "));
finishAll();
click(n); await tick();
log(html(), running().join(" "));
finishAll();
click(inner); await tick();
const p = find("p"), a = anims.at(-1);
log(running().join(" "), p.inert);
click(outer); await tick();
log(a.playState, p.parentNode === null, findAll("section").length, running().length);
click(outer); await tick();
log(html(find("section")), running().length);`)
	expectLines(t, got,
		`<div><button>n</button><button>outer</button><button>inner</button><b>zero</b><i>one</i><section><p>p</p></section></div> b:zero i:one`,
		`<div><button>n</button><button>outer</button><button>inner</button><i>one</i><section><p>p</p></section></div> i:one`,
		`<div><button>n</button><button>outer</button><button>inner</button><b>zero</b><section><p>p</p></section></div> b:zero`,
		`p:p true`,
		`idle true 0 0`,
		`<section></section> 0`)
}

// testList is a list whose items come from location.hash, "#a,b,c". With
// struct items ("#1=a,2=b"), every row is rebuilt on each change.
const testList = `
import "strings"

type It struct {
    K string
    V string
}

func items(h string) []string {
    var out []string
    for _, s := range strings.Split(strings.TrimPrefix(h, "#"), ",") {
        if s != "" {
            out = append(out, s)
        }
    }
    return out
}

func structs(h string) []It {
    var out []It
    for _, s := range items(h) {
        if kv := strings.Split(s, "="); len(kv) == 2 {
            out = append(out, It{kv[0], kv[1]})
        }
    }
    return out
}

func pick(s string) Transition {
    if s == "x" {
        return Transition{}
    }
    return fade().For("1s")
}
`

// A {for} whose rows have transitions: removed rows leave in place, frozen,
// while the rest of the list moves on; a key added again while its old row
// leaves gets a new row; and rows rebuilt for a changed item neither
// leave nor enter.
func TestMotionFor(t *testing.T) {
	got := runAppWith(t, testList+`
component App() {
    h := hash()
    <div>
        <ul>
            {for _, s := range items(h.Get()) key s {
                <li enter={fade()} exit={pick(s)}>{s}</li>.
            }}
        </ul>
        <ol>
            {for _, it := range structs(h.Get()) key it.K {
                <li exit={slide("up")}>{it.V}</li>
            }}
        </ol>
    </div>
}`, `location.hash = "#a,b,c";`, `
const ul = find("ul"), ol = find("ol");
const show = (msg) => log(msg + ":", html(ul), "|", running().join(" "));
const go = async (h) => { location.hash = h; await tick(); await tick(); };
show("first");
await go("#a,c"); show("remove b");
await go("#a,b,c"); show("b again");
finishAll(); show("done");
await go("#a,x,b,c"); finishAll(); show("add x");
await go("#a,b,c"); show("x has no exit");
await go("#"); show("clear");
await go("#d"); show("d during exits");
finishAll(); show("done");
const inOl = () => anims.filter((a) => a.playState === "running" && a.el.parentNode === ol).map((a) => a.el.textContent).join(" ");
await go("#1=a,2=b,3=c"); log(html(ol), "|", inOl());
await go("#1=A,3=c"); log(html(ol), "|", inOl());
await go("#1=A,3=C"); log(html(ol), "|", inOl());
await go("#"); log(html(ol), "|", inOl());
finishAll(); log(html(ol));`)
	expectLines(t, got,
		`first: <ul><li>a</li>.<li>b</li>.<li>c</li>.</ul> | `,
		`remove b: <ul><li>a</li>.<li>b</li><li>c</li>.</ul> | li:b`,
		`b again: <ul><li>a</li>.<li>b</li><li>b</li>.<li>c</li>.</ul> | li:b li:b`,
		`done: <ul><li>a</li>.<li>b</li>.<li>c</li>.</ul> | `,
		`add x: <ul><li>a</li>.<li>x</li>.<li>b</li>.<li>c</li>.</ul> | `,
		`x has no exit: <ul><li>a</li>.<li>b</li>.<li>c</li>.</ul> | `,
		`clear: <ul><li>a</li><li>b</li><li>c</li></ul> | li:a li:b li:c`,
		`d during exits: <ul><li>a</li><li>b</li><li>c</li><li>d</li>.</ul> | li:a li:b li:c li:d`,
		`done: <ul><li>d</li>.</ul> | `,
		// Struct items are new on every change, so every row is rebuilt,
		// in its place, even while one leaves.
		`<ol><li>a</li><li>b</li><li>c</li></ol> | `,
		`<ol><li>A</li><li>b</li><li>c</li></ol> | b`,
		`<ol><li>A</li><li>b</li><li>C</li></ol> | b`,
		`<ol><li>A</li><li>b</li><li>C</li></ol> | b A C`,
		`<ol></ol>`)
}

// animate={layout()}: rows that move glide from where they were, and the
// rows after a leaving row glide up when it is finally removed.
func TestMotionLayout(t *testing.T) {
	got := runAppWith(t, testList+`
component App() {
    h := hash()
    <ul>
        {for _, s := range items(h.Get()) key s {
            <li exit={fade()} animate={layout()}>{s}</li>
        }}
    </ul>
}`, `location.hash = "#a,b,c";`, logAnims+`
const go = async (h) => { location.hash = h; await tick(); await tick(); };
await go("#c,a,b"); logNew();
await go("#c,b"); logNew();
finishAll(); logNew();
log(html());`)
	expectLines(t, got,
		`li:c {"transform":"translate(0px, 20px)","offset":0} {"duration":200,"easing":"ease-out"}`,
		`li:a {"transform":"translate(0px, -10px)","offset":0} {"duration":200,"easing":"ease-out"}`,
		`li:b {"transform":"translate(0px, -10px)","offset":0} {"duration":200,"easing":"ease-out"}`,
		`li:a {"opacity":0} {"duration":200,"easing":"ease-in","fill":"forwards"}`,
		`li:b {"transform":"translate(0px, 10px)","offset":0} {"duration":200,"easing":"ease-out"}`,
		`<ul><li>c</li><li>b</li></ul>`)
}

// Under prefers-reduced-motion, and in a browser without element.animate,
// nothing animates: leaving nodes go at once and lists do not glide.
func TestMotionReduced(t *testing.T) {
	src := testList + `
component App() {
    h := hash()
    <div>
        {if h.Get() != "#a" {
            <p enter={fade()} exit={fade()}>p</p>
        }}
        <ul>
            {for _, s := range items(h.Get()) key s {
                <li enter={scale()} exit={fade()} animate={layout()}>{s}</li>
            }}
        </ul>
    </div>
}`
	script := `
const go = async (h) => { location.hash = h; await tick(); await tick(); };
await go("#a"); log(html());
await go("#b,a"); log(html());
await go("#a,b"); log(html(), anims.length);`
	for _, before := range []string{`location.hash = "#b"; reduced = true;`, `location.hash = "#b"; delete Element.prototype.animate;`} {
		got := runAppWith(t, src, before, script)
		expectLines(t, got,
			`<div><ul><li>a</li></ul></div>`,
			`<div><p>p</p><ul><li>b</li><li>a</li></ul></div>`,
			`<div><p>p</p><ul><li>a</li><li>b</li></ul></div> 0`)
	}
}

// spring(x) starts at x and follows it, stepping once a frame only while
// it moves; it settles exactly on its target. It stops with the branch
// that made it, and jumps under reduced motion.
func TestSpring(t *testing.T) {
	src := `
component App() {
    to := signal(0.0)
    show := signal(true)
    <div>
        <button onclick={to.Set(to.Get() + 100)}>go</button>
        <button onclick={show.Set(false)}>hide</button>
        {if show.Get() {
            <Dot to={to} />
        }}
    </div>
}

component Dot(to *Signal[float64]) {
    x := spring(to.Get() * 2)
    <p>{x.Get()}</p>
}`
	got := runApp(t, src, `
const [go, hide] = findAll("button");
log(find("p").textContent, rafs.size);
click(go); await tick();
log(rafs.size);
frame(); await tick();
const v1 = Number(find("p").textContent);
frame(); await tick();
const v2 = Number(find("p").textContent);
log(v1 > 0 && v1 < v2 && v2 < 200);
const n = frames(); await tick();
log(find("p").textContent, rafs.size, n > 20 && n < 120);
click(go); await tick(); frame(); await tick();
log(rafs.size);
click(hide); await tick();
log(rafs.size);`)
	expectLines(t, got, `0 0`, `1`, `true`, `200 0 true`, `1`, `0`)

	got = runAppWith(t, src, `reduced = true;`, `
click(findAll("button")[0]); await tick();
log(find("p").textContent, rafs.size);`)
	expectLines(t, got, `200 0`)
}

// Transitions are values: a variable, a prop or a function's result
// works as well as a call in the attribute. The zero Transition plays
// nothing, and a copy of it with .For neither.
func TestMotionValues(t *testing.T) {
	got := runApp(t, `
const Slow den.Duration = "0.5s"

component App() {
    show := signal(true)
    out := slide("right").For(Slow)
    <div>
        <button onclick={show.Set(!show.Get())}>t</button>
        <List show={show} out={out} none={Transition{}} />
        <svg>{if show.Get() { <circle exit={out.For("1e2ms")} r="1" /> }}</svg>
    </div>
}

component List(show *Signal[bool], out Transition, none Transition) {
    var zero Transition
    <ul>
        {if show.Get() {
            <li exit={out}>a</li>
            <li exit={none}>b</li>
            <li exit={zero.For("1s")}>c</li>
        }}
    </ul>
}`, `
click(find("button")); await tick();
log(html());
log(anims.map((a) => a.el.tagName + " " + JSON.stringify(a.k) + " " + a.o.duration).join("\n"));
finishAll();`)
	expectLines(t, got,
		`<div><button>t</button><ul><li>a</li></ul><svg><circle r="1"></circle></svg></div>`,
		`li {"opacity":0,"transform":"translateX(100%)"} 500`,
		`circle {"opacity":0,"transform":"translateX(100%)"} 100`)
}

// Programs keep their own Transition, Layout, scale and so on; den's then
// give way, and the others still work. A program with its own den writes
// .For's duration as a string.
func TestRunOwnMotionNames(t *testing.T) {
	for _, c := range []struct{ src, want string }{{`
func scale(x int) int { return x * 2 }

type Layout struct{ W int }

component App() {
    show := signal(true)
    <div>{if show.Get() { <p enter={fade()} exit={slide("up")}>{scale(2)} {Layout{W: 3}.W}</p> }}</div>
}`, `<div><p>4 3</p></div>`}, {`
type den struct{ N int }

component App() {
    show := signal(true)
    xs := signal([]int{1})
    <div>
        {if show.Get() { <p exit={fade().For("2s")}>{den{N: 5}.N}</p> }}
        <ul>{for _, x := range xs.Get() key x { <li animate={layout()}>{x}</li> }}</ul>
    </div>
}`, `<div><p>5</p><ul><li>1</li></ul></div>`}, {`
func spring(n int) int { return n + 1 }

component App() {
    <p>{spring(1)}</p>
}`, `<p>2</p>`}} {
		expectLines(t, runApp(t, c.src, `log(html());`), c.want)
	}
}

// The rows of a list directly in an {if} branch are elements that branch
// adds and removes: they enter and leave with it.
func TestMotionListInBranch(t *testing.T) {
	got := runApp(t, `
component App() {
    show := signal(true)
    xs := signal([]string{"a", "b"})
    <div>
        <button onclick={show.Set(!show.Get())}>t</button>
        {if show.Get() {
            {for _, x := range xs.Get() key x {
                <i enter={fade()} exit={fade()}>{x}</i>
            }}
        }}
    </div>
}`, `
log(running().length);
click(find("button")); await tick();
log(html(), running().join(" "));
finishAll();
click(find("button")); await tick();
log(html(), running().join(" "));`)
	expectLines(t, got,
		`0`,
		`<div><button>t</button><i>a</i><i>b</i></div> i:a i:b`,
		`<div><button>t</button><i>a</i><i>b</i></div> i:a i:b`)
}

// A leaving element takes no input. SVG has no inert, so it also gets
// pointer-events: none; focus inside it is dropped, and an event that
// still reaches it (here dispatched straight at it) stops before den's
// handlers see it, delegated row handlers included.
func TestMotionLeavingTakesNoInput(t *testing.T) {
	got := runApp(t, `
func without(xs []string, x string) []string {
    var out []string
    for _, y := range xs {
        if y != x {
            out = append(out, y)
        }
    }
    return out
}

component App() {
    xs := signal([]string{"a", "b"})
    seen := signal("")
    <div>
        <p>{seen.Get()}</p>
        <svg>
            {for _, x := range xs.Get() key x {
                <rect exit={fade()} onclick={
                    seen.Set(seen.Get() + "svg:" + x + ";")
                    xs.Set(without(xs.Get(), x))
                } />
            }}
        </svg>
        <ul>
            {for _, x := range xs.Get() key x {
                <li exit={fade()}><button onclick={seen.Set(seen.Get() + "html:" + x + ";")} onkeydown={seen.Set(seen.Get() + "key:" + x + ";")}>{x}</button></li>
            }}
        </ul>
    </div>
}`, `
const ra = findAll("rect")[0], ba = find("button", "a"), la = find("li");
ba.focus();
click(ra); await tick();
log(find("p").textContent, ra.style.pointerEvents, ra.inert, la.style.pointerEvents, la.inert, document.activeElement === null);
click(ra); click(ba); key(ba, "Enter"); await tick();
ba.focus(); ba.dispatch("focusin");
log(find("p").textContent, document.activeElement === null, findAll("rect").length);
click(findAll("rect")[1]); await tick();
log(find("p").textContent);
finishAll();`)
	expectLines(t, got,
		`svg:a; none true none true true`,
		`svg:a; true 2`,
		`svg:a;svg:b;`)
}

// Without animation frames (a hidden tab) an exit animation never
// finishes; a timer then ends it 100ms after it should have finished,
// as a finish would, layout glide included, and only once.
func TestMotionNoFrames(t *testing.T) {
	got := runAppWith(t, testList+`
component App() {
    h := hash()
    show := signal(true)
    <div>
        <button onclick={show.Set(false)}>hide</button>
        {if show.Get() {
            <ul>
                {for _, s := range items(h.Get()) key s {
                    <li exit={fade().For("0ms")} animate={layout()}>{s}</li>
                }}
            </ul>
        }}
    </div>
}`, `location.hash = "#a,b,c";`, `
const go = async (h) => { location.hash = h; await tick(); await tick(); };
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const state = () => anims.map((a) => a.el.textContent + ":" + a.playState + (a.k.transform ? ":" + a.k.transform : "")).join(" ");
await go("#a,c");
log(html(find("ul")), state());
await wait(150);
log(html(find("ul")), state());
anims.find((a) => a.el.textContent === "b").onfinish(); // a finish event that comes late
log(state());
anims.length = 0;
await go("#a");
finishAll();
log(html(find("ul")), state());
await wait(150);
log(html(find("ul")), state());
anims.length = 0;
await go("#");
click(find("button")); await tick();
await wait(150);
log(html(), state());`)
	expectLines(t, got,
		`<ul><li>a</li><li>b</li><li>c</li></ul> b:running`,
		`<ul><li>a</li><li>c</li></ul> b:idle c:running:translate(0px, 10px)`,
		`b:idle c:running:translate(0px, 10px)`,
		`<ul><li>a</li></ul> c:finished`,
		`<ul><li>a</li></ul> c:finished`,
		`<div><button>hide</button></div> a:idle`)
}

// Handlers den attaches to the element itself, for events that do not
// bubble (mouseenter, focus), stop too once it is leaving, on a child of
// the leaving element (which may take pointer events of its own) and on
// the leaving element itself.
func TestMotionLeavingStopsDirectHandlers(t *testing.T) {
	got := runApp(t, `
component App() {
    open := signal(true)
    seen := signal("")
    <div>
        <p>{seen.Get()}</p>
        <button onclick={open.Set(false)}>close</button>
        <svg>
            {if open.Get() {
                <g exit={fade().For("6s")}>
                    <circle pointer-events="all" r="5" onmouseenter={seen.Set(seen.Get() + "enter;")} />
                </g>
            }}
        </svg>
        {if open.Get() {
            <input exit={fade()} onfocus={seen.Set(seen.Get() + "focus;")} />
        }}
    </div>
}`, `
const c = find("circle"), inp = find("input");
c.dispatch("mouseenter"); inp.dispatch("focus"); await tick();
log(find("p").textContent);
click(find("button")); await tick();
c.dispatch("mouseenter"); inp.dispatch("focus"); await tick();
log(find("p").textContent, findAll("circle").length, findAll("input").length);
finishAll();`)
	expectLines(t, got, `enter;focus;`, `enter;focus; 1 1`)
}
