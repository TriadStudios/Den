package main

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/TriadStudios/Den/compiler"
)

// The dev server answers a path that has no file, and looks like a page
// rather than an asset, with index.html, so apps that route by path load
// at deep links.
func TestDevFiles(t *testing.T) {
	out := t.TempDir()
	os.WriteFile(filepath.Join(out, "index.html"), []byte("index"), 0o644)
	os.WriteFile(filepath.Join(out, "app.js"), []byte("js"), 0o644)
	h := devFiles(out)
	for _, c := range []struct {
		method, path string
		code         int
		body         string
	}{
		{"GET", "/", 200, "index"},
		{"GET", "/app.js", 200, "js"},
		{"GET", "/users/7", 200, "index"},
		{"GET", "/users/7/", 200, "index"},
		{"HEAD", "/users/7", 200, ""},
		{"GET", "/missing.js", 404, "404 page not found\n"},
		{"GET", "/users/7.json", 404, "404 page not found\n"},
		{"GET", "/__den/nothing", 404, "404 page not found\n"},
		{"POST", "/users/7", 404, "404 page not found\n"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(c.method, c.path, nil))
		if w.Code != c.code || w.Body.String() != c.body {
			t.Errorf("%s %s: %d %q, want %d %q", c.method, c.path, w.Code, w.Body.String(), c.code, c.body)
		}
		if w.Header().Get("Content-Security-Policy") != csp {
			t.Errorf("%s %s: no CSP header", c.method, c.path)
		}
		if c.code == 200 && w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s %s: no Cache-Control: no-store header", c.method, c.path)
		}
	}
}

// The components' scoped CSS goes in den.css, linked before the app's own
// style.css, which is copied as it is: what only works at the start of a
// file (a byte order mark, @charset, @import) still does, and the app's
// rules come later and can override the components' tokens. An app
// without style blocks gets exactly what it got before den.css existed.
func TestWriteDistCSS(t *testing.T) {
	const own = "\xef\xbb\xbf@charset \"utf-8\";\n@import url(reset.css);\nbody { margin: 0 }\n"
	const scoped = ":root{--ink:#000}\np[d-abcde]{color:var(--ink)}\n"
	const denLink, ownLink = `<link rel="stylesheet" href="den.css">`, `<link rel="stylesheet" href="style.css">`
	read := func(name string) (string, bool) {
		b, err := os.ReadFile(name)
		return string(b), err == nil
	}
	for _, c := range []struct {
		name        string
		own, scoped string
	}{
		{"both", own, scoped},
		{"only the app's", own, ""},
		{"only scoped", "", scoped},
		{"neither", "", ""},
	} {
		dir, out := t.TempDir(), t.TempDir()
		if c.own != "" {
			os.WriteFile(filepath.Join(dir, "style.css"), []byte(c.own), 0o644)
		}
		// A den.css from an earlier build, when the app had style blocks.
		os.WriteFile(filepath.Join(out, "den.css"), []byte("old"), 0o644)
		if err := writeDist(dir, out, &compiler.Result{JS: "js", CSS: c.scoped}, false, "den app"); err != nil {
			t.Fatal(err)
		}
		page, _ := read(filepath.Join(out, "index.html"))
		css, hasCSS := read(filepath.Join(out, "style.css"))
		den, hasDen := read(filepath.Join(out, "den.css"))
		if css != c.own || hasCSS != (c.own != "") || strings.Contains(page, ownLink) != hasCSS {
			t.Errorf("%s: style.css %q (%t), want %q; page:\n%s", c.name, css, hasCSS, c.own, page)
		}
		if den != c.scoped || hasDen != (c.scoped != "") || strings.Contains(page, denLink) != hasDen {
			t.Errorf("%s: den.css %q (%t), want %q; page:\n%s", c.name, den, hasDen, c.scoped, page)
		}
		if hasDen && hasCSS && strings.Index(page, denLink) > strings.Index(page, ownLink) {
			t.Errorf("%s: den.css is linked after style.css:\n%s", c.name, page)
		}
		if !hasDen && page != indexHTML(false, hasCSS, false, false) {
			t.Errorf("%s: page differs from the one without den.css:\n%s", c.name, page)
		}
	}
}

// The files of an app's public directory are copied into dist as they
// are, at the same paths, and dev watches them. One that would replace a
// file den writes is an error, and nothing is written then.
func TestWriteDistPublic(t *testing.T) {
	write := func(root string, files map[string]string) {
		for name, body := range files {
			p := filepath.Join(root, filepath.FromSlash(name))
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, []byte(body), 0o644)
		}
	}
	res := &compiler.Result{JS: "js"}
	dir, out := t.TempDir(), t.TempDir()
	write(dir, map[string]string{"public/pages/1.json": `{"n":1}`, "public/robots.txt": "x\r\n", "public/sub/app.js": "not den's"})
	before := snapshot(dir)
	if err := writeDist(dir, out, res, false, "den app"); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"pages/1.json": `{"n":1}`, "robots.txt": "x\r\n", "sub/app.js": "not den's", "app.js": "js"} {
		if b, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(name))); err != nil || string(b) != want {
			t.Errorf("dist/%s: %q, %v; want %q", name, b, err, want)
		}
	}
	write(dir, map[string]string{"public/pages/2.json": "[]"})
	if snapshot(dir) == before {
		t.Error("a new file in public does not change the dev snapshot")
	}

	// An output directory inside public is an error, which den dev, which
	// watches public, cannot rebuild in a loop: it is found as the same
	// directory as public, whatever its case on a file system that ignores
	// case, and before anything is written.
	dir = t.TempDir()
	write(dir, map[string]string{"public/a.json": "1"})
	outs := []string{filepath.Join(dir, "public", "dist"), filepath.Join(dir, "public"), filepath.Join(dir, "public", "x", "..", "dist")}
	if _, err := os.Stat(filepath.Join(dir, "PUBLIC")); err == nil {
		outs = append(outs, filepath.Join(dir, "PUBLIC", "dist"))
	}
	for _, inside := range outs {
		err := writeDist(dir, inside, res, false, "den app")
		if err == nil || !strings.Contains(err.Error(), "is inside") {
			t.Errorf("-o %s: %v", inside, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "public", "dist")); err == nil {
			t.Fatalf("-o %s: wrote dist into public", inside)
		}
	}

	for _, name := range []string{"app.js", "INDEX.HTML", "den.css", "style.css", "__den/reload.js", "App.js/x.json"} {
		dir, out := t.TempDir(), t.TempDir()
		write(dir, map[string]string{"public/" + name: "x"})
		err := writeDist(dir, out, res, false, "den app")
		if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, "public", strings.Split(name, "/")[0])) {
			t.Errorf("public/%s: %v", name, err)
		}
		if entries, _ := os.ReadDir(out); len(entries) > 0 {
			t.Errorf("public/%s: wrote %d files before failing", name, len(entries))
		}
	}

	// Links could lead out of public: a symbolic link (which creating may
	// need a privilege) or a Windows junction (mklink /J needs none) is an
	// error, in public or as public, before anything is written.
	secret := t.TempDir()
	write(secret, map[string]string{"key.txt": "secret"})
	links := 0
	for _, c := range []struct{ name, target string }{{"public/key.txt", "key.txt"}, {"public/dir", ""}, {"public", ""}} {
		dir, out := t.TempDir(), t.TempDir()
		if c.name != "public" {
			write(dir, map[string]string{"public/a.json": "1"})
		}
		link := filepath.Join(dir, filepath.FromSlash(c.name))
		made := os.Symlink(filepath.Join(secret, c.target), link) == nil
		if !made && runtime.GOOS == "windows" && c.target == "" {
			made = exec.Command("cmd", "/c", "mklink", "/J", link, secret).Run() == nil
		}
		if !made {
			continue
		}
		links++
		if err := writeDist(dir, out, res, false, "den app"); err == nil {
			t.Errorf("a link at %s: no error", c.name)
		}
		if entries, _ := os.ReadDir(out); len(entries) > 0 {
			t.Errorf("a link at %s: wrote %d files before failing", c.name, len(entries))
		}
	}
	if runtime.GOOS == "windows" {
		t.Logf("checked %d of 3 links; a file symlink skips without the privilege", links)
	} else if links < 3 {
		t.Fatalf("created %d of 3 links; Linux CI must create the symbolic links", links)
	}

	// A file named public is no directory.
	dir = t.TempDir()
	write(dir, map[string]string{"public": "x"})
	if err := writeDist(dir, t.TempDir(), res, false, "den app"); err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("public as a file: %v", err)
	}
}

func TestSize(t *testing.T) {
	for n, want := range map[int]string{0: "0 B", 40: "40 B", 1023: "1023 B", 1024: "1.0 KB", 1317: "1.3 KB"} {
		if got := size(n); got != want {
			t.Errorf("size(%d) = %q, want %q", n, got, want)
		}
	}
}

// An app that routes by path loads its files from the site root; others
// keep relative URLs, so their pages are unchanged.
func TestIndexHTML(t *testing.T) {
	for _, c := range []struct {
		router       bool
		den, css, js string
		not          string
	}{
		{false, `<link rel="stylesheet" href="den.css">`, `<link rel="stylesheet" href="style.css">`, `<script src="app.js" defer></script>`, `"/`},
		{true, `<link rel="stylesheet" href="/den.css">`, `<link rel="stylesheet" href="/style.css">`, `<script src="/app.js" defer></script>`, `"app.js"`},
	} {
		page := indexHTML(true, true, false, c.router)
		if !strings.Contains(page, c.den) || !strings.Contains(page, c.css) || !strings.Contains(page, c.js) || strings.Contains(page, c.not) {
			t.Errorf("router %t:\n%s", c.router, page)
		}
	}
}

// The document title is user text. html.EscapeString quotes it so a title
// cannot close the element or insert a tag. The default page is unchanged.
func TestTitleEscape(t *testing.T) {
	page := indexPage(false, false, false, false, `A & B <script> "quoted"`)
	const want = `<title>A &amp; B &lt;script&gt; &#34;quoted&#34;</title>`
	if !strings.Contains(page, want) {
		t.Fatalf("quoted title:\n%s", page)
	}
	closed := indexPage(false, false, false, false, "</title>")
	inner := titleText(t, closed)
	if inner != "&lt;/title&gt;" {
		t.Fatalf("title text %q in:\n%s", inner, closed)
	}
	if indexHTML(false, true, false, false) != indexPage(false, true, false, false, "den app") {
		t.Fatal("indexHTML is not the den app page")
	}
	if !strings.Contains(indexHTML(false, false, false, false), "<title>den app</title>") {
		t.Fatal("default title changed")
	}
}

func titleText(t *testing.T, page string) string {
	t.Helper()
	const open = "<title>"
	i := strings.Index(page, open)
	if i < 0 {
		t.Fatalf("no title in:\n%s", page)
	}
	rest := page[i+len(open):]
	j := strings.Index(rest, "</title>")
	if j < 0 {
		t.Fatalf("title not closed:\n%s", page)
	}
	return rest[:j]
}

// -title and -o are flags wherever they sit. The build compiles that
// directory, not ".", and both orders write the same escaped title.
func TestBuildTitle(t *testing.T) {
	dir := t.TempDir()
	const src = "component App() {\n\t<p>DENCOUNTER</p>\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "app.den"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	title := `A & B <script> "quoted"`
	const want = `<title>A &amp; B &lt;script&gt; &#34;quoted&#34;</title>`

	build := func(out string, args ...string) string {
		t.Helper()
		if code := run(args); code != 0 {
			t.Fatalf("den %s: exit %d", strings.Join(args, " "), code)
		}
		page, err := os.ReadFile(filepath.Join(out, "index.html"))
		if err != nil {
			t.Fatal(err)
		}
		js, err := os.ReadFile(filepath.Join(out, "app.js"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(js), "DENCOUNTER") {
			t.Fatalf("app.js is not from %s:\n%s", dir, js)
		}
		return string(page)
	}

	outBefore := t.TempDir()
	pageBefore := build(outBefore, "build", "-title", title, "-o", outBefore, dir)
	outAfter := t.TempDir()
	pageAfter := build(outAfter, "build", "-o", outAfter, dir, "-title", title)
	if pageBefore != pageAfter {
		t.Fatalf("title position changed index.html\nbefore:\n%s\nafter:\n%s", pageBefore, pageAfter)
	}
	if !strings.Contains(pageBefore, want) {
		t.Fatalf("title not escaped:\n%s", pageBefore)
	}
	if _, err := os.Stat(filepath.Join(dir, "dist")); err == nil {
		t.Fatal("wrote " + filepath.Join(dir, "dist") + "; -o was ignored")
	}

	outEq := t.TempDir()
	pageEq := build(outEq, "build", "-title="+title, dir, "-o", outEq)
	if pageEq != pageBefore {
		t.Fatalf("-title= form differs:\n%s", pageEq)
	}

	outO := t.TempDir()
	pageO := build(outO, "build", "-o", outO, dir)
	if !strings.Contains(pageO, "<title>den app</title>") {
		t.Fatalf("default title:\n%s", pageO)
	}
	if _, err := os.Stat(filepath.Join(dir, "dist")); err == nil {
		t.Fatal("-o before the directory still wrote next to the source")
	}

	if code := run([]string{"build", dir, t.TempDir()}); code != 2 {
		t.Fatalf("two directories: exit %d, want 2", code)
	}
}
