package mzs

import (
	"strings"
	"testing"
)

// colModule calls a member of a built-in module the way `json.parse(x)` dispatches:
// through LookupModule, so a gated-off module reports itself as absent. Modules are
// ordinary lowercase values in the root scope; there is no CONST kind and no `::` (§12.8).
func colModule(c *Ctx, module, member string, args ...Value) (Value, error) {
	opts := c.Options()
	mod, ok := LookupModule(module, &opts)
	if !ok {
		return Nil(), nameErrorf("undefined variable '%s'", module)
	}
	fn, ok := mod.odict().Get(Str(member))
	if !ok {
		return Nil(), undefinedMemberError(module, member, mod.Keys())
	}
	if fn.Kind() != KFunc {
		return fn, nil
	}
	return c.Call(fn, args...)
}

func TestJSONModule(t *testing.T) {
	c := colCtx(t, DefaultOptions())

	tests := []struct {
		name    string
		member  string
		args    []Value
		want    string
		wantErr bool
	}{
		{name: "parse an object", member: "parse", args: []Value{Str(`{"a":1}`)}, want: `{"a":1}`},
		{name: "parse keeps key order", member: "parse",
			args: []Value{Str(`{"b":1,"a":2,"c":3}`)}, want: `{"b":1,"a":2,"c":3}`},
		{name: "parse cyrillic and emoji", member: "parse",
			args: []Value{Str(`{"имя":"Иван","флаг":"🇷🇺"}`)}, want: `{"имя":"Иван","флаг":"🇷🇺"}`},
		{name: "parse an integral number as an int", member: "parse", args: []Value{Str(`2`)}, want: "2"},
		{name: "parse a fractional number as a float", member: "parse", args: []Value{Str(`1.5`)}, want: "1.5"},
		{name: "parse null", member: "parse", args: []Value{Str(`null`)}, want: "nil"},
		{name: "parse the webhook corpus shape", member: "parse",
			args: []Value{Str(`[{"generated_text":"ok"}]`)}, want: `[{"generated_text":"ok"}]`},
		{name: "parse rejects malformed input", member: "parse", args: []Value{Str(`{`)}, wantErr: true},
		{name: "parse rejects trailing data", member: "parse", args: []Value{Str(`{} {}`)}, wantErr: true},
		{name: "parse takes exactly one argument", member: "parse",
			args: []Value{Str(`1`), Bool(true)}, wantErr: true},
		{name: "parse rejects a non-string", member: "parse", args: []Value{Int(1)}, wantErr: true},
		{name: "pretty indents by two spaces", member: "pretty",
			args: []Value{Dict(Str("a"), Int(1))}, want: "\"{\\n  \\\"a\\\": 1\\n}\""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := colModule(c, "json", tt.member, tt.args...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("json.%s error = %v; wantErr %v", tt.member, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.Inspect() != tt.want {
				t.Errorf("json.%s = %s; want %s", tt.member, got.Inspect(), tt.want)
			}
		})
	}

	// §12.8: encoding is the free function `json(x)`, so there is no second name for
	// the second half of the pair (D17).
	for _, absent := range []string{"generate", "dump", "encode", "stringify"} {
		t.Run("json has no "+absent, func(t *testing.T) {
			if _, err := colModule(c, "json", absent); err == nil {
				t.Errorf("json.%s exists; encoding is the free function json(x)", absent)
			}
		})
	}
}

// TestJSONRoundTripThroughTheFreeFunction pins that json.parse and json(x) are inverses
// over the shapes the bot corpus actually carries: Cyrillic, emoji, nesting, quotes.
func TestJSONRoundTripThroughTheFreeFunction(t *testing.T) {
	c := colCtx(t, DefaultOptions())

	tests := []struct {
		name string
		json string
	}{
		{name: "scalars", json: `{"i":1,"f":1.5,"s":"x","b":true,"n":null}`},
		{name: "cyrillic keys and values", json: `{"имя":"Иван Петров","город":"Москва"}`},
		{name: "emoji", json: `{"flag":"RU 🇷🇺","tree":"🌲"}`},
		{name: "nested arrays of dicts", json: `[[{"text":"0","data":"var:date:0"}],[{"text":"1","data":"var:date:1"}]]`},
		{name: "apostrophes and quotes", json: `{"name":"О'Брайен","q":"say \"hi\""}`},
		{name: "empty containers", json: `{"a":[],"b":{}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := colModule(c, "json", "parse", Str(tt.json))
			if err != nil {
				t.Fatalf("json.parse(%s) error = %v", tt.json, err)
			}
			out, err := stdBuiltin(t, c, "json", parsed)
			if err != nil {
				t.Fatalf("json() error = %v", err)
			}
			if out.Str() != tt.json {
				t.Errorf("round trip = %s; want %s", out.Str(), tt.json)
			}
		})
	}
}

func TestJSONPretty(t *testing.T) {
	c := colCtx(t, DefaultOptions())
	got, err := colModule(c, "json", "pretty", Dict(Str("a"), Int(1), Str("b"), colInts(2)))
	if err != nil {
		t.Fatalf("pretty error = %v", err)
	}
	want := "{\n  \"a\": 1,\n  \"b\": [\n    2\n  ]\n}"
	if got.Str() != want {
		t.Errorf("pretty =\n%s\nwant\n%s", got.Str(), want)
	}
}

// TestJSONRefusesCycles pins that a self-referential value is an error rather than a
// stack overflow, which Go cannot recover from and which would take the host down.
func TestJSONRefusesCycles(t *testing.T) {
	c := colCtx(t, DefaultOptions())

	tests := []struct {
		name string
		make func() Value
	}{
		{name: "an array containing itself", make: func() Value {
			a := Array()
			a.Append(a)
			return a
		}},
		{name: "a dict containing itself", make: func() Value {
			d := Dict()
			d.Set(Str("self"), d)
			return d
		}},
		{name: "a two-step cycle", make: func() Value {
			a, b := Array(), Array()
			a.Append(b)
			b.Append(a)
			return a
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := tt.make()
			if _, err := stdBuiltin(t, c, "json", v); err == nil {
				t.Errorf("json() of a cyclic value = nil error; want a cycle error")
			}
		})
	}

	t.Run("the same value twice is not a cycle", func(t *testing.T) {
		shared := colInts(1)
		got, err := stdBuiltin(t, c, "json", Array(shared, shared))
		if err != nil {
			t.Fatalf("json() error = %v", err)
		}
		if got.Str() != "[[1],[1]]" {
			t.Errorf("json() = %s; want [[1],[1]]", got.Str())
		}
	})
}

// TestJSONBoundsRanges pins that serialising a range is charged against
// MaxCollection: encodeJSON materialises one without asking.
func TestJSONBoundsRanges(t *testing.T) {
	opts := DefaultOptions()
	opts.MaxCollection = 10
	c := colCtx(t, opts)
	if _, err := colInvoke(c, KRange, "json", rangeOf(1, 1000, false)); err == nil {
		t.Errorf("json of an oversized range = nil error; want a limit error")
	}
	got, err := colInvoke(c, KRange, "json", rangeOf(1, 3, false))
	if err != nil || got.Str() != "[1,2,3]" {
		t.Errorf("json of a small range = %s, %v; want [1,2,3]", got.Str(), err)
	}
}

// TestJSONRejectsDeepNesting pins the pre-scan that keeps the decoder's recursion off
// the Go stack limit.
func TestJSONRejectsDeepNesting(t *testing.T) {
	c := colCtx(t, DefaultOptions())
	deep := strings.Repeat("[", 5000) + strings.Repeat("]", 5000)
	if _, err := colModule(c, "json", "parse", Str(deep)); err == nil {
		t.Errorf("parse of a 5000-deep document = nil error; want a depth error")
	}
	shallow := strings.Repeat("[", 8) + strings.Repeat("]", 8)
	if _, err := colModule(c, "json", "parse", Str(shallow)); err != nil {
		t.Errorf("parse of an 8-deep document = %v; want no error", err)
	}
}

func TestMathModule(t *testing.T) {
	c := colCtx(t, DefaultOptions())

	tests := []struct {
		name    string
		member  string
		args    []Value
		want    string
		wantErr bool
	}{
		{name: "pi", member: "pi", want: "3.141592653589793"},
		{name: "e", member: "e", want: "2.718281828459045"},
		{name: "sqrt", member: "sqrt", args: []Value{Int(9)}, want: "3.0"},
		{name: "cbrt", member: "cbrt", args: []Value{Int(27)}, want: "3.0"},
		{name: "log of e", member: "log", args: []Value{Float(2.718281828459045)}, want: "1.0"},
		{name: "log2", member: "log2", args: []Value{Int(8)}, want: "3.0"},
		{name: "log10", member: "log10", args: []Value{Int(1000)}, want: "3.0"},
		{name: "exp of zero", member: "exp", args: []Value{Int(0)}, want: "1.0"},
		{name: "pow", member: "pow", args: []Value{Int(2), Int(10)}, want: "1024.0"},
		{name: "hypot", member: "hypot", args: []Value{Int(3), Int(4)}, want: "5.0"},
		{name: "atan2", member: "atan2", args: []Value{Int(0), Int(1)}, want: "0.0"},
		{name: "sin of zero", member: "sin", args: []Value{Int(0)}, want: "0.0"},
		{name: "cos of zero", member: "cos", args: []Value{Int(0)}, want: "1.0"},
		{name: "tan of zero", member: "tan", args: []Value{Int(0)}, want: "0.0"},
		{name: "atan of zero", member: "atan", args: []Value{Int(0)}, want: "0.0"},
		{name: "sqrt of a string is a type error", member: "sqrt", args: []Value{Str("9")}, wantErr: true},
		{name: "sqrt with no argument", member: "sqrt", wantErr: true},
		{name: "pow with one argument", member: "pow", args: []Value{Int(2)}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := colModule(c, "math", tt.member, tt.args...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("math.%s error = %v; wantErr %v", tt.member, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.Inspect() != tt.want {
				t.Errorf("math.%s = %s; want %s", tt.member, got.Inspect(), tt.want)
			}
		})
	}
}

// §12.8: module names are lowercase, and the Ruby-shaped constants are not values at
// all. SecureRandom is gone entirely — `uuid` is a §12.1 builtin gated on Options.Rand.
func TestModuleNamesAreLowercase(t *testing.T) {
	tests := []struct {
		name    string
		module  string
		opts    Options
		present bool
	}{
		{"json is always installed", "json", DefaultOptions(), true},
		{"math is always installed", "math", DefaultOptions(), true},
		{"JSON is not a module", "JSON", DefaultOptions(), false},
		{"Math is not a module", "Math", DefaultOptions(), false},
		{"Time is not a module", "Time", Options{EnableTime: true}, false},
		{"Date is not a module", "Date", Options{EnableTime: true}, false},
		{"SecureRandom is gone", "SecureRandom", DefaultOptions(), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := tt.opts.normalize()
			if _, ok := LookupModule(tt.module, &o); ok != tt.present {
				t.Errorf("LookupModule(%q) present = %v; want %v", tt.module, ok, tt.present)
			}
		})
	}
}
