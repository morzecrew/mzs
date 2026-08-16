# Values

Every kind of value mzs has, how each one is written, and the rules — truthiness, rune
indexing, insertion order, copying — that differ from most other languages.

## The kinds

Nine kinds make up the value model; `type` reports four more, plus whatever names your own
`record` declarations add ([#records](#records)).

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
| `seq` | — | a lazy sequence; `is(s, "array")` is **false** ([../stdlib/sequences.md](../stdlib/sequences.md)) |

```
type(1..5)               # range
is(1..5, "array")        # true
type((1..5).seq)         # seq
is((1..5).seq, "array")  # false
```

A range and a seq are both lazy and they answer `is("array")` differently on purpose: a
range can be materialised on demand under the collection cap, and a seq is the value that
refuses to be — so code that takes an array is never handed one by accident.

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
{1 -> "A", 1.5 -> "B"}   # `->` is the separator a key that is not a string takes
{name -> "Ivan"}         # the same as `name:` — a bare word is the string either way
```

Keys in brackets are a diagnostic naming the replacement, never a silent reading:

```
[a: 1]                   # syntax: a dict is written {a: 1}
[:]                      # syntax: the empty dict is written {}
```

A dict is read in operand position, because elsewhere the brace is already spoken for:
after an `if`/`while`/`for`/`fn` header it opens the body, in a `try` clause it opens that
clause's block, and after a call it opens the trailing closure. Each case has a fix-it:

```
status = if ready { {code: 200} } else { {code: 503} }   # body, then the dict inside it
f({a: 1})                # a dict argument — `f {a: 1}` and `f(a: 1)` are diagnostics
xs.each { }              # still an empty closure; an empty closure value is { nil }
```

Dicts keep **insertion order**, both when iterated and in JSON:

```
d = {b: 1, a: 2}; d["c"] = 3; d.json      # {"b":1,"a":2,"c":3}
```

Keys may be `nil`, a bool, a number, a string, a regex or a time. `1` and `1.0` are the same
key; an array, dict, function, range, task or seq key is an error.

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

## Records

`record` names a **shape** over the dict you already had. It is not a new kind and not a
class: the value it builds is a dict, and `json`, `keys`, `dig`, `merge`, `==` and every
other dict row go on meaning exactly what they meant.

```
record Money(amount, currency = "RUB")

m = Money(1500, "USD")
m.amount                # 1500 — a field, by name
type(m)                 # "Money"
m.is("dict")            # true — it never stopped being one
m.is("Money")           # true
m.json                  # {"amount":1500,"currency":"USD"}
```

A field list is a parameter list, so a call is an ordinary call
([./functions.md](./functions.md)): a default is filled at each call, a field may be given
by name, and a missing one is the arity error.

```
Money(700)                             # {"amount":700,"currency":"RUB"}
Money(currency = "EUR", amount = 3)    # a field by name
Money()                                # argument: Money expects 2 argument(s), got 0
```

The name is an ordinary binding holding the constructor — pass it, store it, call it
through a variable — and it hoists like a `fn`, so a shape may be used above the line that
declares it.

**Matching on the shape.** A bare record name in a `match` arm asks whether the subject was
built by that declaration ([./control-flow.md](./control-flow.md)):

```
fn describe(x) {
  match x {
    Money -> "${x.amount} ${x.currency}"
    else  -> "a plain ${type(x)}"
  }
}
```

Identity belongs to the declaration, not to the spelling: two `record Money(…)` statements
are two shapes.

**The label is a label, not content.** Equality, `hash`, `json`, `keys` and iteration all
see a plain dict, so a record and a hand-written dict with the same entries are equal.
What carries it forward is the copy a record makes of itself:

```
Money(1500) == {amount: 1500, currency: "RUB"}   # true
type(Money(1500).dup)                            # Money
type(Money(1500).merge({amount: 900}))           # Money — the with-update
type(Money(1500).filter { (k, _) -> k == "amount" })   # dict — it may no longer fit
```

A record is mutable like any dict: `m["amount"] = 2` writes it in place and the label
survives.

**Two things to know.** A field may be named after a stdlib method — `record Page(len, …)`
— and on that shape the field wins, so `p.len` is the field and `len(p)` is still the entry
count; the compiler warns once, at the declaration. And the `.field` spelling is resolved
where the file compiles, so a shape declared in an **included module** is read with
`m["field"]`; `type(m)` and `m.is("Money")` work there as they do anywhere, because they
ride on the value.

What a record deliberately is not: a class. No inheritance, no methods on the type, no
`self`. Functions over a shape stay free functions and are reached both ways —
`total(cart)` and `cart.total` are one thing ([./functions.md](./functions.md)).

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
- [`examples/37_records.mzs`](../../examples/37_records.mzs) — records end to end
