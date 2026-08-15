# Core functions

The functions that are not tied to one kind: output, sizes and kinds, conversions, aggregates, formatting, and errors.

Every row is also a method (`len(x)` ≡ `x.len`); see [README.md](./README.md). The one
exception is `defined`, which must stay a call because it never evaluates its operand.

## Output

| Name | Signature | Does | Example → output |
|---|---|---|---|
| `print` | `print(*args) -> nil` | writes `str` of each arg, no separator, no newline | `print("a", "b")` → `ab` |
| `println` | `println(*args) -> nil` | adds `\n` after each arg; `println()` writes one `\n`; an Array prints one element per line | `println([1, 2])` → `1\n2\n` |
| `debug` | `debug(*args) -> any` | writes `inspect` + `\n`, returns the first arg | `debug("x")` → `"x"` |

```
debug("a", "b").upper    # writes "a" and "b" on their own lines, evaluates to A
```

Output goes to the interpreter's stdout, not to `os.Stdout` directly — a host can capture it.

## Size and kind

| Name | Signature | Does | Example → value |
|---|---|---|---|
| `len` | `len(x) -> int` | runes of a String, elements of Array/Dict/Range; `nil` → 0 | `len("привет")` → `6` |
| `empty` | `empty(x) -> bool` | `len(x) == 0` | `[].empty` → `true` |
| `type` | `type(x) -> string` | kind name | `type(1.5)` → `float` |
| `is` | `is(x, name: string) -> bool` | kind test; an unknown name raises | `is(1, "int")` → `true` |

A Range answers `true` to `is(r, "array")` while `type(r)` stays `"range"`.
`1.is("Integer")` raises `argument: is: unknown type name "Integer"` rather than returning false.

## Conversions

| Name | Signature | Does | Example → value |
|---|---|---|---|
| `str` | `str(x) -> string` | `nil`→`""`, Float keeps a `.`, Array/Dict→JSON | `str(2.0)` → `2.0` |
| `int` | `int(x) -> int` | leading sign+digits, base 10, **never raises** | `"12abc".int` → `12` |
| `float` | `float(x) -> float` | leading number, never raises | `"3.7kg".float` → `3.7` |
| `bool` | `bool(x) -> bool` | truthiness: only `nil` and `false` are falsy | `bool("")` → `true` |
| `array` | `array(x) -> array` | Range materialises, Dict → `[k,v]` pairs, `nil` → `[]`, else wraps | `(1..3).array` → `[1,2,3]` |
| `dict` | `dict(x) -> dict` | from an Array of `[k, v]` pairs | `[[1,2]].dict` → `{"1":2}` |
| `json` | `json(x) -> string` | compact JSON, keys in insertion order | `{a: 1}.json` → `{"a":1}` |
| `inspect` | `inspect(x) -> string` | like `str`, but strings are quoted and `nil` → `nil` | `inspect("да")` → `"да"` |

```
"0x1f".int      # 0 — base 10 only
"1_000".int     # 1000
int(-1.9)       # -1 — truncates toward zero
"abc".int       # 0
```

`int` never raising is what makes `$price.int + 1200` safe when `$price` is unset.
After `include json` the bare `json(x)` call is the module; the function is only `x.json`.

## nil and bool receivers

| Receiver | `str` | `int` | `float` | `json` | `array` | `inspect` |
|---|---|---|---|---|---|---|
| `nil` | `""` | `0` | `0.0` | `null` | `[]` | `nil` |
| `true` | `true` | `1` | `1.0` | `true` | `[true]` | `true` |
| `false` | `false` | `0` | `0.0` | `false` | `[false]` | `false` |

`len` → `0`, `empty` → `true`, `bool`, `dup` (identity) and `hash` answer as well; past
those and the any-receiver rows (`type`, `is`, `tap`, `pipe`, `debug`) nothing does —
`nil.upper` is `name: undefined method 'upper' for nil`.

There is no `nil?` predicate: write `x == nil`.

```
x = nil; x == nil          # true
x.nil                      # name: undefined method 'nil'
x.nil?                     # syntax: '?' is not part of an identifier
inspect(nil.str)           # ""    only inspect spells nil as "nil"
inspect(nil?.upper.len)    # nil   ?. stops the chain instead
```

## Copies, hashing, taps

| Name | Signature | Does | Example → value |
|---|---|---|---|
| `hash` | `hash(x) -> int` | FNV-1a, stable across runs | `hash("a")` → `1463908424326387805` |
| `dup` | `dup(x) -> any` | shallow copy of Array/Dict, identity otherwise | see below |
| `tap` | `tap(x) { (v) -> … } -> any` | runs the closure, returns `x` | `5.tap { println("saw ${it}") }` → `5` |
| `pipe` | `pipe(x) { (v) -> … } -> any` | runs the closure, returns **its** value | `" 42 ".pipe { it.trim.int }` → `42` |

```
x = [1]; y = dup(x); y.push(2); x.len    # 1 — y is a separate array
```

## Constructors

| Name | Signature | Does | Example → value |
|---|---|---|---|
| `regex` | `regex(pattern: string, flags: string = "") -> regex` | compiles at runtime, cached | `regex('\d+', "i").str` → `/\d+/i` |
| `range` | `range(a: int, b: int = nil, step: int = 1) -> array` | half-open, an Array not a Range | `range(3)` → `[0,1,2]` |

```
range(1, 7, 2)     # [1,3,5]
range(5, 1, -1)    # [5,4,3,2]
range(3, 0)        # []
```

An invalid pattern raises at the call, not at parse time:

```
regex("(", "")     # regex: cannot compile /(/: missing closing ): `(?m)(`
```

## Numbers and aggregates

| Name | Signature | Does | Example → value |
|---|---|---|---|
| `sum` | `sum(xs: array) -> number` | numeric sum; empty → `0` | `sum(1..4)` → `10` |
| `min` / `max` | `(xs: array \| *args) -> any` | by `<=>`; empty → `nil` | `max(1, 2, 3)` → `3` |
| `abs` | `abs(x: number) -> number` | | `(-2).abs` → `2` |
| `round` | `round(x: number, digits: int = 0) -> number` | half away from zero; `digits == 0` → Int | `round(1.256, 2)` → `1.26` |
| `ceil` / `floor` | `(x: number, digits: int = 0) -> number` | | `floor(1.239, 2)` → `1.23` |
| `sort` | `sort(xs: array) [{ (a, b) -> int }] -> array` | stable, new array | `[3,1,2].sort` → `[1,2,3]` |

```
round(-2.5)                          # -3
[3,1,2].sort { (a, b) -> b <=> a }   # [3,2,1]
sort([1, "a"])                       # type: sort: string and int are not comparable
```

The `sort` closure is a comparator returning a `<=>`-style int, not a key extractor.

## format

`format(fmt: string, *args) -> string`. Verbs `%s %d %i %f %g %e %x %X %o %b %c %% %j`,
flags `- + 0 space #`, width, `.precision`, `*` for a width taken from the arguments, and
`%<name>s` / `%{name}` when the first argument is a Dict.

```
format("%.2f", 1.5)          # 1.50
format("%05d|%s", 42, "x")   # 00042|x
format("%-6s|", "ab")        # ab    |
format("%*d", 5, 42)         # "   42"
format("%j", {a: 1})         # {"a":1}
format("%<n>s", {n: "x"})    # x
"%s-%d" % ["a", 1]           # a-1
```

An unknown verb or a missing argument raises: `format("%q", 1)` → `argument: unknown format verb '%q'`.

## Errors and introspection

| Name | Signature | Does | Example |
|---|---|---|---|
| `raise` | `raise(msg: any) -> never` | raises a script error; a Dict is attached under the caught error's `"data"` key | `raise("bad")` |
| `assert` | `assert(cond: any, msg: string = "assertion failed") -> nil` | raises when falsy | `assert(x.len == 1, "bad name")` |
| `defined` | `defined(name) -> bool` | is the identifier or `$var` bound; never evaluates its operand | `defined($price)` |
| `exit` | `exit(code: int = 0) -> never` | ends the run with that status; `try` never catches it | `exit(1)` |

```
try raise("bad") else "caught"                       # caught
try raise({code: 42}) else (e) -> e["data"]["code"]  # 42
assert(false, "nope")                                # raise: nope
defined(zzz)                                         # false
```

`assert(0)` does not raise — `0` is truthy.

`exit` stops the program where it stands and hands the status to whoever ran it. Nothing
after it runs, no diagnostic is printed, and `try` does not catch it — it is not a failure,
it is the program saying it is done.

```sh
$ mzs -e 'println("done"); exit(2); println("never")'
done
$ echo $?
2
```

Inside an `async fn` it travels like any other error the task produced: the `await` is where
it reaches the program, and a task nobody awaited is reported on stderr instead ([async](../language/async.md)).

The code is a status, so it is an integer from 0 to 255: `exit(256)` and `exit("x")` are
refused where they are written. Inside an embedder nothing touches the process — the Run
ends and `mzs.ExitCode(err)` reports the number, which the host is free to ignore. In `-n`
line mode the first line that exits ends the whole run, and in the REPL `exit` leaves the
session (see [the REPL](../getting-started/repl.md#ctrl-c)).

## Host-gated

These three exist only when the host enables them; otherwise the name is undefined.

| Name | Signature | Needs | Example → value |
|---|---|---|---|
| `rand` | `rand(n: int = 0) -> number` | `--rand` | `rand(6) < 6` → `true` |
| `uuid` | `uuid() -> string` | `--rand` | `uuid().len` → `36` |
| `now` | `now() -> time` | `--time` | `type(now())` → `time` |

`rand()` with no argument returns a float in `[0, 1)`; `rand(n)` an int in `0..n-1`.
Without the flag the name does not resolve at all: `name: undefined function 'rand' (did you
mean 'band'?)`.

## See also

- [strings.md](./strings.md) — the string rows these conversions feed
- [numbers.md](./numbers.md) — per-number rounding, predicates and bit functions
- [../language/errors.md](../language/errors.md) — `try`/`else` and what `raise` produces
- [../reference/sandbox.md](../reference/sandbox.md) — why `rand`, `uuid` and `now` are gated
