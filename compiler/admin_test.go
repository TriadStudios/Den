package compiler

import (
	"os"
	"strings"
	"testing"
)

// TestAdminPage runs examples/admin on the test DOM. The page has no
// Playwright script; this is the browser stand-in, together with the Go
// tests in examples/admin.
func TestAdminPage(t *testing.T) {
	srcb, err := os.ReadFile("../examples/admin/app.den")
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcb)
	if strings.Contains(src, "http://") || strings.Contains(src, "https://") {
		t.Fatal("examples/admin/app.den contains an absolute http(s) URL")
	}
	css, err := os.ReadFile("../examples/admin/style.css")
	if err != nil {
		t.Fatal(err)
	}
	style := string(css)
	if strings.Contains(style, "url(") || strings.Contains(strings.ToLower(style), "@import") || strings.Contains(style, "@font-face") {
		t.Fatal("examples/admin/style.css has a url, import, or font face")
	}
	got := runApp(t, src, `
input(find("input"), "den");
click(find("button", "Sign in"));
await tick();
const login = fetches.filter((f) => f.url.endsWith("/api/login"));
if (login.length !== 1) throw new Error("login " + fetches.map((f) => f.url).join(" "));
log("body", String(login[0].opts.body));
reply(login[0], 200, '{"csrf":"tok"}');
await tick();
const svc = fetches.filter((f) => f.url.endsWith("/api/services") && !f.done);
if (svc.length !== 1) throw new Error("svc " + fetches.map((f) => f.done + " " + f.url).join(" | "));
log("csrf", svc[0].opts.headers["x-csrf"]);
const es = eventSources.at(-1);
log("es", es.url, es.withCredentials);
sseOpen(es);
await tick();
log("live", findAll("p").map((p) => p.textContent).join("|"));
sseEvent(es, "state", '{"error":"disk full"}', "1");
frame();
await tick();
log("note", findAll("p").map((p) => p.textContent).join("|"));
input(find("input"), "cleared");
await tick();
log("after", findAll("p").map((p) => p.textContent).join("|"));
reply(svc[0], 200, '[{"id":"web","name":"Web","running":false},{"id":"worker","name":"Worker","running":false}]');
await tick();
const starts = findAll("button").filter((b) => b.textContent === "Start");
if (starts.length !== 2) throw new Error("starts " + findAll("button").map((b) => b.textContent).join(","));
click(starts[1]);
await tick();
const post = fetches.at(-1);
log("start", post.opts.method, post.url.replace("http://localhost", ""), post.opts.headers["x-csrf"], String(post.opts.body));
`)
	expectLines(t, got,
		`body {"password":"den"}`,
		`csrf tok`,
		`es http://localhost/api/events true`,
		`live Connected`,
		`note Connected|disk full`,
		`after Connected`,
		`start POST /api/services/worker/start tok {}`,
	)
}
