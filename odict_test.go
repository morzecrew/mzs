package mzs

import (
	"strings"
	"testing"
)

func keysOf(d *OrderedDict) string {
	parts := make([]string, 0, d.Len())
	for _, k := range d.Keys() {
		parts = append(parts, k.Str())
	}
	return strings.Join(parts, ",")
}

// D11: dicts are insertion-ordered. Re-assigning an existing key must not move it, or
// JSON output would depend on write order rather than on declaration order.
func TestOrderedDictInsertionOrder(t *testing.T) {
	tests := []struct {
		name string
		ops  []string // "k=v" writes, "-k" deletes
		want string
	}{
		{"declaration order", []string{"b=1", "a=2", "c=3"}, "b,a,c"},
		{"update keeps position", []string{"b=1", "a=2", "b=9"}, "b,a"},
		{"delete removes", []string{"a=1", "b=2", "c=3", "-b"}, "a,c"},
		{"reinsert goes to the end", []string{"a=1", "b=2", "-a", "a=3"}, "b,a"},
		{"delete everything", []string{"a=1", "-a"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewOrderedDict()
			for _, op := range tt.ops {
				if strings.HasPrefix(op, "-") {
					m.Delete(Str(op[1:]))
					continue
				}
				k, v, _ := strings.Cut(op, "=")
				if err := m.Set(Str(k), Str(v)); err != nil {
					t.Fatalf("Set(%q) error = %v", k, err)
				}
			}
			if got := keysOf(m); got != tt.want {
				t.Errorf("key order = %q; want %q", got, tt.want)
			}
			if got, want := m.Len(), len(strings.FieldsFunc(tt.want, func(r rune) bool { return r == ',' })); got != want {
				t.Errorf("Len() = %d; want %d", got, want)
			}
		})
	}
}

// §7.6: keys may be nil, bool, int, float, string or regex (hashed by source+flags).
// 1 and 1.0 are the same key. Arrays, dicts, functions and ranges are not hashable.
func TestOrderedDictKeyKinds(t *testing.T) {
	re, err := Regex("a", "i")
	if err != nil {
		t.Fatalf("Regex: %v", err)
	}

	tests := []struct {
		name    string
		key     Value
		wantErr bool
	}{
		{"nil", Nil(), false},
		{"bool", Bool(true), false},
		{"int", Int(1), false},
		{"float", Float(1.5), false},
		{"string", Str("привет"), false},
		{"regex", re, false},
		{"array is not hashable", Array(Int(1)), true},
		{"dict is not hashable", Dict(), true},
		{"function is not hashable", Fn("f", 0, nil), true},
		{"range is not hashable", rangeOf(0, 2, false), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewOrderedDict()
			err := m.Set(tt.key, Int(1))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Set(%s) error = %v; wantErr %v", tt.key.Inspect(), err, tt.wantErr)
			}
			if tt.wantErr {
				if !strings.Contains(err.Error(), "hashable") {
					t.Errorf("error = %q; want it to mention hashability", err)
				}
				return
			}
			if v, ok := m.Get(tt.key); !ok || v.Int() != 1 {
				t.Errorf("Get(%s) = %s, %v; want 1, true", tt.key.Inspect(), v.Inspect(), ok)
			}
		})
	}
}

func TestOrderedDictIntegralFloatIsTheSameKey(t *testing.T) {
	tests := []struct {
		name string
		a, b Value
		same bool
	}{
		{"1 and 1.0", Int(1), Float(1), true},
		{"0 and -0.0", Int(0), Float(0), true},
		{"1 and 1.5", Int(1), Float(1.5), false},
		{"1 and \"1\"", Int(1), Str("1"), false},
		{"true and 1", Bool(true), Int(1), false},
		{"nil and false", Nil(), Bool(false), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewOrderedDict()
			if err := m.Set(tt.a, Str("first")); err != nil {
				t.Fatalf("Set: %v", err)
			}
			if err := m.Set(tt.b, Str("second")); err != nil {
				t.Fatalf("Set: %v", err)
			}
			wantLen := 2
			if tt.same {
				wantLen = 1
			}
			if got := m.Len(); got != wantLen {
				t.Errorf("Len() = %d; want %d", got, wantLen)
			}
			v, _ := m.Get(tt.a)
			want := "first"
			if tt.same {
				want = "second"
			}
			if v.Str() != want {
				t.Errorf("Get(%s) = %q; want %q", tt.a.Inspect(), v.Str(), want)
			}
		})
	}
}

func TestOrderedDictDeleteReturnsValue(t *testing.T) {
	tests := []struct {
		name    string
		key     Value
		wantVal string
		wantOk  bool
	}{
		{"present", Str("a"), "1", true},
		{"absent", Str("zz"), "", false},
		{"unhashable", Array(), "", false},
	}

	m := NewOrderedDict()
	_ = m.Set(Str("a"), Int(1))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := m.Delete(tt.key)
			if ok != tt.wantOk {
				t.Fatalf("Delete(%s) ok = %v; want %v", tt.key.Inspect(), ok, tt.wantOk)
			}
			if got.Str() != tt.wantVal {
				t.Errorf("Delete(%s) = %q; want %q", tt.key.Inspect(), got.Str(), tt.wantVal)
			}
		})
	}
}

// Deleting tombstones a slot; iteration must skip it and Len must stay exact even
// after enough churn to trigger compaction.
func TestOrderedDictTombstoneChurn(t *testing.T) {
	m := NewOrderedDict()
	for i := 0; i < 200; i++ {
		if err := m.Set(Int(int64(i)), Int(int64(i*2))); err != nil {
			t.Fatalf("Set(%d): %v", i, err)
		}
	}
	for i := 0; i < 200; i += 2 {
		m.Delete(Int(int64(i)))
	}
	if got := m.Len(); got != 100 {
		t.Fatalf("Len() = %d; want 100", got)
	}

	seen := 0
	prev := int64(-1)
	m.Each(func(k, v Value) bool {
		seen++
		if k.Int()%2 == 0 {
			t.Errorf("Each visited a deleted key %d", k.Int())
		}
		if k.Int() <= prev {
			t.Errorf("Each visited %d after %d; insertion order broken", k.Int(), prev)
		}
		prev = k.Int()
		if v.Int() != k.Int()*2 {
			t.Errorf("value for %d = %d; want %d", k.Int(), v.Int(), k.Int()*2)
		}
		return true
	})
	if seen != 100 {
		t.Errorf("Each visited %d entries; want 100", seen)
	}
	if got := len(m.Keys()); got != 100 {
		t.Errorf("len(Keys()) = %d; want 100", got)
	}
	if got := len(m.Values()); got != 100 {
		t.Errorf("len(Values()) = %d; want 100", got)
	}
	// Every surviving key must still be reachable after compaction rebuilt the index.
	for i := 1; i < 200; i += 2 {
		if _, ok := m.Get(Int(int64(i))); !ok {
			t.Fatalf("key %d lost after compaction", i)
		}
	}
}

func TestOrderedDictEachStopsEarly(t *testing.T) {
	m := NewOrderedDict()
	for _, k := range []string{"a", "b", "c"} {
		_ = m.Set(Str(k), Int(1))
	}
	visited := 0
	m.Each(func(k, v Value) bool {
		visited++
		return visited < 2
	})
	if visited != 2 {
		t.Errorf("Each visited %d entries after the callback stopped; want 2", visited)
	}
}

// Clone is shallow (§12.1 `dup`): the top level is independent, nested references are
// still shared.
func TestOrderedDictClone(t *testing.T) {
	inner := Array(Int(1))
	m := NewOrderedDict()
	_ = m.Set(Str("a"), Int(1))
	_ = m.Set(Str("nested"), inner)

	c := m.Clone()
	_ = c.Set(Str("b"), Int(2))
	if m.Len() != 2 {
		t.Errorf("Clone mutated the original: Len() = %d; want 2", m.Len())
	}
	if c.Len() != 3 {
		t.Errorf("clone Len() = %d; want 3", c.Len())
	}
	if got := keysOf(c); got != "a,nested,b" {
		t.Errorf("clone key order = %q; want %q", got, "a,nested,b")
	}
	nested, _ := c.Get(Str("nested"))
	nested.Append(Int(2))
	if inner.Len() != 2 {
		t.Errorf("Clone deep-copied a nested array; dup must be shallow")
	}
}

func TestOrderedDictMerge(t *testing.T) {
	tests := []struct {
		name      string
		base      []string
		other     []string
		wantKeys  string
		wantValue string
	}{
		{"disjoint appends", []string{"a=1"}, []string{"b=2"}, "a,b", "2"},
		{"overlap keeps position", []string{"a=1", "b=2"}, []string{"a=9"}, "a,b", "9"},
		{"empty other", []string{"a=1"}, nil, "a", "1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := NewOrderedDict()
			for _, op := range tt.base {
				k, v, _ := strings.Cut(op, "=")
				_ = base.Set(Str(k), Str(v))
			}
			other := NewOrderedDict()
			var lastKey string
			for _, op := range tt.other {
				k, v, _ := strings.Cut(op, "=")
				lastKey = k
				_ = other.Set(Str(k), Str(v))
			}
			base.Merge(other)
			if got := keysOf(base); got != tt.wantKeys {
				t.Errorf("key order = %q; want %q", got, tt.wantKeys)
			}
			if lastKey == "" {
				return
			}
			got, _ := base.Get(Str(lastKey))
			if got.Str() != tt.wantValue {
				t.Errorf("merged value for %q = %q; want %q", lastKey, got.Str(), tt.wantValue)
			}
		})
	}
}

func TestOrderedDictPairs(t *testing.T) {
	m := NewOrderedDict()
	_ = m.Set(Str("b"), Int(2))
	_ = m.Set(Str("a"), Int(1))

	pairs := m.Pairs()
	if len(pairs) != 2 {
		t.Fatalf("Pairs() length = %d; want 2", len(pairs))
	}
	want := []struct{ k, v string }{{"b", "2"}, {"a", "1"}}
	for i, w := range want {
		e := pairs[i].Elems()
		if len(e) != 2 || e[0].Str() != w.k || e[1].Str() != w.v {
			t.Errorf("Pairs()[%d] = %s; want [%q, %q]", i, pairs[i].Str(), w.k, w.v)
		}
	}
}

func TestNilOrderedDictIsSafe(t *testing.T) {
	var d *OrderedDict
	if d.Len() != 0 {
		t.Errorf("nil Len() = %d; want 0", d.Len())
	}
	if _, ok := d.Get(Str("a")); ok {
		t.Errorf("nil Get() reported a hit")
	}
	if d.Has(Str("a")) {
		t.Errorf("nil Has() reported a hit")
	}
	if _, ok := d.Delete(Str("a")); ok {
		t.Errorf("nil Delete() reported a hit")
	}
	if d.Keys() != nil || d.Values() != nil || d.Pairs() != nil {
		t.Errorf("nil dict returned non-nil slices")
	}
	d.Each(func(k, v Value) bool {
		t.Errorf("nil Each() visited %s", k.Inspect())
		return true
	})
	if c := d.Clone(); c == nil || c.Len() != 0 {
		t.Errorf("nil Clone() = %v; want an empty dict", c)
	}
}
