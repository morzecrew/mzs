# The `json` module

Parsing with `json.parse`, encoding with `x.json` and `json.pretty`, and the exact value
mapping in both directions.

```
include json

json.parse('{"id":7,"tags":["a","b"]}')   # {"id":7,"tags":["a","b"]} — a dict
{id: 7, tags: ["a", "b"]}.json            # {"id":7,"tags":["a","b"]} — a string
```

Encoding is the core `json` method (`x.json`) and needs no include; only `json.parse` and
`json.pretty` come from the module. There is no `json.generate`.

| Call | Signature | Result |
|---|---|---|
| `json.parse(s)` | `(string) -> any` | raises on malformed input |
| `json.pretty(x)` | `(any) -> string` | 2-space indent |
| `x.json` | any value | compact string |

## What `parse` returns

```
include json
d = json.parse('{"b":1,"a":2,"n":null,"f":1.5,"ok":true,"xs":[1,2]}')
[d.keys, d["n"] == nil, type(d["b"]), type(d["f"]), type(d["xs"]), type(d["ok"])].json
# [["b","a","n","f","ok","xs"],true,"int","float","array","bool"]
```

| JSON | mzs |
|---|---|
| object | dict, keys in document order |
| array | array |
| number, integral and within int64 | int |
| any other number | float |
| string | string |
| `true` / `false` | bool |
| `null` | nil |

Integrality decides, not the spelling: `json.parse("1.0")` and `json.parse("1e3")` are the
ints `1` and `1000`, while `json.parse("12345678901234567890")` is a float.

## What encoding does

```
[1.0, 2.5].json          # [1,2.5]      — an integral float loses the .0
(1..3).json              # [1,2,3]      — a range materialises
{cb: { it }}.json        # {"cb":null}  — a function has no JSON form
nil.json + " " + true.json   # null true
```

Dict keys that are not strings are encoded as strings — `d[1] = "a"; d[true] = "b"` gives
`{"1":"a","true":"b"}` — and NaN or an infinity encodes as `null`. A value that contains
itself is refused instead of looping:

```
c = [1, 2]; c.push(c)
try c.json else (e) -> e["message"]      # json: value contains a cycle
```

## Order is preserved, round trips are lossless

```
include json
d = json.parse('{"z":1,"a":2}')
[d.keys, json.parse(d.json) == d].json   # [["z","a"],true]
```

Equality is structural, so the comparison is a real check and not a string comparison.

## Reading a document nobody promised you

```
include json
d = json.parse('{"a":{"b":1}}')
[d.dig("a", "b"), d.dig("a", "z"), d.dig("z", "b") ?? "none"].json
# [1,null,"none"]
```

`dig` walks missing keys to nil instead of raising, and `??` supplies the default.

## Malformed input

A parse failure is an ordinary catchable `argument` error:

```sh
mzs -e 'include json; json.parse("{oops")'
```

```
-e:1:20: argument: json.parse: invalid character 'o'
  include json; json.parse("{oops")
                     ^
```

```
include json
try json.parse("{oops") else (e) -> e["message"]        # json.parse: invalid character 'o'
try json.parse("") else (e) -> e["message"]             # json.parse: EOF
try json.parse('{"a":}') else (e) -> e["kind"]          # argument
```

Two things that are *not* errors: text after a complete value is ignored
(`json.parse('{"a":1} trailing')` is the dict `{"a":1}`), and `json.parse("null")` is nil,
not a failure — test with `== nil` if that distinction matters.

## Limits

Both directions refuse a document nested deeper than 256 levels, before recursing:

```
include json
try json.parse("[" * 300 + "]" * 300) else (e) -> e["message"]
# json.parse: input is nested too deeply (300 levels)
```

Encoding charges the result against the collection, string and step limits, and those are
`limit` errors that `try` does not catch:

```sh
mzs -e 'include json; try (1..2000000).json else "caught"'
```

```
-e:1:32: limit: collection too large: 2000000 elements exceeds the limit of 1000000
```

## See also

- [./README.md](./README.md) — why `include json` is required
- [../stdlib/dicts.md](../stdlib/dicts.md) — `dig`, `merge`, `keys` on a parsed document
- [../language/errors.md](../language/errors.md) — `try`/`else` and error kinds
- [./http.md](./http.md) — JSON in and out over HTTP
