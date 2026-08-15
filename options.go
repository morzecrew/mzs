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
	// Stderr receives warnings. nil discards.
	Stderr io.Writer
	// Now enables now(), time.now and date.today. nil keeps evaluation deterministic.
	Now func() time.Time
	// Rand enables rand(), uuid(), sample and shuffle. nil keeps evaluation
	// deterministic.
	Rand *rand.Rand
	// EnableTime installs the time and date modules (§12.8). Members that read the clock
	// additionally need Now.
	EnableTime bool
	// ModuleLoader lets scripts include other scripts: `include lib from "./lib.mzs"`
	// asks it for the source (§12.8). nil — the default — means includes of a path are
	// an error, because reading source is filesystem access and §14.3 does not hand
	// that to a script. Built-in modules do not go through it.
	ModuleLoader ModuleLoader
	// FS installs the io module (§12.13) and is the whole of a script's filesystem: the
	// host writes the implementation, so what a path may name is the host's policy, the
	// way ModuleLoader already is for source. nil — the default — means `include io` is
	// an error that says so.
	FS FileSystem
	// Stdin is what io.stdin and io.lines read, once per Run. nil is not an error: it is
	// an empty input, so a script written for a pipe still runs where there is none.
	Stdin io.Reader
	// Env answers io.env. nil means every name is unset, and os.Getenv is the whole of
	// what a host that wants the real environment has to pass. An empty result counts as
	// unset, so io.env("HOME", "/tmp") falls back the way a shell's ${HOME:-/tmp} does.
	Env func(name string) string
	// Location is the default zone for strftime and in_time_zone. nil means UTC.
	Location *time.Location
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

// errOut returns the sink for warnings; never nil.
func (o *Options) errOut() io.Writer {
	if o.Stderr == nil {
		return io.Discard
	}
	return o.Stderr
}
