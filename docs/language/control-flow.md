# Control flow

`if`, `while`, `for`, the statement modifiers and `match` — all of them expressions, each
with a value you can assign.

## if

```
if true { "a" } else { "b" }                                 # a
x = if 3 > 5 { "big" } else if 3 > 2 { "mid" } else { "sm" } # mid
inspect(if false { 1 })                                      # nil
```

`else if` is two words; `elsif` is not a keyword. Only `nil` and `false` are falsy
([./values.md](./values.md)).

A condition may ask about membership with `in` — a range, an array, a dict's keys or a
string's substrings ([./operators.md](./operators.md)):

```
code = 250
if code in 200..<300 { "ok" } else { "failed" }              # ok
if name in ["да", "yes"] && !(name in blocked) { … }
```

## while

The value is `nil` unless a `break` supplies one.

```
i = 0; while i < 3 { i += 1 }; i                     # 3
inspect(while false { 1 })                           # nil
n = 0; while n < 5 { n += 1; break "stop" if n == 3 } # stop
```

## for

`for x in xs` over an array, a range, a string (rune by rune) or a dict (as `[key, value]`
pairs); `for k, v in xs` destructures each item, which is the form a dict is read with.

```
for x in 1..3 { say(x) }              # prints 1, 2, 3
for ch in "abc" { say(ch) }           # prints a, b, c
for k, v in {a: 1, b: 2} { say("${k}=${v}") }   # prints a=1, b=2
```

The value of a `for` is the thing it iterated, and `for k, v` insists on a pair per item:

```
inspect(for x in [1,2,3] { x })       # [1,2,3]
for i, ch in "ab".chars { say(ch) }
# type: cannot destructure string: a two-variable 'for' takes an array of two per item
```

## break and next

`break` and `next` are the keywords — there is no `continue`. Both take an optional value.
`next` ends one iteration or one closure invocation; `break` ends the loop, or the call the
closure was passed to.

```
n = 0; for x in [1,2,3,4] { next if x.even; n += x }; n   # 4
r = for x in [1,2,3] { break x * 10 if x == 2 }; r        # 20
[1,2,3].each { break it if it == 2 }                      # 2
[1,2,3].map { next 0 if it.even; it }                     # [1,0,3]
i = 0; while true { i += 1; break if i > 2 }; i           # 3
```

## Statement modifiers

Only `if` and `while`, and they bind loosest of everything.

```
i = 0; i += 1 while i * i < 500; i          # 23
xs = []; xs.push(1) if 2 > 1; xs            # [1]
```

A modifier body is a body, so it is a scope: a variable that only ever appears there does
not survive it.

```
total = 1 if true; total              # name: undefined variable 'total'
total = 0; total = 1 if true; total   # 1
```

## match

```
match Subject {
  Pattern [, Pattern…] [if Guard] -> Expr
  else -> Expr
}
```

Arms are separated by a newline or `;`, so a whole `match` fits on one line. Arms are tried
top to bottom, the first hit wins, and the value of the `match` is the value of that arm.

| Arm | Fires when |
|---|---|
| a literal or any expression | `subject == it` |
| a regex | `subject ~ it` |
| `in expr` | `expr.has(subject)` — array, range, dict keys, or substring |
| `if expr` | `expr` is truthy; the subject is not consulted |
| `[p, …]` | the subject is an array or range of that length; bare names bind |
| `else` | always; last arm only |

```
match 3 { 1 -> "one"; 3 -> "three"; else -> "?" }         # three
match "HI" { /^h/i -> "greet"; else -> "?" }              # greet
match "yes" { in ["yes","ok"] -> 1; else -> 0 }           # 1
match 7 { in 1..5 -> "small"; in 6..10 -> "mid"; else -> "big" }   # mid
match "b" { in {a: 1, b: 2} -> "key"; else -> "no" }      # key    dict keys
match "menu" { in "main menu here" -> "sub"; else -> "no" } # sub  substring
v = 2; match 4 { v * 2 -> "double"; else -> "?" }         # double
inspect(match 9 { 1 -> "one" })                           # nil    no arm, no else
```

Commas mean "or", and every pattern in one arm must use the same form:

```
match 5 { 1, 3, 5 -> "odd"; else -> "even" }              # odd
match "abc" { /^a/, /z/ -> "hit"; else -> "?" }           # hit
match "abc" { in ["x"], in ["abc"] -> "hit"; else -> "?" }  # hit
match "abc" { in ["a"], /c$/ -> "hit"; else -> "?" }
# syntax: every pattern in one arm must use the same form
```

### Guards

`if Guard` after the patterns is an extra condition. A bare name is *not* a binding here —
it is an ordinary expression compared with `==`:

```
n = 7; match n { in 1..10 if n.odd -> "small odd"; else -> "?" }   # small odd
match 5 { limit if limit > 3 -> "big"; else -> "small" }   # name: undefined variable 'limit'
```

Use an array pattern to bind, or `match` with no subject.

### Array patterns

An array pattern decomposes instead of comparing. It must be the only pattern in its arm,
and the names it binds live in that arm alone.

```
match [1,2]   { [x, y] -> x + y; else -> 0 }              # 3
match [1,[2,3]] { [x, [y, z]] -> x+y+z; else -> 0 }       # 6
match [0, 7]  { [0, n] -> n; else -> -1 }                 # 7   a literal still compares
match [3,1]   { [m, n] if m > n -> "desc"; else -> "asc" } # desc
match 1..2    { [a, b] -> a + b; else -> 0 }              # 3   a range has positions
match [1,2,3] { [a,b] -> "two"; [a,b,c] -> "three"; else -> "?" }   # three
match 2       { [x] -> x; else -> "no" }                  # no  wrong kind, not an error
match [1,2] { [left, right] -> left + right; else -> 0 }; inspect(left)
# name: undefined variable 'left'
match [1] { [x], [y] -> x; else -> "no" }
# syntax: an array pattern must be the only pattern in its arm
```

More in [./destructuring.md](./destructuring.md).

### match with no subject

Every pattern is then simply a condition — this is the full replacement for an `if`/`else if`
ladder.

```
match { 1 > 2 -> "a"; 2 > 1 -> "b"; else -> "c" }         # b
s = "call operator"; match { s ~ /operator/ -> "handoff"; else -> "?" }   # handoff
```

### The subject is evaluated once

```
c = [0]
fn bump() { c.push(1); "yes" }
m = match bump() { "no" -> 1; "yes" -> 2; else -> 3 }
"${m} ${c.len}"      # 2 2
```

Two arms were tested, `c` grew by exactly one element.

## Headers and grouping

Inside the header of `if`, `while`, `for` or `match`, the first `{` at bracket depth 0 opens
the body. A trailing closure in a header therefore has to be parenthesised — and `( a; b )`
is itself an expression worth the value of its last statement.

```
xs = [1,2,3]; if xs.any { it > 2 } { "yes" }      # syntax: unexpected '{' after statement
if (xs = [1,2,3]; xs.any { it > 2 }) { "yes" }    # yes
if {a: 1}.has("a") { "yes" }                      # yes — a dict literal needs no parens
```

## See also

- [./destructuring.md](./destructuring.md) — array patterns in all three places
- [./functions.md](./functions.md) — `return`, closures, scope
- [./operators.md](./operators.md) — where the modifiers sit in the precedence table
- [`examples/02_control_flow.mzs`](../../examples/02_control_flow.mzs), [`examples/03_match_dispatch.mzs`](../../examples/03_match_dispatch.mzs)
