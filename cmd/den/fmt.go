package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/TriadStudios/Den/compiler"
)

const fmtUsage = `usage: den fmt [-l] [-w] [-d] [path ...]

Formats .den files. With no path it formats standard input to standard
output. A directory stands for every .den file under it, apart from those
in dist, node_modules and directories whose names start with a dot.
Without -l, -w or -d, the formatted files go to standard output.

`

// runFmt runs den fmt with the arguments after "fmt" and returns its exit
// status: 2 if any file could not be read or formatted, as gofmt does.
func runFmt(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("fmt", flag.ContinueOnError)
	flags.SetOutput(stderr)
	list := flags.Bool("l", false, "list files whose formatting differs from den fmt's")
	write := flags.Bool("w", false, "write the result to the file instead of standard output")
	diff := flags.Bool("d", false, "print a diff instead of the formatted file")
	flags.Usage = func() {
		fmt.Fprint(stderr, fmtUsage)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	f := &fmtRun{list: *list, write: *write, diff: *diff, stdout: stdout, stderr: stderr}
	if flags.NArg() == 0 {
		if f.write {
			fmt.Fprintln(stderr, "den fmt: cannot use -w with standard input")
			return 2
		}
		src, err := io.ReadAll(stdin)
		if err != nil {
			f.fail(err)
		} else {
			f.format("<standard input>", src, nil)
		}
		return f.status
	}
	for _, path := range flags.Args() {
		info, err := os.Stat(path)
		switch {
		case err != nil:
			f.fail(err)
		case info.IsDir():
			err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
				switch {
				case err != nil:
					f.fail(err)
				case d.IsDir() && p != path && skipDir(d.Name()):
					return filepath.SkipDir
				case !d.IsDir() && strings.HasSuffix(p, ".den"):
					f.file(p)
				}
				return nil
			})
			if err != nil {
				f.fail(err)
			}
		default:
			f.file(path)
		}
	}
	return f.status
}

// skipDir reports whether den fmt leaves the files in a directory alone
// when it walks a tree: build output, npm packages and dot directories.
func skipDir(name string) bool {
	return name == "dist" || name == "node_modules" || strings.HasPrefix(name, ".")
}

type fmtRun struct {
	list, write, diff bool
	stdout, stderr    io.Writer
	status            int
}

func (f *fmtRun) fail(err error) {
	fmt.Fprintln(f.stderr, err)
	f.status = 2
}

func (f *fmtRun) file(path string) {
	src, err := os.ReadFile(path)
	if err != nil {
		f.fail(err)
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		f.fail(err)
		return
	}
	f.format(path, src, info)
}

// format formats src, the contents of the file name (with info, or nil for
// standard input), and does what the flags say with the result.
func (f *fmtRun) format(name string, src []byte, info fs.FileInfo) {
	out, err := compiler.Format(name, src)
	if err != nil {
		f.fail(err)
		return
	}
	changed := !bytes.Equal(src, out)
	if f.list && changed {
		fmt.Fprintln(f.stdout, name)
	}
	if f.write && changed {
		if err := writeAtomic(name, out, info.Mode().Perm()); err != nil {
			f.fail(err)
			return
		}
	}
	if f.diff && changed {
		io.WriteString(f.stdout, unifiedDiff(name, string(src), string(out)))
	}
	if !f.list && !f.write && !f.diff {
		f.stdout.Write(out)
	}
}

// writeAtomic replaces the file at path with data, giving it mode perm: it
// writes a temporary file next to it and renames it over the file, so the
// file is never left half written. A symlink is followed, as gofmt does:
// the file it names is replaced, and the link stays a link.
func writeAtomic(path string, data []byte, perm fs.FileMode) error {
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // fails harmlessly once renamed
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ---- diff -------------------------------------------------------------------

type edit struct {
	op   byte // ' ' kept, '-' removed, '+' added
	text string
}

// lineDiff returns the shortest edit script from lines a to lines b, found
// with Myers' algorithm.
func lineDiff(a, b []string) []edit {
	n, m := len(a), len(b)
	if n+m == 0 {
		return nil
	}
	off := n + m
	v := make([]int, 2*off+2)
	var trace [][]int
search:
	for d := 0; d <= off; d++ {
		trace = append(trace, slices.Clone(v))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || k != d && v[off+k-1] < v[off+k+1] {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x, y = x+1, y+1
			}
			v[off+k] = x
			if x >= n && y >= m {
				break search
			}
		}
	}
	var out []edit
	x, y := n, m
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		prevK := k - 1
		if k == -d || k != d && v[off+k-1] < v[off+k+1] {
			prevK = k + 1
		}
		prevX := v[off+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x, y = x-1, y-1
			out = append(out, edit{' ', a[x]})
		}
		if d == 0 {
			break
		}
		if x == prevX {
			out = append(out, edit{'+', b[y-1]})
		} else {
			out = append(out, edit{'-', a[x-1]})
		}
		x, y = prevX, prevY
	}
	slices.Reverse(out)
	return out
}

// splitLines splits s after each newline.
func splitLines(s string) []string {
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// unifiedDiff returns a unified diff, with three lines of context, from
// the file name holding a to it holding b, or "" if they are the same.
func unifiedDiff(name, a, b string) string {
	edits := lineDiff(splitLines(a), splitLines(b))
	const context = 3
	var out strings.Builder
	for i := 0; i < len(edits); {
		if edits[i].op == ' ' {
			i++
			continue
		}
		// A hunk: changes no more than 2*context kept lines apart, with
		// context around them.
		start := max(i-context, 0)
		end := i
		for j := i; j < len(edits); j++ {
			if edits[j].op != ' ' {
				end = j + 1
			} else if j-end >= 2*context {
				break
			}
		}
		end = min(end+context, len(edits))
		aLine, bLine := 1, 1
		for _, e := range edits[:start] {
			if e.op != '+' {
				aLine++
			}
			if e.op != '-' {
				bLine++
			}
		}
		aLen, bLen := 0, 0
		for _, e := range edits[start:end] {
			if e.op != '+' {
				aLen++
			}
			if e.op != '-' {
				bLen++
			}
		}
		if aLen == 0 {
			aLine--
		}
		if bLen == 0 {
			bLine--
		}
		if out.Len() == 0 {
			fmt.Fprintf(&out, "diff %s.orig %s\n--- %s.orig\n+++ %s\n", name, name, name, name)
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", aLine, aLen, bLine, bLen)
		for _, e := range edits[start:end] {
			out.WriteByte(e.op)
			out.WriteString(e.text)
			if !strings.HasSuffix(e.text, "\n") {
				out.WriteString("\n\\ No newline at end of file\n")
			}
		}
		i = end
	}
	return out.String()
}
