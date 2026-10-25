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
