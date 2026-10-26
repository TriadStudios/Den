package compiler

import (
	"regexp"
	"strings"
)

// minifyJS removes comments and the whitespace that JS does not need from
// den's own runtime. It keeps one space between identifier characters and
// keeps the line breaks that automatic semicolon insertion could depend on.
// It understands strings and regex literals, which is all the runtime uses.
func minifyJS(src string) string {
	var b strings.Builder
	word := func(c byte) bool {
		return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
	}
	last := func() byte {
		s := b.String()
		if s == "" {
			return '\n'
		}
		return s[len(s)-1]
	}
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			i--
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			i += end + 3
		case c == '"' || c == '\'' || c == '`' || (c == '/' && strings.IndexByte("(,=:[!&|?{};\n", last()) >= 0):
			j := i + 1
			class := false
			for ; j < len(src); j++ {
				if src[j] == '\\' {
					j++
					continue
				}
				if c == '/' && src[j] == '[' {
					class = true
				} else if c == '/' && src[j] == ']' {
					class = false
				} else if src[j] == c && !class {
					break
				}
			}
			b.WriteString(src[i : j+1])
			i = j
		case c == '\n' || c == '\r':
			// A line break after these can never end a statement early.
			if strings.IndexByte("\n;{},", last()) < 0 {
				b.WriteByte('\n')
			}
		case c == ' ' || c == '\t':
			j := i
			for j < len(src) && (src[j] == ' ' || src[j] == '\t') {
				j++
			}
			if j < len(src) && word(last()) && word(src[j]) {
				b.WriteByte(' ')
			}
			i = j - 1
		case c == '}' && last() == ';':
			// "a;}" is "a}".
			out := b.String()
			b.Reset()
			b.WriteString(out[:len(out)-1])
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	// "(x)=>" is "x=>": a call cannot be followed by "=>", so outside strings
	// (where the runtime never has one) this only matches arrows. true and
	// false are !0 and !1, which the runtime never uses as names.
	out := arrowParen.ReplaceAllString(strings.TrimSpace(b.String()), "$1=>")
	// const is let: the runtime never assigns to a constant.
	out = constWord.ReplaceAllString(out, "${1}let ")
	out = boolWord.ReplaceAllStringFunc(out, func(m string) string {
		i := strings.IndexAny(m, "tf") // where the word starts
		if m[i] == 't' {
			return strings.TrimSuffix(m[:i], " ") + "!0"
		}
		return strings.TrimSuffix(m[:i], " ") + "!1"
	})
	return out + "\n"
}

var (
	arrowParen = regexp.MustCompile(`\(([A-Za-z_$][\w$]*)\)=>`)
	constWord  = regexp.MustCompile(`(^|[;{}(\n])const `)
	boolWord   = regexp.MustCompile(`(?:^|[ ;(,=:?!&|{}\[\]])(?:true|false)\b`)
)
