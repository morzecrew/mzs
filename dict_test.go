package mzs

import (
	"errors"
	"strconv"
	"testing"
)

// The data structure is a **dict**, never a "map": `map` is the higher-order function of
// §12.3 and one name may not mean two things (D17). A dict literal is written `[a: 1]`
// (D3) and is insertion-ordered (D11).

func TestDictMethods(t *testing.T) {
	c := colCtx(t, DefaultOptions())

	base := func() Value {
		return Dict(Str("имя"), Str("Иван"), Str("возраст"), Int(30), Str("emoji"), Str("🌲"))
	}
	pairValue := colBlock(func(c *Ctx, args []Value) (Value, error) {
		return Str(colArg(args, 0).Str() + "=" + colArg(args, 1).Str()), nil
	})
	valueOverOne := colBlock(func(c *Ctx, args []Value) (Value, error) {
		return Bool(colArg(args, 1).Int() > 1), nil
	})
	keyOnly := colBlock(func(c *Ctx, args []Value) (Value, error) {
		return colArg(args, 0), nil
	})

	tests := []struct {
		name    string
		method  string
		recv    Value
		args    []Value
		want    string
		wantErr bool
	}{
		{name: "keys in insertion order", method: "keys", recv: base(), want: `["имя","возраст","emoji"]`},
		{name: "values in insertion order", method: "values", recv: base(), want: `["Иван",30,"🌲"]`},
		{name: "len", method: "len", recv: base(), want: "3"},
		{name: "empty on a fresh dict", method: "empty", recv: Dict(), want: "true"},
		{name: "empty on a populated dict", method: "empty", recv: base(), want: "false"},
		{name: "has a key", method: "has", recv: base(), args: []Value{Str("имя")}, want: "true"},
		{name: "has misses", method: "has", recv: base(), args: []Value{Str("нет")}, want: "false"},
		{name: "has_val", method: "has_val", recv: base(), args: []Value{Int(30)}, want: "true"},
		{name: "has_val misses", method: "has_val", recv: base(), args: []Value{Int(31)}, want: "false"},
		{name: "get", method: "get", recv: base(), args: []Value{Str("имя")}, want: `"Иван"`},
		{name: "get missing is nil", method: "get", recv: base(), args: []Value{Str("нет")}, want: "nil"},
		{name: "get with a default", method: "get", recv: base(), args: []Value{Str("нет"), Int(0)}, want: "0"},
		{name: "fetch present", method: "fetch", recv: base(), args: []Value{Str("возраст")}, want: "30"},
		{name: "fetch missing raises", method: "fetch", recv: base(), args: []Value{Str("нет")}, wantErr: true},
		// `get(k, default)` is the defaulting operation, under its own single name (D17),
		// so fetch takes the key and nothing else.
		{name: "fetch takes no default", method: "fetch", recv: base(),
			args: []Value{Str("нет"), Str("—")}, wantErr: true},
		{name: "an integer key and a float key are the same key", method: "get",
			recv: Dict(Int(1), Str("one")), args: []Value{Float(1)}, want: `"one"`},
		{name: "an unhashable key cannot be set", method: "set", recv: Dict(),
			args: []Value{colInts(1), Int(1)}, wantErr: true},
		{name: "delete returns the removed value", method: "delete", recv: base(),
			args: []Value{Str("возраст")}, want: "30"},
		{name: "delete of a missing key is nil", method: "delete", recv: base(),
			args: []Value{Str("нет")}, want: "nil"},
		{name: "merge is a new dict and the later key wins", method: "merge", recv: Dict(Str("a"), Int(1)),
			args: []Value{Dict(Str("a"), Int(2), Str("b"), Int(3))}, want: `{"a":2,"b":3}`},
		{name: "merge takes several dicts", method: "merge", recv: Dict(Str("a"), Int(1)),
			args: []Value{Dict(Str("b"), Int(2)), Dict(Str("c"), Int(3))}, want: `{"a":1,"b":2,"c":3}`},
		{name: "merge rejects a non-dict", method: "merge", recv: Dict(), args: []Value{Int(1)}, wantErr: true},
		{name: "dig nested", method: "dig", recv: Dict(Str("a"), Dict(Str("b"), Int(7))),
			args: []Value{Str("a"), Str("b")}, want: "7"},
		{name: "dig through an array", method: "dig", recv: Dict(Str("a"), colInts(10, 11)),
			args: []Value{Str("a"), Int(1)}, want: "11"},
		{name: "dig stops at the first miss", method: "dig", recv: Dict(Str("a"), Dict()),
			args: []Value{Str("a"), Str("b"), Str("c")}, want: "nil"},
		{name: "map yields the key and the value", method: "map", recv: Dict(Str("a"), Int(1), Str("b"), Int(2)),
			args: []Value{pairValue}, want: `["a=1","b=2"]`},
		{name: "filter returns a dict", method: "filter", recv: Dict(Str("a"), Int(1), Str("b"), Int(2)),
			args: []Value{valueOverOne}, want: `{"b":2}`},
		{name: "reject returns a dict", method: "reject", recv: Dict(Str("a"), Int(1), Str("b"), Int(2)),
			args: []Value{valueOverOne}, want: `{"a":1}`},
		{name: "find returns the pair", method: "find", recv: Dict(Str("a"), Int(1), Str("b"), Int(2)),
			args: []Value{valueOverOne}, want: `["b",2]`},
		{name: "find missing is nil", method: "find", recv: Dict(Str("a"), Int(1)),
			args: []Value{valueOverOne}, want: "nil"},
		{name: "any", method: "any", recv: Dict(Str("a"), Int(1), Str("b"), Int(2)),
			args: []Value{valueOverOne}, want: "true"},
		{name: "any on empty is false", method: "any", recv: Dict(), args: []Value{valueOverOne}, want: "false"},
		{name: "all", method: "all", recv: Dict(Str("b"), Int(2)), args: []Value{valueOverOne}, want: "true"},
		{name: "all on empty is true", method: "all", recv: Dict(), args: []Value{valueOverOne}, want: "true"},
		{name: "invert", method: "invert", recv: Dict(Str("a"), Int(1)), want: `{"1":"a"}`},
		{name: "invert refuses an unhashable value", method: "invert", recv: Dict(Str("a"), colInts(1)),
			wantErr: true},
		{name: "sort_by yields the key and the value", method: "sort_by",
			recv: Dict(Str("b"), Int(2), Str("a"), Int(1)), args: []Value{keyOnly},
			want: `[["a",1],["b",2]]`},
		{name: "array is the pair list", method: "array", recv: Dict(Str("a"), Int(1)), want: `[["a",1]]`},
		{name: "json keeps insertion order and unicode", method: "json", recv: base(),
			want: `"{\"имя\":\"Иван\",\"возраст\":30,\"emoji\":\"🌲\"}"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := colInvoke(c, KDict, tt.method, tt.recv, tt.args...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("%s(%v) error = %v; wantErr %v", tt.method, tt.args, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.Inspect() != tt.want {
				t.Errorf("%s(%v) = %s; want %s", tt.method, tt.args, got.Inspect(), tt.want)
			}
		})
	}
}

// TestDictMutation pins which rows write through to the receiver (D11: re-assigning an
// existing key must not move it) and which build a new dict.
func TestDictMutation(t *testing.T) {
	c := colCtx(t, DefaultOptions())

	tests := []struct {
		name     string
		run      func(d Value) (Value, error)
		want     string
		wantSelf string
	}{
		{
			name:     "set appends a new key at the end",
			run:      func(d Value) (Value, error) { return colInvoke(c, KDict, "set", d, Str("c"), Int(3)) },
			want:     `{"a":1,"b":2,"c":3}`,
			wantSelf: `{"a":1,"b":2,"c":3}`,
		},
		{
			name:     "set keeps an existing key in place",
			run:      func(d Value) (Value, error) { return colInvoke(c, KDict, "set", d, Str("a"), Int(9)) },
			want:     `{"a":9,"b":2}`,
			wantSelf: `{"a":9,"b":2}`,
		},
		{
			name: "merge_in_place writes into the receiver",
			run: func(d Value) (Value, error) {
				return colInvoke(c, KDict, "merge_in_place", d, Dict(Str("b"), Int(8)))
			},
			want:     `{"a":1,"b":8}`,
			wantSelf: `{"a":1,"b":8}`,
		},
		{
			name:     "merge leaves the receiver alone",
			run:      func(d Value) (Value, error) { return colInvoke(c, KDict, "merge", d, Dict(Str("b"), Int(8))) },
			want:     `{"a":1,"b":8}`,
			wantSelf: `{"a":1,"b":2}`,
		},
		{
			name:     "delete removes the entry",
			run:      func(d Value) (Value, error) { return colInvoke(c, KDict, "delete", d, Str("a")) },
			want:     "1",
			wantSelf: `{"b":2}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Dict(Str("a"), Int(1), Str("b"), Int(2))
			got, err := tt.run(d)
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if got.Inspect() != tt.want {
				t.Errorf("result = %s; want %s", got.Inspect(), tt.want)
			}
			if d.Inspect() != tt.wantSelf {
				t.Errorf("receiver = %s; want %s", d.Inspect(), tt.wantSelf)
			}
		})
	}
}

// TestDictEachIsSnapshotted pins that writing to a dict from inside `each` cannot make
// the walk diverge.
func TestDictEachIsSnapshotted(t *testing.T) {
	c := colCtx(t, DefaultOptions())
	d := Dict(Str("a"), Int(1))
	seen := 0
	block := colBlock(func(bc *Ctx, args []Value) (Value, error) {
		seen++
		if seen < 5 {
			d.Set(Str("k"+colArg(args, 0).Str()), Int(1))
		}
		return Nil(), nil
	})
	got, err := colInvoke(c, KDict, "each", d, block)
	if err != nil {
		t.Fatalf("each: %v", err)
	}
	if seen != 1 {
		t.Errorf("each visited %d entries; want 1", seen)
	}
	if got.Kind() != KDict {
		t.Errorf("each = %s; want the receiver", got.Inspect())
	}
}

// TestDictClosureProtocol pins `break` and `next` for the dict rows too.
func TestDictClosureProtocol(t *testing.T) {
	c := colCtx(t, DefaultOptions())
	d := Dict(Str("a"), Int(1), Str("b"), Int(2))

	tests := []struct {
		name   string
		method string
		block  Value
		want   string
	}{
		{
			name:   "break ends each with its value",
			method: "each",
			block:  colBlock(func(bc *Ctx, args []Value) (Value, error) { return Nil(), breakSignal(Str("stop")) }),
			want:   `"stop"`,
		},
		{
			name:   "next supplies the closure's value",
			method: "map",
			block:  colBlock(func(bc *Ctx, args []Value) (Value, error) { return Nil(), nextSignal(Int(0)) }),
			want:   "[0,0]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := colInvoke(c, KDict, tt.method, d, tt.block)
			if err != nil {
				t.Fatalf("%s error = %v", tt.method, err)
			}
			if got.Inspect() != tt.want {
				t.Errorf("%s = %s; want %s", tt.method, got.Inspect(), tt.want)
			}
		})
	}
}

// D17 for §12.4: the Hash spellings are gone, `map` on a dict is the higher-order
// function and not the data structure, and there is one name for each operation.
func TestDictHasNoOldNames(t *testing.T) {
	tests := []struct {
		old, use string
	}{
		{"length", "len"},
		{"size", "len"},
		{"count", "len"},
		{"has_key", "has"},
		{"key", "has"},
		{"include", "has"},
		{"member", "has"},
		{"has_value", "has_val"},
		{"value", "has_val"},
		{"store", "set"},
		{"update", "merge_in_place"},
		{"merge!", "merge_in_place"},
		{"transform_values", "map"},
		{"transform_keys", "map"},
		{"each_pair", "each"},
		{"select", "filter"},
		{"collect", "map"},
		{"detect", "find"},
		{"none", "all"},
		{"to_a", "array"},
		{"to_h", "dict"},
		{"to_json", "json"},
	}

	for _, tt := range tests {
		t.Run(tt.old, func(t *testing.T) {
			if HasMethod(KDict, tt.old) {
				t.Errorf("dict answers %q; D17 allows only %q", tt.old, tt.use)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Every row, one property at a time
// ---------------------------------------------------------------------------
//
// Walked from the registry, like the array tables in array_test.go: a row added to
// §12.4 is covered as soon as it is registered, and one missing from dictRowArgs fails
// the first test rather than being quietly skipped.

var dictRowArgs = map[string][]Value{
	"array": nil, "empty": nil, "invert": nil, "json": nil, "keys": nil,
	"len": nil, "values": nil,
	"all": {colIdentity}, "any": {colIdentity}, "each": {colIdentity},
	"filter": {colIdentity}, "find": {colIdentity}, "map": {colIdentity},
	"reject": {colIdentity}, "sort_by": {colIdentity},
	"delete": {Str("k1")}, "dig": {Str("k1")}, "fetch": {Str("k1")},
	"get": {Str("k1")}, "has": {Str("k1")}, "has_val": {Int(1)},
	"merge": {Dict(Str("z"), Int(0))}, "merge_in_place": {Dict(Str("z"), Int(0))},
	"set": {Str("k1"), Int(2)},
}

func TestDictRowArgsTableIsComplete(t *testing.T) {
	for _, name := range MethodNames(KDict) {
		if _, ok := dictRowArgs[name]; !ok {
			t.Errorf("no arguments listed for the %q row; add it to dictRowArgs", name)
		}
	}
}

// A7: a host reaching a dict row through LookupMethod with the wrong receiver gets a
// diagnostic, not a panic.
func TestDictRowsRefuseANonDictReceiver(t *testing.T) {
	c := colCtx(t, DefaultOptions())

	for _, name := range MethodNames(KDict) {
		t.Run(name, func(t *testing.T) {
			_, err := colInvoke(c, KDict, name, colInts(1, 2), dictRowArgs[name]...)
			if err == nil {
				t.Fatalf("%q accepted an array receiver; want a type error", name)
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("%q returned %T (%v); every failure is an *Error (§17)", name, err, err)
			}
			if e.Kind != ErrKindType && e.Kind != ErrKindArgument {
				t.Errorf("%q reported kind %q; a wrong receiver is %q", name, e.Kind, ErrKindType)
			}
		})
	}
}

// §14.1 for §12.4: a row that walks the dict charges the step budget for it, so a big
// dict cannot outrun the interruption point. The two lists below are the assertion, and
// they say something the code alone does not: which rows answer in constant time, and
// which walk the receiver today **without** charging. The second list is not an
// exemption — those rows are bounded by the receiver's own size, which MaxCollection
// already capped when the dict was built — but if §14.1 is ever tightened, this is the
// list that moves.
func TestDictRowsChargeTheStepBudget(t *testing.T) {
	constantTime := map[string]bool{
		"len": true, "empty": true, "has": true, "get": true, "fetch": true,
		"set": true, "delete": true, "dig": true,
	}
	unchargedCopy := map[string]bool{
		"keys": true, "values": true, "merge": true, "merge_in_place": true,
	}

	opts := DefaultOptions()
	opts.StepBudget = 1

	d := NewOrderedDictCap(4 * stepCheckInterval)
	for i := 0; i < 4*stepCheckInterval; i++ {
		if err := d.Set(Str("k"+strconv.Itoa(i)), Int(int64(i))); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	big := dictOf(d)

	for _, name := range MethodNames(KDict) {
		t.Run(name, func(t *testing.T) {
			c := colCtx(t, opts)
			_, err := colInvoke(c, KDict, name, big, dictRowArgs[name]...)
			if constantTime[name] || unchargedCopy[name] {
				if errors.Is(err, ErrBudget) {
					t.Errorf("%q spent the budget on a %d-key dict; it is listed as not charging",
						name, big.Len())
				}
				return
			}
			if !errors.Is(err, ErrBudget) {
				t.Errorf("%q walked %d keys on a budget of one step and returned %v; "+
					"either it charges nothing (§14.1) or it belongs in one of the lists above",
					name, big.Len(), err)
			}
		})
	}
}
