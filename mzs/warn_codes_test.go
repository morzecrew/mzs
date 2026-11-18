package mzs

import (
	"strconv"
	"strings"
	"testing"
)

// RFC 0019 §1: every warning has a stable code, a program can allow one by code, and the
// new rules fire where they should and nowhere else.

// codesOf compiles src and lists its warnings as "line:code".
func codesOf(t *testing.T, o Options, src string) []string {
	t.Helper()
	prog, err := New(o).Compile("w.mzs", src)
	if err != nil {
		t.Fatalf("compile %q: %v", src, err)
	}
	var out []string
	for _, w := range prog.Warnings() {
		out = append(out, strconv.Itoa(w.Line)+":"+w.Code)
	}
	return out
}

func TestWarningCodes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, src string
		want      []string // "line:code", in source order
	}{
		{"unused variable in a function", "fn f() {\n  a = 1\n  2\n}\nf()", []string{"2:unused-variable"}},
		{"a read is a use", "fn f() {\n  a = 1\n  a + 1\n}\nf()", nil},
		{"a closure's read is a use", "fn f(xs) {\n  n = 0\n  xs.each { (x) -> n += x }\n  n\n}\nf([1])", nil},
		{"the last statement is the value", "fn f() {\n  a = 1\n}\nf()", nil},
		{"an underscore says unused on purpose", "fn f() {\n  _a = 1\n  2\n}\nf()", nil},
		{"file scope is not a local", "a = 1\nb = 2\na", nil},
		{"a block's local", "fn f(c) {\n  if c {\n    t = 1\n    2\n  }\n}\nf(true)", []string{"3:unused-variable"}},
		{"assigning a parameter is not reading it", "[1].map { (x) -> x = 1 }", []string{"1:unused-parameter"}},
		{"an unused local in a pattern arm", "fn f(v) {\n  match v {\n    [a] -> {\n      dead = 1\n      a\n    }\n  }\n}\nf([1])", []string{"4:unused-variable"}},
		{"destructured locals", "fn f() {\n  [a, b] = [1, 2]\n  a\n}\nf()", []string{"2:unused-variable"}},
		{"a destructuring that is the value", "fn f() {\n  [a, b] = [1, 2]\n}\nf()", nil},
		{"for variables are not tracked", "fn f(xs) {\n  n = 0\n  for x in xs { n += 1 }\n  n\n}\nf([1])", nil},
		{"||= reads the binding", "fn f() {\n  x = nil\n  x ||= 1\n  0\n}\nf()", nil},
		{"??= reads the binding", "fn f() {\n  x = nil\n  x ??= 1\n  0\n}\nf()", nil},
		{"a discarded group's last assignment", "fn f() {\n  (a = 1)\n  0\n}\nf()", []string{"2:unused-variable"}},
		{"a discarded group inside a discarded group", "fn f() {\n  ((a = 1))\n  0\n}\nf()", []string{"2:unused-variable"}},
		{"a group that is the value", "fn f() {\n  (a = 1)\n}\nf()", nil},
		{"a group whose value is assigned", "fn f() {\n  x = (a = 1)\n  x\n}\nf()", nil},
		{"a discarded if's branches", "fn f(c) {\n  if c { a = 1 } else if !c { b = 2 } else { d = 3 }\n  0\n}\nf(true)", []string{"2:unused-variable", "2:unused-variable", "2:unused-variable"}},
		{"an if that is the value", "fn f(c) {\n  if c { a = 1 } else { b = 2 }\n}\nf(true)", nil},
		{"an if whose value is assigned", "fn f(c) {\n  y = if c { a = 1 } else { b = 2 }\n  y\n}\nf(true)", nil},
		{"a nested destructuring keeps the outer one tracked", "fn f() {\n  xs = [0]\n  [xs[([_skip] = [0]; 0)], dead] = [1, 2]\n  xs\n}\nf()", []string{"3:unused-variable"}},
		{"after return", "fn f() {\n  return 1\n  2\n}\nf()", []string{"3:unreachable"}},
		{"after raise", "fn f() {\n  raise(\"x\")\n  2\n}\ntry f() else 0", []string{"3:unreachable"}},
		{"after break, once", "while true {\n  break\n  1\n  2\n}", []string{"3:unreachable"}},
		{"a local named raise may return", "fn f(raise) {\n  raise(1)\n  2\n}\nf({ (x) -> x })", nil},
		{"if true", "if true { 1 }", []string{"1:constant-condition"}},
		{"if false", "if false { 1 } else { 2 }", []string{"1:constant-condition"}},
		{"while false", "while false { 1 }", []string{"1:constant-condition"}},
		{"while true is a loop", "while true { break }", nil},
		{"duplicate key", "{a: 1, \"a\": 2, b: 3}", []string{"1:duplicate-key"}},
		{"distinct keys", "{a: 1, b: 2}", nil},
		{"duplicate int key", "{1 -> \"a\", 1 -> \"b\"}", []string{"1:duplicate-key"}},
		{"duplicate signed key", "{-1 -> \"a\", -1 -> \"b\", 1 -> \"c\"}", []string{"1:duplicate-key"}},
		{"after a group that raises midway", "fn f() {\n  (raise(\"x\"); 0)\n  2\n}\ntry f() else 0", []string{"2:unreachable", "3:unreachable"}},
		{"an int and a string are two keys", "{1 -> \"a\", \"1\": \"b\"}", nil},
		{"after a grouped raise", "fn f() {\n  (raise(\"x\"))\n  2\n}\ntry f() else 0", []string{"3:unreachable"}},
		{"self compare", "x = 1\nx == x", []string{"2:self-compare"}},
		{"self compare !=", "x = 1\nx != x", []string{"2:self-compare"}},
		{"two names", "x = 1\ny = 2\nx == y", nil},
		{"empty braced else", "try { 1 } else { }", []string{"1:empty-else"}},
		{"a default dict is not empty-else", "r = try 1 else {}\nr", nil},
		{"empty ensure", "try { 1 } else { 0 } ensure { }", []string{"1:empty-ensure"}},
		{"the old warnings have codes too", "[1].each { (x) -> 1 }", []string{"1:unused-parameter"}},
		{"assign in condition", "x = 1\nif x = 2 { 1 }", []string{"2:assign-in-condition"}},
	}
	for _, tt := range cases {
		got := codesOf(t, DefaultOptions(), tt.src)
		if strings.Join(got, " ") != strings.Join(tt.want, " ") {
			t.Errorf("%s: warnings = %v; want %v", tt.name, got, tt.want)
		}
	}
}

func TestWarningSuppression(t *testing.T) {
	t.Parallel()
	src := `fn f() {
  # mzs:allow unused-variable
  a = 1
  b = 2 # mzs:allow unused-variable, self-compare
  c = 3 # mzs:allow unreachable
  # mzs:allow-everything is not the marker
  d = 4
  5
}
f()`
	if got := codesOf(t, DefaultOptions(), src); strings.Join(got, " ") != "5:unused-variable 7:unused-variable" {
		t.Errorf("suppression: warnings = %v; want only c's and d's", got)
	}

	o := DefaultOptions()
	o.AllowWarnings = []string{"unused-variable"}
	if got := codesOf(t, o, src); len(got) != 0 {
		t.Errorf("AllowWarnings: warnings = %v; want none", got)
	}

	// StrictWarnings sees what is left after the allow lists, so an allowed rule does
	// not fail the compile.
	o = DefaultOptions()
	o.StrictWarnings = true
	o.AllowWarnings = []string{"unused-variable"}
	if _, err := New(o).Compile("w.mzs", src); err != nil {
		t.Errorf("StrictWarnings with the rule allowed: %v; want a clean compile", err)
	}
	o.AllowWarnings = nil
	if _, err := New(o).Compile("w.mzs", src); err == nil || !strings.Contains(err.Error(), "'c' is assigned and never used") {
		t.Errorf("StrictWarnings: error = %v; want the first warning left", err)
	}
}

func TestWarningStringCarriesTheCode(t *testing.T) {
	t.Parallel()
	w := Warning{Code: "unreachable", Msg: "unreachable: the statement above always leaves", Line: 3, Col: 2}
	if got := w.String(); got != "3:2: warning: unreachable: the statement above always leaves [unreachable]" {
		t.Errorf("String() = %q", got)
	}
}

// Options holds one slice, and the Interp keeps its own copy: a host that edits its list
// after New, or edits what Options() handed back, changes nothing the Interp does.
func TestAllowWarningsIsCopied(t *testing.T) {
	t.Parallel()
	allow := []string{"unused-variable"}
	o := DefaultOptions()
	o.AllowWarnings = allow
	in := New(o)
	allow[0] = "unreachable"
	in.Options().AllowWarnings[0] = "self-compare"
	prog, err := in.Compile("w.mzs", "fn f() {\n  a = 1\n  2\n}\nf()")
	if err != nil {
		t.Fatal(err)
	}
	if ws := prog.Warnings(); len(ws) != 0 {
		t.Errorf("warnings after the host edited its slices = %v; want unused-variable still allowed", ws)
	}
}

// A host function registered as `raise` or `exit` is called before the built-in and may
// return, so a statement after it is not unreachable.
func TestUnreachableTrustsOnlyTheBuiltins(t *testing.T) {
	t.Parallel()
	in := New(DefaultOptions())
	in.Register("raise", 1, func(c *Ctx, args []Value) (Value, error) { return Nil(), nil })
	prog, err := in.Compile("w.mzs", "fn f() {\n  raise(\"x\")\n  2\n}\nf()")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range prog.Warnings() {
		if w.Code == "unreachable" {
			t.Errorf("a host raise that returns: got %v", w)
		}
	}
}
