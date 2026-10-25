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
