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
