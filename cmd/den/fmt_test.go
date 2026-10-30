package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const (
	messy = "component App() {\n    n := signal( 1 )\n    <p>{n.Get()}</p>\n}\n"
	tidy  = "component App() {\n\tn := signal(1)\n\t<p>{n.Get()}</p>\n}\n"
	bad   = "component App() {\n    <div>\n}\n"
)

func runFmtTest(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := runFmt(args, strings.NewReader(stdin), &out, &errs)
	return code, out.String(), errs.String()
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// With no path, den fmt formats standard input to standard output.
func TestFmtStdin(t *testing.T) {
	code, out, errs := runFmtTest(t, messy)
	if code != 0 || out != tidy || errs != "" {
		t.Errorf("got %d %q %q", code, out, errs)
	}
	code, out, errs = runFmtTest(t, bad)
	if code != 2 || out != "" || errs != "<standard input>:2:5: unclosed <div>\n" {
		t.Errorf("bad input: got %d %q %q", code, out, errs)
	}
	if code, _, errs := runFmtTest(t, messy, "-w"); code != 2 || !strings.Contains(errs, "-w") {
		t.Errorf("-w on standard input: got %d %q", code, errs)
	}
}

// Without flags, files are printed formatted; -l lists the files that
// would change, and exits 0 as gofmt does.
func TestFmtPrintAndList(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a.den": messy, "b.den": tidy})
	a, b := filepath.Join(dir, "a.den"), filepath.Join(dir, "b.den")
	if code, out, _ := runFmtTest(t, "", a, b); code != 0 || out != tidy+tidy {
		t.Errorf("print: got %d %q", code, out)
	}
	if code, out, _ := runFmtTest(t, "", "-l", a, b); code != 0 || out != a+"\n" {
		t.Errorf("-l: got %d %q", code, out)
	}
	if readFile(t, a) != messy {
		t.Error("-l changed a file")
	}
}

// -w rewrites only the files that change, through a temporary file renamed
// over the original, and keeps their permissions.
func TestFmtWrite(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a.den": messy, "b.den": tidy})
	a, b := filepath.Join(dir, "a.den"), filepath.Join(dir, "b.den")
	if err := os.Chmod(a, 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(a)
	if runtime.GOOS != "windows" && before.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", before.Mode())
	}
	bInfo, _ := os.Stat(b)
	if code, out, errs := runFmtTest(t, "", "-w", a, b); code != 0 || out != "" || errs != "" {
		t.Fatalf("got %d %q %q", code, out, errs)
	}
	if got := readFile(t, a); got != tidy {
		t.Errorf("a.den is %q", got)
	}
	after, _ := os.Stat(a)
	if after.Mode().Perm() != before.Mode().Perm() {
		t.Errorf("mode %v became %v", before.Mode().Perm(), after.Mode().Perm())
	}
	if bAfter, _ := os.Stat(b); !bAfter.ModTime().Equal(bInfo.ModTime()) {
		t.Error("b.den was formatted already but was rewritten")
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"a.den", "b.den"}) {
		t.Errorf("files left behind: %v", names)
	}
}

// -d prints a unified diff of each file that would change.
func TestFmtDiff(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a.den": messy, "b.den": tidy})
	a, b := filepath.Join(dir, "a.den"), filepath.Join(dir, "b.den")
	code, out, _ := runFmtTest(t, "", "-d", a, b)
	want := "diff " + a + ".orig " + a + "\n--- " + a + ".orig\n+++ " + a + "\n" +
		"@@ -1,4 +1,4 @@\n component App() {\n-    n := signal( 1 )\n-    <p>{n.Get()}</p>\n+\tn := signal(1)\n+\t<p>{n.Get()}</p>\n }\n"
	if code != 0 || out != want {
		t.Errorf("got %d\n%s\nwant\n%s", code, out, want)
	}
	if readFile(t, a) != messy {
		t.Error("-d changed a file")
	}
}

func TestUnifiedDiff(t *testing.T) {
	lines := func(n int, change map[int]string) string {
		var b strings.Builder
		for i := 1; i <= n; i++ {
			if s, ok := change[i]; ok {
				b.WriteString(s)
				continue
			}
			b.WriteString(strings.Repeat("x", i) + "\n")
		}
		return b.String()
	}
	for _, c := range []struct{ name, a, b, want string }{
		{"same", "a\nb\n", "a\nb\n", ""},
		{"two hunks", lines(20, nil), lines(20, map[int]string{2: "two\n", 18: ""}),
			"diff f.orig f\n--- f.orig\n+++ f\n" +
				"@@ -1,5 +1,5 @@\n x\n-xx\n+two\n xxx\n xxxx\n xxxxx\n" +
				"@@ -15,6 +15,5 @@\n " + strings.Repeat("x", 15) + "\n " + strings.Repeat("x", 16) + "\n " + strings.Repeat("x", 17) + "\n-" + strings.Repeat("x", 18) + "\n " + strings.Repeat("x", 19) + "\n " + strings.Repeat("x", 20) + "\n"},
		{"from empty", "", "a\n", "diff f.orig f\n--- f.orig\n+++ f\n@@ -0,0 +1,1 @@\n+a\n"},
		{"no final newline", "a\nb", "a\nb\n", "diff f.orig f\n--- f.orig\n+++ f\n@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+b\n"},
	} {
		if got := unifiedDiff("f", c.a, c.b); got != c.want {
			t.Errorf("%s:\n%s\nwant\n%s", c.name, got, c.want)
		}
	}
}

// A directory stands for the .den files under it, but not those in dist,
// node_modules or dot directories. A file that does not parse is reported
// as file:line:col, left as it is, and makes the exit status 2, but the
// other files are still formatted.
func TestFmtDirectories(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"a.den":                 messy,
		"sub/b.den":             messy,
		"sub/deeper/c.den":      tidy,
		"sub/broken.den":        bad,
		"dist/d.den":            messy,
		".git/e.den":            messy,
		"node_modules/x/f.den":  messy,
		"notes.txt":             messy,
		"sub/deeper/g.den.orig": messy,
	})
	code, out, errs := runFmtTest(t, "", "-l", "-w", dir)
	want := []string{filepath.Join(dir, "a.den"), filepath.Join(dir, "sub", "b.den")}
	if got := strings.Fields(out); code != 2 || !slices.Equal(got, want) {
		t.Errorf("got %d %q, want 2 %q", code, got, want)
	}
	if wantErr := filepath.Join(dir, "sub", "broken.den") + ":2:5: unclosed <div>\n"; errs != wantErr {
		t.Errorf("stderr %q, want %q", errs, wantErr)
	}
	for name, src := range map[string]string{
		"a.den": tidy, "sub/b.den": tidy, "sub/broken.den": bad, "dist/d.den": messy,
		".git/e.den": messy, "node_modules/x/f.den": messy, "notes.txt": messy,
	} {
		if got := readFile(t, filepath.Join(dir, name)); got != src {
			t.Errorf("%s is %q, want %q", name, got, src)
		}
	}
	// A file named on the command line is formatted whatever its name.
	if code, out, _ := runFmtTest(t, "", filepath.Join(dir, "notes.txt")); code != 0 || out != tidy {
		t.Errorf("named file: got %d %q", code, out)
	}
	if code, _, errs := runFmtTest(t, "", filepath.Join(dir, "missing.den")); code != 2 || errs == "" {
		t.Errorf("missing file: got %d %q", code, errs)
	}
}
