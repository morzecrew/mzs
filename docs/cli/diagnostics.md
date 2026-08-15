# Diagnostics

The shape of an mzs error message, the fix-its for constructs brought from other
languages, and what `--check` reports.

## Format

`<file>:<line>:<col>: <kind>: <message>`, then the offending source line and a caret. It
goes to stderr; the column counts runes, and a leading tab in the source stays a tab in the
caret line.

```sh
$ mzs one.mzs
one.mzs:2:6: syntax: unexpected '!' after '='; did you mean '!='?
  str =! "sdfsdf"
       ^
```

`-e` sources are named `-e`, a piped program is named `<stdin>`, a file is named as you
typed it. Some blocks below quote only the first line of a diagnostic; the source line and
the caret always follow it.

## Error kinds

| Kind | Raised by |
|---|---|
| `syntax` | the lexer and parser, including every fix-it below |
| `name` | an undefined variable, function, method, or module capability |
| `type` | an operation on the wrong kind |
| `argument` | wrong arity, or an argument out of range |
| `index` | a missing dict key, a destructuring length mismatch |
| `zero-division` | `1/0`, `1%0` |
| `regex` | a pattern that does not compile |
| `raise` | `raise(...)` from the script |
| `limit` | timeout, step budget, call depth, tasks — exit 3 |
| `internal` | a bug in the interpreter |

```sh
$ mzs -e '"a" + 1'
-e:1:5: type: cannot add int to string
$ mzs -e '{a: 1}.fetch("b")'
-e:1:8: index: key not found: "b"
$ mzs -e '"a" ~ /*/'
-e:1:7: regex: cannot compile /*/: missing argument to repetition operator: `*`
$ mzs --steps 1000 -e 'while true { 1 }'
-e:1:1: limit: step budget exceeded (1000 steps)
$ mzs -e '"a".lenght'
-e:1:5: name: undefined method 'lenght'; did you mean 'len'?
$ mzs -e 'puts("hi")'
-e:1:1: name: undefined function 'puts' (did you mean 'println'?)
```

A `name` error suggests the nearest name it knows, in three passes: an exact hit in the
rename table below, then a one-edit search over the names the receiver actually offers,
then a one-edit search over the rename table's own keys — which is how `lenght` reaches
`length` and so `len`.

## Fix-its

Each construct below gets one fixed diagnostic, produced before any other error can cascade
from it. Only the first diagnostic on a line is shown, and at most 10 per compile.

| You wrote | Message |
|---|---|
| `-2 ** 2` | `ambiguous: write -(2 ** 2) or (-2) ** 2` |
| `0..5.map { it }` | `ambiguous range: write (0..5).map` |
| `1..2..3` | `range operator is non-associative` |
| `a in b in c` | `'in' is non-associative: write (a in b) in c if that is what you meant` |
| `f(1, a: 2)` | `a named argument is written 'a = …'; for a dict argument write f({a: …})` |
| `f(a = 1, 2)` | `a positional argument may not follow a named one; move it before 'a = …'` |
| `f(a = 1, a = 2)` | `argument 'a' is named twice` |
| `f(a = 1) { … }` | `a trailing closure is a positional argument, so it cannot follow the named argument 'a = …': …` |
| `s == /re/` | `'==' with a regex operand: use '~' to match` |
| `s =~ /re/` | `'=~' is not an mzs operator; use '~'` |
| `str =! "x"` | `unexpected '!' after '='; did you mean '!='?` |
| `x.empty?` | `'?' is not part of an identifier; did you mean 'empty'?` |
| `and`, `or`, `not` | `'and'/'or'/'not' are not mzs keywords; use '&&', '\|\|', '!'` |
| `do`, `end` | `'do'/'end' are not mzs keywords; use braces: if c { … }` |
| `then` | `'then' is not an mzs keyword; use braces: if c { … }` |
| `elsif c { … }` | `'elsif' is not an mzs keyword; use 'else if'` |
| `unless c { … }` | `'unless' is not an mzs keyword; use 'if !(c)'` |
| `until c { … }` | `'until' is not an mzs keyword; use 'while !(c)'` |
| `loop { … }` | `'loop' is not an mzs keyword; use 'while true { … }'` |
| `def f() { }` | `'def' is not an mzs keyword; use 'fn'` |
| `a rescue b` | `'rescue' is not an mzs keyword; use 'try a else b'` |
| `require "…"` | `'require' is not an mzs keyword; use 'include': include lib from "./lib.mzs"` |
| `%w[a b]` | `'%w' is not mzs; write ["a", "b"]` |
| `:name` | `mzs has no symbols; write "name"` |
| `[a: 1]` | `a dict is written {a: 1}` |
| `[:]` | `the empty dict is written {}` |
| `{1: "A"}` | `a dict key that is not a string takes '->', not ':'` |
| `(x) -> x * 2` | `an arrow function's body is braced: (x) -> { x * 2 }, or write the closure { (x) -> x * 2 }` |
| `async (x) -> { x }` | ``an async function is written `async fn(a, b) { … }` `` |
| `{a: 1, (k) -> 2}` | `a computed dict key takes ':', not '->': write (k): v` |
| `f {a: 1}` | `a dict after a call is written f({a: 1})` |
| `if c {a: 1}` | `this '{' opens the if body; write { {a: 1} } for a dict` |
| `[k => v]` | `'=>' is not an mzs operator; write {k: v} for a dict, { (x) -> … } for a closure` |
| `{ \|x\| … }` | `closure parameters are parenthesised: { (x) -> … }` |
| `x &. y` | `'&.' is not an mzs operator; use '?.'` |
| `a & b` | `'&' is not an mzs operator; use band(a, b), or '&&' for logical and` |
| `a \| b` | `'\|' is not an mzs operator; use bor(a, b), or '\|\|' for logical or` |
| `a ^ b` | `'^' is not an mzs operator; use bxor(a, b), or '**' to raise to a power` |
| `"#{x}"` | `string interpolation is "${x}"` |
| `1...5` | `'...' is not an mzs operator; use '..<'` |
| `a::B` | `'::' is not an mzs operator; use '.'` |
| `@ivar` | `'@' is reserved; instance variables do not exist in v0.1` |
| `x.to_s`, `.to_i`, `.to_f`, `.to_a`, `.to_h`, `.to_json` | `undefined method; use 'str' / 'int' / 'float' / 'array' / 'dict' / 'json'` |

`import lib` and `use lib` produce the same message with their own name in it.

Renamed methods get the name they have here — `undefined method 'X'; did you mean 'Y'?`:

| X | Y | X | Y |
|---|---|---|---|
| `downcase` | `lower` | `length`, `size` | `len` |
| `upcase` | `upper` | `gsub` | `replace` |
| `strip` | `trim` | `sub` | `replace_first` |
| `lstrip` | `trim_start` | `scan` | `matches` |
| `rstrip` | `trim_end` | `select` | `filter` |
| `include`, `has_key`, `cover` | `has` | `collect` | `map` |
| `detect` | `find` | `inject` | `reduce` |

The suggester knows four more that the parser does not rewrite: `match` → `captures`,
`puts` → `println`, `p` → `debug`, and mzs's own former spelling `say` → `println`. Those
come out as `name` errors, the table above as `syntax`.

`case`/`when` has no fix-it of its own — it fails as `unexpected 'x' after statement`. The
construct to use is [`match`](../language/control-flow.md).

One paste of Ruby costs one diagnostic per line, not a cascade:

```sh
$ mzs -e '{ |x| x }'
-e:1:3: syntax: closure parameters are parenthesised: { (x) -> … }
  { |x| x }
    ^
```

## Warnings

Warnings print in the same format and never fail a compile (an embedder can set
`StrictWarnings` to make them fatal). The current set:

| Message |
|---|
| `"\\b" matches a literal backslash; did you mean "\b"? (pattern probably came from a JSON string)` |
| `'=' assigns; did you mean '==' ?` — an assignment as a whole `if` condition |
| `closure parameter 'x' is never used` |
| `closure literal in statement position: its value is discarded` |
| `anonymous 'fn' in statement position: its value is discarded` |
| `'<name>' is already bound; the include replaces it` |

```sh
$ mzs -e '"food" ~ /\\bfood\\b/'
-e:1:10: warning: "\\b" matches a literal backslash; did you mean "\b"? (pattern probably came from a JSON string)
  "food" ~ /\\bfood\\b/
           ^
false
```

## `--check`

Compile without running: report errors and warnings, print `<name>: ok` on stdout and exit
0, or exit 1 with the diagnostics on stderr.

```sh
$ mzs --check examples/05_arrays_pipeline.mzs
examples/05_arrays_pipeline.mzs: ok

$ mzs --check -e 'x = 1
if x = 2 { 3 }'
-e:2:6: warning: '=' assigns; did you mean '==' ?
  if x = 2 { 3 }
       ^
-e: ok

$ mzs --check -e '"a".nosuch'                # exit 1
-e:1:5: name: undefined method 'nosuch'
$ mzs --check -e '"a" + 1'                   # exit 0: a type error is a run-time error
-e: ok
```

`--check` sees everything the compiler sees — syntax, the fix-its, undefined names and
methods, bad regex literals — and nothing that only happens at run time.

`--tokens` still dumps the token stream for a program that does not parse, which is the
last resort when a diagnostic points at a rune you did not expect.

`--ast` dumps the tree instead, after names are resolved — so it shows what the compiler
already did to the source:

```sh
$ mzs --ast -e '[1 + 2, 2 ** 10].len'
Program "-e"
  ExprStmt
    MethodCall .len
      recv:
        Array
          Int 3
          Binary **
            Int 2
            Int 10
```

`1 + 2` arrives folded. `+`, `-` and `*` over two number literals fold, as does `+` over two
string literals, at any depth — `"x${1+2}"` interpolates an `Int 3`. Nothing else does: `**`,
`/` and `%` (even `6 / 2`), the comparisons, `&&`, `!`, `"a" * 3` and every method call all
survive. The fold is bottom-up and left-associative: `1 - 2 - 3` is `Int -4`, while
`a + 1 + 2` keeps both additions.

## See also

- [./README.md](./README.md) — flags and exit codes
- [../language/errors.md](../language/errors.md) — `try`, `raise`, what is catchable
- [../reference/limitations.md](../reference/limitations.md) — reserved syntax
- [../embedding/errors.md](../embedding/errors.md) — the same kinds from Go
