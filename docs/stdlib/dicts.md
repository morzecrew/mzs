# Dict functions

Every function whose first argument is a dict, plus the insertion-order guarantee that
makes dict output byte-stable.

A dict literal is `{key: value}` and the empty dict is `{}`. A dict never dispatches `.`
to its own keys: values are read with `[]`, `get`, `fetch` or `dig`.

## Insertion order

Keys keep the order they were first written, in every row: `keys`, `values`, `each`,
`map`, `filter`, `reject`, `find`, `any`, `all`, `array`, `json`. Re-assigning an existing
key keeps its position; deleting and re-inserting moves it to the end.

```
d = {b: 2, a: 1}; d.keys             # ["b","a"]
d = {a: 1}; d.set("a", 9); d.keys    # ["a"]
d = {a: 1, b: 2}; d.delete("a"); d.set("a", 9); d.keys   # ["b","a"]
```

`sort_by` is the only ordering row, and it returns pairs rather than reordering the dict.

## Reading

```
{a: 1}.len                  # 1
{}.empty                   # true
{a: 1, b: 2}.keys           # ["a","b"]
{a: 1, b: 2}.values         # [1,2]
{a: 1}.has("a")             # true
{a: 1}.has_val(1)           # true
{a: 1}["a"]                 # 1
{a: 1}["zz"]                # nil
{a: 1}.get("b")             # nil
{a: 1}.get("b", 0)          # 0
{a: nil}.get("a", 99)       # nil — the key is present, so the default is not used
{a: 1}.fetch("a")           # 1
{a: {b: {c: 3}}}.dig("a", "b", "c")   # 3
{a: 1}.dig("x", "y")        # nil
```

`get` never raises. `fetch` raises an `index` error on a miss and names the key:

```
{a: 1}.fetch("b")
# -e:1:8: index: key not found: "b"
```

`dig` walks dicts and arrays and stops at the first `nil`, so half-present data reads as
`nil` rather than an error.

## Writing

```
d = {a: 1}; d.set("b", 2)            # {"a":1,"b":2}  — mutates, returns the receiver
d = {a: 1}; d["b"] = 2; d            # {"a":1,"b":2}
d = {a: 1, b: 2}; d.delete("a")      # 1  — the removed value; d is now {"b":2}
d = {a: 1}; d.delete("zz")           # nil
```

## merge vs merge_in_place

`merge` builds a new dict; `merge_in_place` writes into the receiver. Both take any
number of dicts and later arguments win.

```
{a: 1}.merge({b: 2})                 # {"a":1,"b":2}
{a: 1}.merge({a: 9}, {b: 2})         # {"a":9,"b":2}
d = {a: 1}; d.merge({b: 2}); d       # {"a":1}          — unchanged
d = {a: 1}; d.merge_in_place({b: 2}); d   # {"a":1,"b":2}
```

A key that already exists keeps its position when merged over; a new key is appended.

## Iteration

Every closure below receives the key and the value as two arguments; a one-parameter
closure simply drops the value.

| Row | Returns |
|---|---|
| `each` | the receiver |
| `map` | an **array** of the closure's results |
| `filter` / `reject` | a dict, original order |
| `find` | the `[key, value]` pair, or `nil` |
| `any` / `all` | bool |
| `sort_by` | an array of `[key, value]` pairs |
| `invert` | a dict, values become keys |
| `array` | `[[key, value], …]` |

```
{a: 1, b: 2}.each { (k, v) -> print("${k}=${v} ") }   # prints a=1 b=2
{a: 1, b: 2}.map { (k, v) -> "${k}${v}" }             # ["a1","b2"]
{a: 1, b: 2}.map { it }                               # ["a","b"]
{a: 1, b: 2}.filter { (_, v) -> v > 1 }               # {"b":2}
{a: 1, b: 2}.reject { (_, v) -> v > 1 }               # {"a":1}
{a: 1, b: 2}.find { (_, v) -> v > 1 }                 # ["b",2]
{a: 1}.find { (_, v) -> v > 9 }                       # nil
{a: 1, b: 2}.any { (k) -> k == "b" }                  # true
{a: 1, b: 2}.all { (_, v) -> v > 1 }                  # false
{a: 1, b: 2, c: 3}.sort_by { (_, v) -> -v }           # [["c",3],["b",2],["a",1]]
{a: 1}.invert                                         # {"1":"a"}
{a: 1}.array                                          # [["a",1]]
```

Naming a parameter you do not use is a compile warning; `_` is the way to skip one.

A `for` loop over a dict yields the pairs, and two loop variables destructure them:

```
for k, v in {a: 1, b: 2} { print("${k}=${v} ") }   # prints a=1 b=2
for p in {a: 1} { print(p) }                       # prints ["a",1]
```

## Keys

Keys are hashable values: nil, bool, int, float, string, regex, time. An array or a dict
as a key is a type error. A float that is integral hashes as the equivalent int.

```
d = {}; d.set([1], 2)
# -e:1:12: type: type: dict key must be hashable, got array

d = {}; d.set(1.0, "a"); d.set(1, "b"); d      # {"1.0":"b"} — one entry
```

The literal syntax takes a bare identifier or a string before `:`, and the identifier is a
literal key rather than a variable reference.

```
{"a b": 1}                  # {"a b":1}
k = "x"; {k: 1}             # {"k":1}  — the key is "k", not "x"
[[1, 2], [3, 4]].dict       # {"1":2,"3":4}
```

`->` separates an entry wherever `:` does, and it is what puts a key that is **not** a
string in a literal — a number, a bool, `nil`, a regex:

```
{1 -> "A", 2 -> "B"}[1]     # A
{-2.5 -> "cold"}            # a signed number is a key too
{true -> 1, nil -> 2}       # bool and nil keys
{1 -> "A", a: 2, "b" -> 3}  # the two separators mix freely
{a -> 1} == {a: 1}          # true — a bare word is the string either way
```

A computed key keeps its own spelling, `(k): v`, because `(k) ->` is a closure's parameter
list. The two wrong separators each name their replacement:

```
k = "x"; {(k): 1}           # {"x":1}
{1: "A"}
# -e:1:3: syntax: a dict key that is not a string takes '->', not ':'
{a: 1, (k) -> 2}
# -e:1:12: syntax: a computed dict key takes ':', not '->': write (k): v
```

## JSON

`str`, `json` and the CLI's default printing all use the same encoder, so a dict prints
as a JSON object in insertion order. Keys are rendered with `str(key)`, which is how a
non-string key survives the trip.

```
{a: [1, {b: 2}]}.json          # {"a":[1,{"b":2}]}
[1,1,2].tally.json             # {"1":2,"2":1}
d = {}; d.set(1, "x"); d.set(true, "y"); d.json    # {"1":"x","true":"y"}
```

`dup` is a shallow copy: the top level is new, nested containers are still shared.

```
d = {a: 1}; e = d.dup; e.set("b", 2); d                 # {"a":1}
d = {a: {b: 1}}; e = d.dup; e["a"].set("b", 9); d       # {"a":{"b":9}}
```

See `examples/06_dicts_records.mzs` and `examples/13_word_frequency.mzs`.

## See also

- [Arrays](./arrays.md) — `tally`, `group_by` and `dict` produce dicts
- [Core functions](./core.md) — `dict`, `json`, `inspect`, `dup`
- [The json module](../modules/json.md) — parsing and pretty-printing
- [Values](../language/values.md) — dict literals, hashing, equality
