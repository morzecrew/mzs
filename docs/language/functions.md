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

## Anonymous `fn`

Leave the name out and the `fn` is an expression: a value that is not hoisted and binds
nothing, so the only way to reach it is the value itself.

```
add = fn(a, b) { a + b }
add(2, 3)                                  # 5
fn(x) { x * 3 }(5)                         # 15 — called where it stands
[1, 2, 3].map(fn(x) { x * 2 })             # [2,4,6]
ops = {add: fn(a, b) { a + b }}; ops["add"](1, 2)      # 3
```

It is a **function**, not a closure, in the two ways you can tell them apart: its arity is
checked, and `return` returns from it rather than from the function around it.

```
f = fn(a, b) { a + b }; f(1)
# -e:1:25: argument: function expects 2 argument(s), got 1
fn outer() { g = fn() { return 1 }; g(); "still here" }; outer()    # still here
```

So the choice is about what the body does: `{ … }` for the short one a library calls,
`fn(…) { … }` for the one with an interface of its own. `async fn(…) { … }` is a value the
same way — see [async](async.md).

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

Arity is checked for a named function; `*rest` makes it variadic (`arity` is `-1`). Keyword
arguments are collected into one trailing dict argument — there is no keyword-parameter
binding.

```
fn add(a, b) { a + b }; add(1)
# -e:1:25: argument: add expects 2 argument(s), got 1
fn f(a, b) { [a, b.json] }; f(1, x: 2, y: 3)     # [1,"{\"x\":2,\"y\":3}"]
```

## Closures and `it`

`{ … }` is the one and only function-value form. With no parameter list it declares one
implicit parameter named `it`.

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
[1, 2].each { (a, b, c) -> say(inspect([a, b, c])) }   # [1,null,null] / [2,null,null]
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

Paren-less calls with arguments do not exist (`say "hi"` is a syntax error), and calling a
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
