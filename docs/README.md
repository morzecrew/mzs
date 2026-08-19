# mzs documentation

Reference documentation for mzs, an embeddable scripting language written in Go, one page per topic.

**Start here:** [getting-started/README.md](./getting-started/README.md) — what the language is, the rules its syntax follows, and a first program. In a hurry, [the cheat sheet](./getting-started/cheatsheet.md) is one runnable line per row.

## Getting started

| Page | Answers |
|---|---|
| [getting-started/README.md](./getting-started/README.md) | What kind of language this is, a first program, what surprises newcomers |
| [getting-started/install.md](./getting-started/install.md) | Building the binary, and every way to hand it a program |
| [getting-started/repl.md](./getting-started/repl.md) | The interactive session: inspect output, continuation, dot-commands, what persists |
| [getting-started/cheatsheet.md](./getting-started/cheatsheet.md) | One-liners for text, collections, JSON, numbers, regex, files — with their results |

## CLI

| Page | Answers |
|---|---|
| [cli/README.md](./cli/README.md) | Every flag, the printing rules, exit codes, `$ARGV` |
| [cli/input.md](./cli/input.md) | Whether stdin carries the program or the data, `--in`, `-n` and `-l` line mode |
| [cli/diagnostics.md](./cli/diagnostics.md) | The shape of an error message, the error kinds, the fix-its, warnings, `--check` |

## Language

| Page | Answers |
|---|---|
| [language/README.md](./language/README.md) | The three rules, expression orientation, the keyword list, an index of these pages |
| [language/values.md](./language/values.md) | The kinds and their literals, `record` shapes, truthiness, equality, copying |
| [language/operators.md](./language/operators.md) | The precedence table, integer division, no implicit conversion, the runes that are not operators |
| [language/control-flow.md](./language/control-flow.md) | `if`, `while`, `for`, `break`/`next`, statement modifiers, `match` — all as expressions |
| [language/destructuring.md](./language/destructuring.md) | One shape rule in three places: assignment, `match` arm, `for` header |
| [language/functions.md](./language/functions.md) | `fn`, defaults and `*rest`, closures and `it`, UFCS, scope, recursion depth |
| [language/strings.md](./language/strings.md) | The three forms — two quoted and the heredoc — escapes, `$`-interpolation, runes not bytes, the `%` operator |
| [language/errors.md](./language/errors.md) | `try … else … ensure`, the error dict and its kind, `raise`, `assert`, and what is never catchable |
| [language/host-variables.md](./language/host-variables.md) | `$name`: a namespace the host owns, where an unbound read is `nil` rather than an error |
| [language/async.md](./language/async.md) | `async fn`, `await`, `done`, one evaluator at a time, the task and time budgets |
| [language/regex.md](./language/regex.md) | `/…/` literals, `~` and `!~`, the two engines, flags, the step budget, dynamic regexes |

## Standard library

| Page | Answers |
|---|---|
| [stdlib/README.md](./stdlib/README.md) | One flat namespace, how `x.f(y)` resolves, how to read the tables |
| [stdlib/core.md](./stdlib/core.md) | Output and `input`, sizes and kinds, conversions, aggregates, `format`, error introspection |
| [stdlib/strings.md](./stdlib/strings.md) | Case, trimming, testing, searching, splitting, replacing, padding — in runes |
| [stdlib/arrays.md](./stdlib/arrays.md) | Every array function, with the ten mutating rows kept separate |
| [stdlib/dicts.md](./stdlib/dicts.md) | Reading, writing, merging and iterating a dict, and the insertion-order guarantee |
| [stdlib/numbers.md](./stdlib/numbers.md) | int and float behaviour, rounding, predicates, and the eight bit functions |
| [stdlib/ranges.md](./stdlib/ranges.md) | `..` and `..<`, laziness, `step`, ranges as indices and in `for`/`match` |
| [stdlib/sequences.md](./stdlib/sequences.md) | `seq`: what is pulled rather than built, and the input that does not fit in an array |

## Modules

| Page | Answers |
|---|---|
| [modules/README.md](./modules/README.md) | How `include` works, what a module value is, which built-in needs which capability |
| [modules/json.md](./modules/json.md) | `json.parse`, `x.json`, `json.pretty`, and the value mapping in both directions |
| [modules/math.md](./modules/math.md) | The complete member list, and what happens out of domain |
| [modules/time.md](./modules/time.md) | The clock capability, the `time` kind, parsing, `strftime`, duration arithmetic |
| [modules/io.md](./modules/io.md) | The eight io members — stdin, files, env — the filesystem a host must supply, and streaming a file that does not fit |
| [modules/http.md](./modules/http.md) | The server whose routes are closures, and the client whose answers are dicts |
| [modules/decimal.md](./modules/decimal.md) | Exact base-ten money: why a float is not one, the fourteen members, and the `+` that refuses |
| [modules/crypto.md](./modules/crypto.md) | Hex, base64 in both alphabets, sha256/sha1/md5, HMAC, and why the signature check is `crypto.equal` |
| [modules/url.md](./modules/url.md) | The eight keys of a parsed URL, the inverse that builds one, and the two encodings the `+` tells apart |
| [modules/custom.md](./modules/custom.md) | `include … from`, `export`, and how a path resolves |

## Embedding in Go

| Page | Answers |
|---|---|
| [embedding/README.md](./embedding/README.md) | `New`, `Compile`, `Run`, `RunResult`, `Eval`, the `Options` table, concurrency |
| [embedding/functions.md](./embedding/functions.md) | `Register`, `RegisterModule`, `SetGlobal`, `Unregister`, and the `Ctx` a host function gets |
| [embedding/filesystem.md](./embedding/filesystem.md) | `FS`, `Stdin`, `Env`, `ModuleLoader`, and why the host resolves every path |
| [embedding/values.md](./embedding/values.md) | Building and reading a `mzs.Value`, the `Kind` list, why no accessor panics |
| [embedding/errors.md](./embedding/errors.md) | `*mzs.Error` and its kinds, the sentinels, telling a script error from a limit |

## Reference

| Page | Answers |
|---|---|
| [reference/sandbox.md](./reference/sandbox.md) | What is never reachable, what the host grants, the limits every run is bounded by |
| [reference/limitations.md](./reference/limitations.md) | What the language does not have, and the syntax reserved as a parse error |
| [reference/verification.md](./reference/verification.md) | The four checks, the acceptance suite, coverage, CI |

## See also

* [../SPEC.md](../SPEC.md) — the normative specification; where it and these pages disagree, it wins
* [../examples/README.md](../examples/README.md) — 44 example programs, in order of difficulty
