// Package engine is the drop-in replacement for morzebot-backend-v2's
// pkg/engine/eval. It keeps that package's call shapes — Bool for condition blocks,
// String for need_eval_* fields — and adds Value for need_eval_buttons, but evaluates
// with mzs instead of forking `ruby -e` (SPEC §13.6, §19).
//
// There is no dialect and no mode: the stored expressions are migrated once (§19.2)
// and then mean exactly what §1–§12 say they mean. Host values are bound, never
// substituted into the source (§10), so an apostrophe, a space or an emoji in a value
// can no longer reach the parser.
//
// The contract callers rely on is unchanged: an error means "no match" to
// condition.go and "fall back to the literal" to the need_eval path, so nothing here
// ever panics and every failure comes back as a usable error.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"time"

	"mzs"
	"mzs/internal/lru"
)

// ErrNilResult is what String returns when the expression evaluated to nil. The
// caller must not send an empty bubble, so a nil result is a failure and not an
// empty string (§13.6).
var ErrNilResult = errors.New("mzs/engine: expression evaluated to nil")

// ErrEmpty guards the blank expression the old runner refused to hand to ruby. It
// wraps ErrNilResult because "no expression" and "no value" are the same fallback
// decision for every caller.
var ErrEmpty = fmt.Errorf("mzs/engine: empty expression: %w", ErrNilResult)

// progName is the file name diagnostics are stamped with. Conditions have no file,
// and a stable name keeps error strings comparable in tests and logs.
const progName = "expr"

// defaultCacheSize is the compiled-expression LRU (§13.6). 107 unique conditions
// were mined from production, so 1024 holds every hot dialogue several times over.
const defaultCacheSize = 1024

// Options configures an Engine. The zero value is the documented default: a one
// second timeout, no clock, no randomness, no output.
//
// There is deliberately no field for the filesystem. An Engine evaluates conditions
// somebody else wrote and stored, which is the one embedding that must never reach a
// file, so mzs.Options.FS is left nil below and `include io` inside a condition is a
// compile error naming an option this package does not offer (SPEC §12.13, §14.3).
type Options struct {
	// Timeout caps one evaluation. 0 means one second; negative disables it.
	Timeout time.Duration
	// CacheSize bounds the compiled-expression LRU. 0 means defaultCacheSize.
	CacheSize int
	// Stdout receives say/print/debug from inside scripts. nil discards.
	Stdout io.Writer
	// Now enables now(), time.now and date.today.
	Now func() time.Time
	// Rand enables rand(), uuid(), sample and shuffle.
	Rand *rand.Rand
}

// Engine evaluates conditions. It is safe for concurrent use and caches compiled
// programs, which is where the ~45 ms → ~5 µs difference against the ruby fork
// comes from.
type Engine struct {
	in    *mzs.Interp
	opts  Options
	cache *lru.Cache[string, *compiled]
}

// compiled memoises the outcome of a compile, failures included: a condition with a
// typo is retried on every message of a hot dialogue, and re-parsing it each time
// would cost more than the conditions that work.
type compiled struct {
	prog *mzs.Program
	err  error
}

// New builds an Engine. Do any Register calls on Interp() before the first
// evaluation; registration is setup-only (§13.3).
func New(o Options) *Engine {
	size := o.CacheSize
	if size <= 0 {
		size = defaultCacheSize
	}
	return &Engine{
		in: mzs.New(mzs.Options{
			EnableTime: true,
			Timeout:    o.Timeout,
			StepBudget: mzs.DefaultStepBudget,
			Stdout:     o.Stdout,
			Now:        o.Now,
			Rand:       o.Rand,
		}),
		opts:  o,
		cache: lru.New[string, *compiled](size),
	}
}

// Default is the Engine the package-level helpers use: Options{} defaults.
func Default() *Engine { return defaultEngine }

var defaultEngine = New(Options{})

// Interp exposes the underlying interpreter so a host can Register extra
// capabilities before the first evaluation. Scripts get nothing else: no
// filesystem, no network, no process (§14.3).
func (e *Engine) Interp() *mzs.Interp { return e.in }

// Bool evaluates expr as a condition. Any error — syntax, runtime, timeout —
// returns (false, err); condition.go treats a non-nil error as "no match", which is
// what keeps a broken condition from taking a dialogue down.
func (e *Engine) Bool(ctx context.Context, expr string, vars map[string]string) (bool, error) {
	v, err := e.eval(ctx, expr, vars)
	if err != nil {
		return false, err
	}
	return v.Truthy(), nil
}

// String evaluates expr for a need_eval_* field and stringifies the result.
//
// A compile error returns the source text unchanged and no error: a plain-text
// answer is not a program, mzs has no bareword shim to pretend otherwise (§9.3), so
// the fallback that used to live in the language lives here instead (§13.6 rule 5,
// §19.3). A nil result is still ErrNilResult, so an empty bubble is never sent.
func (e *Engine) String(ctx context.Context, expr string, vars map[string]string) (string, error) {
	if strings.TrimSpace(expr) == "" {
		return "", ErrEmpty
	}
	p, err := e.program(expr)
	if err != nil {
		return expr, nil
	}
	v, err := e.run(ctx, p, vars)
	if err != nil {
		return "", err
	}
	if v.IsNil() {
		return "", ErrNilResult
	}
	return v.Str(), nil
}

// Value evaluates expr and returns the raw value, for need_eval_buttons: an array
// of dicts the caller serialises straight into an inline keyboard, with no string
// round-trip in between (§19.3).
func (e *Engine) Value(ctx context.Context, expr string, vars map[string]string) (mzs.Value, error) {
	return e.eval(ctx, expr, vars)
}

// Check compiles expr without running it and returns its warnings. This is the
// publish-time validator of §19.4: a compile is a far better gate than the legacy
// condition_validator.rb, which rejects the .downcase/.strip/=~ forms that >99% of
// pre-migration conditions use.
func (e *Engine) Check(expr string) ([]mzs.Warning, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, ErrEmpty
	}
	p, err := e.program(expr)
	if err != nil {
		return nil, err
	}
	return p.Warnings(), nil
}

// eval is the compile-then-run path Bool and Value share. String does the same two
// steps by hand because it is the one entry point that treats a compile error
// differently from a runtime one.
func (e *Engine) eval(ctx context.Context, expr string, vars map[string]string) (mzs.Value, error) {
	if strings.TrimSpace(expr) == "" {
		return mzs.Nil(), ErrEmpty
	}
	p, err := e.program(expr)
	if err != nil {
		return mzs.Nil(), err
	}
	return e.run(ctx, p, vars)
}

// program compiles through the LRU. The key is the source text alone (§13.6 rule 3):
// values are bound rather than substituted, so a dialogue that sends a new message
// every turn still compiles its condition exactly once.
func (e *Engine) program(src string) (p *mzs.Program, err error) {
	if c, ok := e.cache.Get(src); ok {
		return c.prog, c.err
	}
	defer func() {
		if r := recover(); r != nil {
			p, err = nil, fmt.Errorf("mzs/engine: internal panic: %v", r)
		}
	}()
	p, err = e.in.Compile(progName, src)
	e.cache.Add(src, &compiled{prog: p, err: err})
	return p, err
}

// run binds the host's variables and executes. The recover is belt and braces: Run
// already turns an internal panic into an error (A7), and a panic escaping into a bot
// worker would be the one failure mode this package exists to prevent.
func (e *Engine) run(ctx context.Context, p *mzs.Program, vars map[string]string) (v mzs.Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			v, err = mzs.Nil(), fmt.Errorf("mzs/engine: internal panic: %v", r)
		}
	}()
	return e.in.Run(ctx, p, lift(vars))
}

// lift turns the host's string map into bound values, normalising every key to the
// '$'-prefixed form the interpreter's globals table uses (§10). Values stay strings,
// which is why migrated expressions convert explicitly (§9.1).
func lift(vars map[string]string) map[string]mzs.Value {
	if len(vars) == 0 {
		return nil
	}
	out := make(map[string]mzs.Value, len(vars))
	for k, v := range vars {
		out[dollar(k)] = mzs.Str(v)
	}
	return out
}

func dollar(name string) string {
	if name == "" || name[0] == '$' {
		return name
	}
	return "$" + name
}

// Package-level conveniences over Default(), matching the shape morzebot's
// pkg/engine/eval forwards to (§19.4).

// Bool evaluates expr as a condition with the default engine.
func Bool(ctx context.Context, expr string, vars map[string]string) (bool, error) {
	return defaultEngine.Bool(ctx, expr, vars)
}

// String evaluates expr and stringifies the result with the default engine.
func String(ctx context.Context, expr string, vars map[string]string) (string, error) {
	return defaultEngine.String(ctx, expr, vars)
}

// Value evaluates expr and returns the raw value with the default engine.
func Value(ctx context.Context, expr string, vars map[string]string) (mzs.Value, error) {
	return defaultEngine.Value(ctx, expr, vars)
}
