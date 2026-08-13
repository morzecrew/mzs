// Package rx is the regex facade of SPEC §11: one interface, two backends. RE2 (the
// stdlib regexp) handles everything it can do correctly; patterns it cannot — Unicode
// \b/\B, lookaround, backreferences, atomic and possessive forms — are routed to the
// bundled backtracking engine through the single call site rx.Compile makes to
// bt.Compile.
//
// Every index this package returns or accepts is a **rune** index, because the corpus
// pins `"привет" =~ /вет/` to 3 rather than 6 (SPEC §11.3).
package rx

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"mzs/internal/rx/bt"
)

// DefaultSteps is the per-match budget for the backtracking backend
// (Options.RegexSteps, SPEC §11.2). Compile installs it; the interpreter overrides it
// from Options with SetBudget.
const DefaultSteps = 200_000

// Group is one capture span. Start and End are rune indices; Text is the matched text;
// Name is "" for an unnamed group. Ok is false for a group that did not participate,
// in which case Start and End are -1.
type Group struct {
	Start int
	End   int
	Text  string
	Name  string
	Ok    bool
}

// Regexp is a compiled pattern. It is immutable after Compile (SetBudget aside, which
// the interpreter calls once before the program runs) and safe for concurrent use.
type Regexp struct {
	src    string
	flags  string
	re     *regexp.Regexp // RE2 backend; nil when bt is in use
	back   *bt.Regexp     // backtracking backend; nil when RE2 is in use
	names  []string
	steps  int
	approx bool
}

// Compile compiles a Ruby-flavoured pattern with Ruby flag letters ("imxsu").
//
// Backend selection is decided here and fixed thereafter: a pattern that needs
// Unicode \b, lookaround, a backreference or an atomic/possessive form goes to the
// bundled backtracking engine and nowhere else. An error from that engine is the
// answer — silently degrading to RE2 would make /\bменю\b/i compile and then never
// match Cyrillic, which is precisely the bug this package exists to remove.
func Compile(pattern, flags string) (*Regexp, error) {
	fl, err := normFlags(flags)
	if err != nil {
		return nil, err
	}
	r := &Regexp{src: pattern, flags: fl, steps: DefaultSteps}

	if scanPattern(pattern).needsBT() {
		br, berr := bt.Compile(pattern, fl)
		if berr != nil {
			return nil, fmt.Errorf("cannot compile /%s/%s: %w", pattern, fl, berr)
		}
		r.back = br
		r.names = br.GroupNames()
		return r, nil
	}

	gp, err := translate(pattern, fl)
	if err != nil {
		return nil, fmt.Errorf("cannot compile /%s/%s: %w", pattern, fl, err)
	}
	re, err := regexp.Compile(gp)
	if err != nil {
		return nil, fmt.Errorf("cannot compile /%s/%s: %w", pattern, fl, cleanRE2Error(err))
	}
	r.re = re
	r.names = re.SubexpNames()
	return r, nil
}

// MustCompile is Compile for patterns known good at build time; it panics on error and
// is only for tests and package initialisation.
func MustCompile(pattern, flags string) *Regexp {
	r, err := Compile(pattern, flags)
	if err != nil {
		panic(err)
	}
	return r
}

// Source returns the pattern text between the slashes.
func (r *Regexp) Source() string { return r.src }

// Flags returns the canonical flag letters ("s" is folded into "m").
func (r *Regexp) Flags() string { return r.flags }

// String renders the literal form, which is also `to_s` for a regex value (§12.6).
func (r *Regexp) String() string { return "/" + r.src + "/" + r.flags }

// Key is the identity used for regex map keys (§7.6) and for the compile cache.
func (r *Regexp) Key() string { return r.src + "\x00" + r.flags }

// Groups returns the capture-group names by index; index 0 is the whole match and is
// always "". An unnamed group has an empty entry.
func (r *Regexp) Groups() []string { return r.names }

// NumGroups returns the number of capturing groups, not counting the whole match.
func (r *Regexp) NumGroups() int {
	if len(r.names) == 0 {
		return 0
	}
	return len(r.names) - 1
}

// Backend reports which engine compiled the pattern: "re2" or "bt".
func (r *Regexp) Backend() string {
	if r.back != nil {
		return "bt"
	}
	return "re2"
}

// Approx reports that the pattern was compiled by a backend that cannot reproduce its
// Ruby semantics exactly. Now that the backtracking engine handles every construct
// scanPattern routes to it, nothing sets this: it stays for `mzs --check`, which
// reports it, and as the escape hatch if a construct is ever downgraded again.
func (r *Regexp) Approx() bool { return r.approx }

// SetBudget installs the per-match step budget for the backtracking backend.
func (r *Regexp) SetBudget(n int) {
	if n > 0 {
		r.steps = n
	}
}

// Budget returns the per-match step budget.
func (r *Regexp) Budget() int { return r.steps }

func (r *Regexp) nameAt(i int) string {
	if i < len(r.names) {
		return r.names[i]
	}
	return ""
}

// ---------------------------------------------------------------------------
// Matching. The *Err forms are primary: a backtracking budget overrun is a script
// error, not a hang and not a silent non-match. The shorter forms are the SPEC §11.2
// signatures and swallow that error as "no match"; the interpreter should prefer the
// *Err forms so `Options.RegexSteps` is observable.
// ---------------------------------------------------------------------------

// MatchErr reports whether the pattern matches anywhere in s.
func (r *Regexp) MatchErr(s string) (bool, error) {
	if r.re != nil {
		return r.re.MatchString(s), nil
	}
	gs, err := r.back.FindFrom([]rune(s), 0, r.steps)
	return gs != nil, err
}

// Match is MatchErr without the error (SPEC §11.2).
func (r *Regexp) Match(s string) bool { ok, _ := r.MatchErr(s); return ok }

// FindIndexErr returns the rune bounds of the leftmost match.
func (r *Regexp) FindIndexErr(s string) (start, end int, ok bool, err error) {
	if r.re != nil {
		loc := r.re.FindStringIndex(s)
		if loc == nil {
			return 0, 0, false, nil
		}
		return utf8.RuneCountInString(s[:loc[0]]), utf8.RuneCountInString(s[:loc[1]]), true, nil
	}
	gs, err := r.back.FindFrom([]rune(s), 0, r.steps)
	if err != nil || gs == nil {
		return 0, 0, false, err
	}
	return gs[0].Start, gs[0].End, true, nil
}

// FindIndex returns the rune indices of the leftmost match. This is what `=~` uses:
// `"привет" =~ /вет/` is 3.
func (r *Regexp) FindIndex(s string) (start, end int, ok bool) {
	start, end, ok, _ = r.FindIndexErr(s)
	return
}

// FindSubmatchErr returns the groups of the leftmost match; element 0 is the whole
// match, as `R.match(S)` requires (§8.4).
func (r *Regexp) FindSubmatchErr(s string) ([]Group, bool, error) {
	all, err := r.findAll(s, 1)
	if err != nil || len(all) == 0 {
		return nil, false, err
	}
	return all[0], true, nil
}

// FindSubmatch is FindSubmatchErr without the error.
func (r *Regexp) FindSubmatch(s string) ([]Group, bool) {
	g, ok, _ := r.FindSubmatchErr(s)
	return g, ok
}

// FindAllErr returns every non-overlapping match; limit < 0 means all.
func (r *Regexp) FindAllErr(s string, limit int) ([][]Group, error) {
	return r.findAll(s, limit)
}

// FindAll is FindAllErr without the error.
func (r *Regexp) FindAll(s string, limit int) [][]Group {
	all, _ := r.findAll(s, limit)
	return all
}

func (r *Regexp) findAll(s string, limit int) ([][]Group, error) {
	if limit == 0 {
		return nil, nil
	}
	if r.re != nil {
		locs := r.re.FindAllStringSubmatchIndex(s, limit)
		if locs == nil {
			return nil, nil
		}
		om := newOffsetMap(s)
		out := make([][]Group, 0, len(locs))
		for _, loc := range locs {
			g := make([]Group, len(loc)/2)
			for i := range g {
				a, b := loc[2*i], loc[2*i+1]
				g[i].Name = r.nameAt(i)
				if a < 0 || b < 0 {
					g[i].Start, g[i].End = -1, -1
					continue
				}
				g[i].Start, g[i].End = om.at(a), om.at(b)
				g[i].Text = s[a:b]
				g[i].Ok = true
			}
			out = append(out, g)
		}
		return out, nil
	}

	rs := []rune(s)
	var out [][]Group
	for pos := 0; pos <= len(rs); {
		if limit > 0 && len(out) >= limit {
			break
		}
		gs, err := r.back.FindFrom(rs, pos, r.steps)
		if err != nil {
			return out, err
		}
		if gs == nil {
			break
		}
		g := make([]Group, len(gs))
		for i, bg := range gs {
			g[i].Name = r.nameAt(i)
			if !bg.Ok {
				g[i].Start, g[i].End = -1, -1
				continue
			}
			g[i].Start, g[i].End = bg.Start, bg.End
			g[i].Text = string(rs[bg.Start:bg.End])
			g[i].Ok = true
		}
		out = append(out, g)
		if gs[0].End == gs[0].Start {
			pos = gs[0].End + 1
		} else {
			pos = gs[0].End
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Replacement and splitting
// ---------------------------------------------------------------------------

// ReplaceErr substitutes repl for the first match (all == false) or every match.
// repl understands `\0`/`\&` (whole match), `\1`..`\9`, `\k<name>` and `\\`.
func (r *Regexp) ReplaceErr(s, repl string, all bool) (string, error) {
	limit := 1
	if all {
		limit = -1
	}
	ms, err := r.findAll(s, limit)
	if err != nil {
		return s, err
	}
	if len(ms) == 0 {
		return s, nil
	}
	rs := []rune(s)
	var sb strings.Builder
	prev := 0
	for _, g := range ms {
		sb.WriteString(string(rs[prev:g[0].Start]))
		sb.WriteString(Expand(repl, g))
		prev = g[0].End
	}
	sb.WriteString(string(rs[prev:]))
	return sb.String(), nil
}

// Replace is the SPEC §11.2 signature.
func (r *Regexp) Replace(s, repl string, all bool) string {
	out, _ := r.ReplaceErr(s, repl, all)
	return out
}

// ReplaceAll replaces every match.
func (r *Regexp) ReplaceAll(s, repl string) string {
	out, _ := r.ReplaceErr(s, repl, true)
	return out
}

// ReplaceFunc is the block form of gsub/sub: f receives the groups of each match and
// returns its replacement verbatim (no `\1` expansion).
func (r *Regexp) ReplaceFunc(s string, all bool, f func([]Group) (string, error)) (string, error) {
	limit := 1
	if all {
		limit = -1
	}
	ms, err := r.findAll(s, limit)
	if err != nil {
		return s, err
	}
	if len(ms) == 0 {
		return s, nil
	}
	rs := []rune(s)
	var sb strings.Builder
	prev := 0
	for _, g := range ms {
		rep, err := f(g)
		if err != nil {
			return "", err
		}
		sb.WriteString(string(rs[prev:g[0].Start]))
		sb.WriteString(rep)
		prev = g[0].End
	}
	sb.WriteString(string(rs[prev:]))
	return sb.String(), nil
}

// SplitErr splits s around every match. limit < 0 means unlimited; a positive limit
// caps the number of returned pieces. Capture groups are not interleaved into the
// result, and trailing empty fields are kept — Ruby's `split` drops them, and str.go
// applies that rule so the policy lives in exactly one place.
func (r *Regexp) SplitErr(s string, limit int) ([]string, error) {
	if limit == 0 {
		return nil, nil
	}
	max := -1
	if limit > 0 {
		max = limit - 1
	}
	ms, err := r.findAll(s, max)
	if err != nil {
		return nil, err
	}
	rs := []rune(s)
	out := make([]string, 0, len(ms)+1)
	prev := 0
	for _, g := range ms {
		if g[0].Start == 0 && g[0].End == 0 && prev == 0 {
			// An empty match at position 0 would emit a leading "" in Ruby too, but
			// only for a zero-width pattern; keep the piece and move on.
			continue
		}
		out = append(out, string(rs[prev:g[0].Start]))
		prev = g[0].End
	}
	out = append(out, string(rs[prev:]))
	return out, nil
}

// Split is the SPEC §11.2 signature.
func (r *Regexp) Split(s string, limit int) []string {
	out, _ := r.SplitErr(s, limit)
	return out
}

// Expand renders a replacement template against a match's groups.
func Expand(repl string, g []Group) string {
	if !strings.ContainsRune(repl, '\\') {
		return repl
	}
	rs := []rune(repl)
	var sb strings.Builder
	for i := 0; i < len(rs); i++ {
		if rs[i] != '\\' || i+1 >= len(rs) {
			sb.WriteRune(rs[i])
			continue
		}
		n := rs[i+1]
		switch {
		case n == '\\':
			sb.WriteRune('\\')
			i++
		case n == '&' || n == '0':
			if len(g) > 0 && g[0].Ok {
				sb.WriteString(g[0].Text)
			}
			i++
		case n >= '1' && n <= '9':
			idx := int(n - '0')
			if idx < len(g) && g[idx].Ok {
				sb.WriteString(g[idx].Text)
			}
			i++
		case n == 'k' && i+2 < len(rs) && rs[i+2] == '<':
			end := -1
			for j := i + 3; j < len(rs); j++ {
				if rs[j] == '>' {
					end = j
					break
				}
			}
			if end < 0 {
				sb.WriteRune(rs[i])
				continue
			}
			name := string(rs[i+3 : end])
			for _, gr := range g {
				if gr.Name == name && gr.Ok {
					sb.WriteString(gr.Text)
					break
				}
			}
			i = end
		default:
			sb.WriteRune(rs[i])
		}
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// Byte-to-rune offsets
// ---------------------------------------------------------------------------

// offsetMap converts the byte offsets RE2 reports into the rune offsets mzs exposes.
// For an all-ASCII subject — most `/start`-style conditions — it is the identity and
// allocates nothing.
type offsetMap struct {
	ascii bool
	tab   []int
}

func newOffsetMap(s string) offsetMap {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			tab := make([]int, len(s)+1)
			ri, bi := 0, 0
			for bi < len(s) {
				_, w := utf8.DecodeRuneInString(s[bi:])
				for k := 0; k < w; k++ {
					tab[bi+k] = ri
				}
				bi += w
				ri++
			}
			tab[len(s)] = ri
			return offsetMap{tab: tab}
		}
	}
	return offsetMap{ascii: true}
}

func (o offsetMap) at(b int) int {
	if o.ascii || b < 0 {
		return b
	}
	if b >= len(o.tab) {
		return o.tab[len(o.tab)-1]
	}
	return o.tab[b]
}

// cleanRE2Error strips the Go-specific "error parsing regexp:" prefix so diagnostics
// read as mzs messages rather than as leaked stdlib text.
func cleanRE2Error(err error) error {
	msg := err.Error()
	if i := strings.Index(msg, ": "); i >= 0 && strings.HasPrefix(msg, "error parsing regexp") {
		msg = strings.TrimSpace(msg[i+2:])
	}
	return errors.New(msg)
}
