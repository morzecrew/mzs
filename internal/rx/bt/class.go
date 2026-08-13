package bt

import (
	"sort"
	"strings"
	"sync"
	"unicode"
)

// rrange is an inclusive rune range. Ranges are the bulk of every character class;
// Unicode properties are kept as tables instead so that `\p{Cyrillic}` costs one
// binary search rather than a few hundred merged ranges.
type rrange struct{ lo, hi rune }

// charClass is one `[…]`, one `\d`-style shorthand, or one `.`. It is built during
// parsing and never mutated afterwards, so a compiled Regexp is safe to share.
type charClass struct {
	neg     bool
	fold    bool
	ranges  []rrange
	tabs    []*unicode.RangeTable
	negtabs []*unicode.RangeTable
}

func (c *charClass) addRune(r rune) { c.ranges = append(c.ranges, rrange{r, r}) }

func (c *charClass) addRange(lo, hi rune) { c.ranges = append(c.ranges, rrange{lo, hi}) }

func (c *charClass) addRanges(rs []rrange) { c.ranges = append(c.ranges, rs...) }

func (c *charClass) addProp(p propDef, negated bool) {
	if negated {
		// `[\D]` and `[\P{L}]` are "not in this table" *inside* an otherwise positive
		// class, which is not the same as negating the whole class.
		c.negtabs = append(c.negtabs, p.tabs...)
		if len(p.ranges) > 0 {
			c.negtabs = append(c.negtabs, rangeTable(p.ranges))
		}
		return
	}
	c.tabs = append(c.tabs, p.tabs...)
	c.addRanges(p.ranges)
}

// normalize sorts and merges the range list so contains can binary-search it.
func (c *charClass) normalize() *charClass {
	if len(c.ranges) > 1 {
		sort.Slice(c.ranges, func(i, j int) bool { return c.ranges[i].lo < c.ranges[j].lo })
		out := c.ranges[:1]
		for _, r := range c.ranges[1:] {
			last := &out[len(out)-1]
			if r.lo <= last.hi+1 {
				if r.hi > last.hi {
					last.hi = r.hi
				}
				continue
			}
			out = append(out, r)
		}
		c.ranges = out
	}
	return c
}

func (c *charClass) contains(r rune) bool {
	if n := len(c.ranges); n > 0 {
		if n <= 4 {
			for _, rg := range c.ranges {
				if r >= rg.lo && r <= rg.hi {
					return true
				}
			}
		} else {
			i := sort.Search(n, func(i int) bool { return c.ranges[i].hi >= r })
			if i < n && r >= c.ranges[i].lo {
				return true
			}
		}
	}
	for _, t := range c.tabs {
		if unicode.Is(t, r) {
			return true
		}
	}
	for _, t := range c.negtabs {
		if !unicode.Is(t, r) {
			return true
		}
	}
	return false
}

// match applies the `i` flag by trying every simple case fold of r, so `[а-я]` with
// `i` accepts `Я` even though no range in the class covers it.
func (c *charClass) match(r rune) bool {
	in := c.contains(r)
	if !in && c.fold {
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if c.contains(f) {
				in = true
				break
			}
		}
	}
	return in != c.neg
}

func rangeTable(rs []rrange) *unicode.RangeTable {
	t := &unicode.RangeTable{}
	for _, r := range rs {
		if r.hi <= 0xFFFF {
			t.R16 = append(t.R16, unicode.Range16{Lo: uint16(r.lo), Hi: uint16(r.hi), Stride: 1})
			continue
		}
		lo := r.lo
		if lo <= 0xFFFF {
			t.R16 = append(t.R16, unicode.Range16{Lo: uint16(lo), Hi: 0xFFFF, Stride: 1})
			lo = 0x10000
		}
		t.R32 = append(t.R32, unicode.Range32{Lo: uint32(lo), Hi: uint32(r.hi), Stride: 1})
	}
	for i := range t.R16 {
		if t.R16[i].Hi <= unicode.MaxLatin1 {
			t.LatinOffset = i + 1
		}
	}
	return t
}

// ---------------------------------------------------------------------------
// Shorthands
// ---------------------------------------------------------------------------

// Ruby's \w, \d and \s are ASCII-only even in a UTF-8 regexp, and RE2's are too, so
// the two backends agree. \b is the exception: Onigmo's word boundary asks the
// *encoding* whether a rune is a word rune, which for UTF-8 means every letter and
// digit — that is why /\bменю\b/ matches in Ruby and needs this engine (SPEC §11.2).
var (
	shDigit = []rrange{{'0', '9'}}
	shWord  = []rrange{{'0', '9'}, {'A', 'Z'}, {'_', '_'}, {'a', 'z'}}
	shSpace = []rrange{{'\t', '\r'}, {' ', ' '}}
	shHex   = []rrange{{'0', '9'}, {'A', 'F'}, {'a', 'f'}}
)

// shorthand returns the class for \d \D \w \W \s \S \h \H, or nil.
func shorthand(r rune, fold bool) *charClass {
	var rs []rrange
	neg := false
	switch r {
	case 'd':
		rs = shDigit
	case 'D':
		rs, neg = shDigit, true
	case 'w':
		rs = shWord
	case 'W':
		rs, neg = shWord, true
	case 's':
		rs = shSpace
	case 'S':
		rs, neg = shSpace, true
	case 'h':
		rs = shHex
	case 'H':
		rs, neg = shHex, true
	default:
		return nil
	}
	c := &charClass{neg: neg, fold: fold && !neg}
	c.addRanges(rs)
	return c.normalize()
}

// dotClass is `.`: every rune but a newline, unless the `m` flag is on.
func dotClass(dotAll bool) *charClass {
	c := &charClass{neg: true}
	if !dotAll {
		c.addRune('\n')
	}
	return c.normalize()
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// ---------------------------------------------------------------------------
// Unicode properties and POSIX bracket classes
// ---------------------------------------------------------------------------

type propDef struct {
	tabs   []*unicode.RangeTable
	ranges []rrange
}

var (
	propOnce sync.Once
	propMap  map[string]propDef
)

// normProp folds a property name the way Onigmo does: case-insensitively and
// ignoring `_`, `-` and spaces, so \p{Cyrillic}, \p{cyrillic} and \p{CYRILLIC} agree.
func normProp(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '_' || r == '-' || r == ' ' {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func initProps() {
	m := map[string]propDef{}
	for name, t := range unicode.Categories {
		m[normProp(name)] = propDef{tabs: []*unicode.RangeTable{t}}
	}
	for name, t := range unicode.Scripts {
		m[normProp(name)] = propDef{tabs: []*unicode.RangeTable{t}}
	}
	cat := func(names ...string) []*unicode.RangeTable {
		out := make([]*unicode.RangeTable, 0, len(names))
		for _, n := range names {
			if t, ok := unicode.Categories[n]; ok {
				out = append(out, t)
			}
		}
		return out
	}
	// The POSIX bracket names are Unicode-aware in Ruby: [[:alpha:]] matches Cyrillic.
	m["alpha"] = propDef{tabs: cat("L")}
	m["alnum"] = propDef{tabs: cat("L", "N")}
	m["upper"] = propDef{tabs: cat("Lu")}
	m["lower"] = propDef{tabs: cat("Ll")}
	m["digit"] = propDef{tabs: cat("Nd")}
	m["punct"] = propDef{tabs: cat("P", "S")}
	m["cntrl"] = propDef{tabs: cat("Cc")}
	m["graph"] = propDef{tabs: cat("L", "M", "N", "P", "S")}
	m["print"] = propDef{tabs: cat("L", "M", "N", "P", "S", "Zs")}
	m["word"] = propDef{tabs: cat("L", "M", "N", "Pc"), ranges: []rrange{{'_', '_'}}}
	m["space"] = propDef{tabs: []*unicode.RangeTable{unicode.White_Space}}
	m["blank"] = propDef{tabs: cat("Zs"), ranges: []rrange{{'\t', '\t'}}}
	m["xdigit"] = propDef{ranges: shHex}
	m["ascii"] = propDef{ranges: []rrange{{0, 0x7F}}}
	m["any"] = propDef{ranges: []rrange{{0, unicode.MaxRune}}}
	m["assigned"] = propDef{tabs: cat("L", "M", "N", "P", "S", "Z", "C")}
	propMap = m
}

func lookupProp(name string) (propDef, bool) {
	propOnce.Do(initProps)
	p, ok := propMap[normProp(name)]
	return p, ok
}
