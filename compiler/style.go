package compiler

import (
	"fmt"
	"go/constant"
	"go/types"
	"hash/fnv"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Style blocks. A component's `style { ... }` holds CSS that applies only
// to the elements its own markup creates. Each such element gets a static
// scope attribute (d-xxxxx) in its template, and every compound selector
// in the block is narrowed to it, so a rule can neither leak out of the
// component nor into the components it uses. `$name` in a value is a
// design token: a package-level Go constant of type den.Color, den.Length,
// den.Duration, den.Font or den.Token, checked at compile time and emitted
// as a CSS custom property so the app's style.css can theme it.
//
// The CSS of every block, scoped, goes into den.css, which the page links
// before the app's own style.css: the page's CSP allows no inline styles,
// and none of it needs any JavaScript at run time.

// cssAtom returns the end of the CSS string, comment or unquoted url(...)
// that starts at i, or i+1 for anything else, and a message if it is not
// terminated. As in CSS, a newline (\n, \r or \f) that is not escaped ends
// a string that is still open.
//
// Backslashes are allowed only inside strings (styler.lex), so only
// strings have escapes to read.
func cssAtom(src string, i int) (int, string) {
	switch c := src[i]; {
	case c == '"' || c == '\'':
		for j := i + 1; j < len(src); {
			switch src[j] {
			case '\\':
				j = stringEscapeEnd(src, j)
			case c:
				return j + 1, ""
			case '\n', '\r', '\f':
				return len(src), "unterminated string in style block"
			default:
				j++
			}
		}
		return len(src), "unterminated string in style block"
	case c == '/' && strings.HasPrefix(src[i:], "/*"):
		if end := strings.Index(src[i+2:], "*/"); end >= 0 {
			return i + end + 4, ""
		}
		return len(src), "unterminated comment in style block"
	case urlAt(src, i):
		j := i + 4
		for j < len(src) && isCSSSpace(src[j]) {
			j++
		}
		if j < len(src) && (src[j] == '"' || src[j] == '\'') {
			return i + 1, "" // a quoted url is a function holding a string
		}
		for ; j < len(src); j++ {
			switch src[j] {
			case '\\':
				j++
			case ')':
				return j + 1, ""
			case '\n', '\r', '\f':
				return len(src), "unterminated url( in style block"
			}
		}
		return len(src), "unterminated url( in style block"
	}
	return i + 1, ""
}

// stringEscapeEnd returns the end of the escape at src[i], a backslash in
// a CSS string, read as CSS reads it: a backslash and a newline (CR LF
// counts as one) continue the string on the next line; a backslash and up
// to six hex digits stand for a code point, and one space or newline after
// them belongs to the escape; any other character after a backslash
// stands for itself.
func stringEscapeEnd(src string, i int) int {
	j := i + 1
	switch {
	case j >= len(src):
		return j
	case strings.HasPrefix(src[j:], "\r\n"):
		return j + 2
	case isHex(src[j]):
		for k := 0; k < 6 && j < len(src) && isHex(src[j]); k++ {
			j++
		}
		if strings.HasPrefix(src[j:], "\r\n") {
			return j + 2
		}
		if j < len(src) && isCSSSpace(src[j]) {
			j++
		}
		return j
	}
	return j + 1
}

// urlAt reports whether a url( that CSS reads as a URL starts at i: the
// u must begin an identifier, not continue one (_url( and -url( are
// functions), and # or @ before it would make it part of a hash or an
// at-keyword.
func urlAt(src string, i int) bool {
	if len(src) < i+4 || !strings.EqualFold(src[i:i+4], "url(") {
		return false
	}
	return i == 0 || !cssNameChar(src[i-1]) && src[i-1] != '#' && src[i-1] != '@'
}

func isCSSSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }

func cssNameStart(c byte) bool { return isLetter(c) || c == '-' || c >= 0x80 }

func cssNameChar(c byte) bool { return cssNameStart(c) || isDigit(c) }

// tokenTypes are the design token types of the built-in package den.
var tokenTypes = []string{"Color", "Length", "Duration", "Font", "Token"}

// tokenKind returns the name of t's design token type, or "".
func tokenKind(t types.Type) string {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil || n.Obj().Pkg().Path() != "den" || !slices.Contains(tokenTypes, n.Obj().Name()) {
		return ""
	}
	return n.Obj().Name()
}

// scopeNames gives each component with a style block its scope attribute:
// d- and five base-36 digits of an FNV hash of its name, so the name stays
// the same from one build to the next. Components are taken in name order,
// and one whose name is already taken hashes its name again with a count.
func scopeNames(comps []*Component) map[*Component]string {
	sorted := slices.Clone(comps)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	taken := map[string]bool{}
	out := map[*Component]string{}
	for _, c := range sorted {
		for n := 0; ; n++ {
			key := c.Name
			if n > 0 {
				key += "#" + strconv.Itoa(n)
			}
			h := fnv.New32a()
			h.Write([]byte(key))
			s := strconv.FormatUint(uint64(h.Sum32()%(36*36*36*36*36)), 36)
			name := "d-" + strings.Repeat("0", 5-len(s)) + s
			if !taken[name] {
				taken[name] = true
				out[c] = name
				break
			}
		}
	}
	return out
}

// styles checks every design token and compiles every style block. It
// returns each styled component's scope attribute and the CSS: a :root
// rule giving the tokens the blocks use, then each component's rules in
// component order. The CSS is empty when no component has a style block.
func styles(ck *checked) (map[*Component]string, string, ErrorList) {
	var errs ErrorList
	for id, obj := range ck.info.Defs {
		if c, ok := obj.(*types.Const); ok && tokenKind(c.Type()) != "" {
			if msg := checkToken(tokenKind(c.Type()), constant.StringVal(c.Val())); msg != "" {
				p := ck.fset.Position(id.Pos())
				errs = append(errs, &Error{File: p.Filename, Pos: Pos{p.Line, p.Column}, Msg: fmt.Sprintf("design token %s: %s", c.Name(), msg)})
			}
		}
	}
	var styled []*Component
	for _, cc := range ck.comps {
		if cc.c.Style != nil {
			styled = append(styled, cc.c)
		}
	}
	scopes := scopeNames(styled)
	vars := map[string]*types.Const{}
	var body strings.Builder
	for _, c := range styled {
		s := &styler{ck: ck, c: c, src: c.Style.Src, attr: scopes[c], vars: vars, errs: &errs}
		body.WriteString(s.css())
	}
	if len(errs) > 0 {
		return nil, "", errs
	}
	var root string
	if len(vars) > 0 {
		names := make([]string, 0, len(vars))
		for n := range vars {
			names = append(names, n)
		}
		sort.Strings(names)
		decl := func(n string) string { return "--" + n + ":" + strings.TrimSpace(constant.StringVal(vars[n].Val())) }
		rule := cssRule{prelude: ":root"}
		for _, n := range names {
			rule.decls = append(rule.decls, decl(n))
		}
		root = cssText([]cssRule{rule})
		if !readsAsWritten(root, []cssRule{rule}) {
			// checkToken should have made this impossible.
			bad := vars[names[0]]
			for _, n := range names {
				one := []cssRule{{prelude: ":root", decls: []string{decl(n)}}}
				if !readsAsWritten(cssText(one), one) {
					bad = vars[n]
					break
				}
			}
			p := ck.fset.Position(bad.Pos())
			return nil, "", ErrorList{{File: p.Filename, Pos: Pos{p.Line, p.Column},
				Msg: fmt.Sprintf("internal error: generated CSS for design token %s does not parse as written; please report", bad.Name())}}
		}
	}
	return scopes, root + body.String(), nil
}

// styler compiles one component's style block.
type styler struct {
	ck   *checked
	c    *Component
	src  string
	attr string                  // the scope attribute
	kf   map[string]bool         // the block's @keyframes names
	vars map[string]*types.Const // the custom properties tokens use, and their constants
	errs *ErrorList
}

// cssRule is a rule as den writes it: a selector or at-rule prelude, and
// a block of declarations, or of rules for @media and the like.
type cssRule struct {
	prelude string
	decls   []string
	rules   []cssRule
}

func (r cssRule) write(b *strings.Builder) {
	b.WriteString(r.prelude)
	b.WriteByte('{')
	b.WriteString(strings.Join(r.decls, ";"))
	for _, c := range r.rules {
		c.write(b)
	}
	b.WriteByte('}')
}

// cssText writes rules one per line.
func cssText(rules []cssRule) string {
	var b strings.Builder
	for _, r := range rules {
		r.write(&b)
		b.WriteByte('\n')
	}
	return b.String()
}

// readsAsWritten reports whether a browser reads css, the text of rules,
// as exactly those rules: each with the same prelude, and the same
// declarations or rules in its block (see readCSS).
func readsAsWritten(css string, rules []cssRule) bool {
	items, problem := readCSS(css)
	return problem == "" && sameRules(rules, items)
}

func sameRules(rules []cssRule, items []cssItem) bool {
	if len(rules) != len(items) {
		return false
	}
	for i, r := range rules {
		it := items[i]
		if !it.rule || it.text != r.prelude || len(it.items) != len(r.decls)+len(r.rules) {
			return false
		}
		for j, d := range r.decls {
			if it.items[j].rule || it.items[j].text != d {
				return false
			}
		}
		if !sameRules(r.rules, it.items[len(r.decls):]) {
			return false
		}
	}
	return true
}

// css compiles the block and returns its CSS, one top-level rule per line,
// or "" when it has errors. Before the CSS is returned it is read again as
// a browser reads it (readCSS), and if that finds anything but the rules
// den meant to write, it is an error: den's own scanner got the block
// wrong, and the rules would have broken the CSS that follows them.
func (s *styler) css() string {
	errs := len(*s.errs)
	rules := s.sheet()
	if len(*s.errs) > errs {
		return ""
	}
	css := cssText(rules)
	if !readsAsWritten(css, rules) {
		*s.errs = append(*s.errs, &Error{File: s.c.File, Pos: s.c.Style.Pos,
			Msg: fmt.Sprintf("internal error: generated CSS for %s does not parse as written; please report", s.c.Name)})
		return ""
	}
	return css
}

// pos returns the position in the .den file of src[off].
func (s *styler) pos(off int) Pos {
	base := s.c.Style.Pos
	nl := strings.LastIndexByte(s.src[:off], '\n')
	if nl < 0 {
		return Pos{base.Line, base.Col + off}
	}
	return Pos{base.Line + strings.Count(s.src[:off], "\n"), off - nl}
}

func (s *styler) errAt(off int, format string, args ...any) {
	*s.errs = append(*s.errs, &Error{File: s.c.File, Pos: s.pos(off), Msg: fmt.Sprintf(format, args...)})
}

const backslashMsg = "backslash escapes are only allowed inside strings in style blocks"

// sheet compiles the block's rules, or returns nil when it has errors.
func (s *styler) sheet() []cssRule {
	errs := len(*s.errs)
	s.lex()
	if len(*s.errs) > errs {
		return nil
	}
	s.kf = map[string]bool{}
	s.keyframes(0, len(s.src))
	return s.rules(0, len(s.src))
}

// lex checks the block for what the rest of the styler, and the CSS it
// writes, rely on. Escapes are allowed only inside strings: elsewhere a
// backslash changes where a selector, value or bracket ends in ways that
// are easy to get wrong, and nothing needs one. Control characters other
// than whitespace are not allowed outside comments. Every ( [ and { is
// closed, and by its own kind of bracket, as a browser pairs them; one
// that is not would take the CSS after it into its block. And there is no
// code: old browsers ran expression(...) and javascript: URLs. Strings
// are text and may say either, unless they are URLs, which checkURL checks.
func (s *styler) lex() {
	lower := strings.ToLower(s.src)
	var open []int // the ( [ and { not yet closed
	for i := 0; i < len(s.src); {
		next, _ := cssAtom(s.src, i)
		c := s.src[i]
		if !strings.HasPrefix(s.src[i:], "/*") {
			for j := i; j < next; j++ {
				for _, bad := range codeInCSS {
					if c != '"' && c != '\'' && strings.HasPrefix(lower[j:], bad) {
						s.errAt(j, "%q is not allowed in a style block", bad)
					}
				}
				if b := s.src[j]; b < 0x20 && !isCSSSpace(b) || b == 0x7f {
					s.errAt(j, "a style block cannot contain the control character U+%04X", b)
				}
			}
			if k := strings.IndexByte(s.src[i:next], '\\'); k >= 0 && c != '"' && c != '\'' {
				s.errAt(i+k, backslashMsg)
			}
			// CSS reads <!-- as one token, so -- after it starts nothing:
			// in <!--url(x), url( would be a URL that den read as a name.
			if strings.HasPrefix(s.src[i:], "<!--") {
				s.errAt(i, "<!-- is not allowed in a style block")
			}
		}
		switch c {
		case '(', '[', '{':
			open = append(open, i)
		case ')', ']', '}':
			want := "([{"[strings.IndexByte(")]}", c)]
			k := len(open) - 1
			for k >= 0 && s.src[open[k]] != want {
				k--
			}
			switch {
			case k < 0:
				s.errAt(i, "unexpected %c", c)
			case k < len(open)-1:
				s.errAt(open[len(open)-1], "unclosed %c", s.src[open[len(open)-1]])
				open = open[:k]
			default:
				open = open[:k]
			}
		}
		i = next
	}
	if len(open) > 0 {
		s.errAt(open[len(open)-1], "unclosed %c", s.src[open[len(open)-1]])
	}
}

// skipSpace moves past whitespace and comments.
func (s *styler) skipSpace(i, end int) int {
	for i < end {
		switch {
		case isCSSSpace(s.src[i]):
			i++
		case strings.HasPrefix(s.src[i:end], "/*"):
			i, _ = cssAtom(s.src, i)
		default:
			return i
		}
	}
	return i
}

// find returns the offset of the first byte in stops in src[i:end] that is
// outside strings, comments, parentheses and brackets, or end.
func (s *styler) find(i, end int, stops string) int {
	depth := 0
	for i < end {
		c := s.src[i]
		switch {
		case depth == 0 && strings.IndexByte(stops, c) >= 0:
			return i
		case c == '(' || c == '[':
			depth++
		case (c == ')' || c == ']') && depth > 0:
			depth--
		}
		i, _ = cssAtom(s.src, i)
	}
	return end
}

// preludeEnd returns the offset of the '{' or ';' that ends the selector
// or at-rule prelude at i, or end. A '{' ends it even inside parentheses,
// where it has no business, so an unclosed one is reported by the
// selector rather than taking the rest of the block with it.
func (s *styler) preludeEnd(i, end int) int {
	for i < end && s.src[i] != '{' && s.src[i] != ';' {
		i, _ = cssAtom(s.src, i)
	}
	return min(i, end)
}

// matchBrace returns the offset of the '}' that closes the '{' at open.
// The parser checked that the braces of the block balance.
func (s *styler) matchBrace(open, end int) int {
	depth := 0
	for i := open; i < end; {
		switch s.src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
		i, _ = cssAtom(s.src, i)
	}
	return end
}

// atName reads the name of the at-rule at i: its end, and the name in
// lower case.
func (s *styler) atName(i, end int) (int, string) {
	j := i + 1
	for j < end && cssNameChar(s.src[j]) {
		j++
	}
	return j, strings.ToLower(s.src[i+1 : j])
}

// keyframes collects the names of the block's @keyframes, so animation
// values can name them before or after they are declared.
func (s *styler) keyframes(i, end int) {
	for i = s.skipSpace(i, end); i < end; i = s.skipSpace(i, end) {
		stop := s.preludeEnd(i, end)
		if stop >= end || s.src[stop] == ';' {
			i = stop + 1
			continue
		}
		close := s.matchBrace(stop, end)
		if s.src[i] == '@' {
			switch nameEnd, name := s.atName(i, end); name {
			case "keyframes":
				if name := strings.TrimSpace(s.src[nameEnd:stop]); !animationKeywords[strings.ToLower(name)] {
					s.kf[name] = true
				}
			case "media", "supports", "container":
				s.keyframes(stop+1, close)
			}
		}
		i = close + 1
	}
}

// rules compiles the rules in src[i:end]: the whole block, or the body of
// an @media, @supports or @container rule.
func (s *styler) rules(i, end int) []cssRule {
	var out []cssRule
	for i = s.skipSpace(i, end); i < end; i = s.skipSpace(i, end) {
		start := i
		stop := s.preludeEnd(i, end)
		if stop >= end || s.src[stop] == ';' {
			if s.src[start] == '@' {
				_, name := s.atName(start, end)
				s.errAt(start, "@%s is not allowed in a style block; put it in style.css", name)
			} else {
				s.errAt(start, "expected { after the selector")
			}
			i = stop + 1
			continue
		}
		close := s.matchBrace(stop, end)
		i = close + 1
		if s.src[start] == '@' {
			if r, ok := s.atRule(start, stop, close); ok {
				out = append(out, r)
			}
			continue
		}
		sel, ok := s.selectorList(start, stop)
		if decls := s.declarations(stop+1, close); ok && len(decls) > 0 {
			out = append(out, cssRule{prelude: sel, decls: decls})
		}
	}
	return out
}

// atRule compiles the at-rule at start whose block is src[open:close+1].
func (s *styler) atRule(start, open, close int) (cssRule, bool) {
	nameEnd, name := s.atName(start, open)
	switch name {
	case "media", "supports", "container":
		prelude := s.value("@"+name, nameEnd, open)
		inner := s.rules(open+1, close)
		if len(inner) == 0 {
			return cssRule{}, false
		}
		if prelude != "" {
			prelude = " " + prelude
		}
		return cssRule{prelude: "@" + name + prelude, rules: inner}, true
	case "keyframes":
		kname := strings.TrimSpace(s.src[nameEnd:open])
		if !cssIdent(kname) {
			s.errAt(start, "@keyframes needs a name, like @keyframes fade-in")
			return cssRule{}, false
		}
		if animationKeywords[strings.ToLower(kname)] {
			s.errAt(s.skipSpace(nameEnd, open), "%q cannot name @keyframes: animation reads it as a keyword; choose another name", kname)
			return cssRule{}, false
		}
		var frames []cssRule
		for i := s.skipSpace(open+1, close); i < close; i = s.skipSpace(i, close) {
			stop := s.preludeEnd(i, close)
			if stop >= close || s.src[stop] == ';' {
				s.errAt(i, "expected { after the keyframe selector")
				i = stop + 1
				continue
			}
			end := s.matchBrace(stop, close)
			var sels []string
			for _, part := range s.splitTop(i, stop) {
				sel := strings.ToLower(strings.TrimSpace(s.src[part[0]:part[1]]))
				if sel != "from" && sel != "to" && !isPercentage(sel) {
					s.errAt(s.skipSpace(part[0], part[1]), "keyframe selectors are from, to or a percentage, not %q", sel)
				}
				sels = append(sels, sel)
			}
			frames = append(frames, cssRule{prelude: strings.Join(sels, ","), decls: s.declarations(stop+1, end)})
			i = end + 1
		}
		return cssRule{prelude: "@keyframes " + kname + "-" + s.attr, rules: frames}, true
	}
	s.errAt(start, "@%s is not allowed in a style block; put it in style.css", name)
	return cssRule{}, false
}

// animationKeywords are the words the animation properties read as
// keywords, never as a @keyframes name: a block's references to its own
// keyframes are renamed, so a keyframes name among them would rename the
// keyword too. Besides the CSS-wide keywords and none, they are the
// keywords of the properties the animation shorthand sets: duration
// (auto), easing, iteration count, direction, fill mode, play state and
// timeline (auto, none), and of animation-composition, which browsers may
// add to the shorthand.
var animationKeywords = map[string]bool{
	"initial": true, "inherit": true, "unset": true, "revert": true, "revert-layer": true, "default": true, "none": true, "auto": true,
	"ease": true, "ease-in": true, "ease-out": true, "ease-in-out": true, "linear": true, "step-start": true, "step-end": true,
	"infinite": true, "normal": true, "reverse": true, "alternate": true, "alternate-reverse": true,
	"forwards": true, "backwards": true, "both": true, "running": true, "paused": true,
	"replace": true, "add": true, "accumulate": true,
}

// cssIdent reports whether name is a plain CSS identifier.
func cssIdent(name string) bool {
	if name == "" || !(cssNameStart(name[0]) || name[0] == '_') || strings.HasPrefix(name, "--") || (name[0] == '-' && len(name) > 1 && isDigit(name[1])) {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !cssNameChar(name[i]) && name[i] != '_' {
			return false
		}
	}
	return name != "-"
}

func isPercentage(s string) bool {
	n, ok := strings.CutSuffix(s, "%")
	return ok && isNumber(n)
}

// isNumber reports whether s is a CSS number without a sign, such as 1,
// 0.5, .5, 100 or 1e3.
func isNumber(s string) bool { return s != "" && numberEnd(s) == len(s) }

// numberEnd returns the length of the CSS number without a sign at the
// start of s: digits with an optional fraction (1, 0.5, .5) and an
// optional exponent (1e3, 1.5E-1), or 0 if s does not start with one.
func numberEnd(s string) int {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	digits := i > 0
	if i+1 < len(s) && s[i] == '.' && isDigit(s[i+1]) {
		for i++; i < len(s) && isDigit(s[i]); i++ {
		}
		digits = true
	}
	if !digits {
		return 0
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && isDigit(s[j]) {
			for i = j; i < len(s) && isDigit(s[i]); i++ {
			}
		}
	}
	return i
}

// splitTop splits src[i:end] at the commas outside parentheses, brackets
// and strings.
func (s *styler) splitTop(i, end int) [][2]int {
	var out [][2]int
	for {
		comma := s.find(i, end, ",")
		out = append(out, [2]int{i, comma})
		if comma >= end {
			return out
		}
		i = comma + 1
	}
}

// declarations compiles the declarations in src[i:end].
func (s *styler) declarations(i, end int) []string {
	var out []string
	for i < end {
		stop := s.find(i, end, ";{")
		if stop < end && s.src[stop] == '{' {
			if j := s.skipSpace(i, stop); strings.HasPrefix(s.src[j:stop], "--") && s.find(j, stop, ":") < stop {
				s.errAt(stop, "{} blocks in custom property values are not supported")
			} else {
				s.errAt(stop, "nested rules are not supported in a style block; write each rule with its full selector")
			}
			break
		}
		if d := s.declaration(i, stop); d != "" {
			out = append(out, d)
		}
		i = stop + 1
	}
	return out
}

// declaration compiles `property: value` in src[i:end].
func (s *styler) declaration(i, end int) string {
	i = s.skipSpace(i, end)
	if i >= end {
		return ""
	}
	colon := s.find(i, end, ":")
	if colon >= end {
		s.errAt(i, "expected property: value")
		return ""
	}
	name := strings.TrimRight(s.src[i:colon], " \t\r\n\f")
	custom := strings.HasPrefix(name, "--") && len(name) > 2
	if !custom && !cssIdent(name) {
		s.errAt(i, "%q is not a CSS property name", name)
		return ""
	}
	if custom {
		for j := 2; j < len(name); j++ {
			if !cssNameChar(name[j]) && name[j] != '_' {
				s.errAt(i, "%q is not a CSS property name", name)
				return ""
			}
		}
	} else {
		name = strings.ToLower(name)
	}
	if name == "behavior" || name == "-moz-binding" {
		s.errAt(i, "%s is not allowed in a style block: it can run code", name)
		return ""
	}
	errs := len(*s.errs)
	v := s.value(name, colon+1, end)
	if v == "" && !custom && len(*s.errs) == errs {
		s.errAt(i, "%s has no value", name)
		return ""
	}
	return name + ":" + v
}

// value compiles the value of property prop in src[i:end], or the
// condition of an at-rule when prop is "@media" and the like: comments are
// dropped, whitespace collapsed, tokens replaced by custom properties and
// URLs checked. In animation values, the names of the block's @keyframes
// become their scoped names.
func (s *styler) value(prop string, i, end int) string {
	var b strings.Builder
	space := false
	write := func(t string) {
		if t == "" {
			return
		}
		if space && b.Len() > 0 {
			last := b.String()[b.Len()-1]
			if last != ',' && last != '(' && t[0] != ',' && t[0] != ')' {
				b.WriteByte(' ')
			}
		}
		space = false
		b.WriteString(t)
	}
	anim := strings.TrimPrefix(prop, "-webkit-") == "animation" || strings.TrimPrefix(prop, "-webkit-") == "animation-name"
	// An open ( or [ would make the browser read the rest of the CSS as
	// part of this value, so they must balance.
	var open []int
	var fns []string // the function each open ( belongs to, in lower case
	defer func() {
		if len(open) > 0 {
			s.errAt(open[len(open)-1], "unclosed %c", s.src[open[len(open)-1]])
		}
	}()
	start := i
	for i < end {
		c := s.src[i]
		switch {
		case isCSSSpace(c):
			space = true
			i++
		case strings.HasPrefix(s.src[i:end], "/*"):
			space = true
			i, _ = cssAtom(s.src, i)
		case c == '"' || c == '\'':
			j, _ := cssAtom(s.src, i)
			if len(fns) > 0 && urlFuncs[fns[len(fns)-1]] {
				s.checkURL(i, s.src[i+1:j-1], fns[len(fns)-1])
			}
			write(s.src[i:j])
			i = j
		case c == '$':
			j := i + 1
			for j < end && (cssNameChar(s.src[j]) || s.src[j] == '_') {
				j++
			}
			name := s.src[i+1 : j]
			fn := slices.IndexFunc(fns, func(f string) bool { return urlFuncs[f] })
			switch {
			case name == "":
				s.errAt(i, "stray $: a design token is written $name, like $space-4")
			case strings.HasPrefix(prop, "@"):
				s.errAt(i, "design tokens cannot be used in %s conditions: CSS custom properties do not work there", prop)
			case fn >= 0:
				s.errAt(i, "design tokens cannot be used in %s(): its URLs are checked when the app builds", fns[fn])
			default:
				if v := s.token(i, name, prop); v != "" {
					write(v)
				}
			}
			i = j
		case end-i >= 4 && urlAt(s.src, i):
			write(s.url(i, end, &i))
		case cssNameStart(c) || c == '_' || c == '#' || isDigit(c) || (c == '.' && i+1 < end && isDigit(s.src[i+1])):
			j := i + 1
			for j < end && (cssNameChar(s.src[j]) || s.src[j] == '_' || (s.src[j] == '.' && !cssNameStart(c))) {
				j++
			}
			word := s.src[i:j]
			if anim && len(open) == 0 && s.kf[word] && (j >= end || s.src[j] != '(') {
				word += "-" + s.attr
			}
			write(word)
			i = j
		default:
			switch c {
			case '(', '[':
				k := i
				for k > start && c == '(' && cssNameChar(s.src[k-1]) {
					k--
				}
				open, fns = append(open, i), append(fns, strings.ToLower(s.src[k:i]))
			case ')', ']':
				want := byte('(')
				if c == ']' {
					want = '['
				}
				if len(open) == 0 || s.src[open[len(open)-1]] != want {
					s.errAt(i, "unexpected %c", c)
					open = nil
					return ""
				}
				open, fns = open[:len(open)-1], fns[:len(fns)-1]
			case '{', '}':
				s.errAt(i, "unexpected %c", c)
				open = nil
				return ""
			}
			write(string(c))
			i++
		}
	}
	return b.String()
}

// url checks the url(...) at i, sets *next past it and returns it.
func (s *styler) url(i, end int, next *int) string {
	j := i + 4
	for j < end && isCSSSpace(s.src[j]) {
		j++
	}
	var arg, out string
	if j < end && (s.src[j] == '"' || s.src[j] == '\'') {
		k, _ := cssAtom(s.src, j)
		arg, out = s.src[j+1:k-1], s.src[j:k]
		j = s.skipSpace(k, end)
		if j >= end || s.src[j] != ')' {
			s.errAt(i, "expected ) to close url(")
			*next = s.find(j, end, ")") + 1
			return ""
		}
		*next = j + 1
	} else {
		k, _ := cssAtom(s.src, i)
		arg = strings.TrimRight(s.src[j:k-1], " \t\r\n\f")
		out, *next = arg, k
		// A browser reads any of these as a broken URL, and a broken URL
		// as running to the next ), wherever that is.
		if bad := strings.IndexAny(arg, " \t\r\n\f\"'("); bad >= 0 {
			s.errAt(j+bad, "unquoted url() cannot contain spaces, quotes or (; put the URL in quotes")
			return ""
		}
	}
	if strings.Contains(arg, "$") {
		s.errAt(i, "design tokens cannot be used in url(): CSS custom properties do not work there")
	} else {
		s.checkURL(i, arg, "url")
	}
	return "url(" + out + ")"
}

// urlFuncs are the functions other than url() whose string arguments are
// URLs.
var urlFuncs = map[string]bool{"image-set": true, "-webkit-image-set": true, "src": true, "image": true}

// codeInCSS is what old browsers ran as code in CSS.
var codeInCSS = []string{"expression(", "javascript:"}

// checkURL checks the URL arg at off, given to the function fn: the same
// URLs as a static href, and data:image/ URLs, which the page's CSP
// allows as images and an image cannot run code.
func (s *styler) checkURL(off int, arg, fn string) {
	lower := strings.ToLower(arg)
	switch {
	case strings.Contains(arg, "\\"):
		s.errAt(off, "escapes are not allowed in %s()", fn)
	case slices.ContainsFunc(codeInCSS, func(bad string) bool { return strings.Contains(lower, bad) }):
		s.errAt(off, "%s() cannot contain %q or %q", fn, codeInCSS[0], codeInCSS[1])
	case strings.HasPrefix(lower, "data:image/"):
	case !safeStaticURL(arg):
		s.errAt(off, "unsafe URL in %s(): only http, https, mailto, tel, data:image/ and relative URLs are allowed", fn)
	}
}

// token resolves the design token $name at off, used in property prop,
// records its custom property and returns var(--name).
func (s *styler) token(off int, name, prop string) string {
	camel, ok := tokenCamel(name)
	if !ok {
		s.errAt(off, "$%s is not a design token name; write it in kebab case, like $space-4", name)
		return ""
	}
	want := "want a const " + strings.ToUpper(camel[:1]) + camel[1:] + " of type den.Color, den.Length, den.Duration, den.Font or den.Token"
	var found []*types.Const
	for _, n := range []string{strings.ToLower(camel[:1]) + camel[1:], strings.ToUpper(camel[:1]) + camel[1:]} {
		if c, ok := s.ck.pkg.Scope().Lookup(n).(*types.Const); ok && !slices.Contains(found, c) {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 0:
		s.errAt(off, "unknown token $%s (%s)", name, want)
		return ""
	case 2:
		s.errAt(off, "design token $%s is ambiguous: both %s and %s are constants", name, found[0].Name(), found[1].Name())
		return ""
	}
	c := found[0]
	kind := tokenKind(c.Type())
	if kind == "" {
		s.errAt(off, "$%s is %s, not a design token type (%s)", name, errorf("%s", c.Type()), want)
		return ""
	}
	if msg := tokenFits(kind, prop); msg != "" {
		s.errAt(off, "$%s is a den.%s, %s", name, kind, msg)
		return ""
	}
	v := kebab(c.Name())
	s.vars[v] = c
	return "var(--" + v + ")"
}

// tokenCamel turns a token's kebab-case name into the camel case of the
// constant it names: space-4 is space4, radius-lg is radiusLg.
func tokenCamel(name string) (string, bool) {
	parts := strings.Split(name, "-")
	var b strings.Builder
	for i, p := range parts {
		if p == "" {
			return "", false
		}
		for j := 0; j < len(p); j++ {
			if !isLetter(p[j]) && !isDigit(p[j]) || p[j] == '_' {
				return "", false
			}
		}
		if i > 0 {
			p = strings.ToUpper(p[:1]) + p[1:]
		}
		b.WriteString(p)
	}
	return b.String(), isLetter(name[0])
}

// kebab is the custom property name of the constant name: Space4 is
// space-4 and RadiusLg is radius-lg, so tokenCamel leads back to it.
func kebab(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case i > 0 && 'A' <= c && c <= 'Z':
			b.WriteByte('-')
			c += 'a' - 'A'
		case i > 0 && isDigit(c) && !isDigit(name[i-1]):
			b.WriteByte('-')
		case i == 0 && 'A' <= c && c <= 'Z':
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}

// tokenFits reports why a token of type kind cannot be used in property
// prop, or "" when it can. Only properties that take nothing else are
// checked; shorthands and unknown properties take any token.
func tokenFits(kind, prop string) string {
	if strings.HasPrefix(prop, "--") {
		return ""
	}
	for _, v := range []string{"-webkit-", "-moz-", "-ms-", "-o-"} {
		prop = strings.TrimPrefix(prop, v)
	}
	switch kind {
	case "Color":
		if lengthOnly(prop) {
			return "but " + prop + " takes a length"
		}
	case "Length":
		if colorOnly(prop) {
			return "but " + prop + " takes a color"
		}
	case "Duration":
		if !strings.HasPrefix(prop, "transition") && !strings.HasPrefix(prop, "animation") {
			return "which only transition and animation properties take"
		}
	case "Font":
		if prop != "font-family" && prop != "font" {
			return "which only font-family and font take"
		}
	}
	return ""
}

func lengthOnly(p string) bool {
	switch p {
	case "width", "height", "min-width", "max-width", "min-height", "max-height",
		"top", "right", "bottom", "left", "font-size", "line-height", "letter-spacing":
		return true
	}
	return strings.HasPrefix(p, "padding") || strings.HasPrefix(p, "margin") || strings.HasPrefix(p, "inset") ||
		p == "gap" || strings.HasSuffix(p, "-gap") ||
		strings.HasPrefix(p, "border") && strings.HasSuffix(p, "radius")
}

func colorOnly(p string) bool {
	switch p {
	case "color", "background-color", "outline-color", "fill", "stroke", "accent-color", "caret-color", "text-decoration-color":
		return true
	}
	return strings.HasPrefix(p, "border") && strings.HasSuffix(p, "-color")
}

// checkToken checks the value of a design token of type kind, which is
// written into the CSS as it is. It returns what is wrong, or "".
func checkToken(kind, v string) string {
	// Control characters include \n, \r and \f, which end a CSS string:
	// one in a quoted token would leave the string open and turn the rest
	// of the CSS into part of the :root rule.
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] == 0x7f {
			return fmt.Sprintf("a token cannot contain a control character (U+%04X)", v[i])
		}
	}
	if strings.TrimSpace(v) == "" {
		return "a token cannot be empty"
	}
	for _, bad := range []string{";", "{", "}", "<", "\\", "url(", "expression(", "/*"} {
		if strings.Contains(strings.ToLower(v), bad) {
			return fmt.Sprintf("a token cannot contain %q", bad)
		}
	}
	if strings.Contains(strings.ToLower(strings.ReplaceAll(v, " ", "")), "javascript:") {
		return `a token cannot contain "javascript:"`
	}
	if !balanced(v) {
		return "its quotes, parentheses or brackets do not balance"
	}
	lv := strings.ToLower(strings.TrimSpace(v))
	switch kind {
	case "Color":
		if !isColor(lv) {
			return fmt.Sprintf("%q is not a color (want a hex color like #fff, a function like rgb(...) or oklch(...), transparent, currentColor or a color name)", v)
		}
	case "Length":
		if !isLength(lv) {
			return fmt.Sprintf("%q is not a length (want 0, a number with a unit like 16px or 1.5rem, or calc(), min(), max() or clamp())", v)
		}
	case "Duration":
		if !isDuration(lv) {
			return fmt.Sprintf("%q is not a duration (want a number of ms or s, like 150ms)", v)
		}
	case "Font":
		if !isFont(strings.TrimSpace(v)) {
			return fmt.Sprintf("%q is not a list of font families (want names separated by commas, like system-ui, \"Segoe UI\", sans-serif)", v)
		}
	}
	return ""
}

// balanced reports whether the strings in v are closed and its
// parentheses and brackets balance.
func balanced(v string) bool {
	var stack []byte
	for i := 0; i < len(v); i++ {
		switch c := v[i]; c {
		case '"', '\'':
			j := strings.IndexByte(v[i+1:], c)
			if j < 0 {
				return false
			}
			i += j + 1
		case '(':
			stack = append(stack, ')')
		case '[':
			stack = append(stack, ']')
		case ')', ']':
			if len(stack) == 0 || stack[len(stack)-1] != c {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
	return len(stack) == 0
}

// cssFunc reports whether v is one call of a function in names, such as
// rgb(...), with nothing after its closing parenthesis.
func cssFunc(v string, names ...string) bool {
	open := strings.IndexByte(v, '(')
	return open > 0 && slices.Contains(names, v[:open]) && closesAtEnd(v[open:])
}

// closesAtEnd reports whether the '(' at the start of v is closed by the
// last byte of v.
func closesAtEnd(v string) bool {
	depth := 0
	for i := 0; i < len(v); i++ {
		switch v[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i == len(v)-1
			}
		}
	}
	return false
}

func isColor(v string) bool {
	if h, ok := strings.CutPrefix(v, "#"); ok {
		for i := 0; i < len(h); i++ {
			if !isDigit(h[i]) && !('a' <= h[i] && h[i] <= 'f') {
				return false
			}
		}
		return len(h) == 3 || len(h) == 4 || len(h) == 6 || len(h) == 8
	}
	return v == "transparent" || v == "currentcolor" || cssColors[v] ||
		cssFunc(v, "rgb", "rgba", "hsl", "hsla", "hwb", "lab", "lch", "oklab", "oklch", "color", "color-mix")
}

func isLength(v string) bool {
	if v == "0" || cssFunc(v, "calc", "min", "max", "clamp") {
		return true
	}
	n := trimSign(v)
	i := numberEnd(n)
	return i > 0 && cssUnits[n[i:]]
}

func isDuration(v string) bool {
	n := trimSign(v)
	unit := "s"
	if strings.HasSuffix(n, "ms") {
		unit = "ms"
	}
	num, ok := strings.CutSuffix(n, unit)
	return ok && isNumber(num)
}

func trimSign(v string) string {
	if v != "" && (v[0] == '+' || v[0] == '-') {
		return v[1:]
	}
	return v
}

// isFont reports whether v is a comma-separated list of font family
// names, each a quoted string or one or more identifiers.
func isFont(v string) bool {
	for _, fam := range strings.Split(v, ",") {
		fam = strings.TrimSpace(fam)
		if fam == "" {
			return false
		}
		if q := fam[0]; q == '"' || q == '\'' {
			if len(fam) < 2 || fam[len(fam)-1] != q || strings.IndexByte(fam[1:len(fam)-1], q) >= 0 {
				return false
			}
			continue
		}
		for _, word := range strings.Fields(fam) {
			if !cssIdent(word) {
				return false
			}
		}
	}
	return true
}

// cssUnits are the CSS length and percentage units.
var cssUnits = map[string]bool{}

// cssColors are the CSS named colors.
var cssColors = map[string]bool{}

func init() {
	for _, u := range strings.Fields(`% px rem em ex ch lh rlh cap ic rex rch rcap ric
		vw vh vi vb vmin vmax svw svh svi svb svmin svmax lvw lvh lvi lvb lvmin lvmax
		dvw dvh dvi dvb dvmin dvmax cqw cqh cqi cqb cqmin cqmax cm mm q in pt pc fr`) {
		cssUnits[u] = true
	}
	for _, c := range strings.Fields(`aliceblue antiquewhite aqua aquamarine azure beige bisque black
		blanchedalmond blue blueviolet brown burlywood cadetblue chartreuse chocolate coral
		cornflowerblue cornsilk crimson cyan darkblue darkcyan darkgoldenrod darkgray darkgreen
		darkgrey darkkhaki darkmagenta darkolivegreen darkorange darkorchid darkred darksalmon
		darkseagreen darkslateblue darkslategray darkslategrey darkturquoise darkviolet deeppink
		deepskyblue dimgray dimgrey dodgerblue firebrick floralwhite forestgreen fuchsia gainsboro
		ghostwhite gold goldenrod gray green greenyellow grey honeydew hotpink indianred indigo
		ivory khaki lavender lavenderblush lawngreen lemonchiffon lightblue lightcoral lightcyan
		lightgoldenrodyellow lightgray lightgreen lightgrey lightpink lightsalmon lightseagreen
		lightskyblue lightslategray lightslategrey lightsteelblue lightyellow lime limegreen linen
		magenta maroon mediumaquamarine mediumblue mediumorchid mediumpurple mediumseagreen
		mediumslateblue mediumspringgreen mediumturquoise mediumvioletred midnightblue mintcream
		mistyrose moccasin navajowhite navy oldlace olive olivedrab orange orangered orchid
		palegoldenrod palegreen paleturquoise palevioletred papayawhip peachpuff peru pink plum
		powderblue purple rebeccapurple red rosybrown royalblue saddlebrown salmon sandybrown
		seagreen seashell sienna silver skyblue slateblue slategray slategrey snow springgreen
		steelblue tan teal thistle tomato turquoise violet wheat white whitesmoke yellow
		yellowgreen`) {
		cssColors[c] = true
	}
}

// selectorList narrows each selector in src[i:end] to the component.
func (s *styler) selectorList(i, end int) (string, bool) {
	var out []string
	ok := true
	for _, part := range s.splitTop(i, end) {
		sel, good := s.selector(part[0], part[1])
		ok = ok && good
		out = append(out, sel)
	}
	return strings.Join(out, ","), ok
}

// selector narrows the complex selector in src[i:end] to the component:
// every compound selector in it gets the scope attribute, before any
// pseudo-element. Arguments of pseudo-classes such as :not() and :is()
// stay as they are written.
func (s *styler) selector(i, end int) (string, bool) {
	var comps []string
	var offs []int
	var combs []byte
	var cur strings.Builder
	curOff := 0
	comb := byte(0) // the combinator since the last compound
	ok := true
	bad := func(off int, format string, args ...any) {
		s.errAt(off, format, args...)
		ok = false
	}
	endCompound := func() {
		if cur.Len() == 0 {
			return
		}
		if len(comps) > 0 {
			combs = append(combs, comb)
		}
		comps, offs = append(comps, cur.String()), append(offs, curOff)
		cur.Reset()
		comb = 0
	}
	for j := i; j < end; {
		c := s.src[j]
		switch {
		case strings.HasPrefix(s.src[j:end], "/*"):
			// Inside a compound, a comment keeps what is on either side of
			// it apart, as it does for a browser: .a/**/b is not .ab.
			next, _ := cssAtom(s.src, j)
			if cur.Len() > 0 && next < end && !isCSSSpace(s.src[next]) && strings.IndexByte(">+~", s.src[next]) < 0 &&
				!strings.HasPrefix(s.src[next:end], "/*") {
				cur.WriteString("/**/")
			}
			j = next
			continue
		case isCSSSpace(c):
			endCompound()
			if comb == 0 {
				comb = ' '
			}
			j++
			continue
		case c == '>' || c == '+' || c == '~':
			endCompound()
			switch {
			case len(comps) == 0:
				bad(j, "a selector cannot start with %c", c)
			case comb != 0 && comb != ' ':
				bad(j, "two combinators in a row in a selector")
			}
			comb = c
			j++
			continue
		case c == '&':
			bad(j, "& (CSS nesting) is not supported in a style block; write each rule with its full selector")
		case c == '$':
			bad(j, "design tokens cannot be used in selectors")
		case c == ')' || c == ']' || c == '<':
			bad(j, "unexpected %c in selector", c)
		}
		if cur.Len() == 0 {
			curOff = j
		}
		var next int
		switch c {
		case '(', '[':
			closer := ")"
			if c == '[' {
				closer = "]"
			}
			next = s.find(j+1, end, closer) + 1
			if next > end {
				bad(j, "unclosed %c in selector", c)
				next = end
			}
			for k := j; k < next; {
				n, _ := cssAtom(s.src, k)
				switch {
				case s.src[k] == '{' || s.src[k] == '}' || s.src[k] == '<':
					bad(k, "unexpected %c in selector", s.src[k])
				case n > k+1 && urlAt(s.src, k):
					bad(k, "url() cannot be used in a selector")
				}
				k = n
			}
			cur.WriteString(collapseCSS(s.src[j:next]))
		default:
			next, _ = cssAtom(s.src, j)
			if next > j+1 && urlAt(s.src, j) {
				bad(j, "url() cannot be used in a selector")
			}
			cur.WriteString(s.src[j:next])
		}
		j = next
	}
	endCompound()
	switch {
	case len(comps) == 0:
		bad(i, "empty selector")
		return "", false
	case comb != 0 && comb != ' ':
		bad(s.skipSpace(i, end), "a selector cannot end with a combinator")
	}
	var b strings.Builder
	for k, comp := range comps {
		if k > 0 {
			if combs[k-1] != ' ' {
				b.WriteByte(combs[k-1])
			} else {
				b.WriteByte(' ')
			}
		}
		if name := pageCompound(comp); name != "" {
			bad(offs[k], "%s is outside every component, so a component's style can never match it; put page-wide styles in style.css", name)
		}
		at := pseudoElement(comp)
		b.WriteString(comp[:at] + "[" + s.attr + "]" + comp[at:])
	}
	return b.String(), ok
}

func isHex(c byte) bool { return isDigit(c) || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' }

// collapseCSS drops the comments in src and collapses its whitespace,
// leaving strings as they are. A comment between two other things is kept
// as /**/, which keeps them apart as the comment did: dropped, a/**/b
// would become ab, and //**/* would become the start of a comment.
func collapseCSS(src string) string {
	var b strings.Builder
	space := false
	for i := 0; i < len(src); {
		switch {
		case isCSSSpace(src[i]):
			space = true
			i++
		case strings.HasPrefix(src[i:], "/*"):
			next, _ := cssAtom(src, i)
			if b.Len() > 0 && !space && next < len(src) && !isCSSSpace(src[next]) && !strings.HasPrefix(src[next:], "/*") {
				b.WriteString("/**/")
			}
			i = next
		default:
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			j := i + 1
			if c := src[i]; c == '"' || c == '\'' {
				j, _ = cssAtom(src, i)
			}
			b.WriteString(src[i:j])
			i = j
		}
	}
	return b.String()
}

// topLevelBytes calls f with the offset of each byte of the compound selector
// comp that is outside parentheses, brackets and strings.
func topLevelBytes(comp string, f func(i int) bool) {
	depth := 0
	for i := 0; i < len(comp); {
		c := comp[i]
		switch {
		case c == '"' || c == '\'':
			i, _ = cssAtom(comp, i)
			continue
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case depth == 0:
			if !f(i) {
				return
			}
		}
		i++
	}
}

// pseudoElement returns where the first pseudo-element of the compound
// selector comp starts, or len(comp).
func pseudoElement(comp string) int {
	at := len(comp)
	topLevelBytes(comp, func(i int) bool {
		if comp[i] != ':' {
			return true
		}
		if strings.HasPrefix(comp[i:], "::") {
			at = i
			return false
		}
		j := i + 1
		for j < len(comp) && cssNameChar(comp[j]) {
			j++
		}
		switch strings.ToLower(comp[i+1 : j]) {
		case "before", "after", "first-line", "first-letter":
			at = i
			return false
		}
		return true
	})
	return at
}

// pageCompound returns html, body or :root when the compound selector
// comp names one of them, which no component's markup creates.
func pageCompound(comp string) string {
	j := 0
	for j < len(comp) && cssNameChar(comp[j]) {
		j++
	}
	if t := strings.ToLower(comp[:j]); t == "html" || t == "body" {
		return t
	}
	found := ""
	topLevelBytes(comp, func(i int) bool {
		if comp[i] == ':' && (i == 0 || comp[i-1] != ':') && len(comp) >= i+5 && strings.EqualFold(comp[i:i+5], ":root") &&
			(i+5 == len(comp) || !cssNameChar(comp[i+5])) {
			found = ":root"
			return false
		}
		return true
	})
	return found
}
