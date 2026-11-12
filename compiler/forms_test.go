package compiler

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Text inputs and textareas bind strings both ways, and the element's own
// handlers still run, after bind has set the signal.
func TestRunBindText(t *testing.T) {
	got := runApp(t, `
component App() {
    name := signal("Ann")
    seen := signal("")
    <div>
        <input type="email" bind={name} oninput={seen.Set("saw " + name.Get())} />
        <textarea bind={name} />
        <p>{name.Get()}|{seen.Get()}</p>
        <button onclick={name.Set("Bo")}>set</button>
    </div>
}`, `
const a = find("input"), ta = find("textarea");
const show = () => log(a.value, ta.value, find("p").textContent);
show();
input(a, "Cy"); await tick(); show();
input(ta, "Di"); await tick(); show();
click(find("button")); await tick(); show();`)
	expectLines(t, got,
		`Ann Ann Ann|`,
		`Cy Cy Cy|saw Cy`,
		`Di Di Di|saw Cy`,
		`Bo Bo Bo|saw Cy`)
}

// Number and range inputs parse what is typed: ints as strconv.Atoi does,
// within the range of the kind, and floats as decimal numbers. Text that
// does not parse leaves the signal as it is, and text that already means
// the value ("01", "2.") is never rewritten under the cursor.
func TestRunBindNumber(t *testing.T) {
	got := runApp(t, `
component App() {
    n := signal(5)
    f := signal(1.5)
    b := signal(int8(0))
    u := signal(uint(3))
    <div>
        <input type="number" bind={n} />
        <input type="range" bind={f} />
        <input type="number" bind={b} />
        <input type="NUMBER" bind={u} />
        <p>{n.Get()} {f.Get()} {b.Get()} {u.Get()}</p>
        <button onclick={n.Set(n.Get() + 1); f.Set(0.25)}>inc</button>
    </div>
}`, `
const [a, r, c, d] = findAll("input");
const show = () => log(JSON.stringify([a.value, r.value, c.value, d.value]), find("p").textContent);
show();
input(a, "01"); await tick(); show();
input(a, "abc"); await tick(); show();
input(a, ""); await tick(); show();
input(a, "7.5"); await tick(); show();
input(a, "+9"); await tick(); show();
click(find("button")); await tick(); show();
input(r, "2."); await tick(); show();
input(r, "1e1"); await tick(); show();
input(r, ".5"); await tick(); show();
input(r, "1e999"); await tick(); show();
input(r, "1e"); await tick(); show();
input(c, "300"); await tick(); show();
input(c, "-128"); await tick(); show();
input(d, "-1"); await tick(); show();
input(d, "42"); await tick(); show();`)
	expectLines(t, got,
		`["5","1.5","0","3"] 5 1.5 0 3`,
		`["01","1.5","0","3"] 1 1.5 0 3`,
		`["abc","1.5","0","3"] 1 1.5 0 3`,
		`["","1.5","0","3"] 1 1.5 0 3`,
		`["7.5","1.5","0","3"] 1 1.5 0 3`,
		`["+9","1.5","0","3"] 9 1.5 0 3`,
		`["10","0.25","0","3"] 10 0.25 0 3`,
		`["10","2.","0","3"] 10 2 0 3`,
		`["10","1e1","0","3"] 10 10 0 3`,
		`["10",".5","0","3"] 10 0.5 0 3`,
		`["10","1e999","0","3"] 10 0.5 0 3`,
		`["10","1e","0","3"] 10 0.5 0 3`,
		`["10","1e","300","3"] 10 0.5 0 3`,
		`["10","1e","-128","3"] 10 0.5 -128 3`,
		`["10","1e","-128","-1"] 10 0.5 -128 3`,
		`["10","1e","-128","42"] 10 0.5 -128 42`)
}

// A field keeps the parse error of its element's text until the text
// parses again, and its error and touched state are reactive. Set and
// Reset clear the parse error and show the value; focusout and Touch()
// touch it.
func TestRunBindField(t *testing.T) {
	got := runApp(t, `
import "errors"

component App() {
    age := field(18, func(n int) error {
        if n < 13 {
            return errors.New("too young")
        }
        return nil
    })
    <div>
        <input type="number" bind={age} />
        {if age.Touched() && age.Error() != nil {
            <p>{age.Error().Error()}</p>
        }}
        <i>{age.Get()} {age.Touched()}</i>
        <button onclick={age.Touch()}>touch</button>
        <button onclick={age.Reset()}>reset</button>
        <button onclick={age.Set(40)}>forty</button>
    </div>
}`, `
const a = find("input"), btn = (s) => find("button", s);
const show = () => log(JSON.stringify(a.value), findAll("p").map((p) => p.textContent).join() || "-", find("i").textContent);
show();
input(a, "12"); await tick(); show();
leave(a); await tick(); show();
input(a, "1x"); await tick(); show();
input(a, "1."); await tick(); show();
input(a, "abc"); await tick(); show();
click(btn("forty")); await tick(); show();
input(a, "abc"); await tick(); show();
click(btn("forty")); await tick(); show();
click(btn("reset")); await tick(); show();
click(btn("touch")); await tick(); show();
input(a, "5"); await tick(); show();`)
	expectLines(t, got,
		`"18" - 18 false`,
		`"12" - 12 false`,
		`"12" too young 12 true`,
		`"1x" strconv.Atoi: parsing "1x": invalid syntax 12 true`,
		`"1." strconv.Atoi: parsing "1.": invalid syntax 12 true`,
		`"abc" strconv.Atoi: parsing "abc": invalid syntax 12 true`,
		`"40" - 40 true`,
		`"abc" strconv.Atoi: parsing "abc": invalid syntax 40 true`,
		`"40" - 40 true`,
		`"18" - 18 false`,
		`"18" - 18 true`,
		`"5" too young 5 true`)
}

// A string field with a check, as in a signup form, and one with no check.
func TestRunBindFieldString(t *testing.T) {
	got := runApp(t, `
import (
    "errors"
    "strings"
)

component App() {
    email := field("", func(s string) error {
        if !strings.Contains(s, "@") {
            return errors.New("enter an email address")
        }
        return nil
    })
    note := field("hi", nil)
    ok := computed(email.Error() == nil && note.Error() == nil)
    <form onsubmit={e.PreventDefault(); email.Touch(); note.Touch()}>
        <input type="email" bind={email} />
        <textarea bind={note} />
        {if email.Touched() && email.Error() != nil {
            <p>{email.Error().Error()}</p>
        }}
        <b>{ok.Get()} {note.Touched()}</b>
    </form>
}`, `
const a = find("input"), ta = find("textarea");
const show = () => log(findAll("p").map((p) => p.textContent).join() || "-", find("b").textContent);
show();
find("form").dispatch("submit"); await tick(); show();
input(a, "me@x"); await tick(); show();
input(ta, ""); leave(ta); await tick(); show();`)
	expectLines(t, got,
		`- false false`,
		`enter an email address false true`,
		`- true true`,
		`- true true`)
}

// Checkboxes bind bools; radios check while the signal equals their
// value, static or not, of any basic type, and checking one sets it.
// Radios in a keyed list, with a delegated handler of their own, too.
func TestRunBindCheckboxRadio(t *testing.T) {
	got := runApp(t, `
component App() {
    agree := signal(false)
    plan := signal("free")
    n := signal(2)
    ns := signal([]int{1, 2, 3})
    seen := signal(0)
    yes := signal(true)
    <div>
        <input type="checkbox" bind={agree} />
        <input type="radio" name="p" value="free" bind={plan} />
        <input type="radio" name="p" value="pro" bind={plan} />
        {for _, v := range ns.Get() key v {
            <input type="radio" value={v} bind={n} onchange={seen.Set(v)} />
        }}
        <input type="radio" value={false} bind={yes} />
        <p>{agree.Get()} {plan.Get()} {n.Get()} {seen.Get()} {yes.Get()}</p>
        <button onclick={plan.Set("pro"); n.Set(3); agree.Set(true)}>set</button>
        <button onclick={ns.Set([]int{3, 4, 1})}>list</button>
    </div>
}`, `
const radios = () => findAll("input").filter((i) => i.getAttribute("type") === "radio");
const show = () => log(findAll("input").map((i) => (i.checked ? 1 : 0)).join(""), radios().slice(2).map((i) => i.value).join(","), find("p").textContent);
show();
check(findAll("input")[0], true); await tick(); show();
check(radios()[1], true); await tick(); show();
check(radios()[2], true); await tick(); show();
click(find("button", "set")); await tick(); show();
click(find("button", "list")); await tick(); show();
check(radios()[3], true); await tick(); show();
check(radios().at(-1), true); await tick(); show();`)
	expectLines(t, got,
		`0100100 1,2,3,false false free 2 0 true`,
		`1100100 1,2,3,false true free 2 0 true`,
		`1010100 1,2,3,false true pro 2 0 true`,
		`1011000 1,2,3,false true pro 1 1 true`,
		`1010010 1,2,3,false true pro 3 1 true`,
		`1011000 3,4,1,false true pro 3 1 true`,
		`1010100 3,4,1,false true pro 4 4 true`,
		`1010101 3,4,1,false true pro 4 4 false`)
}

// A select binds its option's value, parsed as T. It is bound after its
// options are made, so options from a {for} are selected from the start.
// An option whose value does not parse leaves an int signal as it is.
func TestRunBindSelect(t *testing.T) {
	got := runApp(t, `
component App() {
    plan := signal("pro")
    n := signal(2)
    f := signal(0.5)
    ns := signal([]int{1, 2, 3})
    <div>
        <select bind={plan}><option value="free">Free</option><option value="pro">Pro</option></select>
        <select bind={n}>
            <option value="none">none</option>
            {for _, v := range ns.Get() key v {
                <option value={v}>{v}</option>
            }}
        </select>
        <select bind={f}><option>0.25</option><option>0.5</option></select>
        <p>{plan.Get()} {n.Get()} {f.Get()}</p>
        <button onclick={plan.Set("pro"); n.Set(1)}>set</button>
    </div>
}`, `
const [s1, s2, s3] = findAll("select");
const show = () => log(s1.value, s2.value, s3.value, find("p").textContent);
show();
choose(s2, "3"); await tick(); show();
choose(s2, "none"); await tick(); show();
choose(s1, "free"); choose(s3, "0.25"); await tick(); show();
click(find("button")); await tick(); show();`)
	expectLines(t, got,
		`pro 2 0.5 pro 2 0.5`,
		`pro 3 0.5 pro 3 0.5`,
		`pro none 0.5 pro 3 0.5`,
		`free none 0.25 free 3 0.25`,
		`pro 1 0.25 pro 1 0.25`)
}

// bind works in {if} branches, {key} blocks and keyed list rows, where
// its signal may be the row's item's.
func TestRunBindInBlocks(t *testing.T) {
	got := runApp(t, `
type Row struct {
    ID   int
    Name *Signal[string]
}

component App() {
    show := signal(true)
    k := signal(0)
    name := signal("x")
    rows := signal([]Row{{1, signal("a")}, {2, signal("b")}})
    <div>
        {if show.Get() {
            <input class="if" bind={name} />
        }}
        {key k.Get() {
            <input class="key" bind={name} />
        }}
        <ul>
            {for _, r := range rows.Get() key r.ID {
                <li><input bind={r.Name} onkeydown={r.Name.Set("")} /></li>
            }}
        </ul>
        <p>{name.Get()} {for _, r := range rows.Get() key r.ID {{r.Name.Get()}}}</p>
        <button onclick={show.Set(!show.Get())}>show</button>
        <button onclick={k.Set(k.Get() + 1)}>key</button>
        <button onclick={rows.Set([]Row{rows.Get()[1], rows.Get()[0]})}>swap</button>
    </div>
}`, `
const show = () => log(findAll("input").map((i) => (i.getAttribute("class") ?? "") + "=" + i.value).join(" "), find("p").textContent);
show();
input(find("input"), "y"); await tick(); show();
click(find("button", "show")); await tick(); show();
input(findAll("input")[0], "z"); await tick(); show();
click(find("button", "show")); await tick(); show();
click(find("button", "key")); await tick(); show();
input(findAll("input")[2], "A"); await tick(); show();
click(find("button", "swap")); await tick(); show();
input(findAll("input")[2], "B"); await tick(); show();
key(findAll("input")[3], "x"); await tick(); show();`)
	expectLines(t, got,
		`if=x key=x =a =b x ab`,
		`if=y key=y =a =b y ab`,
		`key=y =a =b y ab`,
		`key=z =a =b z ab`,
		`if=z key=z =a =b z ab`,
		`if=z key=z =a =b z ab`,
		`if=z key=z =A =b z Ab`,
		`if=z key=z =b =A z bA`,
		`if=z key=z =B =A z BA`,
		`if=z key=z =B = z B`)
}

// A leaving element takes no input: bind's events join $evs, so they are
// stopped too, and the field it binds stays as it was.
func TestMotionBind(t *testing.T) {
	src := `
component App() {
    show := signal(true)
    name := field("", nil)
    <div>
        {if show.Get() {
            <input exit={fade()} bind={name} />
        }}
        <p>{name.Get()} {name.Touched()}</p>
        <button onclick={show.Set(false)}>hide</button>
    </div>
}`
	res, err := compileOne(src)
	if err != nil {
		t.Fatal(err)
	}
	if want := `const $evs = ["click", "focusout", "input"];`; !strings.Contains(res.JS, want) {
		t.Errorf("JS has no %s", want)
	}
	got := runApp(t, src, `
const a = find("input");
input(a, "ok"); await tick(); log(find("p").textContent);
click(find("button")); await tick();
log(running().join());
input(a, "no"); a.dispatch("focusout"); await tick(); log(find("p").textContent);
finishAll(); log(findAll("input").length);`)
	expectLines(t, got, `ok false`, `input:`, `ok false`, `0`)
}

// Chromium fires focusout at a focused element that is removed, and
// $exit blurs focus inside a leaving element. Neither is the user leaving
// the field, and both happen while den updates the page: a field whose
// element den takes away stays untouched. Leaving it while it is shown
// touches it.
func TestRunBindFieldRemoved(t *testing.T) {
	got := runApp(t, `
component App() {
    show := signal(true)
    name := field("", nil)
    other := field("", nil)
    <div>
        {if show.Get() {
            <label><input class="a" bind={name} /></label>
        }}
        {if show.Get() {
            <input class="b" exit={fade()} bind={other} />
        }}
        <p>{name.Touched()} {other.Touched()}</p>
        <button onclick={show.Set(!show.Get())}>toggle</button>
    </div>
}`, `
focusEvents = true;
const toggle = () => click(find("button")), p = () => log(find("p").textContent);
find("input").focus();
toggle(); await tick(); p();
toggle(); await tick(); findAll("input").at(-1).focus();
toggle(); await tick(); p();
finishAll(); toggle(); await tick();
find("input").focus(); find("input").blur(); await tick(); p();
findAll("input")[1].focus(); findAll("input")[1].blur(); await tick(); p();`)
	expectLines(t, got, `false false`, `false false`, `true false`, `true true`)
}

// A bound input can have a ref too: onMount focuses it once it is in the
// document, in a component shown by an {if}. Typing still binds, leaving
// touches the field, and taking the component away while its input has
// focus does not.
func TestRunBindRefOnMount(t *testing.T) {
	got := runApp(t, `
component App() {
    open := signal(false)
    q := field("go", nil)
    <div>
        <button onclick={open.Set(!open.Get())}>open</button>
        {if open.Get() {
            <Search q={q} />
        }}
        <p>{q.Get()} {q.Touched()}</p>
    </div>
}

component Search(q *Field[string]) {
    box := ref()
    onMount(func() { box.Focus() })
    <input ref={box} bind={q} />
}`, `
focusEvents = true;
const p = () => log(find("p").textContent);
click(find("button")); await settle();
const a = find("input");
log(document.activeElement === a, a.focusedInDoc, a.value);
input(a, "den"); await tick(); p();
click(find("button")); await settle(); p();
click(find("button")); await settle();
log(document.activeElement === find("input"), find("input").value);
find("input").blur(); await tick(); p();`)
	expectLines(t, got, `true true go`, `den false`, `den false`, `true den`, `den true`)
}

// A Field can be provided as context and bound in a component below:
// bind={use[Form]().Email} reads no signal (use reads the context once,
// when the element is made), so den allows it.
func TestRunBindContextField(t *testing.T) {
	got := runApp(t, `
import (
    "errors"
    "strings"
)

type Form struct {
    Email *Field[string]
}

func checkEmail(s string) error {
    if !strings.Contains(s, "@") {
        return errors.New("enter an email address")
    }
    return nil
}

component App() {
    provide(Form{Email: field("", checkEmail)})
    f := use[Form]()
    <div>
        <EmailInput />
        {if f.Email.Touched() && f.Email.Error() != nil {
            <p>{f.Email.Error().Error()}</p>
        }}
        <b>{f.Email.Get()}</b>
        <button onclick={f.Email.Reset()}>reset</button>
    </div>
}

component EmailInput() {
    <label>Email <input type="email" bind={use[Form]().Email} /></label>
}`, `
const show = () => log(JSON.stringify(find("input").value), findAll("p").map((p) => p.textContent).join() || "-", "[" + find("b").textContent + "]");
show();
input(find("input"), "ada"); leave(find("input")); await tick(); show();
input(find("input"), "ada@x"); await tick(); show();
click(find("button")); await tick(); show();`)
	expectLines(t, got,
		`"" - []`,
		`"ada" enter an email address [ada]`,
		`"ada@x" - [ada@x]`,
		`"" - []`)
}

// bind in markup passed as children, which the callee shows in each of its
// keyed rows, where it reads the context the callee is built in: each
// row's copy binds the same signals, and a row added later is bound too.
func TestRunBindSlotRowsContext(t *testing.T) {
	got := runApp(t, `
type Theme struct {
    Name *Signal[string]
}

type Row struct {
    ID    int
    Label *Signal[string]
}

component App() {
    provide(Theme{Name: signal("light")})
    note := signal("n")
    rows := signal([]Row{{1, signal("a")}, {2, signal("b")}})
    <div>
        <List rows={rows}>
            <input class="note" bind={note} />
            <input class="theme" bind={use[Theme]().Name} />
        </List>
        <p>{note.Get()} {use[Theme]().Name.Get()}</p>
        <button onclick={rows.Set(append(rows.Get(), Row{3, signal("c")}))}>add</button>
    </div>
}

component List(rows *Signal[[]Row], children den.Children) {
    <ul>
        {for _, r := range rows.Get() key r.ID {
            <li><input class="label" bind={r.Label} />{children}</li>
        }}
    </ul>
}`, `
const vals = () => log(findAll("input").map((i) => i.getAttribute("class")[0] + "=" + i.value).join(" "), find("p").textContent);
vals();
input(findAll("input")[1], "N"); await tick(); vals();
input(findAll("input")[5], "dark"); await tick(); vals();
click(find("button")); await tick(); vals();
input(findAll("input")[6], "C"); await tick(); vals();`)
	expectLines(t, got,
		`l=a n=n t=light l=b n=n t=light n light`,
		`l=a n=N t=light l=b n=N t=light N light`,
		`l=a n=N t=dark l=b n=N t=dark N dark`,
		`l=a n=N t=dark l=b n=N t=dark l=c n=N t=dark N dark`,
		`l=a n=N t=dark l=b n=N t=dark l=C n=N t=dark N dark`)
}

// Programs that do not use bind, field or errors ship none of their code;
// each part ships alone.
func TestFormsTreeShaking(t *testing.T) {
	forms := []string{"$field", "$bind", "$str", "$int", "$float", "$check", "$radio", "$touch"}
	cases := []struct {
		name, src string
		ships     []string
	}{
		{"text", `
component App() {
    s := signal("")
    <input bind={s} />
}`, []string{"$bind", "$str"}},
		{"number", `
component App() {
    n := signal(int16(0))
    <input type="number" bind={n} />
}`, []string{"$bind", "$int"}},
		{"checkbox", `
component App() {
    b := signal(false)
    <input type="checkbox" bind={b} />
}`, []string{"$check"}},
		{"field", `
component App() {
    f := field(1.5, nil)
    <input type="range" bind={f} />
}`, []string{"$field", "$bind", "$float", "$touch"}},
		{"errors", `
import "errors"

component App() {
    <p>{errors.New("x").Error()}</p>
}`, nil},
	}
	dirs, _ := filepath.Glob("../examples/*")
	dirs = append(dirs, "../bench/den")
	for _, dir := range dirs {
		if filepath.Base(dir) == "form" {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(dir, "*.den"))
		var src strings.Builder
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			src.Write(b)
			src.WriteString("\n")
		}
		cases = append(cases, struct {
			name, src string
			ships     []string
		}{dir, src.String(), nil})
	}
	for _, c := range cases {
		res, err := compileOne(c.src)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		for _, w := range forms {
			if got, want := strings.Contains(res.JS, w+"("), slices.Contains(c.ships, w); got != want {
				t.Errorf("%s: ships %s: %t, want %t", c.name, w, got, want)
			}
		}
	}
}

// A program with its own field or Field keeps it, and has neither of den's.
func TestRunOwnField(t *testing.T) {
	got := runApp(t, `
type Field struct{ Name string }

func field(n string) Field { return Field{n} }

component App() {
    s := signal("a")
    <p>{field(s.Get()).Name}<input bind={s} /></p>
}`, `log(find("p").textContent, find("input").value);`)
	expectLines(t, got, `a a`)
}

// The form example: a signup form whose messages show after blur, and a
// summary on submit.
func TestFormExample(t *testing.T) {
	files, _ := filepath.Glob("../examples/form/*.den")
	var srcs []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		srcs = append(srcs, string(b))
	}
	got := runApp(t, strings.Join(srcs, "\n"), `
const byName = (n) => findAll("input").find((i) => i.getAttribute("name") === n);
const msgs = () => log(findAll("p").filter((p) => p.getAttribute("class") === "error").map((p) => p.textContent).join(" | ") || "-");
msgs();
leave(byName("name")); await tick(); msgs();
input(byName("name"), "Ada"); input(byName("email"), "ada"); leave(byName("email")); await tick(); msgs();
input(byName("age"), "abc"); leave(byName("age")); await tick(); msgs();
find("form").dispatch("submit"); await tick(); msgs();
log(findAll("section").length, findAll("input").map((i) => i.getAttribute("aria-invalid")).join());
input(byName("email"), "ada@example.com"); input(byName("age"), "36"); check(byName("terms"), true);
choose(find("select"), "pro"); check(findAll("input").find((i) => i.value === "never"), true);
find("form").dispatch("submit"); await tick(); msgs();
log(find("h2").textContent, "/", find("section").childNodes[1].textContent);
click(find("button")); await tick(); msgs();
log(JSON.stringify(findAll("input").map((i) => (i.getAttribute("type") === "radio" || i.getAttribute("type") === "checkbox" ? i.checked : i.value))), find("select").value);`)
	expectLines(t, got,
		`-`,
		`Enter your name`,
		`Enter an email address, like ada@example.com`,
		`Enter an email address, like ada@example.com | Enter your age as a whole number`,
		`Enter an email address, like ada@example.com | Enter your age as a whole number | Accept the terms to sign up`,
		`0 false,true,true,,,,true`,
		`-`,
		`Welcome, Ada! / ada@example.com, 36, plan pro, updates never.`,
		`-`,
		`["","","18",false,true,false,false] free`)
}
