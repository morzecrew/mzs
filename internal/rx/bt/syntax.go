package bt

import (
	"fmt"
	"strconv"
	"strings"
)

// The pattern tree. It is a plain AST: the compiler in compile.go lowers it to the
// instruction list the matcher runs, and firstSet/widths read it directly.
type node interface{ isNode() }

type nEmpty struct{}
type nChar struct {
	r    rune
	fold bool
}
type nClass struct{ cls *charClass }
type nCat struct{ subs []node }
type nAlt struct{ alts []node }
type nGroup struct { // idx == 0 means non-capturing
	idx int
	sub node
}
type nRepeat struct {
	sub      node
	min, max int // max == -1 is unbounded
	lazy     bool
	poss     bool
}
type nAssert struct{ kind uint8 }
type nLook struct {
	neg    bool
	behind bool
	sub    node
}
type nAtomic struct{ sub node }
type nBackref struct {
	idx  int
	fold bool
}

func (*nEmpty) isNode()   {}
func (*nChar) isNode()    {}
func (*nClass) isNode()   {}
func (*nCat) isNode()     {}
func (*nAlt) isNode()     {}
func (*nGroup) isNode()   {}
func (*nRepeat) isNode()  {}
func (*nAssert) isNode()  {}
func (*nLook) isNode()    {}
func (*nAtomic) isNode()  {}
func (*nBackref) isNode() {}

const (
	assertBOL      uint8 = iota // ^ — a line anchor, always (Ruby semantics)
	assertEOL                   // $
	assertBOS                   // \A
	assertEOS                   // \z
	assertEOSNL                 // \Z
	assertWordB                 // \b — Unicode-aware, the reason this engine exists
	assertNotWordB              // \B
)

// flagSet is the inline flag state; it is scoped to the enclosing group, so
// `(?i:x)y` leaves y case-sensitive.
type flagSet struct {
	i      bool
	dotAll bool
	x      bool
}

// parseFlags accepts the Ruby flag letters. `s` is Ruby's alias for dot-matches-
// newline and `u` is inert; internal/rx has usually canonicalised them already, but
// bt must not depend on that.
func parseFlags(flags string) (flagSet, error) {
	var fl flagSet
	for _, r := range flags {
		switch r {
		case 'i':
			fl.i = true
		case 'm', 's':
			fl.dotAll = true
		case 'x':
			fl.x = true
		case 'u':
		default:
			return fl, fmt.Errorf("unknown regex flag %q", string(r))
		}
	}
	return fl, nil
}

// maxParseDepth bounds group nesting so a pathological pattern cannot exhaust the Go
// stack; the interpreter must never crash on untrusted input.
const maxParseDepth = 200

// maxRepeatCount bounds `{n,m}`, which the compiler expands into copies.
const maxRepeatCount = 10000

type parser struct {
	src    []rune
	pos    int
	fl     flagSet
	ngrp   int
	names  []string
	depth  int
	maxref int // highest \1..\9 seen; validated once the group count is final
}

func (p *parser) errf(format string, a ...any) error {
	return fmt.Errorf(format, a...)
}

func (p *parser) eof() bool { return p.pos >= len(p.src) }

func (p *parser) peek() rune {
	if p.eof() {
		return -1
	}
	return p.src[p.pos]
}

func (p *parser) at(off int) rune {
	if p.pos+off >= len(p.src) {
		return -1
	}
	return p.src[p.pos+off]
}

// skipX drops the whitespace and `#` comments that the `x` flag makes insignificant.
// It runs between atoms and before quantifiers, so `a  *` is `a*` in extended mode.
func (p *parser) skipX() {
	if !p.fl.x {
		return
	}
	for !p.eof() {
		switch p.src[p.pos] {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			p.pos++
		case '#':
			for !p.eof() && p.src[p.pos] != '\n' {
				p.pos++
			}
		default:
			return
		}
	}
}

func (p *parser) parse() (node, error) {
	n, err := p.parseAlt()
	if err != nil {
		return nil, err
	}
	if !p.eof() {
		return nil, p.errf("unmatched ')' at offset %d", p.pos)
	}
	return n, nil
}

func (p *parser) parseAlt() (node, error) {
	first, err := p.parseCat()
	if err != nil {
		return nil, err
	}
	if p.peek() != '|' {
		return first, nil
	}
	alts := []node{first}
	for p.peek() == '|' {
		p.pos++
		n, err := p.parseCat()
		if err != nil {
			return nil, err
		}
		alts = append(alts, n)
	}
	return &nAlt{alts: alts}, nil
}

func (p *parser) parseCat() (node, error) {
	var subs []node
	for {
		p.skipX()
		if p.eof() || p.peek() == '|' || p.peek() == ')' {
			break
		}
		n, err := p.parseRepeat()
		if err != nil {
			return nil, err
		}
		if n != nil {
			if _, empty := n.(*nEmpty); !empty {
				subs = append(subs, n)
			}
		}
	}
	switch len(subs) {
	case 0:
		return &nEmpty{}, nil
	case 1:
		return subs[0], nil
	}
	return &nCat{subs: subs}, nil
}

func (p *parser) parseRepeat() (node, error) {
	atom, err := p.parseAtom()
	if err != nil {
		return nil, err
	}
	for {
		p.skipX()
		min, max, ok, err := p.parseQuant()
		if err != nil {
			return nil, err
		}
		if !ok {
			return atom, nil
		}
		if !quantifiable(atom) {
			return nil, p.errf("target of repeat operator is not specified")
		}
		rep := &nRepeat{sub: atom, min: min, max: max}
		switch p.peek() {
		case '?':
			p.pos++
			rep.lazy = true
		case '+':
			p.pos++
			rep.poss = true
		}
		atom = rep
	}
}

// quantifiable rejects a quantifier with nothing to repeat. A quantified assertion
// (`^*`) is pointless but legal in Ruby and in RE2, and the loop-progress guard makes
// it terminate, so it is allowed through.
func quantifiable(n node) bool {
	_, empty := n.(*nEmpty)
	return !empty
}

func scanInt(rs []rune, i int) (val, next int, ok bool) {
	start := i
	for i < len(rs) && rs[i] >= '0' && rs[i] <= '9' {
		if val <= maxRepeatCount {
			val = val*10 + int(rs[i]-'0')
		}
		i++
	}
	return val, i, i > start
}

// parseQuant reads `* + ? {n} {n,} {n,m} {,m}`. A `{` that does not open a valid
// bound is an ordinary literal, as in Ruby.
func (p *parser) parseQuant() (min, max int, ok bool, err error) {
	switch p.peek() {
	case '*':
		p.pos++
		return 0, -1, true, nil
	case '+':
		p.pos++
		return 1, -1, true, nil
	case '?':
		p.pos++
		return 0, 1, true, nil
	case '{':
		i := p.pos + 1
		n1, i, has1 := scanInt(p.src, i)
		if i < len(p.src) && p.src[i] == '}' && has1 {
			if n1 > maxRepeatCount {
				return 0, 0, false, p.errf("repetition count %d is too large", n1)
			}
			p.pos = i + 1
			return n1, n1, true, nil
		}
		if i < len(p.src) && p.src[i] == ',' {
			n2, j, has2 := scanInt(p.src, i+1)
			if j < len(p.src) && p.src[j] == '}' {
				if !has2 {
					n2 = -1
				}
				if n1 > maxRepeatCount || n2 > maxRepeatCount {
					return 0, 0, false, p.errf("repetition count is too large")
				}
				if n2 >= 0 && n2 < n1 {
					return 0, 0, false, p.errf("min repeat greater than max repeat")
				}
				p.pos = j + 1
				return n1, n2, true, nil
			}
		}
	}
	return 0, 0, false, nil
}

func (p *parser) parseAtom() (node, error) {
	if p.eof() {
		return &nEmpty{}, nil
	}
	c := p.src[p.pos]
	switch c {
	case '(':
		return p.parseGroup()
	case '[':
		return p.parseClass()
	case '.':
		p.pos++
		return &nClass{cls: dotClass(p.fl.dotAll)}, nil
	case '^':
		p.pos++
		return &nAssert{kind: assertBOL}, nil
	case '$':
		p.pos++
		return &nAssert{kind: assertEOL}, nil
	case '\\':
		return p.parseEscape()
	case '*', '+', '?':
		return nil, p.errf("target of repeat operator is not specified")
	case ')':
		return nil, p.errf("unmatched ')'")
	}
	p.pos++
	return &nChar{r: c, fold: p.fl.i}, nil
}

func (p *parser) parseGroup() (node, error) {
	if p.depth++; p.depth > maxParseDepth {
		return nil, p.errf("regular expression is nested too deeply")
	}
	defer func() { p.depth-- }()

	p.pos++ // '('
	saved := p.fl

	closeGroup := func(n node) (node, error) {
		if p.peek() != ')' {
			return nil, p.errf("end pattern with unmatched parenthesis")
		}
		p.pos++
		p.fl = saved
		return n, nil
	}

	if p.peek() != '?' {
		p.ngrp++
		idx := p.ngrp
		p.names = append(p.names, "")
		sub, err := p.parseAlt()
		if err != nil {
			return nil, err
		}
		return closeGroup(&nGroup{idx: idx, sub: sub})
	}

	switch p.at(1) {
	case ':':
		p.pos += 2
		sub, err := p.parseAlt()
		if err != nil {
			return nil, err
		}
		return closeGroup(&nGroup{sub: sub})

	case '=', '!':
		neg := p.at(1) == '!'
		p.pos += 2
		sub, err := p.parseAlt()
		if err != nil {
			return nil, err
		}
		return closeGroup(&nLook{neg: neg, sub: sub})

	case '>':
		p.pos += 2
		sub, err := p.parseAlt()
		if err != nil {
			return nil, err
		}
		return closeGroup(&nAtomic{sub: sub})

	case '#':
		for !p.eof() && p.src[p.pos] != ')' {
			p.pos++
		}
		if p.eof() {
			return nil, p.errf("end pattern with unmatched parenthesis")
		}
		p.pos++
		return &nEmpty{}, nil

	case '<':
		if c := p.at(2); c == '=' || c == '!' {
			neg := c == '!'
			p.pos += 3
			sub, err := p.parseAlt()
			if err != nil {
				return nil, err
			}
			return closeGroup(&nLook{neg: neg, behind: true, sub: sub})
		}
		name, err := p.scanGroupName('<', '>')
		if err != nil {
			return nil, err
		}
		p.ngrp++
		idx := p.ngrp
		p.names = append(p.names, name)
		sub, err := p.parseAlt()
		if err != nil {
			return nil, err
		}
		return closeGroup(&nGroup{idx: idx, sub: sub})

	case '\'':
		name, err := p.scanGroupName('\'', '\'')
		if err != nil {
			return nil, err
		}
		p.ngrp++
		idx := p.ngrp
		p.names = append(p.names, name)
		sub, err := p.parseAlt()
		if err != nil {
			return nil, err
		}
		return closeGroup(&nGroup{idx: idx, sub: sub})
	}

	// (?imx-imx) and (?imx-imx: … )
	fl, err := p.scanInlineFlags()
	if err != nil {
		return nil, err
	}
	switch p.peek() {
	case ')':
		// A bare flag group applies to the rest of the enclosing group, so the
		// saved flags are deliberately *not* restored here.
		p.pos++
		p.fl = fl
		return &nEmpty{}, nil
	case ':':
		p.pos++
		p.fl = fl
		sub, err := p.parseAlt()
		if err != nil {
			return nil, err
		}
		return closeGroup(&nGroup{sub: sub})
	}
	return nil, p.errf("undefined group option")
}

func (p *parser) scanGroupName(open, close rune) (string, error) {
	if p.at(1) != open {
		return "", p.errf("invalid group name")
	}
	i := p.pos + 2
	start := i
	for i < len(p.src) && p.src[i] != close {
		i++
	}
	if i >= len(p.src) || i == start {
		return "", p.errf("invalid group name")
	}
	name := string(p.src[start:i])
	p.pos = i + 1
	return name, nil
}

func (p *parser) scanInlineFlags() (flagSet, error) {
	fl := p.fl
	i := p.pos + 1
	on := true
	for i < len(p.src) {
		switch p.src[i] {
		case 'i':
			fl.i = on
		case 'm', 's':
			fl.dotAll = on
		case 'x':
			fl.x = on
		case 'u', 'a', 'd':
			// encoding options; inert here
		case '-':
			on = false
		default:
			p.pos = i
			return fl, nil
		}
		i++
	}
	p.pos = i
	return fl, p.errf("end pattern in group")
}

// parseEscape handles every backslash form outside a character class.
func (p *parser) parseEscape() (node, error) {
	if p.pos+1 >= len(p.src) {
		return nil, p.errf("trailing backslash")
	}
	c := p.src[p.pos+1]
	switch c {
	case 'b':
		p.pos += 2
		return &nAssert{kind: assertWordB}, nil
	case 'B':
		p.pos += 2
		return &nAssert{kind: assertNotWordB}, nil
	case 'A':
		p.pos += 2
		return &nAssert{kind: assertBOS}, nil
	case 'z':
		p.pos += 2
		return &nAssert{kind: assertEOS}, nil
	case 'Z':
		p.pos += 2
		return &nAssert{kind: assertEOSNL}, nil
	case 'G', 'K':
		return nil, fmt.Errorf("%w: \\%c", ErrUnsupported, c)
	case 'g':
		return nil, fmt.Errorf("%w: subexpression call \\g", ErrUnsupported)
	case 'k':
		if n := p.at(2); n == '<' || n == '\'' {
			closer := '>'
			if n == '\'' {
				closer = '\''
			}
			i := p.pos + 3
			start := i
			for i < len(p.src) && p.src[i] != closer {
				i++
			}
			if i >= len(p.src) || i == start {
				return nil, p.errf("invalid backref name")
			}
			name := string(p.src[start:i])
			p.pos = i + 1
			idx := -1
			for j, nm := range p.names {
				if nm == name && j > 0 {
					idx = j
					break
				}
			}
			if idx < 0 {
				return nil, p.errf("undefined name <%s> reference", name)
			}
			return &nBackref{idx: idx, fold: p.fl.i}, nil
		}
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		p.pos += 2
		idx := int(c - '0')
		if idx > p.maxref {
			p.maxref = idx
		}
		return &nBackref{idx: idx, fold: p.fl.i}, nil
	}
	if cls := shorthand(c, p.fl.i); cls != nil {
		p.pos += 2
		return &nClass{cls: cls}, nil
	}
	if c == 'p' || c == 'P' {
		cls, err := p.parseProp()
		if err != nil {
			return nil, err
		}
		return &nClass{cls: cls}, nil
	}
	r, err := p.parseCharEscape()
	if err != nil {
		return nil, err
	}
	return &nChar{r: r, fold: p.fl.i}, nil
}

// parseProp reads `\p{Name}` / `\P{Name}` (and the single-letter `\pL` form).
func (p *parser) parseProp() (*charClass, error) {
	neg := p.src[p.pos+1] == 'P'
	i := p.pos + 2
	var name string
	if i < len(p.src) && p.src[i] == '{' {
		j := i + 1
		for j < len(p.src) && p.src[j] != '}' {
			j++
		}
		if j >= len(p.src) {
			return nil, p.errf("invalid character property name")
		}
		name = string(p.src[i+1 : j])
		p.pos = j + 1
	} else if i < len(p.src) {
		name = string(p.src[i])
		p.pos = i + 1
	} else {
		return nil, p.errf("invalid character property name")
	}
	if strings.HasPrefix(name, "^") {
		neg = !neg
		name = name[1:]
	}
	def, ok := lookupProp(name)
	if !ok {
		return nil, p.errf("invalid character property name {%s}", name)
	}
	c := &charClass{neg: neg, fold: p.fl.i && !neg}
	c.addProp(def, false)
	return c.normalize(), nil
}

// parseCharEscape reads the escapes that denote a single rune. An unknown escape is
// the literal character, which is what makes `\/`, `\.` and `\-` work.
func (p *parser) parseCharEscape() (rune, error) {
	c := p.src[p.pos+1]
	p.pos += 2
	switch c {
	case 'n':
		return '\n', nil
	case 'r':
		return '\r', nil
	case 't':
		return '\t', nil
	case 'f':
		return '\f', nil
	case 'v':
		return '\v', nil
	case 'a':
		return 7, nil
	case 'e':
		return 27, nil
	case '0':
		return 0, nil
	case 'x':
		return p.parseHex(2)
	case 'u':
		return p.parseHex(4)
	}
	return c, nil
}

func (p *parser) parseHex(n int) (rune, error) {
	if p.pos < len(p.src) && p.src[p.pos] == '{' {
		j := p.pos + 1
		for j < len(p.src) && p.src[j] != '}' {
			j++
		}
		if j >= len(p.src) {
			return 0, p.errf("invalid hex escape")
		}
		v, err := strconv.ParseUint(string(p.src[p.pos+1:j]), 16, 32)
		if err != nil || v > 0x10FFFF {
			return 0, p.errf("invalid hex escape")
		}
		p.pos = j + 1
		return rune(v), nil
	}
	end := p.pos
	for end < len(p.src) && end-p.pos < n && isHexDigit(p.src[end]) {
		end++
	}
	if end == p.pos {
		return 0, p.errf("invalid hex escape")
	}
	v, err := strconv.ParseUint(string(p.src[p.pos:end]), 16, 32)
	if err != nil {
		return 0, p.errf("invalid hex escape")
	}
	p.pos = end
	return rune(v), nil
}

func isHexDigit(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
}

// parseClass reads `[…]`, including negation, ranges, shorthands, `\p{…}` and the
// POSIX `[:alpha:]` names.
func (p *parser) parseClass() (node, error) {
	p.pos++ // '['
	c := &charClass{fold: p.fl.i}
	if p.peek() == '^' {
		c.neg = true
		c.fold = false
		p.pos++
	}
	first := true
	for {
		if p.eof() {
			return nil, p.errf("premature end of char-class")
		}
		r := p.src[p.pos]
		if r == ']' && !first {
			p.pos++
			return &nClass{cls: c.normalize()}, nil
		}
		first = false

		// [:alpha:] only inside a class, and only when it closes properly.
		if r == '[' && p.at(1) == ':' {
			if j := indexRune(p.src, p.pos+2, ":]"); j >= 0 {
				name := string(p.src[p.pos+2 : j])
				neg := strings.HasPrefix(name, "^")
				if neg {
					name = name[1:]
				}
				def, ok := lookupProp(name)
				if !ok {
					return nil, p.errf("invalid POSIX bracket type [:%s:]", name)
				}
				c.addProp(def, neg)
				p.pos = j + 2
				continue
			}
		}

		lo, isClass, err := p.classItem(c)
		if err != nil {
			return nil, err
		}
		if isClass {
			continue
		}
		// A range needs a '-' that is neither trailing nor followed by ']'.
		if p.peek() == '-' && p.at(1) != ']' && p.pos+1 < len(p.src) {
			p.pos++
			hi, isClass2, err := p.classItem(c)
			if err != nil {
				return nil, err
			}
			if isClass2 {
				// `[\d-z]`: Ruby treats the dash as a literal here.
				c.addRune(lo)
				c.addRune('-')
				continue
			}
			if hi < lo {
				return nil, p.errf("empty range in char class")
			}
			c.addRange(lo, hi)
			continue
		}
		c.addRune(lo)
	}
}

// classItem consumes one member of a character class. It returns isClass when the
// member was a shorthand or property that it already merged into dst.
func (p *parser) classItem(dst *charClass) (r rune, isClass bool, err error) {
	if p.src[p.pos] != '\\' {
		r = p.src[p.pos]
		p.pos++
		return r, false, nil
	}
	if p.pos+1 >= len(p.src) {
		return 0, false, p.errf("trailing backslash")
	}
	c := p.src[p.pos+1]
	switch c {
	case 'b':
		// \b inside a class is a backspace, not a boundary.
		p.pos += 2
		return 8, false, nil
	case 'p', 'P':
		cls, err := p.parseProp()
		if err != nil {
			return 0, false, err
		}
		if cls.neg {
			dst.negtabs = append(dst.negtabs, cls.tabs...)
			if len(cls.ranges) > 0 {
				dst.negtabs = append(dst.negtabs, rangeTable(cls.ranges))
			}
		} else {
			dst.tabs = append(dst.tabs, cls.tabs...)
			dst.addRanges(cls.ranges)
		}
		return 0, true, nil
	}
	if sh := shorthand(c, false); sh != nil {
		p.pos += 2
		if sh.neg {
			dst.negtabs = append(dst.negtabs, rangeTable(sh.ranges))
		} else {
			dst.addRanges(sh.ranges)
		}
		return 0, true, nil
	}
	r, err = p.parseCharEscape()
	return r, false, err
}

func indexRune(rs []rune, from int, sub string) int {
	s := []rune(sub)
	for i := from; i+len(s) <= len(rs); i++ {
		ok := true
		for j, r := range s {
			if rs[i+j] != r {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}
