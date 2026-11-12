package main

import (
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestEmbedRecipe compiles the Go server documented in the README and
// checks the handler it exports. The fences are the recipe; this test
// supplies the fixture and the assertions.
func TestEmbedRecipe(t *testing.T) {
	root := moduleRoot(t)
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	md := strings.ReplaceAll(string(readme), "\r\n", "\n")
	handlerSrc := extractFence(t, md, "<!-- den-embed-handler -->", "<!-- /den-embed-handler -->")
	mainSrc := extractFence(t, md, "<!-- den-embed-main -->", "<!-- /den-embed-main -->")
	if strings.Contains(handlerSrc, "//go:embed") {
		t.Fatal("handler fence contains //go:embed; the test passes an fs.FS")
	}
	if !strings.Contains(mainSrc, "//go:embed all:dist") {
		t.Fatal("main fence missing //go:embed all:dist")
	}
	if !strings.Contains(handlerSrc, "require-trusted-types-for 'script'; trusted-types 'none'") ||
		!strings.Contains(handlerSrc, "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: https:; ") {
		t.Fatal("handler fence does not contain den's CSP")
	}
	handlerSrc = mustGofmt(t, "handler", handlerSrc)
	mainSrc = mustGofmt(t, "main", mainSrc)

	mod := t.TempDir()
	writeFile(t, filepath.Join(mod, "go.mod"), "module example.com/denpage\n\ngo "+goVersion(filepath.Join(root, "go.mod"))+"\n")
	writeFile(t, filepath.Join(mod, "handler.go"), handlerSrc)
	writeFile(t, filepath.Join(mod, "handler_test.go"), handlerTestSrc)
	writeFile(t, filepath.Join(mod, "cmd", "server", "main.go"), mainSrc)
	writeFixture(t, filepath.Join(mod, "dist"))
	writeFixture(t, filepath.Join(mod, "cmd", "server", "dist"))

	testCmd := exec.Command("go", "test", "-count=1", ".")
	testCmd.Dir = mod
	testCmd.Env = append(os.Environ(), "GOFLAGS=-buildvcs=false")
	if out, err := testCmd.CombinedOutput(); err != nil {
		t.Fatalf("go test of the embed recipe: %v\n%s", err, out)
	}

	bin := filepath.Join(t.TempDir(), "denpage-server")
	build := exec.Command("go", "build", "-o", bin, "./cmd/server")
	build.Dir = mod
	build.Env = append(os.Environ(), "GOFLAGS=-buildvcs=false")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/server: %v\n%s", err, out)
	}
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		os.Remove(bin + ".exe")
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "README.md")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("module root not found from " + file)
		}
		dir = parent
	}
}

func goVersion(goMod string) string {
	b, err := os.ReadFile(goMod)
	if err != nil {
		return "1.22"
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "go "); ok && v != "" && !strings.Contains(v, " ") {
			return v
		}
	}
	return "1.22"
}

func extractFence(t *testing.T, md, open, close string) string {
	t.Helper()
	i := strings.Index(md, open)
	if i < 0 {
		t.Fatalf("README missing %s", open)
	}
	rest := md[i+len(open):]
	if !strings.HasPrefix(rest, "\n") {
		t.Fatalf("%s is not on its own line", open)
	}
	j := strings.Index(rest, close)
	if j < 0 {
		t.Fatalf("README missing %s", close)
	}
	if !strings.HasSuffix(rest[:j], "\n") {
		t.Fatalf("%s is not on its own line", close)
	}
	block := strings.Trim(rest[:j], "\n")
	if !strings.HasPrefix(block, "```go\n") || !strings.HasSuffix(block, "```") {
		t.Fatalf("%s is not a go fence", open)
	}
	src := strings.TrimSuffix(strings.TrimPrefix(block, "```go\n"), "```")
	return strings.TrimRight(src, "\n") + "\n"
}

func mustGofmt(t *testing.T, name, src string) string {
	t.Helper()
	out, err := format.Source([]byte(src))
	if err != nil {
		t.Fatalf("%s fence does not parse: %v\n%s", name, err, src)
	}
	if string(out) != src {
		t.Fatalf("%s fence is not gofmt-clean\n--- have\n%s\n--- want\n%s", name, src, out)
	}
	return src
}

func writeFixture(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"index.html": "<!doctype html><html><body>DENINDEX</body></html>\n",
		"app.js":     "console.log(1)",
		"style.css":  "body{}",
		"den.css":    "x{}",
		"readme.txt": "plain",
	}
	for name, body := range files {
		writeFile(t, filepath.Join(dir, name), body)
	}
	writeFile(t, filepath.Join(dir, "logo-0123abcd.png"), "PNG")
	writeFile(t, filepath.Join(dir, "sub", "index.html"), "SUBINDEX")
	writeFile(t, filepath.Join(dir, "bare", "note.txt"), "NOTE")
	writeFile(t, filepath.Join(dir, ".secret"), "SECRET")
	writeFile(t, filepath.Join(dir, "dir", ".env"), "DOTENV")
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const handlerTestSrc = `package denpage_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/denpage"
)

const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: https:; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; " +
	"require-trusted-types-for 'script'; trusted-types 'none'"

func TestHandler(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	h := denpage.Handler(os.DirFS(filepath.Join(wd, "dist")))

	for _, p := range []string{"/", "/users/7"} {
		w := do(t, h, http.MethodGet, p)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "DENINDEX") {
			t.Fatalf("GET %s: %d %q", p, w.Code, w.Body.String())
		}
		if loc := w.Header().Get("Location"); loc != "" {
			t.Fatalf("GET %s: Location %q", p, loc)
		}
	}

	w := do(t, h, http.MethodGet, "/index.html")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "DENINDEX") {
		t.Fatalf("GET /index.html: %d %q", w.Code, w.Body.String())
	}
	if w.Code == http.StatusMovedPermanently || w.Code == http.StatusFound {
		t.Fatalf("GET /index.html redirected: %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Fatalf("GET /index.html: Location %q", loc)
	}

	w = do(t, h, http.MethodGet, "/app.js")
	if w.Code != http.StatusOK || w.Body.String() != "console.log(1)" {
		t.Fatalf("GET /app.js: %d %q", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("app.js Cache-Control %q", w.Header().Get("Cache-Control"))
	}
		if w.Header().Get("Content-Security-Policy") != csp {
			t.Fatalf("CSP %q", w.Header().Get("Content-Security-Policy"))
		}
		if w.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatalf("X-Frame-Options %q", w.Header().Get("X-Frame-Options"))
		}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("X-Content-Type-Options %q", w.Header().Get("X-Content-Type-Options"))
	}
	if w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("Referrer-Policy %q", w.Header().Get("Referrer-Policy"))
	}

	for _, p := range []string{"/den.css", "/style.css", "/index.html"} {
		got := do(t, h, http.MethodGet, p)
		if got.Code != http.StatusOK || got.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s: %d cache %q", p, got.Code, got.Header().Get("Cache-Control"))
		}
	}
	if body := do(t, h, http.MethodGet, "/den.css").Body.String(); body != "x{}" {
		t.Fatalf("den.css %q", body)
	}
	if body := do(t, h, http.MethodGet, "/style.css").Body.String(); body != "body{}" {
		t.Fatalf("style.css %q", body)
	}

	w = do(t, h, http.MethodGet, "/logo-0123abcd.png")
	if w.Code != http.StatusOK || w.Body.String() != "PNG" || w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("logo: %d %q cache %q", w.Code, w.Body.String(), w.Header().Get("Cache-Control"))
	}
	w = do(t, h, http.MethodGet, "/readme.txt")
	if w.Code != http.StatusOK || w.Body.String() != "plain" || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("readme: %d %q cache %q", w.Code, w.Body.String(), w.Header().Get("Cache-Control"))
	}
	if w := do(t, h, http.MethodGet, "/missing.js"); w.Code != http.StatusNotFound {
		t.Fatalf("missing.js: %d %q", w.Code, w.Body.String())
	}
	for _, p := range []string{"/api/start", "/api"} {
		w := do(t, h, http.MethodGet, p)
		if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "DENINDEX") {
			t.Fatalf("GET %s: %d %q", p, w.Code, w.Body.String())
		}
	}

	w = do(t, h, http.MethodHead, "/app.js")
	if w.Code != http.StatusOK || w.Body.String() != "" || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("HEAD /app.js: %d %q cache %q", w.Code, w.Body.String(), w.Header().Get("Cache-Control"))
	}
	if w := do(t, h, http.MethodPost, "/"); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /: %d", w.Code)
	}

	for _, p := range []string{"/sub", "/sub/"} {
		w := do(t, h, http.MethodGet, p)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "SUBINDEX") {
			t.Fatalf("GET %s: %d %q", p, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "<a href") {
			t.Fatalf("GET %s listed the directory: %q", p, w.Body.String())
		}
	}
	for _, p := range []string{"/bare", "/bare/"} {
		w := do(t, h, http.MethodGet, p)
		if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "note.txt") || strings.Contains(w.Body.String(), "NOTE") || strings.Contains(w.Body.String(), "<a href") {
			t.Fatalf("GET %s: %d %q", p, w.Code, w.Body.String())
		}
	}
	for _, p := range []string{"/.secret", "/dir/.env"} {
		w := do(t, h, http.MethodGet, p)
		if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "SECRET") || strings.Contains(w.Body.String(), "DOTENV") {
			t.Fatalf("GET %s: %d %q", p, w.Code, w.Body.String())
		}
	}
}

func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}
`
