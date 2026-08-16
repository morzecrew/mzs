package mzs

import "sort"

// Dict methods (§12.4). Every one of them preserves insertion order (D11): iteration,
// keys, values, filter and merge all follow the order the keys were first written, so a
// script's JSON output is byte-stable across runs (§8.13).
//
// The data structure is a **dict**, never a "map": `map` is the higher-order function of
// §12.3 and one name may not mean two things (D17). A closure is an ordinary trailing
// argument (§4.2), counted in Min/Max and reached through Ctx.CallClosure; every row that
// takes one receives the key and the value as two arguments, and a one-parameter closure
// simply drops the value (§7.7).
//
// A dict never dispatches `.` to its own keys (§8.7): values are read with `[]`, `get`
// or `dig`.

func init() {
	RegisterMethods(KDict,
		Method{Name: "keys", Fn: dictvKeys},
		Method{Name: "values", Fn: dictvValues},
		Method{Name: "len", Fn: dictvLen},
		Method{Name: "empty", Fn: dictvEmpty},
		Method{Name: "has", Min: 1, Max: 1, Fn: dictvHas},
		Method{Name: "has_val", Min: 1, Max: 1, Fn: dictvHasVal},
		Method{Name: "get", Min: 1, Max: 2, Fn: dictvGet},
		Method{Name: "fetch", Min: 1, Max: 1, Fn: dictvFetch},
		Method{Name: "set", Min: 2, Max: 2, Fn: dictvSet},
		Method{Name: "delete", Min: 1, Max: 1, Fn: dictvDelete},
		Method{Name: "merge", Min: 1, Max: -1, Fn: dictvMerge},
		Method{Name: "merge_in_place", Min: 1, Max: -1, Fn: dictvMergeInPlace},
		Method{Name: "dig", Min: 1, Max: -1, Fn: dictvDig},
		Method{Name: "each", Min: 1, Max: 1, Fn: dictvEach},
		Method{Name: "map", Min: 1, Max: 1, Fn: dictvMap},
		Method{Name: "filter", Min: 1, Max: 1, Fn: dictvFilter},
		Method{Name: "reject", Min: 1, Max: 1, Fn: dictvReject},
		Method{Name: "find", Min: 1, Max: 1, Fn: dictvFind},
		Method{Name: "any", Min: 1, Max: 1, Fn: dictvAny},
		Method{Name: "all", Min: 1, Max: 1, Fn: dictvAll},
		Method{Name: "invert", Fn: dictvInvert},
		Method{Name: "sort_by", Min: 1, Max: 1, Fn: dictvSortBy},
		Method{Name: "array", Fn: dictvArray},
		Method{Name: "json", Fn: dictvJSON},
	)
}

// dictvOf is the receiver check every row starts with. A Dict value always carries an
// OrderedDict, so a nil result means the receiver was not a dict at all.
func dictvOf(c *Ctx, v Value) (*OrderedDict, error) {
	if d := v.odict(); d != nil {
		return d, nil
	}
	return nil, c.TypeErrorf("%s expects a dict, got %s", c.Name(), v.Kind())
}

// dictvIter charges one step per entry before a loop.
func dictvIter(c *Ctx, d *OrderedDict) error { return c.Step(int64(d.Len())) }

func dictvKeys(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	return arrayOf(d.Keys()), nil
}

func dictvValues(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	return arrayOf(d.Values()), nil
}

func dictvLen(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	return Int(int64(d.Len())), nil
}

func dictvEmpty(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	return Bool(d.Len() == 0), nil
}

// dictvHas is the key test, and therefore what an `in` pattern calls on a dict (§5.3).
func dictvHas(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	return Bool(d.Has(args[0])), nil
}

func dictvHasVal(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := dictvIter(c, d); err != nil {
		return Nil(), err
	}
	found := false
	d.Each(func(_, v Value) bool {
		if v.Equal(args[0]) {
			found = true
			return false
		}
		return true
	})
	return Bool(found), nil
}

// dictvGet never raises: a missing key is nil, or the supplied default (§12.4).
func dictvGet(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	if v, ok := d.Get(args[0]); ok {
		return v, nil
	}
	if len(args) == 2 {
		return args[1], nil
	}
	return Nil(), nil
}

// dictvFetch raises when the key is absent, which is the whole difference from `get`.
// There is no default argument here: `get(k, default)` is that operation, under its own
// single name (D17).
func dictvFetch(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	if v, ok := d.Get(args[0]); ok {
		return v, nil
	}
	// `key` and not `index`: a handler that tells a missing key from a position out of
	// range is the reason kinds are a closed list (§13.5).
	return Nil(), c.ErrorfKind(ErrKindKey, "key not found: %s", args[0].Inspect())
}

func dictvSet(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := d.Set(args[0], args[1]); err != nil {
		return Nil(), c.TypeErrorf("%s", err.Error())
	}
	return recv, nil
}

// dictvDelete returns the value that was removed, or nil (§12.4).
func dictvDelete(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	v, _ := d.Delete(args[0])
	return v, nil
}

// dictvMerge builds a new dict; later arguments win. The receiver is untouched, which is
// why merge_in_place is a separate row — the mutation is named at the call site.
func dictvMerge(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	out := d.Clone()
	if err := dictvMergeInto(c, out, args); err != nil {
		return Nil(), err
	}
	return dictOf(out), nil
}

func dictvMergeInPlace(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := dictvMergeInto(c, d, args); err != nil {
		return Nil(), err
	}
	return recv, nil
}

func dictvMergeInto(c *Ctx, dst *OrderedDict, args []Value) error {
	for _, a := range args {
		src := a.odict()
		if src == nil {
			return c.TypeErrorf("%s expects a dict, got %s", c.Name(), a.Kind())
		}
		if err := c.Step(int64(src.Len())); err != nil {
			return err
		}
		dst.Merge(src)
	}
	return nil
}

// dictvDig walks dicts and arrays and stops at the first nil, so `d.dig("a", "b")` on
// half-present data is nil rather than an error — the shape a one-liner wants (§8.8).
func dictvDig(c *Ctx, recv Value, args []Value) (Value, error) {
	if _, err := dictvOf(c, recv); err != nil {
		return Nil(), err
	}
	return digPath(c, recv, args)
}

// dictvEach yields the key and the value as two arguments and returns the receiver.
func dictvEach(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := dictvIter(c, d); err != nil {
		return Nil(), err
	}
	// Snapshot the entries: a closure that writes into the dict must not disturb the walk.
	keys, vals := d.Keys(), d.Values()
	for i, k := range keys {
		if _, err := c.CallClosure(k, vals[i]); err != nil {
			return Nil(), err
		}
	}
	return recv, nil
}

// dictvMap returns an Array of the closure's results (§12.4), not a dict.
func dictvMap(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := dictvIter(c, d); err != nil {
		return Nil(), err
	}
	keys, vals := d.Keys(), d.Values()
	out := make([]Value, len(keys))
	for i, k := range keys {
		r, err := c.CallClosure(k, vals[i])
		if err != nil {
			return Nil(), err
		}
		out[i] = r
	}
	return arrayOf(out), nil
}

func dictvFilter(c *Ctx, recv Value, args []Value) (Value, error) {
	return dictvSieve(c, recv, true)
}

func dictvReject(c *Ctx, recv Value, args []Value) (Value, error) {
	return dictvSieve(c, recv, false)
}

// dictvSieve returns a Dict (§12.4) with the surviving entries in their original order.
func dictvSieve(c *Ctx, recv Value, keep bool) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := dictvIter(c, d); err != nil {
		return Nil(), err
	}
	keys, vals := d.Keys(), d.Values()
	out := NewOrderedDictCap(len(keys))
	for i, k := range keys {
		r, err := c.CallClosure(k, vals[i])
		if err != nil {
			return Nil(), err
		}
		if r.Truthy() == keep {
			if err := out.Set(k, vals[i]); err != nil {
				return Nil(), c.TypeErrorf("%s", err.Error())
			}
		}
	}
	return dictOf(out), nil
}

// dictvFind returns the first matching [key, value] pair, or nil.
func dictvFind(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := dictvIter(c, d); err != nil {
		return Nil(), err
	}
	keys, vals := d.Keys(), d.Values()
	for i, k := range keys {
		r, err := c.CallClosure(k, vals[i])
		if err != nil {
			return Nil(), err
		}
		if r.Truthy() {
			return Array(k, vals[i]), nil
		}
	}
	return Nil(), nil
}

func dictvAny(c *Ctx, recv Value, args []Value) (Value, error) {
	return dictvQuantify(c, recv, "any")
}

func dictvAll(c *Ctx, recv Value, args []Value) (Value, error) {
	return dictvQuantify(c, recv, "all")
}

// dictvQuantify implements any/all. Both take the closure the §12.4 row declares: a
// dict entry is a pair, so there is no element truthiness to fall back on.
func dictvQuantify(c *Ctx, recv Value, mode string) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := dictvIter(c, d); err != nil {
		return Nil(), err
	}
	keys, vals := d.Keys(), d.Values()
	for i, k := range keys {
		r, err := c.CallClosure(k, vals[i])
		if err != nil {
			return Nil(), err
		}
		if mode == "any" && r.Truthy() {
			return Bool(true), nil
		}
		if mode == "all" && !r.Truthy() {
			return Bool(false), nil
		}
	}
	return Bool(mode != "any"), nil
}

// dictvInvert swaps keys and values. A value that cannot be a key (an array, a dict)
// reports the same diagnostic a dict literal would (§7.6).
func dictvInvert(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := dictvIter(c, d); err != nil {
		return Nil(), err
	}
	out := NewOrderedDictCap(d.Len())
	var failed error
	d.Each(func(k, v Value) bool {
		if err := out.Set(v, k); err != nil {
			failed = c.TypeErrorf("%s", err.Error())
			return false
		}
		return true
	})
	if failed != nil {
		return Nil(), failed
	}
	return dictOf(out), nil
}

// dictvSortBy returns the [key, value] pairs ordered by the closure's value, leaving the
// dict's own insertion order alone (§12.4). The closure receives the key and the value as
// two arguments, so it cannot delegate to the array row.
func dictvSortBy(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := dictvIter(c, d); err != nil {
		return Nil(), err
	}
	keys, vals := d.Keys(), d.Values()
	type entry struct{ sortKey, pair Value }
	entries := make([]entry, len(keys))
	for i, k := range keys {
		sk, err := c.CallClosure(k, vals[i])
		if err != nil {
			return Nil(), err
		}
		entries[i] = entry{sortKey: sk, pair: Array(k, vals[i])}
	}
	var failed error
	sort.SliceStable(entries, func(i, j int) bool {
		if failed != nil {
			return false
		}
		n, err := arrCompare(c, entries[i].sortKey, entries[j].sortKey)
		if err != nil {
			failed = err
			return false
		}
		return n < 0
	})
	if failed != nil {
		return Nil(), failed
	}
	out := make([]Value, len(entries))
	for i, e := range entries {
		out[i] = e.pair
	}
	return arrayOf(out), nil
}

// dictvArray is the [[k, v], …] form (§12.1), the inverse of `array.dict`. There is no
// `dict` row here: a dict already is one, and the universal `dict(x)` answers with the
// receiver itself.
func dictvArray(c *Ctx, recv Value, args []Value) (Value, error) {
	d, err := dictvOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	if err := dictvIter(c, d); err != nil {
		return Nil(), err
	}
	return arrayOf(d.Pairs()), nil
}

func dictvJSON(c *Ctx, recv Value, args []Value) (Value, error) {
	if _, err := dictvOf(c, recv); err != nil {
		return Nil(), err
	}
	s, err := jsonvText(c, recv, "")
	if err != nil {
		return Nil(), err
	}
	return Str(s), nil
}
