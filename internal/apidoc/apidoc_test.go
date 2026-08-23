package apidoc

import (
	"os"
	"testing"
)

// The two copies of api.json are one generated file (see the package comment). If this
// fails, `editors/vscode/data/gen`'s parse.js has written one of them and not the other.
func TestFileMatchesEditorCopy(t *testing.T) {
	const editorCopy = "../../editors/vscode/data/api.json"
	b, err := os.ReadFile(editorCopy)
	if err != nil {
		t.Fatalf("reading %s: %v", editorCopy, err)
	}
	if string(b) != string(raw) {
		t.Errorf("internal/apidoc/api.json and %s differ; regenerate both:\n"+
			"  cd editors/vscode/data/gen && go run . > registry.json && node parse.js", editorCopy)
	}
}

func TestLoadHasTheStandardLibrary(t *testing.T) {
	a := Load()
	if len(a.Methods) == 0 || len(a.Builtins) == 0 || len(a.Modules) == 0 {
		t.Fatalf("empty table: methods=%d builtins=%d modules=%d",
			len(a.Methods), len(a.Builtins), len(a.Modules))
	}
	if e, kinds := a.Method("upper"); e.Empty() || len(kinds) == 0 {
		t.Errorf("upper: got %+v on %v, want a documented string method", e, kinds)
	}
	if _, ok := a.Builtin("println"); !ok {
		t.Errorf("println is not in the builtins")
	}
	if _, ok := a.Member("json", "parse"); !ok {
		t.Errorf("json.parse is not in the modules")
	}
	if !a.IsModule("json") || a.IsModule("nosuch") {
		t.Errorf("IsModule disagrees with the table")
	}
}

// A name several kinds answer to is documented once per kind, and the entry a hover shows
// must be the same one every run: `all` is `[{ (x) -> … }] -> bool` on an array and
// `{ (k, v) -> … } -> bool` on a dict, and Go's map iteration order is not a tie-breaker
// anyone can read.
func TestMethodIsDeterministic(t *testing.T) {
	a := Load()
	shared := 0
	for _, name := range a.MethodNames() {
		first, kinds := a.Method(name)
		if len(kinds) < 2 {
			continue
		}
		shared++
		for i := 0; i < 100; i++ {
			got, gotKinds := a.Method(name)
			if got != first {
				t.Fatalf("%s: two calls disagree:\n%+v\n%+v", name, first, got)
			}
			if len(gotKinds) != len(kinds) {
				t.Fatalf("%s: the receiver list changed between calls", name)
			}
		}
	}
	if shared == 0 {
		t.Fatal("no method is answered by two kinds; this test proves nothing")
	}
}

// The entry shown is the first non-empty one in the sorted order of the kinds, which is
// what makes it predictable rather than merely stable.
func TestMethodPrefersTheFirstKindAlphabetically(t *testing.T) {
	a := Load()
	e, kinds := a.Method("all")
	if len(kinds) < 2 || kinds[0] != "array" {
		t.Fatalf("all is answered by %v; this test assumed array came first", kinds)
	}
	if want := a.Methods["array"]["all"]; e != want {
		t.Errorf("all = %+v, want the array row %+v", e, want)
	}
}
