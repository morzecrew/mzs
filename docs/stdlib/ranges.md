# Ranges

The `..` and `..<` literals, the rows a range answers, and where materialising one costs
memory.

## Literals

```
1..5              # 1..5   — inclusive
(1..5).array      # [1,2,3,4,5]
(1..<5).array     # [1,2,3,4]  — exclusive of the upper endpoint
type(1..5)        # "range"
str(1..5)         # "1..5"
```

Endpoints must be numbers, and they are read as ints:

```
"a".."c"
# -e:1:4: type: a range needs integer endpoints, got string and string

1.9..3.9          # 1..3   — a float endpoint truncates
```

A descending range is **empty**, not reversed:

```
(5..1).array      # []
(5..1).len        # 0
```

## Laziness

A range stores two endpoints. `len`, `empty`, `has`, `first`, `last` and indexing answer
from those endpoints alone; every other row materialises the elements first, charged
against `MaxCollection` (default 1,000,000).

```
(1..10000000).len         # 10000000
(1..10000000).has(3)      # true
(1..10000000).first       # 1
(1..10000000).last(2)     # [9999999,10000000]
(1..10000000).empty       # false
(1..5)[0]                 # 1

(1..2000000).sum
# -e:1:14: limit: collection too large: 2000000 elements exceeds the limit of 1000000
```

`seq` is the third answer, for the rows that would otherwise have to materialise: it walks
the range by counting, so nothing is ever built and the cap is never reached.

```
(1..1000000000).seq.filter { it % 7 == 0 }.take(3).array   # [7,14,21]
(1..1000000000).seq.map { it * 2 }.first                   # 2
```

See [Sequences](./sequences.md).

A range answers `true` to `is("array")` while `type(r)` stays `"range"` — the one place
the two disagree.

## The rows

Every non-mutating array row except `dig` is registered for ranges as well, and `step` is
the one row a range has that an array does not. There are no mutating rows: a range has
nothing to mutate.

```
(1..5).len                              # 5
(1..5).has(3)                           # true
(1..5).first(2)                         # [1,2]
(1..5).last(2)                          # [4,5]
(1..5).min                              # 1
(1..5).max                              # 5
(1..5).sum                              # 15
(1..0).sum                              # 0
(1..5).array                            # [1,2,3,4,5]
(1..5).map { it * 2 }                   # [2,4,6,8,10]
(1..5).filter { it.even }               # [2,4]
(1..5).reject { it.even }               # [1,3,5]
(1..5).find { it > 3 }                  # 4
(1..5).count { it.even }                # 2
(1..5).each { print(it) }               # prints 12345, returns 1..5
(1..5).reduce { (a, x) -> a + x }       # 15
(1..6).each_slice(2)                    # [[1,2],[3,4],[5,6]]
(1..3).each_cons(2)                     # [[1,2],[2,3]]
(1..5).reverse                          # [5,4,3,2,1]
(1..5).zip([9])                         # [[1,9],[2,null],[3,null],[4,null],[5,null]]
(1..5).group_by { it.even }             # {"false":[1,3,5],"true":[2,4]}
(1..5).json                             # "[1,2,3,4,5]"
```

`push` and `dig` are array-only:

```
(1..5).push(6)    # -e:1:8: name: undefined method 'push' for range
(1..5).dig(0)     # -e:1:8: name: undefined method 'dig' for range
```

## step

`step(n)` walks the range by `n`. Without a closure it returns the array, so it chains;
with one it iterates and returns the receiver. `n` must be positive.

```
(0..10).step(2)                  # [0,2,4,6,8,10]
(0..10).step(3) { print(it) }    # prints 0369, returns 0..10
(0..10).step(2).map { it + 1 }   # [1,3,5,7,9,11]
```

## Descending and stepped sequences

A range literal is always ascending by one. For anything else use `reverse`, the number
iterators, or the `range` function.

```
(1..5).reverse       # [5,4,3,2,1]
5.downto(1)          # [5,4,3,2,1]
0.step(10, 3)        # [0,3,6,9]
10.step(0, -3)       # [10,7,4,1]
```

`range(hi)`, `range(lo, hi)` and `range(lo, hi, step)` build an **array**, not a range,
and the upper bound is exclusive:

```
range(5)             # [0,1,2,3,4]
range(1, 5)          # [1,2,3,4]
range(0, 10, 2)      # [0,2,4,6,8]
range(10, 0, -2)     # [10,8,6,4,2]
type(range(1, 5))    # "array"
```

## In for and match

```
for i in 1..3 { print(i) }        # prints 123
for i in 1..<3 { print(i) }       # prints 12
```

An `in` arm of a `match` tests membership, which for a range is the endpoint test — no
materialisation, so a wide range costs nothing:

```
match 7 {
  in 1..5  -> "low"
  in 6..10 -> "high"
  else     -> "?"
}
# "high"
```

```
match 7 { in 1..<7 -> "low"; else -> "other" }   # "other"
```

## As an index

A range indexes strings and arrays, and the result is a new string or array:

```
"hello"[1..3]        # "ell"
[1,2,3,4][1..2]      # [2,3]
```

## See also

- [Arrays](./arrays.md) — the rows a range shares with arrays
- [Sequences](./sequences.md) — `(a..b).seq`, for the rows that would otherwise materialise
- [Numbers](./numbers.md) — `times`, `upto`, `downto`, `step`
- [Control flow](../language/control-flow.md) — `for` and `match`
- [Sandbox and limits](../reference/sandbox.md) — `MaxCollection` and the step budget
