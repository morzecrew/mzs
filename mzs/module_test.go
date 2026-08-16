package mzs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// §12.8: nothing is ambient. These tests pin both halves of that — what `include` makes
// reachable, and that the same name is unreachable without it — plus the export rules a
// script module lives by.

// modSource is a loader over an in-memory file set, which is all a host has to provide.
// It records what was asked for, so a diamond can be shown to load once.
type modSource struct {
	files map[string]string
	asked []string
}

func (m *modSource) loader() ModuleLoader {
	return func(from, path string) (string, string, error) {
		m.asked = append(m.asked, path)
		src, ok := m.files[path]
		if !ok {
			return "", "", fmt.Errorf("no such module")
		}
		return path, src, nil
	}
}

func modInterp(files map[string]string) (*Interp, *modSource) {
	src := &modSource{files: files}
	o := DefaultOptions()
	o.EnableTime = true
	o.Now = colClock
	o.ModuleLoader = src.loader()
	return New(o), src
}

func evalMod(t *testing.T, in *Interp, code string) (Value, error) {
	t.Helper()
	return in.Eval(context.Background(), code, nil)
}

// TestIncludeBuiltinModule pins that a built-in module is a declaration away, and only a
// declaration away: the same expression without the include is a compile error that says
// what to add.
func TestIncludeBuiltinModule(t *testing.T) {
	t.Parallel()
	in, _ := modInterp(nil)

	v, err := evalMod(t, in, "include json\njson.parse('{\"a\":1}')[\"a\"]")
	if err != nil {
		t.Fatalf("with the include: %v", err)
	}
	if v.Str() != "1" {
		t.Fatalf("got %q; want 1", v.Str())
	}

	_, err = evalMod(t, in, `json.parse("{}")`)
	if err == nil || !strings.Contains(err.Error(), "include json") {
		t.Fatalf("without the include: %v; want a diagnostic naming `include json`", err)
	}
}

// TestIncludeDiagnostics covers the three ways an include can fail before anything runs.
func TestIncludeDiagnostics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts func() Options
		src  string
		want string
	}{
		{"a module that is not one", func() Options { return DefaultOptions() },
			"include nosuch", `unknown module 'nosuch'`},
		{"a clock module names its option", func() Options { return DefaultOptions() },
			"include time", "EnableTime"},
		{"a filesystem module names its option", func() Options { return DefaultOptions() },
			"include io", "Options.FS"},
		{"a path without a loader", func() Options { return DefaultOptions() },
			`include lib from "./lib.mzs"`, "did not enable module loading"},
		{"export of a name that is not defined", func() Options { return DefaultOptions() },
			"export nope", "cannot export 'nope'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(tt.opts()).Compile("t", tt.src)
			if err == nil {
				t.Fatalf("%s: no error", tt.src)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v; want it to mention %q", err, tt.want)
			}
		})
	}
}

// TestIncludeIsNotAmbientPerProgram pins that an include belongs to the program that
// wrote it: one script including json does not make json reachable in the next.
func TestIncludeIsNotAmbientPerProgram(t *testing.T) {
	t.Parallel()
	in, _ := modInterp(nil)

	if _, err := evalMod(t, in, "include json\njson.pretty([1])"); err != nil {
		t.Fatalf("first program: %v", err)
	}
	if _, err := evalMod(t, in, `json.pretty([1])`); err == nil {
		t.Fatal("the second program reached json without including it")
	}
}

// TestScriptModuleExports is the whole export contract in one file: `export fn`,
// `export x = …`, a later `export name`, and everything else staying private.
func TestScriptModuleExports(t *testing.T) {
	t.Parallel()

	in, _ := modInterp(map[string]string{
		"./lib.mzs": `
			export fn greet(n) { "привет, " + n }
			export limit = 3
			secret = "приватно"
			helper = { (x) -> x * 2 }
			export helper
			limit = 4
		`,
	})

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"an exported fn", `lib.greet("Иван")`, "привет, Иван"},
		{"an exported closure", `lib.helper(21)`, "42"},
		{"a later assignment wins", `lib.limit.str`, "4"},
		{"the exports are a value", `lib.keys.join(",")`, "greet,limit,helper"},
		{"a private name is absent", `defined(lib.secret).str`, "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := evalMod(t, in, "include lib from \"./lib.mzs\"\n"+tt.src)
			if err != nil {
				t.Fatalf("%s: %v", tt.src, err)
			}
			if got := v.Str(); got != tt.want {
				t.Fatalf("%s = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestScriptModuleRunsOnce pins the per-Run cache: a diamond runs the shared module
// once, and a second Run of the same program runs it again — caching is per Run, so two
// concurrent dialogues still share nothing (§10).
func TestScriptModuleRunsOnce(t *testing.T) {
	t.Parallel()

	in, _ := modInterp(map[string]string{
		"./util.mzs": "$loads = ($loads ?? 0) + 1\nexport fn id(x) { x }",
		"./a.mzs":    "include u from \"./util.mzs\"\nexport fn a() { u.id(1) }",
		"./b.mzs":    "include u from \"./util.mzs\"\nexport fn b() { u.id(2) }",
	})

	src := `
		include a from "./a.mzs"
		include b from "./b.mzs"
		a.a() + b.b() + $loads
	`
	prog, err := in.Compile("main", src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for run := 0; run < 2; run++ {
		res, err := in.RunResult(context.Background(), prog, nil)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		// 1 + 2 + one load: a second load in the same Run would make it 5.
		if got := res.Value.Str(); got != "4" {
			t.Fatalf("run %d = %s; want 4 (the diamond loaded util twice)", run, got)
		}
	}
}

// TestScriptModuleSharesGlobals pins that a module is part of the Run that included it:
// it reads the host's $vars and writes back to the same table (§10).
func TestScriptModuleSharesGlobals(t *testing.T) {
	t.Parallel()

	in, _ := modInterp(map[string]string{
		"./cfg.mzs": `$seen = $__sent.upper` + "\nexport fn seen() { $seen }",
	})
	prog, err := in.Compile("main", "include cfg from \"./cfg.mzs\"\ncfg.seen()")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	res, err := in.RunResult(context.Background(), prog, map[string]Value{"$__sent": Str("да")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Value.Str() != "ДА" {
		t.Fatalf("value = %q; want ДА", res.Value.Str())
	}
	if got := res.Globals["$seen"].Str(); got != "ДА" {
		t.Fatalf("$seen = %q; want the module's write to reach the host", got)
	}
}

// TestIncludeCycle pins that a cycle is a named error and not a hang or a stack
// overflow.
func TestIncludeCycle(t *testing.T) {
	t.Parallel()

	in, _ := modInterp(map[string]string{
		"./a.mzs": `include b from "./b.mzs"` + "\nexport fn a() { 1 }",
		"./b.mzs": `include a from "./a.mzs"` + "\nexport fn b() { 2 }",
	})
	_, err := evalMod(t, in, `include a from "./a.mzs"`)
	if err == nil || !strings.Contains(err.Error(), "include cycle") {
		t.Fatalf("error = %v; want an include cycle", err)
	}
}

// TestModuleErrorsCarryTheirFile pins that a failure inside a module points at the
// module, not at the line that included it: that is the whole value of the file name in
// a diagnostic (§17).
func TestModuleErrorsCarryTheirFile(t *testing.T) {
	t.Parallel()

	in, _ := modInterp(map[string]string{
		"./bad.mzs": "export fn boom() { 1 }\nx = 1 / 0",
	})
	_, err := evalMod(t, in, `include bad from "./bad.mzs"`)
	if err == nil {
		t.Fatal("no error")
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error = %v; want an *Error", err)
	}
	if e.File != "./bad.mzs" || e.Line != 2 {
		t.Fatalf("error at %s:%d; want ./bad.mzs:2", e.File, e.Line)
	}
}

// TestCallingAModuleIsAnError pins that a module is never callable: `include json` makes
// `json` the module and nothing else, so the §12.1 function is reached the one way that
// cannot be read as calling the module — `x.json` (§12.8).
func TestCallingAModuleIsAnError(t *testing.T) {
	t.Parallel()
	in, _ := modInterp(nil)

	_, err := evalMod(t, in, "include json\njson([1, 2])")
	if err == nil {
		t.Fatal("calling the module compiled")
	}
	for _, want := range []string{"is a module", "json.parse", "'x.json'"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v; want it to mention %q", err, want)
		}
	}

	v, err := evalMod(t, in, "include json\n[1, 2].json + json.pretty([1])[0]")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.Str() != "[1,2][" {
		t.Fatalf("got %q; want the method and the module in one expression", v.Str())
	}
}

// TestIncludeDoesNotBreakTheMethod is the other half: the module binding must not stand
// in the way of the UFCS fallback either. `nil.json` and `false.json` have no row of
// their own — they resolve to the function of §12.1 — and an include of the same name
// used to turn them into `not a function: dict`.
func TestIncludeDoesNotBreakTheMethod(t *testing.T) {
	t.Parallel()
	in, _ := modInterp(nil)

	v, err := evalMod(t, in, "include json\nnil.json + false.json")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.Str() != "nullfalse" {
		t.Fatalf("got %q; want the two §12.1 encodings", v.Str())
	}
}

// TestCallingAScriptModuleIsAnError pins that the rule is about modules, not about the
// one spelling that is also a builtin: a script module has no function half at all, and
// the diagnostic still arrives at compile time rather than as `not a function: dict`.
func TestCallingAScriptModuleIsAnError(t *testing.T) {
	t.Parallel()
	in, _ := modInterp(map[string]string{"./lib.mzs": "export fn greet(n) { n }"})

	_, err := evalMod(t, in, "include lib from \"./lib.mzs\"\nlib(1)")
	if err == nil {
		t.Fatal("calling the module compiled")
	}
	if !strings.Contains(err.Error(), "'lib' is a module, not a function") {
		t.Fatalf("error = %v; want the module diagnostic", err)
	}
}

// TestIncludeIsScoped pins that an include binds where it stands, like every other
// declaration (§8.2).
func TestIncludeIsScoped(t *testing.T) {
	t.Parallel()
	in, _ := modInterp(nil)

	if _, err := evalMod(t, in, "if true { include json\njson.pretty([1]) }\njson.pretty([2])"); err == nil {
		t.Fatal("an include inside a block leaked out of it")
	}
}

// TestExportOutsideAModuleIsHarmless pins that one file is both a program and a module:
// running it directly ignores the exports rather than refusing them.
func TestExportOutsideAModuleIsHarmless(t *testing.T) {
	t.Parallel()
	in, _ := modInterp(nil)

	v, err := evalMod(t, in, "export fn f() { 7 }\nf()")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.Str() != "7" {
		t.Fatalf("got %q; want 7", v.Str())
	}
}
