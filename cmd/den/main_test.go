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
		if err := writeDist(dir, out, &compiler.Result{JS: "js", CSS: c.scoped}, false); err != nil {
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
