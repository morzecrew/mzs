package mzs

import (
	"math"
	"sort"
)

// dictKey is the comparable projection of a Value used as a dict key (§7.6). 1 and 1.0
// project to the same key because an integral Float normalises to Int; a regex hashes
// by source and flags; nil, bool, int, float, string, regex and time are hashable and
// nothing else is.
type dictKey struct {
	k Kind
	n int64
	s string
}

func hashKey(v Value) (dictKey, error) {
	switch v.k {
	case KNil:
		return dictKey{k: KNil}, nil
	case KBool:
		return dictKey{k: KBool, n: v.n}, nil
	case KInt:
		return dictKey{k: KInt, n: v.n}, nil
	case KFloat:
		f := v.float()
		if f == math.Trunc(f) && !math.IsInf(f, 0) && math.Abs(f) < math.MaxInt64 {
			return dictKey{k: KInt, n: int64(f)}, nil
		}
		return dictKey{k: KFloat, n: v.n}, nil
	case KString:
		return dictKey{k: KString, s: v.s}, nil
	case KRegex:
		return dictKey{k: KRegex, s: v.rx().Key()}, nil
	case KTime:
		return dictKey{k: KTime, n: v.Time().UnixNano()}, nil
	}
	return dictKey{}, newError(ErrKindType, "dict key must be hashable, got %s", v.k)
}

// OrderedDict is the insertion-ordered hash behind every mzs dict (D11). Iteration,
// Keys, Values and JSON output all follow insertion order, which is what makes script
// output deterministic (§8.13). Deleting a key tombstones its slot; iteration skips
// tombstones and Len is O(1).
//
// An OrderedDict is not safe for concurrent mutation. Values are per-Run, and a
// *Program never holds one, so that is not a constraint on the concurrency contract.
type OrderedDict struct {
	idx  map[dictKey]int
	keys []Value
	vals []Value
	n    int // live entries
}

// NewOrderedDict returns an empty dict.
func NewOrderedDict() *OrderedDict {
	return &OrderedDict{idx: make(map[dictKey]int)}
}

// NewOrderedDictCap returns an empty dict sized for n entries.
func NewOrderedDictCap(n int) *OrderedDict {
	if n < 0 {
		n = 0
	}
	return &OrderedDict{
		idx:  make(map[dictKey]int, n),
		keys: make([]Value, 0, n),
		vals: make([]Value, 0, n),
	}
}

// Len is the number of live entries.
func (d *OrderedDict) Len() int {
	if d == nil {
		return 0
	}
	return d.n
}

// Get returns the value stored under key and whether it was present.
func (d *OrderedDict) Get(key Value) (Value, bool) {
	if d == nil || d.n == 0 {
		return Nil(), false
	}
	k, err := hashKey(key)
	if err != nil {
		return Nil(), false
	}
	i, ok := d.idx[k]
	if !ok {
		return Nil(), false
	}
	return d.vals[i], true
}

// Has reports whether key is present.
func (d *OrderedDict) Has(key Value) bool {
	_, ok := d.Get(key)
	return ok
}

// Set inserts or updates key. A new key is appended at the end; an existing key keeps
// its original position, so re-assigning never reorders a dict. It returns an error
// only for an unhashable key.
func (d *OrderedDict) Set(key, val Value) error {
	k, err := hashKey(key)
	if err != nil {
		return err
	}
	if d.idx == nil {
		d.idx = make(map[dictKey]int)
	}
	if i, ok := d.idx[k]; ok {
		d.vals[i] = val
		return nil
	}
	d.idx[k] = len(d.keys)
	d.keys = append(d.keys, key)
	d.vals = append(d.vals, val)
	d.n++
	return nil
}

// Delete removes key and returns the value it held.
func (d *OrderedDict) Delete(key Value) (Value, bool) {
	if d == nil || d.n == 0 {
		return Nil(), false
	}
	k, err := hashKey(key)
	if err != nil {
		return Nil(), false
	}
	i, ok := d.idx[k]
	if !ok {
		return Nil(), false
	}
	old := d.vals[i]
	delete(d.idx, k)
	d.keys[i] = Value{k: kindTomb}
	d.vals[i] = Nil()
	d.n--
	// Reclaim the slots once the dict is mostly holes, so a long-lived dict that is
	// repeatedly filled and emptied does not grow without bound.
	if d.n*2 < len(d.keys) && len(d.keys) > 16 {
		d.compact()
	}
	return old, true
}

func (d *OrderedDict) compact() {
	keys := d.keys[:0]
	vals := d.vals[:0]
	for i, k := range d.keys {
		if k.k == kindTomb {
			continue
		}
		keys = append(keys, k)
		vals = append(vals, d.vals[i])
	}
	d.keys = keys
	d.vals = vals
	d.idx = make(map[dictKey]int, len(keys))
	for i, k := range keys {
		hk, err := hashKey(k)
		if err != nil {
			continue
		}
		d.idx[hk] = i
	}
}

// Each visits every live entry in insertion order until f returns false.
func (d *OrderedDict) Each(f func(key, val Value) bool) {
	if d == nil {
		return
	}
	for i, k := range d.keys {
		if k.k == kindTomb {
			continue
		}
		if !f(k, d.vals[i]) {
			return
		}
	}
}

// Keys returns the live keys in insertion order.
func (d *OrderedDict) Keys() []Value {
	if d == nil {
		return nil
	}
	out := make([]Value, 0, d.n)
	for _, k := range d.keys {
		if k.k == kindTomb {
			continue
		}
		out = append(out, k)
	}
	return out
}

// Values returns the live values in insertion order.
func (d *OrderedDict) Values() []Value {
	if d == nil {
		return nil
	}
	out := make([]Value, 0, d.n)
	for i, k := range d.keys {
		if k.k == kindTomb {
			continue
		}
		out = append(out, d.vals[i])
	}
	return out
}

// Pairs returns [[k, v], …] in insertion order, which is what `array(dict)` produces
// (§12.1).
func (d *OrderedDict) Pairs() []Value {
	if d == nil {
		return nil
	}
	out := make([]Value, 0, d.n)
	d.Each(func(k, v Value) bool {
		out = append(out, Array(k, v))
		return true
	})
	return out
}

// Clone is a shallow copy: nested arrays and dicts stay shared, matching `dup` (§12.1).
func (d *OrderedDict) Clone() *OrderedDict {
	if d == nil {
		return NewOrderedDict()
	}
	out := NewOrderedDictCap(d.n)
	d.Each(func(k, v Value) bool {
		_ = out.Set(k, v)
		return true
	})
	return out
}

// Merge copies every entry of other over the receiver; later writes win. It mutates the
// receiver, so `merge` must call it on a Clone and `merge_in_place` on the receiver.
func (d *OrderedDict) Merge(other *OrderedDict) {
	if other == nil {
		return
	}
	other.Each(func(k, v Value) bool {
		_ = d.Set(k, v)
		return true
	})
}

// Sorted returns the entries ordered by a comparison over keys, without disturbing the
// dict's own insertion order. `sort_by` (§12.4) builds on it.
func (d *OrderedDict) Sorted(less func(ka, va, kb, vb Value) bool) []Value {
	pairs := d.Pairs()
	sort.SliceStable(pairs, func(i, j int) bool {
		a, b := pairs[i].Elems(), pairs[j].Elems()
		return less(a[0], a[1], b[0], b[1])
	})
	return pairs
}
