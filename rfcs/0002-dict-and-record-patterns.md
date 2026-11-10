# RFC 0002 — Dict and record patterns

**Status:** Draft · **Area:** language · **Depends on:** 0001 (for `**rest`) · **Audit:** L4, L5, L6

## Summary

Destructuring (SPEC §8.15) understands arrays and nothing else. This RFC adds the dict
pattern `{name, age: a}` everywhere the array pattern already works — assignment, `match`
arms — adds a record-shaped form `Money {amount}`, and lets a `for` header take a pattern.

## Motivation

The data a mzs program handles is JSON: dicts inside arrays inside dicts. Today:

```
{a, b} = {a: 1, b: 2}                      # syntax: unexpected ','
match ev { {kind: "paid", total: t} -> t } # syntax: expected '->' in match arm, found ':'
for [id, row] in pairs { … }               # syntax: expected a loop variable, found '['
```

A `match` over records can ask "is this a `Money`?" and then has to index the subject by
hand in the arm body. Error handling — the one place the language *hands* you a dict — is
`match e["kind"] { … }` followed by `e["data"]["short_by"]`.

## Design

### Grammar delta

```ebnf
TargetPattern  = ArrayTarget | DictTarget ;
DictTarget     = "{" DictTargetEntry { "," DictTargetEntry } [ "," ] "}" ;
DictTargetEntry= IDENT                               (* {name}  ≡ {name: name} *)
               | ( IDENT | String ) ":" Target       (* key: where it goes *)
               | "**" IDENT ;                        (* the remaining entries, RFC 0001 *)

MatchPattern   = ArrayMatch | DictMatch | RecordName DictMatch ;   (* the last: a record shape *)
RecordName     = IDENT [ "." IDENT ] ;   (* a record of this unit, or one an included module exports (RFC 0003) *)
DictMatch      = "{" [ DictMatchEntry { "," DictMatchEntry } [ "," ] ] "}" ;
DictMatchEntry = IDENT                               (* binds the value under that key *)
               | ( IDENT | String ) ":" MatchElem    (* IDENT binds, a pattern nests, an Expr compares *)
               | ( IDENT | String ) ":" "in" Expr    (* membership, as an `in` arm pattern (§5.3) *)
               | "**" [ IDENT ] ;

ForExpr        = "for" ForTarget [ "," ForTarget ] "in" Expr Closure ;
ForTarget      = IDENT | TargetPattern ;
```

### Semantics

**Assignment.** `{name, age: a} = user` binds `name = user["name"]`, `a = user["age"]`.
The right side must be a dict (`type` error otherwise). A key that is absent is a `key`
error, the same one `fetch` raises — the array form is strict about length, the dict form
is strict about presence. Extra keys are ignored: a dict pattern is open, because JSON
objects grow. `**rest` binds a new dict of the entries not named.

**`match` arms.** A dict arm matches when the subject is a dict, every named key is
present, and every value position that is not a binder matches — by the rule array
patterns already use (§8.15): an identifier binds, a nested pattern recurses, any other
expression is compared as an arm pattern (so a regex matches). Membership has its own
entry form, `{age: in 18..65}`, which matches exactly when `subject["age"] in 18..65`
(§8.4) — `{age: 18..65}` compares for equality with a range and is never true of an int.

```
match ev {
  {kind: "paid", total: t} if t > 0   -> credit(t)
  {kind: "refund", data: {reason}}    -> log(reason)
  {kind: /^test\./}                   -> nil
  else                                -> raise("unknown event")
}
```

Unlike assignment, a missing key in an arm is **no match**, not an error — an arm is a
question.

**Record shape.** `Money {amount, currency: "RUB"} -> …` matches when the subject was built
by that `record` declaration (identity, exactly as the bare `Money ->` arm, §7.8) and the
dict pattern matches. Field names are checked against the declaration at compile time:
`Money {amout}` is `syntax: record Money has no field 'amout'; it has amount, currency`.

**`for`.** `for [id, row] in pairs`, `for {name, age} in users`, and
`for k, {total} in by_user` destructure each item (or each key/value). The existing
two-variable form is unchanged; it is now simply two `ForTarget`s.

### Why not `Money(amount, currency)` as the record pattern

It is the obvious spelling, and it is already legal: an arm pattern is an `Expr`, so
`Money(a, b) -> …` today *calls the constructor* and compares the subject with the result.
Reading the same text as a binder would change the meaning of a program that parses —
§21.1 forbids that, and D16 forbids the guess.

`Name {…}` in arm position is nearly free — `Money {a, b}` and `Money {a: x}` are syntax
errors today — but not entirely. `f {it}` is a call with a trailing closure, and when `f`
is an ordinary function that call can match today:

```
fn choose(f) { 1 }
match 1 { choose {it} -> 7; else -> 0 }    # 7
```

So the record reading is decided by **what the name is**, which the compile pass knows
because `record` declarations hoist (§8.2) and an include's exports are known (RFC 0003):
`Name {…}` is a record pattern only when `Name` (or `mod.Name`) resolves to a `record`
declaration. Every other name keeps today's meaning, so `choose {it}` above still answers
7. The only program whose meaning changes is one that passes a trailing closure to a
*record constructor* inside an arm and compares the subject with the resulting dict — a
dict holding that very closure, which no subject can equal — and that change is still
listed under §21.4. A local variable that shadows a record name is not a record name, so
the reading follows the binding, never the spelling.

### Diagnostics

| Written | Message |
|---|---|
| `{a, b} = [1, 2]` | `type: cannot destructure array with a dict pattern` |
| `{a} = {}` | `key: destructuring: key not found: "a"` |
| `{a: 1} = d` | `syntax: a destructuring target binds; '1' is not a name` (comparison belongs in `match`) |
| `{} = d` | `syntax: an empty dict pattern binds nothing` (in `match`, `{}` is fine: "any dict") |

## Compatibility

Additive, with the one declared corner above. All three forms are syntax errors today. §3.12's closure-or-dict lookahead is
unaffected in arms (the parser is already in arm-pattern position). At statement start,
`{a, b} = …` requires the statement parser to look ahead for `}` followed by `=`/`:=`;
a closure can never be followed by `=`, so there is no second reading.

## Alternatives

* **Only `match`, not assignment.** Halves the parser work, but `{status, body} =
  http.get(u)` is the line people will write first.
* **Lenient assignment (missing key → `nil`).** Consistent with `d["x"]`, inconsistent with
  the array form's strictness and with D16. A default can be added later as
  `{port = 8080}` without breaking anything.

## Open questions

* Defaults in a target (`{port = 8080} = cfg`) — natural, mirrors parameters; deferred to
  keep this RFC reviewable.
* A scalar binder for `match` (`in 1..9 as n -> …`), see audit "noted, not proposed".

## Implementation

`internal/parser` (statement-start lookahead, arm patterns, `for` header),
`internal/ast` (`DictPattern`), `mzs/eval.go` (the matcher that array patterns use gains a
dict case), `mzs/record.go` (field check), `internal/printer`, LSP symbol collection for
the new binders.
