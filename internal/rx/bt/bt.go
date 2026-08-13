// Package bt is the backtracking regex backend of SPEC §11.2. It exists because RE2
// cannot express the three things the production corpus needs: a Unicode-aware \b
// (/\bменю\b/i ships today), lookaround (/^(?!❌ Отмена).*$/), and backreferences.
//
// The pattern is parsed as **Ruby** — `^`/`$` are line anchors, `m` means dot-matches-
// newline, `x` is extended, `i` folds through unicode.SimpleFold so `И`/`и` agree —
// then compiled to a small instruction list and run by an explicit backtracking loop
// over a []rune. Every index in and out is a rune index.
//
// Untrusted bot authors write these patterns, so the matcher is bounded: FindFrom
// takes a step budget and returns ErrBudget rather than letting `(a+)+$` hang the
// host.
package bt

import (
	"errors"
	"fmt"
	"sync"
)

// ErrUnsupported is returned by Compile for a pattern this engine cannot handle.
// internal/rx tests for it with errors.Is before deciding whether to degrade to RE2.
var ErrUnsupported = errors.New("mzs/rx/bt: pattern not supported")

// ErrBudget is returned by FindFrom when a match exceeds the step budget. It is a
// script error, never a hang (SPEC §11.2).
var ErrBudget = errors.New("mzs/rx/bt: regex step budget exceeded")

// Group is one capture span, in **rune** indices. Group 0 is the whole match.
// Ok is false for a group that did not participate in the match.
type Group struct {
	Start int
	End   int
	Ok    bool
}

// Regexp is a compiled pattern. It is immutable after Compile and safe for concurrent
// use by many goroutines; the only mutable state is a pool of scratch matchers.
type Regexp struct {
	src      string
	flags    string
	ngrp     int
	names    []string
	progs    []prog
	nslot    int
	first    []*charClass
	anchored bool
	pool     sync.Pool
}

// Compile parses a **Ruby-flavoured** pattern: `^`/`$` are line anchors, `\A`/`\z`/`\Z`
// are string anchors, and flags are the Ruby letters "imxsu" (m means dot-matches-
// newline). The pattern is the raw text between the slashes; no translation has been
// applied by the caller.
func Compile(pattern, flags string) (*Regexp, error) {
	fl, err := parseFlags(flags)
	if err != nil {
		return nil, err
	}
	p := &parser{src: []rune(pattern), fl: fl, names: []string{""}}
	root, err := p.parse()
	if err != nil {
		return nil, err
	}
	if p.maxref > p.ngrp {
		return nil, fmt.Errorf("invalid backref number \\%d", p.maxref)
	}

	c := &compiler{markBase: 2 * (p.ngrp + 1)}
	idx, err := c.compileProg(root, 0, 0)
	if err != nil {
		return nil, err
	}
	if idx != 0 {
		return nil, fmt.Errorf("internal: main program is not index 0")
	}

	r := &Regexp{
		src:      pattern,
		flags:    flags,
		ngrp:     p.ngrp,
		names:    p.names,
		progs:    c.progs,
		nslot:    2*(p.ngrp+1) + c.nmark,
		first:    firstSet(root),
		anchored: anchoredBOS(root),
	}
	return r, nil
}

// Source returns the pattern text Compile was given.
func (r *Regexp) Source() string { return r.src }

// Flags returns the flag letters Compile was given.
func (r *Regexp) Flags() string { return r.flags }

// NumGroups returns the number of capturing groups, not counting group 0.
func (r *Regexp) NumGroups() int { return r.ngrp }

// GroupNames returns one entry per group index 0..NumGroups; "" means unnamed.
func (r *Regexp) GroupNames() []string { return r.names }

// String renders the literal form, for diagnostics.
func (r *Regexp) String() string { return "/" + r.src + "/" + r.flags }

func (r *Regexp) machine() *machine {
	if v := r.pool.Get(); v != nil {
		m := v.(*machine)
		m.re = r
		return m
	}
	return &machine{re: r, slots: make([]int32, r.nslot)}
}

func (r *Regexp) release(m *machine) {
	m.src = nil
	r.pool.Put(m)
}

// FindFrom searches src for the leftmost match starting at or after rune index from.
// It returns nil groups (and a nil error) when there is no match. budget bounds the
// number of matcher steps for the whole call; a value <= 0 means unlimited.
//
// This is the single primitive internal/rx builds Match, FindIndex, FindSubmatch,
// FindAll, Replace and Split on top of. Adding methods here is fine; changing this
// signature is not.
func (r *Regexp) FindFrom(src []rune, from int, budget int) (gs []Group, err error) {
	if r == nil || len(r.progs) == 0 {
		return nil, ErrUnsupported
	}
	if from < 0 {
		from = 0
	}
	if from > len(src) {
		return nil, nil
	}
	if r.anchored && from > 0 {
		return nil, nil
	}

	m := r.machine()
	defer r.release(m)
	m.reset(src, budget)

	// A pattern with a derivable first-rune set — every alternation of literals in
	// the corpus — skips whole start positions without touching the matcher.
	for start := from; start <= len(src); start++ {
		if r.first != nil {
			for start < len(src) && !r.firstAllows(src[start]) {
				start++
			}
			if start >= len(src) {
				break
			}
		}
		m.clearSlots()
		end, ok, err := m.run(0, start, -1)
		if err != nil {
			return nil, err
		}
		if ok {
			return r.groups(m, start, end), nil
		}
		if r.anchored {
			break
		}
	}
	return nil, nil
}

func (r *Regexp) firstAllows(c rune) bool {
	for _, cl := range r.first {
		if cl.match(c) {
			return true
		}
	}
	return false
}

func (r *Regexp) groups(m *machine, start, end int) []Group {
	gs := make([]Group, r.ngrp+1)
	gs[0] = Group{Start: start, End: end, Ok: true}
	for i := 1; i <= r.ngrp; i++ {
		s, e := m.slots[2*i], m.slots[2*i+1]
		if s < 0 || e < 0 || e < s {
			gs[i] = Group{Start: -1, End: -1}
			continue
		}
		gs[i] = Group{Start: int(s), End: int(e), Ok: true}
	}
	return gs
}
