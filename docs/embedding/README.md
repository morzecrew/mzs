# Embedding mzs in Go

How a Go program creates an interpreter, compiles a script and runs it.

The module is named `mzs` and the root package is the whole embedding surface:

```go
import "mzs"
```

## New, Compile, Run

```go
in := mzs.New(mzs.Options{Timeout: time.Second, Stdout: os.Stdout})

prog, err := in.Compile("cond#12", `$sent.lower.trim == "yes"`)
if err != nil { return err }

v, err := in.Run(ctx, prog, map[string]mzs.Value{"$sent": mzs.Str("  YES ")})
// v.Truthy() == true
```

`Compile(name, src)` — `name` is what appears in diagnostics: an int `$sent` fails that
program as `cond#12:1:7: name: undefined method 'lower' for int`.
Vars may be spelled `"$sent"` or `"sent"`; both reach `$sent` in the script.

## RunResult

`Run` is `RunResult` with the extras dropped.

```go
res, err := in.RunResult(ctx, prog, vars)
res.Value    // mzs.Value — the value of the last expression
res.Globals  // map[string]mzs.Value — final $vars, keys '$'-prefixed, incl. ones the script created
res.Steps    // int64 — interpreter steps charged
res.Elapsed  // time.Duration
```

`res.Globals` is how a script hands values back: assigning `$total = …` in the script makes
`res.Globals["$total"]` readable afterwards.

## Eval and the program cache

```go
v, err := in.Eval(ctx, `$price.int + 1200`, map[string]mzs.Value{"$price": mzs.Str("800")})
```

`Eval` compiles under the fixed name `eval` and runs. `Compile` itself is cached too, keyed
on `(name, src)`, so calling it twice with the same pair returns the identical `*Program`
pointer. Set `ProgramCache: -1` to turn the cache off; `0` means "use the default", not
"disabled".

## Concurrency

A `*Program` is immutable: compile once, run it from as many goroutines as you like — all
mutable state lives in the per-Run frame, and two concurrent Runs never see each other's
`$vars`. One `*Interp` serves unlimited concurrent Runs. `Register`, `RegisterModule`,
`SetGlobal` and `Unregister` are setup-only and must all happen before the first Run.

## Options

Every *host-granted* capability is off by default; the zero `Options` computes and nothing
else. The one exception is `http`, installed with no option asked for — a host that must
not allow network access calls `Unregister("http")`, see
[../reference/sandbox.md](../reference/sandbox.md#http-is-the-one-exception). `0` means
"use the default" for every limit; a row that can be switched *off* at all says with what.

| Field | Default | Notes |
|---|---|---|
| `StrictWarnings` | `false` | promotes `Program.Warnings()` to a compile error |
| `Timeout` | `1s` | wall clock per Run; negative disables the deadline |
| `StepBudget` | `5000000` | `-1` disables |
| `MaxDepth` | `200` | call/recursion depth; cannot be disabled |
| `MaxTasks` | `64` | unfinished tasks per Run; `-1` forbids `async fn` |
| `MaxCollection` | `1000000` | elements one operation may materialise; cannot be disabled |
| `MaxStringBytes` | `8388608` | bytes of one produced string; cannot be disabled |
| `RegexSteps` | `200000` | backtracking budget per match; cannot be disabled |
| `RegexCacheSize` | `256` | runtime regex compile cache, per `*Interp` |
| `ProgramCache` | `512` | compiled-source LRU; `-1` disables |
| `Stdout` | `nil` | sink for `print`/`println`/`debug`; nil discards |
| `Stderr` | `nil` | runtime notices (a failed unawaited task, an http handler error) |
| `Now` | `nil` | enables `now()`, `time.now`, `date.today` |
| `Rand` | `nil` | enables `rand()`, `uuid()`, `sample`, `shuffle` |
| `EnableTime` | `false` | installs the `time` and `date` modules |
| `ModuleLoader` | `nil` | enables `include x from "…"` |
| `FS` | `nil` | installs the `io` module |
| `Stdin` | `nil` | `io.stdin`/`io.lines`/`input()`; nil is empty input |
| `Env` | `nil` | answers `io.env`; nil means every name is unset |
| `Location` | `time.UTC` | default zone for `strftime`/`in_time_zone` |

`mzs.DefaultOptions()` returns the same values explicitly. `in.Options()` returns the
normalised set the interpreter actually runs with. `http` is the one module installed
unconditionally — take it away with `in.Unregister("http")`.

The CLI spells the off switches as `0`, not `-1`: `-t 0`, `--steps 0` and `--tasks 0`
translate to the negative field values above ([../cli/README.md](../cli/README.md)).

## A complete program

```go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"mzs"
)

func main() {
	in := mzs.New(mzs.Options{Timeout: time.Second, Stdout: os.Stdout})
	prog, err := in.Compile("greet.mzs", `
println("hello, " + $who)
$total = $price.int + 1200
$who.len`)
	if err != nil {
		panic(err)
	}
	res, err := in.RunResult(context.Background(), prog, map[string]mzs.Value{
		"$who": mzs.Str("Ann"), "$price": mzs.Str("800"),
	})
	fmt.Println(res.Value.Inspect(), res.Globals["$total"].Inspect(), res.Steps, err)
}
```

```
hello, Ann
3 2000 15 <nil>
```

## See also

- [./functions.md](./functions.md) — registering your own functions and modules
- [./values.md](./values.md) — building and reading `mzs.Value`
- [./errors.md](./errors.md) — telling a script error from a limit
- [../reference/sandbox.md](../reference/sandbox.md) — what the defaults do and do not grant
