package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RFC 0019 §3: `mzs test --cover` reports the included files' statements, not the tests'.
func coverFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"lib.mzs": "export fn grade(n) {\n" + // 1
			"  if n > 5 {\n" + // 2
			"    \"high\"\n" + // 3
			"  } else {\n" +
			"    \"low\"\n" + // 5
			"  }\n" +
			"}\n",
		"lib_test.mzs": "include test\ninclude lib from \"./lib.mzs\"\ntest.case(\"high\") { test.eq(lib.grade(9), \"high\") }\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCoverReportsIncludedFiles(t *testing.T) {
	dir := coverFixture(t)
	lcov := filepath.Join(t.TempDir(), "out.lcov")
	code, out, errOut := exec([]string{"test", "--cover-out", lcov, dir}, "")
	if code != exitOK {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if strings.Contains(out, "lib_test.mzs   ") || strings.Contains(out, "lib_test.mzs  ") {
		t.Errorf("the test file is in the report:\n%s", out)
	}
	if !strings.Contains(out, "lib.mzs") || !strings.Contains(out, "75.0%   (3/4)   missed: 5") {
		t.Errorf("report:\n%s", out)
	}
	if !strings.Contains(out, "total") {
		t.Errorf("no total line:\n%s", out)
	}
	b, err := os.ReadFile(lcov)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SF:" + filepath.Join(dir, "lib.mzs"), "DA:5,0", "DA:3,1", "BRDA:2,0,0,1", "BRDA:2,0,1,0", "LF:4\nLH:3", "BRF:2\nBRH:1", "end_of_record"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("LCOV lacks %q:\n%s", want, b)
		}
	}
}

func TestCoverMinFailsTheRun(t *testing.T) {
	dir := coverFixture(t)
	code, _, errOut := exec([]string{"test", "--cover-min", "80", dir}, "")
	if code != exitError || !strings.Contains(errOut, "coverage 75.0% is below --cover-min 80%") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
	if code, _, errOut := exec([]string{"test", "--cover-min", "75%", dir}, ""); code != exitOK {
		t.Errorf("75%% of 75%%: code %d, stderr %q", code, errOut)
	}
}

func TestCoverJSON(t *testing.T) {
	dir := coverFixture(t)
	code, out, errOut := exec([]string{"test", "--json", "--cover", dir}, "")
	if code != exitOK {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var last struct {
		Coverage []struct {
			File       string `json:"file"`
			Statements int    `json:"statements"`
			Run        int    `json:"run"`
			Missed     []int  `json:"missed"`
		} `json:"coverage"`
		Total float64 `json:"total"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("last line is not JSON: %v\n%s", err, out)
	}
	if len(last.Coverage) != 1 || last.Coverage[0].Run != 3 || last.Coverage[0].Statements != 4 ||
		len(last.Coverage[0].Missed) != 1 || last.Coverage[0].Missed[0] != 5 || last.Total != 75 {
		t.Errorf("coverage JSON = %+v", last)
	}
}

func TestCoverFlags(t *testing.T) {
	for _, argv := range [][]string{
		{"test", "--cover-min", "lots"},
		{"test", "--cover-min", "120"},
		{"test", "--cover-out"},
		{"test", "--cover-out", ""},
		{"test", "--cover-out="},
		{"test", "--cover-min", "NaN"},
		{"test", "--cover-min", "-1"},
	} {
		if code, _, _ := exec(argv, ""); code != exitUsage {
			t.Errorf("%v: code %d, want %d", argv, code, exitUsage)
		}
	}
	if got := lineRanges([]int{1, 2, 3, 5, 7, 8}); got != "1-3, 5, 7-8" {
		t.Errorf("lineRanges = %q", got)
	}
	// A run that includes nothing has nothing to cover, and says so.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a_test.mzs"), []byte("include test\ntest.case(\"x\") { assert(true) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := exec([]string{"test", "--cover", dir}, "")
	if code != exitOK || !strings.Contains(out, "coverage: the tests included no file to cover") {
		t.Errorf("code %d, stdout %q", code, out)
	}
	// An LCOV file that cannot be written fails the run.
	code, _, errOut := exec([]string{"test", "--cover-out", filepath.Join(dir, "no", "such", "dir.lcov"), dir}, "")
	if code != exitError || !strings.Contains(errOut, "--cover-out") {
		t.Errorf("unwritable LCOV: code %d, stderr %q", code, errOut)
	}
}

// A test file named on the command line is a test whatever it is called, and is not
// covered.
func TestCoverLeavesOutANamedTestFile(t *testing.T) {
	dir := coverFixture(t)
	checks := filepath.Join(dir, "checks.mzs")
	src, err := os.ReadFile(filepath.Join(dir, "lib_test.mzs"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checks, src, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := exec([]string{"test", "--cover", checks}, "")
	if code != exitOK {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	report := out[strings.LastIndex(out, "\n\n"):]
	if strings.Contains(report, "checks.mzs") {
		t.Errorf("the named test file is in the report:\n%s", report)
	}
	if !strings.Contains(report, "lib.mzs") {
		t.Errorf("the included file is missing:\n%s", report)
	}
}

// A test file is left out under any spelling: run as typed, included absolutely.
func TestCoverLeavesOutATestFileIncludedByAnother(t *testing.T) {
	dir := coverFixture(t)
	write := func(name, src string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("checks.mzs", "include test\nexport fn helper() { 1 }\ntest.case(\"c\") { assert(true) }\n")
	write("more.mzs", "include test\ninclude checks from \"./checks.mzs\"\ntest.case(\"m\") { assert(checks.helper() == 1) }\n")
	t.Chdir(dir)
	code, out, errOut := exec([]string{"test", "--cover", "checks.mzs", "more.mzs"}, "")
	if code != exitOK {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	report := out[strings.LastIndex(out, "\n\n"):]
	if strings.Contains(report, "checks.mzs") || strings.Contains(report, "more.mzs") {
		t.Errorf("a test file is in the report:\n%s", report)
	}
}

// A test file reached through a link is the same test: run as checks.mzs and included
// as ./alias.mzs, it is still left out, and a tests-only run is not held to a minimum.
func TestCoverLeavesOutATestFileReachedThroughALink(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("checks.mzs", "include test\nexport fn never() { 1 }\ntest.case(\"c\") { assert(true) }\n")
	if err := os.Symlink("checks.mzs", filepath.Join(dir, "alias.mzs")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	write("more.mzs", "include test\ninclude alias from \"./alias.mzs\"\ntest.case(\"m\") { assert(true) }\n")
	t.Chdir(dir)
	code, out, errOut := exec([]string{"test", "--cover-min", "100", "checks.mzs", "more.mzs"}, "")
	if code != exitOK {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "coverage: the tests included no file to cover") {
		t.Errorf("a test file reached through a link was covered:\n%s", out)
	}
}
