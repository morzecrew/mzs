# Destructuring

One shape rule written in three places: an assignment, a `match` arm, and the two-variable `for`.

## The assignment

```
a, b = [1, 2]                 # a=1 b=2
[x, y] = [1, 2]               # the same thing, brackets written
lo, hi = 1..2                 # a range has positions too
first, [second, third] = [1, [2, 3]]   # nesting is recursive
a, b = [b, a]                 # a swap needs no temporary
```

Only an **Array** or a **Range** has positions. The right side is evaluated once, and the
positions are filled left to right.

```
user, host = "ivan@example.ru".split("@")
host                          # example.ru

lows, highs = [120, 90, 160, 140].partition { it < 130 }
highs                         # [160,140]
```

## Targets

A target is a name, a `$var`, an index, a record's field, or a nested `[ … ]`.

```
d = {row: 0, col: 0}
d["row"], d["col"], $sold = [12, 4, true]
d.json                        # {"row":12,"col":4}

record Point(x, y)
p = Point(0, 0)
p.x, p.y = [3, 4]             # a field is written as it is read
```

Anything else is rejected at compile time:

```
[3, b] = [1, 2]
# -e:1:2: syntax: cannot assign to this expression: a destructuring target is a name,
#         a $var, an index, a record field or a nested [ … ]
```

Because the right side is read exactly once, a target may write into the array being taken
apart:

```
xs = [1, 2]; xs[1], xs[0] = xs; xs      # [2,1]
```

## The rest element: `*rest`

`*name` takes every position no other target takes, as a **new array** — possibly empty:

```
head, *tail = [1, 2, 3]       # head=1 tail=[2,3]
*init, last = [1, 2, 3]       # init=[1,2] last=3
a, *mid, z = 1..5             # a=1 mid=[2,3,4] z=5
h, *t = [1]                   # h=1 t=[]
[a, [b, *c], *d] = [1, [2, 3, 4], 5, 6]   # each level may have its own
```

Targets before the rest read from the front and targets after it read from the back, so
`z` above is always the last element. With a rest the length rule is "at least the other
targets", and the rest is a copy — pushing to `tail` leaves the original array alone.

```
a, *mid, z = [1]
# -e:1:1: index: destructuring expects at least 2 values, got 1
a, *b, *c = [1, 2, 3]
# -e:1:8: syntax: a pattern takes one rest element
*rest = [1, 2]
# -e:1:1: syntax: a rest element needs a neighbour; write rest = xs.array
```

A line may start with the rest — `*init, last = xs` — even though a line starting with `*`
otherwise continues the one before it: a `*` that begins a target list starts a statement of
its own.

## Lengths must match

Without a rest element, too few and too many are both run-time errors — never a silent
`nil`, never a dropped value.

| Written | Message |
|---|---|
| `a, b = [1, 2, 3]` | `index: destructuring expects 2 values, got 3` |
| `a, b = [1]` | `index: destructuring expects 2 values, got 1` |
| `a, b = 1` | `type: cannot destructure int: the right side must be an array` |
| `a, b = "ab"` | `type: cannot destructure string: the right side must be an array` |
| `a, b = {x: 1, y: 2}` | `type: cannot destructure dict: the right side must be an array` |

A dict is not positional: key order is insertion order, not a contract. Take one apart by
key with a [dict pattern](#dict-patterns) — `{x, y} = d` — or iterate it with
`for k, v in d`.

No compound operator destructures:

```
a, b += [1, 2]
# -e:1:6: syntax: destructuring assigns with '=' or ':=', not '+='
```

`=` and `:=` mean per position what they mean alone: `=` writes the nearest existing binding
and creates one here when there is none, `:=` always creates here.

```
n = 0; f = { n, m := [1, 2]; n }; [f.call(), n]    # [1,0]
```

## Dict patterns

`{ … }` on the left of `=` takes a dict apart **by key**. It is the shape JSON arrives in:

```
user = {name: "Ann", age: 41, city: "Riga"}
{name, age: a} = user                 # name = "Ann", a = 41
{"content-type": ct} = {"content-type": "text/csv"}   # a key that is not a word
{data: {reason}} = {data: {reason: "late"}}           # nested, by key
[id, {name}] = [7, user]              # inside an array pattern
{name, **rest} = user                 # rest = {"age":41,"city":"Riga"}
```

`{name}` is short for `{name: name}`. A pattern is **open**: keys it does not name are
ignored, because a JSON object grows. Every key it does name must be there:

| Written | Message |
|---|---|
| `{a} = {}` | `key: destructuring: key not found: "a"` |
| `{a, b} = [1, 2]` | `type: cannot destructure array with a dict pattern` |
| `{a: 1} = d` | `syntax: a destructuring target binds, so the value of 'a' goes to a name, …` |
| `{} = d` | `syntax: an empty dict pattern binds nothing` |
| `{**r, a} = d` | `syntax: '**' collects the entries no key names, so it comes last in a dict pattern` |

`**rest` is a **new** dict of what no key names, in the source's order. A record is a dict,
so a pattern takes one apart too; its rest is a plain dict, as `{**p}` is.

## In a `match` arm

The same shape asks a question instead of asserting one: a subject of the wrong kind or the
wrong length is the next arm, not an error.

```
match [1, [2, 3]] { [x, [y, z]] -> x + y + z; else -> -1 }   # 6
match 1           { [a, b] -> "fits"; else -> "moved on" }   # moved on
match 1..2        { [a, b] -> "range fits ${a}${b}"; else -> "no" }   # range fits 12
```

A rest element works here too, and makes the length "at least": `*name` binds the positions
in between and a bare `*` skips them.

```
match ["GET", "users", "7"] { ["GET", *path] -> path.join("/"); else -> "?" }   # users/7
match [3, 1, 2]             { [first, *] -> first }                           # 3
match []                    { [first, *] -> first; else -> "empty" }          # empty
```

A name binds, a literal still compares, and a guard sees what the pattern bound — see
[`examples/33_destructuring.mzs`](../../examples/33_destructuring.mzs) for the full set.

A dict pattern in an arm fires when the subject is a dict with every key it names and every
value fits. A missing key is the next arm, not an error:

```
fn describe(ev) {
  match ev {
    {kind: "paid", total: t} if t > 0 -> "credit ${t}"
    {kind: "refund", data: {reason}}  -> "refund: ${reason}"
    {kind: /^test\./}                 -> "a test event"
    {age: in 18..65}                  -> "adult"
    {}                                -> "some other dict"
    else                              -> "not a dict"
  }
}
describe({kind: "refund", data: {reason: "late"}})   # refund: late
describe({kind: "paid", total: 0})                   # some other dict
```

A record's name in front of the braces asks for that shape as well — the same question
the bare `Money ->` arm asks — and its keys are checked against the declaration:

```
record Money(amount, currency = "RUB")
match m {
  Money {amount, currency: "EUR"} -> "€${amount}"
  Money {amount} if amount > 100   -> "a lot"
  Money {}                         -> "some money"
}
# Money {amout} -> …   syntax: record Money has no field 'amout'; it has amount, currency
```

`Name {…}` is a record pattern only when `Name` is a record; `choose {it} ->` is still a call
with a trailing closure. A record from an included module, `money.Money {…}`, is not a
pattern yet.

`{age: in 18..65}` asks membership; `{age: 18..65}` would compare with a range and is never
true of an int. `**rest` binds the other entries, and a bare `**` skips them. A name in a
pattern binds even if one is in scope — write `{k: (limit)}` to compare with its value, and
`({a: 1}) ->` to compare the whole subject with a dict.

## In a `for` header

```
for k, v in {a: 1, b: 2} { println("${k}=${v}") }   # a=1 / b=2
for k, v in [[1, 2], [3, 4]] { println(k + v) }     # 3 / 7
```

A dict iterates as `[key, value]` pairs, which is why the two-variable form reads the way it
does. An item that is not a pair is an error:

```
for k, v in [1, 2] { println(k) }
# -e:1:1: type: cannot destructure int: a two-variable 'for' takes an array of two per item
```

Either loop variable may be a pattern, which takes each item apart as `:=` would:

```
for [id, row] in [[1, "a"], [2, "b"]] { println("${id}:${row}") }
for {name, age} in users { println("${name} is ${age}") }
for k, {total} in {ann: {total: 3}, bob: {total: 5}} { println("${k}=${total}") }
```

A loop pattern binds fresh names for each item, so it holds names, rests and nested
patterns — not a `$var`, an index or a field.

## Statement or expression

The bare `a, b = …` form is a **statement** — a comma at bracket depth zero means something
else inside a call, a collection, a `for` header and a `match` arm. The bracketed form is an
ordinary expression whose value is the right side:

```
x = ([a, b] = [1, 2]); x      # [1,2]
```

## See also

- [Control flow](./control-flow.md) — `match`, `for` and the modifiers
- [Values](./values.md) — why an array has positions and a dict does not
- [Functions](./functions.md) — closure parameters, which are not destructuring
