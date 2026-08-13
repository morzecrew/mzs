package bt

import (
	"fmt"
	"unicode"
)

type opcode uint8

const (
	opChar     opcode = iota // consume one rune, folded when fold is set
	opClass                  // consume one rune matching cls
	opSplit                  // try x first, keep y as a backtrack point
	opJmp                    // continue at x
	opSave                   // store the current position in slot x
	opMark                   // remember the position in slot x (loop-progress guard)
	opProgress               // fail if the position equals mark slot x
	opAssert                 // zero-width assertion of kind x
	opBackref                // re-match the text captured by group x
	opLook                   // lookaround: sub-program x, y bit 0 = negative, bit 1 = behind
	opAtomic                 // sub-program x, matched once and never retried
	opMatch                  // this program succeeded
)

type inst struct {
	op   opcode
	fold bool
	x, y int32
	r    rune
	cls  *charClass
}

// prog is one instruction list. progs[0] is the pattern itself; the others are the
// bodies of lookarounds, atomic groups and possessive quantifiers. min/max are the
// body's rune width and matter only for lookbehind, which has to know how far back
// to start.
type prog struct {
	code     []inst
	min, max int
}

// maxProgSize bounds the expansion of `{n,m}`, which the compiler unrolls.
const maxProgSize = 100_000

type compiler struct {
	progs    []prog
	markBase int
	nmark    int
}

// newMark reserves the register that holds the position the current loop iteration
// started at, which is all opProgress needs.
func (c *compiler) newMark() int32 {
	m := c.markBase + c.nmark
	c.nmark++
	return int32(m)
}

// compileProg lowers one node into its own program and returns its index.
func (c *compiler) compileProg(n node, min, max int) (int, error) {
	idx := len(c.progs)
	c.progs = append(c.progs, prog{min: min, max: max})
	code, err := c.node(nil, n)
	if err != nil {
		return 0, err
	}
	code = append(code, inst{op: opMatch})
	c.progs[idx].code = code
	return idx, nil
}

func (c *compiler) node(code []inst, n node) ([]inst, error) {
	if len(code) > maxProgSize {
		return nil, fmt.Errorf("regular expression is too large")
	}
	var err error
	switch t := n.(type) {
	case *nEmpty:
		return code, nil

	case *nChar:
		return append(code, inst{op: opChar, r: t.r, fold: t.fold}), nil

	case *nClass:
		return append(code, inst{op: opClass, cls: t.cls}), nil

	case *nAssert:
		return append(code, inst{op: opAssert, x: int32(t.kind)}), nil

	case *nBackref:
		return append(code, inst{op: opBackref, x: int32(t.idx), fold: t.fold}), nil

	case *nCat:
		for _, s := range t.subs {
			if code, err = c.node(code, s); err != nil {
				return nil, err
			}
		}
		return code, nil

	case *nAlt:
		var jumps []int
		for i, a := range t.alts {
			if i == len(t.alts)-1 {
				if code, err = c.node(code, a); err != nil {
					return nil, err
				}
				break
			}
			sp := len(code)
			code = append(code, inst{op: opSplit})
			if code, err = c.node(code, a); err != nil {
				return nil, err
			}
			jumps = append(jumps, len(code))
			code = append(code, inst{op: opJmp})
			code[sp].x = int32(sp + 1)
			code[sp].y = int32(len(code))
		}
		for _, j := range jumps {
			code[j].x = int32(len(code))
		}
		return code, nil

	case *nGroup:
		if t.idx == 0 {
			return c.node(code, t.sub)
		}
		code = append(code, inst{op: opSave, x: int32(2 * t.idx)})
		if code, err = c.node(code, t.sub); err != nil {
			return nil, err
		}
		return append(code, inst{op: opSave, x: int32(2*t.idx + 1)}), nil

	case *nAtomic:
		idx, err := c.compileProg(t.sub, 0, 0)
		if err != nil {
			return nil, err
		}
		return append(code, inst{op: opAtomic, x: int32(idx)}), nil

	case *nLook:
		min, max := 0, 0
		if t.behind {
			min, max = widths(t.sub)
			if max < 0 {
				return nil, fmt.Errorf("invalid pattern in look-behind: unbounded width")
			}
		}
		idx, err := c.compileProg(t.sub, min, max)
		if err != nil {
			return nil, err
		}
		var y int32
		if t.neg {
			y |= 1
		}
		if t.behind {
			y |= 2
		}
		return append(code, inst{op: opLook, x: int32(idx), y: y}), nil

	case *nRepeat:
		return c.repeat(code, t)
	}
	return nil, fmt.Errorf("internal: unknown regex node %T", n)
}

func (c *compiler) repeat(code []inst, t *nRepeat) ([]inst, error) {
	// A possessive quantifier is exactly an atomic group around the greedy form,
	// so it costs one sub-program and no separate matcher path.
	if t.poss {
		idx, err := c.compileProg(&nRepeat{sub: t.sub, min: t.min, max: t.max, lazy: t.lazy}, 0, 0)
		if err != nil {
			return nil, err
		}
		return append(code, inst{op: opAtomic, x: int32(idx)}), nil
	}
	var err error
	for i := 0; i < t.min; i++ {
		if code, err = c.node(code, t.sub); err != nil {
			return nil, err
		}
		if len(code) > maxProgSize {
			return nil, fmt.Errorf("regular expression is too large")
		}
	}
	if t.max < 0 {
		return c.star(code, t.sub, t.lazy)
	}
	return c.optional(code, t.sub, t.max-t.min, t.lazy)
}

// star emits the unbounded form. The mark/progress pair is what stops `(a*)*` from
// spinning on an empty body; it is only emitted when the body can match nothing.
//
// The rule is Onigmo's, because Ruby is the reference: an iteration that consumed
// nothing skips the jump back to the top and falls out of the loop, keeping whatever
// it captured. RE2 prunes the same state differently and can report a different span
// for the group of a nullable repeat — a corner the corpus never reaches, and one
// where Ruby wins.
func (c *compiler) star(code []inst, sub node, lazy bool) ([]inst, error) {
	sp := len(code)
	code = append(code, inst{op: opSplit})
	var mark int32 = -1
	prog := -1
	if nullable(sub) {
		mark = c.newMark()
		code = append(code, inst{op: opMark, x: mark})
	}
	var err error
	if code, err = c.node(code, sub); err != nil {
		return nil, err
	}
	if mark >= 0 {
		prog = len(code)
		code = append(code, inst{op: opProgress, x: mark})
	}
	code = append(code, inst{op: opJmp, x: int32(sp)})
	exit := len(code)
	if prog >= 0 {
		code[prog].y = int32(exit)
	}
	patchSplit(code, sp, sp+1, exit, lazy)
	return code, nil
}

// optional emits `x{0,n}` as nested optionals, so the greedy preference order is the
// same as Ruby's: the outer copy is tried before the inner ones are abandoned.
func (c *compiler) optional(code []inst, sub node, n int, lazy bool) ([]inst, error) {
	if n <= 0 {
		return code, nil
	}
	sp := len(code)
	code = append(code, inst{op: opSplit})
	var err error
	if code, err = c.node(code, sub); err != nil {
		return nil, err
	}
	if code, err = c.optional(code, sub, n-1, lazy); err != nil {
		return nil, err
	}
	patchSplit(code, sp, sp+1, len(code), lazy)
	return code, nil
}

// patchSplit points a split at the preferred branch first: the body for a greedy
// quantifier, the exit for a lazy one.
func patchSplit(code []inst, sp, body, exit int, lazy bool) {
	if lazy {
		code[sp].x, code[sp].y = int32(exit), int32(body)
		return
	}
	code[sp].x, code[sp].y = int32(body), int32(exit)
}

// nullable reports whether the node can match the empty string.
func nullable(n node) bool {
	switch t := n.(type) {
	case *nEmpty, *nAssert, *nLook:
		return true
	case *nChar, *nClass:
		return false
	case *nBackref:
		// A group may well have captured nothing, so assume it can.
		return true
	case *nCat:
		for _, s := range t.subs {
			if !nullable(s) {
				return false
			}
		}
		return true
	case *nAlt:
		for _, a := range t.alts {
			if nullable(a) {
				return true
			}
		}
		return false
	case *nGroup:
		return nullable(t.sub)
	case *nAtomic:
		return nullable(t.sub)
	case *nRepeat:
		return t.min == 0 || nullable(t.sub)
	}
	return true
}

// widths returns the rune width a node can consume; max is -1 when unbounded. Only
// lookbehind needs it, and Onigmo rejects an unbounded lookbehind for the same reason
// this does: there is no start position to scan back to.
func widths(n node) (int, int) {
	switch t := n.(type) {
	case *nEmpty, *nAssert, *nLook:
		return 0, 0
	case *nChar, *nClass:
		return 1, 1
	case *nBackref:
		return 0, -1
	case *nCat:
		lo, hi := 0, 0
		for _, s := range t.subs {
			a, b := widths(s)
			lo += a
			if hi < 0 || b < 0 {
				hi = -1
				continue
			}
			hi += b
		}
		return lo, hi
	case *nAlt:
		lo, hi := -1, 0
		for _, a := range t.alts {
			x, y := widths(a)
			if lo < 0 || x < lo {
				lo = x
			}
			if hi >= 0 {
				if y < 0 {
					hi = -1
				} else if y > hi {
					hi = y
				}
			}
		}
		if lo < 0 {
			lo = 0
		}
		return lo, hi
	case *nGroup:
		return widths(t.sub)
	case *nAtomic:
		return widths(t.sub)
	case *nRepeat:
		lo, hi := widths(t.sub)
		if t.max < 0 || hi < 0 {
			return lo * t.min, -1
		}
		return lo * t.min, hi * t.max
	}
	return 0, -1
}

// firstSet derives the classes a match must begin with, so FindFrom can skip start
// positions without entering the matcher at all. It is an over-approximation: a rune
// outside the set can never start a match, but a rune inside it may still fail.
// nil means "no useful prefilter".
func firstSet(n node) []*charClass {
	var acc []*charClass
	nullable, ok := firstOf(n, &acc)
	if !ok || nullable || len(acc) == 0 {
		return nil
	}
	return mergeFirst(acc)
}

// mergeFirst folds the alternatives into one sorted range set when it can, which
// turns the per-position prefilter into a single binary search with no case folding
// at match time. Scanning a message that does not match is the common case — most
// conditions in the corpus are a miss — so this is the loop worth flattening.
func mergeFirst(acc []*charClass) []*charClass {
	out := &charClass{}
	for _, c := range acc {
		if c.neg || len(c.tabs) > 0 || len(c.negtabs) > 0 {
			return acc
		}
		for _, r := range c.ranges {
			if !c.fold {
				out.ranges = append(out.ranges, r)
				continue
			}
			if r.hi-r.lo > 256 {
				return acc // too wide to expand the fold closure by hand
			}
			for x := r.lo; x <= r.hi; x++ {
				out.addRune(x)
				for f := unicode.SimpleFold(x); f != x; f = unicode.SimpleFold(f) {
					out.addRune(f)
				}
			}
		}
	}
	return []*charClass{out.normalize()}
}

const maxFirstClasses = 24

func firstOf(n node, acc *[]*charClass) (nullable, ok bool) {
	if len(*acc) > maxFirstClasses {
		return false, false
	}
	switch t := n.(type) {
	case *nEmpty, *nAssert, *nLook:
		return true, true // zero-width: whatever follows decides
	case *nChar:
		c := &charClass{fold: t.fold}
		c.addRune(t.r)
		*acc = append(*acc, c.normalize())
		return false, true
	case *nClass:
		*acc = append(*acc, t.cls)
		return false, true
	case *nBackref:
		return false, false
	case *nCat:
		for _, s := range t.subs {
			nu, o := firstOf(s, acc)
			if !o {
				return false, false
			}
			if !nu {
				return false, true
			}
		}
		return true, true
	case *nAlt:
		anyNil := false
		for _, a := range t.alts {
			nu, o := firstOf(a, acc)
			if !o {
				return false, false
			}
			anyNil = anyNil || nu
		}
		return anyNil, true
	case *nGroup:
		return firstOf(t.sub, acc)
	case *nAtomic:
		return firstOf(t.sub, acc)
	case *nRepeat:
		nu, o := firstOf(t.sub, acc)
		if !o {
			return false, false
		}
		return nu || t.min == 0, true
	}
	return false, false
}

// anchoredBOS reports that every match must start at rune 0, which lets FindFrom stop
// after a single attempt for `\A…` patterns.
func anchoredBOS(n node) bool {
	switch t := n.(type) {
	case *nAssert:
		return t.kind == assertBOS
	case *nCat:
		for _, s := range t.subs {
			if anchoredBOS(s) {
				return true
			}
			if !zeroWidth(s) {
				return false
			}
		}
		return false
	case *nAlt:
		for _, a := range t.alts {
			if !anchoredBOS(a) {
				return false
			}
		}
		return len(t.alts) > 0
	case *nGroup:
		return anchoredBOS(t.sub)
	case *nAtomic:
		return anchoredBOS(t.sub)
	}
	return false
}

func zeroWidth(n node) bool {
	switch n.(type) {
	case *nEmpty, *nAssert, *nLook:
		return true
	}
	return false
}
