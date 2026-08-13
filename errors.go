package mzs

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"mzs/internal/token"
)

// Sentinel errors (§13.5). ErrTimeout, ErrBudget, ErrDepth and ErrCanceled are the
// unrecoverable limits: `try … else …` never catches them, they always reach the host
// (§8.11, §14.1). ErrFatal is what a host function wraps to make its own error
// uncatchable too.
var (
	ErrTimeout  = errors.New("mzs: execution timed out")
	ErrBudget   = errors.New("mzs: step budget exceeded")
	ErrDepth    = errors.New("mzs: max call depth exceeded")
	ErrCanceled = errors.New("mzs: canceled")
	ErrFatal    = errors.New("mzs: fatal")
)

// Error kinds (§13.5). These are the exact strings that appear between the position
// and the message in a rendered diagnostic.
const (
	ErrKindSyntax   = "syntax"
	ErrKindName     = "name"
	ErrKindType     = "type"
	ErrKindArgument = "argument"
	ErrKindIndex    = "index"
	ErrKindZeroDiv  = "zero-division"
	ErrKindRegex    = "regex"
	ErrKindRaise    = "raise"
	ErrKindLimit    = "limit"
	ErrKindInternal = "internal"
)

// Frame is one entry of a script call stack, innermost first.
type Frame struct {
	Fn   string
	Line int
	Col  int
}

func (f Frame) String() string {
	return f.Fn + " (" + strconv.Itoa(f.Line) + ":" + strconv.Itoa(f.Col) + ")"
}

// Warning is a non-fatal diagnostic produced at compile time (§11.5, §17).
// Options.StrictWarnings promotes the whole set to a compile error.
type Warning struct {
	Msg  string
	Line int
	Col  int
}

func (w Warning) String() string {
	return strconv.Itoa(w.Line) + ":" + strconv.Itoa(w.Col) + ": warning: " + w.Msg
}

// Error is every script-visible failure: a syntax error from Compile, a runtime type
// error, a `raise`, or a limit. Every Error carries a position — a node without one is
// a bug (§17).
type Error struct {
	Kind    string
	Msg     string
	File    string
	Line    int
	Col     int
	Stack   []Frame
	Data    Value
	wrapped error
}

// Error renders as `<file>:<line>:<col>: <kind>: <message>` (§17).
func (e *Error) Error() string {
	var sb strings.Builder
	if e.File != "" {
		sb.WriteString(e.File)
		sb.WriteByte(':')
	}
	if e.Line > 0 {
		sb.WriteString(strconv.Itoa(e.Line))
		sb.WriteByte(':')
		sb.WriteString(strconv.Itoa(e.Col))
		sb.WriteString(": ")
	} else if e.File != "" {
		sb.WriteString(" ")
	}
	if e.Kind != "" {
		sb.WriteString(e.Kind)
		sb.WriteString(": ")
	}
	sb.WriteString(e.Msg)
	return sb.String()
}

func (e *Error) Unwrap() error { return e.wrapped }

// Catchable reports whether `try X else Y` may swallow this error. Limits and anything
// wrapping ErrFatal are not catchable (§8.11).
func (e *Error) Catchable() bool {
	if e == nil {
		return false
	}
	if e.Kind == ErrKindLimit || e.Kind == ErrKindInternal {
		return false
	}
	switch {
	case errors.Is(e.wrapped, ErrFatal),
		errors.Is(e.wrapped, ErrTimeout),
		errors.Is(e.wrapped, ErrBudget),
		errors.Is(e.wrapped, ErrDepth),
		errors.Is(e.wrapped, ErrCanceled):
		return false
	}
	return true
}

// ErrorValue is the dict a `try X else (e) -> Y` clause binds to e (§8.11).
func (e *Error) ErrorValue() Value {
	d := NewOrderedDictCap(4)
	_ = d.Set(Str("message"), Str(e.Msg))
	_ = d.Set(Str("kind"), Str(e.Kind))
	_ = d.Set(Str("line"), Int(int64(e.Line)))
	if e.Data.Kind() != KNil {
		_ = d.Set(Str("data"), e.Data)
	}
	return dictOf(d)
}

// At stamps a position onto e and returns it, so call sites can build an error without
// knowing where they are and let the evaluator position it on the way out.
func (e *Error) At(file string, line, col int) *Error {
	if e.File == "" {
		e.File = file
	}
	if e.Line == 0 {
		e.Line = line
		e.Col = col
	}
	return e
}

// newError builds a positionless script error; the evaluator positions it with At.
func newError(kind, format string, a ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, a...)}
}

// wrapError builds an error that carries a sentinel for errors.Is.
func wrapError(kind string, sentinel error, format string, a ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, a...), wrapped: sentinel}
}

// The kind-specific constructors. Every stdlib and runtime file should use these
// rather than fmt.Errorf, so the Kind field is always right and `mzs --check`,
// condition.go's "error means no match" rule and `try` all see consistent values.
func typeErrorf(format string, a ...any) *Error  { return newError(ErrKindType, format, a...) }
func nameErrorf(format string, a ...any) *Error  { return newError(ErrKindName, format, a...) }
func argErrorf(format string, a ...any) *Error   { return newError(ErrKindArgument, format, a...) }
func indexErrorf(format string, a ...any) *Error { return newError(ErrKindIndex, format, a...) }
func regexErrorf(format string, a ...any) *Error { return newError(ErrKindRegex, format, a...) }
func syntaxErrorf(format string, a ...any) *Error {
	return newError(ErrKindSyntax, format, a...)
}

func limitErrorf(sentinel error, format string, a ...any) *Error {
	return wrapError(ErrKindLimit, sentinel, format, a...)
}

// zeroDivError is the one arithmetic error mzs raises; float division by zero is IEEE
// and never errors (§8.3).
func zeroDivError() *Error { return newError(ErrKindZeroDiv, "divided by 0") }

// raiseError is what the `raise` builtin produces. data is the payload of raise(dict).
func raiseError(msg string, data Value) *Error {
	return &Error{Kind: ErrKindRaise, Msg: msg, Data: data}
}

// asError extracts an *Error from err, or synthesises one. Everything crossing the
// host boundary goes through it so Run always returns a positioned *Error.
func asError(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	kind := ErrKindRaise
	switch {
	case errors.Is(err, ErrTimeout), errors.Is(err, ErrBudget),
		errors.Is(err, ErrDepth), errors.Is(err, ErrCanceled):
		kind = ErrKindLimit
	}
	return &Error{Kind: kind, Msg: err.Error(), wrapped: err}
}

// ---------------------------------------------------------------------------
// "did you mean" suggestions (§17)
// ---------------------------------------------------------------------------

// renameTable is the Ruby-to-mzs table of §19.2, restricted to the spellings that lex
// as mzs identifiers (there is no `?`/`!` suffix, §3.4). The codemod and the
// did-you-mean diagnostics are driven by the same table, so an expression the codemod
// missed fails loudly with the right fix-it instead of a bare "undefined method". None
// of these names exists in the standard library — D17 forbids aliases — so an exact hit
// here is always a rename, never a shadow.
var renameTable = map[string]string{
	"downcase": "lower",
	"upcase":   "upper",
	"strip":    "trim",
	"lstrip":   "trim_start",
	"rstrip":   "trim_end",
	"include":  "has",
	"has_key":  "has",
	"cover":    "has",
	"length":   "len",
	"size":     "len",
	"gsub":     "replace",
	"sub":      "replace_first",
	"scan":     "matches",
	"match":    "captures",
	"select":   "filter",
	"collect":  "map",
	"detect":   "find",
	"inject":   "reduce",
	"to_s":     "str",
	"to_i":     "int",
	"to_f":     "float",
	"to_a":     "array",
	"to_h":     "dict",
	"to_json":  "json",
	"puts":     "say",
	"p":        "debug",
}

// renameKeys is renameTable's keys, sorted, so the near-miss pass over them is
// deterministic.
var renameKeys = sortedKeys(renameTable)

// suggest produces the "did you mean" half of a name or type diagnostic (§17), from the
// two sources the spec names: an exact lookup in the rename table of §19.2, and a
// Levenshtein-1 search over the candidates the receiver actually offers.
//
// The third pass is what makes the §17 example work: `lenght` is one edit from `length`,
// which the rename table maps to `len`. One-liners are typed by hand into a web input,
// and a bad suggestion costs less than none.
func suggest(name string, candidates []string) string {
	if name == "" {
		return ""
	}
	if to, ok := renameTable[name]; ok {
		return to
	}
	if s := nearest(name, candidates); s != "" {
		return s
	}
	if k := nearest(name, renameKeys); k != "" {
		return renameTable[k]
	}
	return ""
}

// nearest returns the alphabetically first candidate within edit distance 1, or "".
// The distance counts a transposition as one edit, so `lenght` reaches `length` — typing
// two adjacent runes the wrong way round is the mistake this exists for.
func nearest(name string, candidates []string) string {
	lower := strings.ToLower(name)
	pool := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if spellable(c) {
			pool = append(pool, c)
		}
	}
	sort.Strings(pool)
	for _, c := range pool {
		if editDistance(lower, strings.ToLower(c)) <= 1 {
			return c
		}
	}
	return ""
}

// spellable rejects the rows a script cannot name. §12.9 registers `&`, `|` and `^` on
// bool, but §3.9 has no such lexeme and §3.4 no such identifier, so they are reachable
// only through the Go API — and one rune away from every one-letter typo, which made
// `[a: 1].a` suggest `'&'`. A suggestion nobody can type is worse than none.
func spellable(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 && !token.IsIdentStart(r) {
			return false
		}
		if i > 0 && !token.IsIdentPart(r) {
			return false
		}
	}
	return true
}

// undefinedMethodError produces the §8.7 message with the §17 suggestion, drawn from the
// receiver kind's own method table plus the universal ones.
func undefinedMethodError(k Kind, name string) *Error {
	cands := append(MethodNames(k), MethodNames(KAny)...)
	if s := suggest(name, cands); s != "" {
		return nameErrorf("undefined method '%s' for %s (did you mean '%s'?)", name, k, s)
	}
	return nameErrorf("undefined method '%s' for %s", name, k)
}

// undefinedFunctionError is the same idea for a call whose name is not a method of any
// kind and not a function in scope — UFCS step 3 with the receiver's kind unknown
// (§4.3), and a plain `f(1)` whose f does not exist.
func undefinedFunctionError(name string, extra []string) *Error {
	if s := suggest(name, namePool(extra)); s != "" {
		return nameErrorf("undefined function '%s' (did you mean '%s'?)", name, s)
	}
	return nameErrorf("undefined function '%s'", name)
}

// undefinedMemberError is what `math.max(1, 2)` gets: `math` is a module, `max` is not
// one of its members, and UFCS deliberately does not apply to a module receiver, so the
// only truthful thing to say names the module (§12.8).
func undefinedMemberError(module, name string, members []Value) *Error {
	cands := make([]string, 0, len(members))
	for _, m := range members {
		cands = append(cands, m.Str())
	}
	if s := suggest(name, cands); s != "" {
		return nameErrorf("undefined member '%s' in module '%s' (did you mean '%s'?)", name, module, s)
	}
	return nameErrorf("undefined member '%s' in module '%s'", name, module)
}

// namePool is the whole callable namespace: the builtins, every stdlib row of every kind
// (UFCS makes them all callable in prefix position, §12) and the names extra adds from
// the failing scope.
func namePool(extra []string) []string {
	pool := append(BuiltinNames(), extra...)
	for _, k := range dispatchKinds {
		pool = append(pool, MethodNames(k)...)
	}
	return pool
}

// undefinedVariableError is what Compile reports for an identifier that is not a local,
// a parameter, a declared fn, a builtin or a module (§9.3). There is no bareword shim:
// a plain-text answer template is not a program. extra carries the names visible in the
// failing scope.
func undefinedVariableError(name string, extra []string) *Error {
	cands := append(BuiltinNames(), extra...)
	if s := suggest(name, cands); s != "" {
		return nameErrorf("undefined variable '%s' (did you mean '%s'?)", name, s)
	}
	return nameErrorf("undefined variable '%s'", name)
}

// editDistance is the optimal string alignment distance: insertion, deletion,
// substitution and the transposition of two adjacent runes each cost one. It works on
// runes, not bytes, because the identifiers it compares are routinely Cyrillic (§3.4).
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}
	// Three rows: the transposition case needs the row before the previous one.
	prev2 := make([]int, len(br)+1)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			d := min(min(cur[j-1]+1, prev[j]+1), prev[j-1]+cost)
			if i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1] {
				d = min(d, prev2[j-2]+1)
			}
			cur[j] = d
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(br)]
}

// ---------------------------------------------------------------------------
// Control-flow signals (§8.10)
// ---------------------------------------------------------------------------

// `return`, `break` and `next` travel as sentinel error values rather than as Go
// panics, so the step budget stays accurate and the cost stays predictable (§8.10). They
// are never returned to a host: the evaluator absorbs them at the function, loop or
// call boundary that owns them.
type ctrlKind uint8

const (
	ctrlReturn ctrlKind = iota + 1
	ctrlBreak
	ctrlNext
)

type ctrl struct {
	kind ctrlKind
	val  Value
}

func (c *ctrl) Error() string {
	switch c.kind {
	case ctrlReturn:
		return "mzs: return outside a function"
	case ctrlBreak:
		return "mzs: break outside a loop"
	}
	return "mzs: next outside a closure"
}

func returnSignal(v Value) error { return &ctrl{kind: ctrlReturn, val: v} }
func breakSignal(v Value) error  { return &ctrl{kind: ctrlBreak, val: v} }
func nextSignal(v Value) error   { return &ctrl{kind: ctrlNext, val: v} }

// errNilChain is the signal a `?.` sends when its receiver is nil: the whole postfix
// chain built on top of it is skipped and its value is nil, and no argument anywhere in
// the chain is evaluated (§4.3, §8.7). It travels only through the receiver position of
// a trailer; the evaluator turns it into nil at every other expression boundary, so a
// `?.` inside parentheses or inside an argument stops there.
var errNilChain = errors.New("mzs: nil chain")

// undefinedNameMessage is the §17 diagnostic for a name that resolved to nothing. A
// name that is a module the program never included is the common case now that nothing
// is ambient (§12.8), and saying so beats a suggestion list that cannot help.
func undefinedNameMessage(in *Interp, name string, visible []string) string {
	if _, ok := lookupIncludable(in, name); ok {
		return fmt.Sprintf("undefined variable '%s' (add `include %s` at the top of the file)", name, name)
	}
	if _, ok := moduleGateOf(name); ok {
		// A module that exists but is gated off: the fix is a host option, not a line
		// at the top of the file.
		return unknownModuleMessage(in, name)
	}
	return undefinedVariableError(name, visible).Msg
}

// ctrlOf reports whether err is a control signal, and which.
func ctrlOf(err error) (*ctrl, bool) {
	var c *ctrl
	if errors.As(err, &c) {
		return c, true
	}
	return nil, false
}

// isBreak reports a `break v` travelling out of a closure. A stdlib method must return
// such an error unchanged; the evaluator's method-call site turns it into the value of
// the whole call, which is what makes `xs.each { break 1 }` evaluate to 1 (§8.10).
func isBreak(err error) (Value, bool) {
	if c, ok := ctrlOf(err); ok && c.kind == ctrlBreak {
		return c.val, true
	}
	return Nil(), false
}
