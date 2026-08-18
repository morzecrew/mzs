# Standard library

How the library is organised: one flat namespace, every function usable as a method, exactly one name per operation.

## One namespace

Every entry in the library is a plain function. There is no separate "global functions"
namespace and "methods on a type" namespace — the two spellings are the same function.

```
len("привет")        # 6
"привет".len         # 6
json([1, 2])         # [1,2]
[1, 2].json          # [1,2]
```

`x.f(y)` resolves in this order:

1. the method table for `x`'s kind (the rows of these pages, grouped by first argument);
2. a function named `f` visible in scope, called as `f(x, y)`;
3. otherwise `name: undefined method 'f'`, with a did-you-mean suggestion when a near name
   exists, or the receiver's kind when the name is a method of some other kind
   (`5.await` → `name: undefined method 'await' for int`).

Step 2 means your own functions are methods immediately:

```
fn shout(s) { s.upper + "!" }
"yes".shout          # YES!
```

```
fn tax(n, r) { n * r }
100.tax(0.2)         # 20.0
```

A Dict never dispatches `.` to its own keys — that is what keeps step 2 unambiguous.
Read dict values with `[]`, `.get` or `.dig`:

```
d = {a: 1}
d["a"]               # 1
d.a                  # name: undefined method 'a'; did you mean 'd'?
```

## No aliases

Each operation has one spelling. The names other languages use are compiled into a
diagnostic rather than silently missing:

```
"ПРИВЕТ".downcase    # syntax: undefined method 'downcase'; did you mean 'lower'?
"  a ".strip         # syntax: undefined method 'strip'; did you mean 'trim'?
"a".gsub(/a/, "b")   # syntax: undefined method 'gsub'; did you mean 'replace'?
"12".to_i            # syntax: undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'
"a".empty?           # syntax: '?' is not part of an identifier; did you mean 'empty'?
```

## Reading the tables

| Notation | Meaning |
|---|---|
| `name(arg: type = default) -> type` | argument type and default, result type |
| `-> type` in a method table | the receiver is the implicit first argument |
| `*args` | variadic |
| `[{ (x) -> … }]` | optional trailing closure |
| `a \| b` | either kind is accepted |
| `any` | any value |

Nothing coerces. A wrong argument kind raises a `type:` error naming both sides —
`"abc".has(/b/)` → `type: has expects a string, got regex`; the rows that accept both a
string and a regex say so explicitly.

## Pages

| Page | Contents |
|---|---|
| [core.md](./core.md) | printing and `input`, sizes, kinds, conversions, aggregates, `format`, errors |
| [strings.md](./strings.md) | case, trimming, searching, splitting, replacing, slicing |
| [arrays.md](./arrays.md) | building, iterating, mapping, sorting, grouping |
| [dicts.md](./dicts.md) | keys, lookup with defaults, merging, nested `dig` |
| [numbers.md](./numbers.md) | rounding, predicates, loops, bit functions |
| [ranges.md](./ranges.md) | `a..b`, what a Range answers, materialising it |
| [sequences.md](./sequences.md) | `seq`: lazy sources, the lazy rows and the terminals, and why it is not an array |

Modules are not part of this namespace: `json`, `math`, `time`, `io` and `http` exist only
after an `include`. See [../modules/README.md](../modules/README.md).

## See also

- [../language/functions.md](../language/functions.md) — closures, `it`, and how UFCS dispatch is resolved
- [../language/values.md](../language/values.md) — the kinds these tables are grouped by
- [../modules/README.md](../modules/README.md) — the parts of the library that need `include`
