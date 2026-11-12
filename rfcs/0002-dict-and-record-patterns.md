# RFC 0002 — Dict and record patterns

**Status:** Landed (#24, #25) · **Area:** language · **Depends on:** 0001 (for `**rest`) · **Audit:** L4, L5, L6

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

## As landed

Part 1 landed in #24: dict patterns on the left of `=` and `:=`, in `match` arms, nested in
array patterns (and array patterns in them), and patterns as `for` variables. Part 2 landed
in #25: the record shape, `Money {amount, currency: "RUB"} ->`, with its compile-time field
check. SPEC §4 (grammar), §5.3 (the arm forms) and §8.15 (dict patterns, record shapes,
loop patterns) are the normative text.

The **`mod.Name {…}` form moves to RFC 0003.** Telling `money.Money {amount}` from a method
call with a trailing closure needs the records an include exports known when the includer
compiles, and that export table is what RFC 0003 builds (its §2 already shows the arm).
Until then `mod.Name {…}` keeps the reading it has today.

* **Compatibility — the RFC was wrong about one form.** It said all three forms were syntax
  errors. In assignment and in a `for` header they were. In a `match` arm they were not: a
  dict literal is an expression, and an expression arm compares, so `{a: 1} ->` fired on a
  subject *equal* to `{a: 1}` and `{} ->` on the empty dict. Under this RFC both are dict
  patterns — open, so `{a: 1}` fires on any dict whose `"a"` is `1`, and `{}` on any dict —
  and a name in value position binds instead of being read. That is a change of meaning for
  a program that runs, so it is declared as a deliberate break under §21.4 in the
  CHANGELOG, with the spelling that keeps the old meaning: `({a: 1}) ->` compares with the
  whole dict and `{k: (limit)}` with a variable, exactly as `(xs) ->` already compares with
  an array instead of destructuring it (§5.3). The alternative — a closed pattern when no
  name appears, an open one when one does — would make one spelling mean two things by
  what is inside it, which is the guess D16 forbids.
* **A bracket is a pattern only when it is the whole element.** The parser decides over
  tokens, before anything is consumed: a `{` (or `[`) in pattern position is a pattern when
  the token after its close is `,`, `]`, `}`, `->`, `if` or an assignment operator. So
  `{a: 1}.len ->` and `{a: 1} in xs ->` stay the operands they were, compared as any
  expression is, and only the arms that are a dict and nothing more change. In a `for`
  header the `in` (or the `,`) after the close is what ends the pattern.
* **`**rest` comes last**, and a pattern takes one. The grammar allowed it anywhere; last
  is the reading nobody has to look up, and an error can later become a feature. A bare
  `**` is an arm's only (it says nothing about the other entries); on the left of `=` a
  rest is `**` and a name, as `*rest` is.
* **A key is a word or a plain string.** An interpolated key is refused — a pattern names the
  keys it takes — and so is a literal of another kind (`{1: v}`): the RFC's grammar gives
  `IDENT | String`, and a non-string key can be added later as an addition. A key named
  twice in one pattern is a parse error.
* **The rest of a record is a plain dict**, as `{**p}` is (RFC 0001): a record is a shape
  its constructor promised, and a dict with fields taken out is no longer that shape.
* **Every value is read before the first is written**, at every depth, so a target writing
  into the dict being taken apart — or into a nested container a later key reads — cannot
  change what that key reads. Destructuring is two passes, read then write, for the array
  form too, which had the same gap one level down; a shape that does not fit therefore
  writes nothing.
* **A spreading arm.** `{**defaults} ->` binds the subject's entries rather than comparing
  with a copy, and `{**defaults, a: 1} ->` no longer compiles, because a pattern's rest
  comes last. Both are part of the declared §21.4 change; parenthesised, both compare.
* **A loop pattern holds names, rests and nested patterns only.** A `$var` or an index
  target is refused in a `for` header — a loop binds fresh names per item, and writing into
  a global on every turn is what the body is for. The anonymous `*` and `**` stay `match`
  arm forms; a loop pattern, like an assignment, names what it binds.
* **Diagnostics.** `{a: 1} = d` is `syntax: a destructuring target binds, so the value of
  'a' goes to a name, a $var, an index or a nested pattern; comparing belongs in a match
  arm` — the RFC's wording quoted the source text, which the parser does not keep; the
  other three are as the table proposed.
* **The LSP outline** lists every name a top-level destructuring binds — for the array
  patterns of RFC 0001 too, which it had missed.
* **The record shape is decided by resolution, as proposed.** `Name {…}` in pattern
  position parses to a `DictPattern` with `Record` set; for the two shapes a trailing
  closure also has, `Name {}` and `Name {x}`, the parser keeps the call reading beside it
  (`Alt`), built exactly as a trailing closure is. The compile pass asks which binding the
  name resolves to: a `record` declaration (hoisted, so one declared further down counts)
  makes it a record pattern; anything else — a function, a variable, a `:=` local that
  shadows a record — puts the call reading back, so `choose {it} ->` means what it meant.
  A shape no closure has (`f {a, b}`) is a diagnostic when `f` is not a record. The parser
  only tries the pattern reading for a brace that is empty or holds a dict pattern
  (`{x}`, `{**…}`, a key before `:` or `,`), so `choose { (x) -> x }` is never read as one.
* **The shape is asked of the constructor at run time**, as the bare `Money ->` arm does
  (§7.8); the compile-time resolution decides the reading and checks the field names.
* **A record shape is a match pattern only.** On the left of `=` it is a diagnostic: a
  record is a dict, and `{amount} = m` takes it apart.
* **Formatting.** `Name {x}` is one tree whichever way it is read, so the formatter spells
  it as a pattern when the file declares a record of that name and as a trailing closure
  (`choose { it }`) otherwise — a program that does not use records formats as before.
* **The §21.4 corner** is the one the RFC named: `Money {x} ->` and `Money {} ->` on a record
  name used to construct a record holding the closure and compare with it, which could
  never fire; they now ask for the shape. Declared in the CHANGELOG.
* **Open questions.** Defaults in a target (`{port = 8080} = cfg`) and a scalar binder in
  `match` stay deferred, as the RFC proposed.
