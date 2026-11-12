package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunEventsConnect(t *testing.T) {
	got := runApp(t, `
import "strconv"

type Ev struct {
	N int "json:\"n\""
}

func line(s *Stream) string {
	e := "-"
	if s.Err() != nil {
		e = s.Err().Error()
	}
	return strconv.FormatBool(s.Connected()) + " " + e
}

component App() {
	s := events("/feed")
	n := signal(0)
	on[Ev](s, "tick", func(v Ev) { n.Set(v.N) })
	<p>{line(s)} {itoa(n.Get())}</p>
}`, `
const es = eventSources[0];
log("src", es.url, es.withCredentials, es.headers);
log("down", find("p").textContent);
sseOpen(es);
await tick();
log("open", find("p").textContent);
sseEvent(es, "tick", '{"n":2}', "1");
frame();
await tick();
log("event", find("p").textContent);`)
	expectLines(t, got,
		`src http://localhost/feed true undefined`,
		`down false - 0`,
		`open true - 0`,
		`event true - 2`)
}

func TestRunEventsError(t *testing.T) {
	got := runApp(t, `
import "strconv"

func line(s *Stream) string {
	e := "-"
	if s.Err() != nil {
		e = s.Err().Error()
	}
	return strconv.FormatBool(s.Connected()) + " " + e
}

component App() {
	s := events("/feed")
	<p>{line(s)}</p>
}`, `
const es = eventSources[0];
sseOpen(es);
await tick();
log("open", find("p").textContent, es.closed);
sseError(es);
await tick();
log("lost", find("p").textContent, es.closed);
sseOpen(es);
await tick();
log("back", find("p").textContent, es.closed);`)
	expectLines(t, got,
		`open true - false`,
		`lost false events: connection lost false`,
		`back true - false`)
}

func TestRunEventsOrigin(t *testing.T) {
	got := runApp(t, `
import "strconv"

func line(s *Stream) string {
	e := "-"
	if s.Err() != nil {
		e = s.Err().Error()
	}
	return strconv.FormatBool(s.Connected()) + " " + e
}

component App() {
	a := events("javascript:")
	b := events("https://evil.example/x")
	c := events("http://user:pass@localhost/x")
	d := events("/ok")
	<ul>
		<li>{line(a)}</li>
		<li>{line(b)}</li>
		<li>{line(c)}</li>
		<li>{line(d)}</li>
	</ul>
}`, `
log("n", eventSources.length);
log("url", eventSources.map((e) => e.url).join(","));
for (const li of findAll("li")) log(li.textContent);`)
	expectLines(t, got,
		`n 1`,
		`url http://localhost/ok`,
		`false events: javascript: is not on this origin`,
		`false events: https://evil.example/x is not on this origin`,
		`false events: http://user:pass@localhost/x is not on this origin`,
		`false -`)
}

func TestRunEventsURL(t *testing.T) {
	got := runApp(t, `
import "strconv"

type Ev struct {
	N int "json:\"n\""
}

func line(s *Stream) string {
	e := "-"
	if s.Err() != nil {
		e = s.Err().Error()
	}
	return strconv.FormatBool(s.Connected()) + " " + e
}

component App() {
	u := signal("/a")
	s := events(u.Get())
	n := signal(0)
	on[Ev](s, "tick", func(v Ev) { n.Set(n.Get() + v.N) })
	<div>
		<p>{line(s)} {itoa(n.Get())}</p>
		<button onclick={u.Set("/b")}>go</button>
	</div>
}`, `
const es = eventSources[0];
sseOpen(es);
await tick();
sseEvent(es, "tick", '{"n":1}', "1");
click(find("button"));
await tick();
log("gap", find("p").textContent, es.closed, eventSources.length, eventSources[1].url);
frame();
await tick();
log("dropped", find("p").textContent);
sseOpen(eventSources[1]);
await tick();
log("open", find("p").textContent);
sseEvent(eventSources[1], "tick", '{"n":5}', "2");
frame();
await tick();
log("got", find("p").textContent);`)
	expectLines(t, got,
		`gap false - 0 true 2 http://localhost/b`,
		`dropped false - 0`,
		`open true - 0`,
		`got true - 5`)
}

func TestRunEventsDispose(t *testing.T) {
	got := runApp(t, `
component App() {
	show := signal(true)
	<div>
		<button onclick={show.Set(false)}>hide</button>
		{if show.Get() { <Pane/> }}
	</div>
}

component Pane() {
	events("/live")
	<p>pane</p>
}`, `
log("up", eventSources.length, eventSources[0].closed, findAll("p").length);
click(find("button"));
await tick();
log("down", eventSources[0].closed, findAll("p").length);`)
	expectLines(t, got,
		`up 1 false 1`,
		`down true 0`)
}

func TestRunEventsBadJSON(t *testing.T) {
	got := runApp(t, `
import "strconv"

type Ev struct {
	N int "json:\"n\""
}

func line(s *Stream) string {
	e := "-"
	if s.Err() != nil {
		e = s.Err().Error()
	}
	return strconv.FormatBool(s.Connected()) + " " + e
}

component App() {
	s := events("/feed")
	n := signal(0)
	on[Ev](s, "tick", func(v Ev) { n.Set(v.N) })
	<p>{line(s)} {itoa(n.Get())}</p>
}`, `
const es = eventSources[0];
sseOpen(es);
await tick();
sseEvent(es, "tick", "nope", "1");
frame();
await tick();
const bad = find("p").textContent;
log("bad", bad.startsWith("true events: invalid JSON:"), bad.endsWith(" 0"), es.closed);
sseEvent(es, "tick", '{"n":4}', "2");
frame();
await tick();
const good = find("p").textContent;
log("good", good.startsWith("true events: invalid JSON:"), good.endsWith(" 4"), es.closed);`)
	expectLines(t, got,
		`bad true true false`,
		`good true true false`)
}

func TestRunEventsBatch(t *testing.T) {
	got := runApp(t, `
type Ev struct {
	N int "json:\"n\""
}

component App() {
	s := events("/feed")
	seen := signal("")
	on[Ev](s, "tick", func(v Ev) { seen.Set(seen.Get() + itoa(v.N)) })
	<p>{seen.Get()}</p>
}`, `
const es = eventSources[0];
sseEvent(es, "tick", '{"n":1}', "a");
sseEvent(es, "tick", '{"n":2}', "b");
log("queued", JSON.stringify(find("p").textContent));
frame();
await tick();
log("once", JSON.stringify(find("p").textContent));
frame();
await tick();
log("again", JSON.stringify(find("p").textContent));`)
	expectLines(t, got,
		`queued ""`,
		`once "12"`,
		`again "12"`)
}

func TestRunEventsTimer(t *testing.T) {
	got := runApp(t, `
type Ev struct {
	N int "json:\"n\""
}

component App() {
	s := events("/feed")
	seen := signal("")
	on[Ev](s, "tick", func(v Ev) { seen.Set(seen.Get() + itoa(v.N)) })
	<p>{seen.Get()}</p>
}`, `
fakeTimers();
const es = eventSources[0];
sseEvent(es, "tick", '{"n":1}', "a");
sseEvent(es, "tick", '{"n":2}', "b");
log("queued", JSON.stringify(find("p").textContent));
await advance(49);
log("early", JSON.stringify(find("p").textContent));
await advance(1);
log("due", JSON.stringify(find("p").textContent));
frame();
await advance(0);
log("frame", JSON.stringify(find("p").textContent));`)
	expectLines(t, got,
		`queued ""`,
		`early ""`,
		`due "12"`,
		`frame "12"`)
}

func TestRunEventsLogCap(t *testing.T) {
	got := runApp(t, `
type Row struct {
	ID int "json:\"id\""
}

component App() {
	s := events("/log")
	rows := signal([]Row{})
	on[Row](s, "log", func(r Row) {
		xs := append(rows.Get(), r)
		if len(xs) > 50 {
			xs = xs[len(xs)-50:]
		}
		rows.Set(xs)
	})
	<ul>{for _, r := range rows.Get() key r.ID { <li>{itoa(r.ID)}</li> }}</ul>
}`, `
const es = eventSources[0];
for (let i = 1; i <= 2000; i++) sseEvent(es, "log", JSON.stringify({ id: i }), String(i));
frame();
await tick();
const lis = findAll("li");
log(lis.length);
log(lis[0].textContent, lis[lis.length - 1].textContent);
log("ops", ops());`)
	lines := strings.Split(got, "\n")
	if len(lines) < 2 || lines[0] != "50" || lines[1] != "1951 2000" {
		t.Fatalf("got:\n%s", got)
	}
	t.Log(got)
}

func TestRunEventsTwoHandlers(t *testing.T) {
	got := runApp(t, `
type Ev struct {
	N int "json:\"n\""
}

component App() {
	s := events("/feed")
	a := signal(0)
	b := signal(0)
	on[Ev](s, "tick", func(v Ev) { a.Set(a.Get() + v.N) })
	on[Ev](s, "tick", func(v Ev) { b.Set(b.Get() + v.N) })
	<p>{itoa(a.Get())} {itoa(b.Get())}</p>
}`, `
const es = eventSources[0];
sseEvent(es, "tick", '{"n":2}', "1");
frame();
await tick();
log(find("p").textContent);`)
	expectLines(t, got, `2 2`)
}

// A counter-sized program, and every example, ships no event stream. A
// program that follows one ships $events and $onEv.
func TestEventsTreeShaking(t *testing.T) {
	res, err := compileOne(`
component App() {
	n := signal(1)
	<p>{n.Get()}</p>
}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"$events", "$onEv"} {
		if strings.Contains(res.JS, name) {
			t.Errorf("a counter-sized program ships %s", name)
		}
	}
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
			srcs = append(srcs, Source{Name: filepath.Base(f), Code: string(b)})
		}
		res, err := Compile(srcs)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		for _, name := range []string{"$events", "$onEv"} {
			if strings.Contains(res.JS, name) {
				t.Errorf("%s ships %s", dir, name)
			}
		}
	}
	res, err = compileOne(`
type Ev struct {
	N int "json:\"n\""
}

component App() {
	s := events("/feed")
	on[Ev](s, "tick", func(v Ev) {})
	<p>{s.Connected()}</p>
}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"$events", "$onEv"} {
		if !strings.Contains(res.JS, name) {
			t.Errorf("a program that uses events does not ship %s", name)
		}
	}
}
