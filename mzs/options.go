package mzs

import (
	"io"
	"math/rand"
	"time"
)

// Default limits (§13.2). A zero field in Options means "use the default", so the zero
// Options value is a usable, sandboxed configuration.
const (
	DefaultTimeout        = time.Second
	DefaultStepBudget     = int64(5_000_000)
	DefaultMaxDepth       = 200
	DefaultMaxCollection  = 1_000_000
	DefaultMaxStringBytes = 8 << 20
	DefaultRegexSteps     = 200_000
	DefaultRegexCacheSize = 256
	DefaultProgramCache   = 512
	DefaultMaxTasks       = 64
)

// FileSystem is how a host hands the filesystem to a script (§12.13). It is what
// Options.FS wants, and without one the `io` module is not installed at all — reading a
// file is a capability like the clock, not a given (§14.3).
//
// The names a script writes reach these methods unchanged: the interpreter never joins,
// cleans or resolves a path, because what a path is allowed to name is the host's policy
// and nothing the language should have an opinion about. A host that confines a script to
// one directory does it here, exactly as ModuleLoader does it for `include … from`.
//
// Every method may return an ordinary error, which the script sees as a catchable error
// (§8.11); nothing here is expected to panic.
type FileSystem interface {
	// Open reads a file. io.read closes the reader.
	Open(name string) (io.ReadCloser, error)
	// Create truncates or creates a file for writing. io.write closes the writer.
	Create(name string) (io.WriteCloser, error)
	// Append opens a file for appending, creating it when it does not exist.
	Append(name string) (io.WriteCloser, error)
	// Stat answers io.exists. A name that does not exist is (false, 0, false, nil), not
	// an error: not being there is an answer, not a failure.
	Stat(name string) (exists bool, size int64, dir bool, err error)
	// List returns the entry names of a directory, without their path.
	List(dir string) ([]string, error)
}

// The four interfaces below are optional: a FileSystem may implement any of them, and the
// io member each one backs exists only when it does (§12.13). FileSystem itself never
// grows, so every implementation written against it still compiles.
//
// The three that change the shape of a filesystem — FileRemover, DirMaker, FileMover —
// install their member only when Options.FSMutations is also true. Go interfaces are
// satisfied implicitly, and a FileSystem that already had a Remove method for reasons of
// its own must not hand scripts io.rm on upgrade: the field is the deliberate act.

// FileRemover backs io.rm. Remove deletes a file, an empty directory or a link (not what
// it points to). A name that does not exist is an error for which
// errors.Is(err, fs.ErrNotExist) holds — os.Remove's errors do — and io.rm answers false
// for it rather than raising.
type FileRemover interface {
	Remove(name string) error
}

// DirMaker backs io.mkdir. MkdirAll creates a directory and every missing parent, and a
// directory that already exists is not an error, as os.MkdirAll has it.
type DirMaker interface {
	MkdirAll(name string) error
}

// FileMover backs io.mv. Rename moves within the filesystem and replaces a file at to, as
// os.Rename does.
type FileMover interface {
	Rename(from, to string) error
}

// LinkStater backs io.walk, which needs to tell a symbolic link from what it names
// without following it — FileSystem.Stat follows links. Lstat is Stat for the name itself:
// link is true when the name is a symbolic link, and then dir and size describe the link,
// not its target. A name that does not exist is (false, 0, false, false, nil).
//
// io.walk yields a link as a name and never descends into one it has seen as a link, so a
// walk of a tree that does not change while it runs cannot enter a linked directory or a
// cycle of links below its root. The root is a name the script chose and reaches List like
// any other: one that is itself a link is refused, but a link among its parent components
// resolves as it would for Open, and a host that confines scripts refuses that root in List
// as it refuses the path in Open. A directory replaced by a link between Lstat and List can still be
// entered if List follows links. A host that must hold the rule against concurrent changes
// needs a List that follows no link in any component of the path, not only the last one:
// O_NOFOLLOW covers the final component alone, and an os.Root confines a walk to its tree
// but still follows links that stay inside it, so os.Root on its own is not enough. On
// Linux that means opening each component in turn with openat(…, O_NOFOLLOW|O_DIRECTORY)
// from the one before, or openat2 with RESOLVE_NO_SYMLINKS.
type LinkStater interface {
	Lstat(name string) (exists bool, size int64, dir, link bool, err error)
}

// Options configures an *Interp. Every capability is off by default, so a script that
// is handed nothing can compute and nothing else: no clock, no randomness, no output,
// no filesystem, no process (§14.3). One field grants one capability and nothing more —
// EnableTime installs the clock modules, FS installs the io module — and there is no
// field for processes at all; that a host can only hand over itself, through Register.
// The http module (§12.11) is the exception: it is always installed, so a script may
// reach the network without the host doing anything. A host that must not allow that
// takes the name away with Interp.Unregister("http").
// There is exactly one semantics (§9): no strict mode, no compat mode, no Options.Compat
// and therefore no field here that changes what a program means.
type Options struct {
	// StrictWarnings promotes Program.Warnings to compile errors.
	StrictWarnings bool
	// AllowWarnings drops the warnings with these codes (§17) before StrictWarnings
	// sees them: a host that promotes warnings can still opt out of one rule. A code
	// names a rule, so the list keeps working when a message's wording improves.
	AllowWarnings []string

	// Coverage, when set, counts every statement and branch each Run executes (RFC
	// 0019 §3). The host reads it with Coverage.Files after the Runs it cares about;
	// the counts of every Run that shares it add up. nil, the default, costs one
	// predictable branch per statement.
	Coverage *Coverage

	// Timeout is the wall clock allowed per Run. 0 uses DefaultTimeout; a negative
	// value disables the deadline, which is not recommended outside tests.
	Timeout time.Duration
	// StepBudget is the interpreter steps allowed per Run. 0 uses the default,
	// -1 disables the budget.
	StepBudget int64
	// MaxDepth bounds call and recursion depth so the Go stack can never overflow.
	MaxDepth int
	// MaxTasks bounds how many tasks of one Run may be unfinished at once (§8.14). A
	// task is a goroutine, so this is what keeps a `while true { work() }` over an
	// `async fn` from spawning without end. 0 uses DefaultMaxTasks; -1 disables tasks
	// entirely, and then calling an `async fn` is a limit error.
	MaxTasks int
	// MaxCollection bounds the elements any single operation may materialise.
	MaxCollection int
	// MaxStringBytes bounds the size of any single produced string.
	MaxStringBytes int
	// MaxMemory bounds the bytes a Run may hold live in script values at once (§14.2).
	// It is enforced by accounting and a reachability walk over the Run's own values,
	// never by asking the Go runtime, so the same program fails at the same point on
	// every run. 0 uses DefaultMaxMemory; a negative value disables the budget.
	MaxMemory int64
	// RegexSteps is the backtracking budget per match attempt.
	RegexSteps int
	// RegexCacheSize bounds the runtime regex compile cache, shared per *Interp.
	RegexCacheSize int
	// ProgramCache bounds the compiled-source cache used by Eval. 0 is the zero value
	// and therefore means "the default" (DefaultProgramCache), like every other bound
	// here; pass a negative size to turn caching off. normalize maps the two.
	ProgramCache int

	// Stdout receives print/println/debug. nil discards.
	Stdout io.Writer
	// Stderr receives warnings and what a script writes with eprint and eprintln
	// (§12.1). nil discards.
	Stderr io.Writer
	// Now enables now(), time.now and date.today. nil keeps evaluation deterministic.
	Now func() time.Time
	// Rand enables rand(), uuid(), sample and shuffle. nil keeps evaluation
	// deterministic.
	Rand *rand.Rand
	// CryptoRand enables crypto.random (§12.16): the bytes of a token or a nonce. It is
	// separate from Rand on purpose — Rand may be a seeded PRNG, which is what a test
	// wants and exactly what a secret must never come from. A host that sets it passes
	// crypto/rand.Reader. nil leaves crypto.random absent.
	CryptoRand io.Reader
	// EnableTime installs the time and date modules (§12.8). Members that read the clock
	// additionally need Now.
	EnableTime bool
	// ModuleLoader lets scripts include other scripts: `include lib from "./lib.mzs"`
	// asks it for the source (§12.8). nil — the default — means includes of a path are
	// an error, because reading source is filesystem access and §14.3 does not hand
	// that to a script. Built-in modules do not go through it.
	ModuleLoader ModuleLoader
	// CompileIncludes lets Compile read, through ModuleLoader, every `include … from` at the
	// top level of a file, so the fields of the records that module exports are known to
	// the includer: `m.amount` on a `money.Money`, `m.amount = v`, and the record pattern
	// `money.Money {amount} ->` (§7.8, RFC 0003). Only field names are kept; each Run still
	// loads and runs the module exactly as it does without this. false — the default —
	// means Compile never calls the loader, because a loader is not promised to answer a
	// second read the way it answered the first; a host whose loader is a file read, as
	// the CLI's is, sets it.
	CompileIncludes bool
	// FS installs the io module (§12.13) and is the whole of a script's filesystem: the
	// host writes the implementation, so what a path may name is the host's policy, the
	// way ModuleLoader already is for source. nil — the default — means `include io` is
	// an error that says so.
	FS FileSystem
	// FSMutations lets io.rm, io.mkdir and io.mv exist, each when FS also implements the
	// interface behind it (FileRemover, DirMaker, FileMover). false — the default — means
	// a script can read and write files but not remove, create or move one, whatever
	// methods FS happens to have (§12.13).
	FSMutations bool
	// Stdin is what io.stdin and io.lines read. The reader gives its bytes away once, so
	// the two members share it: io.stdin drains and caches it, io.lines pulls a line at a
	// time, and whichever is asked for first decides what the other can still have
	// (§12.13). nil is not an error: it is an empty input, so a script written for a pipe
	// still runs where there is none.
	Stdin io.Reader
	// Env answers io.env. nil means every name is unset, and os.Getenv is the whole of
	// what a host that wants the real environment has to pass. An empty result counts as
	// unset, so io.env("HOME", "/tmp") falls back the way a shell's ${HOME:-/tmp} does.
	Env func(name string) string
	// Location is the default zone for strftime and in_time_zone. nil means UTC.
	Location *time.Location
	// Test is how a host that runs test files wants the `test` module to report
	// (§12.18). The zero value is the module as a script sees it on its own: every case
	// runs, and what it prints and its report line go to Stdout as they happen.
	Test TestOptions
}

// TestOptions configures the `test` module for a runner such as `mzs test` (§12.18,
// §15.2). None of it changes what a case does — only which cases run, where their output
// goes, and whether the Run goes on after a failure.
type TestOptions struct {
	// Filter, when set, decides which cases run, given a case's full name: its
	// enclosing groups and its own name joined with " › ". A case it refuses is neither
	// run nor recorded, and its test.case answers nil.
	Filter func(name string) bool
	// FailFast ends the Run at the first case that fails, after recording it, with an
	// error that wraps ErrFailFast. Nothing catches that error: the point is to stop.
	FailFast bool
	// Quiet keeps the report lines off Stdout. Result.Tests is the whole report then,
	// which is how a host that writes its own (the CLI's --json) reads it.
	Quiet bool
	// Capture keeps what a case prints out of Stdout and in its TestCase.Output. Unless
	// Quiet, the output of a case that failed is printed under its report line, and so is
	// every case's under Verbose. A passing case's Output is kept only under Verbose, and
	// each case's capture holds at most MaxStringBytes, so a run that prints without end
	// stays inside the Run's limits (§14.1).
	Capture bool
	// Verbose prints a passing case's captured output too. Without Capture it changes
	// nothing: the output was printed as it happened.
	Verbose bool
}

// DefaultOptions returns the documented defaults: one second, five million steps, no
// capabilities.
func DefaultOptions() Options {
	return Options{
		Timeout:        DefaultTimeout,
		StepBudget:     DefaultStepBudget,
		MaxDepth:       DefaultMaxDepth,
		MaxTasks:       DefaultMaxTasks,
		MaxCollection:  DefaultMaxCollection,
		MaxStringBytes: DefaultMaxStringBytes,
		MaxMemory:      DefaultMaxMemory,
		RegexSteps:     DefaultRegexSteps,
		RegexCacheSize: DefaultRegexCacheSize,
		ProgramCache:   DefaultProgramCache,
	}
}

// normalize fills in the defaults for the zero fields. New calls it once; nothing else
// should read a raw Options.
func (o Options) normalize() Options {
	if o.Timeout == 0 {
		o.Timeout = DefaultTimeout
	}
	if o.StepBudget == 0 {
		o.StepBudget = DefaultStepBudget
	}
	if o.MaxDepth <= 0 {
		o.MaxDepth = DefaultMaxDepth
	}
	if o.MaxTasks == 0 {
		o.MaxTasks = DefaultMaxTasks
	} else if o.MaxTasks < 0 {
		o.MaxTasks = 0
	}
	if o.MaxCollection <= 0 {
		o.MaxCollection = DefaultMaxCollection
	}
	if o.MaxStringBytes <= 0 {
		o.MaxStringBytes = DefaultMaxStringBytes
	}
	if o.MaxMemory == 0 {
		o.MaxMemory = DefaultMaxMemory
	} else if o.MaxMemory < 0 {
		o.MaxMemory = 0
	}
	if o.RegexSteps <= 0 {
		o.RegexSteps = DefaultRegexSteps
	}
	if o.RegexCacheSize <= 0 {
		o.RegexCacheSize = DefaultRegexCacheSize
	}
	if o.ProgramCache < 0 {
		o.ProgramCache = 0
	} else if o.ProgramCache == 0 {
		o.ProgramCache = DefaultProgramCache
	}
	if o.Location == nil {
		o.Location = time.UTC
	}
	return o
}

// out returns the sink for print/println/debug; never nil.
func (o *Options) out() io.Writer {
	if o.Stdout == nil {
		return io.Discard
	}
	return o.Stdout
}

// errOut returns the sink for warnings and eprint/eprintln; never nil.
func (o *Options) errOut() io.Writer {
	if o.Stderr == nil {
		return io.Discard
	}
	return o.Stderr
}
