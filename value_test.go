package mzs

import (
	"math"
	"testing"
)

func TestKindString(t *testing.T) {
	tests := []struct {
		name string
		k    Kind
		want string
	}{
		{"nil", KNil, "nil"},
		{"bool", KBool, "bool"},
		{"int", KInt, "int"},
		{"float", KFloat, "float"},
		{"string", KString, "string"},
		{"regex", KRegex, "regex"},
		{"array", KArray, "array"},
		{"dict", KDict, "dict"},
		{"func", KFunc, "function"},
		{"time", KTime, "time"},
		{"range", KRange, "range"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.k.String(); got != tt.want {
				t.Errorf("Kind(%d).String() = %q; want %q", tt.k, got, tt.want)
			}
		})
	}
}

// Truthiness is D6/§7.3: only nil and false are falsy. 0, 0.0, "", [], [:] and NaN are
// truthy, which is why `s.index(/re/)` returning 0 counts as a hit.
func TestTruthy(t *testing.T) {
	tests := []struct {
		name string
		v    Value
		want bool
	}{
		{"nil", Nil(), false},
		{"false", Bool(false), false},
		{"true", Bool(true), true},
		{"zero int", Int(0), true},
		{"zero float", Float(0), true},
		{"NaN", Float(math.NaN()), true},
		{"empty string", Str(""), true},
		{"empty array", Array(), true},
		{"empty dict", Dict(), true},
		{"empty range", rangeOf(5, 1, false), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.Truthy(); got != tt.want {
				t.Errorf("%s.Truthy() = %v; want %v", tt.v.Inspect(), got, tt.want)
			}
		})
	}
}

func TestValueToInt(t *testing.T) {
	tests := []struct {
		name string
		v    Value
		want int64
	}{
		{"int", Int(42), 42},
		{"float truncates toward zero", Float(-1.9), -1},
		{"true", Bool(true), 1},
		{"false", Bool(false), 0},
		{"nil", Nil(), 0},
		{"empty string never errors", Str(""), 0},
		{"leading digits", Str("12abc"), 12},
		{"underscores", Str("1_000"), 1000},
		{"signed", Str("  -37 "), -37},
		{"hex is base 10 only", Str("0x1f"), 0},
		{"not a number", Str("abc"), 0},
		{"NaN", Float(math.NaN()), 0},
		{"array", Array(Int(1)), 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.Int(); got != tt.want {
				t.Errorf("%s.Int() = %d; want %d", tt.v.Inspect(), got, tt.want)
			}
		})
	}
}

func TestValueToFloat(t *testing.T) {
	tests := []struct {
		name string
		v    Value
		want float64
	}{
		{"float", Float(1.5), 1.5},
		{"int", Int(3), 3},
		{"string", Str("1.5kg"), 1.5},
		{"exponent", Str("1.5e-3"), 0.0015},
		{"empty", Str(""), 0},
		{"nil", Nil(), 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.Float(); math.Abs(got-tt.want) > 1e-12 {
				t.Errorf("%s.Float() = %v; want %v", tt.v.Inspect(), got, tt.want)
			}
		})
	}
}

// str per §12.7 and §8.12: a Float always carries a '.' or an exponent but never an
// exponent below 1e21, collections render as JSON, nil is the empty string.
func TestValueToString(t *testing.T) {
	re, err := Regex("при", "i")
	if err != nil {
		t.Fatalf("Regex: %v", err)
	}

	tests := []struct {
		name string
		v    Value
		want string
	}{
		{"nil", Nil(), ""},
		{"true", Bool(true), "true"},
		{"int", Int(-7), "-7"},
		{"float keeps a point", Float(2), "2.0"},
		{"float shortest round trip", Float(1.5), "1.5"},
		{"float big uses exponent", Float(1e21), "1.0e+21"},
		{"float small stays decimal", Float(1e-9), "0.000000001"},
		{"string is itself", Str("привет"), "привет"},
		{"regex", re, "/при/i"},
		{"array as json", Array(Int(1), Str("a")), `[1,"a"]`},
		{"dict as json", Dict(Str("a"), Int(1)), `{"a":1}`},
		{"range", rangeOf(0, 6, false), "0..6"},
		{"exclusive range", rangeOf(0, 6, true), "0..<6"},
		{"func", Fn("f", 0, nil), "#<fn f>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.Str(); got != tt.want {
				t.Errorf("Str() = %q; want %q", got, tt.want)
			}
		})
	}
}

func TestValueInspect(t *testing.T) {
	tests := []struct {
		name string
		v    Value
		want string
	}{
		{"nil spelled out", Nil(), "nil"},
		{"string quoted", Str("a\"b"), `"a\"b"`},
		{"cyrillic stays readable", Str("да"), `"да"`},
		{"newline escaped", Str("a\nb"), `"a\nb"`},
		{"int unchanged", Int(5), "5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.Inspect(); got != tt.want {
				t.Errorf("Inspect() = %q; want %q", got, tt.want)
			}
		})
	}
}

// §7.4: numbers compare across Int/Float, collections structurally, dict order is
// insignificant, and every other cross-kind pair is false rather than an error. There is
// no number/string coercion and no `===`.
func TestValueEqual(t *testing.T) {
	f := Fn("f", 0, nil)
	re1, err := Regex("да", "i")
	if err != nil {
		t.Fatalf("Regex: %v", err)
	}
	re2, err := Regex("да", "i")
	if err != nil {
		t.Fatalf("Regex: %v", err)
	}
	re3, err := Regex("да", "")
	if err != nil {
		t.Fatalf("Regex: %v", err)
	}

	tests := []struct {
		name string
		a, b Value
		want bool
	}{
		{"nil equals nil", Nil(), Nil(), true},
		{"nil differs from false", Nil(), Bool(false), false},
		{"int equals float", Int(1), Float(1), true},
		{"int differs from float", Int(1), Float(1.5), false},
		{"strings are byte exact", Str("Да"), Str("да"), false},
		{"int vs string is false", Int(1), Str("1"), false},
		{"arrays are deep and ordered", Array(Int(1), Int(2)), Array(Int(1), Int(2)), true},
		{"array order matters", Array(Int(1), Int(2)), Array(Int(2), Int(1)), false},
		{"array length matters", Array(Int(1)), Array(Int(1), Int(2)), false},
		{"dicts ignore insertion order", Dict(Str("a"), Int(1), Str("b"), Int(2)),
			Dict(Str("b"), Int(2), Str("a"), Int(1)), true},
		{"dicts compare values", Dict(Str("a"), Int(1)), Dict(Str("a"), Int(2)), false},
		{"functions compare by identity", f, f, true},
		{"different functions differ", f, Fn("f", 0, nil), false},
		{"ranges compare by bounds", rangeOf(0, 6, false), rangeOf(0, 6, false), true},
		{"range exclusivity matters", rangeOf(0, 6, false), rangeOf(0, 6, true), false},
		{"regexes compare by source and flags", re1, re2, true},
		{"regex flags matter", re1, re3, false},
		{"regex against another kind is false", re1, Str("да"), false},
		{"cross kind never errors", Array(), Dict(), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Equal(tt.b); got != tt.want {
				t.Errorf("%s.Equal(%s) = %v; want %v", tt.a.Inspect(), tt.b.Inspect(), got, tt.want)
			}
			if got := tt.b.Equal(tt.a); got != tt.want {
				t.Errorf("reversed: %s.Equal(%s) = %v; want %v",
					tt.b.Inspect(), tt.a.Inspect(), got, tt.want)
			}
		})
	}
}

// §7.5: <=> is -1/0/1 for comparable operands and nil (ok == false) otherwise. Mixed
// number/string is incomparable here; ops.go turns that into an error for < <= > >=.
func TestCompare(t *testing.T) {
	tests := []struct {
		name   string
		a, b   Value
		want   int
		wantOk bool
	}{
		{"ints", Int(1), Int(2), -1, true},
		{"mixed numbers", Int(2), Float(2), 0, true},
		{"strings by code point", Str("a"), Str("b"), -1, true},
		{"bools order false first", Bool(false), Bool(true), -1, true},
		{"arrays element wise", Array(Int(1), Int(2)), Array(Int(1), Int(3)), -1, true},
		{"arrays then by length", Array(Int(1)), Array(Int(1), Int(0)), -1, true},
		{"number vs string is incomparable", Int(2), Str("10"), 0, false},
		{"NaN is incomparable", Float(math.NaN()), Float(1), 0, false},
		{"dict is incomparable", Dict(), Dict(), 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := compare(tt.a, tt.b)
			if ok != tt.wantOk {
				t.Fatalf("compare(%s, %s) ok = %v; want %v",
					tt.a.Inspect(), tt.b.Inspect(), ok, tt.wantOk)
			}
			if ok && got != tt.want {
				t.Errorf("compare(%s, %s) = %d; want %d",
					tt.a.Inspect(), tt.b.Inspect(), got, tt.want)
			}
		})
	}
}

// D10: len and indexing are rune-based, never byte-based. The corpus is Cyrillic and
// emoji throughout, so a byte length is always the wrong answer.
func TestLenAndIndexAreRuneBased(t *testing.T) {
	tests := []struct {
		name    string
		v       Value
		wantLen int
		idx     int
		want    Value
	}{
		{"ascii", Str("hello"), 5, 1, Str("e")},
		{"cyrillic", Str("привет"), 6, 0, Str("п")},
		{"emoji", Str("EN 🇬🇧"), 5, 3, Str("🇬")},
		{"negative index", Str("привет"), 6, -1, Str("т")},
		{"out of range is nil", Str("да"), 2, 9, Nil()},
		{"array", Array(Int(1), Int(2), Int(3)), 3, -1, Int(3)},
		{"array out of range", Array(Int(1)), 1, 4, Nil()},
		{"dict counts entries", Dict(Str("a"), Int(1), Str("b"), Int(2)), 2, 0, Nil()},
		{"range counts elements", rangeOf(0, 6, false), 7, 2, Int(2)},
		{"exclusive range", rangeOf(0, 6, true), 6, 5, Int(5)},
		{"descending range is empty", rangeOf(5, 1, false), 0, 0, Nil()},
		{"scalar has no length", Int(1234), 0, 0, Nil()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.Len(); got != tt.wantLen {
				t.Errorf("Len() = %d; want %d", got, tt.wantLen)
			}
			if got := tt.v.Index(tt.idx); !got.Equal(tt.want) || got.Kind() != tt.want.Kind() {
				t.Errorf("Index(%d) = %s; want %s", tt.idx, got.Inspect(), tt.want.Inspect())
			}
		})
	}
}

// Array and Dict values are references (§7.1): a copy aliases the same data.
func TestReferenceSemantics(t *testing.T) {
	arr := Array(Int(1))
	alias := arr
	alias.Append(Int(2))
	if arr.Len() != 2 {
		t.Errorf("array copy did not alias: len = %d; want 2", arr.Len())
	}

	d := Dict(Str("a"), Int(1))
	dAlias := d
	dAlias.Set(Str("b"), Int(2))
	if d.Len() != 2 {
		t.Errorf("dict copy did not alias: len = %d; want 2", d.Len())
	}

	// Clone is `dup`: shallow, and no longer aliased at the top level.
	c := arr.Clone()
	c.Append(Int(3))
	if arr.Len() != 2 {
		t.Errorf("Clone aliased the original: len = %d; want 2", arr.Len())
	}
}

func TestSetIndexExtendsWithNils(t *testing.T) {
	tests := []struct {
		name    string
		start   Value
		idx     int
		wantLen int
		wantErr bool
	}{
		{"in range", Array(Int(1), Int(2)), 0, 2, false},
		{"extends", Array(Int(1)), 3, 4, false},
		{"negative from end", Array(Int(1), Int(2)), -1, 2, false},
		{"negative past start errors", Array(Int(1)), -5, 1, true},
		{"strings are immutable", Str("ab"), 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.start.setIndex(tt.idx, Str("x"))
			if (err != nil) != tt.wantErr {
				t.Fatalf("setIndex(%d) error = %v; wantErr %v", tt.idx, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got := tt.start.Len(); got != tt.wantLen {
				t.Errorf("len after setIndex = %d; want %d", got, tt.wantLen)
			}
		})
	}
}

func TestJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"object keeps key order", `{"b":1,"a":2}`, `{"b":1,"a":2}`},
		{"nested", `{"a":[1,2,{"c":null}]}`, `{"a":[1,2,{"c":null}]}`},
		{"unicode is not escaped", `{"t":"да 🌲"}`, `{"t":"да 🌲"}`},
		{"integral floats become ints", `[1.0,2]`, `[1,2]`},
		{"true and false", `[true,false]`, `[true,false]`},
		{"empty containers", `{"a":[],"b":{}}`, `{"a":[],"b":{}}`},
		{"escapes", `{"a":"x\ny"}`, `{"a":"x\ny"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := decodeJSON([]byte(tt.src))
			if err != nil {
				t.Fatalf("decodeJSON(%s) error = %v", tt.src, err)
			}
			if got := encodeJSON(v, ""); got != tt.want {
				t.Errorf("re-encoded = %s; want %s", got, tt.want)
			}
		})
	}
}

func TestJSONDecodeErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"truncated object", `{"a":`},
		{"trailing data", `{} {}`},
		{"bare word", `nope`},
		{"empty", ``},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeJSON([]byte(tt.src)); err == nil {
				t.Errorf("decodeJSON(%q) = nil error; want an error", tt.src)
			}
		})
	}
}

func TestFromAndInterface(t *testing.T) {
	type point struct {
		X int    `json:"x"`
		Y string `json:"y"`
	}

	tests := []struct {
		name     string
		in       any
		wantKind Kind
		wantStr  string
	}{
		{"nil", nil, KNil, ""},
		{"bool", true, KBool, "true"},
		{"int", 7, KInt, "7"},
		{"int64", int64(7), KInt, "7"},
		{"float", 1.5, KFloat, "1.5"},
		{"string", "да", KString, "да"},
		{"bytes", []byte("ab"), KString, "ab"},
		{"slice", []int{1, 2}, KArray, "[1,2]"},
		{"Go map is key sorted for determinism", map[string]any{"b": 2, "a": 1}, KDict, `{"a":1,"b":2}`},
		{"Go string map", map[string]string{"a": "x"}, KDict, `{"a":"x"}`},
		{"struct through json", point{X: 1, Y: "q"}, KDict, `{"x":1,"y":"q"}`},
		{"value passes through", Str("s"), KString, "s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := From(tt.in)
			if err != nil {
				t.Fatalf("From(%#v) error = %v", tt.in, err)
			}
			if v.Kind() != tt.wantKind {
				t.Errorf("From(%#v).Kind() = %s; want %s", tt.in, v.Kind(), tt.wantKind)
			}
			if got := v.Str(); got != tt.wantStr {
				t.Errorf("From(%#v).Str() = %q; want %q", tt.in, got, tt.wantStr)
			}
			// Interface() must not panic for anything From produces.
			_ = v.Interface()
		})
	}
}

func TestFromRejectsUnsupported(t *testing.T) {
	if _, err := From(map[int]string{1: "a"}); err == nil {
		t.Errorf("From(map[int]string) = nil error; want an error")
	}
	if _, err := From(make(chan int)); err == nil {
		t.Errorf("From(chan) = nil error; want an error")
	}
}

func TestRegexValueAndKey(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		flags   string
		wantStr string
		wantErr bool
	}{
		{"plain", "abc", "", "/abc/", false},
		{"case insensitive", "оператор", "i", "/оператор/i", false},
		{"the s spelling folds to m", "a.b", "s", "/a.b/m", false},
		{"unknown flag", "a", "q", "", true},
		{"unterminated group", "(", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := Regex(tt.pattern, tt.flags)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Regex(%q, %q) error = %v; wantErr %v", tt.pattern, tt.flags, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got := v.Str(); got != tt.wantStr {
				t.Errorf("Regex(%q, %q).Str() = %q; want %q", tt.pattern, tt.flags, got, tt.wantStr)
			}
		})
	}
}

func TestNoOperationPanicsOffKind(t *testing.T) {
	// Every accessor is total: an off-kind receiver yields a zero value, never a
	// panic. This is what keeps A7 (a script can never panic the host) achievable.
	vals := []Value{Nil(), Bool(true), Int(1), Float(1), Str("s"), Array(Int(1)),
		Dict(Str("a"), Int(1)), Fn("f", 0, nil), rangeOf(0, 2, false)}

	for _, v := range vals {
		t.Run(v.Kind().String(), func(t *testing.T) {
			_ = v.Int()
			_ = v.Float()
			_ = v.Str()
			_ = v.Inspect()
			_ = v.Len()
			_ = v.Bool()
			_ = v.Truthy()
			_ = v.Index(0)
			_ = v.Get(Str("k"))
			_ = v.Keys()
			_ = v.Elems()
			_ = v.Clone()
			_ = v.Interface()
			v.Set(Str("k"), Int(1))
			v.Append(Int(1))
			if _, err := v.MarshalJSON(); err != nil {
				t.Errorf("MarshalJSON() error = %v", err)
			}
		})
	}
}
