package mzs

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
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
		{`path.dir("a/b/..")`, `"a/b"`},
		{`path.dir("a/b/.")`, `"a/b"`},
		{`path.dir("../..")`, `".."`},
		{`path.dir("//")`, `"/"`},

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

// The bounds are on what a member builds: a join exactly at MaxStringBytes is fine, one
// byte over is not, and a rel whose `..`s make it longer than either argument is measured
// on its own.
func TestPathResultLimits(t *testing.T) {
	t.Parallel()

	in := New(Options{MaxStringBytes: 8})
	for src, ok := range map[string]bool{
		`path.join("12345678")`:          true,
		`path.join("1234", "567")`:       true,
		`path.join("1234", "", "5678")`:  false,
		`path.join("/", "1234567")`:      true,
		`path.join("a/..", "12345678")`:  true,
		`path.join("a", "../1234567")`:   true,
		`path.rel("a/a", "b")`:           true,
		`path.rel("a/a/a/a", "b/b/b/b")`: false,
	} {
		_, err := in.Eval(context.Background(), "include path; "+src, nil)
		if ok && err != nil {
			t.Errorf("%s: %v", src, err)
		}
		if !ok && (err == nil || !strings.Contains(err.Error(), "limit")) {
			t.Errorf("%s: err = %v; want the string limit", src, err)
		}
	}
}

// A pattern built to make one element match slow — a `*` before a long literal, against
// a long name — is charged for that work before it starts, so it ends on the budget
// rather than inside a Go call the deadline cannot reach.
func TestPathMatchChargesTheCharacterWork(t *testing.T) {
	t.Parallel()

	in := New(Options{Timeout: 10 * time.Second})
	pat := "*" + strings.Repeat("a", 100_000) + "b"
	name := strings.Repeat("a", 100_000)
	start := time.Now()
	_, err := in.Eval(context.Background(), fmt.Sprintf("include path\npath.match(%q, %q)", pat, name), nil)
	if err == nil || !strings.Contains(err.Error(), "step budget") {
		t.Errorf("err = %v; want the step budget", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v", d)
	}
	// An ordinary match costs little.
	in = New(Options{StepBudget: 200})
	if _, err := in.Eval(context.Background(), `include path; path.match("src/**/*.mzs", "src/lib/deep/a.mzs")`, nil); err != nil {
		t.Errorf("an ordinary match: %v", err)
	}
}

// A join is bounded on the way, not only at the end: a splat of many references to one
// long string is refused once the running result passes the limit, without first
// building their whole concatenation.
func TestPathJoinBoundsTheIntermediate(t *testing.T) {
	t.Parallel()

	in := New(Options{MaxStringBytes: 1 << 20, Timeout: 10 * time.Second})
	start := time.Now()
	_, err := in.Eval(context.Background(), `include path
big = "a" * 600_000
path.join(*([big] * 1000))`, nil)
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("err = %v; want the string limit", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v", d)
	}
	// Parts that clean away keep the running result small, so many of them still join.
	got, err := evalPath(t, `path.join(*(["a/b", ".."] * 500), "c")`)
	if want := `"` + strings.Repeat("a/", 500) + `c"`; err != nil || got != want {
		t.Errorf("join of parts that clean away = %.40s… (%v)", got, err)
	}
	// Joining one part at a time gives what one join of them all gives.
	for _, parts := range []string{`"a", "b/../c", "./d"`, `"/", "x", "..", "..", "y"`, `"../a", "../../b"`, `"a/", "", "/b/"`} {
		one, err1 := evalPath(t, "path.join("+parts+")")
		all, err2 := evalPath(t, "path.clean(["+parts+"].filter { it != \"\" }.join(\"/\"))")
		if err1 != nil || err2 != nil || one != all {
			t.Errorf("join(%s) = %s, clean of the joined parts = %s (%v %v)", parts, one, all, err1, err2)
		}
	}
}

// A host may hand over a string longer than MaxStringBytes. As a part of a join it is
// cleaned with what it is joined to, never on its own — which would give a different
// answer — and the result is held to the limit: one that cleans down joins, one that does
// not is refused.
func TestPathJoinOfAnOversizedHostPart(t *testing.T) {
	t.Parallel()

	in := New(Options{MaxStringBytes: 1 << 10, Timeout: 10 * time.Second})
	big := strings.Repeat("a/../", 100_000) + "x" // ~500 KB that cleans to "x"
	huge := strings.Repeat("b", 10<<20)           // 10 MB that cleans to itself
	for _, tc := range []struct {
		v    string
		want string
	}{{big, `"y/x"`}, {huge, ""}, {"/../a/../x", `"x"`}} {
		v, err := in.Eval(context.Background(), `include path; path.join("y", $big)`, map[string]Value{"$big": Str(tc.v)})
		switch {
		case tc.want == "" && (err == nil || !strings.Contains(err.Error(), "limit")):
			t.Errorf("an oversized part that does not clean down: err = %v", err)
		case tc.want != "" && (err != nil || v.Inspect() != tc.want):
			t.Errorf("an oversized part that cleans down = %v, %v", v, err)
		}
	}
}

// A path of many slashes is a short string and a long array: split, rel and match count
// the elements and hold them to MaxCollection before allocating any, and match charges
// its whole cost before it splits or validates the pattern.
func TestPathElementCountsAreBoundedFirst(t *testing.T) {
	t.Parallel()

	in := New(Options{MaxCollection: 100, Timeout: 10 * time.Second})
	many := strings.Repeat("a/", 1000) + "a"
	for _, src := range []string{
		`path.split($p)`,
		`path.rel($p, "a")`,
		`path.rel("a", $p)`,
		`path.match($p, "a")`,
		`path.match("a", $p)`,
	} {
		_, err := in.Eval(context.Background(), "include path; "+src, map[string]Value{"$p": Str(many)})
		if err == nil || !strings.Contains(err.Error(), "limit") {
			t.Errorf("%s with 1001 elements under MaxCollection 100: err = %v", src, err)
		}
	}
	// Under the bound they work as before.
	in = New(Options{MaxCollection: 2000})
	if v, err := in.Eval(context.Background(), "include path; path.split($p).len", map[string]Value{"$p": Str(many)}); err != nil || v.Inspect() != "1001" {
		t.Errorf("split under the bound = %v, %v", v, err)
	}
	// A pattern of a million slashes is refused on the budget before it is validated.
	in = New(Options{MaxCollection: 10_000_000, StepBudget: 1000})
	slashes := strings.Repeat("/", 1_000_000)
	if _, err := in.Eval(context.Background(), "include path; path.match($p, $p)", map[string]Value{"$p": Str(slashes)}); err == nil || !strings.Contains(err.Error(), "step budget") {
		t.Errorf("a slash-heavy match: err = %v", err)
	}
}

// The root is one element: it fits a collection bound of one. And a pattern's bytes are
// charged even against an empty name, since validating the pattern reads them all.
func TestPathRootAndEmptyNameBounds(t *testing.T) {
	t.Parallel()

	in := New(Options{MaxCollection: 1})
	for src, want := range map[string]string{
		`path.split("/")`:    `["/"]`,
		`path.rel("/", "/")`: `"."`,
	} {
		v, err := in.Eval(context.Background(), "include path; "+src, nil)
		if err != nil || v.Inspect() != want {
			t.Errorf("%s under MaxCollection 1 = %v, %v; want %s", src, v, err, want)
		}
	}
	in = New(Options{StepBudget: 1000})
	_, err := in.Eval(context.Background(), "include path; path.match($p, \"\")", map[string]Value{"$p": Str(strings.Repeat("a", 1_000_000))})
	if err == nil || !strings.Contains(err.Error(), "step budget") {
		t.Errorf("a long pattern against an empty name: err = %v", err)
	}
}
