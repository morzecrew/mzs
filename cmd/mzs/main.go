// Command mzs runs mzs scripts and one-liners (SPEC §15).
//
// One-liners are the point: `mzs -e '<expr>'` compiles, runs and prints in a few
// microseconds, `-v` binds $vars without any quoting hazard, and --bool turns a
// condition into a shell exit status. A file, a pipe and an interactive REPL are
// the same evaluator with a different front door — there is one semantics and no
// mode flag that changes what a program means (§9).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mzs"
	"mzs/internal/lexer"
	"mzs/internal/rx"
	"mzs/internal/token"
)

const version = "mzs 2.0.0"

// Exit codes (§15). 3 is separate from 1 so a supervisor can tell "this condition
// is wrong" from "this condition ran away".
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
	exitLimit = 3
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Stdin))
}

// config is the parsed command line.
type config struct {
	exprs []string
	file  string
	args  []string

	printVal     bool
	printSet     bool
	printImplied bool
	asJSON       bool
	boolMode     bool

	vars map[string]mzs.Value

	timeout    time.Duration
	timeoutSet bool
	steps      int64
	stepsSet   bool
	tasks      int
	tasksSet   bool

	enableTime bool
	// stdinIsSource records that stdin was consumed as the program text — a piped script,
	// an explicit `-`, or the REPL's console. When it was not, stdin is free to be *data*
	// and goes to Options.Stdin for io.stdin/io.lines (§12.13).
	stdinIsSource bool
	// ioOn is --io/--no-io: whether this host hands the script a filesystem at all. The
	// CLI says yes by default, being its own host; --no-io is for running somebody else's
	// script the way an embedder would see it.
	ioOn bool
	// inPath is --in: a file that becomes the data stream, so the program may still arrive
	// on stdin while the data comes from somewhere else.
	inPath string
	// perLine is -n and printEach is the extra half of -l: run the program once for each
	// line of the data stream, with the line bound to $_.
	perLine   bool
	printEach bool
	// root confines `include … from` to one directory tree: the script's own directory,
	// or the working directory for -e and stdin.
	root     string
	randOn   bool
	randSeed int64

	tokens bool
	ast    bool
	check  bool
	repl   bool
	stats  bool
}

func run(argv []string, stdout, stderr io.Writer, stdin io.Reader) int {
	cfg, showHelp, showVersion, err := parseArgs(argv)
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "mzs: %v\n", err)
		fmt.Fprintf(stderr, "run 'mzs --help' for usage\n")
		return exitUsage
	case showVersion:
		fmt.Fprintln(stdout, version)
		return exitOK
	case showHelp:
		fmt.Fprint(stdout, usage)
		return exitOK
	}

	name, src, interactive, err := source(cfg, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "mzs: %v\n", err)
		return exitUsage
	}
	cfg.root = includeRoot(cfg)
	if interactive {
		// Nobody supplied a program, and with -n there is no REPL to fall back to: the
		// flag says "run this for every line" and there is no this.
		if cfg.perLine {
			fmt.Fprintf(stderr, "mzs: -n needs a program: pass -e '<source>' or a script file\n")
			return exitUsage
		}
		return repl(cfg, stdout, stderr, stdin)
	}

	if cfg.tokens {
		return dumpTokens(name, src, stdout, stderr)
	}

	data, closeData, err := dataStream(cfg, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "mzs: %v\n", err)
		return exitUsage
	}
	defer closeData()
	if cfg.perLine && data == nil {
		fmt.Fprintf(stderr, "mzs: -n has nothing to read: stdin is the program itself; give the data with --in <path>\n")
		return exitUsage
	}

	in := mzs.New(options(cfg, stdout, stderr, data))
	prog, err := in.Compile(name, src)
	if err != nil {
		reportErr(stderr, name, src, err)
		return exitError
	}
	reportWarnings(stderr, name, src, prog.Warnings())
	if cfg.check {
		lintRegexes(stderr, name, src)
		fmt.Fprintf(stdout, "%s: ok\n", name)
		return exitOK
	}
	if cfg.ast {
		dump := prog.String()
		if !strings.HasSuffix(dump, "\n") {
			dump += "\n"
		}
		fmt.Fprint(stdout, dump)
		return exitOK
	}

	if cfg.perLine {
		return runLines(cfg, in, prog, name, src, stdout, stderr, data)
	}

	ctx := context.Background()
	res, err := in.RunResult(ctx, prog, cfg.vars)
	if cfg.stats {
		fmt.Fprintf(stderr, "mzs: %d steps in %s\n", res.Steps, res.Elapsed.Round(time.Microsecond))
	}
	if err != nil {
		reportErr(stderr, name, src, err)
		return codeFor(err)
	}
	if cfg.printVal && !(cfg.printImplied && res.Value.IsNil()) {
		printValue(stdout, res.Value, cfg.asJSON)
	}
	if cfg.boolMode && !res.Value.Truthy() {
		return exitError
	}
	return exitOK
}

// source decides what to evaluate: -e wins, then a file, then a pipe, then the
// REPL. Reading stdin when it is not a terminal is what makes `cat s.mzs | mzs`
// work with no flag at all.
//
// It also settles who owns stdin. Exactly one of two things can be piped in — the program
// or its data — and the deciding question is whether the program came from somewhere else
// already: with `-e` or a file it did, so stdin is data and reaches the script as
// io.stdin. Without them stdin is the program, as it has always been.
func source(cfg *config, stdin io.Reader) (name, src string, interactive bool, err error) {
	cfg.stdinIsSource = true
	switch {
	case len(cfg.exprs) > 0:
		cfg.stdinIsSource = false
		return "-e", strings.Join(cfg.exprs, "\n"), false, nil
	case cfg.file != "" && cfg.file != "-":
		cfg.stdinIsSource = false
		b, rerr := os.ReadFile(cfg.file)
		if rerr != nil {
			return "", "", false, rerr
		}
		return cfg.file, string(b), false, nil
	case cfg.file == "-" || cfg.repl || !isTerminal(stdin):
		if cfg.repl {
			return "", "", true, nil
		}
		b, rerr := io.ReadAll(stdin)
		if rerr != nil {
			return "", "", false, rerr
		}
		return "<stdin>", string(b), false, nil
	default:
		return "", "", true, nil
	}
}

// dataStream is the other half of the same question source() answers: once the program
// has been found, what does the script get to *read*? `--in` names a file and always
// wins — that is what makes `cat script.mzs | mzs --in access.log -n` sensible, with the
// program on the pipe and the data in the file. Otherwise it is stdin, but only when
// stdin was not already spent on the program: a reader has one set of bytes, and they
// are either the source or the data (§15).
//
// A nil reader means "no data", which is not an error anywhere: io.stdin is then "" and
// io.lines is [], so a script written for a pipe still runs outside one (§12.13).
func dataStream(cfg *config, stdin io.Reader) (io.Reader, func(), error) {
	noop := func() {}
	if cfg.inPath != "" {
		f, err := os.Open(cfg.inPath)
		if err != nil {
			return nil, noop, err
		}
		return f, func() { f.Close() }, nil
	}
	if cfg.stdinIsSource {
		return nil, noop, nil
	}
	return stdin, noop, nil
}

// runLines is -n and -l: the program runs once for every line of the data stream, with
// the line bound to $_. This is the shape the one-liner genre is built around —
// `cat access.log | mzs -n -e '$_.split(" ")[0]' | sort | uniq -c` — and it is a loop on
// this side of the door, not a new mode in the language: the same *Program, run again.
//
// Lines are read as they arrive rather than slurped, so `tail -f | mzs -n` prints as the
// file grows, and each line is charged its own timeout and step budget (§14.1) — a
// per-line limit is the only one that means anything over an input of unknown length.
//
// Between lines the globals carry over and the locals do not, which is exactly what
// "every Run gets a fresh frame" (§10) already says: `$n = ($n ?? 0) + 1` counts, a local
// `n` starts again each time. Nothing else could persist without giving the CLI a state
// the language does not have.
func runLines(cfg *config, in *mzs.Interp, prog *mzs.Program, name, src string, stdout, stderr io.Writer, data io.Reader) int {
	sc := bufio.NewScanner(data)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	ctx := context.Background()

	vars := make(map[string]mzs.Value, len(cfg.vars)+1)
	for k, v := range cfg.vars {
		vars[k] = v
	}

	var (
		lines   int
		steps   int64
		elapsed time.Duration
		truthy  bool
	)
	stats := func() {
		if cfg.stats {
			fmt.Fprintf(stderr, "mzs: %d steps in %s over %d line(s)\n", steps, elapsed.Round(time.Microsecond), lines)
		}
	}

	for sc.Scan() {
		lines++
		// bufio.ScanLines drops the terminator and a CRLF's carriage return, so a file
		// off a Windows machine reads like any other — the same promise io.lines makes.
		vars["$_"] = mzs.Str(sc.Text())

		res, err := in.RunResult(ctx, prog, vars)
		steps += res.Steps
		elapsed += res.Elapsed
		if err != nil {
			stats()
			// Which line of the input broke is the one thing the diagnostic below cannot
			// say: its position is a position in the program, and the program is fine.
			fmt.Fprintf(stderr, "mzs: input line %d:\n", lines)
			reportErr(stderr, name, src, err)
			return codeFor(err)
		}
		for k, v := range res.Globals {
			vars[k] = v
		}
		if cfg.printVal && !(cfg.printImplied && res.Value.IsNil()) {
			printValue(stdout, res.Value, cfg.asJSON)
		}
		if res.Value.Truthy() {
			truthy = true
		}
	}
	if err := sc.Err(); err != nil {
		stats()
		fmt.Fprintf(stderr, "mzs: reading input: %v\n", err)
		return exitError
	}
	stats()

	// --bool over many values is grep's question, not `if`'s: the run succeeded if any
	// line answered yes. Every line still runs — a program with a `say` in it must not
	// print a different amount of output depending on where the first match landed.
	if cfg.boolMode && !truthy {
		return exitError
	}
	return exitOK
}

// maxLineBytes bounds one input line in -n mode. It is the REPL's figure, and beyond it
// the read fails loudly instead of quietly handing the program half a line.
const maxLineBytes = 4 << 20

// includeRoot is the directory includes are confined to: where the script lives, or the
// working directory when the source came from -e or a pipe and has no directory of its
// own.
func includeRoot(cfg *config) string {
	if cfg.file != "" && cfg.file != "-" {
		if dir := filepath.Dir(cfg.file); dir != "" {
			return dir
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// options builds the interpreter configuration. A user-supplied 0 means "no limit",
// which mzs.Options spells as a negative value. The last argument is the *data* stream
// (dataStream above), not the console: what a script reads, never what it was written in.
func options(cfg *config, stdout, stderr io.Writer, data io.Reader) mzs.Options {
	o := mzs.Options{
		Stdout:     stdout,
		Stderr:     stderr,
		EnableTime: cfg.enableTime,
	}
	if cfg.timeoutSet {
		o.Timeout = cfg.timeout
		if cfg.timeout == 0 {
			o.Timeout = -1
		}
	}
	if cfg.stepsSet {
		o.StepBudget = cfg.steps
		if cfg.steps == 0 {
			o.StepBudget = -1
		}
	}
	if cfg.tasksSet {
		o.MaxTasks = cfg.tasks
		if cfg.tasks == 0 {
			o.MaxTasks = -1
		}
	}
	if cfg.enableTime {
		o.Now = time.Now
	}
	if cfg.randOn {
		o.Rand = rand.New(rand.NewSource(cfg.randSeed))
	}
	o.ModuleLoader = fileLoader(cfg.root)
	// The io module (§12.13) is a host capability, and on the command line the host is
	// the person typing it: a one-liner that cannot read a file is not a shell tool. An
	// embedder gets none of this unless it installs it, which is the whole point of the
	// capability living in Options rather than in the language. `--no-io` is how the
	// person typing takes the capability back for one command — to run a script someone
	// else wrote and see it the way an embedder would.
	if cfg.ioOn {
		o.FS, o.Env = hostFS(), os.Getenv
		// In line mode the CLI owns the reader: it is being split into $_ one line at a
		// time, and handing the same reader to io.stdin as well would have two consumers
		// racing for the bytes. `io.stdin` is "" there, and the line is in $_.
		if !cfg.perLine {
			o.Stdin = data
		}
	}
	return o
}

// fileLoader is the CLI's answer to `include lib from "./lib.mzs"`. A path is resolved
// against the file doing the including — which is what makes a script movable — and then
// checked against root, the directory of the program the user ran: a script may include
// its neighbours and its subdirectories, and nothing above them. An embedder gets no
// loader at all unless it installs one (§14.3).
func fileLoader(root string) mzs.ModuleLoader {
	return func(from, path string) (string, string, error) {
		if filepath.IsAbs(path) {
			return "", "", fmt.Errorf("an include path must be relative")
		}
		base := filepath.Dir(from)
		if base == "" || base == "." || from == "" || from == "-e" || from == "<stdin>" {
			base = root
		}
		resolved, err := filepath.Abs(filepath.Join(base, path))
		if err != nil {
			return "", "", err
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return "", "", err
		}
		rel, err := filepath.Rel(absRoot, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", "", fmt.Errorf("outside the root directory %s", absRoot)
		}
		b, err := os.ReadFile(resolved)
		if err != nil {
			return "", "", err
		}
		return resolved, string(b), nil
	}
}

func printValue(w io.Writer, v mzs.Value, asJSON bool) {
	if asJSON {
		b, err := v.MarshalJSON()
		if err != nil {
			fmt.Fprintln(w, "null")
			return
		}
		fmt.Fprintln(w, string(b))
		return
	}
	fmt.Fprintln(w, v.Str())
}

// dumpTokens implements --tokens. It runs before compiling so a program that does
// not parse can still be inspected.
func dumpTokens(name, src string, stdout, stderr io.Writer) int {
	toks, errs := lexer.Lex(name, src)
	for _, t := range toks {
		if t.Kind == token.REGEX && t.Flags != "" {
			fmt.Fprintf(stdout, "%4d:%-3d %s /%s\n", t.Pos.Line, t.Pos.Col, t.String(), t.Flags)
			continue
		}
		fmt.Fprintf(stdout, "%4d:%-3d %s\n", t.Pos.Line, t.Pos.Col, t.String())
	}
	for _, e := range errs {
		fmt.Fprintf(stderr, "%s:%d:%d: syntax: %s\n", name, e.Pos.Line, e.Pos.Col, e.Msg)
	}
	if len(errs) > 0 {
		return exitError
	}
	return exitOK
}

// lintRegexes reports regex literals whose backend cannot reproduce the pattern
// exactly (§11.2). Nothing sets that flag today — the backtracking engine handles
// every construct the scanner routes to it — so this is the escape hatch that keeps
// a downgrade from ever being silent, and the place a pattern that only fails to
// compile at run time is caught by --check instead.
func lintRegexes(w io.Writer, name, src string) {
	toks, _ := lexer.Lex(name, src)
	for _, t := range toks {
		if t.Kind != token.REGEX {
			continue
		}
		r, err := rx.Compile(t.Value, t.Flags)
		if err != nil {
			fmt.Fprintf(w, "%s:%d:%d: regex: %v\n", name, t.Pos.Line, t.Pos.Col, err)
			continue
		}
		if r.Approx() {
			fmt.Fprintf(w, "%s:%d:%d: warning: /%s/%s uses an approximate backend; matches may differ from Ruby\n",
				name, t.Pos.Line, t.Pos.Col, t.Value, t.Flags)
		}
	}
}

func parseArgs(argv []string) (cfg *config, help, showVersion bool, err error) {
	cfg = &config{vars: map[string]mzs.Value{}, ioOn: true}
	positional := []string{}
	endOfFlags := false

	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if endOfFlags || a == "" || a[0] != '-' || a == "-" {
			if a == "-" && !endOfFlags && len(positional) == 0 {
				cfg.file = "-"
				continue
			}
			positional = append(positional, a)
			continue
		}
		if a == "--" {
			endOfFlags = true
			continue
		}

		name, val, hasVal := splitFlag(a)
		// need consumes the flag's argument, from "--flag=v" or the next word.
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
		case "-h", "--help":
			help = true
		case "--version":
			showVersion = true
		case "-e", "--eval":
			v, e := need()
			if e != nil {
				return nil, false, false, e
			}
			cfg.exprs = append(cfg.exprs, v)
		case "-p", "--print":
			cfg.printVal, cfg.printSet = true, true
		case "--no-print":
			cfg.printVal, cfg.printSet = false, true
		case "--json":
			cfg.asJSON = true
		case "--bool":
			cfg.boolMode = true
		case "--io":
			cfg.ioOn = true
		case "--no-io":
			cfg.ioOn = false
		case "--in":
			v, e := need()
			if e != nil {
				return nil, false, false, e
			}
			cfg.inPath = v
		case "-n", "--for-each-line":
			cfg.perLine = true
		case "-l", "--print-each-line":
			cfg.perLine, cfg.printEach = true, true
		case "-v", "--var":
			v, e := need()
			if e != nil {
				return nil, false, false, e
			}
			k, sv, ok := strings.Cut(v, "=")
			if !ok {
				return nil, false, false, fmt.Errorf("-v wants name=value, got %q", v)
			}
			cfg.vars[dollar(k)] = mzs.Str(sv)
		case "--vars":
			v, e := need()
			if e != nil {
				return nil, false, false, e
			}
			if e := addJSONVars(cfg, []byte(v)); e != nil {
				return nil, false, false, e
			}
		case "--vars-file":
			v, e := need()
			if e != nil {
				return nil, false, false, e
			}
			b, re := os.ReadFile(v)
			if re != nil {
				return nil, false, false, re
			}
			if e := addJSONVars(cfg, b); e != nil {
				return nil, false, false, e
			}
		case "-t", "--timeout":
			v, e := need()
			if e != nil {
				return nil, false, false, e
			}
			d, de := parseDuration(v)
			if de != nil {
				return nil, false, false, de
			}
			cfg.timeout, cfg.timeoutSet = d, true
		case "--steps":
			v, e := need()
			if e != nil {
				return nil, false, false, e
			}
			n, ne := strconv.ParseInt(v, 10, 64)
			if ne != nil {
				return nil, false, false, fmt.Errorf("--steps wants an integer, got %q", v)
			}
			cfg.steps, cfg.stepsSet = n, true
		case "--tasks":
			v, e := need()
			if e != nil {
				return nil, false, false, e
			}
			n, ne := strconv.Atoi(v)
			if ne != nil || n < 0 {
				return nil, false, false, fmt.Errorf("--tasks wants a non-negative integer, got %q", v)
			}
			cfg.tasks, cfg.tasksSet = n, true
		case "--time":
			cfg.enableTime = true
		case "--stats":
			cfg.stats = true
		case "--rand":
			cfg.randOn = true
			cfg.randSeed = time.Now().UnixNano()
			if hasVal {
				n, se := strconv.ParseInt(val, 10, 64)
				if se != nil {
					return nil, false, false, fmt.Errorf("--rand wants an integer seed, got %q", val)
				}
				cfg.randSeed = n
			} else if i+1 < len(argv) {
				if n, se := strconv.ParseInt(argv[i+1], 10, 64); se == nil {
					cfg.randSeed = n
					i++
				}
			}
		case "--tokens":
			cfg.tokens = true
		case "--ast":
			cfg.ast = true
		case "--check":
			cfg.check = true
		case "--repl":
			cfg.repl = true
		case "--net":
			// Removed in 2.1: the http module is installed unconditionally. Saying so
			// beats "unknown flag" for the one flag that used to be mandatory and is
			// still typed in old command lines and READMEs.
			return nil, false, false, fmt.Errorf("--net is gone: the http module is always available")
		default:
			return nil, false, false, fmt.Errorf("unknown flag %s", name)
		}
	}

	if len(positional) > 0 {
		if cfg.file == "" {
			cfg.file, positional = positional[0], positional[1:]
		}
		cfg.args = positional
	}
	if len(cfg.exprs) > 0 && cfg.file != "" && cfg.file != "-" {
		return nil, false, false, fmt.Errorf("-e and a script file are mutually exclusive")
	}
	if cfg.perLine && cfg.repl {
		return nil, false, false, fmt.Errorf("-n reads stdin as data and --repl reads it as commands; pick one")
	}
	if !cfg.printSet {
		// A one-liner is asked for its value; a script is asked to do its job, and -l
		// asks a script for the value of every line. printImplied records that nobody
		// typed -p, which is what lets `mzs -e 'say("hi")'` print "hi" and not "hi" plus
		// a blank line for the nil that say returns. An explicit -p still prints
		// whatever there is, nils included.
		cfg.printVal = (len(cfg.exprs) > 0 || cfg.printEach) && !cfg.boolMode
		cfg.printImplied = true
	}
	if cfg.asJSON && !cfg.printSet && !cfg.boolMode {
		// --json is a request for the value, so it turns printing on for a file too;
		// --bool is a request for an exit status and stays silent.
		cfg.printVal, cfg.printImplied = true, false
	}
	if len(cfg.args) > 0 {
		argv := make([]mzs.Value, len(cfg.args))
		for i, a := range cfg.args {
			argv[i] = mzs.Str(a)
		}
		cfg.vars["$ARGV"] = mzs.Array(argv...)
	}
	return cfg, help, showVersion, nil
}

// splitFlag separates "--name=value" without disturbing "-v" style flags.
func splitFlag(a string) (name, val string, hasVal bool) {
	if i := strings.IndexByte(a, '='); i > 0 {
		return a[:i], a[i+1:], true
	}
	return a, "", false
}

// parseDuration accepts both "500ms" and a bare number of seconds, because a
// timeout typed into a shell is usually just "2".
func parseDuration(s string) (time.Duration, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Duration(f * float64(time.Second)), nil
	}
	return 0, fmt.Errorf("bad duration %q", s)
}

// addJSONVars binds every member of a JSON object as a $var, keeping JSON types
// (numbers stay numbers, arrays stay arrays) rather than flattening to strings.
func addJSONVars(cfg *config, data []byte) error {
	var v mzs.Value
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("--vars wants a JSON object: %w", err)
	}
	if v.Kind() != mzs.KDict {
		return fmt.Errorf("--vars wants a JSON object, got %s", v.Kind())
	}
	for _, k := range v.Keys() {
		cfg.vars[dollar(k.Str())] = v.Get(k)
	}
	return nil
}

func dollar(name string) string {
	if name == "" || name[0] == '$' {
		return name
	}
	return "$" + name
}

// isTerminal reports whether r is an interactive terminal, which is how the CLI
// decides between "read a piped script" and "start a REPL".
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

const usage = `mzs — a small scripting language for fast one-liners

Usage:
  mzs [flags] script.mzs [args...]
  mzs [flags] -e '<source>' ...
  cat script.mzs | mzs -
  cat data | mzs -n -e '<source>'
  mzs                      # REPL, when stdin is a terminal

Flags:
  -e, --eval <src>   evaluate <src>; repeatable, joined with newlines
  -p, --print        print the value of the last expression (default with -e)
      --no-print     never print the value
      --json         print the value as JSON instead of str
      --bool         exit 0 when the value is truthy, 1 when it is not
  -n, --for-each-line    run the program once per input line; the line is $_
  -l, --print-each-line  -n, and print each line's value when it is not nil
      --in <path>    read the data from a file instead of stdin
      --no-io        withhold the io module: no files, no env, no stdin
  -v, --var k=v      bind $k to the string v; repeatable
      --vars <json>  bind every member of a JSON object as a $var
      --vars-file <path>   same, read from a file
  -t, --timeout <d>  wall clock per run (default 1s; 0 disables)
      --steps <n>    step budget (default 5000000; 0 disables)
      --tasks <n>    tasks running at once, for async fn (default 64; 0 forbids them)
      --time         enable the time/date modules and a real clock
      --rand [seed]  enable rand()/uuid(); pass a seed for reproducibility
      --stats        print step count and elapsed time to stderr
      --tokens       dump the token stream and exit
      --ast          dump the AST and exit
      --check        compile only; report errors and warnings
      --repl         force the interactive REPL
  -h, --help         this text
      --version      print the version

Exit codes: 0 ok, 1 error (or falsy with --bool), 2 usage, 3 timeout or budget.

Examples:
  mzs -e 'say("hi")'
  mzs -e '"привет".upper'
  cat access.log | mzs -n -e '$_.split(" ")[0]' | sort | uniq -c
  ls | mzs -e 'include io; io.lines.filter { it.ends_with(".mzs") }.len'
  mzs -v '__sent=  ОПЕРАТОР ' -e '$__sent.lower.trim == "оператор"'
  mzs -e 's = $__sent.lower; s ~ /привет|hello/i' --vars '{"__sent":"Привет!"}'
  mzs -e '(0..6).map { it * 2 }.each_slice(2).array' --json
  mzs -e 'fn f(a, b) { a += b; return a }; f(1, 2)'
  mzs -e 'match $__sent.lower.trim { in ["да","ага"] -> 1; else -> 0 }' -v '__sent=Ага'
  mzs --bool -v '__sent=да' -e '$__sent == "да"' && echo match
`
