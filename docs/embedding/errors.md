# Errors at the Go boundary

What `Compile`, `Run`, `RunResult` and `Eval` return on failure, and how to tell a script
error from a limit.

## *mzs.Error

Every script-visible failure is a `*mzs.Error`. `Run` never panics: a recovered internal
panic becomes `Kind == "internal"`.

```go
type Error struct {
	Kind  string   // one of the table below
	Msg   string
	File  string   // the name given to Compile
	Line  int
	Col   int
	Stack []Frame  // innermost first; Frame{Fn string; Line, Col int}
	Data  Value    // payload of raise(dict)
}

func (e *Error) Error() string      // "script.mzs:3:12: type: cannot add string to int"
func (e *Error) Unwrap() error      // the sentinel, when there is one
func (e *Error) Catchable() bool    // whether `try … else …` may swallow it
func (e *Error) ErrorValue() Value  // the dict a `try X else (e) -> …` arm binds
```

## Kinds

| `Kind` | Constant | Raised by |
|---|---|---|
| `syntax` | `ErrKindSyntax` | `Compile` |
| `name` | `ErrKindName` | undefined variable, method, member, module |
| `type` | `ErrKindType` | wrong operand or argument kind |
| `argument` | `ErrKindArgument` | arity and argument-shape failures, including undecodable text in `crypto` and `url` |
| `index` | `ErrKindIndex` | index out of range, e.g. `[1,2].insert(9, 3)` |
| `key` | `ErrKindKey` | a key that is not in the dict — `fetch` |
| `zero-division` | `ErrKindZeroDiv` | integer `/` or `%` by 0 |
| `regex` | `ErrKindRegex` | a pattern that will not compile |
| `json` | `ErrKindJSON` | `json.parse` on bad input, a value that will not encode |
| `decimal` | `ErrKindDecimal` | text that is not a decimal, a result past the width, a division with no exact form |
| `http` | `ErrKindHTTP` | a transport failure, a response over `MaxStringBytes` |
| `io` | `ErrKindIO` | a filesystem or stream failure, a read over `MaxStringBytes` |
| `raise` | `ErrKindRaise` | the `raise` builtin, and host `c.Errorf` |
| `limit` | `ErrKindLimit` | timeout, step budget, depth, cancel, collection, string size |
| `exit` | `ErrKindExit` | the `exit` builtin; read it with `ExitCode` |
| `internal` | `ErrKindInternal` | a recovered panic |

The list is closed for the runtime, but not for a script: `raise(msg, kind)` puts any other
name on an error — `"user"`, `"billing"` — so a `switch` over `Kind` needs a `default`. Four
are refused to a script, because a host reads them as facts about the Run rather than as
something the program said: `syntax`, `limit`, `exit` and `internal`.

A host function picks its own kind with `c.ErrorfKind(mzs.ErrKindIO, …)`; plain `c.Errorf`
is kind `raise`.

Observed shapes:

```
syntax   s.mzs:1:4  "unexpected end of input"
type     eval:1:3   "cannot add string to int"
raise    eval:1:1   "nope"      Data={"code":402,"message":"nope"}   Stack=[raise (1:1)]
limit    eval:1:10  "max call depth exceeded (200)"
limit    eval:1:1   "step budget exceeded (2000 steps)"
limit    eval:1:1   "execution timed out after 1ms"
limit    eval:1:11  "collection too large: 1000 elements exceeds the limit of 10"
```

`Compile` may return several errors joined with `errors.Join`; `errors.As` still finds the
first `*mzs.Error` in the tree, and `Unwrap() []error` reaches all of them.

## Sentinels

```go
var (
	ErrTimeout  = errors.New("mzs: execution timed out")
	ErrBudget   = errors.New("mzs: step budget exceeded")
	ErrDepth    = errors.New("mzs: max call depth exceeded")
	ErrCanceled = errors.New("mzs: canceled")
	ErrFatal    = errors.New("mzs: fatal")   // wrap to make a host error uncatchable
	ErrExit     = errors.New("mzs: exit")    // the script called exit(code)
)
```

The first four are the unrecoverable limits: `try` never catches them, and they always
reach the host. `ErrCanceled` is what a cancelled `context.Context` produces.

## `exit`

`exit(code)` ends the Run and names a status. It travels like a limit — uncatchable, always
reaching the host — but it is not a failure, so ask for it before you report anything:

```go
res, err := in.RunResult(ctx, prog, vars)
if code, ok := mzs.ExitCode(err); ok {
	// The script says it is done. Nothing was printed for it, and `res.Globals`
	// still holds everything it wrote.
	os.Exit(code)      // …or ignore the number: it is a request, not an act
}
```

A host function can end a Run the same way, by returning an error that wraps the sentinel —
`fmt.Errorf("shutting down: %w", mzs.ErrExit)`. It named no status, so `ExitCode` reports 0,
and `try` does not catch it either.

`ExitCode` answers only for an actual `exit`, so a script that failed on its own is never
mistaken for one. Nothing in the library calls `os.Exit` itself: a script inside a server
has no business ending the process, and whether the status means anything is the host's
decision. The code is an integer from 0 to 255 — `exit(256)` is refused in the script.

## Script error or limit

`errors.Is` against the four limit sentinels is the test; `errors.As` gets the position and
kind of everything else. A limit is a host problem (the script asked for too much time or
memory), a script error is a program problem.

```go
func classify(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, mzs.ErrTimeout), errors.Is(err, mzs.ErrBudget),
		errors.Is(err, mzs.ErrDepth), errors.Is(err, mzs.ErrCanceled):
		return "limit: " + err.Error()
	}
	var e *mzs.Error
	if errors.As(err, &e) {
		return e.Kind + " at line " + fmt.Sprint(e.Line) + ": " + e.Msg
	}
	return "host: " + err.Error()
}
```

```go
slow := mzs.New(mzs.Options{Timeout: time.Millisecond, StepBudget: -1})
_, err := slow.Eval(ctx, "i = 0\nwhile true { i = i + 1 }", nil)
fmt.Println(classify(err))
```

```
limit: eval:1:1: limit: execution timed out after 1ms
```

`e.Kind == "limit"` and the sentinels agree, but the sentinel is the sharper test: it also
matches when the error travelled out of a host function or an `include`.

## try, and what it cannot catch

Limits are not catchable, whatever the script writes:

```go
budget := mzs.New(mzs.Options{StepBudget: 2000})
budget.Eval(ctx, `try (while true { 1 }) else "caught"`, nil)
// eval:1:1: limit: step budget exceeded (2000 steps)
```

An error a host function returns is catchable unless it wraps `ErrFatal`:

```
try boom()  else "caught"   => "caught"
try fatal() else "caught"   => eval:1:5: raise: cannot continue: mzs: fatal
```

## Warnings

`Compile` succeeds with warnings; `Options.StrictWarnings` turns the first one into a
syntax error.

```go
p, _ := in.Compile("w.mzs", "x = 1\nif x = 2 { 3 }")
fmt.Println(p.Warnings())   // [2:6: warning: '=' assigns; did you mean '==' ?]
```

## See also

- [./README.md](./README.md) — where these errors come from
- [./functions.md](./functions.md) — `c.Errorf` and `ErrFatal` from a host function
- [../language/errors.md](../language/errors.md) — `try` / `raise` from the script side
- [../cli/diagnostics.md](../cli/diagnostics.md) — the same errors rendered by the CLI
