package mzs

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// RFC 0015: the path module is lexical, `/`-only and ungated, so every answer is pinned
// here as a table — what it says for the names a script actually has, and for the edges
// Go's own path package answers in ways a script would not guess.

func evalPath(t *testing.T, src string) (string, error) {
	t.Helper()
	v, err := New(Options{}).Eval(context.Background(), "include path\n"+src, nil)
	if err != nil {
		return "", err
	}
	return v.Inspect(), nil
}

func TestPathMembers(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ src, want string }{
		{`path.join("in", "report.json")`, `"in/report.json"`},
		{`path.join("a/", "/b", "", "c/..")`, `"a/b"`},
		{`path.join("/", "etc", "hosts")`, `"/etc/hosts"`},
		{`path.join(*["a", "b", "c"])`, `"a/b/c"`},
		{`path.join()`, `""`},
		{`path.join("", "")`, `""`},
		{`path.join("..", "x")`, `"../x"`},

		{`path.dir("a/b/c.txt")`, `"a/b"`},
		{`path.dir("a/b/")`, `"a"`},
		{`path.dir("x")`, `"."`},
		{`path.dir("/x")`, `"/"`},
		{`path.dir("")`, `"."`},

		{`path.base("a/b.txt")`, `"b.txt"`},
		{`path.base("a/b/")`, `"b"`},
		{`path.base("")`, `"."`},
		{`path.base("/")`, `"/"`},

		{`path.ext("a/b.tar.gz")`, `".gz"`},
		{`path.ext(".gitignore")`, `""`},
		{`path.ext("a/.profile.bak")`, `".bak"`},
		{`path.ext("noext")`, `""`},
		{`path.ext("a.")`, `"."`},
		{`path.ext("..")`, `""`},
		{`path.ext("a.d/b")`, `""`},

		{`path.stem("a/b.tar.gz")`, `"b.tar"`},
		{`path.stem(".gitignore")`, `".gitignore"`},
		{`path.stem("x")`, `"x"`},

		{`path.clean("a//b/./c/../d/")`, `"a/b/d"`},
		{`path.clean("/../x")`, `"/x"`},
		{`path.clean("../../x")`, `"../../x"`},
		{`path.clean("")`, `"."`},

		{`path.split("a/b/c")`, `["a","b","c"]`},
		{`path.split("/a/b")`, `["/","a","b"]`},
		{`path.split("a//b/")`, `["a","b"]`},
		{`path.split(".")`, `[]`},
		{`path.split("")`, `[]`},
		{`path.split("/")`, `["/"]`},
		{`path.split("../x")`, `["..","x"]`},

		{`path.rel("a/b/c", "a")`, `"b/c"`},
		{`path.rel("a", "a/b/c")`, `"../.."`},
		{`path.rel("a/x", "a/b")`, `"../x"`},
		{`path.rel("a", "a")`, `"."`},
		{`path.rel("/a/b", "/")`, `"a/b"`},
		{`path.rel("../x", ".")`, `"../x"`},
		{`path.rel("/a", "b")`, `nil`},
		{`path.rel("a", "/b")`, `nil`},
		{`path.rel("x", "..")`, `nil`},
		{`path.rel("x", "../y")`, `nil`},

		{`path.is_abs("/x")`, `true`},
		{`path.is_abs("x/")`, `false`},
		{`path.is_abs("")`, `false`},

		{`path.match("*.mzs", "a.mzs")`, `true`},
		{`path.match("*.mzs", "src/a.mzs")`, `false`},
		{`path.match("**/*.mzs", "a.mzs")`, `true`},
		{`path.match("**/*.mzs", "src/lib/a.mzs")`, `true`},
		{`path.match("src/**/test_*.mzs", "src/test_a.mzs")`, `true`},
		{`path.match("src/**/test_*.mzs", "src/x/y/test_a.mzs")`, `true`},
		{`path.match("src/**/test_*.mzs", "lib/test_a.mzs")`, `false`},
		{`path.match("src/**", "src/lib/")`, `true`},
		{`path.match("**/*.mzs", "src/lib/")`, `false`},
		{`path.match("**", "")`, `true`},
		{`path.match("**/**/a", "a")`, `true`},
		{`path.match("a/**/b/**/c", "a/b/x/b/c")`, `true`},
		{`path.match("a/**/b/**/c", "a/c")`, `false`},
		{`path.match("?.txt", "ab.txt")`, `false`},
		{`path.match("[a-c]?.txt", "bz.txt")`, `true`},
		{`path.match("[^a-c]*", "d")`, `true`},
		{`path.match("\\*", "*")`, `true`},
		{`path.match("a**b", "axxb")`, `true`},
		{`path.match("a**b", "ax/xb")`, `false`},
	} {
		got, err := evalPath(t, tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s = %s, want %s", tc.src, got, tc.want)
		}
	}
}

// The members are each other's inverses where a script would rely on it.
func TestPathRoundTrips(t *testing.T) {
	t.Parallel()

	names := []string{"a/b/c.txt", "/etc/hosts", "../x/./y/", "a//b", ".", "", "/", ".gitignore", "a.tar.gz", "../.."}
	for _, p := range names {
		src := fmt.Sprintf(`p = %q
[path.stem(p) + path.ext(p) == path.base(p),
 path.join(*path.split(p)) == (path.clean(p) == "." ? "" : path.clean(p)),
 path.join(path.dir(p), path.base(p)) == path.clean(p) || p == "/" || path.clean(p) == "."]`, p)
		got, err := evalPath(t, src)
		if err != nil {
			t.Fatalf("%q: %v", p, err)
		}
		if got != "[true,true,true]" {
			t.Errorf("%q: %s", p, got)
		}
	}
	for _, pair := range [][2]string{{"a/b/c", "a"}, {"a", "a/b/c"}, {"a/x", "a/b"}, {"/a/b", "/"}, {"../x", "."}, {"x", "."}, {"a/b", "a/b"}} {
		src := fmt.Sprintf(`path.join(%q, path.rel(%q, %q)) == path.clean(%q)`, pair[1], pair[0], pair[1], pair[0])
		if got, err := evalPath(t, src); err != nil || got != "true" {
			t.Errorf("rel(%q, %q) does not join back: %s %v", pair[0], pair[1], got, err)
		}
	}
}

func TestPathErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ src, want string }{
		{`path.join("a", 1)`, "type"},
		{`path.ext(nil)`, "type"},
		{`path.rel("a", 2)`, "type"},
		{`path.match(1, "a")`, "type"},
		// A malformed pattern is an error whatever it is matched against — including a
		// name the pattern would have stopped looking at before the bad part.
		{`path.match("[", "x")`, `argument: path.match: malformed pattern "["`},
		{`path.match("a[", "b")`, `argument: path.match: malformed pattern "a["`},
		{`path.match("x/[", "y")`, `argument: path.match: malformed pattern "x/["`},
		{`path.dir()`, "argument"},
	} {
		_, err := evalPath(t, tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v; want %q", tc.src, err, tc.want)
		}
	}
}

// The module needs no host option: it never reaches a filesystem.
func TestPathIsUngated(t *testing.T) {
	t.Parallel()

	if _, ok := LookupModule("path", &Options{}); !ok {
		t.Fatal("path is gated")
	}
	got, err := evalPath(t, `path.keys.join(",")`)
	if err != nil || got != `"join,dir,base,ext,stem,clean,split,rel,is_abs,match"` {
		t.Errorf("path.keys = %s %v", got, err)
	}
}

// A long match is bounded by the budget, not by luck: the table is len(pattern)·len(name).
func TestPathMatchIsCharged(t *testing.T) {
	t.Parallel()

	in := New(Options{StepBudget: 1000})
	pat := strings.Repeat("**/", 50) + "x"
	name := strings.Repeat("a/", 50) + "y"
	_, err := in.Eval(context.Background(), fmt.Sprintf("include path\npath.match(%q, %q)", pat, name), nil)
	if err == nil || !strings.Contains(err.Error(), "step budget") {
		t.Errorf("err = %v; want the step budget", err)
	}
}

// Every member checks its arguments, and the two that build something are bounded.
func TestPathArgumentsAndBounds(t *testing.T) {
	t.Parallel()

	for _, m := range []string{"dir", "base", "ext", "stem", "clean", "split", "is_abs"} {
		if _, err := evalPath(t, fmt.Sprintf("path.%s(1)", m)); err == nil || !strings.Contains(err.Error(), "type") {
			t.Errorf("path.%s(1): err = %v", m, err)
		}
	}
	for _, src := range []string{`path.rel(1, "a")`, `path.match("a", 1)`} {
		if _, err := evalPath(t, src); err == nil || !strings.Contains(err.Error(), "type") {
			t.Errorf("%s: err = %v", src, err)
		}
	}
	in := New(Options{MaxStringBytes: 16})
	if _, err := in.Eval(context.Background(), `include path; path.join("aaaaaaaa", "bbbbbbbb", "c")`, nil); err == nil {
		t.Error("a join longer than MaxStringBytes was built")
	}
	in = New(Options{MaxCollection: 2})
	if _, err := in.Eval(context.Background(), `include path; path.split("a/b/c")`, nil); err == nil {
		t.Error("a split larger than MaxCollection was built")
	}
}
