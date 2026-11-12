package compiler

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// jsonTypes are the types of the decoding conformance test, declared the
// same way for Go and for den. Struct tags are interpreted strings, as Go
// allows, so they fit in this raw string.
const jsonTypes = `
type Basic struct {
	S   string
	B   bool
	I   int
	I8  int8
	I64 int64
	U   uint
	U8  uint8
	U16 uint16
	U64 uint64
	F   float64
	F32 float32
}

type Tagged struct {
	ID     int    "json:\"id\""
	Name   string "json:\"name,omitempty\""
	Skip   int    "json:\"-\""
	Dash   int    "json:\"-,\""
	Odd    int    "json:\"odd key!\""
	Empty  int    "json:\",omitempty\""
	Zero   int    "json:\",omitzero\""
	hidden int
	Other  string "xml:\"other\""
}

type Folded struct {
	ID     int
	MyName string
	K      int
	S      int
	Größe  int
	Ab     int "json:\"a\""
	AB     int "json:\"A\""
}

type Base struct {
	ID   int
	Name string "json:\"name\""
}

type inner struct{ X int }

type Label string

type Emb struct {
	Base
	inner
	Label
	Name string
}

type C1 struct{ A, Shared int }

type C2 struct{ B, Shared int }

type Conflict struct {
	C1
	C2
}

type D1 struct{ P int "json:\"P\"" }

type D2 struct{ P int }

type TagWins struct {
	D1
	D2
}

type Shallow struct {
	P int
	D1
}

type Named struct {
	Base "json:\"base\""
	ID   int
}

type Coll struct {
	L  []int
	A  [3]int
	M  map[string]int
	MI map[int]string
	MU map[uint8]bool
	ML map[Label]int
	B  []byte
	BA [2]byte
	NS [][]string
}

type Rep struct {
	S Base
	L []Base
	A [2]Base
	M map[string]int
}

type Node struct {
	Name string
	Kids []Node
}

type Page[T any] struct {
	Items []T
	Next  bool
}
`

// jsonCases are the inputs of the decoding conformance test, by type.
var jsonCases = []struct {
	typ    string
	inputs []string
}{
	{"Basic", []string{
		`{"S":"x","B":true,"I":-5,"I8":127,"I64":9007199254740993,"U":7,"U8":255,"U16":65535,"U64":18446744073709551615,"F":1.5,"F32":0.1}`,
		`{"I":1.0}`, `{"I":1e2}`, `{"I":1.5}`, `{"I":-0}`, `{"U":-0}`, `{"U":-1}`, `{"I8":128}`, `{"I8":-128}`, `{"I8":-129}`,
		`{"U8":256}`, `{"I64":9223372036854775807}`, `{"I64":9223372036854775808}`, `{"I64":-9223372036854775808}`,
		`{"U64":18446744073709551616}`, `{"F":1e400}`, `{"F":-0}`, `{"F":1E-400}`, `{"F":-1.5e3}`, `{"F32":3.5e38}`,
		`{"F32":3.4028235e38}`, `{"F32":16777217}`, `{"F32":1e-50}`, `{"S":1}`, `{"B":"true"}`, `{"I":"1"}`, `{"I":true}`,
		`{"S":null,"I":null,"B":null}`, `{"S":"x","S":null}`, `null`, `{}`, `[]`, `"x"`, `1`,
		`{`, `{"S":"x",}`, ``, ` `, `nul`, `{"S":"x"} x`, `{'S':1}`, `{"S":"a` + "\x01" + `"}`,
		`{"S":"a\u0000b\ud800\u00e9\ud83d\ude00\"\\\/\b\f\n\r\t"}`, " \t\n{ \"S\" : \"x\" } \r\n",
		`{"Z":1,"S":"y","nested":{"a":[1,2,{"b":null}],"I":"not this one"}}`,
		`{"s":"lower","i":3,"f32":2.5}`,
		// float32s that round once from the text, not through a float64
		// that lands halfway: the largest, and just past it; one above
		// and below 1's upper midpoint; and around the smallest one.
		`{"F32":3.4028235677973366e38}`, `{"F32":-3.4028235677973366e38}`, `{"F32":3.40282356779733661637539395458142568448e38}`,
		`{"F32":1.00000005960464477539062500001}`, `{"F32":1.00000005960464477539062499999}`, `{"F32":1.000000059604644775390625}`,
		`{"F32":7.0064923216240862e-46}`, `{"F32":7.0064923216240853e-46}`, `{"F32":1.4e-45}`, `{"F32":-7.0064923216240862e-46}`,
		`{"F32":7.00649232162408535461864791644958065640130970938257885878534141944895541342930300743319094181060791015625e-46}`,
		// Go reads JSON nested 10,000 levels deep, here under an unknown
		// key, and no deeper.
		`{"junk":` + strings.Repeat("[", 9999) + strings.Repeat("]", 9999) + `,"I":1}`,
		`{"junk":` + strings.Repeat("[", 10000) + strings.Repeat("]", 10000) + `,"I":1}`,
	}},
	{"Tagged", []string{
		`{"id":1,"name":"n","Skip":5,"-":6,"odd key!":7,"Empty":10,"Zero":11,"hidden":12,"Other":"o","other":"p"}`,
		`{"ID":2,"NAME":"N","DASH":3,"Hidden":4}`, `{"id":"x"}`,
	}},
	{"Folded", []string{
		`{"id":1,"myname":"x","\u212a":2,"\u017f":3,"GRÖßE":4,"a":5,"A":6}`,
		`{"my_name":"x","GROSSE":1,"größe":2}`, `{"A":1,"a":2}`, `{"aB":1}`, `{"k":1,"s":2}`, `{"ıd":1}`,
	}},
	{"Emb", []string{
		`{"ID":1,"name":"x","Name":"y","X":2,"Label":"l"}`, `{"NAME":"z"}`, `{"Base":{"ID":1},"inner":{"X":1}}`,
		`{"ID":"x"}`, `{"X":"x"}`,
	}},
	{"Conflict", []string{`{"A":1,"B":2,"Shared":3}`}},
	{"TagWins", []string{`{"P":1}`, `{"p":2}`}},
	{"Shallow", []string{`{"P":1}`}},
	{"Named", []string{`{"base":{"ID":1,"name":"x"},"ID":2,"name":"y"}`, `{"Base":{"ID":1}}`}},
	{"Coll", []string{
		`{"L":[1,2],"A":[1,2,3,4],"M":{"a":1},"MI":{"1":"x","-2":"y","+3":"z","007":"w"},"MU":{"255":true},"ML":{"k":1},"B":"aGk=","BA":[1,2],"NS":[["a"],[],null]}`,
		`{"A":[1]}`, `{"A":[1,2,3,"x"]}`, `{"A":["x"]}`, `{"L":null,"M":null,"A":null,"MI":null}`, `{"L":[]}`,
		`{"MI":{"x":"y"}}`, `{"MI":{"1.0":"y"}}`, `{"MI":{"01":"a","1":"b"}}`, `{"MU":{"256":true}}`, `{"MU":{"-1":true}}`,
		`{"MU":{"+1":true}}`, `{"B":"aGk"}`, `{"B":"aG\nk="}`, `{"B":"a Gk="}`, `{"B":[104,105]}`, `{"B":""}`, `{"B":"aGk=aGk="}`,
		`{"BA":"aGk="}`, `{"L":{}}`, `{"M":[]}`, `{"M":{"a":"x"}}`, `{"NS":[["a",1]]}`,
	}},
	{"Rep", []string{
		`{"S":{"ID":1},"S":{"name":"x"},"L":[{"ID":1},{"ID":2}],"L":[{"name":"a"}],"L":[{"name":"b"},{"name":"c"}],"A":[{"ID":1},{"ID":2}],"A":[{"name":"q"}],"M":{"a":1},"M":{"b":2}}`,
		`{"L":[{"ID":1}],"L":[],"L":[{"name":"a"}]}`, `{"S":{"ID":1},"S":null}`, `{"L":[{"ID":1}],"L":null,"L":[{"name":"a"}]}`,
	}},
	{"Node", []string{`{"Name":"r","Kids":[{"Name":"a","Kids":[]},{"Name":"b","Kids":[{"Name":"c"}]}]}`, `{"Kids":[1]}`}},
	{"Page[Base]", []string{`{"Items":[{"ID":1,"name":"a"}],"Next":true}`, `{"Items":{}}`}},
	{"[]int", []string{`[1,2,3]`, `[1,"x"]`, `[]`, `null`, `{}`}},
	{"map[string][]int", []string{`{"a":[1],"b":null,"c":[]}`, `{"a":1}`}},
	{"map[string]Base", []string{`{"a":{"ID":1},"a":{"name":"x"}}`, `{"a":null}`}},
	{"[2]string", []string{`["a","b","c"]`, `["a"]`, `[1]`}},
	{"int", []string{`42`, `"42"`, `4.2`, `null`}},
	{"string", []string{`"\u00e9"`, `null`, `1`}},
	{"float32", []string{`0.1`, `1e39`}},
	{"bool", []string{`true`, `false`, `0`}},
}

// TestFetchJSONConformance decodes each input of jsonCases into its type
// with Go's encoding/json and with den's decoder, run under node, and
// requires the same outcome: the same value, or an error from both.
func TestFetchJSONConformance(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	var calls, app strings.Builder
	n := 0
	for i, c := range jsonCases {
		fmt.Fprintf(&app, "\t_ = resource(fetchJSON[%s](%q))\n", c.typ, "/"+strconv.Itoa(i))
		for _, in := range c.inputs {
			fmt.Fprintf(&calls, "\ttry[%s](%s)\n", c.typ, strconv.Quote(in))
			n++
		}
	}
	// Go: decode each input, print the value or "error".
	dir := t.TempDir()
	prog := "package main\n\nimport (\n\t\"encoding/json\"\n\t\"fmt\"\n\t\"math\"\n\t\"reflect\"\n\t\"sort\"\n\t\"strconv\"\n\t\"strings\"\n)\n" +
		jsonTypes + goDump + "\nfunc main() {\n" + calls.String() + "}\n"
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(prog), 0o644)
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module conform\n\ngo 1.24\n"), 0o644)
	run := func(experiment string) (string, error) {
		cmd := exec.Command(goTool, "run", ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOEXPERIMENT="+experiment)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	// Den follows encoding/json as built on json/v2, the default since Go
	// 1.27 and GOEXPERIMENT=jsonv2 in 1.25 and 1.26. Go 1.24 has only the
	// older implementation, which differs on the inputs in jsonV2Only.
	v2 := true
	want, err := run("jsonv2")
	if err != nil {
		v2 = false
		if want, err = run(""); err != nil {
			t.Fatalf("go run: %v\n%s", err, want)
		}
	}
	// den: the same types decoded by the generated descriptors.
	res, err := compileOne(jsonTypes + "\ncomponent App() {\n" + app.String() + "\t<p>x</p>\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	var idx []string
	for _, m := range regexp.MustCompile(`\$fetchJSON\(\$jt, (\d+)\)`).FindAllStringSubmatch(res.JS, -1) {
		idx = append(idx, m[1])
	}
	if len(idx) != len(jsonCases) {
		t.Fatalf("found %d fetchJSON calls in the JS, want %d", len(idx), len(jsonCases))
	}
	var script strings.Builder
	script.WriteString("const cases = [\n")
	for i, c := range jsonCases {
		for _, in := range c.inputs {
			fmt.Fprintf(&script, "  [%s, %s],\n", idx[i], jsString(in))
		}
	}
	script.WriteString("];\n" + jsDump + `
for (const [i, s] of cases) {
  let out;
  try { out = dump(den.dec(den.J, i, den.parse(s), den.zero(den.J, i))); } catch (e) { out = "error"; }
  log(out);
}`)
	js := strings.Replace(res.JS, "$mount(App,", "globalThis.den = { J: $jt, dec: $jdec, parse: $jparse, zero: $jzero, Slice: $Slice };\n$mount(App,", 1)
	got := runJS(t, node, js, script.String())
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	if len(wl) != n || len(gl) != n {
		t.Fatalf("got %d lines from Go and %d from den, want %d\ngo:\n%s\nden:\n%s", len(wl), len(gl), n, want, got)
	}
	k := 0
	for _, c := range jsonCases {
		for _, in := range c.inputs {
			if wl[k] != gl[k] && (v2 || !jsonV2Only[c.typ+" "+in]) {
				t.Errorf("%s from %q:\n  go: %s\n den: %s", c.typ, in, wl[k], gl[k])
			}
			k++
		}
	}
	t.Logf("%d inputs, against encoding/json built on json/v2: %t", n, v2)
}

// jsonV2Only are the inputs that the older encoding/json, Go's default
// before 1.27, decodes otherwise. A key that matches no field exactly but
// matches two ignoring case goes to the first in breadth-first order:
// here the outer Name rather than the promoted Base.Name. The older one
// took the first in field order.
var jsonV2Only = map[string]bool{
	`Emb {"NAME":"z"}`: true,
}

// goDump prints a Go value in a form that jsDump prints den's values in:
// numbers as the bits of their float64 (den holds every number as one),
// strings with every rune outside printable ASCII escaped, nil slices as
// nil, and maps with their entries sorted.
const goDump = `
func try[T any](in string) {
	var v T
	if err := json.Unmarshal([]byte(in), &v); err != nil {
		fmt.Println("error")
		return
	}
	fmt.Println(dump(reflect.ValueOf(v)))
}

func num(f float64) string { return strconv.FormatUint(math.Float64bits(f), 16) }

func dump(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		var b strings.Builder
		for _, r := range v.String() {
			if r >= 0x20 && r < 0x7f && r != '"' && r != '\\' {
				b.WriteRune(r)
			} else {
				fmt.Fprintf(&b, "\\u{%x}", r)
			}
		}
		return "\"" + b.String() + "\""
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return num(float64(v.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return num(float64(v.Uint()))
	case reflect.Float32, reflect.Float64:
		return num(v.Float())
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return "nil"
		}
		var out []string
		for i := 0; i < v.Len(); i++ {
			out = append(out, dump(v.Index(i)))
		}
		return "[" + strings.Join(out, ",") + "]"
	case reflect.Map:
		var out []string
		for _, k := range v.MapKeys() {
			out = append(out, dump(k)+":"+dump(v.MapIndex(k)))
		}
		sort.Strings(out)
		return "map[" + strings.Join(out, " ") + "]"
	case reflect.Struct:
		var out []string
		for i := 0; i < v.NumField(); i++ {
			out = append(out, v.Type().Field(i).Name+":"+dump(v.Field(i)))
		}
		return "{" + strings.Join(out, " ") + "}"
	}
	return "?" + v.Kind().String()
}
`

// jsDump is goDump's dump for den's values.
const jsDump = `
const num = (f) => { const b = new DataView(new ArrayBuffer(8)); b.setFloat64(0, f); return b.getBigUint64(0).toString(16); };
const str = (s) => '"' + [...s].map((c) => { const r = c.codePointAt(0); return r >= 0x20 && r < 0x7f && c !== '"' && c !== "\\" ? c : "\\u{" + r.toString(16) + "}"; }).join("") + '"';
const dump = (v) =>
  typeof v === "string" ? str(v) :
  typeof v === "boolean" ? String(v) :
  typeof v === "number" ? num(v) :
  v === null ? "nil" :
  v instanceof den.Slice ? "[" + v.a.slice(v.o, v.o + v.n).map(dump).join(",") + "]" :
  Array.isArray(v) ? "[" + v.map(dump).join(",") + "]" :
  v instanceof Map ? "map[" + [...v].map(([k, x]) => dump(k) + ":" + dump(x)).sort().join(" ") + "]" :
  "{" + Object.entries(v).map(([k, x]) => k + ":" + dump(x)).join(" ") + "}";
`

// runJS runs js, a compiled app, against testdata/dom.js, then script,
// and returns what it logged.
func runJS(t *testing.T, node, js, script string) string {
	t.Helper()
	dom, err := os.ReadFile("testdata/dom.js")
	if err != nil {
		t.Fatal(err)
	}
	prog := string(dom) + "\n" + js + "\n(async () => {\n" + script +
		"\n})().catch((e) => { console.error(e); process.exit(1); });\n"
	file := filepath.Join(t.TempDir(), "app.js")
	if err := os.WriteFile(file, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, file).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// usersSrc pages through /users/N.json, showing the state of the resource.
const usersSrc = `
type User struct {
	ID   int    "json:\"id\""
	Name string "json:\"name\""
}

component App() {
	page := signal(1)
	users := resource(fetchJSON[[]User]("/users/" + itoa(page.Get()) + ".json"))
	<div>
		<p>{users.Loading()} {len(users.Data())} {users.Err() != nil}</p>
		{if users.Err() != nil {
			<i>{users.Err().Error()}</i>
		}}
		<ul>
			{for _, u := range users.Data() key u.ID {
				<li>{u.ID}:{u.Name}</li>
			}}
		</ul>
		<button onclick={page.Set(page.Get() + 1)}>next</button>
		<button onclick={users.Reload()}>reload</button>
	</div>
}`

// usersShow logs the page without its buttons, and the requests pending.
const usersShow = `const show = () => log(html(find("div")).replace(/<button>.*$/, ""), "|", inFlight().join(" "));
`

// A resource starts loading when the component is made, keeps its data
// while it loads again, and keeps it when a request fails: with an HTTP
// error, JSON that does not parse or JSON that does not fit the type.
func TestRunResource(t *testing.T) {
	got := runApp(t, usersSrc, usersShow+`
show();
const o = fetches[0].opts;
log(o.mode, o.credentials, o.headers.accept, o.signal === fetches[0].signal && !o.signal.aborted);
reply(fetches[0], 200, '[{"id": 1, "name": "Ann"}, {"id": 2, "name": "Bob", "extra": true}]'); await tick();
show();
click(find("button", "next")); await tick();
show();
reply(fetches[1], 404, "no such page"); await tick();
show();
click(find("button", "reload")); await tick();
show();
reply(fetches[2], 200, "[1,"); await tick();
show();
click(find("button", "reload")); await tick();
reply(fetches[3], 200, '[{"id": "3"}]'); await tick();
show();
click(find("button", "reload")); await tick();
fail(fetches[4], "Failed to fetch"); await tick();
show();
click(find("button", "reload")); await tick();
reply(fetches[5], 200, '[{"ID": 3, "NAME": "Cy"}]'); await tick();
show();
log(fetches.length, fetches.map((f) => f.url.replace("http://localhost", "")).join(" "));`)
	expectLines(t, got,
		`<div><p>true 0 false</p><ul></ul> | http://localhost/users/1.json`,
		`same-origin same-origin application/json true`,
		`<div><p>false 2 false</p><ul><li>1:Ann</li><li>2:Bob</li></ul> | `,
		`<div><p>true 2 false</p><ul><li>1:Ann</li><li>2:Bob</li></ul> | http://localhost/users/2.json`,
		`<div><p>false 2 true</p><i>fetchJSON: GET /users/2.json: 404 Not Found</i><ul><li>1:Ann</li><li>2:Bob</li></ul> | `,
		`<div><p>true 2 true</p><i>fetchJSON: GET /users/2.json: 404 Not Found</i><ul><li>1:Ann</li><li>2:Bob</li></ul> | http://localhost/users/2.json`,
		`<div><p>false 2 true</p><i>fetchJSON: GET /users/2.json: invalid JSON: Unexpected end of JSON input</i><ul><li>1:Ann</li><li>2:Bob</li></ul> | `,
		`<div><p>false 2 true</p><i>json: cannot unmarshal string into Go struct field User.id of type int</i><ul><li>1:Ann</li><li>2:Bob</li></ul> | `,
		`<div><p>false 2 true</p><i>fetchJSON: GET /users/2.json: Failed to fetch</i><ul><li>1:Ann</li><li>2:Bob</li></ul> | `,
		`<div><p>false 1 false</p><ul><li>3:Cy</li></ul> | `,
		`6 /users/1.json /users/2.json /users/2.json /users/2.json /users/2.json /users/2.json`)
}

// A change to what the URL reads aborts the request in flight, and so
// does Reload; an answer to an aborted request never lands, even one that
// arrives after the next request started.
func TestRunResourceAbort(t *testing.T) {
	got := runApp(t, usersSrc, usersShow+`
const next = find("button", "next"), reload = find("button", "reload");
const first = fetches[0];
click(next); await tick();
show();
log(first.signal.aborted, fetches[1].signal.aborted);
reply(first, 200, '[{"id": 1, "name": "late"}]'); await tick();
show();
// The answer to /users/2 arrives just as the page moves on to /users/3.
reply(fetches[1], 200, '[{"id": 2, "name": "late"}]');
click(next); await tick();
show();
click(reload); await tick();
log(fetches[2].signal.aborted, inFlight().join(" "));
reply(fetches[3], 200, '[{"id": 3, "name": "Cy"}]'); await tick();
show();`)
	expectLines(t, got,
		`<div><p>true 0 false</p><ul></ul> | http://localhost/users/2.json`,
		`true false`,
		`<div><p>true 0 false</p><ul></ul> | http://localhost/users/2.json`,
		`<div><p>true 0 false</p><ul></ul> | http://localhost/users/3.json`,
		`true http://localhost/users/3.json`,
		`<div><p>false 1 false</p><ul><li>3:Cy</li></ul> |`)
}

// Disposing of the component that made a resource aborts its request,
// and nothing lands afterwards: here the parent keeps showing the
// resource of a component that is gone.
func TestRunResourceDispose(t *testing.T) {
	got := runApp(t, `
component Users(held *Signal[*Resource[[]int]]) {
	users := resource(fetchJSON[[]int]("/ids.json"))
	<button onclick={held.Set(users)}>hold</button>
}

func count(r *Resource[[]int]) string {
	if r == nil {
		return "none"
	}
	return itoa(len(r.Data())) + " " + strconv.FormatBool(r.Loading())
}

import "strconv"

component App() {
	show := signal(true)
	held := signal[*Resource[[]int]](nil)
	<div>
		{if show.Get() {
			<Users held={held} />
		}}
		<p>{count(held.Get())}</p>
		<button onclick={show.Set(!show.Get())}>toggle</button>
	</div>
}`, `
const p = () => find("p").textContent, toggle = find("button", "toggle");
click(find("button", "hold")); await tick();
log(p(), inFlight().join(" "));
click(toggle); await tick();
log(p(), fetches[0].signal.aborted, inFlight().length);
reply(fetches[0], 200, "[1, 2]"); await tick();
log(p());
click(toggle); await tick();
click(find("button", "hold")); await tick();
log(p(), inFlight().join(" "));
// The answer arrives just as the component goes.
reply(fetches[1], 200, "[1, 2, 3]");
click(toggle); await tick(); await tick();
log(p(), fetches[1].signal.aborted);`)
	expectLines(t, got,
		`0 true http://localhost/ids.json`,
		`0 true true 0`,
		`0 true`,
		`0 true http://localhost/ids.json`,
		`0 true true`)
}

// A URL on another origin is refused without a request, and so is one
// that is no URL; one on the page's origin, written in full, is fetched.
func TestRunResourceOrigin(t *testing.T) {
	got := runApp(t, `
func show(r *Resource[int]) string {
	if r.Err() == nil {
		return strconv.FormatBool(r.Loading())
	}
	return r.Err().Error()
}

import "strconv"

component App() {
	url := signal("https://evil.example/n.json")
	r := resource(fetchJSON[int](url.Get()))
	<div>
		<p>{show(r)}</p>
		<button onclick={url.Set("//evil.example/n.json")}>a</button>
		<button onclick={url.Set("javascript:alert(1)")}>b</button>
		<button onclick={url.Set("http://[")}>c</button>
		<button onclick={url.Set("http://localhost/n.json")}>d</button>
	</div>
}`, `
const p = () => find("p").textContent;
await tick();
log(p(), fetches.length);
for (const b of ["a", "b", "c"]) {
  click(find("button", b)); await tick(); await tick();
  log(p(), fetches.length);
}
click(find("button", "d")); await tick();
log(p(), inFlight().join(" "));
reply(fetches[0], 200, "42"); await tick();
log(p());`)
	expectLines(t, got,
		`fetchJSON: https://evil.example/n.json is not on this origin 0`,
		`fetchJSON: //evil.example/n.json is not on this origin 0`,
		`fetchJSON: javascript:alert(1) is not on this origin 0`,
		`fetchJSON: http://[ is not on this origin 0`,
		`fetchJSON: http://[ is not on this origin http://localhost/n.json`, // Err stays while it loads
		`false`)
}

// A panic while the arguments are evaluated is reported, and the resource
// keeps its state, the request in flight included, Reload or not.
func TestRunResourceArgsPanic(t *testing.T) {
	got := runApp(t, `
component App() {
	i := signal(0)
	urls := []string{"/a.json"}
	r := resource(fetchJSON[int](urls[i.Get()]))
	<div>
		<p>{r.Loading()} {r.Data()}</p>
		<button onclick={i.Set(5)}>bad</button>
		<button onclick={i.Set(0)}>good</button>
		<button onclick={r.Reload()}>reload</button>
	</div>
}`, `
const p = () => find("p").textContent;
log(p(), inFlight().join(" "));
click(find("button", "bad")); await tick();
log(p(), inFlight().join(" "));
click(find("button", "reload")); await tick();
log(p(), inFlight().join(" "), fetches[0].signal.aborted);
reply(fetches[0], 200, "7"); await tick();
log(p());
click(find("button", "good")); await tick();
reply(fetches[1], 200, "8"); await tick();
log(p(), fetches.length);`)
	expectLines(t, got,
		`true 0 http://localhost/a.json`,
		`reportError: den: index out of range`,
		`true 0 http://localhost/a.json`,
		`reportError: den: index out of range`,
		`true 0 http://localhost/a.json false`,
		`false 7`,
		`false 8 2`)
}

// fetchJSON decodes JSON nested as deep as Go's encoding/json reads it,
// 10,000 levels, into a recursive type without running out of stack, and
// deeper JSON is Go's error. (Go's results for these: no error at 9,999
// levels, "exceeded max depth" at 10,001.)
func TestRunResourceDeep(t *testing.T) {
	got := runApp(t, `
type Node struct {
	Name string
	Kids []Node
}

func depth(n Node) int {
	d := 1
	for len(n.Kids) > 0 {
		n = n.Kids[0]
		d++
	}
	return d
}

component App() {
	r := resource(fetchJSON[Node]("/tree.json"))
	<p>{depth(r.Data())} {r.Err() != nil}</p>
}`, `
const nest = (n) => '{"Name":"x","Kids":['.repeat(n) + '{"Name":"leaf"}' + "]}".repeat(n);
reply(fetches[0], 200, nest(4999)); await tick();
log(find("p").textContent);
reply(fetches[0], 200, ""); // a no-op: it is answered
await tick();`)
	expectLines(t, got, `5000 false`)
	got = runApp(t, `
type Node struct {
	Kids []Node
}

func show(r *Resource[Node]) string {
	if r.Err() == nil {
		return "ok"
	}
	return r.Err().Error()
}

component App() {
	r := resource(fetchJSON[Node]("/tree.json"))
	<p>{show(r)}</p>
}`, `
reply(fetches[0], 200, '{"Kids":['.repeat(5000) + "{}" + "]}".repeat(5000)); await tick();
log(find("p").textContent);`)
	expectLines(t, got, `fetchJSON: GET /tree.json: invalid JSON: exceeded max depth`)
}

// A JSON string of many megabytes used to throw inside the tokenizer's
// regex (RangeError, on this machine's node once the contents pass about
// 8.4 million characters) before the value was decoded. The scanner walks
// indexOf instead, including across an escaped quote.
func TestRunJSONLongString(t *testing.T) {
	got := runApp(t, `
import "strings"

type Box struct {
	S string
}

func show(b Box) string {
	p := strings.Split(b.S, "\"")
	if len(p) != 3 {
		return "parts " + itoa(len(p))
	}
	if len(p[0]) == 10000000 && strings.HasPrefix(p[0], "aaaa") && p[1] == "q" && len(p[2]) == 20 && strings.HasSuffix(p[2], "bbbb") {
		return "yes"
	}
	return "no " + itoa(len(p[0])) + " [" + p[1] + "] " + itoa(len(p[2]))
}

component App() {
	r := resource(fetchJSON[Box]("/s.json"))
	<p>{show(r.Data())} {r.Err() != nil}</p>
}`, `
const body = '{"S":"' + "a".repeat(10000000) + '\\"q\\"' + "b".repeat(20) + '"}';
reply(fetches[0], 200, body); await tick();
log(find("p").textContent);`)
	expectLines(t, got, `yes false`)
}

// An integer token longer than 100 characters is refused before BigInt,
// and the error names only a short prefix of it. A 120-digit integer
// already misses int64, so the assertion is the shortened text: the
// message contains "..." and not a long run of the digits.
func TestRunJSONLongNumber(t *testing.T) {
	got := runApp(t, `
type Box struct {
	N int
}

func show(r *Resource[Box]) string {
	if r.Err() == nil {
		return "ok"
	}
	return r.Err().Error()
}

component App() {
	r := resource(fetchJSON[Box]("/n.json"))
	<p>{show(r)}</p>
}`, `
reply(fetches[0], 200, '{"N":' + "9".repeat(120) + '}'); await tick();
log(find("p").textContent);`)
	digits := strings.Repeat("9", 120)
	if !strings.Contains(got, "...") || strings.Contains(got, digits) || strings.Contains(got, strings.Repeat("9", 40)) {
		t.Fatalf("long digit token was not shortened:\n%s", got)
	}
}

// A number used as a map key is the same token, and the error names only
// a short prefix of it.
func TestRunJSONLongMapKey(t *testing.T) {
	got := runApp(t, `
type Box struct {
	M map[int]int
}

func show(r *Resource[Box]) string {
	if r.Err() == nil {
		return "ok"
	}
	return r.Err().Error()
}

component App() {
	r := resource(fetchJSON[Box]("/n.json"))
	<p>{show(r)}</p>
}`, `
reply(fetches[0], 200, '{"M":{"' + "9".repeat(120) + '":1}}'); await tick();
log(find("p").textContent);`)
	digits := strings.Repeat("9", 120)
	if !strings.Contains(got, "...") || strings.Contains(got, digits) || strings.Contains(got, strings.Repeat("9", 40)) {
		t.Fatalf("long map key was not shortened:\n%s", got)
	}
}

// The names of the browser's that resource and fetchJSON use are renamed
// in a program that declares its own, so they keep working.
func TestRunResourceGlobals(t *testing.T) {
	got := runApp(t, `
type Blob struct {
	Data []byte
	F    float32
}

func fetch() string      { return "mine" }
func atob() string       { return "mine too" }
func isFinite(n int) int { return n }

component App() {
	URL := "/b.json"
	location := 1
	r := resource(fetchJSON[Blob](URL))
	<p>{fetch()} {atob()} {isFinite(location)} {len(r.Data().Data)} {r.Data().F}</p>
}`, `
reply(fetches[0], 200, '{"Data":"aGk=","F":1.5}'); await tick();
log(find("p").textContent, fetches[0].url);`)
	expectLines(t, got, `mine mine too 1 2 1.5 http://localhost/b.json`)
}

// resource, Resource and fetchJSON are ordinary names: a program that has
// its own keeps it.
func TestRunOwnResourceNames(t *testing.T) {
	got := runApp(t, `
type Resource struct{ Name string }

func resource(name string) Resource { return Resource{Name: name} }

func fetchJSON(url string) string { return "GET " + url }

component App() {
	r := resource("x")
	<p>{r.Name} {fetchJSON("/a")}</p>
}`, `log(find("p").textContent);`)
	expectLines(t, got, `x GET /a`)
}

// An app that does not use resource ships none of it, and one that does
// ships the JSON decoder only with fetchJSON.
func TestResourceTreeShaking(t *testing.T) {
	res, err := compileOne(`
component App() {
	n := signal(1)
	<p>{n.Get()}</p>
}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"$resource", "$fetchJSON", "$jparse", "$jdec", "$jt", "AbortController", "$action", "$sendJSON", "$jenc"} {
		if strings.Contains(res.JS, name) {
			t.Errorf("an app without resource ships %s", name)
		}
	}
	res, err = compileOne(usersSrc)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"$resource", "$fetchJSON", "$jparse", "$jdec", "$jt", "$jzero", "$jnum", "$jfold"} {
		if !strings.Contains(res.JS, name) {
			t.Errorf("an app with resource does not ship %s", name)
		}
	}
	for _, name := range []string{"$action", "$sendJSON", "$jenc", "$jflt", "$jb64"} {
		if strings.Contains(res.JS, name) {
			t.Errorf("an app with resource and no action ships %s", name)
		}
	}
}

// TestFetchExample runs examples/fetch with fetch answering from its
// public directory, as den dev serves it: 404 for a page not there.
func TestFetchExample(t *testing.T) {
	src, err := os.ReadFile("../examples/fetch/app.den")
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob("../examples/fetch/public/pages/*.json")
	pages := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		pages["http://localhost/pages/"+filepath.Base(f)] = string(b)
	}
	if len(pages) != 3 {
		t.Fatalf("found %d pages", len(pages))
	}
	b, _ := json.Marshal(pages)
	got := runAppWith(t, string(src), "const pages = "+string(b)+";\nserve = (u) => (u in pages ? [200, pages[u]] : [404, \"404 page not found\"]);", `
const show = () => log(findAll("p").map((p) => p.textContent).join(" / "), "|", findAll("li").map((li) => li.textContent).join(", "));
show();
await tick(); show();
const next = find("button", "Next"), prev = find("button", "Previous");
log("previous disabled:", prev.getAttribute("disabled") !== null);
click(next); await tick(); await tick(); show();
click(next); await tick(); await tick(); show();
click(next); await tick(); show();
await tick(); show();
click(find("button", "Reload")); await tick(); await tick(); show();
click(prev); await tick(); await tick(); show();
log(fetches.map((f) => f.url.slice("http://localhost".length)).join(" "));
input(find("input"), "io");
click(find("button", "Save")); await tick(); await tick();
const save = fetches.at(-1);
log(save.opts.method, save.opts.body, save.opts.headers["content-type"], save.opts.headers["x-demo"]);
log(findAll("p").map((p) => p.textContent).find((t) => t.includes("sendJSON")));`)
	expectLines(t, got,
		`Each page is JSON, fetched and decoded into Go types. / Loading page 1… | `,
		`Each page is JSON, fetched and decoded into Go types. | Mercuryrocky, 0 moons, Venusrocky, 0 moons, Earthrocky, 1 moon`,
		`previous disabled: true`,
		`Each page is JSON, fetched and decoded into Go types. | Marsrocky, 2 moons, Jupitergas giant, 95 moons, Saturngas giant, 146 moons`,
		`Each page is JSON, fetched and decoded into Go types. / That is every planet. Next asks for a page that is not there. | Uranusice giant, 28 moons, Neptuneice giant, 16 moons`,
		`Each page is JSON, fetched and decoded into Go types. / Loading page 4… / That is every planet. Next asks for a page that is not there. | Uranusice giant, 28 moons, Neptuneice giant, 16 moons`,
		`Each page is JSON, fetched and decoded into Go types. / 404: fetchJSON: GET pages/4.json: 404 Not Found  | Uranusice giant, 28 moons, Neptuneice giant, 16 moons`,
		`Each page is JSON, fetched and decoded into Go types. / 404: fetchJSON: GET pages/4.json: 404 Not Found  | Uranusice giant, 28 moons, Neptuneice giant, 16 moons`,
		`Each page is JSON, fetched and decoded into Go types. / That is every planet. Next asks for a page that is not there. | Uranusice giant, 28 moons, Neptuneice giant, 16 moons`,
		`/pages/1.json /pages/2.json /pages/3.json /pages/4.json /pages/4.json /pages/3.json`,
		`POST {"text":"io"} application/json planets`,
		`sendJSON: POST /notes: 404 Not Found`)
}

// A resource's URL can read a provided context value, and an input bound
// to a signal can drive it: typing aborts the request in flight and
// starts one for the new text.
func TestRunResourceContextBind(t *testing.T) {
	got := runApp(t, `
type API struct{ Base string }

component Search() {
	q := signal("go")
	r := resource(fetchJSON[[]string](use[API]().Base + "/search?q=" + q.Get()))
	<div>
		<input bind={q} />
		<p>{len(r.Data())} {r.Loading()}</p>
	</div>
}

component App() {
	provide(API{Base: "/v2"})
	<main><Search /></main>
}`, `
log(inFlight().join(" "));
reply(fetches[0], 200, '["a"]'); await tick();
log(find("p").textContent);
input(find("input"), "den"); await tick();
input(find("input"), "dens"); await tick();
log(inFlight().join(" "), fetches[1].signal.aborted);
reply(fetches[2], 200, '["a", "b"]'); await tick();
log(find("p").textContent, fetches.length);`)
	expectLines(t, got,
		`http://localhost/v2/search?q=go`,
		`1 false`,
		`http://localhost/v2/search?q=dens true`,
		`2 false 3`)
}

// A resource in a component in a {for} row is aborted when its row goes,
// even when its answer has just arrived, and nothing of it lands.
func TestRunResourceInRow(t *testing.T) {
	got := runApp(t, `
component Row(id int) {
	r := resource(fetchJSON[string]("/rows/" + itoa(id) + ".json"))
	<li>{id}:{r.Data()}</li>
}

component App() {
	ids := signal([]int{1, 2, 3})
	<div>
		<ul>
			{for _, id := range ids.Get() key id {
				<Row id={id} />
			}}
		</ul>
		<button onclick={ids.Set([]int{2, 3})}>one</button>
		<button onclick={ids.Set([]int{3})}>two</button>
	</div>
}`, `
const ul = () => html(find("ul"));
log(inFlight().join(" "));
click(find("button", "one")); await tick();
log(ul(), fetches.map((f) => f.signal.aborted).join(" "));
// Row 2's answer arrives just as its row goes.
reply(fetches[1], 200, '"two"');
click(find("button", "two")); await tick(); await tick();
reply(fetches[2], 200, '"three"'); await tick();
log(ul(), fetches.map((f) => f.signal.aborted).join(" "), fetches.length);`)
	expectLines(t, got,
		`http://localhost/rows/1.json http://localhost/rows/2.json http://localhost/rows/3.json`,
		`<ul><li>2:</li><li>3:</li></ul> true false false`,
		`<ul><li>3:three</li></ul> true true false 3`)
}

// A resource's abort is one of the component's cleanups, run with the
// others newest first: after onCleanup's own when onCleanup comes after
// the resource, and before it when onCleanup comes first.
func TestRunResourceCleanupOrder(t *testing.T) {
	got := runAppWith(t, `
component Before() {
	xs := []int{}
	onCleanup(func() { _ = xs[1] })
	r := resource(fetchJSON[int]("/before.json"))
	<p>{r.Loading()}</p>
}

component After() {
	xs := []int{}
	r := resource(fetchJSON[int]("/after.json"))
	onCleanup(func() { _ = xs[1:5] })
	<p>{r.Loading()}</p>
}

component App() {
	show := signal(true)
	<div>
		{if show.Get() {
			<Before />
			<After />
		}}
		<button onclick={show.Set(false)}>hide</button>
	</div>
}`, `
const realFetch = fetch;
globalThis.fetch = (u, o) => { o.signal.addEventListener("abort", () => log("aborted", u.replace("http://localhost", ""))); return realFetch(u, o); };`, `
log(inFlight().join(" "));
click(find("button", "hide")); await tick();
log(inFlight().length, html(find("div")));`)
	expectLines(t, got,
		`http://localhost/before.json http://localhost/after.json`,
		`reportError: den: slice bounds out of range`,
		`aborted /after.json`,
		`aborted /before.json`,
		`reportError: den: index out of range`,
		`0 <div><button>hide</button></div>`)
}
