# Values

Every kind of value mzs has, how each one is written, and the rules — truthiness, rune
indexing, insertion order, copying — that differ from most other languages.

## The kinds

Nine kinds make up the value model; `type` reports three more.

| `type(x)` | Literal | Notes |
|---|---|---|
| `nil` | `nil` | falsy |
| `bool` | `true` `false` | `false` is the only falsy non-nil |
| `int` | `42` `0xff` `1_000` | `int64`; overflow promotes to `float` |
| `float` | `1.5` `1e9` | `float64` |
| `string` | `"a"` `'a'` | immutable UTF-8, rune-indexed |
| `regex` | `/re/i` | immutable |
| `array` | `[1, 2]` | mutable, reference semantics |
| `dict` | `{a: 1}` `{}` | insertion-ordered, mutable, reference semantics |
| `function` | `fn f(x) { … }` `{ (x) -> … }` | closures capture by reference |
| `range` | `1..5` `1..<5` | lazy; `is(r, "array")` is `true` |
| `time` | — | only when the host enables the clock ([../modules/time.md](../modules/time.md)) |
| `task` | — | the result of calling an `async fn` ([./async.md](./async.md)) |

```
type(1..5)        # range
is(1..5, "array") # true
```

## Numbers

```
[42, 1_000_000, 0xff, 0b1010, 0o17]   # [42,1000000,255,10,15]
0xff + 0b1010 + 0o17                  # 280
1e9                                   # 1000000000.0
0.1 + 0.2                             # 0.30000000000000004
```

Underscores are separators and are stripped. A `.` after digits starts a float only if the
next rune is a digit, so `1.str` is a method call on `1` and `1.2.str` is one on `1.2`.
There are no `Inf` or `NaN` literals.

## Strings

```
"escapes: \n \t \u{1F600} \x41 \$"
'raw: \n stays two characters'
"interpolated: ${1 + 1} and $__name"     # with -v __name=Ivan: interpolated: 2 and Ivan
```

`'…'` is raw — only `\'` and `\\` are escapes, which is what makes it the form for regex
source. `${…}` holds any expression including a local; a bare `$name` is always the host
global ([./host-variables.md](./host-variables.md)). Full detail in [./strings.md](./strings.md).

**Everything is runes, never bytes.**

```
"привет".len        # 6
"привет"[3]         # в
"привет"[1, 2]      # ри   (i, n) is a substring
"abc"[-1]           # c    negative counts from the end
inspect("abc"[10])  # nil  out of range is nil, not an error
```

## Collections

Brackets hold an array, braces hold a dict — the same split JSON uses, so a JSON document
is already a valid mzs literal.

```
[]              # empty array
[1, "two", 3.0]
{}              # empty dict
{name: "Ivan", price: 1500}
{"name": "Ivan"}         # the same dict; a bare key becomes a string
k = "key"; {(k): 1}      # a computed key is parenthesised
```

Keys in brackets are a diagnostic naming the replacement, never a silent reading:

```
[a: 1]                   # syntax: a dict is written {a: 1}
[:]                      # syntax: the empty dict is written {}
```

A dict is read in operand position, because elsewhere the brace is already spoken for:
after an `if`/`while`/`for`/`fn` header it opens the body, and after a call it opens the
trailing closure. Both cases have a fix-it:

```
status = if ready { {code: 200} } else { {code: 503} }   # body, then the dict inside it
f(a: 1)                  # a dict argument — `f {a: 1}` is a diagnostic
xs.each { }              # still an empty closure; an empty closure value is { nil }
```

Dicts keep **insertion order**, both when iterated and in JSON:

```
d = {b: 1, a: 2}; d["c"] = 3; d.json      # {"b":1,"a":2,"c":3}
```

Keys may be `nil`, a bool, a number, a string, a regex or a time. `1` and `1.0` are the same
key; an array, dict, function or range key is an error.

```
d = {}; d[1] = "a"; d[1.0] = "b"; d      # {"1":"b"}
d = {}; d[[1]] = 1                       # type: dict key must be hashable, got array
d = {}; d[1..2] = 1                      # type: dict key must be hashable, got range
```

## Functions

```
double = { (n) -> n * 2 }
{ it * 2 }(21)            # 42
type(fn f(x) { x })       # function
```

A closure with no parameter list binds `it`. See [./functions.md](./functions.md).

## Truthiness

`nil` and `false` are falsy. **Everything else is truthy**, including `0`, `0.0`, `""`, `[]`
and `{}`.

```
[nil, false, 0, 0.0, "", [], {}].filter { bool(it) }.len     # 5
if 0 { "truthy" } else { "falsy" }                            # truthy
```

This is why `"привет".index(/при/)` returning `0` — a match at position 0 — is still a
useful condition.

What those two falsy kinds answer as receivers, and why the test is `x == nil` and never a
`nil?` predicate, is the *nil and bool receivers* table in [../stdlib/core.md](../stdlib/core.md).

## Equality and ordering

```
1 == 1.0                        # true   numbers compare numerically
"2" == 2                        # false  no coercion, never an error
[1, [2]] == [1, [2]]            # true   deep, order significant
{a: 1, b: 2} == {b: 2, a: 1}    # true   dict order is not significant
"apple" <=> "banana"            # -1
inspect(1 <=> "a")              # nil    incomparable
1 < "2"                         # type: cannot compare int with string
```

`==` with a regex literal on either side is a compile error — use `~`
([./operators.md](./operators.md)).

## Introspection

```
type("a")           # string
is(1, "int")        # true
is(1.0, "int")      # false
is(1..5, "array")   # true
```

## Copying

Arrays and dicts are references; `dup` makes a **shallow** copy and is the identity for
everything else.

```
a = [1,2]; b = a;     b.push(3); a         # [1,2,3]
a = [1,2]; b = a.dup; b.push(3); a         # [1,2]
a = [[1]]; b = a.dup; b[0].push(2); a      # [[1,2]]  the inner array is shared
```

Strings are immutable, so they never need copying.

## See also

- [./operators.md](./operators.md) — what the operators do to these kinds
- [./strings.md](./strings.md) — literals, escapes, interpolation
- [../stdlib/README.md](../stdlib/README.md) — the methods each kind answers
- [`examples/01_values_and_operators.mzs`](../../examples/01_values_and_operators.mzs) — this page as a runnable table
