package compiler

import "strings"

// minifyJS removes comments and the whitespace that JS does not need from
// den's own runtime: it keeps one space between identifier characters and
// keeps line breaks, so automatic semicolon insertion is never affected. It
// understands strings and regex literals, which is all the runtime uses.
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
			if last() != '\n' {
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
		default:
			b.WriteByte(c)
		}
	}
	return strings.TrimSpace(b.String()) + "\n"
}
