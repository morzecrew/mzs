# Sandbox and limits

What a script can reach, what a host has to hand over first, and the resource limits every
run is bounded by.

## Never reachable

No process, no `eval`, no reflection, no ambient import: these are not gated options, the
names do not exist, and no source can be compiled at runtime.

```sh
mzs -e 'system("ls")'    # -e:1:1: name: undefined function 'system'
mzs -e 'eval("1+1")'     # -e:1:1: name: undefined function 'eval'
mzs -e 'sleep(1)'        # -e:1:1: name: undefined function 'sleep'
mzs -e 'ENV["HOME"]'     # -e:1:1: name: undefined variable 'ENV'
mzs -e 'File.read("x")'  # -e:1:1: name: undefined variable 'File'
```

## Granted by the host

The zero `Options` is the sandboxed one: it grants no host capability, so a script handed
nothing can compute and nothing else — with one exception, `http`, which is installed
whether or not the host asks and is described [below](#http-is-the-one-exception). One
field grants one capability.

| Capability | `Options` field | CLI |
|---|---|---|
| `io` module (files) | `FS` | on by default, `--no-io` withholds |
| `io.stdin` / `io.lines` / `input()` | `Stdin` | the data stream ([input](../cli/input.md)) |
| `io.env` | `Env` | on with `io` |
| `include x from "…"` | `ModuleLoader` | a `./` path is rooted at the program's directory; a bare name is a library on `$MZSPATH`, rooted where it was found |
| `time` / `date` modules | `EnableTime` | `--time` |
| `now()`, `time.now`, `date.today` | `Now` | `--time` |
| `rand()`, `uuid()`, `sample`, `shuffle` | `Rand` | `--rand [seed]` |
| `print` / `println` / `debug` output | `Stdout` | always wired by the CLI |
| runtime notices (a failed unawaited task, an http handler error) | `Stderr` | always wired by the CLI |

Off, each is a compile-time name error that says which field is missing:

```sh
mzs --no-io -e 'include io; io.read("x")'
# -e:1:9: name: module 'io' needs a filesystem: the host did not install Options.FS
mzs -e 'include time; time.now'
# -e:1:9: name: module 'time' needs a clock: the host did not set EnableTime (mzs --time)
mzs -e '[1,2,3].shuffle'
# -e:1:9: name: undefined method 'shuffle' for array (did you mean 'shuffle'?)
```

The last one is not a typo: a name that only a capability installs stays in the suggestion
table, so the fix-it names the method you wrote and the missing option is the real answer.

`--no-io` withholds `FS`, `Env` and `Stdin`. It does not withhold the loader: `include … from`
is its own capability and not a way to read a file — it reads source, runs it as part of the
same program, and hands back what that source exported, confined to the tree the including
file belongs to. A script that may not reach its own second file is a different script. An
embedder gets neither unless it installs it.

`FS` and `ModuleLoader` are interfaces the host implements, so *what a path may name* is
host policy and never the language's. The CLI's loader refuses to leave the tree the
including file belongs to — the directory of the program it ran, or, for a library found on
`$MZSPATH`, the search directory it came from ([custom modules](../modules/custom.md)):

```sh
mzs -e 'include lib from "../lib.mzs"'
# -e:1:1: name: cannot include "../lib.mzs": outside the root directory …
mzs -e 'include lib from "/etc/passwd"'
# -e:1:1: name: cannot include "/etc/passwd": an include path must be relative
```

## `http` is the one exception

The `http` module is installed with no option asked for, so a script may open a listener or
call out without the host doing anything.

```
include http; type(http)      # dict
```

A host that must not allow that takes the name away, and then a script cannot name it:

```go
in := mzs.New(mzs.Options{})
in.Unregister("http")
// include http; 1
// eval:1:9: name: unknown module 'http' (a script module needs a path: include http from "./http.mzs")
```

Every limit below applies to a client call unchanged. Under `http.serve` the deadline and
the step budget are re-armed per request — a slow handler fails that request with 500 and
the server keeps serving — and what the handlers spent is added back to the Run's counter
when `serve` returns.

## Limits

| Field | Default | Bounds | CLI |
|---|---|---|---|
| `Timeout` | `1s` | wall clock per Run, checked between evaluation steps — a blocking host function is not interrupted by it (see [../embedding/functions.md](../embedding/functions.md#blocking)) | `-t`, `-t 0` disables |
| `StepBudget` | `5000000` | interpreter steps per Run | `--steps`, `--steps 0` disables |
| `MaxDepth` | `200` | call and recursion depth | — |
| `MaxTasks` | `64` | tasks live at once | `--tasks`, `--tasks 0` forbids |
| `MaxCollection` | `1000000` | elements one operation materialises | — |
| `MaxStringBytes` | `8388608` | bytes of one produced string | — |
| `MaxMemory` | `67108864` | bytes held live in script values at once | `--mem 64m`, `--mem 0` disables |
| `RegexSteps` | `200000` | backtracking steps per match attempt | — |
| `RegexCacheSize` | `256` | runtime regex compile cache, per `Interp` | — |
| `ProgramCache` | `512` | compiled-source cache used by `Eval` | — |
| — | `64` | `include` nesting depth; not an `Options` field | — |

Seven of them in one line each, verbatim, exit code `3` — the eighth, `include nesting too
deep (64)`, needs a chain of files ([modules/custom.md](../modules/custom.md)):

```sh
mzs -e 'while true { }'
# -e:1:1: limit: step budget exceeded (5000000 steps)
mzs --steps 0 -t 200ms -e 'while true { }'
# -e:1:1: limit: execution timed out after 200ms
mzs -e 'fn f(n) { f(n+1) }; f(0)'
# -e:1:11: limit: max call depth exceeded (200)
mzs -e '(0..2000000).array'
# -e:1:14: limit: collection too large: 2000001 elements exceeds the limit of 1000000
mzs -e '"a" * 100000000'
# -e:1:5: limit: string too large: 100000000 bytes exceeds the limit of 8388608
mzs --tasks 2 -e 'async fn f(n) { n }; 5.times.map { f(it) }'
# -e:1:36: limit: too many tasks: 2 already running (MaxTasks)
mzs -e 'xs = []; s = "x" * 1000000; i = 0; while true { xs.push(s + i.str); i += 1 }'
# -e:1:59: limit: memory budget exceeded (64.85 MiB live, the limit is 64 MiB)
```

## The memory budget

`MaxCollection` and `MaxStringBytes` bound **one** operation before it allocates. `MaxMemory`
bounds how much a Run holds **at once**: the array of a thousand 1 MB strings above breaks
neither per-operation cap and ends on this one. It is a budget on *live* bytes, not on what
was ever allocated — a loop that builds a string and drops it, an `io.lines.each` over a log
far bigger than the budget, an `http.serve` that runs for a week all stay under it:

```sh
mzs --mem 4m --stats -e 's = "x" * 500000; n = 0; for i in 0..200 { n += (s + i.str).len }; n'
# mzs: 2021 steps in 79ms, peak 0.95 MiB live     (stderr)
# 100500493
```

How it works (SPEC §14.2): allocations are charged as they happen, pessimistically, and when
the total crosses a threshold the Run walks what it can still reach — globals, modules, every
executing scope and every value a call in progress holds, in every task — and replaces the
total with that. The walk is over the script's own values, not a question to the Go runtime,
so a program that fails the budget fails it at the same step every time. It may overshoot by
up to an eighth of the budget before the walk that stops it, and one allocation bigger than
what is left is refused before it is made.

What the host bound is not charged: a 100 MB `$catalog` passed to `Run` costs the script
nothing, and only what the script builds on top of it counts. `Result.PeakMemory` reports the
most a walk found live (0 when none ran), and is the number to size the budget with. The
sizes are estimates — a `Value` slot is 48 bytes, a string its bytes — and leave out Go's
allocator overhead and garbage the Go collector has not reclaimed yet, so the process can be
a small multiple of the budget; what matters is that it is bounded.

A value a registered Go function *keeps* after it returns is invisible to the walk: the host
owns what it retains.

The check lives inside the node loop (every 1024 steps, plus every loop back-edge), which is
why `while true { }` — a loop with nothing between statements — is still interrupted, and why
a pathological regex stops mid-attempt rather than after it.

Waiting is inside these limits rather than beside them. `sleep`, `await`, `t.timeout` and
`tasks.all`/`tasks.race` all wake on the deadline and on the host's context as well as on
what they were waiting for, so a pause is spent from the run's clock and never extends it:

```sh
mzs -e 'sleep(2)'
# -e:1:1: limit: execution timed out after 1s
```

## Limits are not catchable

`try … else …` never swallows a limit; it reaches the host every time.

```sh
mzs -e 'try (while true { }) else "caught"'
# -e:1:1: limit: step budget exceeded (5000000 steps)
mzs -e 'try ((0..2000000).array) else "caught"'
# -e:1:19: limit: collection too large: 2000001 elements exceeds the limit of 1000000
```

Sizes that arrive from *outside* the process — a file, an HTTP body — are ordinary
catchable errors instead, because the reader stops at the limit either way and nothing is
evaded by catching it:

```
include io; try io.read("big.txt").len else (e) -> "${e["kind"]}: ${e["message"]}"
# io: io.read "big.txt": exceeds the 8388608 byte limit
```

The per-attempt regex budget is the same shape — kind `regex`, catchable, the Run continues:

```
try (("a" * 40) ~ /(?!z)(a+)+b/) else "caught"   # caught
```

Uncaught, that expression is `-e:1:12: regex: mzs/rx/bt: regex step budget exceeded`.

## Panics become internal errors

A panic anywhere below `Run` — an evaluator bug or a host function's — is recovered at the
`Run` boundary, tasks are joined, and the embedder gets an `*Error` of kind `internal` that
`try` cannot catch. The Go stack is attached only under `StrictWarnings`.

```go
in.Register("boom", 0, func(c *mzs.Ctx, args []mzs.Value) (mzs.Value, error) { panic("host function bug") })
// try boom() else "caught"  ->  kind=internal catchable=false
// eval: internal: internal error: host function bug
```

## See also

- [Limitations and reserved syntax](./limitations.md)
- [Errors: try, raise, what is not catchable](../language/errors.md)
- [Embedding: FileSystem, Stdin, Env, ModuleLoader](../embedding/filesystem.md)
- [The http module](../modules/http.md)
