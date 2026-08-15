# Errors

`raise`, `assert`, `try … else … ensure …`, the dict a handler binds, the kind that says
what failed, and the failures no script may catch.

## `try` is an expression

```
try (1 + "two") else "fallback"                  # fallback
x = try raise("a") else 0; x                     # 0
```

The `else` side replaces the value of the failed side. To guard several statements, brace
them — or group them, which is the same thing without a scope:

```
try { a = 1; a + nil } else "-"                  # -
try (a := 1; a + nil) else "-"                   # -
```

## The braced form

Every clause takes a block as readily as an expression, and a block is a body: statements,
the last one is the value, and the braces are a scope.

```
n = try {
  s = "  42 ".trim
  raise("not a number") if !(s ~ /^\d+$/)
  s.int
} else {
  0
}
n                                                # 42
```

A name born inside those braces does not outlive them, and that is settled at compile time:

```
try { y = 1 } else 0; y
# name: undefined variable 'y'
```

Two things the braces do **not** change. A `{` that reads as a dict is still a dict, so the
form takes nothing away from the expression one:

```
try {a: 1} else 0                                # {"a":1}   — a dict operand
try {} else 0                                    # {}        — the empty dict
```

And a header's brace is already spoken for, so a braced `try` there needs parentheses:

```
if try { f() } else { 0 } { 1 }
# syntax: a braced 'try' cannot open a header; parenthesise it: if (try { … } else { … }) { … }
if (try { f() } else { 0 }) { 1 }
```

## `ensure`

`ensure` runs on every way out of the `try` that leaves the run alive: the value, an error
caught or not, and a `return`, `break` or `next` out of the body.

```
fn read(name) {
  h = open(name)
  try {
    h.contents
  } ensure {
    close(h)
  }
}
```

With no `else`, `try … ensure` catches nothing: it releases and lets the failure through,
which is what most releases actually want. The `ensure`'s own value is discarded — the
value of the whole expression is the body's, or the `else`'s when one caught.

```
try { 1 } ensure { 99 }                          # 1
try { raise("x") } ensure { 0 }                  # raise: x — the ensure ran, the error went on
try { raise("x") } else { "-" } ensure { 0 }     # -
```

The clause takes a block and nothing else, and it comes last:

```
try f() else 0 ensure g()
# syntax: expected '{' in ensure, found 'g'
```

An `ensure` that raises replaces whatever was pending, because a release that itself broke
is not something to swallow. `e` is not in scope inside it — the binder belongs to the
`else`.

What it does not do is outlive a limit. A timeout, the step budget, the depth limit, a
cancelled context and `exit` all end the run, and no `ensure` runs after them:

```
mzs --steps 5000 -e 'try { while true { } } ensure { println("released") }'
# -e:1:1: limit: step budget exceeded (5000 steps)     — and nothing was printed
```

## The error dict

`try X else (e) -> Y` binds `e` while `Y` is evaluated. Before a block the arrow is
optional, because the brace already separates the name from the handler:

```
try (1 + "two") else (e) -> e.json
# {"message":"cannot add string to int","kind":"type","line":1}

try { 1 + "two" } else (e) { e["kind"] }         # type
```

| Key | Value |
|---|---|
| `message` | the message, without position or kind |
| `kind` | the kind string from the table below |
| `line` | the line the error was raised on |
| `data` | present **only** when the error carries a payload |

```
e = try raise("x") else (e) -> e; e.keys         # ["message","kind","line"]
```

## `raise`

`raise(msg)` raises with kind `raise`; `raise(msg, kind)` names the kind instead.

```
try raise("boom") else (e) -> e.json
# {"message":"boom","kind":"raise","line":1}

try raise("no such order", "user") else (e) -> e["kind"]      # user
```

A dict reads three keys — `message`, `kind` and `data` — which is the spelling for an error
with a payload:

```
fn charge(o) { raise({message: "insufficient funds", kind: "billing", data: {short_by: 30}}) }
try charge("A-3") else (e) -> "declined: short by ${e["data"]["short_by"]}"
# declined: short by 30
```

A dict that names neither a `kind` nor a `data` key is a payload and is carried whole, with
its JSON as the message:

```
fn old(o) { raise({code: "limit", id: o}) }
try old("A-3") else (e) -> "declined (${e["data"]["code"]})"   # declined (limit)
```

A script may invent any kind it likes — `"user"`, `"billing"`, `"retryable"` — except the
four the runtime keeps, each of which is a claim only the runtime can make truthfully:

```
raise("x", "limit")
# argument: kind "limit" belongs to the runtime and cannot be raised by a script
#           ("syntax", "limit", "exit", "internal")
```

`raise()` with no argument is `argument: raise expects at least 1 argument(s), got 0`; a
third is `raise expects at most 2 argument(s), got 3`.

## Handling by kind, and passing on what is not yours

The kind is what a handler branches on. The arm that does not recognise a failure re-raises
it, and a re-raise keeps the file, line and stack of the *original* — the diagnostic names
the line that broke, not the handler that declined it.

```
fn attempt(o) {
  try {
    charge(o)
  } else (e) {
    match e["kind"] {
      "user"    -> "rejected: ${e["message"]}"
      "billing" -> "declined: short by ${e["data"]["short_by"]}"
      else      -> raise(e)
    }
  }
}
```

`message`, `kind` and `data` are read from the dict as it stands, so editing it before
re-raising works. A dict built by hand has no origin to keep and is positioned at the
`raise`.

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

The list is closed for the runtime: every failure the language itself produces is one of
these, stamped where it is born. That is what makes `match e["kind"]` a decision over a
known set instead of a search through English.

| Kind | Raised by |
|---|---|
| `syntax` | the parser (compile time) |
| `name` | an undefined variable, function or method |
| `type` | an operand or receiver of the wrong kind |
| `argument` | wrong argument count, or an argument list a builtin cannot use |
| `index` | a position out of range, a destructuring length mismatch |
| `key` | a key that is not in the dict — `fetch` |
| `zero-division` | integer `/` or `%` by zero |
| `regex` | a pattern that does not compile |
| `json` | `json.parse` on bad input, and a value `json` cannot encode |
| `http` | a transport failure, and a response over the size cap |
| `io` | a filesystem or stream failure, and a read over the size cap |
| `raise` | `raise` and `assert` |
| `limit` | a timeout, the step budget, the depth limit, a size cap |
| `exit` | `exit(code)` — the program saying it is done, not that it failed |
| `internal` | a recovered internal failure |

Plus whatever a script names for itself with `raise(msg, kind)`, which is why a host
switching on the kind wants a default branch.

```
try (7 / 0) else (e) -> [e["kind"], e["message"]]     # ["zero-division","divided by 0"]
7.0 / 0                                               # Infinity — float division is a value

try ({a: 1}.fetch("b")) else (e) -> e["kind"]         # key
try ([1, 2].insert(9, 3)) else (e) -> e["kind"]       # index
```

## What `try` never catches — and no `ensure` outlives

Limits are the host's contract with whoever runs the script, so a script cannot swallow one:
the `else` is ignored, no `ensure` runs, and the CLI exits 3.

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
expression, and every `ensure` on the way out runs as it passes.

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
