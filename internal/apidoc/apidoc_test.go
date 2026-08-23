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
