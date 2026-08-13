# Number functions

Every function whose first argument is an int or a float, then the eight bit functions,
which are a separate world with their own rules.

## int and float

`int` is int64, `float` is float64, and there is no numeric tower between them. An `int`
operation that would overflow promotes to `float`; the bit functions below are the one
exception.

```
7 / 2             # 3       — int division truncates
7.0 / 2           # 3.5
-7 % 3            # 2       — the result takes the sign of the divisor
2 ** 10           # 1024    (int)
2 ** 0.5          # 1.4142135623730951
```

```
3.7.int           # 3       — truncates toward zero
(-3.7).int        # -3
3.float           # 3.0
2.0.str           # "2.0"   — a float always prints with a `.`
255.str(16)       # "ff"    — base 2..36, int receiver only
255.str(2)        # "11111111"
3.14.str(16)      # argument: str: a base needs an int receiver, got float
(-5).abs          # 5       — abs keeps the receiver's kind
2.pow(10)         # 1024    — the same operation as `**`
2.pow(-1)         # 0.5
65.chr            # "A"     — code point to a one-rune string
1055.chr          # "П"
1114112.chr       # argument: chr: 1114112 is out of the Unicode range
```

## round, ceil, floor, clamp

All three rounding rows take an optional digit count. `round` is **half away from zero**,
not banker's rounding. With `digits <= 0` the result is an int; with `digits > 0` it is a
float.

```
2.5.round         # 3
3.5.round         # 4
(-2.5).round      # -3
1.5.round         # 2
type(1.5.round)   # "int"
2.675.round(2)    # 2.68
1.256.round(2)    # 1.26
1250.round(-2)    # 1300
2549.6.round(-2)  # 2500
```

```
2.1.ceil          # 3
(-2.1).ceil       # -2
2.9.floor         # 2
(-2.9).floor      # -3
1.234.ceil(2)     # 1.24
1.238.floor(2)    # 1.23
```

`clamp(lo, hi)` returns the receiver, `lo` or `hi`. An inverted pair is an error, not a
silent swap.

```
5.clamp(1, 3)     # 3
0.clamp(1, 3)     # 1
2.clamp(1, 3)     # 2
2.clamp(3, 1)     # argument: clamp: min 3 is greater than max 1
```

## Predicates

```
0.zero            # true
0.0.zero          # true
1.positive        # true
(-1).negative     # true
4.even            # true
3.odd             # true
3.5.even          # name: undefined method 'even' for float
```

`even` and `odd` are int-only; the other three work on both kinds.

## Iteration

| Row | Sequence |
|---|---|
| `n.times` | `0 … n-1` |
| `a.upto(b)` | `a … b` ascending |
| `a.downto(b)` | `a … b` descending |
| `a.step(limit, by)` | `a`, `a+by`, … while inside `limit` |

Without a closure each row materialises the array. With a closure it iterates and returns
**the receiver**, so a million-step loop allocates nothing.

```
3.times                 # [0,1,2]
0.times                 # []
3.times { print(it) }   # prints 012, returns 3
1.upto(4)               # [1,2,3,4]
4.downto(1)             # [4,3,2,1]
0.step(10, 3)           # [0,3,6,9]
10.step(0, -3)          # [10,7,4,1]
1.step(5, 0)            # argument: step: step cannot be 0
```

Materialising past `MaxCollection` (default 1,000,000) is a limit error — `2000000.times`
says so. The closure form allocates nothing, but every iteration still costs a step
against the step budget (default 5,000,000).

## Durations

`--time` installs five more number methods, the `time` module's: `seconds` `minutes`
`hours` `days` `weeks`. Each one is plain int seconds; a float receiver truncates first,
so `(1.5).hours` is `3600`.

```
[30.seconds, 90.minutes, 2.hours, 3.days, 1.weeks].json   # [30,5400,7200,259200,604800]
```

They arrive with the flag, not with the include — without the clock `1.days` is
`name: undefined method 'days' for int`.

## Bit functions

They are functions, not operators. `&` sitting one keystroke from `&&` is the near-miss
the language refuses, and `<<` / `>>` are reserved syntax. Writing one gets a diagnostic
that names the function:

```
a = 12; b = 10; a & b
# syntax: '&' is not an mzs operator; use band(a, b), or '&&' for logical and
a = 12; b = 10; a | b
# syntax: '|' is not an mzs operator; use bor(a, b), or '||' for logical or
a = 12; b = 10; a ^ b
# syntax: '^' is not an mzs operator; use bxor(a, b), or '**' to raise to a power
```

`<<` and `>>` are not lexemes at all and fail with `unexpected '<'`; `~` is spent already —
it is the regex-match operator, so one's complement is `bnot`, never `~x`.
Under UFCS every function below is also a method: `12.band(10)` is `8`.

| Function | Semantics |
|---|---|
| `band(a, b)` `bor(a, b)` `bxor(a, b)` | and, or, xor, bit for bit |
| `bnot(a)` | one's complement |
| `shl(a, n)` | left shift; bits past 63 are dropped |
| `shr(a, n)` | **arithmetic** right shift; the sign bit is copied in |
| `popcount(a)` | set bits in the two's-complement form |
| `bit(a, i)` | is bit `i` set; `i` outside `0..63` is an error |

```
band(12, 10)      # 8
bor(12, 10)       # 14
bxor(12, 10)      # 6
bnot(0)           # -1
bnot(5)           # -6
shl(1, 4)         # 16
shr(1024, 3)      # 128
shr(-8, 1)        # -4
popcount(255)     # 8
popcount(-1)      # 64
bit(5, 0)         # true
bit(5, 1)         # false
```

### Pure int64, no promotion

`shl` truncates instead of promoting, which is what makes masks and checksums come out
right. A shift count above 63 saturates rather than wrapping.

```
shl(1, 63)        # -9223372036854775808
shl(1, 64)        # 0
shr(-1, 64)       # -1
```

A float argument, a negative shift count and an out-of-range bit index are errors rather
than guesses:

```
2.9.band(1)
# type: band expects an int, got float: bit operations do not round, write x.int
shl(1, -1)
# argument: shl: shift count -1 is negative; the other direction is shr
bit(5, 64)
# argument: bit: index 64 is outside 0..63
```

## bytes and pack_bytes

`bytes` takes a string apart into ints; `pack_bytes` puts an array of ints back together
as one byte each. They are the pair a binary format is built from.

```
"Ω".bytes                                                  # [206,169]
"Ω".bytes.pack_bytes == "Ω"                                # true
"\x01\x02".bytes.reduce(0) { (a, b) -> a.shl(8).bor(b) }   # 258
[300].pack_bytes
# argument: pack_bytes: expected a byte in 0..255 at element 0, got 300
```

`examples/34_bits_and_bytes.mzs` builds flags, RGB masks, an IPv4 subnet test and CRC-32
out of these rows.

## See also

- [Ranges](./ranges.md) — `1..5` next to `1.upto(5)`
- [Arrays](./arrays.md) — `pack_bytes`, `sum`, `min`, `max`
- [Operators](../language/operators.md) — `/`, `%`, `**`, and what is not an operator
- [Values](../language/values.md) — int and float literals, overflow promotion
- [Time and date](../modules/time.md) — the clock gate, and what durations add to
