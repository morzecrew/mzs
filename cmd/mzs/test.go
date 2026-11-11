package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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

	var (
		cases  int
		failed int
		broken int
	)
	start := time.Now()
	for i, path := range files {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintln(stdout, path)
		ran, bad, err := runTestFile(cfg, path, stdout, stderr)
		cases += ran
		failed += bad
		if err != nil {
			broken++
		}
	}

	fmt.Fprintf(stdout, "\n%s, %s, %d failed%s  (%s)\n",
		plural(len(files), "file"), plural(cases, "case"), failed,
		brokenNote(broken), time.Since(start).Round(time.Millisecond))
	if failed > 0 || broken > 0 {
		return exitError
	}
	return exitOK
}

// runTestFile runs one file and returns how many cases it ran, how many failed, and the
// error that ended it early. A file that raises outside a case — a typo, an `include`
// that is not there — is a broken file rather than a failed case, and the two are counted
// apart because the fixes are different.
func runTestFile(cfg *testConfig, path string, stdout, stderr io.Writer) (int, int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "mzs test: %v\n", err)
		return 0, 0, err
	}
	src := string(b)

	opts := cfg.opts
	opts.file = path
	// Each file is its own Run (§15.2), and keeps its own record of what it included.
	opts.loaded = map[string]string{}
	opts.root = includeRoot(&opts)
	in := mzs.New(options(&opts, stdout, stderr, nil))
	prog, err := in.Compile(path, src)
	if err != nil {
		reportErr(stderr, path, src, err)
		return 0, 0, err
	}
	reportWarnings(stderr, path, src, prog.Warnings())

	res, rerr := in.RunResult(context.Background(), prog, nil)
	failed := 0
	for _, t := range res.Tests {
		if !t.Passed() {
			failed++
		}
	}
	if len(res.Tests) == 0 && rerr == nil {
		fmt.Fprintln(stdout, "     (no cases)")
	}
	if rerr != nil {
		if code, ok := mzs.ExitCode(rerr); ok {
			// `exit(0)` inside a test file is the file saying it is done early: the cases
			// it ran up to there still count and nothing is wrong. Any other code is the
			// file reporting a failure of its own, and a runner that answered 0 to it
			// would be hiding the one thing `exit(n)` exists to say (§12.1).
			if code == 0 {
				return len(res.Tests), failed, nil
			}
			fmt.Fprintf(stderr, "mzs test: %s: exit(%d)\n", path, code)
			return len(res.Tests), failed, rerr
		}
		reportErrTrace(stderr, path, src, rerr, !opts.noTrace, opts.loaded)
		return len(res.Tests), failed, rerr
	}
	return len(res.Tests), failed, nil
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
		need := func() (string, error) {
			if hasVal {
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
		default:
			return nil, fmt.Errorf("unknown flag %s", name)
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

Flags:
  -t, --timeout <d>  wall clock per file (default 1s; 0 disables)
      --steps <n>    step budget per file (default 5000000; 0 disables)
      --mem <size>   live memory budget per file, e.g. 64m (default 64m; 0 disables)
      --tasks <n>    tasks running at once, for async fn (default 64; 0 forbids them)
      --time         enable the time/date modules and a real clock
      --rand [seed]  enable rand()/uuid(); pass a seed for reproducibility
      --no-io        withhold the io module: no files, no env, no stdin
      --no-trace     a file that fails outside a case prints its three lines without the
                     call stack (a failed case still lists the calls inside it)
  -h, --help         this text

Each file is its own Run: its globals, its includes and its budget reach no other file.

Exit codes: 0 every case passed, 1 a case failed or a file did not finish, 2 usage.
`
