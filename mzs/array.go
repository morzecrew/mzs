package mzs

import (
	"sort"
	"strings"
)

// Array (§12.3) and Range (§12.10) methods.
//
// A Range is materialised by arrElems, under the collection cap, so every
// non-mutating array row is registered for KRange as well and there is exactly one
// implementation of `map`, `filter` and friends. The mutating rows stay on KArray:
// a range has nothing to mutate.
//
// A closure is an ordinary trailing argument (§4.2), never a block on the Ctx, so a row
// that takes one counts it in Min/Max and reaches it through Ctx.CallClosure. The two
// rows that accept *either* a value or a closure — `count` and `index` — tell them apart
// with arrArgs, which strips the closure off the positional arguments.
//
// Every name here is the one name of its operation (D17): there is no `size`, no
// `collect`, no `detect`, no `inject`, no `include?`, and no `<<`.

func init() {
	// Rows that only read their receiver, and therefore serve both kinds.
	pure := []Method{
		{Name: "len", Fn: arrLen},
		{Name: "empty", Fn: arrEmpty},
		{Name: "count", Max: 1, Fn: arrCount},
		{Name: "first", Max: 1, Fn: arrFirst},
		{Name: "last", Max: 1, Fn: arrLast},
		{Name: "has", Min: 1, Max: 1, Fn: arrHas},
		{Name: "index", Min: 1, Max: 1, Fn: arrIndex},
		{Name: "join", Max: 1, Fn: arrJoin},
		{Name: "map", Min: 1, Max: 1, Fn: arrMap},
		{Name: "flat_map", Min: 1, Max: 1, Fn: arrFlatMap},
		{Name: "each", Min: 1, Max: 1, Fn: arrEach},
		{Name: "each_with_index", Min: 1, Max: 1, Fn: arrEachWithIndex},
		{Name: "each_slice", Min: 1, Max: 2, Fn: arrEachSlice},
		{Name: "each_cons", Min: 1, Max: 2, Fn: arrEachCons},
		{Name: "filter", Min: 1, Max: 1, Fn: arrFilter},
		{Name: "reject", Min: 1, Max: 1, Fn: arrReject},
		{Name: "find", Min: 1, Max: 1, Fn: arrFind},
		{Name: "any", Max: 1, Fn: arrAny},
		{Name: "all", Max: 1, Fn: arrAll},
		{Name: "none", Max: 1, Fn: arrNone},
		{Name: "reduce", Min: 1, Max: 2, Fn: arrReduce},
		{Name: "sum", Max: 1, Fn: arrSum},
		{Name: "min", Max: 1, Fn: arrMin},
		{Name: "max", Max: 1, Fn: arrMax},
		{Name: "min_by", Min: 1, Max: 1, Fn: arrMinBy},
		{Name: "max_by", Min: 1, Max: 1, Fn: arrMaxBy},
		{Name: "sort", Max: 1, Fn: arrSort},
		{Name: "sort_by", Min: 1, Max: 1, Fn: arrSortBy},
		{Name: "group_by", Min: 1, Max: 1, Fn: arrGroupBy},
		{Name: "partition", Min: 1, Max: 1, Fn: arrPartition},
		{Name: "tally", Fn: arrTally},
		{Name: "uniq", Max: 1, Fn: arrUniq},
		{Name: "to_set", Fn: arrToSet},
		{Name: "union", Min: 1, Max: -1, Fn: arrUnion},
		{Name: "intersect", Min: 1, Max: -1, Fn: arrIntersect},
		{Name: "difference", Min: 1, Max: -1, Fn: arrDifference},
		{Name: "subset", Min: 1, Max: 1, Fn: arrSubset},
		{Name: "reverse", Fn: arrReverse},
		{Name: "flatten", Max: 1, Fn: arrFlatten},
		{Name: "compact", Fn: arrCompact},
		{Name: "slice", Min: 1, Max: 2, Fn: arrSlice},
		{Name: "take", Min: 1, Max: 1, Fn: arrTake},
		{Name: "drop", Min: 1, Max: 1, Fn: arrDrop},
		{Name: "take_while", Min: 1, Max: 1, Fn: arrTakeWhile},
		{Name: "drop_while", Min: 1, Max: 1, Fn: arrDropWhile},
		{Name: "zip", Max: -1, Fn: arrZip},
		{Name: "pack_bytes", Fn: arrPackBytes},
		{Name: "array", Fn: arrArray},
		{Name: "dict", Fn: arrDict},
		{Name: "json", Fn: arrJSON},
		{Name: "sample", Fn: arrSample, NeedsRand: true},
		{Name: "shuffle", Fn: arrShuffle, NeedsRand: true},
	}
	RegisterMethods(KArray, pure...)
	RegisterMethods(KRange, pure...)

	// `dig` is an array row (§12.3) and a dict row (§12.4); both walk the same path,
	// so a mixed structure parsed out of JSON digs through in one call (rows 38/39).
	RegisterMethod(KArray, Method{Name: "dig", Min: 1, Max: -1, Fn: arrDig})

	// step is the one row a range has and an array does not (§12.10).
	RegisterMethod(KRange, Method{Name: "step", Min: 1, Max: 2, Fn: rngStep})

	RegisterMethods(KArray,
		Method{Name: "push", Max: -1, Fn: arrPush},
		Method{Name: "pop", Fn: arrPop},
		Method{Name: "shift", Fn: arrShift},
		Method{Name: "unshift", Max: -1, Fn: arrUnshift},
		Method{Name: "insert", Min: 1, Max: -1, Fn: arrInsert},
		Method{Name: "delete_at", Min: 1, Max: 1, Fn: arrDeleteAt},
		Method{Name: "delete", Min: 1, Max: 1, Fn: arrDelete},
		Method{Name: "concat", Min: 1, Max: -1, Fn: arrConcat},
		// The two mutating variants of §12.3, named so at the call site: each is its
		// pure twin plus a write-back, and there is no `!` spelling of anything.
		Method{Name: "sort_in_place", Max: 1, Fn: arrInPlace(arrSort)},
		Method{Name: "reverse_in_place", Fn: arrInPlace(arrReverse)},
	)
}

// arrInPlace turns a pure row into its mutating twin: run the pure form, then replace
// the receiver's backing slice. Every pure row allocates a fresh slice, so the copy
// never aliases the array it is about to overwrite.
func arrInPlace(f func(c *Ctx, recv Value, args []Value) (Value, error)) func(c *Ctx, recv Value, args []Value) (Value, error) {
	return func(c *Ctx, recv Value, args []Value) (Value, error) {
		p, err := arrTarget(c, recv)
		if err != nil {
			return Nil(), err
		}
		out, err := f(c, recv, args)
		if err != nil {
			return Nil(), err
		}
		xs := out.Elems()
		next := make([]Value, len(xs))
		copy(next, xs)
		*p = next
		return recv, nil
	}
}

// ---------------------------------------------------------------------------
// Receiver and argument plumbing
// ---------------------------------------------------------------------------

// arrElems is the one place a receiver becomes a slice of elements. For an array it
// returns the live backing slice: a closure that pushes while iterating therefore does
// not extend the loop, which is what keeps `each` bounded. A range is materialised
// once, charged against MaxCollection and the step budget.
func arrElems(c *Ctx, v Value) ([]Value, error) {
	switch v.Kind() {
	case KArray:
		return *v.arr(), nil
	case KRange:
		r := v.rng()
		n := r.Len()
		if err := c.CheckCollection(n); err != nil {
			return nil, err
		}
		if err := c.Step(int64(n)); err != nil {
			return nil, err
		}
		return r.Elems(), nil
	}
	return nil, c.TypeErrorf("%s expects an array, got %s", c.Name(), v.Kind())
}

// arrCheck is the receiver check for the rows that answer from the header alone and
// never materialise anything.
func arrCheck(c *Ctx, v Value) error {
	if v.Kind() == KArray || v.Kind() == KRange {
		return nil
	}
	return c.TypeErrorf("%s expects an array, got %s", c.Name(), v.Kind())
}

// arrTarget is arrElems for the mutating rows, which need the slice header itself so
// every alias of the array sees the change (§7.1).
func arrTarget(c *Ctx, v Value) (*[]Value, error) {
	if p := v.arr(); p != nil {
		return p, nil
	}
	return nil, c.TypeErrorf("%s expects an array, got %s", c.Name(), v.Kind())
}

// arrArgs is the positional arguments with the trailing closure removed. A closure is
// an ordinary last argument (§4.2) and Ctx.Closure is that same value, so this is how
// `xs.count(1)` is told from `xs.count { it > 1 }` without inspecting kinds by hand.
func arrArgs(c *Ctx, args []Value) []Value {
	if c.HasClosure() && len(args) > 0 {
		return args[:len(args)-1]
	}
	return args
}

// arrArg is the i-th positional argument, the trailing closure excluded, or Nil when
// the call supplied none. It keeps a row that takes both — `each_slice(2) { … }` — from
// having to count arguments itself.
func arrArg(c *Ctx, args []Value, i int) Value {
	if pos := arrArgs(c, args); i < len(pos) {
		return pos[i]
	}
	return Nil()
}

// arrOnlyClosure rejects a value argument for a row whose single optional argument is a
// closure (`any`, `sum`, `sort`, `uniq`, …), so a mistyped call fails loudly instead of
// being ignored.
func arrOnlyClosure(c *Ctx, args []Value) error {
	if pos := arrArgs(c, args); len(pos) > 0 {
		return c.TypeErrorf("%s expects a closure, got %s", c.Name(), pos[0].TypeName())
	}
	return nil
}

// arrIter charges one step per element before a loop. The closure calls inside the loop
// charge themselves, so this only covers the iteration itself.
func arrIter(c *Ctx, xs []Value) error { return c.Step(int64(len(xs))) }

// arrGrow bounds a result that is about to be built (§14.2).
func arrGrow(c *Ctx, n int) error { return c.CheckCollection(n) }

// arrAdd is the numeric `+` that `sum` and `reduce` need. Int overflow promotes to
// Float rather than wrapping (D9).
func arrAdd(c *Ctx, a, b Value) (Value, error) {
	if !a.IsNum() || !b.IsNum() {
		return Nil(), c.TypeErrorf("%s expects numbers, got %s and %s", c.Name(), a.TypeName(), b.TypeName())
	}
	if a.Kind() == KInt && b.Kind() == KInt {
		x, y := a.Int(), b.Int()
		s := x + y
		if (x > 0 && y > 0 && s < 0) || (x < 0 && y < 0 && s >= 0) {
			return Float(float64(x) + float64(y)), nil
		}
		return Int(s), nil
	}
	return Float(a.Float() + b.Float()), nil
}

// arrCompare is `<=>` with a diagnostic: sorting a mixed array is an error, not a
// silent arbitrary order (§7.5).
func arrCompare(c *Ctx, a, b Value) (int, error) {
	if n, ok := compare(a, b); ok {
		return n, nil
	}
	return 0, c.TypeErrorf("comparison of %s with %s failed", a.TypeName(), b.TypeName())
}

// arrSeen is the membership set uniq/tally/group_by share. Hashable elements go into
// the map; arrays, dicts and functions fall back to a linear Equal scan, so `uniq`
// still works on nested data without ever raising.
type arrSeen struct {
	keys map[dictKey]bool
	odd  []Value
}

func newArrSeen(n int) *arrSeen { return &arrSeen{keys: make(map[dictKey]bool, n)} }

// add reports whether v had not been seen before.
func (s *arrSeen) add(v Value) bool {
	k, err := hashKey(v)
	if err == nil {
		if s.keys[k] {
			return false
		}
		s.keys[k] = true
		return true
	}
	for _, o := range s.odd {
		if o.Equal(v) {
			return false
		}
	}
	s.odd = append(s.odd, v)
	return true
}

// has is add without the write, which is what a membership test over a set already built
// needs (`intersect`, `difference`, `subset`).
func (s *arrSeen) has(v Value) bool {
	if k, err := hashKey(v); err == nil {
		return s.keys[k]
	}
	for _, o := range s.odd {
		if o.Equal(v) {
			return true
		}
	}
	return false
}

// arrSetOf reads one argument of a set operation into a membership set. Every argument is
// an array or a range, and each is charged one step per element before it is scanned.
func arrSetOf(c *Ctx, v Value) (*arrSeen, error) {
	ys, err := arrElems(c, v)
	if err != nil {
		return nil, err
	}
	if err := arrIter(c, ys); err != nil {
		return nil, err
	}
	s := newArrSeen(len(ys))
	for _, y := range ys {
		s.add(y)
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Size and access
// ---------------------------------------------------------------------------

// arrLen answers from the range's endpoints, so `(1..1_000_000_000).len` costs nothing
// and never trips the collection cap.
func arrLen(c *Ctx, recv Value, args []Value) (Value, error) {
	if err := arrCheck(c, recv); err != nil {
		return Nil(), err
	}
	return Int(int64(recv.Len())), nil
}

func arrEmpty(c *Ctx, recv Value, args []Value) (Value, error) {
	if err := arrCheck(c, recv); err != nil {
		return Nil(), err
	}
	return Bool(recv.Len() == 0), nil
}

// arrCount is three methods in one row (§12.3): the element count, the number of equal
// elements, and the number of elements a closure accepts.
func arrCount(c *Ctx, recv Value, args []Value) (Value, error) {
	pos := arrArgs(c, args)
	if len(pos) == 0 && !c.HasClosure() {
		return arrLen(c, recv, nil)
	}
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	n := 0
	if len(pos) == 1 {
		for _, x := range xs {
			if x.Equal(pos[0]) {
				n++
			}
		}
		return Int(int64(n)), nil
	}
	for _, x := range xs {
		r, err := c.CallClosure(x)
		if err != nil {
			return Nil(), err
		}
		if r.Truthy() {
			n++
		}
	}
	return Int(int64(n)), nil
}

// arrFirst returns the element without an argument and a prefix with one, which is what
// `xs.first ?? fallback` and `xs.first(2)` both need (§12.3).
func arrFirst(c *Ctx, recv Value, args []Value) (Value, error) {
	if len(args) == 0 {
		if err := arrCheck(c, recv); err != nil {
			return Nil(), err
		}
		return recv.Index(0), nil
	}
	n, err := arrCountArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	return arrPrefix(c, recv, n)
}

func arrLast(c *Ctx, recv Value, args []Value) (Value, error) {
	if len(args) == 0 {
		if err := arrCheck(c, recv); err != nil {
			return Nil(), err
		}
		return recv.Index(-1), nil
	}
	n, err := arrCountArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	return arrSuffix(c, recv, n)
}

// arrPrefix and arrSuffix take n elements from one end. A range is read through its
// endpoints rather than materialised, so `(1..1_000_000_000).first(3)` costs three
// values instead of tripping the collection cap.
func arrPrefix(c *Ctx, recv Value, n int) (Value, error) {
	if r := recv.rng(); r != nil {
		return arrRangeSlice(c, r, 0, n)
	}
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if n > len(xs) {
		n = len(xs)
	}
	return arrCopy(xs[:n]), nil
}

func arrSuffix(c *Ctx, recv Value, n int) (Value, error) {
	if r := recv.rng(); r != nil {
		total := r.Len()
		if n > total {
			n = total
		}
		return arrRangeSlice(c, r, total-n, n)
	}
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if n > len(xs) {
		n = len(xs)
	}
	return arrCopy(xs[len(xs)-n:]), nil
}

func arrRangeSlice(c *Ctx, r *Range, from, n int) (Value, error) {
	if total := r.Len(); from+n > total {
		n = total - from
	}
	if n < 0 {
		n = 0
	}
	if err := arrGrow(c, n); err != nil {
		return Nil(), err
	}
	if err := c.Step(int64(n)); err != nil {
		return Nil(), err
	}
	out := make([]Value, n)
	for i := range out {
		out[i] = Int(r.At(from + i))
	}
	return arrayOf(out), nil
}

// arrCountArg validates an element count: a negative one is an argument error.
func arrCountArg(c *Ctx, v Value) (int, error) {
	n := v.Int()
	if n < 0 {
		return 0, c.ArgErrorf("%s expects a non-negative count, got %d", c.Name(), n)
	}
	if n > int64(int(^uint(0)>>1)) {
		return 0, c.ArgErrorf("%s: count is too large", c.Name())
	}
	return int(n), nil
}

func arrCopy(xs []Value) Value {
	out := make([]Value, len(xs))
	copy(out, xs)
	return arrayOf(out)
}

// arrHas is membership by `==` (§12.3), and the endpoint test for a range (§12.10), so
// `(1..10_000_000).has(3)` does not materialise anything. It is also what an `in`
// pattern calls (§5.3).
func arrHas(c *Ctx, recv Value, args []Value) (Value, error) {
	if recv.Kind() == KRange {
		r := recv.rng()
		switch args[0].Kind() {
		case KInt:
			return Bool(r.Contains(args[0].Int())), nil
		case KFloat:
			f := args[0].Float()
			if r.Excl {
				return Bool(f >= float64(r.Lo) && f < float64(r.Hi)), nil
			}
			return Bool(f >= float64(r.Lo) && f <= float64(r.Hi)), nil
		}
		return Bool(false), nil
	}
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	for _, x := range xs {
		if x.Equal(args[0]) {
			return Bool(true), nil
		}
	}
	return Bool(false), nil
}

// arrIndex takes either a value to compare or a closure to test.
func arrIndex(c *Ctx, recv Value, args []Value) (Value, error) {
	pos := arrArgs(c, args)
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	for i, x := range xs {
		if len(pos) == 1 {
			if x.Equal(pos[0]) {
				return Int(int64(i)), nil
			}
			continue
		}
		r, err := c.CallClosure(x)
		if err != nil {
			return Nil(), err
		}
		if r.Truthy() {
			return Int(int64(i)), nil
		}
	}
	return Nil(), nil
}

// arrJoin renders each element with `str` (§12.3), so a nested array joins as its JSON
// form rather than being flattened.
func arrJoin(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	sep := ""
	if len(args) == 1 {
		sep = args[0].Str()
	}
	var sb strings.Builder
	for i, x := range xs {
		if i > 0 {
			sb.WriteString(sep)
		}
		sb.WriteString(x.Str())
		if err := c.CheckString(sb.Len()); err != nil {
			return Nil(), err
		}
	}
	return Str(sb.String()), nil
}

// arrPackBytes is the inverse of String#bytes (§12.2): one element, one byte. Without it
// `bytes` is a one-way street — you can take a string apart and never put it back — which
// is what makes the bit rows of §12.5 usable on data rather than only on flags.
//
// Every element must be an Int in 0..255 and a bad one names its index: a byte quietly
// truncated from 300 to 44 is a corrupt string that only surfaces much later.
//
// The result is bytes, not runes, so packing arbitrary values can build a string that is
// not valid UTF-8 — exactly as `io.read` of a binary file can (§12.13). The rune-based
// rows then see U+FFFD, and JSON encoding replaces the bad bytes; nothing panics.
func arrPackBytes(c *Ctx, recv Value, _ []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	if err := c.CheckString(len(xs)); err != nil {
		return Nil(), err
	}
	buf := make([]byte, len(xs))
	for i, x := range xs {
		if x.Kind() != KInt {
			return Nil(), c.TypeErrorf("pack_bytes: expected a byte in 0..255 at element %d, got %s",
				i, x.TypeName())
		}
		if x.n < 0 || x.n > 255 {
			return Nil(), c.ArgErrorf("pack_bytes: expected a byte in 0..255 at element %d, got %d", i, x.n)
		}
		buf[i] = byte(x.n)
	}
	return Str(string(buf)), nil
}

// arrDig is the nil-safe nested lookup of §12.3, shared with the dict row of §12.4.
func arrDig(c *Ctx, recv Value, args []Value) (Value, error) {
	if err := arrCheck(c, recv); err != nil {
		return Nil(), err
	}
	return digPath(c, recv, args)
}

// digPath walks one key at a time through Arrays and Dicts and stops at the first nil,
// so `json.parse(s).dig(0, "generated_text")` on an empty array is nil rather than an
// error — which is exactly what makes corpus rows 38 and 39 read the way they do. A key
// that cannot address the current step (a string index into an array, any key into a
// scalar) ends the walk at nil too: `dig` is nil-safe at every step and never raises.
func digPath(c *Ctx, recv Value, keys []Value) (Value, error) {
	cur := recv
	for _, k := range keys {
		if err := c.Step(1); err != nil {
			return Nil(), err
		}
		switch cur.Kind() {
		case KDict:
			cur = cur.Get(k)
		case KArray, KRange:
			if !k.IsNum() {
				return Nil(), nil
			}
			cur = cur.Index(int(k.Int()))
		default:
			return Nil(), nil
		}
		if cur.IsNil() {
			return Nil(), nil
		}
	}
	return cur, nil
}

// ---------------------------------------------------------------------------
// Iteration
// ---------------------------------------------------------------------------

func arrMap(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	out := make([]Value, len(xs))
	for i, x := range xs {
		r, err := c.CallClosure(x)
		if err != nil {
			return Nil(), err
		}
		out[i] = r
	}
	return arrayOf(out), nil
}

func arrFlatMap(c *Ctx, recv Value, args []Value) (Value, error) {
	mapped, err := arrMap(c, recv, args)
	if err != nil {
		return Nil(), err
	}
	return arrFlattenTo(c, mapped.Elems(), 1)
}

func arrEach(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	for _, x := range xs {
		if _, err := c.CallClosure(x); err != nil {
			return Nil(), err
		}
	}
	return recv, nil
}

// arrEachWithIndex always passes both values; a one-parameter closure simply drops the
// index, which is the lenient closure arity of §7.7.
func arrEachWithIndex(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	for i, x := range xs {
		if _, err := c.CallClosure(x, Int(int64(i))); err != nil {
			return Nil(), err
		}
	}
	return recv, nil
}

// arrEachSlice without a closure returns the chunks, which is what makes
// `.each_slice(2).array` (corpus rows 51 and 52) work without an iterator type.
func arrEachSlice(c *Ctx, recv Value, args []Value) (Value, error) {
	n, err := arrChunkArg(c, arrArg(c, args, 0))
	if err != nil {
		return Nil(), err
	}
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	var chunks []Value
	for i := 0; i < len(xs); i += n {
		end := i + n
		if end > len(xs) {
			end = len(xs)
		}
		chunk := arrCopy(xs[i:end])
		if c.HasClosure() {
			if _, err := c.CallClosure(chunk); err != nil {
				return Nil(), err
			}
			continue
		}
		chunks = append(chunks, chunk)
	}
	if c.HasClosure() {
		return recv, nil
	}
	return arrayOf(chunks), nil
}

func arrEachCons(c *Ctx, recv Value, args []Value) (Value, error) {
	n, err := arrChunkArg(c, arrArg(c, args, 0))
	if err != nil {
		return Nil(), err
	}
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	var out []Value
	for i := 0; i+n <= len(xs); i++ {
		win := arrCopy(xs[i : i+n])
		if c.HasClosure() {
			if _, err := c.CallClosure(win); err != nil {
				return Nil(), err
			}
			continue
		}
		out = append(out, win)
	}
	if c.HasClosure() {
		return recv, nil
	}
	return arrayOf(out), nil
}

func arrChunkArg(c *Ctx, v Value) (int, error) {
	n := v.Int()
	if n <= 0 {
		return 0, c.ArgErrorf("%s expects a positive size, got %d", c.Name(), n)
	}
	return int(n), nil
}

func arrFilter(c *Ctx, recv Value, args []Value) (Value, error) {
	return arrSieve(c, recv, true)
}

func arrReject(c *Ctx, recv Value, args []Value) (Value, error) {
	return arrSieve(c, recv, false)
}

// arrSieve is `filter` and `reject`, which differ only in the sense of the test. There
// is no second spelling of either (D17).
func arrSieve(c *Ctx, recv Value, keep bool) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	out := []Value{}
	for _, x := range xs {
		r, err := c.CallClosure(x)
		if err != nil {
			return Nil(), err
		}
		if r.Truthy() == keep {
			out = append(out, x)
		}
	}
	return arrayOf(out), nil
}

func arrFind(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	for _, x := range xs {
		r, err := c.CallClosure(x)
		if err != nil {
			return Nil(), err
		}
		if r.Truthy() {
			return x, nil
		}
	}
	return Nil(), nil
}

func arrAny(c *Ctx, recv Value, args []Value) (Value, error) {
	return arrQuantify(c, recv, args, "any")
}

func arrAll(c *Ctx, recv Value, args []Value) (Value, error) {
	return arrQuantify(c, recv, args, "all")
}

func arrNone(c *Ctx, recv Value, args []Value) (Value, error) {
	return arrQuantify(c, recv, args, "none")
}

// arrQuantify implements any/all/none. Without a closure the elements are tested for
// truthiness, so `[nil, false].any` is false and `[0, ""].any` is true (D6).
func arrQuantify(c *Ctx, recv Value, args []Value, mode string) (Value, error) {
	if err := arrOnlyClosure(c, args); err != nil {
		return Nil(), err
	}
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	for _, x := range xs {
		hit := x.Truthy()
		if c.HasClosure() {
			r, err := c.CallClosure(x)
			if err != nil {
				return Nil(), err
			}
			hit = r.Truthy()
		}
		switch mode {
		case "any":
			if hit {
				return Bool(true), nil
			}
		case "all":
			if !hit {
				return Bool(false), nil
			}
		case "none":
			if hit {
				return Bool(false), nil
			}
		}
	}
	return Bool(mode != "any"), nil
}

// arrReduce seeds with the first element when no initial value is given, so both
// `[1,2,3].reduce { (a, b) -> a + b }` and `[1,2,3].reduce(10) { … }` work.
func arrReduce(c *Ctx, recv Value, args []Value) (Value, error) {
	pos := arrArgs(c, args)
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	var acc Value
	start := 0
	if len(pos) == 1 {
		acc = pos[0]
	} else {
		if len(xs) == 0 {
			return Nil(), nil
		}
		acc, start = xs[0], 1
	}
	for _, x := range xs[start:] {
		r, err := c.CallClosure(acc, x)
		if err != nil {
			return Nil(), err
		}
		acc = r
	}
	return acc, nil
}

// arrSum is a numeric sum; with a closure it sums the closure's results (§12.3).
func arrSum(c *Ctx, recv Value, args []Value) (Value, error) {
	if err := arrOnlyClosure(c, args); err != nil {
		return Nil(), err
	}
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	acc := Int(0)
	for _, x := range xs {
		v := x
		if c.HasClosure() {
			r, err := c.CallClosure(x)
			if err != nil {
				return Nil(), err
			}
			v = r
		}
		acc, err = arrAdd(c, acc, v)
		if err != nil {
			return Nil(), err
		}
	}
	return acc, nil
}

// arrMin and arrMax take an optional comparator closure, the same `(a, b) -> int` shape
// `sort` takes; the key form is min_by/max_by.
func arrMin(c *Ctx, recv Value, args []Value) (Value, error) {
	return arrExtreme(c, recv, args, -1)
}

func arrMax(c *Ctx, recv Value, args []Value) (Value, error) {
	return arrExtreme(c, recv, args, 1)
}

func arrExtreme(c *Ctx, recv Value, args []Value, want int) (Value, error) {
	if err := arrOnlyClosure(c, args); err != nil {
		return Nil(), err
	}
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	if len(xs) == 0 {
		return Nil(), nil
	}
	best := xs[0]
	for _, x := range xs[1:] {
		var n int
		if c.HasClosure() {
			r, err := c.CallClosure(x, best)
			if err != nil {
				return Nil(), err
			}
			n = int(r.Int())
		} else {
			n, err = arrCompare(c, x, best)
			if err != nil {
				return Nil(), err
			}
		}
		if (want < 0 && n < 0) || (want > 0 && n > 0) {
			best = x
		}
	}
	return best, nil
}

func arrMinBy(c *Ctx, recv Value, args []Value) (Value, error) {
	return arrExtremeBy(c, recv, -1)
}

func arrMaxBy(c *Ctx, recv Value, args []Value) (Value, error) {
	return arrExtremeBy(c, recv, 1)
}

func arrExtremeBy(c *Ctx, recv Value, want int) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	if len(xs) == 0 {
		return Nil(), nil
	}
	best, bestKey := Nil(), Nil()
	for i, x := range xs {
		key, err := c.CallClosure(x)
		if err != nil {
			return Nil(), err
		}
		if i == 0 {
			best, bestKey = x, key
			continue
		}
		n, err := arrCompare(c, key, bestKey)
		if err != nil {
			return Nil(), err
		}
		if (want < 0 && n < 0) || (want > 0 && n > 0) {
			best, bestKey = x, key
		}
	}
	return best, nil
}

// ---------------------------------------------------------------------------
// Ordering and grouping
// ---------------------------------------------------------------------------

// arrSort is stable and always returns a new array. A comparator closure that fails
// stops the comparison — sort.SliceStable cannot abort, so the first error is kept and
// every later comparison answers false, then the error is returned instead of a
// half-sorted result.
func arrSort(c *Ctx, recv Value, args []Value) (Value, error) {
	if err := arrOnlyClosure(c, args); err != nil {
		return Nil(), err
	}
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	out := make([]Value, len(xs))
	copy(out, xs)
	var failed error
	sort.SliceStable(out, func(i, j int) bool {
		if failed != nil {
			return false
		}
		var n int
		var err error
		if c.HasClosure() {
			var r Value
			r, err = c.CallClosure(out[i], out[j])
			n = int(r.Int())
		} else {
			n, err = arrCompare(c, out[i], out[j])
		}
		if err != nil {
			failed = err
			return false
		}
		return n < 0
	})
	if failed != nil {
		return Nil(), failed
	}
	return arrayOf(out), nil
}

// arrSortBy computes each key once, so a closure with a cost is called len(xs) times,
// not O(n log n) times.
func arrSortBy(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	type pair struct{ key, val Value }
	pairs := make([]pair, len(xs))
	for i, x := range xs {
		k, err := c.CallClosure(x)
		if err != nil {
			return Nil(), err
		}
		pairs[i] = pair{key: k, val: x}
	}
	var failed error
	sort.SliceStable(pairs, func(i, j int) bool {
		if failed != nil {
			return false
		}
		n, err := arrCompare(c, pairs[i].key, pairs[j].key)
		if err != nil {
			failed = err
			return false
		}
		return n < 0
	})
	if failed != nil {
		return Nil(), failed
	}
	out := make([]Value, len(pairs))
	for i, p := range pairs {
		out[i] = p.val
	}
	return arrayOf(out), nil
}

// arrGroupBy returns a Dict from the closure's value to the elements that produced it,
// in first-seen order (D11).
func arrGroupBy(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	d := NewOrderedDict()
	for _, x := range xs {
		k, err := c.CallClosure(x)
		if err != nil {
			return Nil(), err
		}
		bucket, ok := d.Get(k)
		if !ok {
			bucket = Array()
			if err := d.Set(k, bucket); err != nil {
				return Nil(), c.TypeErrorf("%s", err.Error())
			}
		}
		bucket.Append(x)
	}
	return dictOf(d), nil
}

func arrPartition(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	yes, no := []Value{}, []Value{}
	for _, x := range xs {
		r, err := c.CallClosure(x)
		if err != nil {
			return Nil(), err
		}
		if r.Truthy() {
			yes = append(yes, x)
		} else {
			no = append(no, x)
		}
	}
	return Array(arrayOf(yes), arrayOf(no)), nil
}

// arrTally counts occurrences into a Dict, keyed by the element itself.
func arrTally(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	d := NewOrderedDict()
	for _, x := range xs {
		n, _ := d.Get(x)
		if err := d.Set(x, Int(n.Int()+1)); err != nil {
			return Nil(), c.TypeErrorf("%s", err.Error())
		}
	}
	return dictOf(d), nil
}

// arrUniq keeps the first occurrence (§12.3); with a closure the closure's value is the
// identity.
func arrUniq(c *Ctx, recv Value, args []Value) (Value, error) {
	if err := arrOnlyClosure(c, args); err != nil {
		return Nil(), err
	}
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	seen := newArrSeen(len(xs))
	out := []Value{}
	for _, x := range xs {
		key := x
		if c.HasClosure() {
			k, err := c.CallClosure(x)
			if err != nil {
				return Nil(), err
			}
			key = k
		}
		if seen.add(key) {
			out = append(out, x)
		}
	}
	return arrayOf(out), nil
}

func arrReverse(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	out := make([]Value, len(xs))
	for i, x := range xs {
		out[len(xs)-1-i] = x
	}
	return arrayOf(out), nil
}

// arrFlattenMaxDepth bounds recursion so a self-referential array (`a.push(a)`)
// reports an error instead of overflowing the Go stack, which is not recoverable.
const arrFlattenMaxDepth = 100

func arrFlatten(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	depth := -1
	if len(args) == 1 {
		depth = int(args[0].Int())
	}
	return arrFlattenTo(c, xs, depth)
}

func arrFlattenTo(c *Ctx, xs []Value, depth int) (Value, error) {
	out := []Value{}
	out, err := arrFlattenInto(c, out, xs, depth, 0)
	if err != nil {
		return Nil(), err
	}
	return arrayOf(out), nil
}

func arrFlattenInto(c *Ctx, dst []Value, xs []Value, depth, level int) ([]Value, error) {
	if level > arrFlattenMaxDepth {
		return nil, c.ArgErrorf("flatten: nesting is too deep (%d levels)", level)
	}
	if err := c.Step(int64(len(xs))); err != nil {
		return nil, err
	}
	for _, x := range xs {
		nested := x.Kind() == KArray || x.Kind() == KRange
		if nested && (depth < 0 || level < depth) {
			inner, err := arrElems(c, x)
			if err != nil {
				return nil, err
			}
			dst, err = arrFlattenInto(c, dst, inner, depth, level+1)
			if err != nil {
				return nil, err
			}
			continue
		}
		dst = append(dst, x)
		if err := arrGrow(c, len(dst)); err != nil {
			return nil, err
		}
	}
	return dst, nil
}

func arrCompact(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	out := []Value{}
	for _, x := range xs {
		if !x.IsNil() {
			out = append(out, x)
		}
	}
	return arrayOf(out), nil
}

// ---------------------------------------------------------------------------
// Sets (§12.3)
// ---------------------------------------------------------------------------
//
// There is no ninth kind for a set and no module pretending to be one: a set is a dict
// whose values are `true`, which is what a script already writes by hand the moment it
// needs "have I seen this", and the operations that were missing live here, on the arrays
// where they are wanted.
//
// The four of them answer with a **set**: the first occurrence of each element wins and
// nothing repeats. `intersect`, `difference` and `subset` read the receiver and keep its
// order; `union` has elements the receiver never had, so it is the receiver's order first
// and then each argument's, in the order the arguments were given. Both are decided by the
// input rather than by a hash, which is what §8.13 asks for.
//
// That is what tells them from `+` and `-`, which are the *sequence* operations and keep
// every element they were given — one operation, one name (D17), and the pair that looks
// alike is the pair that must be written down:
//
//	[1, 1, 2] + [2]                  # [1, 1, 2, 2]   — concatenation
//	[1, 1, 2].union([2])             # [1, 2]         — the set of both
//	[1, 1, 2] - [2]                  # [1, 1]         — removal
//	[1, 1, 2].difference([2])        # [1]            — the set of what is left
//
// Membership is `==` (§7.4) through arrSeen, so an array of arrays works as well as an
// array of strings: hashable elements go through the map and the rest through a linear
// scan. `to_set` is the one row that cannot do that — a dict key must be hashable (§7.6) —
// and it says so with the same diagnostic `tally` gives.

// arrToSet is `to_set`: the distinct elements as a dict, every value `true`. It is the
// membership half of a set, the half a dict already is: `xs.to_set.has(x)` is the O(1)
// question `xs.has(x)` answers in O(n).
func arrToSet(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	d := NewOrderedDictCap(len(xs))
	for _, x := range xs {
		if err := d.Set(x, Bool(true)); err != nil {
			return Nil(), c.TypeErrorf("%s", err.Error())
		}
	}
	return dictOf(d), nil
}

// arrUnion is every element of the receiver, in its order, and then every element of each
// argument that has not been seen yet, in the order the arguments were given.
func arrUnion(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	seen := newArrSeen(len(xs))
	out := []Value{}
	take := func(ys []Value) error {
		for _, y := range ys {
			if !seen.add(y) {
				continue
			}
			out = append(out, y)
			if err := arrGrow(c, len(out)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := take(xs); err != nil {
		return Nil(), err
	}
	for _, a := range args {
		ys, err := arrElems(c, a)
		if err != nil {
			return Nil(), err
		}
		if err := arrIter(c, ys); err != nil {
			return Nil(), err
		}
		if err := take(ys); err != nil {
			return Nil(), err
		}
	}
	return arrayOf(out), nil
}

// arrIntersect is the elements of the receiver that every argument has too.
func arrIntersect(c *Ctx, recv Value, args []Value) (Value, error) {
	return arrSetFilter(c, recv, args, true)
}

// arrDifference is the elements of the receiver that no argument has.
func arrDifference(c *Ctx, recv Value, args []Value) (Value, error) {
	return arrSetFilter(c, recv, args, false)
}

// arrSetFilter is the body both of them share: build a membership set per argument, then
// keep the receiver's elements that are in all of them (want) or in none (!want).
func arrSetFilter(c *Ctx, recv Value, args []Value, want bool) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	others := make([]*arrSeen, len(args))
	for i, a := range args {
		if others[i], err = arrSetOf(c, a); err != nil {
			return Nil(), err
		}
	}
	seen := newArrSeen(len(xs))
	out := []Value{}
	for _, x := range xs {
		keep := true
		for _, o := range others {
			if o.has(x) != want {
				keep = false
				break
			}
		}
		if keep && seen.add(x) {
			out = append(out, x)
		}
	}
	return arrayOf(out), nil
}

// arrSubset asks whether every element of the receiver is in the argument. An empty
// receiver is a subset of everything, which is the only answer that keeps
// `a.subset(b) && b.subset(a)` equivalent to "the same set".
func arrSubset(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	other, err := arrSetOf(c, args[0])
	if err != nil {
		return Nil(), err
	}
	for _, x := range xs {
		if !other.has(x) {
			return Bool(false), nil
		}
	}
	return Bool(true), nil
}

// ---------------------------------------------------------------------------
// Slicing
// ---------------------------------------------------------------------------

// arrSlice is `slice(i, n = 1)` and, additionally, `slice(range)`. It always returns an
// array (§12.3), and nil when the start index is outside the array.
func arrSlice(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	start, n := 0, 1
	if args[0].Kind() == KRange {
		if len(args) > 1 {
			return Nil(), c.ArgErrorf("%s takes no length with a range", c.Name())
		}
		r := args[0].rng()
		lo, hi := int(r.Lo), int(r.Hi)
		if lo < 0 {
			lo += len(xs)
		}
		if hi < 0 {
			hi += len(xs)
		}
		if r.Excl {
			hi--
		}
		if hi >= len(xs) {
			hi = len(xs) - 1
		}
		start, n = lo, hi-lo+1
		if n < 0 {
			n = 0
		}
	} else {
		start = int(args[0].Int())
		if start < 0 {
			start += len(xs)
		}
		if len(args) == 2 {
			cnt, err := arrCountArg(c, args[1])
			if err != nil {
				return Nil(), err
			}
			n = cnt
		}
	}
	if start < 0 || start > len(xs) {
		return Nil(), nil
	}
	if start+n > len(xs) {
		n = len(xs) - start
	}
	return arrCopy(xs[start : start+n]), nil
}

func arrTake(c *Ctx, recv Value, args []Value) (Value, error) {
	n, err := arrCountArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	return arrPrefix(c, recv, n)
}

func arrDrop(c *Ctx, recv Value, args []Value) (Value, error) {
	n, err := arrCountArg(c, args[0])
	if err != nil {
		return Nil(), err
	}
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if n > len(xs) {
		n = len(xs)
	}
	return arrCopy(xs[n:]), nil
}

func arrTakeWhile(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	for i, x := range xs {
		r, err := c.CallClosure(x)
		if err != nil {
			return Nil(), err
		}
		if !r.Truthy() {
			return arrCopy(xs[:i]), nil
		}
	}
	return arrCopy(xs), nil
}

func arrDropWhile(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	for i, x := range xs {
		r, err := c.CallClosure(x)
		if err != nil {
			return Nil(), err
		}
		if !r.Truthy() {
			return arrCopy(xs[i:]), nil
		}
	}
	return Array(), nil
}

// arrZip pairs element i of the receiver with element i of every other collection;
// a short partner contributes nil (§12.3).
func arrZip(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	others := make([][]Value, len(args))
	for i, a := range args {
		ys, err := arrElems(c, a)
		if err != nil {
			return Nil(), err
		}
		others[i] = ys
	}
	out := make([]Value, len(xs))
	for i, x := range xs {
		row := make([]Value, 0, len(others)+1)
		row = append(row, x)
		for _, ys := range others {
			if i < len(ys) {
				row = append(row, ys[i])
				continue
			}
			row = append(row, Nil())
		}
		out[i] = arrayOf(row)
	}
	return arrayOf(out), nil
}

// ---------------------------------------------------------------------------
// Conversions (§5.6 — one name each: array, dict, json)
// ---------------------------------------------------------------------------

// arrArray is the identity for an array and materialises a range (§12.10).
func arrArray(c *Ctx, recv Value, args []Value) (Value, error) {
	if recv.Kind() == KArray {
		return recv, nil
	}
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	return arrayOf(xs), nil
}

// arrDict builds a Dict from [[k, v], …] pairs (§12.3), the inverse of `dict.array`.
func arrDict(c *Ctx, recv Value, args []Value) (Value, error) {
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	d := NewOrderedDictCap(len(xs))
	for i, x := range xs {
		if x.Kind() != KArray || x.Len() != 2 {
			return Nil(), c.TypeErrorf("%s expects [key, value] pairs, element %d is %s", c.Name(), i, x.TypeName())
		}
		pair := x.Elems()
		if err := d.Set(pair[0], pair[1]); err != nil {
			return Nil(), c.TypeErrorf("%s", err.Error())
		}
	}
	return dictOf(d), nil
}

func arrJSON(c *Ctx, recv Value, args []Value) (Value, error) {
	if err := arrCheck(c, recv); err != nil {
		return Nil(), err
	}
	s, err := jsonvText(c, recv, "")
	if err != nil {
		return Nil(), err
	}
	return Str(s), nil
}

// ---------------------------------------------------------------------------
// Mutation (§12.3) — arrays only
// ---------------------------------------------------------------------------

func arrPush(c *Ctx, recv Value, args []Value) (Value, error) {
	p, err := arrTarget(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrGrow(c, len(*p)+len(args)); err != nil {
		return Nil(), err
	}
	*p = append(*p, args...)
	return recv, nil
}

func arrPop(c *Ctx, recv Value, args []Value) (Value, error) {
	p, err := arrTarget(c, recv)
	if err != nil {
		return Nil(), err
	}
	if len(*p) == 0 {
		return Nil(), nil
	}
	last := (*p)[len(*p)-1]
	*p = (*p)[:len(*p)-1]
	return last, nil
}

func arrShift(c *Ctx, recv Value, args []Value) (Value, error) {
	p, err := arrTarget(c, recv)
	if err != nil {
		return Nil(), err
	}
	if len(*p) == 0 {
		return Nil(), nil
	}
	first := (*p)[0]
	*p = append((*p)[:0], (*p)[1:]...)
	return first, nil
}

func arrUnshift(c *Ctx, recv Value, args []Value) (Value, error) {
	p, err := arrTarget(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrGrow(c, len(*p)+len(args)); err != nil {
		return Nil(), err
	}
	next := make([]Value, 0, len(*p)+len(args))
	next = append(next, args...)
	next = append(next, (*p)...)
	*p = next
	return recv, nil
}

func arrInsert(c *Ctx, recv Value, args []Value) (Value, error) {
	p, err := arrTarget(c, recv)
	if err != nil {
		return Nil(), err
	}
	xs := *p
	i := int(args[0].Int())
	if i < 0 {
		i += len(xs) + 1
	}
	if i < 0 || i > len(xs) {
		return Nil(), c.ErrorfKind(ErrKindIndex, "index %d is out of range", args[0].Int())
	}
	rest := args[1:]
	if err := arrGrow(c, len(xs)+len(rest)); err != nil {
		return Nil(), err
	}
	next := make([]Value, 0, len(xs)+len(rest))
	next = append(next, xs[:i]...)
	next = append(next, rest...)
	next = append(next, xs[i:]...)
	*p = next
	return recv, nil
}

func arrDeleteAt(c *Ctx, recv Value, args []Value) (Value, error) {
	p, err := arrTarget(c, recv)
	if err != nil {
		return Nil(), err
	}
	xs := *p
	i := int(args[0].Int())
	if i < 0 {
		i += len(xs)
	}
	if i < 0 || i >= len(xs) {
		return Nil(), nil
	}
	old := xs[i]
	*p = append(xs[:i], xs[i+1:]...)
	return old, nil
}

// arrDelete removes every equal element and returns the receiver (§12.3).
func arrDelete(c *Ctx, recv Value, args []Value) (Value, error) {
	p, err := arrTarget(c, recv)
	if err != nil {
		return Nil(), err
	}
	xs := *p
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	next := xs[:0]
	for _, x := range xs {
		if !x.Equal(args[0]) {
			next = append(next, x)
		}
	}
	*p = next
	return recv, nil
}

func arrConcat(c *Ctx, recv Value, args []Value) (Value, error) {
	p, err := arrTarget(c, recv)
	if err != nil {
		return Nil(), err
	}
	for _, a := range args {
		ys, err := arrElems(c, a)
		if err != nil {
			return Nil(), err
		}
		if err := arrGrow(c, len(*p)+len(ys)); err != nil {
			return Nil(), err
		}
		*p = append(*p, ys...)
	}
	return recv, nil
}

// ---------------------------------------------------------------------------
// Randomness (§12.3) — installed only when the host supplies Options.Rand
// ---------------------------------------------------------------------------

func arrSample(c *Ctx, recv Value, args []Value) (Value, error) {
	r, err := c.Rand()
	if err != nil {
		return Nil(), err
	}
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if len(xs) == 0 {
		return Nil(), nil
	}
	return xs[r.Intn(len(xs))], nil
}

func arrShuffle(c *Ctx, recv Value, args []Value) (Value, error) {
	r, err := c.Rand()
	if err != nil {
		return Nil(), err
	}
	xs, err := arrElems(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := arrIter(c, xs); err != nil {
		return Nil(), err
	}
	out := make([]Value, len(xs))
	copy(out, xs)
	r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return arrayOf(out), nil
}

// ---------------------------------------------------------------------------
// Range-only rows (§12.10)
// ---------------------------------------------------------------------------

// rngStep walks the range by n. With a closure it iterates and returns the receiver;
// without one it returns the array, so `(0..10).step(2).map { … }` chains.
func rngStep(c *Ctx, recv Value, args []Value) (Value, error) {
	r := recv.rng()
	if r == nil {
		return Nil(), c.TypeErrorf("%s expects a range, got %s", c.Name(), recv.Kind())
	}
	n := arrArg(c, args, 0).Int()
	if n <= 0 {
		return Nil(), c.ArgErrorf("step expects a positive step, got %d", n)
	}
	hi := r.Hi
	if r.Excl {
		hi--
	}
	var out []Value
	for i := r.Lo; i <= hi; i += n {
		if err := c.Step(1); err != nil {
			return Nil(), err
		}
		if c.HasClosure() {
			if _, err := c.CallClosure(Int(i)); err != nil {
				return Nil(), err
			}
			continue
		}
		out = append(out, Int(i))
		if err := arrGrow(c, len(out)); err != nil {
			return Nil(), err
		}
	}
	if c.HasClosure() {
		return recv, nil
	}
	return arrayOf(out), nil
}
