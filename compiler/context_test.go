package compiler

import (
	"os"
	"strings"
	"testing"
)

// Compound components: Tabs provides its state, and the Tab elements that
// Tabs' caller writes as its children use it, because children see the
// context of the component that renders them, not of the one that wrote
// them. Two Tabs keep their own state.
func TestRunContextCompound(t *testing.T) {
	got := runApp(t, `
type TabState struct {
    Active *Signal[string]
}

component Tabs(first string, children den.Children) {
    provide(TabState{Active: signal(first)})
    <div class="tabs">{children}</div>
}

component Tab(name string) {
    s := use[TabState]()
    <button class={s.Active.Get() == name} onclick={s.Active.Set(name)}>{name}</button>
}

component Panel(name string, children den.Children) {
    s := use[TabState]()
    <section>{if s.Active.Get() == name { {children} }}</section>
}

component App() {
    <main>
        <Tabs first="a">
            <Tab name="a" />
            <Tab name="b" />
            <Panel name="a"><p>A</p></Panel>
            <Panel name="b"><p>B</p></Panel>
        </Tabs>
        <Tabs first="y">
            <Tab name="x" />
            <Tab name="y" />
        </Tabs>
    </main>
}`, `
const show = () => findAll("div").map((d) => findAll("button", d).map((b) => b.textContent + (b.getAttribute("class") === "" ? "*" : "")).join(" ") + " " + findAll("p", d).map((p) => p.textContent).join("")).map((s) => s.trim()).join(" | ");
log(show());
click(find("button", "b")); await tick();
log(show());
click(find("button", "x")); await tick();
log(show());`)
	expectLines(t, got,
		`a* b A | x y*`,
		`a b* B | x y*`,
		`a b* B | x* y`)
}

// The nearest provider of a type wins for its subtree; elsewhere the outer
// one is seen, and with no provider at all, or for a type that nothing
// provides, use returns the zero value.
func TestRunContextNearest(t *testing.T) {
	got := runApp(t, `
type Name struct{ S string }

type Count int

type Missing struct {
    N  int
    Xs []int
    M  map[string]int
}

component Show(where string) {
    n := use[Name]()
    c := use[Count]()
    m := use[Missing]()
    <p>{where}:{n.S}:{c}:{m.N}:{len(m.Xs)}:{len(m.M)}</p>
}

component Inner(children den.Children) {
    provide(Name{"inner"})
    <div><Show where="inside" />{children}</div>
}

component Outer() {
    provide(Name{"outer"})
    provide(Count(7))
    <div>
        <Show where="before" />
        <Inner><Show where="child" /></Inner>
        <Show where="after" />
    </div>
}

component App() {
    <main><Show where="root" /><Outer /></main>
}`, `log(findAll("p").map((p) => p.textContent).join(" "));`)
	expectLines(t, got, `root::0:0:0:0 before:outer:7:0:0:0 inside:inner:7:0:0:0 child:inner:7:0:0:0 after:outer:7:0:0:0`)
}

// A provide applies from where it is written: the component's own later
// statements see it, earlier ones (and closures written before it, even
// when they run later) see the context it was given, and a second provide
// of the same type replaces the first for what follows.
func TestRunContextSameComponent(t *testing.T) {
	got := runApp(t, `
type Name struct{ S string }

component Show() {
    <i>{use[Name]().S}</i>
}

component Mid() {
    before := use[Name]().S
    early := func() string { return use[Name]().S }
    provide(Name{before + "+mid"})
    after := use[Name]().S
    provide(Name{after + "+again"})
    <p>{before}|{early()}|{after}|{use[Name]().S}|<Show /></p>
}

component App() {
    provide(Name{"app"})
    <Mid />
}`, `log(find("p").textContent);`)
	expectLines(t, got, `app|app|app+mid|app+mid+again|app+mid+again`)
}

// Components built by {if}, {for} and {key} blocks, with and without
// transitions, see the context of the component around them, each time the
// block builds them again.
func TestRunContextBlocks(t *testing.T) {
	got := runApp(t, `
type Name struct{ S string }

component Show(n int) {
    <i>{use[Name]().S}{n}</i>
}

component App() {
    on := signal(true)
    xs := signal([]int{1, 2})
    k := signal(0)
    provide(Name{"x"})
    <main>
        <div>{if on.Get() { <Show n={0} /> } else { <b>{use[Name]().S}-</b> }}</div>
        <div>{for _, n := range xs.Get() key n { <Show n={n} /> }}</div>
        <div>{key k.Get() { <Show n={k.Get()} /> }}</div>
        <div>{if on.Get() { <s exit={fade()}>{use[Name]().S}</s> }}</div>
        <div>{for _, n := range xs.Get() key n { <u enter={fade()}><Show n={n} /></u> }}</div>
        <div>{key k.Get() { <em exit={fade()}><Show n={k.Get()} /></em> }}</div>
        <button onclick={on.Set(!on.Get()); xs.Set(append(xs.Get(), len(xs.Get())+1)); k.Set(k.Get() + 1)}>go</button>
    </main>
}`, `
const show = () => findAll("div").map((d) => d.textContent).join(" ");
log(show());
click(find("button")); await tick(); finishAll();
log(show());
click(find("button")); await tick(); finishAll();
log(show());`)
	expectLines(t, got,
		`x0 x1x2 x0 x x1x2 x0`,
		`x- x1x2x3 x1  x1x2x3 x1`,
		`x0 x1x2x3x4 x2 x x1x2x3x4 x2`)
}

// A store is a struct of signals: providing it shares state that changes,
// and every component using it follows.
func TestRunContextStore(t *testing.T) {
	got := runApp(t, `
type Cart struct {
    Items *Signal[[]string]
    Open  *Signal[bool]
}

component Badge() {
    c := use[Cart]()
    <b>{len(c.Items.Get())}</b>
}

component Add(item string) {
    c := use[Cart]()
    <button onclick={c.Items.Set(append(c.Items.Get(), item))}>{item}</button>
}

component List() {
    c := use[Cart]()
    <ul>{if c.Open.Get() { {for i, s := range c.Items.Get() key i { <li>{s}</li> }} }}</ul>
}

component App() {
    cart := Cart{Items: signal([]string{}), Open: signal(false)}
    provide(cart)
    <main>
        <Badge />
        <Add item="tea" />
        <Add item="jam" />
        <button onclick={cart.Open.Set(!cart.Open.Get())}>open</button>
        <List />
    </main>
}`, `
const show = () => find("b").textContent + " " + html(find("ul"));
log(show());
click(find("button", "tea")); await tick();
click(find("button", "jam")); await tick();
log(show());
click(find("button", "open")); await tick();
log(show());
click(find("button", "tea")); await tick();
log(show());`)
	expectLines(t, got,
		`0 <ul></ul>`,
		`2 <ul></ul>`,
		`2 <ul><li>tea</li><li>jam</li></ul>`,
		`3 <ul><li>tea</li><li>jam</li><li>tea</li></ul>`)
}

// Each instance of a generic type is a type of its own, and an alias of a
// named type is that type.
func TestRunContextGeneric(t *testing.T) {
	got := runApp(t, `
type Box[T any] struct{ V T }

type IntBox = Box[int]

type Label string

type Tag = Label

component Show() {
    <p>{use[Box[int]]().V}|{use[Box[string]]().V}|{use[IntBox]().V}|{use[Box[bool]]().V}|{use[Label]()}|{use[Tag]()}</p>
}

component App() {
    provide(Box[string]{"s"})
    provide(IntBox{4})
    provide(Tag("t"))
    <Show />
}`, `log(find("p").textContent);`)
	expectLines(t, got, `4|s|4|false|t|t`)
}

// Markup passed on through another component's slot is rendered by that
// component, and sees its context; so do named slots, slots rendered in
// rows and branches of the callee, and slots built as SVG.
func TestRunContextSlots(t *testing.T) {
	got := runApp(t, `
type Name struct{ S string }

component Show() {
    <i>{use[Name]().S}</i>
}

component Frame(head den.Children, children den.Children) {
    provide(Name{"frame"})
    <section><h1>{head}</h1>{children}</section>
}

component Card(children den.Children) {
    provide(Name{"card"})
    <Frame><:head><Show /></:head>{children}</Frame>
}

component Rows(n int, children den.Children) {
    provide(Name{"rows"})
    <ul>{for i := range n key i { <li>{children}</li> }}</ul>
}

component Shape(children den.Children) {
    provide(Name{"shape"})
    <svg>{children}</svg>
}

component App() {
    provide(Name{"app"})
    <main>
        <Card><Show /></Card>
        <Rows n={2}><Show /></Rows>
        <Shape><circle data-n={use[Name]().S} /></Shape>
        <Show />
    </main>
}`, `
log(findAll("i").map((i) => i.textContent).join(" "));
log(find("circle").getAttribute("data-n"), find("circle").namespaceURI === "http://www.w3.org/2000/svg");`)
	expectLines(t, got,
		`frame frame rows rows app`,
		`shape true`)
}

// Handlers and bindings of list rows that use only the row's item are
// hoisted out of the rows, and single-element rows have their handlers
// delegated; using the context there finds the same context, in a
// component's own markup and in markup passed as children.
func TestRunContextRows(t *testing.T) {
	got := runApp(t, `
type Pre struct{ S string }

component Box(children den.Children) {
    provide(Pre{"box:"})
    <div>{children}</div>
}

component App() {
    xs := signal([]string{"a", "b"})
    picked := signal("")
    provide(Pre{"app:"})
    <main>
        <ul>{for _, x := range xs.Get() key x { <li onclick={picked.Set(use[Pre]().S + x)}>{use[Pre]().S + x}</li> }}</ul>
        <Box><ol>{for _, x := range xs.Get() key x { <li onclick={picked.Set(use[Pre]().S + x)}>{use[Pre]().S + x}</li> }}</ol></Box>
        <p>{picked.Get()}</p>
        <button onclick={xs.Set(append(xs.Get(), "c"))}>add</button>
    </main>
}`, `
const show = () => findAll("li").map((l) => l.textContent).join(" ") + " | " + find("p").textContent;
click(findAll("li", find("ul"))[1]); await tick();
log(show());
click(find("button", "add")); await tick();
click(findAll("li", find("ol"))[2]); await tick();
log(show());`)
	expectLines(t, got,
		`app:a app:b box:a box:b | app:b`,
		`app:a app:b app:c box:a box:b box:c | box:c`)
}

// With no declarations before them, components are written to the
// prelude's file by gen; context still finds them. A type declared in a
// component is a type of its own, and a {key} whose value reads no signal
// may read the context.
func TestRunContextLocalType(t *testing.T) {
	got := runApp(t, `
component App() {
    type L struct{ N int }
    provide(L{3})
    <p>{use[L]().N}{key use[L]().N { <b>{use[L]().N * 2}</b> }}</p>
}`, `log(find("p").textContent);`)
	expectLines(t, got, `36`)
}

// Bindings in the rows of a list that use only the row's item are hoisted
// out of the rows, to where the list is. Slot markup inside such a row is
// a closure the callee runs with its own context, so a binding there that
// uses the context must stay in the closure: hoisted, it read the caller's
// incoming context instead (a verifier's finding). Text and attribute
// bindings, with and without a provide in the callee: the render site wins.
func TestRunContextHoistedSlot(t *testing.T) {
	for _, c := range []struct{ box, want string }{
		{"", `app:a1/app:a1 app:b1/app:b1 | app:a2/app:a2 app:b2/app:b2 app:c2/app:c2`},
		{`provide(Name{"box"})`, `box:a1/box:a1 box:b1/box:b1 | box:a2/box:a2 box:b2/box:b2 box:c2/box:c2`},
	} {
		got := runApp(t, `
type Name struct{ S string }

component Box(children den.Children) {
    `+c.box+`
    <div>{children}</div>
}

component App() {
    xs := signal([]string{"a", "b"})
    n := signal(1)
    provide(Name{"app"})
    <main>
        <ul>{for _, x := range xs.Get() key x { <li><Box><b data-n={use[Name]().S + ":" + x + itoa(n.Get())}>{use[Name]().S + ":" + x + itoa(n.Get())}</b></Box></li> }}</ul>
        <button onclick={n.Set(2); xs.Set(append(xs.Get(), "c"))}>go</button>
    </main>
}`, `
const show = () => findAll("b").map((b) => b.getAttribute("data-n") + "/" + b.textContent).join(" ");
const before = show();
click(find("button")); await tick();
log(before, "|", show());`)
		expectLines(t, got, c.want)
	}
}

// The verifier's repro: a text binding alone.
func TestRunContextHoistedSlotText(t *testing.T) {
	got := runApp(t, `
type Name struct{ S string }
component Box(children den.Children) { <div>{children}</div> }
component App() {
    xs := signal([]string{"a", "b"})
    n := signal(1)
    provide(Name{"app"})
    <ul>{for _, x := range xs.Get() key x { <li><Box><b>{use[Name]().S + ":" + x + itoa(n.Get())}</b></Box></li> }}</ul>
}`, `log(findAll("b").map((b) => b.textContent).join(" "));`)
	expectLines(t, got, `app:a1 app:b1`)
}

// examples/theme: the theme store reaches the toggle and the swatches, and
// the Tab and Panel elements App writes inside each Tabs follow that Tabs.
func TestThemeExample(t *testing.T) {
	src, err := os.ReadFile("../examples/theme/app.den")
	if err != nil {
		t.Fatal(err)
	}
	got := runApp(t, string(src), `
const tabs = () => findAll("button").filter((b) => b.getAttribute("role") === "tab").map((b) => b.textContent + (b.getAttribute("aria-selected") === "true" ? "*" : "")).join(" ");
const show = () => find("main").getAttribute("class") + " | " + tabs() + " | " + findAll("section").map((s) => s.textContent.trim()).filter((t) => t).join(" / ");
log(show());
click(find("button", "Dark")); await tick();
click(find("button", "rose")); await tick();
log(show(), localStorage.getItem("theme-dark"), localStorage.getItem("theme-accent"));
click(find("button", "Nested")); await tick();
log(show());
click(find("button", "Two")); await tick();
log(show());
click(find("button", "How it works")); await tick();
click(find("button", "Nested")); await tick();
log(show());`)
	expectLines(t, got,
		`app accent-teal | Look* Nested How it works | Pick an accent. Each swatch uses the theme it finds above it.tealvioletamberrose`,
		`app accent-rose dark | Look* Nested How it works | Pick an accent. Each swatch uses the theme it finds above it.tealvioletamberrose true "rose"`,
		`app accent-rose dark | Look Nested* How it works One* Two | A Tabs inside a Tabs: each Tab finds the nearest one, so the two keep their own state.OneTwoThe inner tabs are on one. / The inner tabs are on one.`,
		`app accent-rose dark | Look Nested* How it works One Two* | A Tabs inside a Tabs: each Tab finds the nearest one, so the two keep their own state.OneTwoThe inner tabs are on two. / The inner tabs are on two.`,
		`app accent-rose dark | Look Nested* How it works One* Two | A Tabs inside a Tabs: each Tab finds the nearest one, so the two keep their own state.OneTwoThe inner tabs are on one. / The inner tabs are on one.`)
}

// A program that provides nothing pays nothing: its components take no
// context, and use is the zero value.
func TestContextCostsNothingUnused(t *testing.T) {
	res, err := compileOne(`
type Name struct{ S string }

component Show(children den.Children) {
    <p>{use[Name]().S}{children}</p>
}

component App() {
    <Show>x</Show>
}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"$x", "__proto__"} {
		if strings.Contains(res.JS, bad) {
			t.Errorf("output has %s:\n%s", bad, res.JS)
		}
	}
	if !strings.Contains(res.JS, `({S: ""}).S`) {
		t.Errorf("use is not the zero value:\n%s", res.JS)
	}
}

// provide and use are ordinary names: a program with its own keeps them.
func TestRunOwnContextNames(t *testing.T) {
	got := runApp(t, `
func use(n int) int { return n * 2 }

type provide struct{ N int }

component App() {
    p := provide{N: use(3)}
    <p>{p.N}</p>
}`, `log(find("p").textContent);`)
	expectLines(t, got, `6`)
}
