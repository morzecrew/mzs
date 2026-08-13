# mzs

**A small scripting language for Morze Assistant**

[![CI](https://github.com/morzecrew/mzs/actions/workflows/ci.yml/badge.svg)](https://github.com/morzecrew/mzs/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/morzecrew/mzs/graph/badge.svg)](https://codecov.io/gh/morzecrew/mzs)

```sh
mzs -e '"  ОПЕРАТОР ".lower.trim == "оператор"'          # true
mzs -e '"привет".index(/вет/)'                           # 3   (the index is in RUNES)
mzs -e '(0..6).map { it * 2 }.each_slice(2).array' --json
```

Zero external dependencies (`go.mod` without a single `require`), no cgo, no subprocesses.
One `*Interp` serves any number of concurrent evaluations, every run is isolated, and both
the timeout and the step budget are on by default.

Why: in `morzebot-backend-v2` every dialogue condition went through
`exec.Command("ruby", "-e", …)` — about **45 ms** and a whole process for an expression like
`$__sent.lower.trim == "оператор"`. mzs evaluates the same thing **in the same process**,
with a cache of compiled programs:

```
BenchmarkCondition/equality-16                7215 ns/op    1016 B/op     8 allocs/op
BenchmarkCondition/lower_trim_equality-16     7867 ns/op    1112 B/op    12 allocs/op
BenchmarkCondition/re2_regex-16               7875 ns/op    1139 B/op    12 allocs/op
BenchmarkCondition/backtracking_regex-16      7898 ns/op    1152 B/op    13 allocs/op
```

About **8 µs** instead of 45 ms — roughly 5700 times faster, and without a second process
per message.

---

## Contents

1. [What kind of language this is](#what-kind-of-language-this-is)
2. [One-liner cheat sheet](#one-liner-cheat-sheet)
3. [Install and run](#install-and-run)
4. [CLI](#cli)
5. [A tour of the language](#a-tour-of-the-language)
6. [Async functions](#async-functions)
7. [Standard library](#standard-library)
8. [Regular expressions](#regular-expressions)
9. [Embedding in Go](#embedding-in-go)
10. [Migrating morzebot-backend-v2](#migrating-morzebot-backend-v2)
11. [Sandbox and limits](#sandbox-and-limits)
12. [What mzs is not](#what-mzs-is-not)
13. [Verification](#verification)
14. [License](#license)

---

## What kind of language this is

mzs takes the **approach** of the expressive scripting languages — everything is an
expression, the value of the last expression is the result, method chains instead of nested
calls, the one-liner as a first-class citizen — and that is where the borrowing ends. The
syntax is its own.

Three decisions shape everything else:

**`{ … }` is always a closure.** Not a block, not a dict, not "sometimes one and sometimes
the other". The constructs that take a body (`if`, `while`, `for`, `fn`, `match` arms) simply
call it for you.

```
if c { a } else { b }          xs.map { it * 2 }          double = { (n) -> n * 2 }
```

**`[ … ]` is always a collection.** An array or a dict, never a third thing.

```
[]        [1, 2, 3]        [:]        [name: "Ivan", price: 1500]
```

**`x.f(y)` is exactly `f(x, y)`** (UFCS). There are no "global functions" on one side and
"methods" on the other: `len(s)` and `s.len` are the same function, and so is your own:

```
fn shout(s) { s.upper + "!" }
"yes".shout                    # YES!
```

Plus `match` instead of the `if/else if` ladder, sixteen keywords, exactly one name per
operation, and not a single inherited precedence trap: what other languages read silently
and unexpectedly is a parser error with a fix-it here.

The full normative description is [`SPEC.md`](SPEC.md).

---

## One-liner cheat sheet

Every line is a complete program. Try them straight in the terminal: `mzs -e '<line>'`.

| What you need | One-liner | Result |
|---|---|---|
| Normalise a user's answer | `"  ОПЕРАТОР ".lower.trim == "оператор"` | `true` |
| Test against a regex | `"Привет!".lower ~ /привет\|hello/i` | `true` |
| Match index in **runes** | `"привет".index(/вет/)` | `3` |
| Throw the apostrophes out | `"O'Brien".replace(/'/, "")` | `OBrien` |
| Take `login:email` apart | `"ivan:i@x.ru".split(":")[1]` | `i@x.ru` |
| Spread a pair over names | `user, host = "ivan@x.ru".split("@"); host` | `x.ru` |
| First word | `"Ivan Petrov".split(" ").first` | `Ivan` |
| A number out of an empty string, no panic | `"".int + 1200` | `1200` |
| A number with trailing junk | `"12abc".int` | `12` |
| Word frequencies | `"yes,no,yes".split(",").tally.json` | `{"yes":2,"no":1}` |
| An intent router | `match s { in ["yes","yeah"] -> 1; /^no/ -> 0; else -> nil }` | |
| Even numbers | `[1,2,3,4].filter { it % 2 == 0 }` | `[2, 4]` |
| Fold | `[1,2,3].reduce(0) { (a, x) -> a + x }` | `6` |
| Chunk by two | `(0..6).map { it }.each_slice(2).array` | `[[0,1],[2,3],[4,5],[6]]` |
| Dig into JSON | `include json; json.parse(s).dig(0, "generated_text")` | |
| A default value | `[].first ?? "empty"` | `empty` |
| Do not fall over | `include json; try json.parse("{oops") else "broken json"` | |
| Keyboard buttons | `(0..5).map { [text: "${it}:00", data: "t:${it}"] }` | |
| Read a file | `include io; io.read("/etc/hostname").trim` | |
| Count the scripts next door | `include io; io.ls(".").filter { it.ends_with(".mzs") }.len` | |

---

## Install and run

```sh
git clone <repo> mzs && cd mzs
go build ./...
go install ./cmd/mzs          # puts the binary in $(go env GOPATH)/bin
```

```sh
mzs script.mzs                # run a file
mzs script.mzs one two        # the arguments arrive in $ARGV
cat script.mzs | mzs          # stdin, when it is not a terminal
mzs -e 'expression'           # a one-liner; the value is printed automatically
cat data | mzs -n -e '$_…'    # the program runs per input line, the line is in $_
mzs                           # the REPL, when stdin is a terminal
```

---

## CLI

| Flag | Meaning |
|---|---|
| `-e, --eval <src>` | run `<src>`; repeatable, joined with newlines |
| `-p, --print` | print the value of the last expression (on by itself for `-e`) |
| `--no-print` | never print the value |
| `--json` | print the value as JSON |
| `--bool` | exit code 0 on a truthy value, 1 on a falsy one; prints nothing |
| `-n, --for-each-line` | run the program for every input line; the line is in `$_` |
| `-l, --print-each-line` | the same, and print each line's value unless it is `nil` |
| `--in <path>` | take the data from a file rather than from stdin |
| `--io`, `--no-io` | grant or withhold the `io` module (granted by default) |
| `-v, --var k=v` | bind `$k` to the string `v`; repeatable |
| `--vars <json>` | bind every field of a JSON object as a `$var` (types are kept) |
| `--vars-file <path>` | the same, from a file |
| `-t, --timeout <d>` | timeout for one run (`1s` by default, `0` removes it) |
| `--steps <n>` | step budget (5,000,000 by default, `0` removes it) |
| `--tasks <n>` | how many `async fn` tasks may live at once (64 by default, `0` forbids them) |
| `--time` | enable the `time`/`date` modules and a real clock |
| `--rand [seed]` | enable `rand()`/`uuid()`; with a seed, reproducible |
| `--stats` | to stderr: how many steps and how long it took |
| `--tokens` | dump the token stream and exit |
| `--ast` | dump the AST and exit |
| `--check` | compile only: errors, warnings, approximated regexes |
| `--repl` | force the REPL |
| `-h, --help`, `--version` | help and version |

**Exit codes:** `0` success, `1` a run-time error (or a falsy value under `--bool`),
`2` a bad argument, `3` a timeout or an exhausted budget.

### Data: the pipe, `--in`, line mode

What arrives through the pipe is either the program or the data — never both at once: the
reader has one set of bytes. If the program already came from `-e` or from a file, stdin is
free and becomes data: you see it as `io.stdin`/`io.lines`. If it did not, stdin is the
program, exactly as `cat script.mzs | mzs` has always meant. `--in <path>` names the data
explicitly and always wins — which is what makes `cat script.mzs | mzs --in access.log -n`
meaningful: the program in the pipe, the data in the file. There may be no data at all, and
that is not an error: `io.stdin` is then `""`.

`-n` runs the program for every line of the data, and the line arrives in `$_`:

```sh
cat access.log | mzs -n -e '$_.split(" ")[0]' | sort | uniq -c   # the top addresses
mzs -n --in access.log --bool -e '$_ ~ /ERROR/' && echo "there are errors"
printf 'a\nbb\nccc\n' | mzs -n -e '"${$_}: ${$_.len}"'
```

This is a loop on the CLI's side, not a language mode: the same compiled program, started
once more. What follows from that:

* lines are read as they arrive, so `tail -f | mzs -n …` prints as it goes;
* every line is a separate run with its own timeout and its own step budget;
* `$variables` travel from line to line (`$n = ($n ?? 0) + 1` counts), locals do not: every
  run gets a fresh environment. When you need the whole input at once, that is `io.lines`;
* printing follows the ordinary rules: `-e` prints every line's value, a file stays quiet
  until asked, `--no-print` silences everything. `-l` is `-n` plus "print the value unless it
  is `nil`" — which is how a file speaks up too;
* under `-n` the CLI owns the reader, so `io.stdin` is empty — the line is in `$_`;
* an error on a line ends the run and says which line it was: `mzs: input line 7:`;
* `--bool` asks grep's question: `0` when at least one line came out truthy. Every line is
  still processed — a program's output must not depend on where the first hit was;
* `mzs -n` with no data (the program itself came from stdin) is an argument error, not a
  quiet run over zero lines; and so is `-n` with no program at all: the REPL is no substitute
  here.

`--no-io` withholds the whole `io` module — no files, no environment, no stdin: that is how
you run somebody else's script and see it the way an embedder will. It does not affect `-n`;
the CLI reads the lines, not the script.

Errors are printed to stderr with the position, the source line and a caret:

```
$ mzs testdata/one.mzs
testdata/one.mzs:3:6: syntax: unexpected '!' after '='; did you mean '!='?
  str =! "sdfsdf"
       ^
```

There is a separate diagnostic for every construct you might have brought from another
language — one precise fix-it instead of a cascade:

```
$ mzs -e 's =~ /re/'
-e:1:3: syntax: '=~' is not an mzs operator; use '~'

$ mzs -e '{a: 1}'
-e:1:1: syntax: a dict literal is written [a: 1]

$ mzs -e '0..5.map { it }'
-e:1:1: syntax: ambiguous range: write (0..5).map
```

`--check` additionally catches the classic "the regex came out of JSON" mistake:

```
$ mzs --check -e '"food" ~ /\\bfood\\b/'
-e:1:10: warning: "\\b" matches a literal backslash; did you mean "\b"? (pattern probably came from a JSON string)
```

### REPL

```
$ mzs
mzs 2.0.0 — .help for help, .exit to quit
mzs> s = "  HELLO "
"  HELLO "
mzs> s.lower.trim
"hello"
mzs> fn double(n) { n * 2 }
#<fn double>
mzs> double(21)
42
```

Local variables and functions live across lines. An unclosed construct keeps reading with
the `...> ` prompt, and an empty line cancels it.
Commands: `.help`, `.exit`, `.clear`, `.src`, `.vars`.

---

## A tour of the language

### Values

Nine kinds: `nil`, `bool`, `int`, `float`, `string`, `regex`, `array`, `dict`, `function`
(plus `time`, when the host has enabled the clock).

```
n   = 42            0xff    0b1010    1_000_000
f   = 1.5           1e9
s   = "hello"       'raw string'
re  = /hello|hi/i
xs  = [1, 2, "three"]
d   = [name: "Ivan", price: 1500]
e   = [:]                        # an empty dict; [] is an empty array
fun = { (x) -> x * 2 }
```

Strings are immutable and are indexed **by runes**. Dicts keep insertion order — both when
iterated and in JSON. An identifier key becomes a string, so `[name: 1]` serialises to
`{"name":1}` with no symbols anywhere.

Only `nil` and `false` are falsy. `0`, `""`, `[]`, `[:]` are truthy.

### Operators

```
+ - * / %  **           7 / 2 == 3        7.0 / 2 == 3.5        -(2 ** 2) == -4
== !=  < <= > >=  <=>
~  !~                   s ~ /menu/i       # a regex match, always true/false
&& || !                 ?? ?.             # ?? fires on nil only
.. ..<                  (0..5)  (0..<5)
= := += -= *= /= %= **= ||= &&= ??=
? :                     x > 3 ? "yes" : "no"
```

Integer division when both sides are `int`; `%` takes the sign of the divisor. An `int`
overflow is promoted to `float` rather than wrapped — except in the bit functions, which
stay in `int64` (see Numbers below; `&`, `|` and `^` are not operators here).

There are no conversions: `"2" + 1` is an error, write `"2".int + 1`. The one implicit
conversion is string interpolation.

### Control flow

All of these are expressions; every one has a value.

```
if c { a } else if d { b } else { c }

while x < 5 { x += 1 }
for x in xs { say(x) }
for k, v in d { say("${k}=${v}") }

x = 1 if c              # modifiers
x += 1 while x < 5
```

`match` replaces the `if/else if` ladder and works on one line:

```
intent = match s {
  in ["yes", "yeah", "sure"]   -> "confirm"
  /\boperator|transfer/i       -> "handoff"
  in 1..5                      -> "small"
  s.len > 500 if verbose       -> "too_long"
  else                         -> "unknown"
}
```

A literal is compared with `==`, a regex is matched, `in` tests membership (an array, a
range, a dict's keys, a substring). Several patterns separated by commas mean "or", and an
`if` after the patterns is an additional condition. The subject is evaluated once.

An arm written `[x, y]` is not a comparison but a decomposition: it fires when the subject
is an array of that length, and it binds the names inside the arm (see "Destructuring").

With no subject, every pattern is simply a condition:

```
match {
  yes            -> "confirm"
  s ~ /operator/ -> "handoff"
  else           -> "unknown"
}
```

### Destructuring

One shape rule in three places: the assignment, the `match` arm, and `for` with two
variables.

```
a, b = pair                  # an array of exactly two
[a, [b, c]] = [1, [2, 3]]    # the brackets may be written; nesting is recursive
a, b, c = 1..3               # a range has positions too
d["x"], $y = pair            # an index and a $var are targets like any other
a, b = [b, a]                # a swap needs no temporary
for k, v in d { … }          # the same rule, with the pattern in the loop header
```

The lengths must match: `a, b = [1, 2, 3]` and `a, b = 1` are **run-time errors**, not a
silent `nil` in the extra name. Arrays and ranges have positions; a dict does not (key order
is not a positional contract), so a dict is taken apart with `d["x"], d["y"] = …` or with
`for k, v in d`. There is no rest element (`a, *rest`).

In a `match` arm the very same shape asks instead of asserting: a subject of another kind or
another length is simply the next arm, not an error. A name binds, a literal compares:

```
match order {
  [x, [y, z]] -> x + y + z
  [0, n]      -> n            # 0 is compared, n is bound
  [x, y] if x > y -> "desc"   # the guard sees the bound names
  else        -> -1
}
```

### Functions and closures

Parameters are always parenthesised — for a named function and for a closure alike.

```
fn add(a, b) { a + b }
fn greet(name, greeting = "Hello") { "${greeting}, ${name}!" }
fn sum(*nums) { nums.sum }

add2   = { (a, b) -> a + b }
double = { (n) -> n * 2 }
xs.map { it * 2 }                 # with no parameter list the implicit `it` is there
```

`return` is optional — the last expression is the value. A closure is an ordinary value,
which is why "a block" and "a function argument" are the same thing:

```
[1, 2, 3].map(double)             # exactly the same as
[1, 2, 3].map { it * 2 }          # a trailing closure is just the last argument
```

Every `{ … }` is a scope of its own: a variable first created inside is not visible outside.
Assigning to an existing variable works as usual.

### Strings

```
"ordinary: \n \t ❤ \$"
'raw: \n stays two characters, which is handy for regexes'

"Your address is $__sent?"        # $name is a host global
"Total: ${price + 1200} €"        # ${…} is any expression
```

`$name` inside a string means exactly what it means outside — a host variable. A local
variable needs the braces: `"${n}:00"`. The dollar is escaped as `\$`. Inside
`'single quotes'` there is no interpolation at all.

### Errors

```
v = try json.parse(s) else [:]
v = try risky() else (e) -> "did not work: ${e["message"]}"

raise("that is not allowed")
assert(x > 0, "x must be positive")
```

`try` catches script errors only. A timeout, the step budget and a cancelled context are
never caught — they go to the host.

### `$variables`

```
$__sent                           # a read; an unbound variable is nil
$counter = $counter.int + 1       # a write; the host picks it up from Result.Globals
```

Values arrive from the host as **strings** and are never parsed, so spaces, apostrophes
(`O'Brien`) and emoji inside a value are safe. They enter arithmetic through `.int` /
`.float`, which never fail: `"".int == 0`.

---

## Async functions

`async fn` is an ordinary function with one difference: calling it **does not evaluate the
body** but starts a **task** and hands its value back immediately. The task is read with
`await`.

```
include http

async fn fetch(u) { http.get(u)["body"] }

a = fetch(url1)                      # the request is already on the wire
b = fetch(url2)                      # and so is the second one, alongside the first
[a.await, b.await]                   # and here is where we read them
```

A task has exactly two names — and through UFCS they work as `await(t)` and `done(t)` too:

| Name | What it does |
|---|---|
| `t.await` | wait and return the body's value; an error from the body is raised here, at the `await` |
| `t.done` | has the task finished — the only question you may ask a task without waiting |

A fan-out of requests is an ordinary `map`:

```
async fn get(u) { http.get(u)["body"] }
urls.map { get(it) }.map { it.await }     # N slow requests cost about as much as one
```

**Why this is safe.** A task is a goroutine, but **one goroutine evaluates mzs code at a
time**: a task holds the run's lock and releases it exactly where it could not carry on
anyway — at an `await`, inside a blocking host call (the `http` client), and when it ends.
So two tasks never touch one array, one dict or one `$var` at the same time: **a data race
cannot be written**, not even on purpose. What overlaps is the waiting — which is the whole
point.

There is no preemption: a task that only computes runs to the end as soon as it has the
lock. Which of the ready tasks gets it next is undefined, though, so two `say`s from two
tasks may print in either order.

**Limits and the end of the run.** The step budget, the deadline and the context are shared
by the whole run: a task's steps are the run's steps, and waiting wakes up on the deadline,
so a task waiting for something that will not come fails on time rather than hanging
forever. A task nobody awaited still finishes — `notify(user)` on the last line is a use,
not a mistake — and the run does not return until it has: **no goroutine outlives its
`Run`**. An unawaited task's error goes to `Stderr` instead of being lost.

```
$ mzs -e 'async fn boom() { raise("crashed") }
boom()
"alive"'
mzs: task 'boom' failed and was never awaited: -e:1:19: raise: crashed
alive
```

A task's error is caught where it is awaited: `try t.await else "a fallback answer"`. A task
awaiting itself is a clear error, not a hang. How many tasks live at once is bounded by
`MaxTasks` (`--tasks`, 64 by default).

In full — [`SPEC.md` §8.14](SPEC.md).

---

## Standard library

One flat namespace: every row of the tables below can be called as `f(x)` and as `x.f`
alike. There are no aliases — every operation has exactly one name.

### Core

`print` `say` `debug` `len` `empty` `type` `is` `str` `int` `float` `bool` `array` `dict`
`json` `inspect` `hash` `dup` `tap` `pipe` `regex` `range` `sum` `min` `max` `abs` `round`
`ceil` `floor` `sort` `format` `raise` `assert` `defined` `rand` `uuid` `now`

### Strings

`lower` `upper` `capitalize` `swapcase` `trim` `trim_start` `trim_end` `chomp` `chop`
`squeeze` `len` `empty` `blank` `has` `starts_with` `ends_with` `index` `last_index` `count`
`split` `replace` `replace_first` `matches` `captures` `chars` `bytes` `lines` `reverse`
`first` `last` `ljust` `rjust` `center` `slice` `ord` `each_char` `%`

### Arrays

`len` `empty` `count` `first` `last` `push` `pop` `shift` `unshift` `insert` `delete`
`delete_at` `has` `index` `join` `map` `each` `each_with_index` `each_slice` `each_cons`
`filter` `reject` `find` `any` `all` `none` `reduce` `sum` `min` `max` `min_by` `max_by`
`sort_by` `group_by` `partition` `sort` `reverse` `uniq` `flatten` `flat_map` `dig`
`compact` `tally` `slice` `take` `drop` `take_while` `drop_while` `zip` `concat`
`pack_bytes` `sample` `shuffle` `sort_in_place` `reverse_in_place`

### Dicts

`keys` `values` `len` `empty` `has` `has_val` `get` `fetch` `set` `delete` `merge`
`merge_in_place` `dig` `each` `map` `filter` `reject` `find` `any` `all` `invert` `sort_by`

### Numbers

`int` `float` `str` `abs` `round` `ceil` `floor` `clamp` `zero` `positive` `negative`
`even` `odd` `times` `upto` `downto` `step` `pow` `chr`

Bits: `band` `bor` `bxor` `bnot` `shl` `shr` `popcount` `bit`. They are functions rather
than operators — `&` next to `&&` is the kind of near-miss this language refuses, so
writing `a & b` gets you a diagnostic naming `band` — and they are pure `int64`: `shl`
drops the bits it pushes past the top instead of promoting to a Float the way `*` does.
`bytes` takes a string apart into numbers and `pack_bytes` puts it back.

```
flags = bor(READ, WRITE)          # 0b0011
flags.bit(1)                      # true — WRITE is set
flags.band(bnot(WRITE))           # clear it again
"\x01\x02".bytes.reduce(0) { (a, b) -> a.shl(8).bor(b) }   # 258
```

### Ranges and regexes

Range: `len` `array` `each` `map` `filter` `reject` `has` `first` `last` `min` `max`
`sum` `step` `each_slice` `reverse` `reduce`.
Regex: `captures` `matches` `index` `source` `flags` `str` — the match itself is the `~`
operator.

### Modules

Ordinary values in the root scope; no constants and no `::`.

Nothing is available "by itself": a name enters the program only through `include`.

```
include json
include math

json.parse(s)     json.pretty(x)     x.json         # encoding is a method from §12.1
math.sqrt(2)      math.pi
```

| Module | What the host must provide |
|---|---|
| `json`, `math`, `http` | nothing |
| `time`, `date` | `--time` (`EnableTime`, plus `Now` for the clock) |
| `io` | `Options.FS` — the CLI installs one itself, an embedding host has to write it |

Without the `include` it is a compile error with the fix-it ready, not a puzzling "no such
method":

```
$ mzs -e 'json.parse("{}")'
-e:1:1: name: 'json' is a module: add `include json` at the top of the file
```

**A module cannot be called.** `include json` binds the module — and from that point on
`json(x)` is a compile error that names both replacements at once: a member of the module
(`json.parse(s)`) or the `x.json` method, which is the very same §12.1 function, only
written so it cannot be mistaken for the module. Calling a name is always calling a
function; reaching into a module is always a member name:

```
$ mzs -e 'include json
say(json([total: 1500]))'
-e:2:5: name: 'json' is a module, not a function: call one of its members (json.parse,
json.pretty); the 'json' function is written 'x.json'
```

The same holds for a module of your own, which has no "twin function" at all: `cart(order)`
after `include cart from "./cart.mzs"` is the same error at compile time.

The full signatures are in [`SPEC.md` §12](SPEC.md).

### Your own modules: include and export

A neighbouring script is included by the same line, only with a path:

```
include cart from "./cart.mzs"

cart.total(order)
```

A module hands out exactly what is marked `export` — inline, or later, by name:

```
export rate = 0.2                      # inline, in front of the assignment
export fn total(items) { … }           # inline, in front of the declaration
helper = { (x) -> x * 2 }
export helper                          # or on a line of its own, by name
sep = " "                              # without export it is private and invisible outside
```

A module's value is a dict of the exported names: `cart.keys` shows what it can do, and
`defined(cart.sep)` is `false`. Exporting a name that does not exist is a compile error.

A file run directly simply ignores its own `export`s: one and the same file is both a
program and a module. A three-file example is
[`examples/27_modules_main.mzs`](examples/27_modules_main.mzs).

A module is evaluated inside the same run: the same `$vars`, the same step budget, the same
deadline; local names are its own. It is loaded **once per run** (the diamond `a → util ← b`
evaluates `util` once), and a cycle is a named error rather than a hang.

Reading the file is the host's job: `Options.ModuleLoader`, absent by default, and then
`include x from "…"` is an error. The CLI installs a file loader itself: the path is
resolved relative to the including file and cannot leave the directory of the program that
was started.

### A web server

`http` is the only module that reaches outside the process. It requires no flag: like
`json`, it is simply there, and like any module it requires an `include`. Without that line
the name `http` does not exist in the program at all. A host that does not want the network
takes the name away with one line — `in.Unregister("http")` — and then a condition out of
the dialogue store will not open a socket.

```
include http

http.serve(":8080", [
  "GET /hello":    { (req) -> "hello, " + (req["query"]["name"] ?? "world") },
  "GET /get/{id}": { (req) -> http.json([id: req["params"]["id"].int]) },
])
```

```sh
mzs examples/30_http_service.mzs
```

A route key is a `net/http` pattern (Go 1.22): the method, the path and the `{name}`s, which
arrive in `req["params"]`. A miss on the path is a 404 and a miss on the method is a 405;
neither reaches the closure. The handler receives
`[method:, path:, params:, query:, headers:, body:, host:, remote:]`, and whatever it
returns is what goes to the client:

| Returned | Response |
|---|---|
| `nil` | 204, no body |
| a string | 200, `text/plain` |
| a dict with a numeric `status` | that status, its `body` and `headers` |
| anything else | 200, `application/json` |

`http.json(body, status, headers)` and `http.text(…)` build the third form, and
`http.stop()` stops the server from inside a handler.

Handlers run on the same goroutine as `http.serve`: a run's state belongs to one run (§10),
so requests are served one at a time. Waiting for a connection costs no steps, and every
request gets a fresh `StepBudget` and a fresh deadline — a handler that fails or hangs costs
one `500` response, not the server. If you need parallelism, start several runs, each with
its own listener, or leave HTTP in Go and call mzs per request.

The client is built just as plainly: `http.get(url)`, `http.post(url, body)`,
`http.request(method, url, [body:, headers:, timeout:])` return
`[status:, body:, headers:]`. A non-2xx status is a value, not an error; an unreachable
service is an ordinary error and `try http.get(u) else …` catches it; but the run's
exhausted time is a limit, and that is never caught.

### Files, stdin and the environment

`io` is the second and last part of the library that reaches outside the process, and it is
built the other way round from `http`: the network is always there, the files only if the
host gave `Options.FS`. The CLI does give it: the CLI *is* the host, and whoever types the
command owns the machine anyway.

```
include io

io.stdin                          # all of stdin, read once per run
io.lines                          # the same, line by line
io.read(path)                     # a file as a string
io.write(path, s)                 # overwrite, return the byte count
io.append(path, s)                # append
io.exists(path)   io.ls(dir = ".")
io.env("HOME", "/tmp")            # empty or unset gives the default
```

```sh
mzs -e 'include io; io.read("/etc/hostname").trim'
mzs -e 'include io; io.ls(".").filter { it.ends_with(".mzs") }.len'
ls | mzs -e 'include io; io.lines.filter { it.ends_with(".mzs") }.len'
```

The CLI is what puts data into `io.stdin`, by the rules of "the pipe, `--in`, line mode"
above: stdin becomes data once the program has come from somewhere else, and `--in <path>`
names it explicitly. Line-by-line work is more comfortably written with `-n`, where the line
arrives in `$_`; `io.lines` is for when you need the whole input at once.

There are no other members: no `io.rm`, no `io.mkdir`, no `io.open` — a script reads a file,
writes a file and looks at a directory, and the rest is the host's business or the shell's.

**The host resolves paths.** The module joins nothing and normalises nothing: what the
script wrote is what arrives at `Options.FS` — and whether `"../../etc/shadow"` may be read
is decided by the code that implemented the interface. Exactly as with `include … from` and
`ModuleLoader`. The library itself contains no `FileSystem` implementation at all; only the
CLI has one.

A miss is an ordinary error with the path in its text, and `try io.read(p) else ""` catches
it. `Options.Stdin` and `Options.Env` are separate capabilities inside the module: without
them `io.stdin` is `""` and `io.env` is `nil`, but neither is an error — so one and the same
script works both in a pipe and without one.

An example over three operations — [`examples/32_io_files.mzs`](examples/32_io_files.mzs).

---

## Regular expressions

Two engines behind one interface; the choice is made once, at compile time:

* **RE2** (`regexp` from the stdlib) whenever the pattern allows it — linear time, no
  surprises;
* the **built-in backtracking engine** for `\b`/`\B`, lookahead/lookbehind, backreferences
  and possessive quantifiers — with a step budget, so catastrophic backtracking becomes an
  error rather than a hang.

What matters, and what differs from Go's `regexp`:

* `^` and `$` are **always** line anchors, and `\A`/`\z` are the whole string;
* `\b` is **Unicode**: `/\bменю\b/i` really does match Cyrillic, which Go's ASCII `\b`
  silently never does;
* `i` folds case by Unicode, including `И`/`и` and `Ё`/`ё`;
* every index is in **runes**: `"привет".index(/вет/)` is `3`, not `6`.

```
"нужна CRM" ~ /\bcrm\b|црм/i          # true
"Продолжить" ~ /^(?!❌ Отмена).*$/     # true — a negative lookahead
```

Flags: `i` `m`/`s` (the dot matches a newline) `x` (extended) `u` (a no-op).
A dynamic pattern is `regex('\bменю', 'i')`; raw single quotes save you the double escaping.

---

## Embedding in Go

```go
import "mzs"

in := mzs.New(mzs.Options{
    Timeout: time.Second,
    Stdout:  os.Stdout,
})

prog, err := in.Compile("cond#12", `$__sent.lower.trim == "оператор"`)
if err != nil { return err }

v, err := in.Run(ctx, prog, map[string]mzs.Value{
    "$__sent": mzs.Str("  ОПЕРАТОР "),
})
// v.Truthy() == true
```

A `*Program` is immutable and goroutine-safe — compile once, run from as many goroutines as
you like. Mutable state lives only in the frame of a particular run.

```go
res, err := in.RunResult(ctx, prog, vars)
res.Value     // the value
res.Globals   // the final state of $vars — this is how set_var blocks work
res.Steps     // how many steps it took
res.Elapsed
```

The quick path without compiling by hand (an LRU of compiled programs inside):

```go
v, err := in.Eval(ctx, `$price.int + 1200`, map[string]mzs.Value{"$price": mzs.Str("800")})
```

### Your own functions

```go
in.Register("http_get", 1, func(c *mzs.Ctx, args []mzs.Value) (mzs.Value, error) {
    body, err := fetch(c.Context(), args[0].Str())
    if err != nil { return mzs.Nil(), c.Errorf("http_get: %v", err) }
    return mzs.Str(body), nil
})

in.RegisterModule("bitrix", map[string]mzs.Value{
    "version": mzs.Str("24"),
})

in.SetGlobal("$env", mzs.Str("prod"))   // a default value for every run
```

A registered function is immediately available as a method too: `"...".http_get` is UFCS,
not a second registration.

Registration happens during setup only, before the first `Run`.

### Your own access to files

The `io` module appears only if the host gave `Options.FS`, and the same host writes the
policy — the library holds no code that opens a file:

```go
type FileSystem interface {
    Open(name string) (io.ReadCloser, error)
    Create(name string) (io.WriteCloser, error)   // overwrite or create
    Append(name string) (io.WriteCloser, error)   // create when absent
    Stat(name string) (exists bool, size int64, dir bool, err error)
    List(dir string) ([]string, error)
}

in := mzs.New(mzs.Options{
    FS:    myVirtualFS{},   // one directory, or a map in memory
    Stdin: os.Stdin,        // io.stdin / io.lines; nil is empty input
    Env:   os.Getenv,       // io.env; nil means everything is unset
})
```

The name arrives at the interface exactly as the script wrote it: no normalisation happens
before your code, which is why your check for escaping the directory both is yours and
works. An error from a method becomes an ordinary script error, and `try` catches it.

### Values

```go
mzs.Nil() mzs.Bool(b) mzs.Int(i) mzs.Float(f) mzs.Str(s)
mzs.Array(elems...) mzs.Dict(k1, v1, k2, v2) mzs.Fn(name, arity, f)
mzs.From(anyGoValue)     // reflection: scalars, slices, map[string]T, structs

v.Kind() v.Truthy() v.Int() v.Float() v.Str() v.Inspect() v.Len()
v.Index(i) v.Get(key) v.Set(key, val) v.Append(vals...) v.Keys() v.Elems()
v.Interface()            // back into Go
v.MarshalJSON()
```

Every accessor is total: reaching for the wrong kind of value returns the zero value, but
never panics.

### Errors

```go
var e *mzs.Error
if errors.As(err, &e) {
    e.Kind   // "syntax" | "name" | "type" | "argument" | "index" |
             // "zero-division" | "regex" | "raise" | "limit" | "internal"
    e.File; e.Line; e.Col; e.Stack; e.Data
}
errors.Is(err, mzs.ErrTimeout)   // ErrBudget, ErrDepth, ErrCanceled
```

---

## Migrating morzebot-backend-v2

mzs is not Ruby-compatible and has no legacy dialect. The conditions sitting in the flow
store are rewritten **once**, mechanically — and that is cheap, because the corpus consists
of two shapes: 272 records, 107 unique, of which ≈133 are `X == 'literal'` (valid as they
stand) and ≈136 are `X.downcase(.strip) =~ /re/i` (three substitutions per record). The rest
is three expressions done by hand.

The full substitution table, the three hand-migrated cases, the host-side changes and the
cutover order are in [`SPEC.md` §19](SPEC.md). In short:

```go
// pkg/engine/eval/eval.go — throw out exec.Command("ruby", …)
var eng = engine.New(engine.Options{Timeout: time.Second})

func Bool(ctx context.Context, expr string, vars map[string]string) (bool, error) {
    return eng.Bool(ctx, expr, vars)
}
func String(ctx context.Context, expr string, vars map[string]string) (string, error) {
    return eng.String(ctx, expr, vars)
}
```

`condition.go` does not change: any error still means "the condition did not fire".
`Translate` is gone — values are bound rather than substituted into text, and that is
exactly what fixes the long-broken conditions over values such as `Стрижка c фейдом`,
`EN 🇬🇧` and `О'Брайен`.

The cutover takes two steps: a week of shadow mode logging the divergences, then removing
the Ruby path and running the codemod over the live store.

---

## Sandbox and limits

A script has no access to processes, to time or to randomness — and none to files and the
environment until the host hands them over. No `require`, no `import`, no `load`, no
`system`, no `File`, no `ENV`, no `eval`: a script cannot even compile new source on the
fly. Everything else comes from the host alone, by name, through `Register`.

Files and somebody else's source are built the same way and are both off by default:
`Options.FS` (the `io` module) and `Options.ModuleLoader` (`include … from`) are interfaces
the host writes, so what a path may mean is decided by the host's code. Without them
`include io` and `include x from "…"` are compile errors naming the missing field. The
library itself holds no code that opens a file: the only `FileSystem` implementation in the
repository belongs to the CLI, where the host is the person who typed the command. That is
why `mzs -e 'include io; io.read("/etc/hostname")'` works, while the same program inside a
bot evaluating conditions from a store cannot so much as name the module.

The network is the one exception: the `http` module is always installed and has no flag.
That is a deliberate trade: the CLI's job is scripts that speak HTTP and answer requests,
and there is no point paying for it with a flag on every command. An interpreter that
evaluates somebody else's expressions takes the name back with `in.Unregister("http")` — and
the module is gone. Everything else — the limits in the table below — applies to `http` too.

| Setting | Default | What it bounds |
|---|---|---|
| `Timeout` | 1 s | wall-clock time of one run |
| `StepBudget` | 5,000,000 | interpreter steps |
| `MaxDepth` | 200 | call depth, so the Go stack cannot overflow |
| `MaxTasks` | 64 | how many `async fn` tasks live at once (`-1` forbids them entirely) |
| `MaxCollection` | 1,000,000 | elements materialised by one operation |
| `MaxStringBytes` | 8 MiB | the size of one constructed string |
| `RegexSteps` | 200,000 | the backtracking budget for one match |
| `RegexCacheSize` | 256 | the regex compilation cache per `*Interp` |
| `ProgramCache` | 512 | the cache of compiled sources for `Eval` |
| `Now`, `Rand` | `nil` | the clock and randomness are off — evaluation is deterministic |
| `ModuleLoader` | `nil` | `include … from "path"` is off: other sources are not read |
| `FS` | `nil` | there is no `io` module at all: files are neither read nor written |
| `Stdin`, `Env` | `nil` | `io.stdin` is `""`, `io.env` is `nil` |

The limit checks sit inside the node-walking loop, so `while true { }` and a pathological
regex are interrupted **in the middle** of an iteration rather than between statements.
There is no timer per run and no goroutine — beyond the ones the script started itself with
`async fn`; those share the run's budget, deadline and context, and the run does not end
until they have. Limits are not caught by `try`.

A script cannot bring the host down: an internal panic is recovered at the `Run`/`Eval`
boundary and returned as an error with `Kind == "internal"`.

---

## What mzs is not

Not Ruby, not Kotlin, not Go. Coincidences in spelling are coincidences; behaviour is
defined by [`SPEC.md`](SPEC.md), not by intuition from another language.

Absent, and not planned for 2.0: user classes, `method_missing`, modules and mixins,
`require`, mutable strings, symbols, heredocs, `case/when`, `*splat` at a call site,
compatibility modes and dialects of any kind. Shared-memory parallelism is out too: `async
fn` gives concurrency (tasks wait at the same time), but two pieces of mzs code never
execute at once, so there are no locks and no memory model to learn.

Reserved for later versions (a parse error today, so that nothing breaks then): `@ivar`,
`class`, `module`, `import`, `yield`, `defer`, `|>`, `**kwargs`, and the destructuring rest
element `a, *rest = xs` — which will arrive together with `*splat` or not at all.

---

## Verification

```sh
gofmt -l .          # silent
go vet ./...
go test -race ./...
mzs --check examples/*.mzs
```

The acceptance suite is [`corpus_test.go`](corpus_test.go): real conditions from production
flows after the migration ([`SPEC.md` §16.1](SPEC.md)), the regex corpus (§16.2), the
author's own files (§16.3), a test for every known gotcha (§16.4) and for every diagnostic
of §5.6.

The same four commands run on every push and pull request
([`.github/workflows/ci.yml`](.github/workflows/ci.yml)), and the coverage profile goes to
[Codecov](https://codecov.io/gh/morzecrew/mzs). Locally:

```sh
go test -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

Examples: thirty-four complete programs in [`examples/`](examples/README.md) — from the
value model and `match` to BFS through a maze, a FIFO warehouse, `async fn` and an HTTP
service. Each one runs on its own (`mzs examples/11_log_parser.mzs`) and prints a report.
The author's files from §16.3 are not examples; they live in [`testdata/`](testdata/).

---

## License

MIT — see [`LICENSE`](LICENSE).
