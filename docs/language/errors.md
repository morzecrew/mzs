# Errors

`raise`, `assert`, `try … else …`, the dict a handler binds, and the failures no script may
catch.

## `try` is an expression

```
try (1 + "two") else "fallback"                  # fallback
x = try raise("a") else 0; x                     # 0
```

The `else` side replaces the value of the failed side. To guard several statements, group
them:

```
try (a := 1; a + nil) else "-"                   # -
```

## The error dict

`try X else (e) -> Y` binds `e` while `Y` is evaluated.

```
try (1 + "two") else (e) -> e.json
# {"message":"cannot add string to int","kind":"type","line":1}
```

| Key | Value |
|---|---|
| `message` | the message, without position or kind |
| `kind` | the kind string from the table below |
| `line` | the line the error was raised on |
| `data` | present **only** when `raise` was given a dict |

```
e = try raise("x") else (e) -> e; e.keys         # ["message","kind","line"]
```

## `raise`

`raise` takes exactly one argument: a message, or a dict that is carried through as `data`.

```
try raise("boom") else (e) -> e.json
# {"message":"boom","kind":"raise","line":1}

fn charge(o) { raise({code: "limit", id: o}) if true; "ok" }
try charge("A-3") else (e) -> "declined (${e["data"]["code"]})"    # declined (limit)
```

The message of a dict raise is that dict rendered as JSON. `raise()` with no argument is
`argument: raise expects at least 1 argument(s), got 0`; a second argument is
`raise expects at most 1 argument(s), got 2`.

Uncaught, a `raise` reaches the host and, from the CLI, prints and exits 1:

```
raise("out of stock")
# -e:1:1: raise: out of stock
#   raise("out of stock")
#   ^
```

## `assert`

```
try assert(false, "nope") else (e) -> e.json
# {"message":"nope","kind":"raise","line":1}

try assert(1 == 2) else (e) -> e["message"]      # assertion failed
inspect(assert(true, "ok"))                      # nil
```

`assert` is an ordinary raise with a default message; it is not disabled in any mode.

## Error kinds

| Kind | Raised by |
|---|---|
| `syntax` | the parser (compile time) |
| `name` | an undefined variable, function or method |
| `type` | an operand or receiver of the wrong kind |
| `argument` | wrong argument count, or an argument list a builtin cannot use |
| `index` | a missing key, a destructuring length mismatch |
| `zero-division` | integer `/` or `%` by zero |
| `regex` | a pattern that does not compile |
| `raise` | `raise` and `assert` |
| `limit` | a timeout, the step budget, the depth limit, a size cap |
| `exit` | `exit(code)` — the program saying it is done, not that it failed |
| `internal` | a recovered internal failure |

```
try (7 / 0) else (e) -> [e["kind"], e["message"]]     # ["zero-division","divided by 0"]
7.0 / 0                                               # Infinity — float division is a value
```

## What `try` never catches

Limits are the host's contract with whoever runs the script, so a script cannot swallow one.
Each of these ignores the `else` and exits 3 from the CLI:

```
try (while true { }) else "caught"
# -e:1:1: limit: step budget exceeded (5000000 steps)

try ((1..1e9).array) else "caught"
# -e:1:15: limit: collection too large: 1000000000 elements exceeds the limit of 1000000

try (fn r() { r() }; r()) else "caught"
# -e:1:15: limit: max call depth exceeded (200)
```

With the step budget disabled the wall clock is what ends the run —
`mzs --steps 0 -t 300ms -e 'try (while true { }) else "caught"'` prints
`limit: execution timed out after 300ms`. A cancelled context behaves the same way.

`exit(code)` is not catchable either, for the opposite reason: it is not a failure at all,
so there is nothing for an `else` to stand in for. It ends the run with the status it names
and prints nothing.

```
try exit(2) else "caught"     # the program ends; echo $? is 2
```

Compile-time errors are not catchable either, for a different reason: the program never runs.

```
try ("x".nope) else "caught"
# -e:1:10: name: undefined method 'nope'; did you mean 'none'?
```

The same failure discovered at run time **is** catchable, because the receiver's kind is only
known then:

```
fn f(x) { x.upper }; try f(1) else (e) -> e.json
# {"message":"undefined method 'upper' for int","kind":"name","line":1}
```

## Propagating and nesting

An error unwinds through call frames until a `try` catches it; handlers nest like any other
expression.

```
fn a() { raise("deep") }; fn b() { a() }
try b() else (e) -> e["message"]                 # deep

try (raise("inner")) else (e) -> try raise("outer: ${e["message"]}") else (e2) -> e2["message"]
# outer: inner
```

## Not every failure deserves a raise

`??` covers an absent value, `?.` an absent chain, and `.int`/`.float` never fail — reach for
`try` when a call itself fails.

```
cfg = {http: {port: 8080}}
[cfg.dig("http", "host") ?? "0.0.0.0", inspect(cfg["proxy"]?.len), try cfg.fetch("proxy") else "(none)"]
# ["0.0.0.0","nil","(none)"]
```

Validation is a report, not an exception: collect the failures instead of raising on the
first one — see [`examples/08_errors_and_validation.mzs`](../../examples/08_errors_and_validation.mzs).

## See also

- [Diagnostics](../cli/diagnostics.md) — the error output format and `--check`
- [Sandbox and limits](../reference/sandbox.md) — every limit that produces a `limit` error
- [Operators](./operators.md) — `??` and `?.`
- [Embedding: errors](../embedding/errors.md) — the Go `*Error` and the sentinel errors
