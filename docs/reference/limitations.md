# Limitations and reserved syntax

What mzs does not have, what it will not have, and what is spelled as a parse error today so
it can be added later without breaking anyone.

## Not in the language

| Missing | What there is instead |
|---|---|
| classes, user-defined types, mixins | dicts, and `fn` called method-style through UFCS |
| `method_missing`, metaclasses, reflection | nothing |
| operator overloading, macros | nothing |
| string mutation | strings are immutable values — build a new one |
| `require`, `import`, `use`, `load` | `include` ([modules](../modules/README.md)) |
| threads, locks, atomics | `async fn` tasks (below) |
| `system`, `exec`, `spawn` | nothing, permanently |
| `eval` | nothing — a script cannot compile source |
| strict mode, compat mode, dialects | one semantics (below) |
| dependencies outside the Go stdlib | none; no cgo |
| a JIT | a tree-walking interpreter over a compiled AST |

Filesystem access is not out of scope but is not the language's either: `io` and
`include … from` both go through an interface the host implements
([sandbox](./sandbox.md)).

## Reserved syntax

Each of these is a diagnostic today so that the lexeme is free later.

| Written | Diagnostic |
|---|---|
| `@ivar = 1` | `syntax: '@' is reserved; instance variables do not exist in v0.1` |
| `class Foo { }` | `syntax: unexpected 'Foo' after statement` |
| `yield 1` | `syntax: unexpected 1 after statement` |
| `1 << 3` | `syntax: unexpected '<'` — the shifts are `shl` / `shr` |
| `x = <<~TEXT` | `syntax: unexpected '<'` |
| `x \|> f` | `syntax: '\|' is not an mzs operator; use bor(a, b), or '\|\|' for logical or` |
| `fn f(**kw) { }` | `syntax: expected a parameter name, found '**'` |
| `f(*xs)` | `syntax: unexpected '*'` |
| `a, *rest = [1,2,3]` | `syntax: unexpected '*'` |
| `s = "ab"; s[0] = "c"` | `type: cannot assign to an index of string` |

The destructuring rest element arrives together with `*splat` at a call site, or not at all.
Plain destructuring is not reserved — see [destructuring](../language/destructuring.md).

Multi-statement `try { … } else { … }` is reserved as future sugar, and today it is not an
error at all, because `{ … }` is a closure literal:

```
try { 1 } else { 2 }      # #<fn> — the closure itself, not 1
```

Grouping in a `try` is `( … ; … )`.

## One semantics

There is one lexer, one grammar and one evaluator. Nothing about what a program means
depends on a flag, and there is no `Options` field that changes it.

```
1 == "1"        # false
"2".int + 1     # 3
```

```sh
mzs -e '"2" + 1'   # -e:1:5: type: cannot add int to string
```

Anything that would otherwise be ambiguous is a fixed diagnostic rather than a guess, and
each one names its replacement:

```sh
mzs -e 'a and b'
# -e:1:3: syntax: 'and'/'or'/'not' are not mzs keywords; use '&&', '||', '!'
mzs -e 'unless c { 1 }'
# -e:1:1: syntax: 'unless' is not an mzs keyword; use 'if !(c)'
mzs -e '1...5'        # -e:1:2: syntax: '...' is not an mzs operator; use '..<'
mzs -e '"#{1}"'       # -e:1:2: syntax: string interpolation is "${x}"
mzs -e 'f {a: 1}'     # -e:1:3: syntax: a dict after a call is written f({a: 1})
mzs -e ':name'        # -e:1:1: syntax: mzs has no symbols; write "name"
```

The full table is in [diagnostics](../cli/diagnostics.md).

## Concurrency, not parallelism

`async fn` gives tasks; it does not give shared-memory parallelism. One goroutine of a Run
evaluates at a time, so there is no memory model to learn and no lock to forget.

```
$n = 0
async fn bump() { 1000.times.each { $n = $n + 1 } }
tasks = 8.times.map { bump() }
tasks.each { it.await }
$n                       # 8000 — every time, with no lock anywhere
```

Consequences worth knowing:

- Nothing is preemptive. A switch happens at three points and nowhere else: an `await`, a
  blocking host call (the `http` client and the `io` module are the ones in the box;
  `Ctx.Blocking` is how a host adds its own), and a task finishing.
- All tasks of a Run share one step counter, one deadline and one context, so a task cannot
  outlive the limits set for the Run.
- `MaxTasks` (default 64) bounds how many are live at once; `--tasks 0` forbids them.
- Tasks end when the Run does — `Run` never returns with one still going.

See [async](../language/async.md) and `examples/28_async_tasks.mzs`.

## See also

- [Sandbox and limits](./sandbox.md)
- [Verification](./verification.md)
- [Diagnostics and fix-its](../cli/diagnostics.md)
- [Operators](../language/operators.md)
