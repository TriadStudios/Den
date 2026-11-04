package compiler

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
)

// den code can import a curated part of the Go standard library. Each
// package is declared here as Go (for the type checker) and implemented in
// stdlib.js or inline (see stdCalls in emit.go).
var stdPackages = map[string]string{
	"strings": `package strings

func Contains(s, substr string) bool         { return false }
func HasPrefix(s, prefix string) bool        { return false }
func HasSuffix(s, suffix string) bool        { return false }
func TrimSpace(s string) string              { return s }
func TrimPrefix(s, prefix string) string     { return s }
func TrimSuffix(s, suffix string) string     { return s }
func ToLower(s string) string                { return s }
func ToUpper(s string) string                { return s }
func Split(s, sep string) []string           { return nil }
func Join(elems []string, sep string) string { return "" }
func Repeat(s string, count int) string      { return s }
func ReplaceAll(s, old, new string) string   { return s }
func Fields(s string) []string               { return nil }
`,
	"strconv": `package strconv

func Itoa(i int) string           { return "" }
func Atoi(s string) (int, error)  { return 0, nil }
func FormatBool(b bool) string    { return "" }
`,
	// A Duration is a JS number of nanoseconds, exact up to 2^53 (about
	// 104 days). A Time is {w, m} in JS, its wall clock and monotonic
	// clock readings in ms (see $tnow in stdlib.js), and the zero Time is
	// null: Go's zero time, January 1, year 1, UTC. Time holds a func, so
	// go/types rejects comparing two of them, which Go allows (check
	// rewords the error).
	"time": `package time

type Duration int64

const (
	Nanosecond  Duration = 1
	Microsecond          = 1000 * Nanosecond
	Millisecond          = 1000 * Microsecond
	Second               = 1000 * Millisecond
	Minute               = 60 * Second
	Hour                 = 60 * Minute
)

func (d Duration) Milliseconds() int64 { return 0 }
func (d Duration) Seconds() float64    { return 0 }

type Time struct{ _ opaque }

type opaque func()

func Now() Time                    { return Time{} }
func Since(t Time) Duration        { return 0 }
func (t Time) Sub(u Time) Duration { return 0 }
func (t Time) UnixMilli() int64    { return 0 }
func (t Time) Hour() int           { return 0 }
func (t Time) Minute() int         { return 0 }
func (t Time) Second() int         { return 0 }
`,
	"errors": `package errors

func New(text string) error { return nil }
`,
}

// isTime reports whether t is the Time of den's package time.
func isTime(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Name() == "Time" && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "time"
}

//go:embed stdlib.js
var stdlibJS string

// stdImporter type-checks the declarations in stdPackages on demand.
type stdImporter struct {
	fset *token.FileSet
	pkgs map[string]*types.Package
}

func (im stdImporter) Import(path string) (*types.Package, error) {
	if p := im.pkgs[path]; p != nil {
		return p, nil
	}
	src, ok := stdPackages[path]
	if path == "den" {
		src, ok = denPackage, true
	}
	if !ok {
		return nil, fmt.Errorf("package %q is not available in den", path)
	}
	f, err := parser.ParseFile(im.fset, "den_std_"+path+".go", src, 0)
	if err != nil {
		return nil, err
	}
	p, err := (&types.Config{}).Check(path, im.fset, []*ast.File{f}, nil)
	if err != nil {
		return nil, err
	}
	im.pkgs[path] = p
	return p, nil
}
