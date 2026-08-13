# Host variables

`$name` is a first-class identifier bound from a table the host owns — a separate namespace
from local variables, in code and inside strings alike.

## Read, write, unbound

```
inspect($nope)                # nil     — an unbound $var is never an error
$a = 1; $a + 1                # 2       — a write inserts into the table
defined($nope)                # false
```

`nil` is falsy, so a condition mentioning a variable nobody set is simply false rather than a
crash. That is the whole reason the sigil exists.

`$name` is never resolved through the scope chain, so a local and a global with the same name
are unrelated, and a function body reaches `$x` with no capture:

```
sent = "a"; [sent, inspect($sent)]                    # ["a","nil"]
fn f() { $x }; f()                                    # deep — under mzs -v x=deep
[1, 2].each { $n = ($n ?? 0).int + it }; $n           # 3
```

`defined` asks the table, not the value — a `$var` set to `nil` is still bound:

```
$x = nil; defined($x)         # true
```

`defined(name)` without the sigil answers the same question about locals, builtins and
modules: `defined(now)` is `false` until a clock is enabled.

## Values arrive as strings

With `-v` every value is a string, and nothing is coerced:

```sh
mzs -v x=5 -e 'type($x)'              # string
mzs -v x=5 -e '$x + 1'
# -e:1:4: type: cannot add int to string
mzs -v x=5 -e '$x.int + 1'            # 6
```

`.int` and `.float` never raise, which is what makes an unset variable safe in arithmetic:

| Input | `.int` | `.float` |
|---|---|---|
| `""` | `0` | `0.0` |
| `"12abc"` | `12` | `12.0` |
| `"abc"` | `0` | `0.0` |
| unbound (`nil`) | `0` | `0.0` |

```
$counter = $counter.int + 1; $counter                 # 1
```

Values are bound, never parsed, so spaces, apostrophes and emoji inside one cannot affect the
program:

```sh
mzs -v "s=  O'Brien 🇬🇧  " -e 'inspect($s)'           # "  O'Brien 🇬🇧  "
```

## In strings

`$name` inside a double-quoted string is the same global it is outside — this is the one
place a conversion (`str`) is implicit. A *local* needs `${…}`.

```sh
mzs -v price=1500 -e '"total: $price"'                # total: 1500
mzs -v price=1500 -e '"total: ${$price.int + 100}"'   # total: 1600
```

## Setting them from the CLI

| Flag | Effect |
|---|---|
| `-v k=v`, `--var k=v` | bind `$k` to the string `v`; repeatable |
| `--vars <json>` | bind every member of a JSON object, keeping JSON types |
| `--vars-file <path>` | the same, read from a file |

```sh
mzs -v a=1 -v b=2 -e '[$a, $b]' --json
# ["1","2"]

mzs --vars '{"x":5,"tags":["a"],"ok":true}' -e '[type($x), type($tags), type($ok)]' --json
# ["int","array","bool"]

printf '{"__sent":"Привет","price":1500}' > vars.json
mzs --vars-file vars.json -e '[$__sent.lower, $price + 100]' --json
# ["привет",1600]
```

Under `--vars` a JSON number stays a number, so `$price + 100` works without `.int`. Code that
must run under both spellings converts what it needs, when it needs it.

The leading `$` is optional in a key: `-v '$x=5'` and `--vars '{"$y":7}'` bind the same names
as `-v x=5` and `--vars '{"y":7}'`.

## Line mode

Under `-n` / `-l` the program runs once per input line, the line is `$_`, and the globals
table carries across lines — which is how a running total is kept:

```sh
printf 'a\nbb\nccc\n' | mzs -n -e '$n = ($n ?? 0) + 1; "${$n}:${$_}"'
# 1:a
# 2:bb
# 3:ccc
```

`$_` exists only in line mode: `mzs -e 'defined($_)'` is `false`.

## As a condition

The value of the program is its last expression, and `--bool` turns that into an exit code:

```sh
mzs --bool -v '__sent=  OPERATOR ' -e '$__sent.lower.trim == "operator"'; echo $?   # 0
mzs --bool -v '__sent=yes'         -e '$__sent == "no"';                 echo $?   # 1
```

[`examples/09_host_variables.mzs`](../../examples/09_host_variables.mzs) builds a whole set of
such conditions and hands values back by assigning `$intent`, `$score` and friends; an
embedding host reads them from `Result.Globals`.

## See also

- [CLI](../cli/README.md) — every flag and the exit codes
- [Strings](./strings.md) — `$name` versus `${expr}` inside a literal
- [Values](./values.md) — the kinds a `--vars` JSON object can produce
- [Embedding: the Go API](../embedding/README.md) — binding and reading globals from Go
