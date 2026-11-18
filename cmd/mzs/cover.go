package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"mzs/mzs"
)

// The coverage report of `mzs test --cover` (RFC 0019 §3). What is reported is every
// file the tests' Runs loaded through `include … from`; the test files themselves are
// what does the covering and are left out.

// coveredFiles is the Coverage's files without the tests: the files this command ran
// as tests, whatever they are called, and any *_test.mzs another one included.
func coveredFiles(c *mzs.Coverage, tests map[string]bool) []mzs.FileCoverage {
	var out []mzs.FileCoverage
	for _, f := range c.Files() {
		if tests[f.File] || strings.HasSuffix(f.File, "_test.mzs") {
			continue
		}
		out = append(out, f)
	}
	return out
}

// reportCoverage prints the report, writes --cover-out and checks --cover-min. It
// answers false when the run must exit 1 for it: the total is under the minimum, or
// the LCOV file could not be written.
func reportCoverage(cfg *testConfig, stdout, stderr io.Writer) bool {
	files := coveredFiles(cfg.coverage, cfg.ran)
	run, all := 0, 0
	for _, f := range files {
		run += f.StatementsRun
		all += f.Statements
	}
	total := 100.0
	if all > 0 {
		total = 100 * float64(run) / float64(all)
	}

	if cfg.json {
		rows := make([]map[string]any, 0, len(files))
		for _, f := range files {
			rows = append(rows, map[string]any{
				"file": displayPath(f.File), "statements": f.Statements, "run": f.StatementsRun,
				"branches": f.Branches, "taken": f.BranchesTaken, "missed": f.MissedLines(),
			})
		}
		writeJSON(stdout, map[string]any{"coverage": rows, "total": round1(total)})
	} else {
		fmt.Fprint(stdout, coverageText(files, total))
	}

	ok := true
	if cfg.coverOut != "" {
		if err := writeLCOV(cfg.coverOut, files); err != nil {
			fmt.Fprintf(stderr, "mzs test: --cover-out: %v\n", err)
			ok = false
		}
	}
	if cfg.coverMin > 0 && total < cfg.coverMin {
		fmt.Fprintf(stderr, "mzs test: coverage %.1f%% is below --cover-min %g%%\n", total, cfg.coverMin)
		ok = false
	}
	return ok
}

// coverageText is the table under the summary:
//
//	cart.mzs        87.5%   (28/32)   missed: 41-43, 57
//	money.mzs      100.0%   (19/19)
//	total           92.2%
func coverageText(files []mzs.FileCoverage, total float64) string {
	var b strings.Builder
	b.WriteString("\n")
	if len(files) == 0 {
		b.WriteString("coverage: the tests included no file to cover\n")
		return b.String()
	}
	width := len("total")
	for _, f := range files {
		width = max(width, len(displayPath(f.File)))
	}
	for _, f := range files {
		fmt.Fprintf(&b, "%-*s  %6.1f%%   (%d/%d)", width, displayPath(f.File), f.Percent(), f.StatementsRun, f.Statements)
		if missed := f.MissedLines(); len(missed) > 0 {
			b.WriteString("   missed: " + lineRanges(missed))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "%-*s  %6.1f%%\n", width, "total", total)
	return b.String()
}

// lineRanges writes sorted line numbers as runs: 41, 42, 43, 57 is "41-43, 57".
func lineRanges(lines []int) string {
	var parts []string
	for i := 0; i < len(lines); {
		j := i
		for j+1 < len(lines) && lines[j+1] == lines[j]+1 {
			j++
		}
		if j == i {
			parts = append(parts, strconv.Itoa(lines[i]))
		} else {
			parts = append(parts, strconv.Itoa(lines[i])+"-"+strconv.Itoa(lines[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

// displayPath shows a file relative to the working directory when it is under it: the
// loader hands out absolute paths, and a report reads better without the prefix.
func displayPath(p string) string {
	wd, err := os.Getwd()
	if err != nil || !filepath.IsAbs(p) {
		return p
	}
	if rel, err := filepath.Rel(wd, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}

func round1(f float64) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(f, 'f', 1, 64), 64)
	return v
}

// writeLCOV writes the LCOV tracefile format: a record per file with its line counts
// (DA), its branch counts (BRDA) and the totals of each. Every CI coverage service and
// editor gutter plugin reads it.
func writeLCOV(path string, files []mzs.FileCoverage) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, fc := range files {
		fmt.Fprintf(w, "TN:\nSF:%s\n", fc.File)
		hit := 0
		for _, l := range fc.Lines {
			fmt.Fprintf(w, "DA:%d,%d\n", l.Line, l.Hits)
			if l.Hits > 0 {
				hit++
			}
		}
		for _, br := range fc.BranchHits {
			fmt.Fprintf(w, "BRDA:%d,%d,%d,%d\n", br.Line, br.Block, br.Branch, br.Hits)
		}
		fmt.Fprintf(w, "BRF:%d\nBRH:%d\nLF:%d\nLH:%d\nend_of_record\n", fc.Branches, fc.BranchesTaken, len(fc.Lines), hit)
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
