package mzs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"mzs/internal/rx"
)

// Kind is the runtime type tag of a Value (SPEC §7.1 and §13.1).
type Kind uint8

const (
	KNil Kind = iota
	KBool
	KInt
	KFloat
	KString
	KRegex
	KArray
	KDict
	KFunc
	KTime
	// KRange is the lazy integer range of §8.6. type(r) reports "range" while
	// is("array") is still true, which is what §12.10 requires.
	KRange
	// KTask is what calling an `async fn` returns (§8.14): a running body and the value
	// it will have. It is an opaque handle — `await` and `done` are all it answers — and
	// it never leaves the Run that created it.
	KTask
	// KSeq is the lazy sequence of §12.14. type(s) reports "seq" and is("array") is
	// false — the opposite of a Range, which can be materialised under a cap and says so.
	KSeq
)

const (
	// kindTomb marks a deleted slot in an OrderedDict. It is never a Value's kind.
	kindTomb Kind = 254
	// KAny is the pseudo-kind under which methods available on every receiver are
	// registered (§12.1, the "any first argument" table). It is never a Value's kind.
	KAny Kind = 255
)

// Guards against unbounded recursion over a self-referential collection, which scripts
// can build with a single `a.push(a)`. These walks run outside the evaluator's step
// budget, and a Go stack overflow is fatal — recover() cannot catch it — so without a
// depth cap one script could kill the host process (A7). Both limits sit far above any
// realistic nesting depth for bot data.
const (
	maxCompareDepth = 200
	maxRenderDepth  = 200
)

var kindNames = map[Kind]string{
	KNil:    "nil",
	KBool:   "bool",
	KInt:    "int",
	KFloat:  "float",
	KString: "string",
	KRegex:  "regex",
	KArray:  "array",
	KDict:   "dict",
	KFunc:   "function",
	KTime:   "time",
	KRange:  "range",
	KTask:   "task",
	KSeq:    "seq",
	KAny:    "any",
}

// String returns the name `type(x)` reports (§7.2).
func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return "unknown"
}

// Value is an immutable handle on a script value. Copying a Value is free; scalars
// never allocate. Array, Dict and Func values are references — a copy aliases the same
// underlying data, which is what makes `arr.push(1)` visible to every holder (§7.1).
type Value struct {
	k Kind
	n int64  // KInt; KBool as 0/1; KFloat as math.Float64bits
	s string // KString
	p any    // KRegex/KArray/KDict/KFunc/KTime/KRange
}

// ---------------------------------------------------------------------------
// Constructors
// ---------------------------------------------------------------------------

// Nil returns the nil value, which is falsy.
func Nil() Value { return Value{} }

func Bool(b bool) Value {
	if b {
		return Value{k: KBool, n: 1}
	}
	return Value{k: KBool}
}

func Int(i int64) Value { return Value{k: KInt, n: i} }

func Float(f float64) Value { return Value{k: KFloat, n: int64(math.Float64bits(f))} }

func Str(s string) Value { return Value{k: KString, s: s} }

// Regex compiles a pattern with the flag letters of §3.8 ("imxsu") and wraps it as a
// value.
func Regex(pattern, flags string) (Value, error) {
	r, err := rx.Compile(pattern, flags)
	if err != nil {
		return Nil(), newError(ErrKindRegex, "%s", err.Error())
	}
	return Value{k: KRegex, p: r}, nil
}

// Array builds a new array value from elems. The elements are copied into a fresh
// backing slice, so the caller's slice is not aliased.
func Array(elems ...Value) Value {
	xs := make([]Value, len(elems))
	copy(xs, elems)
	return Value{k: KArray, p: &xs}
}

// Dict builds a new dict value from an alternating key/value list. It panics on an odd
// count, which is a programming error in host code, never reachable from a script.
func Dict(pairs ...Value) Value {
	if len(pairs)%2 != 0 {
		panic("mzs.Dict: odd number of key/value arguments")
	}
	d := NewOrderedDict()
	for i := 0; i < len(pairs); i += 2 {
		_ = d.Set(pairs[i], pairs[i+1])
	}
	return Value{k: KDict, p: d}
}

// Fn wraps a Go function as a callable script value. arity -1 means variadic.
func Fn(name string, arity int, f HostFunc) Value {
	return Value{k: KFunc, p: &Func{Name: name, Arity: arity, Host: f}}
}

// --- internal constructors, used by the evaluator and the method tables ---

// arrayOf wraps xs without copying: the value aliases the caller's slice.
func arrayOf(xs []Value) Value { return Value{k: KArray, p: &xs} }

// arrayPtr wraps an existing slice header so two values can share one array.
func arrayPtr(p *[]Value) Value { return Value{k: KArray, p: p} }

// namedArray is an array that also answers to a set of string keys: the match array of
// `captures`, whose named groups are reachable as `m["name"]` as well as by position
// (§8.4). The names ride on the payload, not on the elements, so every array method,
// `type`, `json` and `==` see a plain array (§7.1) and only `[]` consults them.
type namedArray struct {
	elems []Value
	names map[string]int
}

// namedArrayOf wraps xs without copying and attaches the name → position table.
func namedArrayOf(xs []Value, names map[string]int) Value {
	if len(names) == 0 {
		return arrayOf(xs)
	}
	return Value{k: KArray, p: &namedArray{elems: xs, names: names}}
}

func dictOf(d *OrderedDict) Value { return Value{k: KDict, p: d} }

func funcOf(f *Func) Value { return Value{k: KFunc, p: f} }

func regexOf(r *rx.Regexp) Value { return Value{k: KRegex, p: r} }

func timeOf(t time.Time) Value { return Value{k: KTime, p: t} }

func taskOf(t *task) Value { return Value{k: KTask, p: t} }

func seqOf(s *Seq) Value { return Value{k: KSeq, p: s} }

func rangeOf(lo, hi int64, excl bool) Value {
	return Value{k: KRange, p: &Range{Lo: lo, Hi: hi, Excl: excl}}
}

// ---------------------------------------------------------------------------
// Inspection
// ---------------------------------------------------------------------------

func (v Value) Kind() Kind { return v.k }

func (v Value) IsNil() bool { return v.k == KNil }

// Truthy implements D6: only nil and false are falsy. 0, 0.0, "", [], {} and NaN are
// all truthy. It is why `s.index(/re/)` returning 0 — a match at position 0 — still
// counts as a hit (§7.3).
func (v Value) Truthy() bool {
	switch v.k {
	case KNil:
		return false
	case KBool:
		return v.n != 0
	}
	return true
}

// Bool returns the boolean payload; it is false for every non-KBool value. Use Truthy
// for script truthiness.
func (v Value) Bool() bool { return v.k == KBool && v.n != 0 }

// Int applies `int` (§12.7) and never panics: nil is 0, a Float truncates toward zero,
// a String yields its leading integer, everything else is 0. `"".int == 0` is what makes
// `$price.int + 1200` safe when $price is unset (§9.1).
func (v Value) Int() int64 {
	switch v.k {
	case KInt:
		return v.n
	case KBool:
		return v.n
	case KFloat:
		f := v.float()
		if math.IsNaN(f) {
			return 0
		}
		// Converting an out-of-range float with int64() is undefined in Go and on amd64
		// wraps to MinInt64, so `1e30.int` came back large and *negative* — a silent
		// wrong answer that then poisons any arithmetic downstream. mzs is int64-only
		// (D9), so saturating is the honest approximation and at least keeps the sign.
		if f >= math.MaxInt64 {
			return math.MaxInt64
		}
		if f <= math.MinInt64 {
			return math.MinInt64
		}
		return int64(math.Trunc(f))
	case KString:
		return parseLeadingInt(v.s)
	case KTime:
		return v.Time().Unix()
	}
	return 0
}

// Float applies `float` (§12.7) and never panics.
func (v Value) Float() float64 {
	switch v.k {
	case KFloat:
		return v.float()
	case KInt:
		return float64(v.n)
	case KBool:
		return float64(v.n)
	case KString:
		return parseLeadingFloat(v.s)
	case KTime:
		return float64(v.Time().Unix())
	}
	return 0
}

// Str applies `str` (§12.7). Array and Dict render as JSON, which is what string
// interpolation of a collection produces (§8.12).
func (v Value) Str() string {
	switch v.k {
	case KNil:
		return ""
	case KBool:
		if v.n != 0 {
			return "true"
		}
		return "false"
	case KInt:
		return strconv.FormatInt(v.n, 10)
	case KFloat:
		return formatFloat(v.float())
	case KString:
		return v.s
	case KRegex:
		return v.rx().String()
	case KArray, KDict:
		return encodeJSON(v, "")
	case KRange:
		r := v.rng()
		op := ".."
		if r.Excl {
			op = "..<"
		}
		return strconv.FormatInt(r.Lo, 10) + op + strconv.FormatInt(r.Hi, 10)
	case KFunc:
		f := v.fn()
		if f.Name == "" {
			return "#<fn>"
		}
		return "#<fn " + f.Name + ">"
	case KTime:
		return v.Time().Format(time.RFC3339)
	case KTask:
		return v.task().render()
	case KSeq:
		// A sequence has no text: rendering one would mean running it, and a `str` that
		// consumed its receiver is not a `str`. `#<seq>` is what a function prints too,
		// and for the same reason (§12.14).
		return "#<seq>"
	}
	return ""
}

// String makes Value an fmt.Stringer; it is exactly Str.
func (v Value) String() string { return v.Str() }

// Inspect is `str` with strings quoted and nil spelled out (§12.7).
func (v Value) Inspect() string {
	switch v.k {
	case KNil:
		return "nil"
	case KString:
		return quoteString(v.s)
	}
	return v.Str()
}

// Len is the rune length of a String, the element count of an Array/Dict/Range, and 0
// for every scalar (§12.1 `len`).
func (v Value) Len() int {
	switch v.k {
	case KString:
		return utf8.RuneCountInString(v.s)
	case KArray:
		return len(*v.arr())
	case KDict:
		return v.odict().Len()
	case KRange:
		return v.rng().Len()
	}
	return 0
}

// TypeName is what the `type` builtin returns (§7.2): the kind's name, or the record's
// name for a dict built by a `record` constructor (§7.8). It is the one place the label a
// record carries is visible as text — `is("dict")` is still true, and everything a dict
// does it still does.
func (v Value) TypeName() string {
	if r := v.recordType(); r != nil {
		return r.Name
	}
	return v.k.String()
}

// IsNum reports whether v is an Int or a Float.
func (v Value) IsNum() bool { return v.k == KInt || v.k == KFloat }

// Time returns the time payload; the zero time for any other kind.
func (v Value) Time() time.Time {
	if t, ok := v.p.(time.Time); ok && v.k == KTime {
		return t
	}
	return time.Time{}
}

// --- unexported payload accessors, shared by the evaluator and the method tables ---

func (v Value) float() float64 { return math.Float64frombits(uint64(v.n)) }

// arr returns the backing slice pointer of an array, or nil off-kind. Callers mutate
// through the pointer so every alias sees the change.
func (v Value) arr() *[]Value {
	if v.k != KArray {
		return nil
	}
	switch p := v.p.(type) {
	case *[]Value:
		return p
	case *namedArray:
		return &p.elems
	}
	return nil
}

// arrNames is the name → position table of a match array, or nil for every other array.
func (v Value) arrNames() map[string]int {
	if v.k != KArray {
		return nil
	}
	if p, ok := v.p.(*namedArray); ok {
		return p.names
	}
	return nil
}

func (v Value) odict() *OrderedDict {
	if v.k != KDict {
		return nil
	}
	m, _ := v.p.(*OrderedDict)
	return m
}

func (v Value) fn() *Func {
	if v.k != KFunc {
		return nil
	}
	f, _ := v.p.(*Func)
	return f
}

func (v Value) rx() *rx.Regexp {
	if v.k != KRegex {
		return nil
	}
	r, _ := v.p.(*rx.Regexp)
	return r
}

func (v Value) rng() *Range {
	if v.k != KRange {
		return nil
	}
	r, _ := v.p.(*Range)
	return r
}

func (v Value) task() *task {
	if v.k != KTask {
		return nil
	}
	t, _ := v.p.(*task)
	return t
}

func (v Value) seq() *Seq {
	if v.k != KSeq {
		return nil
	}
	s, _ := v.p.(*Seq)
	return s
}

// ---------------------------------------------------------------------------
// Equality and ordering
// ---------------------------------------------------------------------------

// Equal is `==` (§7.4): numbers compare numerically across Int/Float, strings
// byte-exactly, arrays and dicts structurally (dict order is insignificant), functions
// by identity, and every other cross-kind pair is false — never an error. There is no
// number/string coercion (§9.1) and no `===`.
//
// Two regexes compare by source and flags; a regex against any other kind is false. A
// `==` whose operand is a regex *literal* never reaches here — the compile step rejects
// it and points at `~` (D5, §6.3 step 6).
func (v Value) Equal(o Value) bool { return v.equal(o, 0) }

// equal carries a recursion depth so a self-referential collection (`a = []; a.push(a)`)
// terminates instead of overflowing the Go stack. A stack overflow is fatal and cannot be
// recovered, which would let one bot author's script take down the whole host (A7).
// Beyond the limit the comparison reports "not equal" rather than pretending to know.
func (v Value) equal(o Value, depth int) bool {
	if depth > maxCompareDepth {
		return false
	}
	if v.k == KNil || o.k == KNil {
		return v.k == o.k
	}
	if v.IsNum() && o.IsNum() {
		if v.k == KInt && o.k == KInt {
			return v.n == o.n
		}
		return v.Float() == o.Float()
	}
	if v.k != o.k {
		return false
	}
	switch v.k {
	case KBool:
		return v.n == o.n
	case KString:
		return v.s == o.s
	case KRegex:
		return v.rx().Key() == o.rx().Key()
	case KTime:
		return v.Time().Equal(o.Time())
	case KFunc:
		return v.fn() == o.fn()
	case KTask:
		// Identity, like a function: two tasks are the same task or they are not, and
		// what they will produce is not a question `==` may block on.
		return v.task() == o.task()
	case KSeq:
		// Identity as well, and for the stronger reason: comparing two sequences would
		// mean running both, and a `==` with side effects is not one (§7.4, §12.14).
		return v.seq() == o.seq()
	case KRange:
		a, b := v.rng(), o.rng()
		return a.Lo == b.Lo && a.Hi == b.Hi && a.Excl == b.Excl
	case KArray:
		a, b := *v.arr(), *o.arr()
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if !a[i].equal(b[i], depth+1) {
				return false
			}
		}
		return true
	case KDict:
		a, b := v.odict(), o.odict()
		if a.Len() != b.Len() {
			return false
		}
		eq := true
		a.Each(func(k, av Value) bool {
			bv, ok := b.Get(k)
			if !ok || !av.equal(bv, depth+1) {
				eq = false
				return false
			}
			return true
		})
		return eq
	}
	return false
}

// compare implements ordering (§7.5) for Int/Float (mixed allowed), String, Bool,
// Array (element-wise then by length) and Time. It returns -1, 0 or 1, and ok == false
// for incomparable operands, which is what `<=>` turns into nil. Mixed number/string
// ordering is *not* a coercion: it lands here as incomparable, and ops.go turns that
// into `cannot compare string with int` for the relational operators (§7.5, §9.1).
func compare(a, b Value) (int, bool) {
	if a.IsNum() && b.IsNum() {
		if a.k == KInt && b.k == KInt {
			return cmpInt(a.n, b.n), true
		}
		x, y := a.Float(), b.Float()
		if math.IsNaN(x) || math.IsNaN(y) {
			return 0, false
		}
		return cmpFloat(x, y), true
	}
	if a.k != b.k {
		return 0, false
	}
	switch a.k {
	case KString:
		return strings.Compare(a.s, b.s), true
	case KBool:
		return cmpInt(a.n, b.n), true
	case KTime:
		x, y := a.Time(), b.Time()
		switch {
		case x.Before(y):
			return -1, true
		case x.After(y):
			return 1, true
		}
		return 0, true
	case KArray:
		xs, ys := *a.arr(), *b.arr()
		n := min(len(xs), len(ys))
		for i := 0; i < n; i++ {
			c, ok := compare(xs[i], ys[i])
			if !ok {
				return 0, false
			}
			if c != 0 {
				return c, true
			}
		}
		return cmpInt(int64(len(xs)), int64(len(ys))), true
	}
	return 0, false
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// Collections
// ---------------------------------------------------------------------------

// Index returns the i-th element of an Array or the i-th rune of a String as a
// one-rune String. A negative index counts from the end; out of range is nil (§8.8).
func (v Value) Index(i int) Value {
	switch v.k {
	case KArray:
		xs := *v.arr()
		if i < 0 {
			i += len(xs)
		}
		if i < 0 || i >= len(xs) {
			return Nil()
		}
		return xs[i]
	case KString:
		rs := []rune(v.s)
		if i < 0 {
			i += len(rs)
		}
		if i < 0 || i >= len(rs) {
			return Nil()
		}
		return Str(string(rs[i]))
	case KRange:
		r := v.rng()
		n := r.Len()
		if i < 0 {
			i += n
		}
		if i < 0 || i >= n {
			return Nil()
		}
		return Int(r.At(i))
	}
	return Nil()
}

// setIndex assigns into an Array, extending it with nils when needed (§8.8). It is an
// error for every other kind; strings are immutable.
func (v Value) setIndex(i int, x Value) error {
	if v.k != KArray {
		return newError(ErrKindType, "cannot assign to an index of %s", v.k)
	}
	p := v.arr()
	xs := *p
	if i < 0 {
		i += len(xs)
		if i < 0 {
			return newError(ErrKindIndex, "index out of range")
		}
	}
	for len(xs) <= i {
		xs = append(xs, Nil())
	}
	xs[i] = x
	*p = xs
	return nil
}

// Get reads a Dict key; it is nil for a missing key and for every other kind.
func (v Value) Get(key Value) Value {
	if v.k != KDict {
		return Nil()
	}
	x, _ := v.odict().Get(key)
	return x
}

// Set writes a Dict key, appending at the end when the key is new. An unhashable key is
// ignored; the evaluator uses setKey when it needs the diagnostic.
func (v Value) Set(key, val Value) {
	if v.k == KDict {
		_ = v.odict().Set(key, val)
	}
}

func (v Value) setKey(key, val Value) error {
	if v.k != KDict {
		return newError(ErrKindType, "cannot assign to a key of %s", v.k)
	}
	return v.odict().Set(key, val)
}

// Append pushes onto an Array; it is a no-op for every other kind.
func (v Value) Append(vals ...Value) {
	if p := v.arr(); p != nil {
		*p = append(*p, vals...)
	}
}

// Keys returns a Dict's keys in insertion order (D11).
func (v Value) Keys() []Value {
	if v.k != KDict {
		return nil
	}
	return v.odict().Keys()
}

// Elems returns an Array's backing slice. It is *not* copied: do not retain it past
// the next mutation of the array. A Range is materialised (uncapped — callers inside
// the interpreter must go through Ctx.CheckCollection first).
func (v Value) Elems() []Value {
	switch v.k {
	case KArray:
		return *v.arr()
	case KRange:
		return v.rng().Elems()
	}
	return nil
}

// Clone is `dup` (§12.1): a shallow copy for Array and Dict, identity for everything
// else, since scalars are immutable and functions are shared.
func (v Value) Clone() Value {
	switch v.k {
	case KArray:
		xs := *v.arr()
		out := make([]Value, len(xs))
		copy(out, xs)
		return arrayOf(out)
	case KDict:
		return dictOf(v.odict().Clone())
	}
	return v
}

// Range is the lazy integer range behind `a..b` and `a..<b` (§8.6). A descending range
// is empty. It is immutable.
type Range struct {
	Lo   int64
	Hi   int64
	Excl bool
}

// Len is the number of elements, clamped to a non-negative int.
//
// The arithmetic is done in uint64 because both ends of the int64 range are reachable
// from a script: `hi--` on an exclusive range ending at MinInt64 would wrap to MaxInt64
// and turn the empty range into the largest one there is, and `hi - lo + 1` overflows for
// any range wider than MaxInt64. In unsigned arithmetic the distance between two int64s
// is exact, and the clamp happens before the +1 that could carry past it.
func (r *Range) Len() int {
	hi := r.Hi
	if r.Excl {
		if hi == math.MinInt64 {
			return 0
		}
		hi--
	}
	if hi < r.Lo {
		return 0
	}
	d := uint64(hi) - uint64(r.Lo)
	if d >= uint64(math.MaxInt32) {
		return math.MaxInt32
	}
	return int(d) + 1
}

// At returns the i-th element without bounds checking beyond the caller's own.
func (r *Range) At(i int) int64 { return r.Lo + int64(i) }

// Contains implements `has` (§12.10).
func (r *Range) Contains(n int64) bool {
	if r.Excl {
		return n >= r.Lo && n < r.Hi
	}
	return n >= r.Lo && n <= r.Hi
}

// Elems materialises the range. Callers inside the interpreter must charge the result
// against Options.MaxCollection first (§14.2).
func (r *Range) Elems() []Value {
	n := r.Len()
	out := make([]Value, n)
	for i := 0; i < n; i++ {
		out[i] = Int(r.At(i))
	}
	return out
}

// ---------------------------------------------------------------------------
// Conversions
// ---------------------------------------------------------------------------

// formatFloat renders a float the way `str` must (§8.12, §12.7): shortest round-trip,
// always carrying a '.' or an exponent so 2.0 prints as "2.0", and never an exponent for
// |x| < 1e21, so prices and counts read naturally.
func formatFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	var s string
	if math.Abs(f) >= 1e21 {
		s = strconv.FormatFloat(f, 'e', -1, 64)
		// Go writes 1e+21; the shortest round-trip form of §12.7 keeps the mantissa dot.
		if i := strings.IndexByte(s, 'e'); i >= 0 && !strings.Contains(s[:i], ".") {
			s = s[:i] + ".0" + s[i:]
		}
		return s
	}
	s = strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// parseLeadingInt implements `int` on a String (§12.7): optional leading whitespace, an
// optional sign, then decimal digits with '_' separators. Anything else stops the scan.
// It never fails; "" and "abc" are 0, "12abc" is 12, "0x1f" is 0 (base 10 only).
func parseLeadingInt(s string) int64 {
	i := 0
	for i < len(s) && isSpaceByte(s[i]) {
		i++
	}
	neg := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	var n int64
	var digits int
	for ; i < len(s); i++ {
		c := s[i]
		if c == '_' && digits > 0 {
			continue
		}
		if c < '0' || c > '9' {
			break
		}
		d := int64(c - '0')
		if n > (math.MaxInt64-d)/10 {
			if neg {
				return math.MinInt64
			}
			return math.MaxInt64
		}
		n = n*10 + d
		digits++
	}
	if digits == 0 {
		return 0
	}
	if neg {
		return -n
	}
	return n
}

// parseLeadingFloat implements `float` on a String: the longest leading prefix that
// parses as a float, else 0.0. Never fails.
func parseLeadingFloat(s string) float64 {
	i := 0
	for i < len(s) && isSpaceByte(s[i]) {
		i++
	}
	start := i
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '_') {
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '_') {
			i++
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k > j {
			i = k
		}
	}
	text := strings.ReplaceAll(s[start:i], "_", "")
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0
	}
	return f
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// fnv1a is the stable string hash behind `hash` (§12.1). It must not vary between runs,
// so it is written out rather than taken from the runtime.
func fnv1a(s string) int64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return int64(h & 0x7fffffffffffffff)
}

// quoteString renders a script string the way `inspect` does: double quotes, backslash
// escapes for the controls, and every other rune left as itself so Cyrillic and emoji
// stay readable.
func quoteString(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) + 2)
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if r < 0x20 {
				sb.WriteString(fmt.Sprintf(`\u%04x`, r))
				continue
			}
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// ---------------------------------------------------------------------------
// JSON
// ---------------------------------------------------------------------------

// encodeJSON renders v as JSON. indent == "" produces the compact form used by `str`
// and `json`; a non-empty indent produces `json.pretty`. Dict keys are stringified with
// `str` and emitted in insertion order (D11); NaN, ±Inf and functions become null,
// because JSON has no spelling for them.
func encodeJSON(v Value, indent string) string {
	var buf []byte
	buf = appendJSON(buf, v, indent, 0)
	return string(buf)
}

func appendJSON(dst []byte, v Value, indent string, level int) []byte {
	// A self-referential collection (`a = []; a.push(a)`) would otherwise recurse until
	// the Go stack overflows, which is fatal and unrecoverable — it would take the host
	// down, not just the script (A7). Nesting deeper than this is truncated to null,
	// which keeps the output valid JSON; legitimate data never comes close.
	if level > maxRenderDepth && (v.k == KArray || v.k == KDict || v.k == KRange) {
		return append(dst, "null"...)
	}
	nl := func(b []byte, lv int) []byte {
		if indent == "" {
			return b
		}
		b = append(b, '\n')
		for i := 0; i < lv; i++ {
			b = append(b, indent...)
		}
		return b
	}
	switch v.k {
	case KNil:
		return append(dst, "null"...)
	case KBool:
		if v.n != 0 {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	case KInt:
		return strconv.AppendInt(dst, v.n, 10)
	case KFloat:
		f := v.float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return append(dst, "null"...)
		}
		return strconv.AppendFloat(dst, f, 'g', -1, 64)
	case KString:
		return appendJSONString(dst, v.s)
	case KRegex:
		return appendJSONString(dst, v.rx().Source())
	case KTime:
		return appendJSONString(dst, v.Time().Format(time.RFC3339))
	case KFunc, KTask, KSeq:
		// A task is a running body, not data: it encodes as null, exactly as a function
		// does. What a script wants in the document is `t.await`. A seq is the same
		// answer for the same reason — `json` itself raises rather than writing this
		// one out (§12.14), because a sequence *has* a document form and it is
		// `s.array`.
		return append(dst, "null"...)
	case KArray, KRange:
		xs := v.Elems()
		if len(xs) == 0 {
			return append(dst, "[]"...)
		}
		dst = append(dst, '[')
		for i, e := range xs {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = nl(dst, level+1)
			dst = appendJSON(dst, e, indent, level+1)
		}
		dst = nl(dst, level)
		return append(dst, ']')
	case KDict:
		m := v.odict()
		if m.Len() == 0 {
			return append(dst, "{}"...)
		}
		dst = append(dst, '{')
		first := true
		m.Each(func(k, val Value) bool {
			if !first {
				dst = append(dst, ',')
			}
			first = false
			dst = nl(dst, level+1)
			dst = appendJSONString(dst, k.Str())
			dst = append(dst, ':')
			if indent != "" {
				dst = append(dst, ' ')
			}
			dst = appendJSON(dst, val, indent, level+1)
			return true
		})
		dst = nl(dst, level)
		return append(dst, '}')
	}
	return append(dst, "null"...)
}

func appendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch c {
			case '"':
				dst = append(dst, '\\', '"')
			case '\\':
				dst = append(dst, '\\', '\\')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				if c < 0x20 {
					dst = append(dst, fmt.Sprintf(`\u%04x`, c)...)
				} else {
					dst = append(dst, c)
				}
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			dst = append(dst, "�"...)
			i++
			continue
		}
		dst = append(dst, s[i:i+size]...)
		i += size
	}
	return append(dst, '"')
}

// decodeJSON parses JSON into a Value, preserving object key order (D11) and mapping
// integral numbers to Int. It is what JSON.parse and Value.UnmarshalJSON share.
func decodeJSON(data []byte) (Value, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeJSONValue(dec)
	if err != nil {
		return Nil(), err
	}
	if _, err := dec.Token(); err == nil {
		return Nil(), errors.New("unexpected trailing data in JSON")
	}
	return v, nil
}

func decodeJSONValue(dec *json.Decoder) (Value, error) {
	t, err := dec.Token()
	if err != nil {
		return Nil(), err
	}
	return decodeJSONFrom(dec, t)
}

func decodeJSONFrom(dec *json.Decoder, t json.Token) (Value, error) {
	switch x := t.(type) {
	case nil:
		return Nil(), nil
	case bool:
		return Bool(x), nil
	case string:
		return Str(x), nil
	case json.Number:
		if i, err := strconv.ParseInt(x.String(), 10, 64); err == nil {
			return Int(i), nil
		}
		f, err := x.Float64()
		if err != nil {
			return Nil(), err
		}
		if f == math.Trunc(f) && math.Abs(f) < 1<<53 {
			return Int(int64(f)), nil
		}
		return Float(f), nil
	case json.Delim:
		switch x {
		case '[':
			xs := []Value{}
			for dec.More() {
				e, err := decodeJSONValue(dec)
				if err != nil {
					return Nil(), err
				}
				xs = append(xs, e)
			}
			if _, err := dec.Token(); err != nil {
				return Nil(), err
			}
			return arrayOf(xs), nil
		case '{':
			m := NewOrderedDict()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return Nil(), err
				}
				key, ok := kt.(string)
				if !ok {
					return Nil(), errors.New("invalid JSON object key")
				}
				e, err := decodeJSONValue(dec)
				if err != nil {
					return Nil(), err
				}
				_ = m.Set(Str(key), e)
			}
			if _, err := dec.Token(); err != nil {
				return Nil(), err
			}
			return dictOf(m), nil
		}
	}
	return Nil(), fmt.Errorf("invalid JSON token %v", t)
}

// errSeqJSON is what every JSON encoder in the package answers for a value that holds a
// lazy sequence. The text is the script-level one (§12.14) so a host, an HTTP handler and
// a script are told the same thing and given the same fix.
var errSeqJSON = errors.New("a seq is lazy and has no JSON form; materialise it with .array")

// MarshalJSON makes a Value usable anywhere encoding/json is. It **fails** for a value
// that holds a seq anywhere inside it, rather than writing the `null` appendJSON has to
// fall back on: a sequence has a document form — the one `.array` produces — so `null`
// would turn a forgotten `.array` into a field that silently disappeared. That is the same
// answer the `json` builtin gives (§12.14), and this is where the host, the http response
// of §12.11 and the CLI's --json all get it.
func (v Value) MarshalJSON() ([]byte, error) {
	if holdsSeq(v, 0) {
		return nil, errSeqJSON
	}
	return appendJSON(nil, v, "", 0), nil
}

// holdsSeq reports whether v is a seq or contains one. The depth cap is the one every
// other walk over user data carries: a script can build `a = []; a.push(a)` with one line
// and a Go stack overflow is not recoverable (A7). Beyond it the answer is "no", which
// only means the encoder writes what it would have written anyway.
func holdsSeq(v Value, depth int) bool {
	if depth > maxRenderDepth {
		return false
	}
	switch v.k {
	case KSeq:
		return true
	case KArray:
		for _, e := range *v.arr() {
			if holdsSeq(e, depth+1) {
				return true
			}
		}
	case KDict:
		found := false
		v.odict().Each(func(_, val Value) bool {
			if holdsSeq(val, depth+1) {
				found = true
				return false
			}
			return true
		})
		return found
	}
	return false
}

// UnmarshalJSON parses JSON into v, preserving object key order.
func (v *Value) UnmarshalJSON(b []byte) error {
	x, err := decodeJSON(b)
	if err != nil {
		return err
	}
	*v = x
	return nil
}

// ---------------------------------------------------------------------------
// Go interop
// ---------------------------------------------------------------------------

// Interface converts a Value to the closest Go value: nil, bool, int64, float64,
// string, *rx.Regexp, []any, map[string]any, *Func, time.Time. Dict order is lost in the
// Go map — use MarshalJSON when order matters.
func (v Value) Interface() any {
	switch v.k {
	case KNil:
		return nil
	case KBool:
		return v.n != 0
	case KInt:
		return v.n
	case KFloat:
		return v.float()
	case KString:
		return v.s
	case KRegex:
		return v.rx()
	case KFunc:
		return v.fn()
	case KTime:
		return v.Time()
	case KArray, KRange:
		xs := v.Elems()
		out := make([]any, len(xs))
		for i, e := range xs {
			out[i] = e.Interface()
		}
		return out
	case KDict:
		m := v.odict()
		out := make(map[string]any, m.Len())
		m.Each(func(k, val Value) bool {
			out[k.Str()] = val.Interface()
			return true
		})
		return out
	}
	return nil
}

// From converts a Go value into a Value. It handles the scalars, []T, map[string]T,
// json.RawMessage and time.Time directly; anything else goes through encoding/json,
// so any type that marshals to JSON is accepted.
func From(x any) (Value, error) {
	switch t := x.(type) {
	case nil:
		return Nil(), nil
	case Value:
		return t, nil
	case bool:
		return Bool(t), nil
	case int:
		return Int(int64(t)), nil
	case int8:
		return Int(int64(t)), nil
	case int16:
		return Int(int64(t)), nil
	case int32:
		return Int(int64(t)), nil
	case int64:
		return Int(t), nil
	case uint:
		return fromUint64(uint64(t)), nil
	case uint8:
		return Int(int64(t)), nil
	case uint16:
		return Int(int64(t)), nil
	case uint32:
		return Int(int64(t)), nil
	case uint64:
		return fromUint64(t), nil
	case float32:
		return Float(float64(t)), nil
	case float64:
		return Float(t), nil
	case string:
		return Str(t), nil
	case []byte:
		return Str(string(t)), nil
	case json.RawMessage:
		return decodeJSON(t)
	case time.Time:
		return timeOf(t), nil
	case *rx.Regexp:
		return regexOf(t), nil
	case *Func:
		return funcOf(t), nil
	case []any:
		xs := make([]Value, len(t))
		for i, e := range t {
			ev, err := From(e)
			if err != nil {
				return Nil(), err
			}
			xs[i] = ev
		}
		return arrayOf(xs), nil
	case map[string]any:
		return fromStringMap(t)
	case map[string]string:
		m := NewOrderedDict()
		for _, k := range sortedKeys(t) {
			_ = m.Set(Str(k), Str(t[k]))
		}
		return dictOf(m), nil
	}

	rv := reflect.ValueOf(x)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return Nil(), nil
		}
		return From(rv.Elem().Interface())
	case reflect.Slice, reflect.Array:
		xs := make([]Value, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			ev, err := From(rv.Index(i).Interface())
			if err != nil {
				return Nil(), err
			}
			xs[i] = ev
		}
		return arrayOf(xs), nil
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return Nil(), fmt.Errorf("mzs: cannot convert %T: map keys must be strings", x)
		}
		conv := make(map[string]any, rv.Len())
		for _, k := range rv.MapKeys() {
			conv[k.String()] = rv.MapIndex(k).Interface()
		}
		return fromStringMap(conv)
	case reflect.Struct:
		b, err := json.Marshal(x)
		if err != nil {
			return Nil(), fmt.Errorf("mzs: cannot convert %T: %w", x, err)
		}
		return decodeJSON(b)
	}
	return Nil(), fmt.Errorf("mzs: cannot convert %T to a value", x)
}

// fromUint64 lifts an unsigned Go integer, which is the one Go number that does not fit
// the value model: above math.MaxInt64 there is no Int for it. D9's rule decides what
// happens — an Int that would overflow becomes a Float rather than wrapping — so a host
// handing over a large unsigned id gets an approximate positive number instead of a
// silent negative one. uint8/16/32 always fit and take the direct path.
func fromUint64(n uint64) Value {
	if n > math.MaxInt64 {
		return Float(float64(n))
	}
	return Int(int64(n))
}

// fromStringMap converts a Go map into a dict deterministically: Go map iteration is
// randomised, so the keys are sorted before insertion (§8.13 requires determinism).
func fromStringMap(t map[string]any) (Value, error) {
	m := NewOrderedDict()
	for _, k := range sortedKeys(t) {
		ev, err := From(t[k])
		if err != nil {
			return Nil(), err
		}
		_ = m.Set(Str(k), ev)
	}
	return dictOf(m), nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// MustFrom is From for host code that knows its input converts; it panics otherwise.
// It is never reachable from a script.
func MustFrom(x any) Value {
	v, err := From(x)
	if err != nil {
		panic(err)
	}
	return v
}
