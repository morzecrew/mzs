# The `math` module

The complete member list, what each one returns, and how the module behaves on bad
arguments and out-of-domain input.

```
include math

math.sqrt(2)      # 1.4142135623730951
math.pi           # 3.141592653589793
math.pow(2, 10)   # 1024.0
```

`math` needs no host capability — `include math` is enough, in the CLI and in an embedding
host alike.

## Members

| Member | Arguments | Notes |
|---|---|---|
| `pi`, `e` | — | constants, `3.141592653589793` and `2.718281828459045` |
| `sqrt` `cbrt` `exp` | 1 | |
| `log` `log2` `log10` | 1 | `log` is the natural logarithm |
| `sin` `cos` `tan` `atan` | 1 | radians |
| `atan2` `pow` `hypot` | 2 | |

That is the whole module — `math.keys` lists exactly these 15 names, in registration order:

```
include math
math.keys
# ["pi","e","sqrt","cbrt","sin","cos","tan","atan","log","log2","log10","exp","atan2","pow","hypot"]
```

**Every function returns a float**, including one with an integral value: `math.pow(2, 10)`
prints `1024.0` and `type(math.sqrt(4))` is `"float"`. JSON drops the `.0`
(`math.sqrt(4).json` is `2`), and `.int` truncates when you need an int.

## Worked examples

```
include math
[math.sqrt(2), math.cbrt(27), math.pow(2, 10), math.hypot(3, 4), math.exp(1)].json
# [1.4142135623730951,3,1024,5,2.718281828459045]

[math.log(math.e), math.log2(1024), math.log10(1000)].json
# [1,10,3]

[math.sin(0), math.cos(0), math.tan(0), math.atan(1), math.atan2(1, 1)].json
# [0,1,0,0.7853981633974483,0.7853981633974483]
```

Degrees are not built in; convert with `pi`:

```
include math
fn rad(d) { d * math.pi / 180 }
[math.sin(rad(90)), math.cos(rad(180))].json      # [1,-1]
```

Path length along a polyline, with `hypot` doing the pairs:

```
include math
pts = [[0, 0], [3, 4], [6, 8]]
pts.each_cons(2).map { (p) -> math.hypot(p[1][0] - p[0][0], p[1][1] - p[0][1]) }.sum
# 10.0
```

## Out of domain, and out of the module

An impossible result is an IEEE value, not an error:

```
include math
math.sqrt(-1)          # NaN
math.log(0)            # -Infinity
[math.sqrt(-1)].json   # [null]  — JSON has no NaN
```

A non-numeric argument is a `type` error, a wrong count is an `argument` error, and a name
the module does not have is a `name` error. All three happen at run time: `--check` passes
on a branch that never runs, and `try` catches them.

```sh
mzs -e 'include math; math.sqrt("x")'      # type: math.sqrt expects a number, got string
mzs -e 'include math; math.sqrt(2, 3)'     # argument: math.sqrt expects 1 argument(s), got 2
mzs -e 'include math; math.round(1.5)'     # name: undefined member 'round' in module 'math'
```

Rounding, truncation, absolute value, min/max and the bit functions are number methods, not
module members — `(-3.7).abs.ceil` is `4`, and `2.sqrt` is
`undefined method 'sqrt'; did you mean 'sort'?`. See
[../stdlib/numbers.md](../stdlib/numbers.md).

## See also

- [../stdlib/numbers.md](../stdlib/numbers.md) — `round`, `floor`, `clamp`, bit functions
- [./README.md](./README.md) — `include`, and why `math(2)` is an error
- [../language/values.md](../language/values.md) — int and float, and when a result becomes a float
