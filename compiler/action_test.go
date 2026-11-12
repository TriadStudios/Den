package compiler

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"testing"
)

// Do reads the method and URL when it runs, sends JSON with the page's
// headers, and Then runs only after a success.
func TestRunActionSend(t *testing.T) {
	got := runApp(t, `
import "den/http"
import "strconv"

type Note struct {
	Text string "json:\"text\""
}
type Saved struct {
	OK bool "json:\"ok\""
}

component App() {
	u := signal("/a")
	n := signal(0)
	a := action(sendJSON[Note, Saved]("POST", u.Get()))
	a.Then(func(s Saved) {
		if s.OK {
			n.Set(n.Get() + 1)
		}
	})
	_ = http.SetHeader("X-Demo", "planets")
	<div>
		<p>{strconv.FormatBool(a.Pending())} {itoa(n.Get())} {strconv.FormatBool(a.Data().OK)}</p>
		<button onclick={u.Set("/b")}>url</button>
		<button onclick={a.Do(Note{Text: "io"})}>go</button>
	</div>
}`, `
log("mount", fetches.length);
click(find("button", "url")); await tick();
log("url", fetches.length);
click(find("button", "go"));
log("sent", fetches.length);
await tick();
log("pending", find("p").textContent);
const f = fetches[0];
log(f.opts.method, f.url, f.opts.body, f.opts.mode, f.opts.credentials, f.opts.headers.accept, f.opts.headers["content-type"], f.opts.headers["x-demo"]);
reply(f, 200, '{"ok":true}'); await tick();
log("done", find("p").textContent);`)
	expectLines(t, got,
		`mount 0`,
		`url 0`,
		`sent 1`,
		`pending true 0 false`,
		`POST http://localhost/b {"text":"io"} same-origin same-origin application/json application/json planets`,
		`done false 1 true`)
}

// A second Do while one is pending sets Err and does not send. The request
// in flight still settles, and that outcome replaces the refusal.
func TestRunActionBusy(t *testing.T) {
	got := runApp(t, `
import "strconv"

type Note struct {
	Text string "json:\"text\""
}

func show(a *Action[Note, int]) string {
	e := ""
	if a.Err() != nil {
		e = a.Err().Error()
	}
	return strconv.FormatBool(a.Pending()) + "|" + e + "|" + itoa(a.Data())
}

component App() {
	a := action(sendJSON[Note, int]("POST", "/n"))
	<div>
		<p>{show(a)}</p>
		<button onclick={a.Do(Note{Text: "a"})}>go</button>
	</div>
}`, `
const p = () => find("p").textContent;
click(find("button", "go")); await tick();
log(p(), fetches.length);
click(find("button", "go")); await tick();
log(p(), fetches.length);
reply(fetches[0], 200, "4"); await tick();
log(p());`)
	expectLines(t, got,
		`true||0 1`,
		`true|action: Do called while a request is pending|0 1`,
		`false||4`)
}

// struct{} sends {}, except DELETE, which sends no body. A named empty
// struct is a value. 204 and an empty body are the zero Out, and a body
// that is only whitespace is not empty.
func TestRunActionEmpty(t *testing.T) {
	got := runApp(t, `
type E = struct{}
type Named struct{}
type Saved struct {
	OK bool "json:\"ok\""
}

func show(a *Action[E, Saved]) string {
	if a.Err() != nil {
		return a.Err().Error()
	}
	if a.Data().OK {
		return "ok"
	}
	return "zero"
}

component App() {
	empty := action(sendJSON[E, Saved]("POST", "/e"))
	del := action(sendJSON[struct{}, Saved]("DELETE", "/d"))
	put := action(sendJSON[struct{}, Saved]("PUT", "/u"))
	named := action(sendJSON[Named, int]("PATCH", "/p"))
	body := action(sendJSON[Named, int]("DELETE", "/g"))
	<div>
		<p>{show(empty)}</p>
		<i>{show(del)}</i>
		<b>{show(put)}</b>
		<button onclick={empty.Do()}>empty</button>
		<button onclick={del.Do()}>del</button>
		<button onclick={put.Do()}>put</button>
		<button onclick={named.Do(Named{})}>named</button>
		<button onclick={body.Do(Named{})}>body</button>
	</div>
}`, `
const go = async (name, status, resp) => {
  click(find("button", name)); await tick();
  const f = fetches.at(-1);
  log(name, f.opts.method, String(f.opts.body), String(f.opts.headers["content-type"]));
  reply(f, status, resp); await tick();
};
await go("empty", 200, "");
log("empty-out", find("p").textContent);
await go("del", 204, "not json");
log("del-out", find("i").textContent);
await go("put", 200, '{"ok":true}');
log("put-out", find("b").textContent);
await go("named", 200, "1");
await go("body", 200, "1");
await go("empty", 200, " ");
const space = find("p").textContent;
log("space", space.includes("invalid JSON") ? space.slice(0, space.indexOf("invalid JSON") + "invalid JSON".length) : space);`)
	expectLines(t, got,
		`empty POST {} application/json`,
		`empty-out zero`,
		`del DELETE undefined undefined`,
		`del-out zero`,
		`put PUT {} application/json`,
		`put-out ok`,
		`named PATCH {} application/json`,
		`body DELETE {} application/json`,
		`empty POST {} application/json`,
		`space sendJSON: POST /e: invalid JSON`)
}

// A URL on another origin is refused before a request, and so is one with
// a username. One on this origin is sent.
func TestRunActionOrigin(t *testing.T) {
	got := runApp(t, `
func show(a *Action[int, int]) string {
	if a.Err() != nil {
		return a.Err().Error()
	}
	if a.Pending() {
		return "pending"
	}
	return "idle"
}

component App() {
	bad := action(sendJSON[int, int]("POST", "https://evil.example/n"))
	user := action(sendJSON[int, int]("POST", "https://user:pw@localhost/n"))
	ok := action(sendJSON[int, int]("POST", "http://localhost/n"))
	<div>
		<p>{show(bad)}</p>
		<p>{show(user)}</p>
		<p>{show(ok)}</p>
		<button onclick={bad.Do(1)}>bad</button>
		<button onclick={user.Do(1)}>user</button>
		<button onclick={ok.Do(1)}>ok</button>
	</div>
}`, `
const line = () => findAll("p").map((p) => p.textContent).join(" | ");
click(find("button", "bad")); await tick();
log(line(), fetches.length);
click(find("button", "user")); await tick();
log(line(), fetches.length);
click(find("button", "ok")); await tick();
log(line(), fetches.length, fetches[0] && fetches[0].url, fetches[0] && fetches[0].opts.body);`)
	expectLines(t, got,
		`sendJSON: https://evil.example/n is not on this origin | idle | idle 0`,
		`sendJSON: https://evil.example/n is not on this origin | sendJSON: https://user:pw@localhost/n is not on this origin | idle 0`,
		`sendJSON: https://evil.example/n is not on this origin | sendJSON: https://user:pw@localhost/n is not on this origin | pending 1 http://localhost/n 1`)
}

// Disposing of the component aborts the request, and a late response does
// not land.
func TestRunActionDispose(t *testing.T) {
	got := runApp(t, `
import "strconv"

component Box(held *Signal[*Action[struct{}, int]]) {
	a := action(sendJSON[struct{}, int]("POST", "/n"))
	<span>
		<button onclick={held.Set(a)}>hold</button>
		<button onclick={a.Do()}>go</button>
	</span>
}

func show(a *Action[struct{}, int]) string {
	if a == nil {
		return "none"
	}
	return strconv.FormatBool(a.Pending()) + " " + itoa(a.Data())
}

component App() {
	open := signal(true)
	held := signal[*Action[struct{}, int]](nil)
	<div>
		{if open.Get() {
			<Box held={held} />
		}}
		<p>{show(held.Get())}</p>
		<button onclick={open.Set(!open.Get())}>toggle</button>
	</div>
}`, `
log(fetches.length);
click(find("button", "hold"));
click(find("button", "go")); await tick();
log(find("p").textContent, fetches.length);
click(find("button", "toggle")); await tick();
log(find("p").textContent, fetches[0].signal.aborted, inFlight().length);
reply(fetches[0], 200, "7"); await tick();
log(find("p").textContent);`)
	expectLines(t, got,
		`0`,
		`true 0 1`,
		`true 0 true 0`,
		`true 0`)
}

// A failed status keeps the server's code and message, invalid JSON is an
// error, Then does not run, and a value encoding/json refuses is not sent.
func TestRunActionErrors(t *testing.T) {
	got := runApp(t, `
import "den/http"

func nan() float64 {
	x := 0.0
	return x / x
}

func show(a *Action[float64, int]) string {
	if a.Err() == nil {
		return "ok"
	}
	return itoa(http.Status(a.Err())) + "|" + http.Code(a.Err()) + "|" + http.Message(a.Err())
}

component App() {
	n := signal(0)
	a := action(sendJSON[float64, int]("POST", "/n"))
	a.Then(func(_ int) { n.Set(n.Get() + 1) })
	<div>
		<p>{show(a)} {itoa(n.Get())}</p>
		<button onclick={a.Do(1)}>go</button>
		<button onclick={a.Do(nan())}>nan</button>
	</div>
}`, `
click(find("button", "go")); await tick();
reply(fetches[0], 422, '{"error":{"code":"nope","message":"no"}}'); await tick();
log("status", find("p").textContent, fetches.length);
click(find("button", "go")); await tick();
reply(fetches[1], 200, "not json"); await tick();
const raw = find("p").textContent;
log("json", raw.includes("invalid JSON") ? raw.slice(0, raw.indexOf("invalid JSON") + "invalid JSON".length) : raw, fetches.length);
click(find("button", "nan")); await tick();
log("nan", find("p").textContent, fetches.length);`)
	expectLines(t, got,
		`status 422|nope|no 0 1`,
		`json 0||sendJSON: POST /n: invalid JSON 2`,
		`nan 0||json: unsupported value: NaN 0 2`)
}

// A program's own action and sendJSON stay what it declared.
func TestRunOwnActionNames(t *testing.T) {
	got := runApp(t, `
type Action struct{ Name string }

func action(name string) Action { return Action{Name: name} }

func sendJSON(url string) string { return "POST " + url }

component App() {
	a := action("x")
	<p>{a.Name} {sendJSON("/a")}</p>
}`, `log(find("p").textContent);`)
	expectLines(t, got, `x POST /a`)
}

// An action ships the encoder; the calls above already require it. This
// checks the names a program that only decodes must not gain, which
// TestResourceTreeShaking covers from the other side, and that btoa is
// reserved by being used here.
func TestActionTreeShaking(t *testing.T) {
	res, err := compileOne(`
component App() {
	a := action(sendJSON[int, int]("POST", "/n"))
	<button onclick={a.Do(1)}>go</button>
}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"$action", "$sendJSON", "$jenc", "$jstr", "$jint", "btoa"} {
		if name == "btoa" {
			continue
		}
		if !strings.Contains(res.JS, name) {
			t.Errorf("an action does not ship %s", name)
		}
	}
	if strings.Contains(res.JS, "$fetchJSON") {
		t.Error("an action with no resource ships $fetchJSON")
	}
}

func TestSendJSONConformance(t *testing.T) {
	type Inner struct {
		N int `json:"n,omitempty"`
	}
	type Box struct {
		Inner
		A    int            `json:"a,omitempty"`
		Z    int            `json:"z,omitzero"`
		Name string         `json:"name,omitempty"`
		Tags []string       `json:"tags,omitempty"`
		M    map[string]int `json:"m,omitempty"`
		Raw  []byte         `json:"raw,omitempty"`
		B    int            `json:"b"`
		Skip int            `json:"-"`
	}
	type Opt struct {
		B bool   `json:"b,omitempty"`
		N int    `json:"n,omitempty"`
		S string `json:"s,omitempty"`
		T string `json:"t"`
	}
	type Tags struct {
		A []string `json:"a,omitempty"`
		B []string `json:"b,omitzero"`
		C []string `json:"c"`
	}
	type Raw struct {
		A []byte `json:"a,omitempty"`
		B []byte `json:"b,omitzero"`
		C []byte `json:"c"`
	}
	type Hold struct {
		M map[string]int `json:"m"`
	}
	type Keep struct {
		M map[string]int `json:"m,omitzero"`
	}
	type Arr struct {
		A [2]int `json:"a,omitempty"`
		Z [2]int `json:"z,omitzero"`
	}
	type In2 struct {
		A int `json:"a"`
	}
	type Wrap struct {
		S In2 `json:"s,omitzero"`
		T In2 `json:"t,omitempty"`
	}
	type F struct {
		A float64 `json:"a,omitempty"`
		B float64 `json:"b"`
	}
	type Both struct {
		N int `json:"n,omitempty,omitzero"`
		M int `json:"m,omitempty,omitzero"`
	}
	type Item struct {
		N int `json:"n"`
	}
	type Bytes []byte
	type U8 uint8

	negZero := math.Copysign(0, -1)
	f32neg := math.Float32frombits(0x80000000)
	cases := []struct {
		typ string
		js  string
		val any
	}{
		{"Box", `{Inner:{N:0}, A:0, Z:0, Name:"", Tags:null, M:new Map(), Raw:null, B:1, Skip:0}`, Box{B: 1}},
		{"Box", `{Inner:{N:2}, A:0, Z:5, Name:"x", Tags:den.sl(["a"]), M:new Map([["b",1],["a",2]]), Raw:den.sl([1,2,3]), B:1, Skip:9}`, Box{Inner: Inner{N: 2}, Z: 5, Name: "x", Tags: []string{"a"}, M: map[string]int{"b": 1, "a": 2}, Raw: []byte{1, 2, 3}, B: 1, Skip: 9}},
		{"Opt", `{B:false, N:0, S:"", T:"x"}`, Opt{T: "x"}},
		{"Tags", `{A:null, B:null, C:null}`, Tags{}},
		{"Tags", `{A:den.sl([]), B:den.sl([]), C:den.sl([])}`, Tags{A: []string{}, B: []string{}, C: []string{}}},
		{"Raw", `{A:null, B:null, C:null}`, Raw{}},
		{"Raw", `{A:den.sl([]), B:den.sl([]), C:den.sl([])}`, Raw{A: []byte{}, B: []byte{}, C: []byte{}}},
		{"Raw", `{A:den.sl([1,2,3]), B:null, C:null}`, Raw{A: []byte{1, 2, 3}}},
		{"Hold", `{M:new Map()}`, Hold{M: map[string]int{}}},
		{"Hold", `{M:new Map([["b",1],["a",2]])}`, Hold{M: map[string]int{"b": 1, "a": 2}}},
		{"Keep", `{M:new Map()}`, Keep{M: map[string]int{}}},
		{"map[string]int", `new Map([["b",1],["a",2]])`, map[string]int{"b": 1, "a": 2}},
		{"map[int]string", `new Map([[10,"a"],[2,"b"],[-1,"c"]])`, map[int]string{10: "a", 2: "b", -1: "c"}},
		{"map[string]int", `new Map([["\uE000",1],["\u{1F600}",2]])`, map[string]int{"\uE000": 1, "\U0001F600": 2}},
		{"[]int", `null`, []int(nil)},
		{"[]int", `den.sl([])`, []int{}},
		{"[]int", `den.sl([1,2])`, []int{1, 2}},
		{"[]Item", `den.sl([{N:1},{N:2}])`, []Item{{1}, {2}}},
		{"[3]int", `[1,2,3]`, [3]int{1, 2, 3}},
		{"Arr", `{A:[0,0], Z:[0,0]}`, Arr{}},
		{"Wrap", `{S:{A:0}, T:{A:0}}`, Wrap{}},
		{"Wrap", `{S:{A:1}, T:{A:0}}`, Wrap{S: In2{A: 1}}},
		{"F", `{A:0, B:-0}`, F{B: negZero}},
		{"float64", `0`, float64(0)},
		{"float64", `-0`, negZero},
		{"float64", `0.1`, 0.1},
		{"float64", `1e20`, 1e20},
		{"float64", `1e21`, 1e21},
		{"float64", `1e-7`, 1e-7},
		{"float32", `f32(0x` + strings.ToUpper(hex32(math.Float32bits(0.1))) + `)`, float32(0.1)},
		{"float32", `f32(0x` + strings.ToUpper(hex32(math.Float32bits(float32(1e20)))) + `)`, float32(1e20)},
		{"float32", `f32(0x80000000)`, f32neg},
		{"float32", `f32(0x` + strings.ToUpper(hex32(math.Float32bits(float32(-13603536000)))) + `)`, float32(-13603536000)},
		{"string", `"<&>\u2028\n\u0001"`, "<&>\u2028\n\x01"},
		{"bool", `true`, true},
		{"bool", `false`, false},
		{"Both", `{N:0, M:2}`, Both{M: 2}},
		{"Bytes", `den.sl([1,2])`, Bytes{1, 2}},
		{"[]uint8", `den.sl([1,2,3])`, []uint8{1, 2, 3}},
		{"[]U8", `den.sl([1,2])`, []U8{1, 2}},
		{"[4]byte", `[1,2,3,4]`, [4]byte{1, 2, 3, 4}},
		{"struct{}", `{}`, struct{}{}},
	}
	seen := map[string]bool{}
	var types []string
	for _, c := range cases {
		if !seen[c.typ] {
			seen[c.typ] = true
			types = append(types, c.typ)
		}
	}
	var src strings.Builder
	src.WriteString(`
type Inner struct { N int "json:\"n,omitempty\"" }
type Box struct {
	Inner
	A int "json:\"a,omitempty\""
	Z int "json:\"z,omitzero\""
	Name string "json:\"name,omitempty\""
	Tags []string "json:\"tags,omitempty\""
	M map[string]int "json:\"m,omitempty\""
	Raw []byte "json:\"raw,omitempty\""
	B int "json:\"b\""
	Skip int "json:\"-\""
	priv int
}
type Opt struct {
	B bool "json:\"b,omitempty\""
	N int "json:\"n,omitempty\""
	S string "json:\"s,omitempty\""
	T string "json:\"t\""
}
type Tags struct {
	A []string "json:\"a,omitempty\""
	B []string "json:\"b,omitzero\""
	C []string "json:\"c\""
}
type Raw struct {
	A []byte "json:\"a,omitempty\""
	B []byte "json:\"b,omitzero\""
	C []byte "json:\"c\""
}
type Hold struct { M map[string]int "json:\"m\"" }
type Keep struct { M map[string]int "json:\"m,omitzero\"" }
type Arr struct {
	A [2]int "json:\"a,omitempty\""
	Z [2]int "json:\"z,omitzero\""
}
type In2 struct { A int "json:\"a\"" }
type Wrap struct {
	S In2 "json:\"s,omitzero\""
	T In2 "json:\"t,omitempty\""
}
type F struct {
	A float64 "json:\"a,omitempty\""
	B float64 "json:\"b\""
}
type Both struct {
	N int "json:\"n,omitempty,omitzero\""
	M int "json:\"m,omitempty,omitzero\""
}
type Item struct { N int "json:\"n\"" }
type Bytes []byte
type U8 uint8

component App() {
	_ = resource(fetchJSON[Box]("/b"))
`)
	for _, typ := range types {
		src.WriteString("\t_ = action(sendJSON[" + typ + `, int]("POST", "/t"))` + "\n")
	}
	src.WriteString("\t<p>ok</p>\n}\n")
	res, err := compileOne(src.String())
	if err != nil {
		t.Fatal(err)
	}
	matches := regexp.MustCompile(`\$sendJSON\(\$jt, (\d+),`).FindAllStringSubmatch(res.JS, -1)
	if len(matches) != len(types) {
		t.Fatalf("got %d sendJSON calls, want %d", len(matches), len(types))
	}
	idx := map[string]string{}
	for i, typ := range types {
		idx[typ] = matches[i][1]
	}
	js := strings.Replace(res.JS, `$mount(App, document.getElementById("app"));`, `globalThis.den = {J: $jt, enc: $jenc, sl: $sl};`, 1)
	var script strings.Builder
	script.WriteString("const f32 = (bits) => { const b = new DataView(new ArrayBuffer(4)); b.setUint32(0, bits); return b.getFloat32(0); };\n")
	var want []string
	for _, c := range cases {
		b, err := json.Marshal(c.val)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, string(b))
		script.WriteString("log(den.enc(den.J, " + idx[c.typ] + ", " + c.js + "));\n")
	}
	got := runJS(t, "node", js, script.String())
	expectLines(t, got, want...)
}

func hex32(n uint32) string {
	const h = "0123456789abcdef"
	s := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		s[i] = h[n&0xf]
		n >>= 4
	}
	return string(s)
}
