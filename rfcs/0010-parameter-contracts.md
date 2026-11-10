# RFC 0010 — Parameter and field contracts

**Status:** Draft · **Area:** language · **Depends on:** — · **Audit:** L16

## Summary

Let a parameter or a record field name the kind it accepts: `fn pay(amount: int, to:
string)`, `record Money(amount: int, currency: string = "RUB")`. The contract is checked
when the function is called, with an `argument` error that names the parameter. It is a
run-time check with a compile-time shortcut for literals — not a type system.

## Motivation

Every stdlib row "raises `wrong argument type` on a type mismatch" (SPEC §12). User
functions cannot say the same without writing it out:

```
fn pay(amount, to) {
  raise("amount must be an int", "argument") if !amount.is("int")
  raise("to must be a string", "argument")   if !to.is("string")
  …
}
```

Without the guards the failure appears somewhere inside — `type: cannot add int to string`
three calls down — and names neither the function nor the argument. Host values make it
acute: everything bound from the host is a string (D8), so `pay($amount, $to)` instead of
`pay($amount.int, $to)` is the single most likely mistake in an embedded rule, and the
language currently cannot catch it at the boundary where it is made.

Records have the same need with more force: `record Money(amount, currency)` promises a
shape and checks nothing about what is put into it.

## Design

```ebnf
Param      = IDENT [ ":" Contract ] [ "=" Expr ] | "*" IDENT [ ":" Contract ] ;
Field      = IDENT [ ":" Contract ] [ "=" Expr ] ;
Contract   = TypeName [ "?" ] ;
TypeName   = IDENT ;        (* a kind name of §7.2, or a record / enum name in scope *)
```

`:` is free in a parameter list — a named *argument* is `name = value` (§8.7), and
`fn f(x: int)` is `expected ')' in parameter list, found ':'` today. The same `Param`
serves `fn`, arrow functions and closure parameter lists.

**Contract names** are exactly what `is(x, name)` accepts (§12.1): `int`, `float`,
`string`, `bool`, `array`, `dict`, `regex`, `function`, `range`, `seq`, `time`, a record
name, or (RFC 0005) an enum name. They are the `type()` spellings, which is why it is
`function` and not `fn` — `fn` is a keyword and could not be an `IDENT` here anyway. A
trailing `?` also admits `nil`.

Two additions, each made to `is` as well so the contract and the function never disagree:

* `task`. `type(t)` is already `"task"`, yet `t.is("task")` is an argument error today;
  that becomes `true`. Additive: an error becomes an answer.
* `number`, meaning int or float — the single union every numeric function needs.

The precedence `is` has today stays as it is: **the existing kind names answer first**,
so `record int(x); is(1, "int")` is still `true` and `int(3).is("int")` still `false`.
The two *new* names are the only ones a program can already have claimed for a record —
`record number(x); number(7).is("number")` is `true` today — so for `number` and `task`
alone, a record of that name declared in the Run answers first, and the kind meaning
applies only when no such record exists. A contract reads the same table, so `x: number`
means that record in such a Run. Declaring a record named like any kind gains a warning.

**When it is checked.** After argument binding and default evaluation (§8.7), before the
body runs, in parameter order. A default value is checked like any other. On `*rest: int`
every collected element is checked. A record constructor checks its fields the same way —
it binds by the same rules. There is no check on later mutation (`m["amount"] = "x"`); a
contract guards the door, not the room.

```
-e:1:31: argument: pay: 'amount' expects int, got string ("1500")
  fn pay(amount: int) { … }; pay($amount)
                                ^
```

The position is the **call site's argument**, the kind is `argument`, the message names
function, parameter, expected and actual kind, and an `inspect` of the value truncated to
24 runes.

**Compile-time shortcut.** When the callee is statically known (a hoisted `fn`, a record
constructor) and an argument is a literal of the wrong kind, the same message is a compile
error. This is the only static analysis proposed.

**No coercion.** `pay("5")` against `amount: int` is an error, never a conversion (§9.1).

**What is deliberately absent:** return contracts, generics (`array<int>`), unions other
than `number` and `?`, structural dict types, aliases. Each is a step toward a type
system mzs does not have a checker for; a contract that is not checked is a comment.

**Tooling.** Hover and signature help show contracts (RFC 0019); `fn.arity` is unchanged;
no reflection API is added.

## Compatibility

Additive: the syntax is an error today, and a function without contracts behaves
identically. `is(x, "number")` changes from an argument error to an answer.

## Alternatives

* **Do nothing; write `assert`.** Costs a line per parameter and loses the call-site
  position, which is the valuable half.
* **Gradual static typing.** A real project of its own (inference through UFCS, dict
  shapes, `$vars`), and it would make "every construct is an expression, no ceremony" feel
  different. Not this RFC, and probably not this language.
* **Contracts as predicates** (`amount: positive`). Any function of one argument would
  then be a type; elegant, slow, and the error message can no longer say "expects int".

## Open questions

* Is `number` worth being the one union, or should it be `int | float` with a general
  `|`? `|` is a diagnostic today (§5.6) and would need to be legal in exactly this
  position.
* Should `mzs fmt` align contracts? Should `--check` warn when a public (`export`ed)
  function has uncontracted parameters? Probably a lint, off by default.
* Performance: one kind comparison per contracted parameter per call. Measure against the
  A4 budget; contracts on hot closures (`{ (x: int) -> … }` in a `map`) are the worry.

## Implementation

`internal/parser` + `internal/ast` (contract on `Param`), `mzs/call.go` (check after
binding), `mzs/record.go`, compile pass (name resolution of contracts, literal shortcut),
`mzs/builtins.go` (`is("number")`), printer, apidoc for the LSP.
