package mzs

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"mzs/internal/rx"
)

// The seam a host actually touches (§13.1): From lifts a Go value in, Interface hands
// one back, and the accessors have to answer for a receiver of the wrong kind rather
// than reach into a pointer that is not there (A7). None of this is reachable from a
// script, which is exactly why it needs its own tests: the corpus cannot find a bug
// here, and morzebot is on the other side of it.

func TestFromEveryGoScalar(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want Value
	}{
		{"nil", nil, Nil()},
		{"a Value passes through", Str("да"), Str("да")},
		{"bool", true, Bool(true)},
		{"int", int(7), Int(7)},
		{"int8", int8(7), Int(7)},
		{"int16", int16(7), Int(7)},
		{"int32", int32(7), Int(7)},
		{"int64", int64(7), Int(7)},
		{"uint", uint(7), Int(7)},
		{"uint8", uint8(7), Int(7)},
		{"uint16", uint16(7), Int(7)},
		{"uint32", uint32(7), Int(7)},
		{"uint64", uint64(7), Int(7)},
		{"the largest uint64 that still fits", uint64(math.MaxInt64), Int(math.MaxInt64)},
		{"float32", float32(1.5), Float(1.5)},
		{"float64", float64(1.5), Float(1.5)},
		{"string", "привет", Str("привет")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := From(tt.in)
			if err != nil {
				t.Fatalf("From(%#v) error = %v", tt.in, err)
			}
			if !stdSame(got, tt.want) {
				t.Errorf("From(%#v) = %s; want %s", tt.in, got.Inspect(), tt.want.Inspect())
			}
		})
	}
}

// The one Go number the value model has no room for: above math.MaxInt64 there is no
// Int, so D9 decides — it promotes to Float, exactly as an overflowing `+` does, rather
// than wrapping into a negative. A host counter or an unsigned id must not arrive as a
// negative number that every later comparison then reads backwards.
func TestFromLargeUnsignedPromotesInsteadOfWrapping(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want float64
	}{
		{"one past the largest int64", uint64(math.MaxInt64) + 1, float64(uint64(math.MaxInt64) + 1)},
		{"the largest uint64", uint64(math.MaxUint64), float64(uint64(math.MaxUint64))},
		{"a uint of the same magnitude", uint(math.MaxUint64), float64(uint64(math.MaxUint64))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := From(tt.in)
			if err != nil {
				t.Fatalf("From(%v) error = %v", tt.in, err)
			}
			if got.Kind() != KFloat {
				t.Fatalf("From(%v) = %s (%s); want a float, since no int64 holds it",
					tt.in, got.Inspect(), got.Kind())
			}
			if got.Float() != tt.want {
				t.Errorf("From(%v) = %s; want %g", tt.in, got.Inspect(), tt.want)
			}
			if got.Float() < 0 {
				t.Errorf("From(%v) = %s; a positive host number must never arrive negative",
					tt.in, got.Inspect())
			}
		})
	}

	// The smaller unsigned kinds always fit, so they stay Ints.
	for _, in := range []any{uint8(math.MaxUint8), uint16(math.MaxUint16), uint32(math.MaxUint32)} {
		got, err := From(in)
		if err != nil || got.Kind() != KInt {
			t.Errorf("From(%T %v) = %s, %v; want an int", in, in, got.Inspect(), err)
		}
	}
}

func TestFromCompositeGoValues(t *testing.T) {
	when := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	re, err := rx.Compile("до", "")
	if err != nil {
		t.Fatalf("rx.Compile: %v", err)
	}

	tests := []struct {
		name string
		in   any
		want string // Inspect of the result
	}{
		{"[]any", []any{1, "два"}, `[1,"два"]`},
		{"[]string through reflect", []string{"а", "б"}, `["а","б"]`},
		{"[]int through reflect", []int{1, 2}, `[1,2]`},
		{"an array through reflect", [2]int{1, 2}, `[1,2]`},
		{"map[string]any", map[string]any{"b": 2, "a": 1}, `{"a":1,"b":2}`},
		{"map[string]string", map[string]string{"b": "2", "a": "1"}, `{"a":"1","b":"2"}`},
		{"map[string]int through reflect", map[string]int{"b": 2, "a": 1}, `{"a":1,"b":2}`},
		{"json.RawMessage", json.RawMessage(`{"k":[1,2]}`), `{"k":[1,2]}`},
		{"a struct marshals through JSON", struct {
			Name string `json:"name"`
			Qty  int    `json:"qty"`
		}{"гель", 3}, `{"name":"гель","qty":3}`},
		{"a pointer is followed", func() any { n := 42; return &n }(), `42`},
		{"a nil pointer is nil", (*int)(nil), `nil`},
		{"time.Time", when, when.Format("2006-01-02 15:04:05")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := From(tt.in)
			if err != nil {
				t.Fatalf("From(%#v) error = %v", tt.in, err)
			}
			if tt.name == "time.Time" {
				if got.Kind() != KTime || !got.Time().Equal(when) {
					t.Errorf("From(time.Time) = %s; want the same instant", got.Inspect())
				}
				return
			}
			if got.Inspect() != tt.want {
				t.Errorf("From(%#v) = %s; want %s", tt.in, got.Inspect(), tt.want)
			}
		})
	}

	t.Run("*rx.Regexp", func(t *testing.T) {
		got, err := From(re)
		if err != nil || got.Kind() != KRegex {
			t.Fatalf("From(*rx.Regexp) = %s, %v; want a regex", got.Inspect(), err)
		}
	})
	t.Run("*Func", func(t *testing.T) {
		f := Fn("double", 1, func(c *Ctx, args []Value) (Value, error) { return args[0], nil })
		got, err := From(f.fn())
		if err != nil || got.Kind() != KFunc {
			t.Fatalf("From(*Func) = %s, %v; want a function", got.Inspect(), err)
		}
	})
}

// Determinism (§8.13): a Go map has no order, so From sorts the keys. Without this the
// same host value would produce a different JSON string from run to run.
func TestFromSortsMapKeys(t *testing.T) {
	in := map[string]any{"я": 1, "б": 2, "а": 3, "z": 4, "a": 5}
	first, err := From(in)
	if err != nil {
		t.Fatalf("From error = %v", err)
	}
	for i := 0; i < 20; i++ {
		again, err := From(in)
		if err != nil {
			t.Fatalf("From error = %v", err)
		}
		if again.Inspect() != first.Inspect() {
			t.Fatalf("From of the same map gave %s then %s; key order must be sorted",
				first.Inspect(), again.Inspect())
		}
	}
}

func TestFromRejectsWhatItCannotConvert(t *testing.T) {
	tests := []struct {
		name string
		in   any
	}{
		{"a channel", make(chan int)},
		{"a map with non-string keys", map[int]string{1: "a"}},
		{"a struct that cannot marshal", struct{ C chan int }{make(chan int)}},
		{"a slice of unconvertible values", []chan int{make(chan int)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if v, err := From(tt.in); err == nil {
				t.Errorf("From(%T) = %s, nil; want an error", tt.in, v.Inspect())
			}
		})
	}
}

// MustFrom is the spelling for host code that knows its input: same result, and a panic
// instead of an error. The panic is the host's own, not a script's (A7 is about scripts).
func TestMustFrom(t *testing.T) {
	if got := MustFrom([]int{1, 2}); got.Inspect() != `[1,2]` {
		t.Errorf("MustFrom = %s; want [1,2]", got.Inspect())
	}
	defer func() {
		if recover() == nil {
			t.Error("MustFrom(chan) did not panic")
		}
	}()
	MustFrom(make(chan int))
}

// Interface is the way back out, and it is the shape §13.1 promises for each kind.
func TestInterfaceRoundTrip(t *testing.T) {
	when := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	fn := Fn("f", 0, func(c *Ctx, args []Value) (Value, error) { return Nil(), nil })

	tests := []struct {
		name string
		in   Value
		want any
	}{
		{"nil", Nil(), nil},
		{"bool", Bool(true), true},
		{"int", Int(7), int64(7)},
		{"float", Float(1.5), 1.5},
		{"string", Str("да"), "да"},
		{"array", Array(Int(1), Str("а")), []any{int64(1), "а"}},
		{"range materialises", rangeOf(1, 3, false), []any{int64(1), int64(2), int64(3)}},
		{"dict", Dict(Str("a"), Int(1)), map[string]any{"a": int64(1)}},
		{"time", timeOf(when), when},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.Interface(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Interface() = %#v; want %#v", got, tt.want)
			}
		})
	}

	if got := fn.Interface(); got == nil {
		t.Error("Interface() of a function = nil; want the *Func")
	}
	if got := Nil().String(); got != "" {
		t.Errorf("String() of nil = %q; want the empty string", got)
	}
	if got := Int(7).String(); got != "7" {
		t.Errorf("String() = %q; want %q", got, "7")
	}
}

// An accessor asked about the wrong kind answers with the zero value instead of
// dereferencing whatever the other kind stored in the same field.
func TestAccessorsOfTheWrongKind(t *testing.T) {
	s := Str("не коллекция")

	if got := s.Elems(); got != nil {
		t.Errorf("Elems() of a string = %#v; want nil", got)
	}
	if got := s.Time(); !got.IsZero() {
		t.Errorf("Time() of a string = %v; want the zero time", got)
	}
	if got := s.Get(Str("k")); !got.IsNil() {
		t.Errorf("Get() on a string = %s; want nil", got.Inspect())
	}
	if got := s.Keys(); got != nil {
		t.Errorf("Keys() of a string = %#v; want nil", got)
	}
	if got := s.Interface(); got != "не коллекция" {
		t.Errorf("Interface() = %#v", got)
	}
	// The unexported accessors are the ones the evaluator uses everywhere; each must
	// answer nil rather than assert on the pointer it finds.
	if s.arr() != nil || s.odict() != nil || s.fn() != nil || s.rx() != nil ||
		s.rng() != nil || s.task() != nil || s.arrNames() != nil {
		t.Error("an accessor of the wrong kind returned a non-nil pointer")
	}
	if got := Kind(200).String(); got != "unknown" {
		t.Errorf("Kind(200).String() = %q; want %q", got, "unknown")
	}
}

func TestDictPanicsOnAnOddPairList(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Dict with an odd number of arguments did not panic")
		}
	}()
	Dict(Str("a"), Int(1), Str("b"))
}

// Equality and ordering reach kinds a script cannot compare with `<`, and the host can.
func TestTimeEqualityAndOrdering(t *testing.T) {
	early := timeOf(time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC))
	late := timeOf(time.Date(2026, 8, 13, 11, 0, 0, 0, time.UTC))
	same := timeOf(time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC))

	if !early.Equal(same) {
		t.Error("two times at the same instant are not equal")
	}
	if early.Equal(late) {
		t.Error("two different instants are equal")
	}
	for _, tt := range []struct {
		name string
		a, b Value
		want int
	}{
		{"before", early, late, -1},
		{"after", late, early, 1},
		{"same instant", early, same, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := compare(tt.a, tt.b)
			if !ok || got != tt.want {
				t.Errorf("compare = (%d, %v); want (%d, true)", got, ok, tt.want)
			}
		})
	}
	if _, ok := compare(early, Int(1)); ok {
		t.Error("a time and an int compared; they are not ordered against each other")
	}
}

// The float spellings of §12.7 that no arithmetic in the corpus produces.
func TestFormatFloatEdges(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{math.NaN(), "NaN"},
		{math.Inf(1), "Infinity"},
		{math.Inf(-1), "-Infinity"},
		{2, "2.0"},
		{1e21, "1.0e+21"},
	}
	for _, tt := range tests {
		if got := Float(tt.in).Str(); got != tt.want {
			t.Errorf("Float(%v).Str() = %q; want %q", tt.in, got, tt.want)
		}
	}
	// A float that cannot be JSON must not silently become a syntax error in the output.
	if got := encodeJSON(Float(math.NaN()), ""); got != "null" {
		t.Errorf("NaN in JSON = %q; want null", got)
	}
}

// String escaping has to cover the control characters a message can really carry, in
// both the inspect form (§12.7) and JSON.
func TestStringEscaping(t *testing.T) {
	s := Str("а\\б\"в\nг\rд\tе\x01ж")

	insp := s.Inspect()
	for _, want := range []string{`\\`, `\"`, `\n`, `\r`, `\t`, `\u0001`} {
		if !strings.Contains(insp, want) {
			t.Errorf("Inspect() = %s; want it to contain %s", insp, want)
		}
	}
	js := encodeJSON(s, "")
	for _, want := range []string{`\\`, `\"`, `\n`, `\r`, `\t`, `\u0001`} {
		if !strings.Contains(js, want) {
			t.Errorf("json = %s; want it to contain %s", js, want)
		}
	}
	// A byte that is not UTF-8 — what io.read of a binary file gives (§12.13) — is
	// replaced rather than emitted raw, so the JSON stays parseable.
	bad := encodeJSON(Str("\xff\xfe"), "")
	if !json.Valid([]byte(bad)) {
		t.Errorf("json of an invalid UTF-8 string = %s; want valid JSON", bad)
	}
}
