package mzs

import (
	"context"
	"errors"
	"math/rand"
	"sort"
	"testing"

	"mzs/internal/token"
)

// colRand is a fixed-seed source. §8.13 asks for reproducibility, and a test that
// depends on the real seed is a flake.
func colRand() *rand.Rand { return rand.New(rand.NewSource(1)) }

// The collection tests drive the method tables the way the evaluator does — through
// LookupMethod, with the trailing closure both in the argument list and on the Ctx —
// rather than through Eval, so they pin the §12.3/§12.10 contract itself.

// colCtx builds a Ctx on a real Interp, which is what makes CallClosure go through the
// real evaluator seam rather than a stand-in.
func colCtx(t *testing.T, opts Options) *Ctx {
	t.Helper()
	in := New(opts)
	rs := newRunState(in, context.Background(), "test.mzs", nil)
	return newCtx(rs)
}

// colInvoke mirrors call.go's dispatch site exactly: gating, arity, the trailing closure
// argument mirrored onto the Ctx, and `break` turning into the value of the whole call.
func colInvoke(c *Ctx, k Kind, name string, recv Value, args ...Value) (Value, error) {
	m, ok := LookupMethod(k, name)
	if !ok {
		return Nil(), undefinedMethodError(k, name)
	}
	opts := c.Options()
	if !m.Available(&opts) {
		return Nil(), undefinedMethodError(k, name)
	}
	if err := m.CheckArity(len(args)); err != nil {
		return Nil(), err
	}
	restore := c.setCall(name, token.Pos{Line: 1, Col: 1}, args, trailingClosure(args))
	v, err := m.Fn(c, recv, args)
	restore()
	if bv, ok := isBreak(err); ok {
		return bv, nil
	}
	return v, err
}

// colBlock wraps a Go function as a closure value. The evaluator cannot tell it from a
// `{ (x) -> … }` written in a script (§4.2).
func colBlock(f func(c *Ctx, args []Value) (Value, error)) Value {
	return Fn("block", -1, f)
}

// colArg reads a closure parameter leniently: a missing one is nil (§7.7).
func colArg(args []Value, i int) Value {
	if i < 0 || i >= len(args) {
		return Nil()
	}
	return args[i]
}

func colInts(ns ...int64) Value {
	xs := make([]Value, len(ns))
	for i, n := range ns {
		xs[i] = Int(n)
	}
	return Array(xs...)
}

func colStrs(ss ...string) Value {
	xs := make([]Value, len(ss))
	for i, s := range ss {
		xs[i] = Str(s)
	}
	return Array(xs...)
}

var (
	colDouble = colBlock(func(c *Ctx, args []Value) (Value, error) {
		return Int(colArg(args, 0).Int() * 2), nil
	})
	colIsEven = colBlock(func(c *Ctx, args []Value) (Value, error) {
		return Bool(colArg(args, 0).Int()%2 == 0), nil
	})
	colIdentity = colBlock(func(c *Ctx, args []Value) (Value, error) {
		return colArg(args, 0), nil
	})
	colLen = colBlock(func(c *Ctx, args []Value) (Value, error) {
		return Int(int64(colArg(args, 0).Len())), nil
	})
	colSumPair = colBlock(func(c *Ctx, args []Value) (Value, error) {
		return Int(colArg(args, 0).Int() + colArg(args, 1).Int()), nil
	})
)

func TestArrayMethods(t *testing.T) {
	c := colCtx(t, DefaultOptions())

	tests := []struct {
		name    string
		method  string
		recv    Value
		args    []Value
		want    string
		wantErr bool
	}{
		{name: "len", method: "len", recv: colInts(1, 2, 3), want: "3"},
		{name: "count with no argument is len", method: "count", recv: colInts(1, 2, 3), want: "3"},
		{name: "count equal elements", method: "count", recv: colInts(1, 2, 2), args: []Value{Int(2)}, want: "2"},
		{name: "count with a closure", method: "count", recv: colInts(1, 2, 3, 4), args: []Value{colIsEven}, want: "2"},
		{name: "empty true", method: "empty", recv: Array(), want: "true"},
		{name: "empty is false for a zero element", method: "empty", recv: colInts(0), want: "false"},
		{name: "first element", method: "first", recv: colStrs("а", "б"), want: `"а"`},
		{name: "first of empty is nil", method: "first", recv: Array(), want: "nil"},
		{name: "first n", method: "first", recv: colInts(1, 2, 3), args: []Value{Int(2)}, want: "[1,2]"},
		{name: "first n clamps", method: "first", recv: colInts(1), args: []Value{Int(9)}, want: "[1]"},
		{name: "last element", method: "last", recv: colStrs("а", "🌲"), want: `"🌲"`},
		{name: "last n", method: "last", recv: colInts(1, 2, 3), args: []Value{Int(2)}, want: "[2,3]"},
		{name: "a negative count is an error", method: "first", recv: colInts(1), args: []Value{Int(-1)}, wantErr: true},
		{name: "has cyrillic", method: "has", recv: colStrs("да", "нет"), args: []Value{Str("да")}, want: "true"},
		{name: "has compares int and float numerically", method: "has", recv: colInts(1, 2), args: []Value{Float(2)}, want: "true"},
		{name: "has misses", method: "has", recv: colStrs("да"), args: []Value{Str("нет")}, want: "false"},
		{name: "index of a value", method: "index", recv: colStrs("a", "b"), args: []Value{Str("b")}, want: "1"},
		{name: "index missing is nil", method: "index", recv: colStrs("a"), args: []Value{Str("z")}, want: "nil"},
		{name: "index with a closure", method: "index", recv: colInts(1, 3, 4), args: []Value{colIsEven}, want: "2"},
		{name: "join default", method: "join", recv: colStrs("a", "b"), want: `"ab"`},
		{name: "join separator", method: "join", recv: colStrs("Иван", "Пётр"), args: []Value{Str(", ")}, want: `"Иван, Пётр"`},
		{name: "join renders each element with str", method: "join", recv: Array(Int(1), Nil(), Bool(true)),
			args: []Value{Str("-")}, want: `"1--true"`},
		{name: "map", method: "map", recv: colInts(1, 2, 3), args: []Value{colDouble}, want: "[2,4,6]"},
		{name: "map without a closure is an argument error", method: "map", recv: colInts(1), wantErr: true},
		{name: "flat_map", method: "flat_map", recv: Array(colInts(1, 2), colInts(3)),
			args: []Value{colIdentity}, want: "[1,2,3]"},
		{name: "each returns the receiver", method: "each", recv: colInts(1, 2), args: []Value{colIdentity}, want: "[1,2]"},
		{name: "filter", method: "filter", recv: colInts(1, 2, 3, 4), args: []Value{colIsEven}, want: "[2,4]"},
		{name: "reject", method: "reject", recv: colInts(1, 2, 3), args: []Value{colIsEven}, want: "[1,3]"},
		{name: "find", method: "find", recv: colInts(1, 3, 4), args: []Value{colIsEven}, want: "4"},
		{name: "find missing is nil", method: "find", recv: colInts(1, 3), args: []Value{colIsEven}, want: "nil"},
		{name: "any without a closure tests truthiness", method: "any", recv: Array(Nil(), Bool(false)), want: "false"},
		{name: "any counts zero as truthy", method: "any", recv: colInts(0), want: "true"},
		{name: "any with a closure", method: "any", recv: colInts(1, 2), args: []Value{colIsEven}, want: "true"},
		{name: "all on empty", method: "all", recv: Array(), want: "true"},
		{name: "all with a closure", method: "all", recv: colInts(2, 4), args: []Value{colIsEven}, want: "true"},
		{name: "none with a closure", method: "none", recv: colInts(1, 3), args: []Value{colIsEven}, want: "true"},
		{name: "any rejects a value argument", method: "any", recv: colInts(1), args: []Value{Int(1)}, wantErr: true},
		{name: "reduce seeds with the first element", method: "reduce", recv: colInts(1, 2, 3),
			args: []Value{colSumPair}, want: "6"},
		{name: "reduce with an initial value", method: "reduce", recv: colInts(1, 2),
			args: []Value{Int(10), colSumPair}, want: "13"},
		{name: "reduce of empty is nil", method: "reduce", recv: Array(), args: []Value{colSumPair}, want: "nil"},
		{name: "sum", method: "sum", recv: colInts(1, 2, 3), want: "6"},
		{name: "sum of empty is zero", method: "sum", recv: Array(), want: "0"},
		{name: "sum promotes to float", method: "sum", recv: Array(Int(1), Float(0.5)), want: "1.5"},
		{name: "sum with a closure", method: "sum", recv: colStrs("aa", "b"), args: []Value{colLen}, want: "3"},
		{name: "sum of non numbers is a type error", method: "sum", recv: colStrs("a"), wantErr: true},
		{name: "min", method: "min", recv: colInts(3, 1, 2), want: "1"},
		{name: "max", method: "max", recv: colInts(3, 1, 2), want: "3"},
		{name: "min of empty is nil", method: "min", recv: Array(), want: "nil"},
		{name: "min of mixed kinds is an error", method: "min", recv: Array(Int(1), Str("a")), wantErr: true},
		{name: "min with a comparator closure", method: "min", recv: colInts(3, 1, 2),
			args: []Value{colBlock(func(c *Ctx, args []Value) (Value, error) {
				return Int(colArg(args, 1).Int() - colArg(args, 0).Int()), nil
			})}, want: "3"},
		{name: "min_by", method: "min_by", recv: colStrs("ccc", "a", "bb"), args: []Value{colLen}, want: `"a"`},
		{name: "max_by", method: "max_by", recv: colStrs("ccc", "a"), args: []Value{colLen}, want: `"ccc"`},
		{name: "sort", method: "sort", recv: colInts(3, 1, 2), want: "[1,2,3]"},
		{name: "sort strings by code point", method: "sort", recv: colStrs("б", "а"), want: `["а","б"]`},
		{name: "sort with a comparator closure", method: "sort", recv: colInts(1, 2, 3),
			args: []Value{colBlock(func(c *Ctx, args []Value) (Value, error) {
				return Int(colArg(args, 1).Int() - colArg(args, 0).Int()), nil
			})}, want: "[3,2,1]"},
		{name: "sort_by", method: "sort_by", recv: colStrs("ccc", "a", "bb"), args: []Value{colLen},
			want: `["a","bb","ccc"]`},
		{name: "group_by keeps first-seen key order", method: "group_by", recv: colInts(1, 2, 3, 4),
			args: []Value{colIsEven}, want: `{"false":[1,3],"true":[2,4]}`},
		{name: "partition", method: "partition", recv: colInts(1, 2, 3), args: []Value{colIsEven}, want: "[[2],[1,3]]"},
		{name: "tally", method: "tally", recv: colStrs("да", "да", "нет"), want: `{"да":2,"нет":1}`},
		{name: "uniq keeps the first occurrence", method: "uniq", recv: colInts(3, 1, 3, 1), want: "[3,1]"},
		{name: "uniq over unhashable elements", method: "uniq", recv: Array(colInts(1), colInts(1), colInts(2)),
			want: "[[1],[2]]"},
		{name: "uniq with a closure", method: "uniq", recv: colStrs("aa", "bb", "c"), args: []Value{colLen},
			want: `["aa","c"]`},
		{name: "reverse", method: "reverse", recv: colStrs("а", "б"), want: `["б","а"]`},
		{name: "flatten all the way", method: "flatten", recv: Array(Int(1), Array(Int(2), Array(Int(3)))), want: "[1,2,3]"},
		{name: "flatten to a depth", method: "flatten", recv: Array(Int(1), Array(Int(2), Array(Int(3)))),
			args: []Value{Int(1)}, want: "[1,2,[3]]"},
		{name: "compact drops nils", method: "compact", recv: Array(Int(1), Nil(), Int(2)), want: "[1,2]"},
		{name: "dig through mixed containers", method: "dig",
			recv: Array(Dict(Str("generated_text"), Str("ok"))), args: []Value{Int(0), Str("generated_text")},
			want: `"ok"`},
		{name: "dig stops at the first miss", method: "dig", recv: Array(), args: []Value{Int(0), Str("k")}, want: "nil"},
		{name: "slice one element", method: "slice", recv: colInts(1, 2, 3), args: []Value{Int(1)}, want: "[2]"},
		{name: "slice with a length", method: "slice", recv: colInts(1, 2, 3), args: []Value{Int(0), Int(2)}, want: "[1,2]"},
		{name: "slice from the end", method: "slice", recv: colInts(1, 2, 3), args: []Value{Int(-1)}, want: "[3]"},
		{name: "slice out of range is nil", method: "slice", recv: colInts(1), args: []Value{Int(9)}, want: "nil"},
		{name: "slice with a range", method: "slice", recv: colInts(1, 2, 3, 4), args: []Value{rangeOf(1, 2, false)},
			want: "[2,3]"},
		{name: "slice with an exclusive range", method: "slice", recv: colInts(1, 2, 3, 4),
			args: []Value{rangeOf(1, 3, true)}, want: "[2,3]"},
		{name: "take", method: "take", recv: colInts(1, 2, 3), args: []Value{Int(2)}, want: "[1,2]"},
		{name: "drop", method: "drop", recv: colInts(1, 2, 3), args: []Value{Int(2)}, want: "[3]"},
		{name: "take_while", method: "take_while", recv: colInts(2, 4, 5, 6), args: []Value{colIsEven}, want: "[2,4]"},
		{name: "drop_while", method: "drop_while", recv: colInts(2, 4, 5), args: []Value{colIsEven}, want: "[5]"},
		{name: "zip pads with nil", method: "zip", recv: colInts(1, 2), args: []Value{colStrs("a")},
			want: `[[1,"a"],[2,null]]`},
		{name: "each_with_index passes both values", method: "each_with_index", recv: colStrs("a", "b"),
			args: []Value{colIdentity}, want: `["a","b"]`},
		{name: "each_slice without a closure returns the chunks", method: "each_slice", recv: colInts(0, 1, 2, 3, 4),
			args: []Value{Int(2)}, want: "[[0,1],[2,3],[4]]"},
		{name: "each_slice rejects a zero size", method: "each_slice", recv: colInts(1), args: []Value{Int(0)}, wantErr: true},
		{name: "each_cons", method: "each_cons", recv: colInts(1, 2, 3), args: []Value{Int(2)}, want: "[[1,2],[2,3]]"},
		{name: "pack_bytes", method: "pack_bytes", recv: colInts(0x41, 0x42), want: `"AB"`},
		{name: "pack_bytes of nothing is the empty string", method: "pack_bytes", recv: Array(), want: `""`},
		{name: "pack_bytes rejects a value above a byte", method: "pack_bytes", recv: colInts(300), wantErr: true},
		{name: "pack_bytes rejects a negative value", method: "pack_bytes", recv: colInts(-1), wantErr: true},
		{name: "pack_bytes rejects a non-int element", method: "pack_bytes", recv: colStrs("A"), wantErr: true},
		{name: "array is the receiver", method: "array", recv: colInts(1), want: "[1]"},
		{name: "dict from pairs", method: "dict", recv: Array(Array(Str("a"), Int(1)), Array(Str("b"), Int(2))),
			want: `{"a":1,"b":2}`},
		{name: "dict rejects a non-pair", method: "dict", recv: Array(Int(1)), wantErr: true},
		{name: "json with emoji", method: "json", recv: colStrs("привет 👋"), want: `"[\"привет 👋\"]"`},
		{name: "sample is absent without a random source", method: "sample", recv: colInts(1), wantErr: true},
		{name: "shuffle is absent without a random source", method: "shuffle", recv: colInts(1), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := colInvoke(c, KArray, tt.method, tt.recv, tt.args...)
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

func TestArrayMutation(t *testing.T) {
	c := colCtx(t, DefaultOptions())

	tests := []struct {
		name     string
		method   string
		start    []int64
		args     []Value
		want     string // the method's own result
		wantSelf string // the receiver afterwards
		wantErr  bool
	}{
		{name: "push appends and returns the receiver", method: "push", start: []int64{1},
			args: []Value{Int(2), Int(3)}, want: "[1,2,3]", wantSelf: "[1,2,3]"},
		{name: "pop", method: "pop", start: []int64{1, 2}, want: "2", wantSelf: "[1]"},
		{name: "pop on empty is nil", method: "pop", start: nil, want: "nil", wantSelf: "[]"},
		{name: "shift", method: "shift", start: []int64{1, 2}, want: "1", wantSelf: "[2]"},
		{name: "unshift", method: "unshift", start: []int64{2}, args: []Value{Int(0), Int(1)},
			want: "[0,1,2]", wantSelf: "[0,1,2]"},
		{name: "insert in the middle", method: "insert", start: []int64{1, 4}, args: []Value{Int(1), Int(2), Int(3)},
			want: "[1,2,3,4]", wantSelf: "[1,2,3,4]"},
		{name: "insert out of range", method: "insert", start: []int64{1}, args: []Value{Int(9), Int(2)}, wantErr: true},
		{name: "delete_at", method: "delete_at", start: []int64{1, 2, 3}, args: []Value{Int(1)}, want: "2", wantSelf: "[1,3]"},
		{name: "delete_at from the end", method: "delete_at", start: []int64{1, 2}, args: []Value{Int(-1)},
			want: "2", wantSelf: "[1]"},
		{name: "delete_at out of range is nil", method: "delete_at", start: []int64{1}, args: []Value{Int(9)},
			want: "nil", wantSelf: "[1]"},
		{name: "delete removes every equal element", method: "delete", start: []int64{1, 2, 1}, args: []Value{Int(1)},
			want: "[2]", wantSelf: "[2]"},
		{name: "concat", method: "concat", start: []int64{1}, args: []Value{colInts(2, 3)},
			want: "[1,2,3]", wantSelf: "[1,2,3]"},
		{name: "sort_in_place replaces the contents", method: "sort_in_place", start: []int64{3, 1, 2},
			want: "[1,2,3]", wantSelf: "[1,2,3]"},
		{name: "reverse_in_place", method: "reverse_in_place", start: []int64{1, 2},
			want: "[2,1]", wantSelf: "[2,1]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recv := colInts(tt.start...)
			got, err := colInvoke(c, KArray, tt.method, recv, tt.args...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("%s error = %v; wantErr %v", tt.method, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.Inspect() != tt.want {
				t.Errorf("%s = %s; want %s", tt.method, got.Inspect(), tt.want)
			}
			if recv.Inspect() != tt.wantSelf {
				t.Errorf("after %s the receiver is %s; want %s", tt.method, recv.Inspect(), tt.wantSelf)
			}
		})
	}
}

// pack_bytes closes the loop String#bytes opens (§12.3): whatever a string is taken
// apart into puts it back together byte for byte, multi-byte runes included.
func TestPackBytesRoundTrip(t *testing.T) {
	c := colCtx(t, DefaultOptions())

	for _, s := range []string{"", "ascii", "привет", "héllo 🌲", "\x00\x01\xff"} {
		t.Run(s, func(t *testing.T) {
			got, err := colInvoke(c, KString, "bytes", Str(s))
			if err != nil {
				t.Fatalf("bytes: %v", err)
			}
			back, err := colInvoke(c, KArray, "pack_bytes", got)
			if err != nil {
				t.Fatalf("pack_bytes: %v", err)
			}
			if back.Str() != s {
				t.Errorf("round trip = %q; want %q", back.Str(), s)
			}
		})
	}

	// The rows work on bytes, not runes, so a packed string can be invalid UTF-8 — the
	// same thing `io.read` of a binary file produces (§12.13). Nothing panics; the
	// rune-based rows just see the replacement character.
	t.Run("a packed string may not be valid UTF-8", func(t *testing.T) {
		v, err := colInvoke(c, KArray, "pack_bytes", colInts(0xff, 0xfe))
		if err != nil {
			t.Fatalf("pack_bytes: %v", err)
		}
		if len(v.Str()) != 2 {
			t.Errorf("packed %d bytes; want 2", len(v.Str()))
		}
		if got := v.Len(); got != 2 {
			t.Errorf("len = %d; want 2 replacement runes", got)
		}
	})
}

// TestArrayAliasing pins reference semantics (§7.1): a mutation is visible through
// every value that shares the array, and the pure twin of a mutating row is not.
func TestArrayAliasing(t *testing.T) {
	c := colCtx(t, DefaultOptions())
	a := colInts(2, 1)
	b := a
	if _, err := colInvoke(c, KArray, "push", a, Int(3)); err != nil {
		t.Fatalf("push: %v", err)
	}
	if b.Inspect() != "[2,1,3]" {
		t.Errorf("alias sees %s; want [2,1,3]", b.Inspect())
	}
	if _, err := colInvoke(c, KArray, "sort", a); err != nil {
		t.Fatalf("sort: %v", err)
	}
	if b.Inspect() != "[2,1,3]" {
		t.Errorf("sort mutated the receiver: alias sees %s; want [2,1,3]", b.Inspect())
	}
	if _, err := colInvoke(c, KArray, "sort_in_place", a); err != nil {
		t.Fatalf("sort_in_place: %v", err)
	}
	if b.Inspect() != "[1,2,3]" {
		t.Errorf("alias after sort_in_place sees %s; want [1,2,3]", b.Inspect())
	}
}

// TestArrayClosureProtocol pins the four outcomes of CallClosure a stdlib method must
// not interpret: a value, `next`, `break` and `return` (host.go's closure protocol).
func TestArrayClosureProtocol(t *testing.T) {
	c := colCtx(t, DefaultOptions())

	tests := []struct {
		name    string
		method  string
		block   Value
		want    string
		wantErr bool
		isCtrl  ctrlKind
	}{
		{
			name:   "next is absorbed and becomes the closure's value",
			method: "map",
			block: colBlock(func(c *Ctx, args []Value) (Value, error) {
				return Nil(), nextSignal(Int(7))
			}),
			want: "[7,7,7]",
		},
		{
			name:   "break ends the whole method call",
			method: "each",
			block: colBlock(func(c *Ctx, args []Value) (Value, error) {
				return Nil(), breakSignal(Int(1))
			}),
			want: "1",
		},
		{
			name:   "return travels out of the method",
			method: "map",
			block: colBlock(func(c *Ctx, args []Value) (Value, error) {
				return Nil(), returnSignal(Int(5))
			}),
			wantErr: true,
			isCtrl:  ctrlReturn,
		},
		{
			name:   "a script error propagates unchanged",
			method: "filter",
			block: colBlock(func(c *Ctx, args []Value) (Value, error) {
				return Nil(), c.Errorf("boom")
			}),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := colInvoke(c, KArray, tt.method, colInts(1, 2, 3), tt.block)
			if (err != nil) != tt.wantErr {
				t.Fatalf("%s error = %v; wantErr %v", tt.method, err, tt.wantErr)
			}
			if tt.wantErr {
				if tt.isCtrl != 0 {
					sig, ok := ctrlOf(err)
					if !ok || sig.kind != tt.isCtrl {
						t.Fatalf("%s error = %v; want control signal %d", tt.method, err, tt.isCtrl)
					}
				}
				return
			}
			if got.Inspect() != tt.want {
				t.Errorf("%s = %s; want %s", tt.method, got.Inspect(), tt.want)
			}
		})
	}
}

func TestRangeMethods(t *testing.T) {
	c := colCtx(t, DefaultOptions())

	tests := []struct {
		name    string
		method  string
		recv    Value
		args    []Value
		want    string
		wantErr bool
	}{
		{name: "array inclusive", method: "array", recv: rangeOf(0, 3, false), want: "[0,1,2,3]"},
		{name: "array exclusive", method: "array", recv: rangeOf(0, 3, true), want: "[0,1,2]"},
		{name: "a descending range is empty", method: "array", recv: rangeOf(5, 1, false), want: "[]"},
		{name: "len", method: "len", recv: rangeOf(1, 10, false), want: "10"},
		{name: "len of a huge range costs nothing", method: "len", recv: rangeOf(1, 1_000_000_000, false),
			want: "1000000000"},
		{name: "empty", method: "empty", recv: rangeOf(2, 1, false), want: "true"},
		{name: "has inside", method: "has", recv: rangeOf(1, 10, false), args: []Value{Int(3)}, want: "true"},
		{name: "has excludes the end", method: "has", recv: rangeOf(1, 3, true), args: []Value{Int(3)}, want: "false"},
		{name: "has a float", method: "has", recv: rangeOf(1, 3, false), args: []Value{Float(2.5)}, want: "true"},
		{name: "has a string is false", method: "has", recv: rangeOf(1, 3, false), args: []Value{Str("2")}, want: "false"},
		{name: "first", method: "first", recv: rangeOf(2, 5, false), want: "2"},
		{name: "first n", method: "first", recv: rangeOf(2, 5, false), args: []Value{Int(2)}, want: "[2,3]"},
		{name: "first n of a huge range is cheap", method: "first", recv: rangeOf(1, 1_000_000_000, false),
			args: []Value{Int(3)}, want: "[1,2,3]"},
		{name: "last n of a huge range is cheap", method: "last", recv: rangeOf(1, 1_000_000_000, false),
			args: []Value{Int(2)}, want: "[999999999,1000000000]"},
		{name: "take n of a huge range is cheap", method: "take", recv: rangeOf(1, 1_000_000_000, false),
			args: []Value{Int(2)}, want: "[1,2]"},
		{name: "last", method: "last", recv: rangeOf(2, 5, false), want: "5"},
		{name: "last of an exclusive range", method: "last", recv: rangeOf(2, 5, true), want: "4"},
		{name: "map", method: "map", recv: rangeOf(1, 3, false), args: []Value{colDouble}, want: "[2,4,6]"},
		{name: "filter", method: "filter", recv: rangeOf(1, 4, false), args: []Value{colIsEven}, want: "[2,4]"},
		{name: "reject", method: "reject", recv: rangeOf(1, 4, false), args: []Value{colIsEven}, want: "[1,3]"},
		{name: "sum", method: "sum", recv: rangeOf(1, 4, false), want: "10"},
		{name: "min", method: "min", recv: rangeOf(3, 7, false), want: "3"},
		{name: "max", method: "max", recv: rangeOf(3, 7, false), want: "7"},
		{name: "reverse", method: "reverse", recv: rangeOf(1, 3, false), want: "[3,2,1]"},
		{name: "pack_bytes", method: "pack_bytes", recv: rangeOf(0x41, 0x43, false), want: `"ABC"`},
		{name: "step", method: "step", recv: rangeOf(0, 10, false), args: []Value{Int(3)}, want: "[0,3,6,9]"},
		{name: "step rejects zero", method: "step", recv: rangeOf(0, 10, false), args: []Value{Int(0)}, wantErr: true},
		{name: "each_slice chains to array", method: "each_slice", recv: rangeOf(0, 6, false),
			args: []Value{Int(2)}, want: "[[0,1],[2,3],[4,5],[6]]"},
		{name: "reduce", method: "reduce", recv: rangeOf(1, 4, false), args: []Value{colSumPair}, want: "10"},
		{name: "each returns the receiver", method: "each", recv: rangeOf(1, 2, false),
			args: []Value{colIdentity}, want: "1..2"},
		{name: "a range cannot be pushed to", method: "push", recv: rangeOf(1, 2, false),
			args: []Value{Int(3)}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := colInvoke(c, KRange, tt.method, tt.recv, tt.args...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("%s error = %v; wantErr %v", tt.method, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.Inspect() != tt.want {
				t.Errorf("%s = %s; want %s", tt.method, got.Inspect(), tt.want)
			}
		})
	}

	// §12.10: a range answers true to is("array") while type(r) stays "range".
	t.Run("a range reports itself as a range", func(t *testing.T) {
		r := rangeOf(1, 3, false)
		if got := r.TypeName(); got != "range" {
			t.Errorf("type = %q; want \"range\"", got)
		}
		if yes, known := isKindName(r, "array"); !yes || !known {
			t.Errorf(`is("array") = %v, known %v; want true`, yes, known)
		}
	})
}

// TestArrayLimits pins that the collection cap and the depth guard are enforced rather
// than left to the Go allocator or the Go stack.
func TestArrayLimits(t *testing.T) {
	tests := []struct {
		name    string
		run     func(c *Ctx) (Value, error)
		opts    Options
		wantErr bool
	}{
		{
			name: "materialising a range past MaxCollection is a limit error",
			opts: Options{MaxCollection: 10},
			run: func(c *Ctx) (Value, error) {
				return colInvoke(c, KRange, "array", rangeOf(1, 1000, false))
			},
			wantErr: true,
		},
		{
			name: "a range inside the cap materialises",
			opts: Options{MaxCollection: 10},
			run: func(c *Ctx) (Value, error) {
				return colInvoke(c, KRange, "array", rangeOf(1, 5, false))
			},
		},
		{
			name: "flatten refuses a self-referential array",
			opts: DefaultOptions(),
			run: func(c *Ctx) (Value, error) {
				a := Array()
				a.Append(a)
				return colInvoke(c, KArray, "flatten", a)
			},
			wantErr: true,
		},
		{
			name: "push past MaxCollection is a limit error",
			opts: Options{MaxCollection: 2},
			run: func(c *Ctx) (Value, error) {
				return colInvoke(c, KArray, "push", colInts(1, 2), Int(3))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := colCtx(t, tt.opts)
			_, err := tt.run(c)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v; wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestCollectionScripts runs the collection surface the way a user writes it, through
// the whole front end, which is the only way to cover the implicit `it` parameter
// (§8.9) and the chaining that makes corpus rows 50–52 work.
func TestCollectionScripts(t *testing.T) {
	opts := DefaultOptions()
	opts.EnableTime = true
	opts.Now = colClock
	in := New(opts)

	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "implicit it", src: `[1,2,3].map { it * 2 }`, want: "[2,4,6]"},
		{name: "an explicit closure parameter", src: `[1,2,3].filter { (x) -> x % 2 == 1 }`, want: "[1,3]"},
		{name: "corpus row 50", src: `3.times.each { (t) -> t.str }`, want: "[0,1,2]"},
		{name: "corpus row 51", src: `(0..6).map { (n) -> n }.each_slice(2).array`, want: "[[0,1],[2,3],[4,5],[6]]"},
		{name: "corpus row 52 round-trips through json",
			src:  `(0..2).map { (n) -> [text: n.str] }.each_slice(2).array.json`,
			want: `"[[{\"text\":\"0\"},{\"text\":\"1\"}],[{\"text\":\"2\"}]]"`},
		{name: "corpus row 38", src: "include json\n" + `json.parse('[{"generated_text":"ok"}]').dig(0, "generated_text") ?? "нет"`,
			want: `"ok"`},
		{name: "corpus row 39", src: "include json\n" + `json.parse('[]').dig(0, "generated_text") ?? "нет"`, want: `"нет"`},
		{name: "corpus row 49", src: `["да", "ага", "конечно"].has("ага")`, want: "true"},
		{name: "break is the value of the call", src: `[1,2,3].each { break it * 10 }`, want: "10"},
		{name: "next supplies the closure's value", src: `[1,2,3].map { next 0 if it == 2; it }`, want: "[1,0,3]"},
		{name: "map over a dict yields two parameters",
			src: `[a: 1, b: 2].map { (k, v) -> "${k}=${v}" }.join(",")`, want: `"a=1,b=2"`},
		{name: "merge keeps insertion order", src: `[a: 1].merge([b: 2]).json`, want: `"{\"a\":1,\"b\":2}"`},
		{name: "dig through mixed containers", src: `[a: [b: [10, 20]]].dig("a", "b", 1)`, want: "20"},
		{name: "sort_by over a dict", src: `[a: 2, b: 1].sort_by { (k, v) -> v }.first.first`, want: `"b"`},
		{name: "sum with a closure", src: `[1,2,3].sum { it * 2 }`, want: "12"},
		{name: "reduce over a range", src: `(1..4).reduce { (a, b) -> a + b }`, want: "10"},
		{name: "cyrillic keys survive json", src: `["имя": "Иван"].json`, want: `"{\"имя\":\"Иван\"}"`},
		{name: "UFCS reaches a row as a free function", src: `filter([1,2,3,4], { it % 2 == 0 })`, want: "[2,4]"},
		{name: "time parse and strftime", src: "include time\n" + `time.parse("12/03/25").strftime("%d.%m.%Y")`, want: `"12.03.2025"`},
		{name: "date today", src: "include date\n" + `date.today.strftime("%Y-%m-%d")`, want: `"2025-03-12"`},
		{name: "date parse", src: "include date\n" + `date.parse("12/03/25").year`, want: "2025"},
		{name: "math module", src: "include math\n" + `math.sqrt(16)`, want: "4.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := in.Eval(context.Background(), tt.src, nil)
			if err != nil {
				t.Fatalf("Eval(%s) error = %v", tt.src, err)
			}
			if got.Inspect() != tt.want {
				t.Errorf("Eval(%s) = %s; want %s", tt.src, got.Inspect(), tt.want)
			}
		})
	}
}

// TestArrayRandomGating pins D15: sample and shuffle exist only when the host installs
// a random source, and are hidden — not failing — otherwise.
func TestArrayRandomGating(t *testing.T) {
	if _, err := colInvoke(colCtx(t, DefaultOptions()), KArray, "sample", colInts(1, 2)); err == nil {
		t.Errorf("sample without Options.Rand = nil error; want undefined method")
	}
	opts := DefaultOptions()
	opts.Rand = colRand()
	c := colCtx(t, opts)
	got, err := colInvoke(c, KArray, "sample", colInts(7))
	if err != nil || got.Int() != 7 {
		t.Errorf("sample with a random source = %s, %v; want 7", got.Inspect(), err)
	}
	shuffled, err := colInvoke(c, KArray, "shuffle", colInts(1, 2, 3))
	if err != nil || shuffled.Len() != 3 {
		t.Errorf("shuffle = %s, %v; want three elements", shuffled.Inspect(), err)
	}
}

// D17 for §12.3: the Ruby collection spellings and the `!` mutators are gone; the two
// mutating rows that survived are named at the call site instead.
func TestArrayHasNoOldNames(t *testing.T) {
	tests := []struct {
		old, use string
	}{
		{"length", "len"},
		{"size", "len"},
		{"include", "has"},
		{"contains", "has"},
		{"cover", "has"},
		{"collect", "map"},
		{"select", "filter"},
		{"detect", "find"},
		{"inject", "reduce"},
		{"find_index", "index"},
		{"each_index", "each_with_index"},
		{"to_a", "array"},
		{"to_h", "dict"},
		{"to_json", "json"},
		{"clear", "—"},
		{"sort!", "sort_in_place"},
		{"reverse!", "reverse_in_place"},
		{"uniq!", "uniq"},
		{"map!", "map"},
		{"select!", "filter"},
		{"reject!", "reject"},
	}

	for _, tt := range tests {
		t.Run(tt.old, func(t *testing.T) {
			for _, k := range []Kind{KArray, KRange} {
				if HasMethod(k, tt.old) {
					t.Errorf("%s answers %q; D17 allows only %q", k, tt.old, tt.use)
				}
			}
		})
	}

	// The mutating rows have nothing to write to on a range (§12.10).
	for _, name := range []string{"push", "pop", "shift", "unshift", "insert", "delete",
		"delete_at", "concat", "sort_in_place", "reverse_in_place"} {
		t.Run("range has no "+name, func(t *testing.T) {
			if HasMethod(KRange, name) {
				t.Errorf("range answers %q; §12.10 lists only the reading rows", name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Every row, one property at a time
// ---------------------------------------------------------------------------
//
// The two tables below walk the *registry* rather than a list written by hand, so a row
// added to §12.3 tomorrow is covered the moment it is registered — and a row missing
// from arrayRowArgs fails the test instead of being silently skipped.

// arrayRowArgs is a working argument list for every array row: what the row needs in
// order to actually do its job on the receiver. Rows whose argument is optional get the
// spelling that makes them iterate, because that is the interesting case for a limit.
var arrayRowArgs = map[string][]Value{
	"all": {colIsEven}, "any": {colIsEven}, "none": {colIsEven},
	"array": nil, "compact": nil, "dict": nil, "empty": nil, "json": nil,
	"len": nil, "pack_bytes": nil, "pop": nil, "reverse": nil, "shift": nil,
	"tally": nil, "uniq": nil, "sample": nil, "shuffle": nil,
	"count": {colIsEven}, "find": {colIsEven}, "filter": {colIsEven}, "reject": {colIsEven},
	"take_while": {colIsEven}, "drop_while": {colIsEven},
	"each": {colIdentity}, "each_with_index": {colIdentity}, "map": {colIdentity},
	"flat_map": {colIdentity}, "group_by": {colIsEven}, "partition": {colIsEven},
	"min_by": {colIdentity}, "max_by": {colIdentity}, "sort_by": {colIdentity},
	"min": nil, "max": nil, "sum": nil, "sort": nil, "sort_in_place": nil,
	"reverse_in_place": nil, "flatten": nil, "first": nil, "last": nil,
	"reduce":     {colSumPair},
	"each_slice": {Int(2)}, "each_cons": {Int(2)}, "step": {Int(2)},
	"slice": {Int(0), Int(2)}, "take": {Int(2)}, "drop": {Int(2)},
	"has": {Int(5)}, "index": {Int(5)}, "delete": {Int(5)}, "delete_at": {Int(0)},
	"dig": {Int(0)}, "insert": {Int(0), Int(9)}, "push": {Int(9)}, "unshift": {Int(9)},
	"concat": {colInts(7, 8)}, "zip": {colInts(7, 8)},
	"join": {Str("-")},
}

// arrayRowNames is every row registered for arrays and ranges, deduplicated.
func arrayRowNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range []Kind{KArray, KRange} {
		for _, n := range MethodNames(k) {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}

// arrayRowKind is the kind that registers the row: everything is on KArray except the
// one row §12.10 gives a range alone.
func arrayRowKind(name string) Kind {
	if HasMethod(KArray, name) {
		return KArray
	}
	return KRange
}

func TestArrayRowArgsTableIsComplete(t *testing.T) {
	for _, name := range arrayRowNames() {
		if _, ok := arrayRowArgs[name]; !ok {
			t.Errorf("no arguments listed for the %q row; add it to arrayRowArgs so the "+
				"property tests below cover it", name)
		}
	}
}

// A7 in the small: a receiver of the wrong kind is a diagnostic, never a panic. Only a
// host can produce this — dispatch inside a script finds the row by the receiver's kind
// — but LookupMethod is exported (§13), so the guard has to hold anyway.
func TestArrayRowsRefuseANonArrayReceiver(t *testing.T) {
	c := colCtx(t, optsWithRand())

	for _, name := range arrayRowNames() {
		t.Run(name, func(t *testing.T) {
			_, err := colInvoke(c, arrayRowKind(name), name, Str("не массив"), arrayRowArgs[name]...)
			if err == nil {
				t.Fatalf("%q accepted a string receiver; want a type error", name)
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

// §14.1: a row that walks the receiver charges the step budget for it, so a big array
// cannot outrun the interruption point. The budget is sampled every stepCheckInterval
// steps, which is why the receiver here is longer than that.
//
// The two lists are the assertion, and they record something the code alone does not:
// which rows answer in constant time, and which copy a bounded slice of the receiver
// today **without** charging for it. The second list is not an exemption — the work is
// bounded by the receiver, which MaxCollection already capped — but if §14.1 is ever
// tightened, that is the list that moves. Everything else must come back with ErrBudget.
func TestArrayRowsChargeTheStepBudget(t *testing.T) {
	constantTime := map[string]bool{
		"len": true, "empty": true, "first": true, "last": true, "dig": true,
		"push": true, "pop": true, "shift": true, "unshift": true, "insert": true,
		"delete_at": true, "sample": true, "array": true,
	}
	unchargedCopy := map[string]bool{
		"slice": true, "take": true, "drop": true, "concat": true,
	}

	opts := optsWithRand()
	opts.StepBudget = 1

	const n = 4 * stepCheckInterval
	ints := make([]Value, n)
	pairs := make([]Value, n)
	bytes := make([]Value, n)
	for i := range ints {
		ints[i] = Int(int64(i))
		pairs[i] = Array(Int(int64(i)), Int(int64(i)))
		bytes[i] = Int(int64(i % 256))
	}
	// Three rows need a receiver of their own shape, or they would fail on the element
	// type — or, for `step`, on the receiver kind — before reaching the walk this test
	// is about.
	recv := map[string]Value{
		"dict":       Array(pairs...),
		"pack_bytes": Array(bytes...),
		"step":       rangeOf(0, int64(n-1), false),
	}

	for _, name := range arrayRowNames() {
		t.Run(name, func(t *testing.T) {
			c := colCtx(t, opts)
			xs, ok := recv[name]
			if !ok {
				xs = Array(ints...)
			}
			_, err := colInvoke(c, arrayRowKind(name), name, xs, arrayRowArgs[name]...)
			if constantTime[name] || unchargedCopy[name] {
				// Not "no budget error" but "no error at all": a row that fails for
				// another reason would otherwise sit in these lists proving nothing.
				if err != nil {
					t.Errorf("%q = %v; a row listed as not charging must answer on a "+
						"budget of one step", name, err)
				}
				return
			}
			if !errors.Is(err, ErrBudget) {
				t.Errorf("%q walked %d elements on a budget of one step and returned %v; "+
					"either it charges nothing (§14.1) or it belongs in one of the lists above", name, n, err)
			}
		})
	}
}

// optsWithRand is the full-capability Options these property tables need: `sample` and
// `shuffle` are gated on a random source and would otherwise be skipped as absent.
func optsWithRand() Options {
	o := DefaultOptions()
	o.Rand = colRand()
	return o
}

// colBoom is a closure that always raises, for the rows that take one.
var colBoom = colBlock(func(c *Ctx, args []Value) (Value, error) {
	return Nil(), c.Errorf("boom")
})

// A closure is script code, and script code raises. Every row that calls one has to
// stop there and hand the error up, rather than treating the failure as a value —
// which is what makes `xs.map { raise("…") }` land where it was written (§8.11).
func TestArrayRowsPropagateFromTheirClosure(t *testing.T) {
	c := colCtx(t, optsWithRand())

	ran := 0
	for _, name := range arrayRowNames() {
		args := make([]Value, len(arrayRowArgs[name]))
		copy(args, arrayRowArgs[name])
		takesClosure := false
		for i, a := range args {
			if a.Kind() == KFunc {
				args[i] = colBoom
				takesClosure = true
			}
		}
		if !takesClosure {
			continue
		}
		ran++
		t.Run(name, func(t *testing.T) {
			_, err := colInvoke(c, arrayRowKind(name), name, colInts(1, 2, 3), args...)
			if err == nil {
				t.Fatalf("%q swallowed the error its closure raised", name)
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("%q returned %T (%v); want the *Error the closure raised", name, err, err)
			}
			if e.Msg != "boom" {
				t.Errorf("%q reported %q; want the closure's own message", name, e.Msg)
			}
		})
	}
	if ran < 15 {
		t.Errorf("only %d rows were exercised; arrayRowArgs seems to have lost its closures", ran)
	}
}

// A row that needs a closure says so when it is handed something else. The three rows
// that accept *either* a value or a closure are excluded by name, because for them a
// value is the other half of the contract, not a mistake (§12.3).
func TestArrayRowsNeedingAClosureRefuseAValue(t *testing.T) {
	valueOrClosure := map[string]bool{"count": true, "index": true, "has": true}
	c := colCtx(t, optsWithRand())

	for _, name := range arrayRowNames() {
		if valueOrClosure[name] {
			continue
		}
		args := make([]Value, len(arrayRowArgs[name]))
		copy(args, arrayRowArgs[name])
		takesClosure := false
		for i, a := range args {
			if a.Kind() == KFunc {
				args[i] = Int(1)
				takesClosure = true
			}
		}
		if !takesClosure {
			continue
		}
		t.Run(name, func(t *testing.T) {
			if _, err := colInvoke(c, arrayRowKind(name), name, colInts(1, 2, 3), args...); err == nil {
				t.Errorf("%q accepted an int where it needs a closure", name)
			}
		})
	}
}

// each_slice and each_cons have two shapes: without a closure they return the chunks,
// with one they iterate and return the receiver (§12.3). The second shape is the one a
// script uses on a big array, and it is the one that has to stop on a raise.
func TestChunkingRowsWithAClosure(t *testing.T) {
	c := colCtx(t, DefaultOptions())

	for _, name := range []string{"each_slice", "each_cons"} {
		t.Run(name+" iterates and returns the receiver", func(t *testing.T) {
			seen := 0
			count := colBlock(func(c *Ctx, args []Value) (Value, error) {
				seen++
				return Nil(), nil
			})
			got, err := colInvoke(c, KArray, name, colInts(1, 2, 3, 4), Int(2), count)
			if err != nil {
				t.Fatalf("%s error = %v", name, err)
			}
			if got.Inspect() != "[1,2,3,4]" {
				t.Errorf("%s returned %s; want the receiver", name, got.Inspect())
			}
			if seen == 0 {
				t.Errorf("%s never called its closure", name)
			}
		})

		t.Run(name+" stops on a raise", func(t *testing.T) {
			_, err := colInvoke(c, KArray, name, colInts(1, 2, 3, 4), Int(2), colBoom)
			if err == nil {
				t.Errorf("%s swallowed the error its closure raised", name)
			}
		})
	}
}
