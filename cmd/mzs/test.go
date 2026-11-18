package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"mzs/mzs"
)

// `mzs test` (§15). It finds the `*_test.mzs` files, runs each one on its own, and
// answers with a status: 0 when every case passed, 1 when one did not.
//
// Each file is its own Run, which is not a policy this command invented — it is what a
// Run already is (§10). One file's globals, its includes and its budget reach no other,
// so a test that leaves `$state` behind cannot make the next file pass, and the order the
// files run in is not a thing anybody has to know.

// testConfig is the parsed `mzs test` command line.
type testConfig struct {
	paths []string
	opts  config

	// filter is --filter: the cases to run, matched against `group › case`.
	filter *regexp.Regexp
	// failFast is --fail-fast: the first failed case ends the whole command.
	failFast bool
	// json is --json: one object per case on stdout and nothing else there.
	json bool
	// verbose is -v: a passing case's output is printed under it too.
	verbose bool

	// cover is --cover, and --cover-out and --cover-min imply it: every file's Run
	// counts into coverage, and the report follows the summary (RFC 0019 §3).
	cover    bool
	coverOut string
	coverMin float64
	coverage *mzs.Coverage
}

// testTally is what the summary line counts.
type testTally struct {
	cases, failed, skipped int
}

func runTest(argv []string, stdout, stderr io.Writer) int {
	cfg, err := parseTestArgs(argv)
	if err != nil {
		fmt.Fprintf(stderr, "mzs test: %v\n", err)
		fmt.Fprintf(stderr, "run 'mzs test --help' for usage\n")
		return exitUsage
	}
	if cfg == nil {
		fmt.Fprint(stdout, testUsage)
		return exitOK
	}

	files, err := testFiles(cfg.paths)
	if err != nil {
		fmt.Fprintf(stderr, "mzs test: %v\n", err)
		return exitError
	}
	if len(files) == 0 {
		fmt.Fprintf(stderr, "mzs test: no *_test.mzs files under %s\n", strings.Join(cfg.paths, ", "))
		return exitError
	}

	if cfg.cover {
		cfg.coverage = mzs.NewCoverage()
	}
	var (
		total  testTally
		broken int
		ran    int
	)
	start := time.Now()
	for i, path := range files {
		if !cfg.json {
			if i > 0 {
				fmt.Fprintln(stdout)
			}
			fmt.Fprintln(stdout, path)
		}
		t, err := runTestFile(cfg, path, stdout, stderr)
		ran++
		total.cases += t.cases
		total.failed += t.failed
		total.skipped += t.skipped
		if err != nil && !errors.Is(err, mzs.ErrFailFast) {
			broken++
		}
		if cfg.failFast && (t.failed > 0 || err != nil) {
			// --fail-fast stops at the first red thing, a broken file included: the files
			// after it are not run, and the summary counts what was.
			break
		}
	}

	elapsed := time.Since(start)
	if cfg.json {
		writeJSON(stdout, map[string]any{
			"summary": true, "files": ran, "cases": total.cases, "failed": total.failed,
			"skipped": total.skipped, "broken": broken, "ms": elapsed.Milliseconds(),
		})
	} else {
		fmt.Fprintf(stdout, "\n%s, %s, %d failed%s%s  (%s)\n",
			plural(ran, "file"), plural(total.cases, "case"), total.failed,
			skippedNote(total.skipped), brokenNote(broken), elapsed.Round(time.Millisecond))
	}
	code := exitOK
	if total.failed > 0 || broken > 0 {
		code = exitError
	}
	if cfg.cover && !reportCoverage(cfg, stdout, stderr) {
		code = exitError
	}
	return code
}

// runTestFile runs one file and returns what its cases came to, and the error that ended
// it early. A file that raises outside a case — a typo, an `include` that is not there —
// is a broken file rather than a failed case, and the two are counted apart because the
// fixes are different.
func runTestFile(cfg *testConfig, path string, stdout, stderr io.Writer) (testTally, error) {
	var tally testTally
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "mzs test: %v\n", err)
		cfg.jsonError(stdout, path, err, 0)
		return tally, err
	}
	src := string(b)

	opts := cfg.opts
	opts.file = path
	// Each file is its own Run (§15.2), and keeps its own record of what it included.
	opts.loaded = map[string]string{}
	opts.root = includeRoot(&opts)
	out := stdout
	if cfg.json {
		// stdout is the JSON stream, and a line a script printed in the middle of it
		// would break every reader of it; what the script prints goes to stderr instead,
		// as it happens.
		out = stderr
	}
	o := options(&opts, out, stderr, nil)
	o.Coverage = cfg.coverage
	o.Test = mzs.TestOptions{
		FailFast: cfg.failFast,
		Quiet:    cfg.json,
		Capture:  !cfg.json,
		Verbose:  cfg.verbose,
	}
	if re := cfg.filter; re != nil {
		o.Test.Filter = re.MatchString
	}
	in := mzs.New(o)
	prog, err := in.Compile(path, src)
	if err != nil {
		reportErr(stderr, path, src, err)
		cfg.jsonError(stdout, path, err, 0)
		return tally, err
	}
	reportWarnings(stderr, path, src, prog.Warnings())

	res, rerr := in.RunResult(context.Background(), prog, nil)
	for _, t := range res.Tests {
		if cfg.json {
			writeJSON(stdout, caseJSON(path, t))
		}
		if t.RestOfFile {
			continue // the rest of the file skipped: a line in the report, not a case
		}
		tally.cases++
		switch t.Status {
		case mzs.TestFail:
			tally.failed++
		case mzs.TestSkip:
			tally.skipped++
		}
	}
	if len(res.Tests) == 0 && rerr == nil && !cfg.json {
		if cfg.filter != nil {
			fmt.Fprintln(stdout, "     (no cases match the filter)")
		} else {
			fmt.Fprintln(stdout, "     (no cases)")
		}
	}
	if rerr != nil {
		if errors.Is(rerr, mzs.ErrFailFast) {
			return tally, rerr
		}
		if code, ok := mzs.ExitCode(rerr); ok {
			// `exit(0)` inside a test file is the file saying it is done early: the cases
			// it ran up to there still count and nothing is wrong. Any other code is the
			// file reporting a failure of its own, and a runner that answered 0 to it
			// would be hiding the one thing `exit(n)` exists to say (§12.1).
			if code == 0 {
				return tally, nil
			}
			fmt.Fprintf(stderr, "mzs test: %s: exit(%d)\n", path, code)
			cfg.jsonError(stdout, path, rerr, res.Elapsed)
			return tally, rerr
		}
		reportErrTrace(stderr, path, src, rerr, !opts.noTrace, opts.loaded)
		cfg.jsonError(stdout, path, rerr, res.Elapsed)
		return tally, rerr
	}
	return tally, nil
}

// caseJSON is one case as --json writes it (§15.2). line and col are where the case was
// declared; a failure's own position is in its message, as it is in the human report.
func caseJSON(path string, t mzs.TestCase) map[string]any {
	var name, msg any
	if !t.RestOfFile {
		name = t.Name
	}
	switch {
	case t.Status == mzs.TestFail && t.Err != nil:
		msg = failureLine(t.Err)
	case t.Status == mzs.TestSkip:
		msg = t.Reason
	}
	group := t.Group
	if group == nil {
		group = []string{}
	}
	return map[string]any{
		"file": path, "name": name, "group": group, "status": t.Status,
		"message": msg, "line": t.Line, "col": t.Col, "ms": t.Elapsed.Milliseconds(),
	}
}

// jsonError is a file that did not finish, under --json: status "error", no case name,
// the position the error was raised at, and how long the file ran before it ended.
func (cfg *testConfig) jsonError(w io.Writer, path string, err error, ran time.Duration) {
	if !cfg.json {
		return
	}
	line, col := 0, 0
	var e *mzs.Error
	if errors.As(err, &e) {
		line, col = e.Line, e.Col
	}
	writeJSON(w, map[string]any{
		"file": path, "name": nil, "group": []string{}, "status": "error",
		"message": failureLine(err), "line": line, "col": col, "ms": ran.Milliseconds(),
	})
}

// failureLine is the message of a failure with the position it was raised at, as the
// line under FAIL spells it.
func failureLine(err error) string {
	var e *mzs.Error
	if !errors.As(err, &e) {
		return err.Error()
	}
	switch {
	case e.Line > 0 && e.File != "":
		return fmt.Sprintf("%s (%s:%d:%d)", e.Msg, e.File, e.Line, e.Col)
	case e.Line > 0:
		return fmt.Sprintf("%s (%d:%d)", e.Msg, e.Line, e.Col)
	}
	return e.Msg
}

// writeJSON writes one object on one line. The keys are sorted, which is what
// encoding/json does with a map, so the stream is the same from one run to the next.
func writeJSON(w io.Writer, v map[string]any) {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(fmt.Sprintf(`{"status":"error","message":%q}`, err.Error()))
	}
	_, _ = w.Write(append(b, '\n'))
}

// testFiles expands the arguments into the `*_test.mzs` files to run, in a stable order.
// A file named outright is run whatever it is called — that is how one test is rerun —
// and a directory yields only the files whose names say what they are.
func testFiles(paths []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() {
			add(p)
			continue
		}
		var found []string
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if name := d.Name(); path != p && strings.HasPrefix(name, ".") {
					return fs.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, "_test.mzs") {
				found = append(found, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		sort.Strings(found)
		for _, f := range found {
			add(f)
		}
	}
	return out, nil
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

func skippedNote(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", %d skipped", n)
}

func brokenNote(n int) string {
	if n == 0 {
		return ""
	}
	return ", " + plural(n, "file") + " did not finish"
}

// parseTestArgs reads the command line. The flags are the ones that change what a script
// may do (§14.3) plus the budget it does it in, because a test file is a script and the
// host is still the person typing.
func parseTestArgs(argv []string) (*testConfig, error) {
	cfg := &testConfig{opts: config{vars: map[string]mzs.Value{}, ioOn: true}}
	endOfFlags := false
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if endOfFlags || a == "" || a[0] != '-' {
			cfg.paths = append(cfg.paths, a)
			continue
		}
		name, val, hasVal := splitFlag(a)
		// valUsed is main's rule: a flag that takes no value refuses one given with `=`.
		valUsed := false
		need := func() (string, error) {
			if hasVal {
				valUsed = true
				return val, nil
			}
			if i+1 >= len(argv) {
				return "", fmt.Errorf("flag %s needs a value", name)
			}
			i++
			return argv[i], nil
		}
		switch name {
		case "--":
			endOfFlags = true
		case "-h", "--help":
			return nil, nil
		case "-t", "--timeout":
			v, err := need()
			if err != nil {
				return nil, err
			}
			d, err := parseDuration(v)
			if err != nil {
				return nil, err
			}
			cfg.opts.timeout, cfg.opts.timeoutSet = d, true
		case "--steps":
			v, err := need()
			if err != nil {
				return nil, err
			}
			n, perr := strconv.ParseInt(v, 10, 64)
			if perr != nil {
				return nil, fmt.Errorf("--steps wants an integer, got %q", v)
			}
			cfg.opts.steps, cfg.opts.stepsSet = n, true
		case "--mem":
			v, err := need()
			if err != nil {
				return nil, err
			}
			n, perr := parseSize(v)
			if perr != nil {
				return nil, perr
			}
			cfg.opts.mem, cfg.opts.memSet = n, true
		case "--tasks":
			v, err := need()
			if err != nil {
				return nil, err
			}
			n, perr := strconv.Atoi(v)
			if perr != nil || n < 0 {
				return nil, fmt.Errorf("--tasks wants a non-negative integer, got %q", v)
			}
			cfg.opts.tasks, cfg.opts.tasksSet = n, true
		case "--time":
			cfg.opts.enableTime = true
		case "--rand":
			// `--rand [seed]` reads the same here as on the main command line: an
			// attached seed is required to be one, and a following word is taken only
			// when it parses as an integer, so `mzs test --rand lib/` still finds its
			// directory.
			cfg.opts.randOn = true
			cfg.opts.randSeed = time.Now().UnixNano()
			if hasVal {
				valUsed = true
				n, perr := strconv.ParseInt(val, 10, 64)
				if perr != nil {
					return nil, fmt.Errorf("--rand wants an integer seed, got %q", val)
				}
				cfg.opts.randSeed = n
			} else if i+1 < len(argv) {
				if n, perr := strconv.ParseInt(argv[i+1], 10, 64); perr == nil {
					cfg.opts.randSeed = n
					i++
				}
			}
		case "--io":
			cfg.opts.ioOn = true
		case "--no-trace":
			cfg.opts.noTrace = true
		case "--no-io":
			cfg.opts.ioOn = false
		case "--read-only":
			cfg.opts.readOnly = true
		case "--filter":
			v, err := need()
			if err != nil {
				return nil, err
			}
			re, rerr := regexp.Compile(v)
			if rerr != nil {
				return nil, fmt.Errorf("--filter wants a regular expression: %v", rerr)
			}
			cfg.filter = re
		case "--fail-fast":
			cfg.failFast = true
		case "--json":
			cfg.json = true
		case "-v", "--verbose":
			cfg.verbose = true
		case "--cover":
			cfg.cover = true
		case "--cover-out":
			v, err := need()
			if err != nil {
				return nil, err
			}
			cfg.cover, cfg.coverOut = true, v
		case "--cover-min":
			v, err := need()
			if err != nil {
				return nil, err
			}
			n, perr := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
			if perr != nil || n < 0 || n > 100 {
				return nil, fmt.Errorf("--cover-min wants a percentage from 0 to 100, got %q", v)
			}
			cfg.cover, cfg.coverMin = true, n
		case "--now":
			v, err := need()
			if err != nil {
				return nil, err
			}
			t, perr := time.Parse(time.RFC3339, v)
			if perr != nil {
				return nil, fmt.Errorf("--now wants an RFC 3339 time such as 2026-01-31T09:00:00Z, got %q", v)
			}
			// A fixed clock is still a clock: --now implies --time (D15).
			cfg.opts.enableTime = true
			cfg.opts.now, cfg.opts.nowSet = t, true
		default:
			return nil, fmt.Errorf("unknown flag %s", name)
		}
		if hasVal && !valUsed {
			return nil, fmt.Errorf("flag %s takes no value, got %q", name, a)
		}
	}
	if len(cfg.paths) == 0 {
		cfg.paths = []string{"."}
	}
	return cfg, nil
}

const testUsage = `mzs test — run the *_test.mzs files

Usage:
  mzs test [flags] [path...]     a directory is searched, a file is run as it is named

A test file includes the module and names its cases:

  include test
  include decimal
  test.case("нормализация") { assert("  ДА ".lower.trim == "да") }
  test.case("деньги")       { test.eq(decimal.times(decimal.of("10.00"), 3), decimal.of("30.00")) }
  test.group("ошибки") {
    test.case("не число")   { test.raises("decimal") { decimal.of("десять") } }
  }

Flags:
  -t, --timeout <d>  wall clock per file (default 1s; 0 disables)
      --steps <n>    step budget per file (default 5000000; 0 disables)
      --mem <size>   live memory budget per file, e.g. 64m (default 64m; 0 disables)
      --tasks <n>    tasks running at once, for async fn (default 64; 0 forbids them)
      --time         enable the time/date modules and a real clock
      --rand [seed]  enable rand()/uuid(); pass a seed for reproducibility
      --no-io        withhold the io module: no files, no env, no stdin
      --read-only    io reads but cannot write, append, rm, mkdir or mv
      --no-trace     a file that fails outside a case prints its three lines without the
                     call stack (a failed case still lists the calls inside it)
      --now <t>      --time with a fixed clock at t, an RFC 3339 time: date logic that
                     reads the same answer every run
      --filter <re>  run only the cases whose full name (group › case) matches re
      --fail-fast    stop at the first failed case
      --json         one JSON object per case on stdout, then a summary; what the
                     scripts print goes to stderr
      --cover        after the summary, the share of each included file's statements
                     that ran, with the lines that did not
      --cover-out <f>
                     --cover, and write LCOV to f for a CI or an editor to read
      --cover-min <p>
                     --cover, and exit 1 when the total is below p percent
  -v, --verbose      print what each case printed under it (a failed case's always is)
  -h, --help         this text

Each file is its own Run: its globals, its includes and its budget reach no other file.
The module also has ne, near and skip: see docs/modules/test.md.

Exit codes: 0 every case passed (skipped ones count as passed), 1 a case failed or a
file did not finish, 2 usage.
`
