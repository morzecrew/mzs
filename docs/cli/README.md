# The mzs command

Every flag the `mzs` binary accepts, what it prints, and what it exits with.

```sh
mzs [flags] script.mzs [args...]
mzs [flags] -e '<source>' ...
cat script.mzs | mzs -
cat data | mzs -n -e '<source>'
mzs                      # REPL, when stdin is a terminal
```

## Flags

| Flag | Meaning |
|---|---|
| `-e, --eval <src>` | evaluate `<src>`; repeatable, joined with newlines; excludes a script file |
| `-p, --print` | print the last expression's value (already the default with `-e`) |
| `--no-print` | never print the value |
| `--json` | print the value as JSON instead of `str` |
| `--bool` | exit 0 when the value is truthy, 1 when it is not; prints nothing |
| `-n, --for-each-line` | run the program once per input line; the line is `$_` |
| `-l, --print-each-line` | `-n`, and print each line's value when it is not `nil` |
| `--in <path>` | read the data from a file instead of stdin |
| `--io`, `--no-io` | grant or withhold the `io` module; granted by default |
| `-v, --var k=v` | bind `$k` to the string `v`; repeatable |
| `--vars <json>` | bind every member of a JSON object as a `$var`, keeping JSON types |
| `--vars-file <path>` | the same, read from a file |
| `-t, --timeout <d>` | wall clock per run (default `1s`; `0` disables) |
| `--steps <n>` | step budget (default `5000000`; `0` disables) |
| `--tasks <n>` | tasks running at once, for `async fn` (default `64`; `0` forbids them) |
| `--time` | enable the `time`/`date` modules and a real clock |
| `--rand [seed]` | enable `rand()`/`uuid()`; pass a seed for reproducibility |
| `--stats` | print step count and elapsed time to stderr |
| `--tokens` | dump the token stream and exit |
| `--ast` | dump the AST and exit |
| `--check` | compile only; report errors and warnings |
| `--repl` | force the interactive REPL |
| `-h, --help`, `--version` | this text; the version |

`--flag=value` and `--flag value` are both accepted. `--` ends the flags, and a lone `-`
means "the program is on stdin". `--rand` only swallows the next word when it parses as an
integer, so `mzs --rand script.mzs` still finds its file.

Capabilities that are off unless a flag turns them on: the clock (`--time`), randomness
(`--rand`). The `io` module and the `http` module are on by default in the CLI — see
[../reference/sandbox.md](../reference/sandbox.md). `--net` is gone and says so:
`mzs: --net is gone: the http module is always available`.

## Printing

| Situation | What is printed |
|---|---|
| `-e '<src>'` | the last value, except a `nil` |
| a script file, or a program on stdin | nothing |
| `-p` | the last value, `nil` included (an empty line) |
| `--no-print` | nothing, ever |
| `--json` | the value as JSON, even for a script file; `nil` prints as `null` |
| `--bool` | nothing; the value becomes the exit code — unless `-p` is also given |

```sh
mzs -e 'println("hi")'        # hi           — say returns nil, so no second line
mzs -p -e 'println("hi")'     # hi, then an empty line for the nil
mzs -e '"hi"'             # hi
mzs -e '"hi"' --json      # "hi"
mzs --json -e 'nil'       # null
```

## Exit codes

| Code | Meaning |
|---|---|
| 0 | success (or a truthy value under `--bool`) |
| 1 | compile or run-time error, `--check` failure, or a falsy value under `--bool` |
| 2 | bad command line: unknown flag, missing value, unreadable file |
| 3 | timeout, step budget, call depth, or a forbidden task |

3 is separate from 1 so a supervisor can retry a run that ran away and never one that is
simply wrong.

A script that calls [`exit(code)`](../stdlib/core.md#errors-and-introspection) sets the
status itself and nothing is printed for it — that is the one way any number from 0 to 255
comes out of `mzs`. In `-n` mode the first line that exits ends the run.

```sh
mzs -e 'exit(7)'; echo $?          # 7
```

## `$ARGV`

Positional arguments after a script file arrive as an array of strings.

```sh
$ cat argv.mzs
println("name: ${$ARGV[0]}")
println("count: ${$ARGV.len}")

$ mzs argv.mzs alpha beta
name: alpha
count: 2
```

`$ARGV` is unbound — and so `nil` — when there are no arguments; `($ARGV ?? []).len` is the
safe spelling. Use `--` to pass an argument that starts with `-`.

## Worked examples

```sh
$ mzs -e '"привет".upper'
ПРИВЕТ

$ mzs -e '(0..6).map { it * 2 }.each_slice(2).array' --json
[[0,2],[4,6],[8,10],[12]]

$ mzs --vars '{"n": 3, "xs": [1,2]}' -e '$n + $xs.len'
5

$ mzs --bool -v '__sent=да' -e '$__sent == "да"' && echo match
match

$ printf '10.0.0.1 GET /\n10.0.0.2 GET /x\n10.0.0.1 GET /y\n' | mzs -n -e '$_.split(" ")[0]' | sort | uniq -c
      2 10.0.0.1
      1 10.0.0.2

$ mzs --time -e 'include time; time.now.year'
2026

$ mzs --stats -e '(1..100).sum'
mzs: 206 steps in 16µs        # stderr; the step count is deterministic, the time is not
5050

$ mzs --check examples/05_arrays_pipeline.mzs
examples/05_arrays_pipeline.mzs: ok
```

Without `--time` the module is refused rather than faked:

```sh
$ mzs -e 'include time; time.now.year'
-e:1:9: name: module 'time' needs a clock: the host did not set EnableTime (mzs --time)
  include time; time.now.year
          ^
```

## See also

- [./input.md](./input.md) — the pipe, `--in`, and line mode
- [./diagnostics.md](./diagnostics.md) — error output, fix-its, `--check`
- [../getting-started/repl.md](../getting-started/repl.md) — the interactive REPL
- [../reference/sandbox.md](../reference/sandbox.md) — capabilities and limits
