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

A target is a name, a `$var`, an index, or a nested `[ … ]`.

```
d = {row: 0, col: 0}
d["row"], d["col"], $sold = [12, 4, true]
d.json                        # {"row":12,"col":4}
```

Anything else is rejected at compile time:

```
[3, b] = [1, 2]
# -e:1:2: syntax: cannot assign to this expression: a destructuring target is a name,
#         a $var, an index or a nested [ … ]
```

Because the right side is read exactly once, a target may write into the array being taken
apart:

```
xs = [1, 2]; xs[1], xs[0] = xs; xs      # [2,1]
```

## Lengths must match

Too few and too many are both run-time errors — never a silent `nil`, never a dropped value.

| Written | Message |
|---|---|
| `a, b = [1, 2, 3]` | `index: destructuring expects 2 values, got 3` |
| `a, b = [1]` | `index: destructuring expects 2 values, got 1` |
| `a, b = 1` | `type: cannot destructure int: the right side must be an array` |
| `a, b = "ab"` | `type: cannot destructure string: the right side must be an array` |
| `a, b = {x: 1, y: 2}` | `type: cannot destructure dict: the right side must be an array` |

A dict is not positional: key order is insertion order, not a contract. Take one apart by
name — `d["x"], d["y"] = …` — or iterate it with `for k, v in d`.

There is no rest element; `a, *rest` is reserved syntax:

```
a, *rest = [1, 2, 3]
# -e:1:4: syntax: unexpected '*'
```

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

## In a `match` arm

The same shape asks a question instead of asserting one: a subject of the wrong kind or the
wrong length is the next arm, not an error.

```
match [1, [2, 3]] { [x, [y, z]] -> x + y + z; else -> -1 }   # 6
match 1           { [a, b] -> "fits"; else -> "moved on" }   # moved on
match 1..2        { [a, b] -> "range fits ${a}${b}"; else -> "no" }   # range fits 12
```

A name binds, a literal still compares, and a guard sees what the pattern bound — see
[`examples/33_destructuring.mzs`](../../examples/33_destructuring.mzs) for the full set.

## In a `for` header

```
for k, v in {a: 1, b: 2} { say("${k}=${v}") }   # a=1 / b=2
for k, v in [[1, 2], [3, 4]] { say(k + v) }     # 3 / 7
```

A dict iterates as `[key, value]` pairs, which is why the two-variable form reads the way it
does. An item that is not a pair is an error:

```
for k, v in [1, 2] { say(k) }
# -e:1:1: type: cannot destructure int: a two-variable 'for' takes an array of two per item
```

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
