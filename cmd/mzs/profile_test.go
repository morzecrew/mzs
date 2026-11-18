package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RFC 0019 §4: `mzs --profile` prints where the steps went; `--profile-out` writes the
// folded stacks.
func TestProfileFlags(t *testing.T) {
	src := "fn score(x) { s = 0; for i in 1..x { s += i }; s }\n[1, 2].map { (x) -> score(x * 10) }.sum\n"
	folded := filepath.Join(t.TempDir(), "out.folded")
	code, out, errOut := exec([]string{"--profile", "--profile-out", folded, "-e", src}, "")
	if code != exitOK {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if strings.TrimSpace(out) != "265" {
		t.Errorf("stdout = %q: the profile goes to stderr", out)
	}
	lines := strings.Split(strings.TrimRight(errOut, "\n"), "\n")
	if !strings.HasPrefix(strings.TrimSpace(lines[0]), "self") || !strings.Contains(lines[1], "score -e:1") ||
		!strings.HasSuffix(lines[len(lines)-1], " steps") {
		t.Errorf("table:\n%s", errOut)
	}
	b, err := os.ReadFile(folded)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "(top level) -e:1;.map;{} -e:2;score -e:1 ") {
		t.Errorf("folded stacks:\n%s", b)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if i := strings.LastIndexByte(line, ' '); i < 0 || strings.Trim(line[i+1:], "0123456789") != "" {
			t.Errorf("folded line %q does not end in a count", line)
		}
	}

	// --profile-out alone writes the file and prints nothing.
	code, _, errOut = exec([]string{"--profile-out", folded, "-e", "1 + 1"}, "")
	if code != exitOK || errOut != "" {
		t.Errorf("--profile-out alone: code %d, stderr %q", code, errOut)
	}
}

func TestProfileFlagErrors(t *testing.T) {
	for _, argv := range [][]string{
		{"--profile-out", "", "-e", "1"},
		{"--profile-out=", "-e", "1"},
		{"--profile", "-n", "-e", "$_"},
	} {
		if code, _, _ := exec(argv, "a\n"); code != exitUsage {
			t.Errorf("%v: code %d, want %d", argv, code, exitUsage)
		}
	}
	code, _, errOut := exec([]string{"--profile-out", filepath.Join(t.TempDir(), "no", "dir.folded"), "-e", "1"}, "")
	if code != exitError || !strings.Contains(errOut, "--profile-out") {
		t.Errorf("unwritable profile: code %d, stderr %q", code, errOut)
	}
	// More functions than the table shows are summed on one line.
	var b strings.Builder
	for i := range profileRows + 3 {
		b.WriteString("fn f" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "() { 1 }\n")
		b.WriteString("f" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "()\n")
	}
	code, _, errOut = exec([]string{"--profile", "-e", b.String()}, "")
	if code != exitOK || !strings.Contains(errOut, "(4 more)") {
		t.Errorf("long table: code %d\n%s", code, errOut)
	}
}

// A profile that cannot be written does not hide the run's own outcome: the script's
// exit status and its error still come first.
func TestProfileWriteFailureKeepsTheRunsOutcome(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "no", "dir.folded")
	code, _, errOut := exec([]string{"--profile-out", bad, "-e", "exit(3)"}, "")
	if code != 3 || !strings.Contains(errOut, "--profile-out") {
		t.Errorf("exit(3): code %d, stderr %q", code, errOut)
	}
	code, _, errOut = exec([]string{"--profile-out", bad, "-e", "1 / 0"}, "")
	if code == exitOK || !strings.Contains(errOut, "divided by 0") || !strings.Contains(errOut, "--profile-out") {
		t.Errorf("a runtime error: code %d, stderr %q", code, errOut)
	}
}
