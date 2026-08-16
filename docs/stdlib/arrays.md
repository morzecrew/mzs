# Array functions

Every function whose first argument is an array, grouped by what it does, with the
mutating rows separated from the ones that return a new array.

Under UFCS each row is callable both ways: `len(xs)` is `xs.len`, `map(xs, f)` is
`xs.map(f)`. A closure is an ordinary trailing argument written `{ (a, b) -> … }`, or
`{ … it … }` for the one-parameter case.

## Mutation: the whole list

These ten rows write into the receiver. No other row on this page modifies it.

| Row | Returns |
|---|---|
| `push(*v)` `unshift(*v)` `insert(i, *v)` `concat(*other)` `delete(v)` | the receiver |
| `pop` `shift` `delete_at(i)` | the removed element, `nil` when there is none |
| `sort_in_place([cmp])` `reverse_in_place` | the receiver |

```
xs = [3, 1, 2]
ys = xs.sort                    # [1,2,3] — a new array; xs is still [3,1,2]
xs.sort_in_place                # [1,2,3] — xs is now [1,2,3]
xs = [1,2]; ys = xs; ys.push(3); xs         # [1,2,3] — arrays are references
xs = [1,2]; xs.concat([3])                  # [1,2,3] — concat mutates; `+` does not
xs = [1,4]; xs.insert(1, 2, 3)              # [1,2,3,4]
xs = [1,2]; xs.insert(-1, 9)                # [1,2,9] — negative counts from the end
xs = [1,2,3]; xs.delete_at(1)               # 2  (xs is now [1,3])
xs = [1,2,1,3]; xs.delete(1)                # [2,3] — every equal element
```

## Size and access

```
[1,2,3].len                  # 3
[].empty                     # true
[1,2,2,3].count(2)           # 2
[1,2,3,4].count { it.even }  # 2
[1,2,3].count                # 3
[1,2,3].first                # 1
[1,2,3].first(2)             # [1,2]
[1,2,3].last(2)              # [2,3]
[].first                     # nil
[1,2,3].has(2)               # true
[1,2,3].index(3)             # 2
[1,2,3].index { it > 1 }     # 1
[1,2,3].index(9)             # nil
[[1,2],[3]].dig(0, 1)        # 2
```

`count` and `index` take either a value (compared with `==`) or a closure. `dig` walks
arrays and dicts alike and stops at the first `nil` instead of raising.

## Iteration

```
[1,2,3].each { print(it) }                          # prints 123, returns [1,2,3]
["a","b"].each_with_index { (_, i) -> print(i) }    # prints 01
[1,2,3,4].each_slice(2)                             # [[1,2],[3,4]]
[1,2,3,4,5].each_slice(2)                           # [[1,2],[3,4],[5]]
[1,2,3,4].each_cons(2)                              # [[1,2],[2,3],[3,4]]
```

`each` returns the receiver. `each_slice` and `each_cons` return the chunks when called
without a closure and the receiver when called with one.

## Transformation

```
[1,2,3].map { it * 2 }        # [2,4,6]
[1,2].flat_map { [it, it] }   # [1,1,2,2]
[1,[2,[3]]].flatten           # [1,2,3]
[1,[2,[3]]].flatten(1)        # [1,2,[3]]
[1,nil,2].compact             # [1,2]
[1,2,3].reverse               # [3,2,1]
["a","b"].join("-")           # "a-b"
[1,nil,"a"].join(",")         # "1,,a"  — `str` of each element
```

## Selection

```
[1,2,3,4].filter { it.even }     # [2,4]
[1,2,3,4].reject { it.even }     # [1,3]
[1,2,3].find { it > 1 }          # 2
[1,2,3].find { it > 9 }          # nil
[1,2,3].any { it > 2 }           # true
[1,2].all { it > 0 }             # true
[1,2].none { it > 5 }            # true
[0, nil].any                     # true  — without a closure, element truthiness
[1,0,nil].all                    # false
[].all                           # true
```

## Folding

```
[1,2,3].reduce { (a, x) -> a + x }        # 6
[1,2,3].reduce(10) { (a, x) -> a + x }    # 16
[1,2,3].sum                               # 6
[1.5, 2].sum                              # 3.5
[].sum                                    # 0
[{n: 1}, {n: 2}].sum { it["n"] }          # 3
[1,1,2].tally                             # {"1":2,"2":1}
[1,2,3,4].group_by { it.even }            # {"false":[1,3],"true":[2,4]}
[1,2,3,4].partition { it.even }           # [[2,4],[1,3]]
```

`tally` and `group_by` return dicts whose keys keep their own kind — `[1,1,2].tally.keys`
is two ints; JSON only renders keys as strings.

## Ordering

```
[3,1,2].sort                              # [1,2,3]
[3,1,2].sort { (a, b) -> b <=> a }        # [3,2,1]
["bb","a","ccc"].sort_by { it.len }       # ["a","bb","ccc"]
["aa","b"].min_by { it.len }              # "b"
[3,1,2].min                               # 1
[].min                                    # nil
["a","bbb"].max { (a, b) -> a.len <=> b.len }   # "bbb"
[1,"a"].sort                              # type: comparison of string with int failed
```

`sort` is stable. `sort`, `min` and `max` take a **comparator** `{ (a, b) -> int }`;
`sort_by`, `min_by` and `max_by` take a one-argument key closure. The plain-call spelling
words the same failure differently — `sort([1,"a"])` is
`type: sort: string and int are not comparable` ([core.md](./core.md)).

## Sets

```
[1,1,2,1].uniq                        # [1,2]
[[1,"a"],[2,"a"]].uniq { it[1] }      # [[1,"a"]] — first occurrence wins

[1,1,2].to_set                        # {"1":true,"2":true}
[1,1,2].union([2,3])                  # [1,2,3]
[1,2,3].intersect([2,3,4])            # [2,3]
[1,1,2,3].difference([3])             # [1,2]
[1,2].subset([3,2,1])                 # true
[].subset([1])                        # true
[1,2].union([3], [1])                 # [1,2,3] — union, intersect and difference are variadic
```

The four rows answer with a **set**: the first occurrence of each element wins and nothing
repeats. `intersect`, `difference` and `subset` keep the receiver's order; `union` has
elements the receiver never had, so it is the receiver's order first and then each
argument's, in the order they were given:

```
[3, 1].union([2, 1], [4])      # [3,1,2,4] — receiver first, then each argument
```

That is what tells them from `+` and `-`, which keep every element they were given:

```
[1,1,2] + [2]                # [1,1,2,2]   concatenation
[1,1,2].union([2])           # [1,2]       the set of both
[1,1,2] - [2]                # [1,1]       removal
[1,1,2].difference([2])      # [1]         the set of what is left
```

There is no set **kind**. A set is a dict whose values are `true` — which is what you write
by hand the moment you need "have I seen this" — and `to_set` is that dict:

```
seen = ["a","b"].to_set
seen.has("a")                # true   — O(1), where ["a","b"].has("a") is O(n)
seen.set("c", true)          # {"a":true,"b":true,"c":true}
seen.keys                    # ["a","b","c"] — insertion-ordered, so the set is too
```

The row is `to_set` and not `set` because `set(k, v)` is the dict row that writes a key.
Membership in the four rows is `==`, so an array of arrays works; `to_set` is the one that
needs hashable elements, because a dict key does:

```
[[1,2],[1,2]].union([[3]])   # [[1,2],[3]]
[[1,2]].to_set               # type: dict key must be hashable, got array
```

## Slicing

```
[1,2,3].slice(1)          # [2]
[1,2,3].slice(1, 2)       # [2,3]
[1,2,3].slice(-2, 2)      # [2,3]
[1,2,3].slice(9)          # nil
[1,2,3,4].take(2)         # [1,2]
[1,2,3,4].drop(2)         # [3,4]
[1,2,3].take(9)           # [1,2,3]
[1,2,3].take(-1)          # argument: take expects a non-negative count, got -1
[1,2,3,1].take_while { it < 3 }   # [1,2]
[1,2,3,1].drop_while { it < 3 }   # [3,1]
[1,2,3,4][1..2]                   # [2,3] — a range index also slices
```

## Zipping

```
[1,2].zip([3,4])            # [[1,3],[2,4]]
[1,2].zip([3,4], [5,6])     # [[1,3,5],[2,4,6]]
[1,2,3].zip([4])            # [[1,4],[2,null],[3,null]]
```

The receiver sets the length; a short argument pads with `nil`.

## Bytes

```
"Ω".bytes                        # [206,169]
"Ω".bytes.pack_bytes == "Ω"      # true
[72,105].pack_bytes              # "Hi"
[300].pack_bytes                 # argument: pack_bytes: expected a byte in 0..255 at element 0, got 300
[0xFF].pack_bytes.bytes          # [255]
```

`pack_bytes` works in bytes, not runes, so it can build a string that is not valid UTF-8;
the rune-based string functions then see U+FFFD. An out-of-range element is an error that
names the index. See `examples/34_bits_and_bytes.mzs`.

## Conversion and randomness

```
[1,2].array                 # [1,2]  — an array's `array` is the receiver, not a copy
[[1,2],[3,4]].dict          # {"1":2,"3":4}
[[1]].dict                  # type: dict expects [key, value] pairs, element 0 is array
[1,2].json                  # "[1,2]"
```

`sample` and `shuffle` need randomness enabled — `--rand` on the CLI, `Options.Rand` when
embedding. Without it the method does not exist.

```sh
mzs --rand 7 -e '[1,2,3,4,5].shuffle'   # [3,2,4,1,5]
```

## Lazily

`seq` turns an array into a [sequence](./sequences.md): the same rows, evaluated one
element at a time and only as far as the answer needs.

```
[1,2,3].seq.map { it * 2 }.array      # [2,4,6]
(1..1_000_000_000).seq.find { it * it > 500 }   # 23 — nothing was materialised
```

## See also

- [Sequences](./sequences.md) — the lazy form of the rows above, for input that does not fit
- [Ranges](./ranges.md) — every non-mutating row here also works on a range
- [Dicts](./dicts.md) — `tally`, `group_by` and `dict` land here
- [Strings](./strings.md) — `bytes`, `chars`, `split`
- [Stdlib overview](./README.md) — UFCS and the one flat namespace
