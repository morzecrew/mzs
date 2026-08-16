# Sequences

`seq` — a source pulled one element at a time, for input that does not fit in an array and
for pipelines that should stop as soon as they have their answer.

## Why

Every other collection in mzs materialises, and `MaxCollection` caps that at a million
elements. That is the right default for a condition in a dialogue and the wrong one for a
log:

```
(1..1_000_000_000).map { it * 2 }
# limit: collection too large: 1000000000 elements exceeds the limit of 1000000

(1..1_000_000_000).seq.filter { it % 7 == 0 }.take(3).array
# [7,14,21]
```

`map`, `filter` and `take` on a seq **describe** the work. Nothing runs until a terminal
row asks for a value, and then only as far as that value needs.

## Making one

```
[1,2,3].seq                          # from an array
(1..1_000_000_000).seq               # from a range — counted, never materialised
seq { (i) -> i * i }                 # a generator, called with the index
seq([1,2,3])                         # the prefix spelling; UFCS makes them one function
io.lines                             # the input, a line at a time (see modules/io.md)
```

A generator ends by returning `nil`:

```
seq { (i) -> if i < 4 { i * i } }.array     # [0,1,4,9]
seq { (i) -> i }.take(3).array              # [0,1,2] — endless, and that is fine
```

Because `nil` ends it, a generated sequence cannot contain `nil`. There is no `yield`.

## Lazy rows

Each returns another seq and evaluates nothing.

| Row | Signature |
|---|---|
| `map` `filter` `reject` | `{ (x) -> … } -> seq` |
| `flat_map` | `{ (x) -> … } -> seq` — one level, an array/range/seq is pulled through |
| `take` `drop` | `(n) -> seq` |
| `take_while` `drop_while` | `{ (x) -> … } -> seq` |

```
(1..9).seq.map { it * 10 }.array           # [10,20,30,40,50,60,70,80,90]
(1..9).seq.filter { it.even }.array        # [2,4,6,8]
(1..9).seq.take_while { it < 4 }.array     # [1,2,3]
(1..9).seq.drop(6).array                   # [7,8,9]
(1..3).seq.flat_map { [it, -it] }.array    # [1,-1,2,-2,3,-3]
```

## Terminal rows

Each pulls the source until it has its answer.

| Row | Signature |
|---|---|
| `each` `each_with_index` | `{ … } -> seq` — returns the receiver |
| `array` | `-> array` — the materialisation |
| `len` `empty` `count` | `-> int` / `-> bool` / `(v) \| { (x) -> … } -> int` |
| `first` | `(n = nil) -> any \| array` |
| `has` | `(v) -> bool` — and so `x in s` |
| `find` `any` `all` `none` | as on an array, each stopping at the element that decides |
| `reduce` | `(init = nil) { (acc, x) -> … } -> any` |
| `sum` `min` `max` | as on an array, optional closure included |
| `join` | `(sep = "") -> string` |

```
(1..4).seq.sum                             # 10
(1..4).seq.reduce { (a, x) -> a * x }      # 24
(1..9).seq.find { it > 3 }                 # 4
(1..9).seq.first(2)                        # [1,2]
(1..3).seq.join("-")                       # "1-2-3"
(1..9).seq.count { it.even }               # 4
2 in (1..3).seq                            # true
for x in (1..3).seq { print(x) }           # prints 123
```

Laziness is measurable, not a promise. The generator below counts its own calls:

```
pulls = 0
s = seq { (i) -> pulls = pulls + 1; i }
s.filter { it % 7 == 0 }.take(2).array     # [0,7]
pulls                                      # 8 — not a million
```

## Everything else needs the whole sequence

`sort`, `reverse`, `uniq`, `tally`, `group_by` and `last` cannot answer from a prefix, so
they are not seq rows. Materialise first — it is one row and the same name:

```
(1..5).seq.sort            # type: sort expects an array, got seq
(1..5).seq.array.sort      # [1,2,3,4,5]
```

That is deliberate: a row that quietly buffered a gigabyte would defeat the point of
asking for a seq.

## A seq is a recipe, not a cursor

Every terminal opens the source again, so the same chain answers the same thing twice:

```
s = (1..3).seq.map { it * 2 }
s.array        # [2,4,6]
s.array        # [2,4,6]
s.len          # 3
```

Where the source has state of its own, a second run sees what that state left — because
that is what state means:

```
n = 0
s = seq { n = n + 1; if n <= 4 { n } }
s.take(2).array      # [1,2]
s.take(2).array      # [3,4]
```

A reader is the other stateful source: `io.lines` streamed once has given its bytes away.
Nothing is cached — caching a sequence is `.array`, spelled out.

## Not an array

```
type((1..3).seq)          # "seq"
(1..3).seq.is("seq")      # true
(1..3).seq.is("array")    # false
(1..3).is("array")        # true — a range says yes, a seq does not
str((1..3).seq)           # "#<seq>"
```

A range can be materialised on demand under the cap; a seq is the value that refuses to
be, so host code that takes an array is never handed one by accident. For the same reason
a seq is not compared (`==` is identity), not ordered, not a dict key, and has no JSON
form:

```
include json
{items: (1..3).seq}.json
# json: a seq is lazy and has no JSON form; materialise it with .array
{items: (1..3).seq.array}.json      # {"items":[1,2,3]}
```

The refusal reaches inside a value and reaches every encoder — `mzs --json`, an `http`
response body, and a host's `MarshalJSON` — so a forgotten `.array` is a diagnostic
wherever it happens rather than a field that quietly turned into `null`.

## Limits still apply

Every source charges one interpreter step per element, so an endless sequence ends the way
`while true` does — on the step budget or the deadline, never on memory:

```
seq { (i) -> i }.len
# limit: step budget exceeded (5000000 steps)
```

`array` is charged against `MaxCollection` and `join` against `MaxStringBytes`; the rows
that only count charge neither, because they build nothing.

## Gotcha: a closure in a loop header

`for x in seq { … }` reads the braces as the loop body, because in a header they always
are. A generator written there takes its own parentheses:

```
for x in (seq { (i) -> i }) { break if x > 2; print(x) }    # prints 012
```

## See also

- [Arrays](./arrays.md) — the eager rows, and `xs.seq` to make them lazy
- [Ranges](./ranges.md) — `(a..b).seq` is the source that costs nothing
- [io](../modules/io.md) — `io.lines`, the reason this kind exists
- [Sandbox and limits](../reference/sandbox.md) — the budgets a lazy chain still spends
