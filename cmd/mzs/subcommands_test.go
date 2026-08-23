package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SPEC §15: the three subcommands. They are driven through run() exactly as main does,
// so what these tests exercise is the command line a person types.

// ---------------------------------------------------------------------------
// mzs fmt
// ---------------------------------------------------------------------------

func TestFmtReadsStdinAndWritesStdout(t *testing.T) {
	code, out, errOut := exec([]string{"fmt"}, "x=1+2\nif x>2 {println(\"big\")}\n")
	if code != exitOK {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	want := "x = 1 + 2\nif x > 2 { println(\"big\") }\n"
	if out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestFmtWritesFilesInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.mzs")
	if err := os.WriteFile(path, []byte("x=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := exec([]string{"fmt", "-w", path}, "")
	if code != exitOK || out != "" {
		t.Fatalf("code = %d, stdout = %q, stderr = %s", code, out, errOut)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "x = 1\n" {
		t.Errorf("file = %q, want %q", b, "x = 1\n")
	}
	// A second run has nothing to do and must not rewrite the file.
	if code, _, _ := exec([]string{"fmt", "-w", path}, ""); code != exitOK {
		t.Errorf("the second run failed with %d", code)
	}
}

func TestFmtListsOnlyWhatDiffers(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.mzs")
	bad := filepath.Join(dir, "bad.mzs")
	if err := os.WriteFile(good, []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("x=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := exec([]string{"fmt", "-l", dir}, "")
	if code != exitOK {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if strings.TrimSpace(out) != bad {
		t.Errorf("stdout = %q, want only %q", out, bad)
	}
}

// A directory is walked for .mzs files, in a stable order, and files that are not mzs
// are left alone.
func TestFmtWalksADirectory(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"b.mzs":     "y=2\n",
		"a.mzs":     "x=1\n",
		"notes.txt": "x=1\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code, out, _ := exec([]string{"fmt", dir}, "")
	if code != exitOK {
		t.Fatalf("code = %d", code)
	}
	if out != "x = 1\ny = 2\n" {
		t.Errorf("stdout = %q, want a.mzs then b.mzs and nothing else", out)
	}
}

func TestFmtReportsWhatDoesNotParse(t *testing.T) {
	code, out, errOut := exec([]string{"fmt"}, "x = (1 +\n")
	if code != exitError {
		t.Errorf("code = %d, want %d", code, exitError)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
	if !strings.Contains(errOut, "mzs fmt:") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestFmtUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		has  string
	}{
		{"-w and -l disagree", []string{"fmt", "-w", "-l", "."}, "pick one"},
		{"-w has nothing to write to", []string{"fmt", "-w"}, "-w needs files"},
		{"an unknown flag", []string{"fmt", "--nope"}, "unknown flag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, errOut := exec(tc.argv, "")
			if code != exitUsage {
				t.Errorf("code = %d, want %d", code, exitUsage)
			}
			if !strings.Contains(errOut, tc.has) {
				t.Errorf("stderr = %q, want it to mention %q", errOut, tc.has)
			}
		})
	}
}

func TestFmtHelp(t *testing.T) {
	code, out, _ := exec([]string{"fmt", "--help"}, "")
	if code != exitOK || !strings.Contains(out, "mzs fmt") {
		t.Errorf("code = %d, stdout = %q", code, out)
	}
}

// ---------------------------------------------------------------------------
// mzs test
// ---------------------------------------------------------------------------

func writeTestFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTestRunsEveryFileAndCountsThem(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a_test.mzs", "include test\ntest.case(\"one\") { assert(true) }\n")
	writeTestFile(t, dir, "b_test.mzs", "include test\ntest.case(\"two\") { assert(true) }\n")
	writeTestFile(t, dir, "helper.mzs", "x = 1\n")

	code, out, errOut := exec([]string{"test", dir}, "")
	if code != exitOK {
		t.Fatalf("code = %d, stderr = %s\n%s", code, errOut, out)
	}
	for _, want := range []string{"ok   one", "ok   two", "2 files, 2 cases, 0 failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout does not contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "helper.mzs") {
		t.Errorf("a file that is not a test was run:\n%s", out)
	}
}

func TestTestFailsWhenACaseFails(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a_test.mzs", `include test
test.case("passes") { assert(true) }
test.case("fails") { test.eq(1, 2) }
`)
	code, out, _ := exec([]string{"test", dir}, "")
	if code != exitError {
		t.Errorf("code = %d, want %d", code, exitError)
	}
	if !strings.Contains(out, "FAIL fails") || !strings.Contains(out, "1 file, 2 cases, 1 failed") {
		t.Errorf("stdout:\n%s", out)
	}
}

// Each file is its own Run (§10), so what one leaves in a global cannot reach the next.
func TestTestIsolatesFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a_test.mzs", "include test\n$leak = 1\ntest.case(\"sets\") { assert(true) }\n")
	writeTestFile(t, dir, "b_test.mzs", "include test\ntest.case(\"sees nothing\") { assert($leak == nil) }\n")
	code, out, _ := exec([]string{"test", dir}, "")
	if code != exitOK {
		t.Errorf("code = %d, want a pass:\n%s", code, out)
	}
}

// A file that raises outside a case did not finish; the exit status says so and the
// summary counts it apart from a failed case.
func TestTestReportsABrokenFile(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a_test.mzs", "include test\nraise(\"before any case\")\n")
	code, out, errOut := exec([]string{"test", dir}, "")
	if code != exitError {
		t.Errorf("code = %d, want %d", code, exitError)
	}
	if !strings.Contains(out, "did not finish") {
		t.Errorf("stdout:\n%s", out)
	}
	if !strings.Contains(errOut, "before any case") {
		t.Errorf("stderr:\n%s", errOut)
	}
}

func TestTestSaysWhenThereIsNothingToRun(t *testing.T) {
	dir := t.TempDir()
	code, _, errOut := exec([]string{"test", dir}, "")
	if code != exitError {
		t.Errorf("code = %d, want %d", code, exitError)
	}
	if !strings.Contains(errOut, "no *_test.mzs files") {
		t.Errorf("stderr = %q", errOut)
	}
}

// A file named outright runs whatever it is called: that is how one test is rerun.
func TestTestRunsANamedFile(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "checks.mzs", "include test\ntest.case(\"named\") { assert(true) }\n")
	code, out, _ := exec([]string{"test", path}, "")
	if code != exitOK || !strings.Contains(out, "ok   named") {
		t.Errorf("code = %d, stdout:\n%s", code, out)
	}
}

func TestTestPassesTheTimeoutOn(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a_test.mzs", "include test\ntest.case(\"loops\") { while true { 1 } }\n")
	code, _, errOut := exec([]string{"test", "-t", "50ms", dir}, "")
	if code != exitError {
		t.Errorf("code = %d, want %d", code, exitError)
	}
	if !strings.Contains(errOut, "timeout") && !strings.Contains(errOut, "limit") {
		t.Errorf("stderr = %q, want a limit", errOut)
	}
}

func TestTestHelp(t *testing.T) {
	code, out, _ := exec([]string{"test", "--help"}, "")
	if code != exitOK || !strings.Contains(out, "mzs test") {
		t.Errorf("code = %d, stdout = %q", code, out)
	}
}

// ---------------------------------------------------------------------------
// mzs lsp
// ---------------------------------------------------------------------------

func TestLSPHelp(t *testing.T) {
	code, out, _ := exec([]string{"lsp", "--help"}, "")
	if code != exitOK || !strings.Contains(out, "language server") {
		t.Errorf("code = %d, stdout = %q", code, out)
	}
}

func TestLSPRejectsArguments(t *testing.T) {
	code, _, errOut := exec([]string{"lsp", "--nope"}, "")
	if code != exitUsage || !strings.Contains(errOut, "unknown argument") {
		t.Errorf("code = %d, stderr = %q", code, errOut)
	}
}

// An empty stream is a client that closed without a word, which is a clean shutdown.
func TestLSPEndsOnAClosedStream(t *testing.T) {
	code, _, errOut := exec([]string{"lsp"}, "")
	if code != exitOK {
		t.Errorf("code = %d, stderr = %q", code, errOut)
	}
}

// A script may still be called fmt.mzs: the subcommand is the bare word.
func TestASubcommandDoesNotShadowAFile(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "fmt.mzs", "println(\"ran\")\n")
	code, out, errOut := exec([]string{path}, "")
	if code != exitOK || out != "ran\n" {
		t.Errorf("code = %d, stdout = %q, stderr = %q", code, out, errOut)
	}
}
