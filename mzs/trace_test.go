package mzs

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// RFC 0009: the call stack of an error, in the Go API and in the dict `else (e)` binds.

func traceRun(t *testing.T, o Options, src string) *Error {
	t.Helper()
	in := New(o)
	p, err := in.Compile("t.mzs", src)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	_, err = in.Run(context.Background(), p, nil)
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("Run error = %v, want an *Error", err)
	}
	return e
}

func frameNames(fs []Frame) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Fn
	}
	return out
}

// A frame is a call site, innermost first, and the call that *is* the failure — `raise`
// itself — is not repeated: the error's position already names it.
func TestTraceFramesAreCallSites(t *testing.T) {
	e := traceRun(t, Options{}, "fn a() { b() }\nfn b() { c() }\nfn c() { raise(\"boom\") }\na()")
	want := []Frame{
		{Fn: "c", File: "t.mzs", Line: 2, Col: 10},
		{Fn: "b", File: "t.mzs", Line: 1, Col: 10},
		{Fn: "a", File: "t.mzs", Line: 4, Col: 1},
	}
	if !reflect.DeepEqual(e.Stack, want) {
		t.Errorf("Stack = %+v\nwant %+v", e.Stack, want)
	}
	if e.Line != 3 || e.Col != 10 {
		t.Errorf("position = %d:%d, want 3:10", e.Line, e.Col)
	}
}

// The limits the RFC measured with an empty stack, or at the start of the program.
func TestTraceLimitErrorsCarryTheirStack(t *testing.T) {
	t.Run("step budget", func(t *testing.T) {
		o := DefaultOptions()
		o.StepBudget = 10000
		e := traceRun(t, o, "fn a() { b() }\nfn b() { while true { } }\na()")
		if !errors.Is(e, ErrBudget) {
			t.Fatalf("error = %v, want ErrBudget", e)
		}
		if got := frameNames(e.Stack); !reflect.DeepEqual(got, []string{"b", "a"}) {
			t.Errorf("frames = %v, want [b a]", got)
		}
		if e.Line != 2 || e.Col != 10 {
			t.Errorf("position = %d:%d, want the loop at 2:10", e.Line, e.Col)
		}
	})
	t.Run("collection cap", func(t *testing.T) {
		o := DefaultOptions()
		o.MaxCollection = 2
		e := traceRun(t, o, "fn f() {\n  [1, 2, 3]\n}\n\nf()")
		if got := frameNames(e.Stack); !reflect.DeepEqual(got, []string{"f"}) {
			t.Errorf("frames = %v, want [f]", got)
		}
		if e.Line != 2 {
			t.Errorf("position = %d:%d, want the literal on line 2, not the program start", e.Line, e.Col)
		}
	})
	t.Run("call depth keeps every level", func(t *testing.T) {
		o := DefaultOptions()
		o.MaxDepth = 5
		e := traceRun(t, o, "fn f(n) { f(n + 1) }\nf(0)")
		if !errors.Is(e, ErrDepth) || len(e.Stack) != 5 {
			t.Errorf("error = %v with %d frames, want ErrDepth with 5", e, len(e.Stack))
		}
	})
	t.Run("memory budget", func(t *testing.T) {
		o := DefaultOptions()
		o.MaxMemory = 1 << 20
		e := traceRun(t, o, "fn grow() { keep = []; while true { keep.push(\"x\" * 100000) } }\ngrow()")
		if !errors.Is(e, ErrMemory) || len(e.Stack) == 0 || e.Stack[len(e.Stack)-1].Fn != "grow" {
			t.Errorf("error = %v with frames %v, want ErrMemory under grow", e, frameNames(e.Stack))
		}
	})
}

// A function exported by a module runs with the module current: its errors and the
// frames of the calls it makes name the module, while its own frame names the caller.
func TestTraceFramesNameTheirFile(t *testing.T) {
	o := DefaultOptions()
	o.ModuleLoader = func(from, path string) (string, string, error) {
		return "lib.mzs", "export fn boom() {\n  helper()\n}\nfn helper() { raise(\"deep\") }", nil
	}
	e := traceRun(t, o, "include lib from \"./lib.mzs\"\nlib.boom()")
	if e.File != "lib.mzs" || e.Line != 4 {
		t.Errorf("error at %s:%d, want lib.mzs:4", e.File, e.Line)
	}
	want := []Frame{
		{Fn: "helper", File: "lib.mzs", Line: 2, Col: 3},
		{Fn: "boom", File: "t.mzs", Line: 2, Col: 5},
	}
	if !reflect.DeepEqual(e.Stack, want) {
		t.Errorf("Stack = %+v\nwant %+v", e.Stack, want)
	}
}

// The dict `else (e)` binds: every key of §8.11 in order, and a stack a script can read.
func TestTraceErrorDict(t *testing.T) {
	in := New(Options{})
	v, err := in.Eval(context.Background(), `fn inner() { raise("boom", "app") }
fn outer() { inner() }
try outer() else (e) {
  [e.keys.join(","), e["file"], e["line"], e["col"],
   e["stack"].map { "${it["fn"]}@${it["file"]}:${it["line"]}:${it["col"]}" }.join(" ")]
}`, nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	got := v.Elems()
	if got[0].Str() != "message,kind,file,line,col,stack" {
		t.Errorf("keys = %s", got[0].Str())
	}
	if got[2].Int() != 1 || got[3].Int() != 14 {
		t.Errorf("position = %v:%v, want 1:14", got[2], got[3])
	}
	if s := got[4].Str(); !strings.Contains(s, "inner@") || !strings.Contains(s, ":2:14") || !strings.Contains(s, "outer@") {
		t.Errorf("stack = %s", s)
	}

	// At the top level the stack is empty, not absent.
	v, err = in.Eval(context.Background(), `try raise("top") else (e) { e["stack"] }`, nil)
	if err != nil || v.Kind() != KArray || v.Len() != 0 {
		t.Errorf("top-level stack = %v, %v; want []", v, err)
	}

	// raise(e) re-raises with the original stack, whatever the dict was given since.
	v, err = in.Eval(context.Background(), `fn f() { raise("first") }
try (try f() else (e) { e["extra"] = 1; raise(e) }) else (e2) { e2["stack"].map { it["fn"] } }`, nil)
	if err != nil || v.Inspect() != `["f"]` {
		t.Errorf("re-raised stack = %v, %v; want [\"f\"]", v.Inspect(), err)
	}
}

func TestFormatTrace(t *testing.T) {
	f := func(fn string, line int) Frame { return Frame{Fn: fn, File: "x.mzs", Line: line, Col: 3} }
	if got := FormatTrace(nil); len(got) != 0 {
		t.Errorf("FormatTrace(nil) = %v, want nothing", got)
	}
	got := FormatTrace([]Frame{f("fact", 1), f("fact", 1), f("fact", 1), f("main", 9), {Fn: "g", Line: 2, Col: 1}})
	want := []string{
		"in fact, called from x.mzs:1:3 (×3)",
		"in main, called from x.mzs:9:3",
		"in g, called from 2:1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FormatTrace = %q\nwant %q", got, want)
	}
	var long []Frame
	for i := range 12 {
		long = append(long, f("f", i+1))
	}
	got = FormatTrace(long)
	if len(got) != 9 || got[8] != "… 4 more" {
		t.Errorf("FormatTrace of 12 = %q, want 8 lines and \"… 4 more\"", got)
	}
}
