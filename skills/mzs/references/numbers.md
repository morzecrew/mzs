Numbers: int vs float, conversions from variables, rounding, formatting prices, decimal for exact money, math.

# Numbers

Two kinds: `int` (64-bit) and `float`. Int overflow turns into a float, never wraps. A line
`expr  # → value` shows the exact result.

## Int and float arithmetic

```mzs
7 / 2  # → 3
7 / 2.0  # → 3.5
7.float / 2  # → 3.5
-7 / 2  # → -3
7 % 3  # → 1
-7 % 3  # → 2
div(-7, 2)  # → -4
2 ** 10  # → 1024
2 ** -1  # → 0.5
1 == 1.0  # → true
0.1 + 0.2  # → 0.30000000000000004
1 / 2 * 100  # → 0
1 * 100 / 2  # → 50
type(5 / 2)  # → "int"
type(5 / 2.0)  # → "float"
```

`/` between two ints truncates: compute percentages as `part * 100 / total` or use floats.
`x / 0` with ints raises `zero-division` (guard: `total == 0 ? 0 : part * 100 / total`).
`0` is **truthy**: `if count { }` is always true, write `if count > 0 { }`.

## From scenario variables

Every variable comes as a string (or `nil`), so convert before arithmetic or comparison:

```mzs
include bot
price = bot.get("price").int
qty = (bot.get("qty") ?? "1").int
missing = bot.get("no_such_var").int
price * qty  # → 3000
missing  # → 0
"1500.50".float  # → 1500.5
"1 500".replace(/\s/, "").int  # → 1500
"12,5".replace(",", ".").float  # → 12.5
"abc".parse_int ?? -1  # → -1
```

`.int`/`.float` never raise (bad text → 0). To *validate* input use `parse_int`/`parse_float`,
which return `nil` unless the whole string is a number:

```mzs
include bot
n = $message.trim.parse_int
if n == nil || n <= 0 {
  bot.say("Введите количество цифрами, например: 3")
  bot.exit("no")
} else {
  bot.set("qty", n)
  bot.exit("yes")
}
```

## Rounding

```mzs
2.5.round  # → 3
(-2.5).round  # → -3
1.255.round(2)  # → 1.26
1234.5678.round(-2)  # → 1200
3.7.int  # → 3
(-3.7).int  # → -3
3.7.floor  # → 3
3.2.ceil  # → 4
1.239.floor(2)  # → 1.23
5.clamp(1, 3)  # → 3
(-5).abs  # → 5
-5.abs  # → -5
```

`round` is half away from zero; with no digits it returns an int. `.int` truncates toward zero.
A method binds tighter than unary minus: `-5.abs` is `-(5.abs)`; write `(-5).abs`.

## Showing numbers to the visitor

Floats render with a dot and at least one decimal (`1500.0`), so round or format first:

```mzs
total = 1250 * 1.2
"${total}"  # → "1500.0"
"${total.round}"  # → "1500"
"%.2f" % [total]  # → "1500.00"
fn rub(n) { n.round.str.replace(/\B(?=(\d{3})+(?!\d))/, " ") + " ₽" }
rub(1234567.4)  # → "1 234 567 ₽"
fn rub2(n) { ("%.2f" % [n]).replace(".", ",") + " ₽" }
rub2(99.5)  # → "99,50 ₽"
```

## Percentages and discounts

```mzs
price = 2990
discount = 15
(price * (100 - discount) / 100.0).round  # → 2542
(price * discount / 100.0).round(2)  # → 448.5
paid = 3
total = 7
(paid * 100.0 / total).round(1)  # → 42.9
```

## decimal — exact money

Floats are binary (`0.1 + 0.2`). For sums that must be exact to the kopeck use `decimal`.
A decimal is made from a **string or an int** (a float is refused), and printed with
`decimal.str(d, places)`:

```mzs
include decimal
p = decimal.of("1999.90")
q = decimal.times(p, 3)
decimal.str(q)  # → "5999.7"
decimal.str(q, 2)  # → "5999.70"
d15 = decimal.times(p, decimal.of("0.15"))
decimal.str(d15, 2)  # → "299.99"
decimal.str(decimal.minus(q, d15), 2)  # → "5699.72"
decimal.str(decimal.plus(decimal.of("0.1"), decimal.of("0.2")))  # → "0.3"
decimal.str(decimal.div(decimal.of(100), 3, 2))  # → "33.33"
decimal.str(decimal.round(decimal.of("2.345"), 2))  # → "2.35"
decimal.str(decimal.round(decimal.of("2.345"), 2, "half_even"))  # → "2.34"
decimal.split(decimal.of("100"), 3, 2).map { decimal.str(it, 2) }  # → ["33.34","33.33","33.33"]
decimal.str(decimal.sum([decimal.of("10.10"), decimal.of("5.05")]), 2)  # → "15.15"
decimal.cmp(p, decimal.of(2000))  # → -1
decimal.of("1.50") == decimal.of("1.5")  # → true
decimal.int(p)  # → 1999
decimal.float(p)  # → 1999.9
```

Members: `of plus minus times div neg abs cmp round str float int sum split`. Every member but
`of` takes a decimal or an int, **not a string**: `decimal.plus("1", 2)` is an error — wrap with
`decimal.of`. `decimal.of` reads digits, one dot and a sign only: `"1 500,35"` must be cleaned
first (`s.replace(/\s/, "").replace(",", ".")`). `decimal.div` without `places` raises when the
result is not exact (1/3). Compare with `decimal.cmp` (`<` does not work on decimals).

```mzs
include decimal
include bot
raw = (bot.get("price") ?? "0").replace(/\s/, "").replace(",", ".")
amount = try decimal.of(raw) else decimal.of(0)
vat = decimal.round(decimal.times(amount, decimal.of("0.2")), 2)
bot.set("vat", decimal.str(vat, 2))
bot.say("НДС 20%: ${decimal.str(vat, 2)} ₽")
```

## math

```mzs
include math
math.sqrt(16)  # → 4.0
math.pow(2, 0.5).round(3)  # → 1.414
math.pi.round(4)  # → 3.1416
math.log10(1000)  # → 3.0
```

`math`: `pi e inf nan sqrt cbrt sin cos tan asin acos atan atan2 sinh cosh tanh log log2 log10
exp pow hypot`. Without `include math`, `min`/`max`/`abs`/`round` still work: `max(3, 7)`,
`[1, 5].max`.

## Other number methods

`n.even`, `n.odd`, `n.zero`, `n.positive`, `n.negative`, `n.sign`, `a.gcd(b)`, `a.lcm(b)`,
`n.str(16)` (base), `n.times { (i) -> … }`, `1.upto(3)`, `65.chr`, `x.is_nan`. Bit operations are
functions: `band bor bxor bnot shl shr` (there is no `&`, `|`, `^`, `<<`).
