package mzs

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// RFC 0003, part 2: with Options.CompileIncludes, the fields of the records an included
// module exports are known where it is included.

const ledgerLib = `
export record Money(amount, currency = "RUB")
record Refund(of, reason)
export Refund
record Private(secret)
export fn eur(n) { Money(n, "EUR") }
export fn private(s) { Private(s) }
`

func importInterp(compileIncludes bool, files map[string]string) (*Interp, *modSource) {
	src := &modSource{files: files}
	o := DefaultOptions()
	o.ModuleLoader = src.loader()
	o.CompileIncludes = compileIncludes
	return New(o), src
}

func importEval(t *testing.T, compileIncludes bool, files map[string]string, code string) (Value, error) {
	t.Helper()
	in, _ := importInterp(compileIncludes, files)
	return in.Eval(context.Background(), code, nil)
}

func TestImportedRecordFields(t *testing.T) {
	files := map[string]string{"ledger": ledgerLib}
	cases := [][2]string{
		// A field read, a write, every compound operator, a destructuring target.
		{`include ledger from "ledger"; m = ledger.Money(1500); m.amount`, `1500`},
		{`include ledger from "ledger"; m = ledger.Money(1); m.amount = 7; m.amount += 1; [m.amount, type(m)]`, `[8,"Money"]`},
		{`include ledger from "ledger"; m = ledger.Money(1); m.amount, m.currency = [2, "EUR"]; m`, `{"amount":2,"currency":"EUR"}`},
		// A bare `export Name` of a top-level record exports its fields too.
		{`include ledger from "ledger"; r = ledger.Refund(ledger.eur(5), "late"); [r.reason, r.of.amount]`, `["late",5]`},
		// A record pattern on the module's shape, nested, and beside a plain dict.
		{`include ledger from "ledger"
fn f(e) {
  match e {
    ledger.Money {amount, currency: "EUR"} -> "eur ${amount}"
    ledger.Money {amount} -> "rub ${amount}"
    ledger.Refund {of: ledger.Money {amount}, reason} -> "refund ${amount} ${reason}"
    {amount} -> "dict ${amount}"
    else -> "other"
  }
}
[f(ledger.eur(3)), f(ledger.Money(4)), f(ledger.Refund(ledger.Money(5), "x")), f({amount: 6}), f(7)]`,
			`["eur 3","rub 4","refund 5 x","dict 6","other"]`},
		// The shape is asked of the module's constructor, so a dict of the same entries
		// does not fire the arm.
		{`include ledger from "ledger"; d = {amount: 1, currency: "RUB"}; match d { ledger.Money {amount} -> "money"; else -> "dict" }`, `"dict"`},
		// A qualifier that no longer holds the module has no such member: the arm does
		// not fire.
		{`include ledger from "ledger"; m = ledger.Money(1); ledger = {}; match m { ledger.Money {amount} -> "money"; else -> "no" }`, `"no"`},
		// A field name the module declares does not take a call away from a function this
		// unit can see: it stays the call it compiled to (and warns, below).
		{`include ledger from "ledger"; fn amount(m) { "fn" }; ledger.Money(1).amount`, `"fn"`},
	}
	for _, c := range cases {
		v, err := importEval(t, true, files, c[0])
		if err != nil {
			t.Errorf("%s: %v", c[0], err)
			continue
		}
		if got := v.Inspect(); got != c[1] {
			t.Errorf("%s = %s, want %s", c[0], got, c[1])
		}
	}
}

func TestImportedRecordFieldErrors(t *testing.T) {
	files := map[string]string{"ledger": ledgerLib, "broken": "fn (", "other": "export record Money(total)"}
	cases := []struct{ code, kind, msg string }{
		{`include ledger from "ledger"; m = ledger.Money(1); m.amout`, ErrKindName, "undefined method 'amout'; did you mean 'amount'?"},
		{`include ledger from "ledger"; m = ledger.Money(1); m.amout = 2`, ErrKindSyntax, "cannot assign to '.amout': not a record field; did you mean '.amount'?"},
		{`include ledger from "ledger"; match 1 { ledger.Money {amout} -> 1; else -> 0 }`, ErrKindSyntax, "record ledger.Money has no field 'amout'; it has amount, currency"},
		// A record the module does not export teaches the includer nothing.
		{`include ledger from "ledger"; fn f(p) { p.secret }`, ErrKindName, "undefined method 'secret'"},
		{`include ledger from "ledger"; match 1 { ledger.Private {secret, x} -> 1; else -> 0 }`, ErrKindSyntax, "'ledger.Private {…}' is not a record pattern: it exports no record of that name"},
		{`match 1 { nope.Money {a, b} -> 1; else -> 0 }`, ErrKindSyntax, "'nope.Money {…}' is not a record pattern: 'nope' is not a module this file includes"},
		// An include inside a block may never run, so it is not read.
		{`if true { include ledger from "ledger" }; fn f(m) { m.amount }`, ErrKindName, "undefined method 'amount'"},
		// A read that fails is no error of its own; it explains the field nothing resolved.
		{`include gone from "gone"; fn f(m) { m.amount }`, ErrKindName, "undefined method 'amount'; module 'gone' could not be read at compile time: no such module"},
		{`include broken from "broken"; fn f(m) { m.amount }`, ErrKindName, "module 'broken' could not be read at compile time: it does not parse"},
		{`include gone from "gone"; match 1 { gone.Money {a, b} -> 1; else -> 0 }`, ErrKindSyntax, "'gone.Money {…}' is not a record pattern: the module could not be read at compile time"},
		// One name included twice at the top level names whichever ran last when a body
		// runs, so neither module's records are learnt.
		{`include m from "ledger"; fn f(e) { match e { m.Money {amount, currency} -> 1; else -> 0 } }; include m from "other"`, ErrKindSyntax,
			"'m.Money {…}' is not a record pattern: the file includes 'm' more than once"},
		{`include m from "ledger"; include m from "other"; fn f(e) { e.amount }`, ErrKindName, "undefined method 'amount'"},
		// At run time the receiver must still be a record that declares the field.
		{`include ledger from "ledger"; record Mine(amount); m = ledger.private("s"); m.amount = 1`, ErrKindType, "record Private has no field 'amount'; it has secret"},
	}
	for _, c := range cases {
		_, err := importEval(t, true, files, c.code)
		var e *Error
		if !errors.As(err, &e) {
			t.Errorf("%s: error = %v, want an *Error", c.code, err)
			continue
		}
		if e.Kind != c.kind || !strings.Contains(e.Msg, c.msg) {
			t.Errorf("%s: error = %s: %s, want %s: …%s…", c.code, e.Kind, e.Msg, c.kind, c.msg)
		}
	}
}

// A failed read at compile time leaves the include exactly as it was: it fails when it
// runs, and `try` catches it there.
func TestImportedRecordFailedReadStillFailsAtRunTime(t *testing.T) {
	v, err := importEval(t, true, nil, `try { include gone from "gone"; 1 } else (e) { e["message"] }`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v.Str(), `cannot include "gone"`) {
		t.Errorf("value = %s, want the include's own run-time error", v.Inspect())
	}
	_, err = importEval(t, true, nil, `include gone from "gone"`)
	if err == nil || !strings.Contains(err.Error(), `cannot include "gone"`) {
		t.Errorf("error = %v, want the include's run-time error", err)
	}
}

// Without CompileIncludes nothing about includes changes: the loader is not called by
// Compile, a module's field is undefined as a `.name`, and `mod.Name {x}` keeps the call
// reading it always had.
func TestImportsNeedCompileIncludes(t *testing.T) {
	files := map[string]string{"ledger": ledgerLib}
	in, src := importInterp(false, files)
	if _, err := in.Compile("main", `include ledger from "ledger"; ledger.Money(1)`); err != nil {
		t.Fatal(err)
	}
	if len(src.asked) != 0 {
		t.Errorf("Compile asked the loader for %v, want nothing", src.asked)
	}
	_, err := importEval(t, false, files, `include ledger from "ledger"; fn f(m) { m.amount }`)
	if err == nil || !strings.Contains(err.Error(), "undefined method 'amount'") {
		t.Errorf("error = %v, want undefined method 'amount'", err)
	}
	_, err = importEval(t, false, files, `include ledger from "ledger"; match 1 { ledger.Money {amount, currency} -> 1; else -> 0 }`)
	if err == nil || !strings.Contains(err.Error(), "the host compiles without reading includes (Options.CompileIncludes)") {
		t.Errorf("error = %v, want the CompileIncludes diagnostic", err)
	}
	// `ledger.eur {it}` is the method call with a trailing closure, both ways round.
	for _, on := range []bool{false, true} {
		v, err := importEval(t, on, files, `include ledger from "ledger"; match 1 { ledger.eur {it} -> "money"; else -> "else" }`)
		if err != nil || v.Inspect() != `"else"` {
			t.Errorf("CompileIncludes=%v: %v, %v; want \"else\" from the call reading", on, v.Inspect(), err)
		}
	}
}

// CompileIncludes asks the host's loader and nothing else: with none installed there is
// nothing to read, and the include is the compile error it always was. A loader that
// gives no resolved name is read under the path written, as at run time.
func TestCompileIncludesUsesTheLoader(t *testing.T) {
	o := DefaultOptions()
	o.CompileIncludes = true
	_, err := New(o).Compile("main", `include ledger from "ledger"`)
	if err == nil || !strings.Contains(err.Error(), "the host did not enable module loading") {
		t.Errorf("error = %v, want the missing-loader diagnostic", err)
	}
	o.ModuleLoader = func(from, path string) (string, string, error) { return "", ledgerLib, nil }
	v, err := New(o).Eval(context.Background(), `include ledger from "ledger"; m = ledger.Money(3); m.amount`, nil)
	if err != nil || v.Inspect() != "3" {
		t.Errorf("= %s, %v; want 3", v.Inspect(), err)
	}
}

// With CompileIncludes, Compile reads each top-level include once and a Run reads it
// again, as it always did: nothing compiled is kept from the module.
func TestImportsAreReadAgainEachRun(t *testing.T) {
	in, src := importInterp(true, map[string]string{"ledger": ledgerLib})
	prog, err := in.Compile("main", `include ledger from "ledger"; ledger.Money(1).amount`)
	if err != nil {
		t.Fatal(err)
	}
	if len(src.asked) != 1 {
		t.Fatalf("Compile asked %v, want one read", src.asked)
	}
	for i := 0; i < 2; i++ {
		if _, err := in.Run(context.Background(), prog, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(src.asked) != 3 {
		t.Errorf("asked = %v, want one read at compile time and one per Run", src.asked)
	}
	// A pattern on a record the module no longer exports by the time it runs asks for a
	// member that is not there, and the arm stops firing — on a subject the module still
	// builds with that very record.
	src.files["ledger"] = `export record Money(amount); export fn one() { Money(1) }`
	pat, err := in.Compile("pat", `include ledger from "ledger"; match ledger.one() { ledger.Money {amount} -> "money"; else -> "no" }`)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := in.Run(context.Background(), pat, nil); err != nil || v.Inspect() != `"money"` {
		t.Errorf("= %v, %v; want \"money\" while the module exports Money", v.Inspect(), err)
	}
	src.files["ledger"] = `record Money(amount); export fn one() { Money(1) }`
	if v, err := in.Run(context.Background(), pat, nil); err != nil || v.Inspect() != `"no"` {
		t.Errorf("= %v, %v; want \"no\": the module keeps Money private now", v.Inspect(), err)
	}
	src.files["ledger"] = ledgerLib
	// A module whose field is gone by the time it runs reaches the field through run-time
	// dispatch, which answers as for any record without it.
	src.files["ledger"] = `export record Money(total)`
	_, err = in.Run(context.Background(), prog, nil)
	if err == nil || !strings.Contains(err.Error(), "undefined method 'amount'") {
		t.Errorf("error = %v, want the run-time undefined method", err)
	}
}

// The overlap of a module's field with a function this unit can see is a warning, and
// StrictWarnings promotes it like any other.
func TestImportedFieldShadowedByAFunctionWarns(t *testing.T) {
	code := `include ledger from "ledger"; fn amount(m) { 0 }; ledger.Money(1).amount`
	in, _ := importInterp(true, map[string]string{"ledger": ledgerLib})
	prog, err := in.Compile("main", code)
	if err != nil {
		t.Fatal(err)
	}
	w := prog.Warnings()
	if len(w) != 1 || !strings.Contains(w[0].Msg, "'amount' is also a field of ledger.Money; this calls the function") {
		t.Fatalf("warnings = %v, want the overlap", w)
	}
	strict, _ := importInterp(true, map[string]string{"ledger": ledgerLib})
	strict.opts.StrictWarnings = true
	if _, err := strict.Compile("main", code); err == nil {
		t.Error("StrictWarnings: Compile succeeded, want the warning as an error")
	}
	// Without CompileIncludes there is nothing to overlap with.
	plain, _ := importInterp(false, map[string]string{"ledger": ledgerLib})
	prog, err = plain.Compile("main", code)
	if err != nil || len(prog.Warnings()) != 0 {
		t.Errorf("CompileIncludes off: %v, %v; want no warning", prog.Warnings(), err)
	}
}

// A field named like a stdlib row is dispatched at run time, where a record's own field
// wins (§7.8), whatever the compile step knows: the function of that name the includer
// declares does not take it, and nothing warns that it would.
func TestImportedFieldNamedLikeAStdlibRow(t *testing.T) {
	files := map[string]string{"pages": `export record Page(len, text)`}
	code := `include pages from "pages"; fn len(x) { "fn" }; p = pages.Page(3, "abc"); [p.len, len(p)]`
	for _, on := range []bool{false, true} {
		in, _ := importInterp(on, files)
		prog, err := in.Compile("main", code)
		if err != nil {
			t.Fatalf("CompileIncludes=%v: %v", on, err)
		}
		if w := prog.Warnings(); len(w) != 0 {
			t.Errorf("CompileIncludes=%v: warnings %v, want none", on, w)
		}
		v, err := in.Run(context.Background(), prog, nil)
		if err != nil || v.Inspect() != `[3,"fn"]` {
			t.Errorf("CompileIncludes=%v: %v, %v; want [3,\"fn\"]", on, v.Inspect(), err)
		}
	}
}
