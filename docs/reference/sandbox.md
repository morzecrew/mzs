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

The zero `Options` is the sandboxed one: a script handed nothing can compute and nothing
else. One field grants one capability.

| Capability | `Options` field | CLI |
|---|---|---|
| `io` module (files) | `FS` | on by default, `--no-io` withholds |
| `io.stdin` / `io.lines` | `Stdin` | the data stream ([input](../cli/input.md)) |
| `io.env` | `Env` | on with `io` |
| `include x from "…"` | `ModuleLoader` | rooted at the program's directory |
| `time` / `date` modules | `EnableTime` | `--time` |
| `now()`, `time.now`, `date.today` | `Now` | `--time` |
| `rand()`, `uuid()`, `sample`, `shuffle` | `Rand` | `--rand [seed]` |
| `print` / `say` / `debug` output | `Stdout` | always wired by the CLI |
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

`FS` and `ModuleLoader` are interfaces the host implements, so *what a path may name* is
host policy and never the language's. The CLI's loader refuses to leave the directory of
the program it ran:

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
| `Timeout` | `1s` | wall clock per Run | `-t`, `-t 0` disables |
| `StepBudget` | `5000000` | interpreter steps per Run | `--steps`, `--steps 0` disables |
| `MaxDepth` | `200` | call and recursion depth | — |
| `MaxTasks` | `64` | tasks live at once | `--tasks`, `--tasks 0` forbids |
| `MaxCollection` | `1000000` | elements one operation materialises | — |
| `MaxStringBytes` | `8388608` | bytes of one produced string | — |
| `RegexSteps` | `200000` | backtracking steps per match attempt | — |
| `RegexCacheSize` | `256` | runtime regex compile cache, per `Interp` | — |
| `ProgramCache` | `512` | compiled-source cache used by `Eval` | — |
| — | `64` | `include` nesting depth; not an `Options` field | — |

Six of them in one line each, verbatim, exit code `3` — the seventh, `include nesting too
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
```

The check lives inside the node loop (every 1024 steps, plus every loop back-edge), which is
why `while true { }` — a loop with nothing between statements — is still interrupted, and why
a pathological regex stops mid-attempt rather than after it.

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
# raise: io.read "big.txt": exceeds the 8388608 byte limit
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
