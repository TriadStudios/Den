package compiler

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// A second, independent reading of the CSS that style blocks compile to.
//
// Den's own scanner (cssAtom and the styler) decides where strings,
// comments, blocks and declarations end. Wherever it disagreed with a
// browser, one rule could swallow the rules after it, other components'
// rules among them. So the CSS den writes for each block is read again
// here, by a tokenizer that follows the consume-a-token algorithm of CSS
// Syntax Level 3 and shares no code with den's scanner, and its rules and
// declarations must come out exactly as den wrote them.
// If they do not, the build fails instead of shipping CSS that a browser
// reads differently.

// cssItem is a rule or a declaration as a browser reads it.
type cssItem struct {
	text  string    // the prelude of a rule, or the declaration
	rule  bool      // text is followed by a {} block
	items []cssItem // the contents of the block
}

// Token kinds.
const (
	ctWhitespace = iota
	ctString
	ctBadString
	ctURL
	ctBadURL
	ctFunction
	ctOpenParen
	ctCloseParen
	ctOpenBracket
	ctCloseBracket
	ctOpenBrace
	ctCloseBrace
	ctSemicolon
	ctCDO
	ctCDC
	ctOther // ident, at-keyword, hash, number, colon, comma, delim and so on
)

type cssToken struct {
	kind       int
	start, end int
}

// cssTokenizer is the tokenizer. Offsets are into the text as written:
// instead of replacing CR LF, CR and FF with LF beforehand, as the spec's
// preprocessing does, it counts each of them as one newline.
type cssTokenizer struct {
	s       string
	i       int
	problem string // the first thing that would not read as written
}

func (t *cssTokenizer) fail(msg string) {
	if t.problem == "" {
		t.problem = msg
	}
}

func (t *cssTokenizer) at(k int) int {
	if k < len(t.s) {
		return int(t.s[k])
	}
	return -1 // EOF
}

func ctNewline(c int) bool { return c == '\n' || c == '\r' || c == '\f' }

func ctWhite(c int) bool { return ctNewline(c) || c == ' ' || c == '\t' }

func ctDigit(c int) bool { return '0' <= c && c <= '9' }

func ctHexDigit(c int) bool { return ctDigit(c) || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' }

// ctNameStart: a letter, _, or a non-ASCII code point. NUL counts as one:
// the preprocessing turns it into U+FFFD.
func ctNameStart(c int) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || c == '_' || c >= 0x80 || c == 0
}

func ctName(c int) bool { return ctNameStart(c) || ctDigit(c) || c == '-' }

// newlineLen is the length of the newline at k: 2 for CR LF.
func (t *cssTokenizer) newlineLen(k int) int {
	if t.at(k) == '\r' && t.at(k+1) == '\n' {
		return 2
	}
	return 1
}

// validEscape: the two code points at k are a valid escape.
func (t *cssTokenizer) validEscape(k int) bool {
	return t.at(k) == '\\' && !ctNewline(t.at(k+1))
}

// startsIdent: the three code points at k would start an ident sequence.
func (t *cssTokenizer) startsIdent(k int) bool {
	switch c := t.at(k); {
	case c == '-':
		return ctNameStart(t.at(k+1)) || t.at(k+1) == '-' || t.validEscape(k+1)
	case ctNameStart(c):
		return true
	case c == '\\':
		return t.validEscape(k)
	}
	return false
}

// startsNumber: the three code points at k would start a number.
func (t *cssTokenizer) startsNumber(k int) bool {
	switch c := t.at(k); {
	case c == '+' || c == '-':
		return ctDigit(t.at(k+1)) || t.at(k+1) == '.' && ctDigit(t.at(k+2))
	case c == '.':
		return ctDigit(t.at(k + 1))
	}
	return ctDigit(t.at(k))
}

// escape consumes an escaped code point and returns it; t.i is just past
// the backslash.
func (t *cssTokenizer) escape() rune {
	switch c := t.at(t.i); {
	case ctHexDigit(c):
		start := t.i
		for n := 0; n < 6 && ctHexDigit(t.at(t.i)); n++ {
			t.i++
		}
		v, _ := strconv.ParseUint(t.s[start:t.i], 16, 32)
		if ctWhite(t.at(t.i)) {
			t.i += t.newlineLen(t.i)
		}
		if v == 0 || 0xd800 <= v && v <= 0xdfff || v > 0x10ffff {
			return utf8.RuneError
		}
		return rune(v)
	case c < 0:
		return utf8.RuneError
	case c == 0:
		t.i++
		return utf8.RuneError
	default:
		r, n := utf8.DecodeRuneInString(t.s[t.i:])
		t.i += n
		return r
	}
}

// identSeq consumes an ident sequence and returns its value, with its
// escapes decoded.
func (t *cssTokenizer) identSeq() string {
	var b strings.Builder
	for {
		switch {
		case ctName(t.at(t.i)):
			b.WriteByte(t.s[t.i])
			t.i++
		case t.validEscape(t.i):
			t.i++
			b.WriteRune(t.escape())
		default:
			return b.String()
		}
	}
}

func (t *cssTokenizer) number() {
	if c := t.at(t.i); c == '+' || c == '-' {
		t.i++
	}
	for ctDigit(t.at(t.i)) {
		t.i++
	}
	if t.at(t.i) == '.' && ctDigit(t.at(t.i+1)) {
		t.i++
		for ctDigit(t.at(t.i)) {
			t.i++
		}
	}
	if c := t.at(t.i); c == 'e' || c == 'E' {
		k := t.i + 1
		if s := t.at(k); s == '+' || s == '-' {
			k++
		}
		if ctDigit(t.at(k)) {
			t.i = k
			for ctDigit(t.at(t.i)) {
				t.i++
			}
		}
	}
}

func (t *cssTokenizer) numeric() int {
	t.number()
	if t.startsIdent(t.i) {
		t.identSeq()
	} else if t.at(t.i) == '%' {
		t.i++
	}
	return ctOther
}

func (t *cssTokenizer) str(quote int) int {
	t.i++
	for {
		switch c := t.at(t.i); {
		case c == quote:
			t.i++
			return ctString
		case c < 0:
			t.fail("unterminated string")
			return ctString
		case ctNewline(c):
			return ctBadString
		case c == '\\':
			t.i++
			if ctNewline(t.at(t.i)) {
				t.i += t.newlineLen(t.i)
			} else if t.at(t.i) >= 0 {
				t.escape()
			}
		default:
			t.i++
		}
	}
}

func (t *cssTokenizer) identLike() int {
	// "url" is an ASCII case-insensitive match, as the spec says; Unicode
	// folding would also match other letters.
	if name := t.identSeq(); len(name) == 3 && name[0]|0x20 == 'u' && name[1]|0x20 == 'r' && name[2]|0x20 == 'l' && t.at(t.i) == '(' {
		t.i++
		for ctWhite(t.at(t.i)) && ctWhite(t.at(t.i+1)) {
			t.i++
		}
		q := t.at(t.i)
		if q == '"' || q == '\'' || ctWhite(q) && (t.at(t.i+1) == '"' || t.at(t.i+1) == '\'') {
			return ctFunction
		}
		return t.url()
	}
	if t.at(t.i) == '(' {
		t.i++
		return ctFunction
	}
	return ctOther
}

func (t *cssTokenizer) url() int {
	for ctWhite(t.at(t.i)) {
		t.i++
	}
	for {
		switch c := t.at(t.i); {
		case c == ')':
			t.i++
			return ctURL
		case c < 0:
			t.fail("unterminated url(")
			return ctURL
		case ctWhite(c):
			for ctWhite(t.at(t.i)) {
				t.i++
			}
			switch t.at(t.i) {
			case ')':
				t.i++
				return ctURL
			case -1:
				t.fail("unterminated url(")
				return ctURL
			}
			t.badURL()
			return ctBadURL
		case c == '"' || c == '\'' || c == '(' || 1 <= c && c <= 0x08 || c == 0x0b || 0x0e <= c && c <= 0x1f || c == 0x7f:
			// Not NUL: the preprocessing made it U+FFFD.
			t.badURL()
			return ctBadURL
		case c == '\\':
			if !t.validEscape(t.i) {
				t.badURL()
				return ctBadURL
			}
			t.i++
			t.escape()
		default:
			t.i++
		}
	}
}

func (t *cssTokenizer) badURL() {
	for {
		switch {
		case t.at(t.i) == ')':
			t.i++
			return
		case t.at(t.i) < 0:
			return
		case t.validEscape(t.i):
			t.i++
			t.escape()
		default:
			t.i++
		}
	}
}

// next consumes comments and then one token. It returns false at EOF.
func (t *cssTokenizer) next() (cssToken, bool) {
	for strings.HasPrefix(t.s[t.i:], "/*") {
		end := strings.Index(t.s[t.i+2:], "*/")
		if end < 0 {
			t.fail("unterminated comment")
			t.i = len(t.s)
			break
		}
		t.i += end + 4
	}
	start := t.i
	c := t.at(t.i)
	if c < 0 {
		return cssToken{}, false
	}
	kind := ctOther
	switch {
	case ctWhite(c):
		for ctWhite(t.at(t.i)) {
			t.i++
		}
		kind = ctWhitespace
	case c == '"' || c == '\'':
		kind = t.str(c)
	case c == '(':
		t.i++
		kind = ctOpenParen
	case c == ')':
		t.i++
		kind = ctCloseParen
	case c == '[':
		t.i++
		kind = ctOpenBracket
	case c == ']':
		t.i++
		kind = ctCloseBracket
	case c == '{':
		t.i++
		kind = ctOpenBrace
	case c == '}':
		t.i++
		kind = ctCloseBrace
	case c == ';':
		t.i++
		kind = ctSemicolon
	case c == '#':
		t.i++
		if ctName(t.at(t.i)) || t.validEscape(t.i) {
			t.identSeq()
		}
	case c == '+' || c == '.':
		if t.startsNumber(t.i) {
			t.numeric()
		} else {
			t.i++
		}
	case c == '-':
		switch {
		case t.startsNumber(t.i):
			t.numeric()
		case t.at(t.i+1) == '-' && t.at(t.i+2) == '>':
			t.i += 3
			kind = ctCDC
		case t.startsIdent(t.i):
			kind = t.identLike()
		default:
			t.i++
		}
	case c == '<':
		t.i++
		if strings.HasPrefix(t.s[t.i:], "!--") {
			t.i += 3
			kind = ctCDO
		}
	case c == '@':
		t.i++
		if t.startsIdent(t.i) {
			t.identSeq()
		}
	case c == '\\':
		if t.validEscape(t.i) {
			kind = t.identLike()
		} else {
			t.i++
		}
	case ctDigit(c):
		t.numeric()
	case ctNameStart(c):
		kind = t.identLike()
	default:
		_, n := utf8.DecodeRuneInString(t.s[t.i:])
		t.i += n
	}
	return cssToken{kind, start, t.i}, true
}

// readCSS reads the CSS in s as a browser would: its rules and their
// blocks, and the declarations in the blocks. It also returns the first
// thing in s that a browser would not read as it is written: a string,
// url( or comment that is not closed, a bad string or URL, a bracket that
// is closed by the wrong kind or not at all, <!-- or --> where a rule
// starts (a browser skips them there), or a } outside every block.
func readCSS(s string) ([]cssItem, string) {
	t := &cssTokenizer{s: s}
	var toks []cssToken
	for {
		tok, ok := t.next()
		if !ok {
			break
		}
		switch tok.kind {
		case ctBadString:
			t.fail("a string that ends at a newline")
		case ctBadURL:
			t.fail("a url( that is not a URL")
		}
		toks = append(toks, tok)
	}
	r := &cssReader{s: s, toks: toks, problem: t.problem}
	items := r.items(false)
	return items, r.problem
}

type cssReader struct {
	s       string
	toks    []cssToken
	k       int
	problem string
}

func (r *cssReader) fail(msg string) {
	if r.problem == "" {
		r.problem = msg
	}
}

// items reads rules and declarations up to the } that closes the block,
// or to the end at the top level.
func (r *cssReader) items(nested bool) []cssItem {
	var out []cssItem
	for r.k < len(r.toks) {
		tok := r.toks[r.k]
		switch tok.kind {
		case ctWhitespace, ctSemicolon:
			r.k++
			continue
		case ctCloseBrace:
			r.k++
			if nested {
				return out
			}
			r.fail("a } outside every block")
			continue
		case ctCDO, ctCDC:
			if !nested {
				r.fail("<!-- or --> where a rule starts")
			}
		}
		start, end := tok.start, tok.start
	item:
		for r.k < len(r.toks) {
			tok := r.toks[r.k]
			switch tok.kind {
			case ctSemicolon, ctCloseBrace:
				break item
			case ctOpenBrace:
				r.k++
				out = append(out, cssItem{text: r.s[start:end], rule: true, items: r.items(true)})
				start = -1
				break item
			case ctWhitespace:
				r.k++
			default:
				r.value()
				end = r.toks[r.k-1].end
			}
		}
		if start >= 0 {
			out = append(out, cssItem{text: r.s[start:end]})
		}
	}
	if nested {
		r.fail("a { that is not closed")
	}
	return out
}

// value consumes a component value: a token, or a ( [ or function block
// with everything up to its closing bracket.
func (r *cssReader) value() {
	var want []int
	for r.k < len(r.toks) {
		tok := r.toks[r.k]
		r.k++
		switch tok.kind {
		case ctOpenParen, ctFunction:
			want = append(want, ctCloseParen)
		case ctOpenBracket:
			want = append(want, ctCloseBracket)
		case ctOpenBrace:
			want = append(want, ctCloseBrace)
		case ctCloseParen, ctCloseBracket, ctCloseBrace:
			if len(want) == 0 || want[len(want)-1] != tok.kind {
				r.fail("a bracket closed by the wrong kind")
				if len(want) == 0 {
					return
				}
				continue
			}
			want = want[:len(want)-1]
		}
		if len(want) == 0 {
			return
		}
	}
	if len(want) > 0 {
		r.fail("a bracket that is not closed")
	}
}
