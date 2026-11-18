package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RFC 0019 §2: `mzs doc`, driven through run() as the command line types it.

const docLib = `## Total of a cart.
##
## Raises kind "billing" when currencies are mixed.
export fn total(items, cur = "USD") { items.len }

fn helper() { 1 } # a remark, not documentation

## A sum of money.
export record Money(amount, cur = "USD")
`

func TestDocPrintsMarkdownExportsFirst(t *testing.T) {
	code, out, errOut := exec([]string{"doc"}, docLib)
	if code != exitOK {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	want := "# <stdin>\n" +
		"\n## `export fn total(items, cur = \"USD\")`\n" +
		"\nTotal of a cart.\n\nRaises kind \"billing\" when currencies are mixed.\n" +
		"\n## `export record Money(amount, cur = \"USD\")`\n" +
		"\nA sum of money.\n" +
		"\n## `fn helper()`\n"
	if out != want {
		t.Errorf("stdout =\n%s\nwant\n%s", out, want)
	}
}

func TestDocJSONIsTheAPIDocShape(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib.mzs")
	if err := os.WriteFile(lib, []byte(docLib), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := exec([]string{"doc", "--json", lib}, "")
	if code != exitOK {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	var got struct {
		Modules map[string]map[string]docEntry `json:"modules"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	total := got.Modules[lib]["total"]
	want := docEntry{
		Sig:      "`fn total(items, cur = \"USD\")`",
		Sem:      "Total of a cart.\n\nRaises kind \"billing\" when currencies are mixed.",
		Kind:     "fn",
		Exported: true,
		Line:     4,
	}
	if total != want {
		t.Errorf("total = %+v, want %+v", total, want)
	}
	if h := got.Modules[lib]["helper"]; h.Exported || h.Sem != "" || h.Kind != "fn" {
		t.Errorf("helper = %+v", h)
	}
	if len(got.Modules[lib]) != 3 {
		t.Errorf("entries = %v, want total, helper and Money", got.Modules[lib])
	}
}

// A directory is every .mzs file under it except the tests, and a file that does not
// parse is reported without stopping the rest.
func TestDocWalksADirectoryAndReportsWhatDoesNotParse(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.mzs":      "## A.\nfn a() { 1 }\n",
		"a_test.mzs": "## Not part of anything's interface.\nfn t() { 1 }\n",
		"b.mzs":      "x = 1\nfn (\n",
		"c.mzs":      "y = 2\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, out, errOut := exec([]string{"doc", dir}, "")
	if code != exitError {
		t.Fatalf("code = %d, want %d", code, exitError)
	}
	if want := filepath.Join(dir, "b.mzs") + ":3:1: syntax: "; !strings.HasPrefix(errOut, want) {
		t.Errorf("stderr = %q, want it to start with %q", errOut, want)
	}
	if !strings.Contains(out, "## `fn a()`\n\nA.\n") {
		t.Errorf("a.mzs is not documented:\n%s", out)
	}
	if strings.Contains(out, "a_test.mzs") {
		t.Errorf("a test file was documented:\n%s", out)
	}
	if !strings.Contains(out, "# "+filepath.Join(dir, "c.mzs")+"\n\nNothing is declared at the top level.\n") {
		t.Errorf("c.mzs has no section of its own:\n%s", out)
	}

	// Named on the command line, a test file is documented like any other.
	code, out, _ = exec([]string{"doc", filepath.Join(dir, "a_test.mzs")}, "")
	if code != exitOK || !strings.Contains(out, "Not part of anything's interface.") {
		t.Errorf("a named test file: code %d, stdout %q", code, out)
	}
}

func TestDocUsage(t *testing.T) {
	code, out, _ := exec([]string{"doc", "--help"}, "")
	if code != exitOK || !strings.HasPrefix(out, "mzs doc — ") {
		t.Errorf("--help: code %d, stdout %q", code, out)
	}
	code, _, errOut := exec([]string{"doc", "--nope"}, "")
	if code != exitUsage || !strings.Contains(errOut, "unknown flag --nope") {
		t.Errorf("an unknown flag: code %d, stderr %q", code, errOut)
	}
	code, _, errOut = exec([]string{"doc", "no-such-file.mzs"}, "")
	if code != exitError || errOut == "" {
		t.Errorf("a missing file: code %d, stderr %q", code, errOut)
	}
	code, _, errOut = exec([]string{"doc"}, "x = \"open\n")
	if code != exitError || !strings.HasPrefix(errOut, "<stdin>:1:5: syntax: ") {
		t.Errorf("stdin that does not lex: code %d, stderr %q", code, errOut)
	}
}

// A default may hold a backtick, and the signature is still one code span.
func TestDocCodeSpanSurvivesABacktick(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"fn f()", "`fn f()`"},
		{"fn f(q = \"`\")", "``fn f(q = \"`\")``"},
		{"fn f(q = \"``\")", "```fn f(q = \"``\")```"},
		{"`x", "`` `x ``"},
	} {
		if got := codeSpan(tc.in); got != tc.want {
			t.Errorf("codeSpan(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	code, out, _ := exec([]string{"doc"}, "fn f(q = \"`\") { q }\n")
	if code != exitOK || !strings.Contains(out, "## ``fn f(q = \"`\")``\n") {
		t.Errorf("code %d, stdout %q", code, out)
	}
}

// A path that cannot be read is reported, and the files that can are still documented.
func TestDocContinuesPastAMissingPath(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.mzs")
	if err := os.WriteFile(good, []byte("## Good.\nfn g() { 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.mzs")
	code, out, errOut := exec([]string{"doc", missing, good}, "")
	if code != exitError {
		t.Errorf("code = %d, want %d", code, exitError)
	}
	if !strings.Contains(errOut, "missing.mzs") {
		t.Errorf("stderr = %q, want the missing path named", errOut)
	}
	if !strings.Contains(out, "## `fn g()`\n\nGood.\n") {
		t.Errorf("good.mzs was not documented:\n%s", out)
	}
}

// A directory that cannot be read is reported, and the files around it are still found.
func TestDocContinuesPastAnUnreadableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory whatever its mode")
	}
	dir := t.TempDir()
	for name, src := range map[string]string{
		"a.mzs":        "## A.\nfn a() { 1 }\n",
		"z.mzs":        "## Z.\nfn z() { 1 }\n",
		"locked/b.mzs": "## B.\nfn b() { 1 }\n",
		"open/c.mzs":   "## C.\nfn c() { 1 }\n",
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	locked := filepath.Join(dir, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	code, out, errOut := exec([]string{"doc", dir}, "")
	if code != exitError {
		t.Errorf("code = %d, want %d", code, exitError)
	}
	if !strings.Contains(errOut, "locked") {
		t.Errorf("stderr = %q, want the unreadable directory named", errOut)
	}
	for _, want := range []string{"A.", "C.", "Z."} {
		if !strings.Contains(out, "\n"+want+"\n") {
			t.Errorf("%s is missing from the output:\n%s", want, out)
		}
	}
}
