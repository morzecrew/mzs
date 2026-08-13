# mzs

**A small scripting language for text, data and one-liners — and a Go library you can embed.**

[![CI](https://github.com/morzecrew/mzs/actions/workflows/ci.yml/badge.svg)](https://github.com/morzecrew/mzs/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/morzecrew/mzs/graph/badge.svg)](https://codecov.io/gh/morzecrew/mzs)

```sh
mzs -e '"  HELLO ".lower.trim'                  # hello
mzs -e '(1..10).filter { it.even }.sum'         # 30
mzs -e '"a,b,a".split(",").tally' --json        # {"a":2,"b":1}
```

Four rules carry the whole syntax:

* **Everything is an expression.** `if`, `while`, `for` and `match` all have a value, and the last
  expression is the result.
* **`{ … }` is a closure, unless it is a dict.** Position decides and nothing else — `if`, `while`,
  `for`, `fn` and `match` arms simply call it for you; in operand position `{name: "Ivan"}` is a dict.
  So `{}` is the empty dict there, while `xs.each { }` after a call is still the empty closure —
  an empty closure *value* is `{ nil }`.
* **`[ … ]` is always an array, `{ key: … }` always a dict.** `[1, 2]`, `[]`, `{name: "Ivan"}`,
  `{}` — one spelling each, which is JSON's, so a JSON document pastes in as source unedited.
* **`x.f(y)` is exactly `f(x, y)`.** One flat namespace, so every function is also a method — your
  own included — and chains read left to right.

Sixteen keywords, exactly one name per operation, no implicit conversions, and an ambiguity is a
diagnostic with a fix-it rather than a silent reading. The implementation is one Go module with
**no dependencies** — no cgo, no subprocesses, no code generation.

📚 [Documentation](docs/README.md) · 📐 [Specification](SPEC.md) · 🧪 [Examples](examples/README.md)

---

## Install

```sh
git clone https://github.com/morzecrew/mzs.git && cd mzs
go install ./cmd/mzs           # → $(go env GOPATH)/bin/mzs
mzs --version                  # mzs 2.0.0
```

Go 1.26 or newer, and nothing else. `CGO_ENABLED=0 go build ./cmd/mzs` gives one fully static
binary you can copy anywhere.

## Run

| Command | What it does |
|---|---|
| `mzs script.mzs a b` | run a file; the arguments arrive in `$ARGV` |
| `mzs -e 'expr'` | a one-liner; its value is printed for you |
| `cat data \| mzs -n -e '$_…'` | run the program once per input line, the line in `$_` |
| `cat script.mzs \| mzs` | take the program from stdin |
| `mzs` | the REPL |

→ [every flag](docs/cli/README.md) · [pipes and line mode](docs/cli/input.md)

## Quick start

```ruby
include json

orders = [
  {id: 1, user: "ivan", total: 1500},
  {id: 2, user: "olga", total: 4200},
  {id: 3, user: "ivan", total:  800},
]

report = orders
  .group_by { it["user"] }
  .map { (user, rows) -> {user: user, orders: rows.len, total: rows.sum { it["total"] }} }
  .sort_by { -it["total"] }

for r in report {
  say("${r["user"]}: ${r["orders"]} orders, ${r["total"]} total")
}

say(report.json)
```

```sh
$ mzs report.mzs
olga: 1 orders, 4200 total
ivan: 2 orders, 2300 total
[{"user":"olga","orders":1,"total":4200},{"user":"ivan","orders":2,"total":2300}]
```

## The language in one screen

```
n = 42        0xff   1_000_000   1.5   1e9        # int, float
s = "hello"   'raw \n stays two characters'       # string, indexed in runes
re = /hello|hi/i                                  # regex
xs = [1, 2, "three"]                              # array
d  = {name: "Ivan", price: 1500}                  # dict, insertion-ordered
f  = { (x) -> x * 2 }                             # closure
```

```
if x > 3 { "big" } else { "small" }               # every form has a value
while x < 5 { x += 1 }
for k, v in d { say("${k}=${v}") }
x = 1 if ready                                    # statement modifiers

intent = match text {                             # instead of an if/else-if ladder
  in ["yes", "sure"]  -> "confirm"
  /\bhelp\b/i         -> "support"
  in 1..5             -> "small"
  else                -> "unknown"
}
```

```
fn greet(name, greeting = "Hello") { "${greeting}, ${name}!" }
fn sum_all(*nums) { nums.sum }
xs.map { it * 2 }                                 # `it` when you name no parameter
a, b = [b, a]                                     # destructuring: a swap needs no temporary

v = try json.parse(s) else {}                    # an error is a value you decide about
raise("not allowed")     assert(x > 0, "x > 0")
```

→ [the language, page by page](docs/language/README.md)

## What you can write in it

| | Example | Docs |
|---|---|---|
| **Text and Unicode** | `"  ПРИВЕТ ".lower.trim.capitalize` → `Привет` | [strings](docs/stdlib/strings.md) |
| **Regex, two engines** | `"нужна CRM" ~ /\bcrm\b/i` — Unicode `\b`, rune indices, lookahead | [regex](docs/language/regex.md) |
| **Collections** | `xs.group_by { it["k"] }.map { … }.sort_by { -it["n"] }` | [arrays](docs/stdlib/arrays.md) · [dicts](docs/stdlib/dicts.md) |
| **JSON both ways** | `json.parse(s).dig("a", 1)` · `x.json` | [json](docs/modules/json.md) |
| **Files, stdin, env** | `io.lines.filter { it ~ /ERROR/ }.len` | [io](docs/modules/io.md) |
| **HTTP client and server** | `http.serve(":8080", {"GET /hi/{name}": { (req) -> req["params"]["name"] }})` | [http](docs/modules/http.md) |
| **Concurrency** | `urls.map { fetch(it) }.map { it.await }` | [async](docs/language/async.md) |
| **Modules of your own** | `include cart from "./cart.mzs"` | [modules](docs/modules/custom.md) |
| **Shell pipelines** | `cat access.log \| mzs -n -e '$_.split(" ")[0]'` | [line mode](docs/cli/input.md) |
| **Embedding in Go** | `in.Eval(ctx, "$price.int + 1200", vars)` | [embedding](docs/embedding/README.md) |

## Embed it in Go

```go
in := mzs.New(mzs.Options{Timeout: time.Second})

prog, err := in.Compile("rule#12", `$text.lower.trim == "yes"`)
if err != nil { return err }

v, err := in.Run(ctx, prog, map[string]mzs.Value{"$text": mzs.Str("  YES ")})
// v.Truthy() == true
```

A `*Program` is immutable and goroutine-safe: compile once, run it from as many goroutines as you
like, each run isolated in its own frame. Host values are **bound, not substituted into source**, so
a quote, a space or an emoji inside a value can never reach the parser.

One precompiled one-liner with bound variables, `go test -bench Condition` on a 16-core laptop:

```
BenchmarkCondition/equality               835 ns/op    1288 B/op   10 allocs/op
BenchmarkCondition/lower_trim_equality   1617 ns/op    1616 B/op   16 allocs/op
BenchmarkCondition/re2_regex             1527 ns/op    1489 B/op   14 allocs/op
BenchmarkCondition/backtracking_regex    1502 ns/op    1513 B/op   16 allocs/op
BenchmarkCondition/match_ladder          1927 ns/op    1976 B/op   22 allocs/op
```

→ [the Go API](docs/embedding/README.md) · [your own functions](docs/embedding/functions.md)

## Safe by default

A script cannot start a process, read a file, learn the time or produce a random number unless the
host hands that capability over. There is no `eval`, no `require`, no way to compile new source at
run time. Every run is bounded:

| Setting | Default | Bounds |
|---|---|---|
| `Timeout` | 1 s | wall-clock time of one run |
| `StepBudget` | 5,000,000 | interpreter steps |
| `MaxDepth` | 200 | call depth |
| `MaxTasks` | 64 | concurrent `async fn` tasks |
| `MaxCollection` | 1,000,000 | elements materialised by one operation |
| `MaxStringBytes` | 8 MiB | one constructed string |
| `RegexSteps` | 200,000 | backtracking budget per match |

The checks sit inside the node-walking loop, so `while true { }` is interrupted mid-iteration, and
no limit is catchable by `try`. A panic anywhere inside becomes an ordinary error at the `Run`
boundary: a script cannot bring its host down.

→ [sandbox and limits](docs/reference/sandbox.md)

## Documentation

| Section | What is in it |
|---|---|
| [Getting started](docs/getting-started/README.md) | install, a first program, the REPL, a one-liner cheat sheet |
| [CLI](docs/cli/README.md) | flags, exit codes, pipes and line mode, diagnostics |
| [Language](docs/language/README.md) | values, operators, control flow, functions, errors, async, regex |
| [Standard library](docs/stdlib/README.md) | every built-in function, by kind |
| [Modules](docs/modules/README.md) | `json`, `math`, `time`, `io`, `http`, and your own |
| [Embedding in Go](docs/embedding/README.md) | the host API: values, functions, capabilities, errors |
| Reference | [sandbox and limits](docs/reference/sandbox.md) · [limitations](docs/reference/limitations.md) · [verification](docs/reference/verification.md) |

The normative description is [`SPEC.md`](SPEC.md); the runnable one is
[`examples/`](examples/README.md) — 34 programs, from the value model to a maze solver, an HTTP
service and `async fn`.

## Verification

```sh
gofmt -l .          # silent
go vet ./...
go test -race ./...
for f in examples/*.mzs; do mzs --check --time "$f"; done
```

The same four commands run on every push ([CI](.github/workflows/ci.yml)), with coverage going to
[Codecov](https://codecov.io/gh/morzecrew/mzs).

## License

MIT — see [`LICENSE`](LICENSE).
