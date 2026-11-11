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

// The file is replaced, not truncated and refilled: a formatter that died mid-write would
// leave a source file half gone. The mode survives too — formatting a script must not take
// away the bit that lets it run.
func TestFmtWriteKeepsTheModeAndLeavesNoLitter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.mzs")
	if err := os.WriteFile(path, []byte("x=1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := exec([]string{"fmt", "-w", path}, ""); code != exitOK {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", st.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "a.mzs" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("the directory holds %v, want only a.mzs", names)
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

// `-` is stdin for `mzs -`, and it has to be stdin for `mzs fmt -` too: one token, one
// meaning, one binary.
func TestFmtDashIsStdin(t *testing.T) {
	code, out, errOut := exec([]string{"fmt", "-"}, "x=1\n")
	if code != exitOK {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if out != "x = 1\n" {
		t.Errorf("stdout = %q, want the formatted text", out)
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

// `exit(0)` is a file saying it is done; any other code is a file reporting a failure, and
// a runner that answered 0 to it would hide the one thing `exit(n)` exists to say.
func TestTestHonoursANonZeroExit(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a_test.mzs", `include test
test.case("ran") { assert(true) }
exit(3)
`)
	code, out, _ := exec([]string{"test", dir}, "")
	if code != exitError {
		t.Errorf("code = %d, want %d", code, exitError)
	}
	if !strings.Contains(out, "ok   ran") {
		t.Errorf("the case that ran before the exit was lost:\n%s", out)
	}
}

func TestTestZeroExitIsNotAFailure(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a_test.mzs", `include test
test.case("ran") { assert(true) }
exit(0)
`)
	code, out, errOut := exec([]string{"test", dir}, "")
	if code != exitOK {
		t.Errorf("code = %d, stdout:\n%s\nstderr:\n%s", code, out, errOut)
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

// `--rand [seed]` is documented with a space, so a seed written that way has to reach the
// interpreter rather than the list of paths.
func TestTestReadsASeedWrittenWithASpace(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a_test.mzs", `include test
test.case("seeded") { assert(rand() >= 0 && rand() < 1) }
`)
	for _, argv := range [][]string{
		{"test", "--rand", "7", dir},
		{"test", "--rand=7", dir},
		{"test", "--rand", dir},
	} {
		t.Run(strings.Join(argv[1:len(argv)-1], " "), func(t *testing.T) {
			code, out, errOut := exec(argv, "")
			if code != exitOK {
				t.Errorf("code = %d, stdout:\n%s\nstderr:\n%s", code, out, errOut)
			}
		})
	}
}

// A seed is only ever the next word when it is one: `mzs test --rand lib/` still finds
// its directory.
func TestTestRandDoesNotSwallowAPath(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a_test.mzs", "include test\ntest.case(\"one\") { assert(true) }\n")
	code, out, errOut := exec([]string{"test", "--rand", dir}, "")
	if code != exitOK || !strings.Contains(out, "ok   one") {
		t.Errorf("code = %d, stdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
}

// Every flag `mzs test` takes, taken. The point is not the flags but that none of them is
// silently dropped: a budget nobody applied is a test suite with no limit at all.
func TestTestFlags(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a_test.mzs", "include test\ntest.case(\"one\") { assert(true) }\n")
	for _, tc := range []struct {
		name string
		argv []string
		want int
	}{
		{"a timeout", []string{"test", "-t", "5", dir}, exitOK},
		{"a timeout attached", []string{"test", "--timeout=5s", dir}, exitOK},
		{"a step budget", []string{"test", "--steps", "1000000", dir}, exitOK},
		{"no step budget", []string{"test", "--steps", "0", dir}, exitOK},
		{"a memory budget", []string{"test", "--mem", "32m", dir}, exitOK},
		{"no memory budget", []string{"test", "--mem", "0", dir}, exitOK},
		{"a bad memory budget", []string{"test", "--mem", "lots", dir}, exitUsage},
		{"a memory budget with no value", []string{"test", "--mem"}, exitUsage},
		{"a task limit", []string{"test", "--tasks", "8", dir}, exitOK},
		{"a clock", []string{"test", "--time", dir}, exitOK},
		{"randomness", []string{"test", "--rand", dir}, exitOK},
		{"no filesystem", []string{"test", "--no-io", dir}, exitOK},
		{"a filesystem back", []string{"test", "--io", dir}, exitOK},
		{"end of flags", []string{"test", "--", dir}, exitOK},
		{"a bad duration", []string{"test", "-t", "soon", dir}, exitUsage},
		{"a bad step budget", []string{"test", "--steps", "many", dir}, exitUsage},
		{"a bad task count", []string{"test", "--tasks", "-1", dir}, exitUsage},
		{"a bad seed", []string{"test", "--rand=soon", dir}, exitUsage},
		{"a flag with no value", []string{"test", "-t"}, exitUsage},
		{"an unknown flag", []string{"test", "--nope", dir}, exitUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := exec(tc.argv, "")
			if code != tc.want {
				t.Errorf("code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tc.want, out, errOut)
			}
		})
	}
}

// A step budget small enough to trip is a file that did not finish, and the runner says so
// rather than reporting a pass.
func TestTestAppliesTheStepBudget(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a_test.mzs", "include test\ntest.case(\"counts\") { (1..100000).map { it * 2 }.sum }\n")
	code, _, errOut := exec([]string{"test", "--steps", "500", dir}, "")
	if code != exitError {
		t.Errorf("code = %d, want %d", code, exitError)
	}
	if !strings.Contains(errOut, "budget") && !strings.Contains(errOut, "limit") {
		t.Errorf("stderr = %q, want a budget", errOut)
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

// The example `mzs test --help` prints is a program, so it is run: a usage text that
// shows code which does not compile teaches the reader the wrong language.
func TestTestHelpExampleRuns(t *testing.T) {
	const head, tail = "A test file includes the module and names its cases:\n", "\nFlags:"
	start := strings.Index(testUsage, head)
	end := strings.Index(testUsage, tail)
	if start < 0 || end < start {
		t.Fatal("the usage text no longer has its example block")
	}
	var src strings.Builder
	for _, line := range strings.Split(testUsage[start+len(head):end], "\n") {
		src.WriteString(strings.TrimPrefix(line, "  ") + "\n")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "help_test.mzs")
	if err := os.WriteFile(path, []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := exec([]string{"test", path}, "")
	if code != exitOK {
		t.Fatalf("the --help example fails: code = %d\nstdout:\n%s\nstderr:\n%s\nsource:\n%s", code, out, errOut, src.String())
	}
	if !strings.Contains(out, "2 cases, 0 failed") {
		t.Errorf("stdout = %q; want both cases to run and pass", out)
	}
}

// RFC 0009: a failed case shows the calls inside the case that led to the failure, and
// nothing of the runner that called the case.
func TestTestCaseTrace(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "deep_test.mzs", `include test
fn must(x) { x == nil ? raise("missing") : x }
fn load() { must(nil) }
test.case("deep") { load() }
test.case("shallow") { raise("here") }
`)
	_, out, _ := exec([]string{"test", dir}, "")
	if !strings.Contains(out, "FAIL deep\n     missing (") ||
		!strings.Contains(out, ":2:25)\n       in must, called from ") ||
		!strings.Contains(out, "\n       in load, called from ") {
		t.Errorf("stdout =\n%s", out)
	}
	if strings.Contains(out, "in {}") || strings.Contains(out, "in case") {
		t.Errorf("the trace shows the runner's own frames:\n%s", out)
	}
	if i := strings.Index(out, "FAIL shallow"); i < 0 || strings.Contains(out[i:], "called from") {
		t.Errorf("a failure in the case body itself should have no trace:\n%s", out)
	}
}

// The failure of an awaited task carries the task's own stack, which does not end with
// the case's frames: all of it is inside the case and is shown.
func TestTestCaseTraceThroughATask(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "task_test.mzs", `include test
async fn job() { helper() }
fn helper() { raise("in task") }
test.case("awaits") { job().await }
`)
	_, out, _ := exec([]string{"test", dir}, "")
	if !strings.Contains(out, "\n       in helper, called from ") || !strings.Contains(out, "\n       in job, called from ") {
		t.Errorf("stdout =\n%s", out)
	}
}

// `mzs test --no-trace` keeps a file that fails outside a case to its three lines.
func TestTestNoTrace(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "broken_test.mzs", "include test\nfn f() { raise(\"outside\") }\nf()\n")
	_, _, errOut := exec([]string{"test", dir}, "")
	if !strings.Contains(errOut, "in f, called from") {
		t.Errorf("stderr without the flag =\n%s", errOut)
	}
	code, _, errOut := exec([]string{"test", "--no-trace", dir}, "")
	if code != exitError || strings.Contains(errOut, "called from") || !strings.Contains(errOut, "raise: outside") {
		t.Errorf("code = %d, stderr with --no-trace =\n%s", code, errOut)
	}
}

// A case's stderr is passed through unchanged (RFC 0012).
func TestTestPassesStderrThrough(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "e_test.mzs", "include test\ntest.case(\"notes\") { eprintln(\"a note\"); assert(true) }\n")
	code, out, errOut := exec([]string{"test", dir}, "")
	if code != exitOK || !strings.Contains(errOut, "a note\n") || strings.Contains(out, "a note") {
		t.Errorf("code = %d\nstdout =\n%s\nstderr =\n%s", code, out, errOut)
	}
}
