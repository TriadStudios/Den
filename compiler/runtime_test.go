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
	prog := string(dom) + "\n" + res.JS + "\n(async () => {\n" + script +
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
		`<ul><li>a</li><li class="">b</li><li>c</li><li>d</li></ul> 3 left`,
		`b rebuilt, others kept: 0,new,2,3`,
		// Reversing 4 rows keeps the longest run in place and moves the other 3.
		`dcba ops 3 kept 3,2,new,0`,
		`cbaz 3 left`,
		`<ul></ul> 0 left`,
		`<ul><li>z</li></ul>`)
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
	got := runRuntime(t, "$signal $computed $effect $scope", `
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
const [, dispose] = $scope(() => $effect(() => seen.push(d.get())));
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

dispose();
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
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			main := "package main\n\nimport (\n\t\"fmt\"\n\t\"strconv\"\n)\n\nfunc itoa(n int) string { return strconv.Itoa(n) }\n\nfunc main() { fmt.Println(run()) }\n\n" + string(src)
			os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644)
			os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module conform\n\ngo 1.24\n"), 0o644)
			cmd := exec.Command(goTool, "run", ".")
			cmd.Dir = dir
			want, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go run: %v\n%s", err, want)
			}
			got := runApp(t, string(src)+"\ncomponent App() {\n    <p>{run()}</p>\n}\n", `log(find("p").textContent);`)
			if got != strings.TrimSpace(string(want)) {
				t.Errorf("den and Go differ\n  go: %s\n den: %s", strings.TrimSpace(string(want)), got)
			}
		})
	}
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
		`<a href="about:blank#den-blocked-url">x</a>`)
}
