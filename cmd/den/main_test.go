package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
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

// style.css is the components' scoped CSS followed by the app's own, which
// can then override their tokens; an app with neither has no style.css,
// and an app without style blocks gets its own file unchanged.
func TestWriteDistCSS(t *testing.T) {
	const own = "body { margin: 0 }\n"
	const scoped = ":root{--ink:#000}\np[d-abcde]{color:var(--ink)}\n"
	for _, c := range []struct {
		name        string
		own, scoped string
		want        string // "" when there is no style.css
	}{
		{"both", own, scoped, scoped + own},
		{"only the app's", own, "", own},
		{"only scoped", "", scoped, scoped},
		{"neither", "", "", ""},
	} {
		dir, out := t.TempDir(), t.TempDir()
		if c.own != "" {
			os.WriteFile(filepath.Join(dir, "style.css"), []byte(c.own), 0o644)
		}
		if err := writeDist(dir, out, &compiler.Result{JS: "js", CSS: c.scoped}, false); err != nil {
			t.Fatal(err)
		}
		css, err := os.ReadFile(filepath.Join(out, "style.css"))
		page, _ := os.ReadFile(filepath.Join(out, "index.html"))
		link := strings.Contains(string(page), `<link rel="stylesheet" href="style.css">`)
		if c.want == "" {
			if err == nil || link {
				t.Errorf("%s: style.css %q, link %t", c.name, css, link)
			}
			continue
		}
		if string(css) != c.want || !link {
			t.Errorf("%s: style.css %q, want %q; link %t", c.name, css, c.want, link)
		}
	}
}

// An app that routes by path loads its files from the site root; others
// keep relative URLs, so their pages are unchanged.
func TestIndexHTML(t *testing.T) {
	for _, c := range []struct {
		router  bool
		css, js string
		not     string
	}{
		{false, `<link rel="stylesheet" href="style.css">`, `<script src="app.js" defer></script>`, `"/app.js"`},
		{true, `<link rel="stylesheet" href="/style.css">`, `<script src="/app.js" defer></script>`, `"app.js"`},
	} {
		page := indexHTML(true, false, c.router)
		if !strings.Contains(page, c.css) || !strings.Contains(page, c.js) || strings.Contains(page, c.not) {
			t.Errorf("router %t:\n%s", c.router, page)
		}
	}
}
