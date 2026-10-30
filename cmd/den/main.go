// Command den builds, checks, serves and formats den apps.
//
//	den build <dir> [-o dist]   compile <dir>/*.den into dist/app.js + index.html
//	den check <dir>             type-check only
//	den dev   <dir> [-addr :8080] build, serve and rebuild on save with live reload
//	den fmt [-l] [-w] [-d] [path ...]  format .den files, as gofmt formats Go
package main

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TriadStudios/Den/compiler"
)

const usage = `Den: Go-syntax UI components compiled to small JavaScript.

usage:
  den build <dir> [-o dist]       compile <dir>/*.den
  den check <dir>                 type-check without writing files
  den dev   <dir> [-addr :8080]   serve with rebuild + live reload
  den fmt [-l] [-w] [-d] [path ...]
                                  format .den files (stdin without a path):
                                  -l lists files that would change, -w
                                  rewrites them, -d prints diffs
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	if cmd == "fmt" {
		os.Exit(runFmt(args, os.Stdin, os.Stdout, os.Stderr))
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	out := fs.String("o", "", "output directory (default <dir>/dist)")
	addr := fs.String("addr", "localhost:8080", "dev server address")
	dir := "."
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		dir, args = args[0], args[1:]
	}
	fs.Parse(args)
	if *out == "" {
		*out = filepath.Join(dir, "dist")
	}
	switch cmd {
	case "build":
		res, err := compileDir(dir)
		if err != nil {
			fail(err)
		}
		if err := writeDist(dir, *out, res, false); err != nil {
			fail(err)
		}
		report(*out, res)
	case "check":
		res, err := compileDir(dir)
		if err != nil {
			fail(err)
		}
		fmt.Printf("ok: %d components (%s)\n", len(res.Components), strings.Join(res.Components, ", "))
	case "dev":
		dev(dir, *out, *addr)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func denFiles(dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.den"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .den files in %s", dir)
	}
	sort.Strings(files)
	return files, nil
}

func compileDir(dir string) (*compiler.Result, error) {
	files, err := denFiles(dir)
	if err != nil {
		return nil, err
	}
	var srcs []compiler.Source
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		srcs = append(srcs, compiler.Source{Name: f, Code: string(b)})
	}
	return compiler.Compile(srcs)
}

// csp is strict: same-origin scripts only, no eval, no inline code, Trusted
// Types enforced with no policies allowed (the runtime never needs one).
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: https:; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; " +
	"require-trusted-types-for 'script'; trusted-types 'none'"

// indexHTML is the page that loads the app. An app that routes by path is
// served at each of its paths (/users/7 as well as /), so it finds its
// files from the site root; others keep relative URLs and can live in any
// directory. den.css, the CSS of the components' style blocks, comes before
// the app's own style.css, whose rules then win.
func indexHTML(denCSS, hasCSS, dev, router bool) string {
	root := ""
	if router {
		root = "/"
	}
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	fmt.Fprintf(&b, "<meta http-equiv=\"Content-Security-Policy\" content=\"%s\">\n", csp)
	b.WriteString("<title>den app</title>\n")
	if denCSS {
		fmt.Fprintf(&b, "<link rel=\"stylesheet\" href=\"%sden.css\">\n", root)
	}
	if hasCSS {
		fmt.Fprintf(&b, "<link rel=\"stylesheet\" href=\"%sstyle.css\">\n", root)
	}
	fmt.Fprintf(&b, "<script src=\"%sapp.js\" defer></script>\n", root)
	if dev {
		b.WriteString("<script src=\"/__den/reload.js\" defer></script>\n")
	}
	b.WriteString("</head>\n<body>\n<div id=\"app\"></div>\n</body>\n</html>\n")
	return b.String()
}

// writeDist writes app.js, index.html, the app's own style.css as it is,
// and den.css, the CSS of the components' style blocks, when there is any.
// den.css is a file of its own, linked first: the app's rules come later
// and can give the design tokens other values, and its style.css keeps
// what only works at the start of a file, such as @import, @charset or a
// byte order mark. The page's CSP allows no inline styles, so all CSS
// goes in files.
func writeDist(dir, out string, res *compiler.Result, dev bool) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "app.js"), []byte(res.JS), 0o644); err != nil {
		return err
	}
	denCSS := res.CSS != ""
	if denCSS {
		if err := os.WriteFile(filepath.Join(out, "den.css"), []byte(res.CSS), 0o644); err != nil {
			return err
		}
	} else if err := os.Remove(filepath.Join(out, "den.css")); err != nil && !os.IsNotExist(err) {
		return err // the app had style blocks before, but has none now
	}
	css, err := os.ReadFile(filepath.Join(dir, "style.css"))
	hasCSS := err == nil
	if hasCSS {
		if err := os.WriteFile(filepath.Join(out, "style.css"), css, 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(out, "index.html"), []byte(indexHTML(denCSS, hasCSS, dev, res.Router)), 0o644)
}

func gzipSize(b []byte) int {
	var buf bytes.Buffer
	w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	w.Write(b)
	w.Close()
	return buf.Len()
}

func report(out string, res *compiler.Result) {
	js := []byte(res.JS)
	fmt.Printf("built %d components → %s\n", len(res.Components), out)
	fmt.Printf("  app.js  %.1f KB  (%.1f KB gzip)\n", float64(len(js))/1024, float64(gzipSize(js))/1024)
	if css := []byte(res.CSS); len(css) > 0 {
		fmt.Printf("  den.css  %s  (%s gzip)\n", size(len(css)), size(gzipSize(css)))
	}
}

// size formats n bytes: in KB from 1 KB, in bytes below it, where one
// decimal of a KB would round small CSS down to 0.0 KB.
func size(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KB", float64(n)/1024)
}

// ---- dev server -------------------------------------------------------------

const reloadJS = `(() => {
  const es = new EventSource("/__den/events");
  let box = null;
  es.addEventListener("reload", () => location.reload());
  es.addEventListener("error", (ev) => {
    if (!ev.data) return;
    if (!box) {
      box = document.createElement("pre");
      box.style.cssText = "position:fixed;inset:auto 12px 12px 12px;max-height:60vh;overflow:auto;margin:0;padding:14px;background:#2b0d0d;color:#ffd7d7;font:13px/1.45 ui-monospace,monospace;border-radius:10px;z-index:2147483647;white-space:pre-wrap";
      document.body.append(box);
    }
    box.textContent = "Den: build failed\n\n" + ev.data;
  });
})();
`

type hub struct {
	mu   sync.Mutex
	subs map[chan string]bool
}

func (h *hub) broadcast(msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs {
		select {
		case c <- msg:
		default:
		}
	}
}

func dev(dir, out, addr string) {
	h := &hub{subs: map[chan string]bool{}}
	lastErr := ""
	var mu sync.Mutex
	build := func() {
		start := time.Now()
		res, err := compileDir(dir)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			lastErr = err.Error()
			fmt.Fprintln(os.Stderr, lastErr)
			h.broadcast("error\n" + lastErr)
			return
		}
		lastErr = ""
		if err := writeDist(dir, out, res, true); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		fmt.Printf("rebuilt in %s\n", time.Since(start).Round(time.Millisecond))
		h.broadcast("reload\n")
	}
	build()

	go func() {
		stamps := snapshot(dir)
		for range time.Tick(200 * time.Millisecond) {
			now := snapshot(dir)
			if now != stamps {
				stamps = now
				build()
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/__den/reload.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Write([]byte(reloadJS))
	})
	mux.HandleFunc("/__den/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		c := make(chan string, 4)
		h.mu.Lock()
		h.subs[c] = true
		h.mu.Unlock()
		defer func() { h.mu.Lock(); delete(h.subs, c); h.mu.Unlock() }()
		mu.Lock()
		if lastErr != "" {
			c <- "error\n" + lastErr
		}
		mu.Unlock()
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, ": connected\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		for {
			select {
			case <-r.Context().Done():
				return
			case msg := <-c:
				event, data, _ := strings.Cut(msg, "\n")
				fmt.Fprintf(w, "event: %s\n", event)
				for _, line := range strings.Split(data, "\n") {
					fmt.Fprintf(w, "data: %s\n", line)
				}
				fmt.Fprint(w, "\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	})
	mux.Handle("/", devFiles(out))
	fmt.Printf("den dev: http://%s  (watching %s)\n", addr, dir)
	fail(http.ListenAndServe(addr, mux))
}

// devFiles serves the built app under the strict CSP. A GET for a path with
// no file, whose last segment has no '.', gets index.html, so an app that
// routes by path loads at any of its URLs; a missing app.js or image is
// still a 404.
func devFiles(out string) http.Handler {
	files := http.FileServer(http.Dir(out))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("Cache-Control", "no-store")
		name := path.Clean("/" + r.URL.Path)
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && !strings.HasPrefix(name, "/__den/") && !strings.Contains(path.Base(name), ".") {
			if f, err := http.Dir(out).Open(name); err == nil {
				f.Close()
			} else {
				http.ServeFile(w, r, filepath.Join(out, "index.html"))
				return
			}
		}
		files.ServeHTTP(w, r)
	})
}

func snapshot(dir string) string {
	var b strings.Builder
	matches, _ := filepath.Glob(filepath.Join(dir, "*"))
	for _, m := range matches {
		if ext := filepath.Ext(m); ext != ".den" && ext != ".css" {
			continue
		}
		if st, err := os.Stat(m); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", m, st.ModTime().UnixNano(), st.Size())
		}
	}
	return b.String()
}
