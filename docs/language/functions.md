# Functions and closures

`fn` declarations, `{ … }` closures, and the two rules that make them one thing: a trailing
closure is the last argument, and `x.f(y)` is `f(x, y)`.

## `fn`

```
fn add(a, b) { a + b }
add(2, 3)                                  # 5
```

Parameter lists are always parenthesised. `return` is optional — the value is the last
expression evaluated; `return` exists for early exit.

```
fn classify(n) {
  return "negative" if n < 0
  n == 0 ? "zero" : "positive"
}
```

**Top-level** `fn`s are hoisted, so order does not matter and mutual recursion works.

```
x = a(); fn a() { "hoisted" }; x           # hoisted
fn f() { g() }; fn g() { 7 }; f()          # 7
```

A `fn` written inside any `{ … }` is an ordinary declaration in that scope: visible from that
point on, and gone afterwards. Both `if true { fn g() { 1 } }; g()` and
`fn f() { g(); fn g() { 1 } }` report `name: undefined function 'g'`.

## Anonymous functions: `fn(…) { … }` and `(…) -> { … }`

Leave the name out and the `fn` is an expression: a value that is not hoisted and binds
nothing, so the only way to reach it is the value itself.

```
add = fn(a, b) { a + b }
add(2, 3)                                  # 5
fn(x) { x * 3 }(5)                         # 15 — called where it stands
[1, 2, 3].map(fn(x) { x * 2 })             # [2,4,6]
ops = {add: fn(a, b) { a + b }}; ops["add"](1, 2)      # 3
```

The same function has a second spelling, with the keyword dropped and an arrow in its
place. The parameters sit outside the braces either way, so the braces are the **body**:

```
add = (a, b) -> { a + b }
add(2, 3)                                  # 5
(x) -> { x * 3 }(5)                        # 15
[1, 2, 3].map((x) -> { x * 2 })            # [2,4,6]
```

The body is braced. `(x) -> x * 2` is a diagnostic naming both replacements, because the
braceless arrow already means the closure below:

```
f = (x) -> x * 2
# -e:1:9: syntax: an arrow function's body is braced: (x) -> { x * 2 }, or write the closure { (x) -> x * 2 }
```

Two places read `(…) ->` before this rule does and keep their own meaning: the header of an
`if`/`while`/`for`/`match`, where the `{` opens the body, and a `match` arm's pattern, where
the `->` opens the arm. Parentheses settle both — `if ((x) -> { x })(1) { … }` — exactly as
they do for a trailing closure.

Either spelling is a **function**, not a closure, in the two ways you can tell them apart:
its arity is checked, and `return` returns from it rather than from the function around it.

```
f = fn(a, b) { a + b }; f(1)
# -e:1:25: argument: function expects 2 argument(s), got 1
fn outer() { g = fn() { return 1 }; g(); "still here" }; outer()    # still here
```

So the choice is about what the body does: `{ … }` for the short one a library calls,
`fn(…) { … }` or `(…) -> { … }` for the one with an interface of its own. `async` keeps the
keyword spelling, `async fn(…) { … }` — see [async](async.md).

## Default parameters and `*rest`

Defaults are ordinary expressions evaluated at the call, and may read the parameters bound
before them.

```
fn greet(name, greeting = "Hello") { "${greeting}, ${name}" }
greet("Ann")                               # Hello, Ann

fn slice_of(xs, from = 0, upto = xs.len) { xs.slice(from, upto - from) }
slice_of([1, 2, 3, 4, 5], 1)               # [2,3,4,5]

fn tag(name, *classes) { classes.json }
tag("div", "big", "red")                   # ["big","red"]
tag("div")                                 # []
```

Arity is checked for every `fn`, named or not; `*rest` makes it variadic (`arity` is `-1`).

```
fn add(a, b) { a + b }; add(1)
# -e:1:25: argument: add expects 2 argument(s), got 1
```

## Named arguments

An argument written `name = value` binds the parameter of that name, so a defaulted
parameter in the middle can be skipped instead of shifted:

```
fn area(w, h = 2, unit = "cm") { "${w * h} ${unit}²" }

area(3)                    # 6 cm²
area(3, 5)                 # 15 cm²
area(3, unit = "m")        # 6 m²      — `h` keeps its default
area(h = 5, w = 3)         # 15 cm²    — any order, once every argument is named
```

Names come after the positional arguments, each name is given once, and both of those are
caught while parsing. The rest is caught at the call, where the parameter list is known:

```
fn area(w, h = 2) { w * h }

area(3, z = 1)     # argument: area has no parameter named 'z'; it takes 'w' and 'h'
area(3, w = 1)     # argument: area got two values for parameter 'w': one by position and one by name
area(h = 1)        # argument: area is missing a value for parameter 'w'; it takes 'w' and 'h'
area(w = 1, 2)     # syntax: a positional argument may not follow a named one
```

Named arguments work wherever a script function does — a closure, an anonymous `fn`, a
UFCS method call, an `async fn`, a module's exported function:

```
g = { (x, y) -> x - y }; g(y = 1, x = 9)          # 8
fn area(w, h = 2) { w * h }; 3.area(h = 4)        # 12   the receiver is still `w`
```

Only a script function has parameter names. Builtins, host functions and stdlib methods
take their arguments by position, and a name there is an error rather than a guess:

```
print(len = "x")   # argument: print takes its arguments by position, so 'len = …' has no parameter to bind
```

Two spellings that are *not* named arguments: `f(a: 1)` (a dict is written `f({a: 1})`),
and an assignment in argument position, which needs its own parentheses — `f((x = 5))`.

A **trailing closure cannot be combined with a named argument**. The closure is a
positional argument written after the parentheses, so "the closure is the last argument"
and "a positional argument may not follow a named one" would point at different
parameters. Both unambiguous spellings stay:

```
fn retry(times = 1, body) { … }

retry(3) { … }                  # every argument by position, closure included
retry(times = 3, body = { … })  # every argument by name
retry(times = 3) { … }          # syntax: a trailing closure is a positional argument, …
```

## Closures and `it`

`{ … }` is the closure form of a function value — the other is the anonymous `fn` above.
With no parameter list it declares one implicit parameter named `it`.

```
[1, 2, 3].map { it * 2 }                   # [2,4,6]
[1, 2, 3].map { (x) -> x * 2 }             # [2,4,6]
{ () -> 42 }.call()                        # 42
{ (a, b) -> a + b }.arity                  # 2
```

`it` is an ordinary local: an explicit parameter shadows it, and a nested closure gets its own
— `[1, 2].map { [it].map { it * 10 } }` is `[[10],[20]]`.

Closure literals are **exempt** from the arity check, because library functions decide how
many values to pass: extra arguments are dropped, missing ones are `nil`.

```
{ 42 }.call(9)                             # 42
inspect({ (x) -> x }.call())               # nil
[1, 2].each { (a, b, c) -> println(inspect([a, b, c])) }   # [1,null,null] / [2,null,null]
```

`return`, `break` and `next` inside a closure reach the enclosing function or loop:

```
fn f(xs) { xs.each { return "early" if it > 1 }; "end" }
f([1, 2, 3])                               # early
[1, 2, 3].each { break 1 }                 # 1
[1, 2, 3].map { next 0 if it == 2; it }    # [1,0,3]
```

## Functions are values

```
double = { (n) -> n * 2 }
[1, 2, 3].map(double)                      # [2,4,6]
fn f() { 1 }; g = f; g()                   # 1
fns = {inc: { it + 1 }}; fns["inc"](4)     # 5
type({ it })                               # function
str({ it })                                # #<fn>
```

A trailing closure is appended as the last argument and binds to the nearest preceding call:

```
[1, 2, 3].reduce(0) { (a, b) -> a + b }    # 6
[3, 1, 2].map { it }.join(",")             # 3,1,2
```

Paren-less calls with arguments do not exist (`println "hi"` is a syntax error), and calling a
non-function is an error: `5(1)` is `type: not a function: int`.

## Closures capture by reference

The captured environment is shared, not copied, which is how state lives outside both closures
that touch it:

```
fn make_counter(start = 0, step = 1) {
  n = start
  {tick: { n += step; n }, peek: { n }}
}
c = make_counter(100, 5); c["tick"].call(); c["tick"].call(); c["peek"].call()   # 110
```

## UFCS

`x.f(y)` resolves to the stdlib method table for `x`'s kind first, then to a function named
`f` visible in scope, called as `f(x, y)`. A function you write is a method the moment it
exists — of any kind:

```
fn shout(s) { s.upper + "!" }
"yes".shout                                # YES!
fn f(s) { s }; 5.f                         # 5
```

The stdlib row wins for method syntax, so a user function with a stdlib name shadows only the
plain-call spelling:

```
fn len(x) { 999 }; ["ab".len, len("ab")]   # [2,999]
```

An unknown method is a compile-time error with a suggestion: `"x".nope` reports
`name: undefined method 'nope'; did you mean 'none'?`.

## Scope

Every closure is a scope — so every `{ … }` that is a closure or a body is one, and a
dict literal (`{a: 1}`, `{}`) is not: it is a value, and its entries are evaluated in the
scope around it. `=` writes the nearest existing binding and creates one *here* when
there is none; `:=` always creates here.

```
n = 0; { n = 5 }.call(); n                 # 5
n = 0; { n := 5 }.call(); n                # 0
total = 0; for i in 1..3 { total += i }; total   # 6
if true { y = 5 }; y
# -e:1:20: name: undefined variable 'y' (did you mean 'debug'?)
```

A function body sees the bindings that exist where the `fn` is written, not the ones added
later: `fn f() { x }; x = 10; f()` fails to compile with `undefined variable 'x'`, while
`x = 10; fn f() { x }; f()` is `10`.

## Recursion and `MaxDepth`

Recursion is bounded by `Options.MaxDepth`, 200 by default.

```
fn rec(n) { n == 0 ? 0 : 1 + rec(n - 1) }; rec(198)        # 198
try (fn r() { r() }; r()) else "caught"
# -e:1:15: limit: max call depth exceeded (200)          exit code 3
```

The depth limit is a limit, not a script error: `try` never catches it.

## See also

- [Errors](./errors.md) — what `try` catches and what a limit is
- [Destructuring](./destructuring.md) — taking apart what a function returns
- [Values](./values.md) — `function` as one of the nine kinds
- [Standard library](../stdlib/README.md) — the flat namespace UFCS dispatches into
