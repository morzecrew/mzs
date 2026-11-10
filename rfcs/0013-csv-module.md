# RFC 0013 — The `csv` module

**Status:** Draft · **Area:** stdlib · **Depends on:** — · **Audit:** S2

## Summary

Add a built-in module `csv` with four members: `parse`, `rows` (lazy), `str` and `line`.
It implements RFC 4180 quoting through Go's `encoding/csv`, needs no host option, and
reads large inputs through `seq`.

## Motivation

The README's first row is "a small scripting language for text, data and one-liners". CSV
is the most common shape of that data, and `include csv` is `unknown module`. The
repository's own example concedes the point in its header:

```
# `split(",")` is not a CSV parser: it breaks on the first quoted comma. So this file
# writes the six-line scanner that does it properly …
```
— `examples/12_csv_report.mzs`

A scanner every script has to re-derive (and get wrong on `""` escapes and on newlines
inside quotes) is exactly what a standard library is for. The Go standard library already
contains a correct one, so I1 is not in question.

## Design

```
include csv

rows  = csv.parse(text)                          # [["id","name"],["1","Ann"]]
users = csv.parse(text, {header: true})          # [{id: "1", name: "Ann"}]
io.lines.pipe { csv.rows(it, {header: true}) }   # a seq of dicts, one record at a time
    .filter { it["paid"] == "yes" }.count
out   = csv.str(users)                           # header line from the first dict's keys
```

| Member | Signature | Semantics |
|---|---|---|
| `parse` | `(s: string, opts: dict = {}) -> array` | every record of `s`; an array of arrays, or of dicts with `header` |
| `rows` | `(src: seq \| array \| string, opts: dict = {}) -> seq` | the same records, pulled one at a time (§12.14). `src` is a sequence of **lines**, as `io.lines` gives; a quoted field containing a newline consumes as many lines as it spans |
| `str` | `(rows: array \| seq, opts: dict = {}) -> string` | the document: each record on a line ending in `\n`. Dict rows write a header first |
| `line` | `(row: array, opts: dict = {}) -> string` | one record, no terminator — for `io.append` and `println` |

**Options** (a dict, as `http` takes them — builtins do not take named arguments):

| Key | Default | Meaning |
|---|---|---|
| `sep` | `","` | one rune. `"\t"` for TSV, `";"` for the Excel of half of Europe |
| `header` | `false` | reading: the first record names the fields and every later record is a dict in that order (D11). A header that names a field twice (`id,id`) or has an empty name is a `csv` error before any record is read: two fields cannot share one key, and silently dropping one is data loss. Writing: `true` forces a header, `false` suppresses it, default is "when rows are dicts" |
| `strict` | `true` | reading: a record with a different field count than the first is an error. `false` pads with `nil` / keeps extras under their index |
| `trim` | `false` | strip spaces around unquoted fields |
| `comment` | `nil` | a rune; lines starting with it are skipped |

**Values are strings.** A CSV field has no type, and guessing one (`"007"`, `"1e3"`,
`"NULL"`) is the coercion §9.1 refuses. Conversion is the caller's, with `int`, or RFC
0008's `parse_int` when the column must be numeric. An empty field is `""`, not `nil`.

**Writing.** A field is quoted when it contains the separator, a quote, `\r` or `\n`, or
has leading/trailing space; quotes double. Non-string values go through `str`, except
two: `nil` is an empty field, and a `Decimal` is written as `decimal.str` writes it
(`12.5`). `str` of a Decimal is the JSON of its dict (`{"units":125,"scale":1}`, since a
record is a dict, §7.8), which no spreadsheet reads as money; any other dict or array in a
field is an `argument` error naming the column. Dict rows are written in the key
order of the **first** row (or of `header`, when it is given as an array). Any row with a
key that order does not have — the first row too, when `header` set the order — is
**always** an error naming the row and the key, since writing it would drop data. A later row *missing* a key is an error under `strict`, and an
empty field without it.

**Errors.** A new kind `csv` joins the closed list (§13.5), as `json` and `decimal` did:
`csv: line 7: bare '"' in an unquoted field`. The line is the input line, and `data`
carries `{line, col}`. Catchable.

**Limits.** `parse` charges `MaxCollection` per record array and for the outer array;
`rows` materialises one record at a time, so a file larger than any limit is processable,
bounded by the step budget (§14.1). A single field is bounded by `MaxStringBytes`.

**Sandbox.** Pure computation: no gate, like `json` and `url` (§14.3).

## Compatibility

Additive (§21.2): a new module name. `include csv from "./csv.mzs"` — a user's own module
of that name — keeps working, because the path form never consults the built-in table. A
bare `include csv` was an error. A new error kind is additive to a list scripts match with
an `else ->` arm.

## Alternatives

* **Ship it as a library on `$MZSPATH`** written in mzs. Possible today, but slow under a
  step budget (one step per character) and nobody's default.
* **Typed parsing** (`{types: {qty: "int"}}`). Convenient, and a second place where
  conversion rules live. `.map { it + {qty: it["qty"].int} }` is one line.
* **A `table` value** with columns. A new kind for what an array of dicts already is.

## Open questions

* Name of the lazy member: `rows` vs `seq`. `csv.seq` shadows a core name inside the
  module namespace, which is legal but confusing.
* BOM: strip a leading U+FEFF silently? Proposed yes — Excel writes one, and it otherwise
  ends up inside the first header name where it is invisible.
* `header` as an array (`{header: ["id", "name"]}`) to name columns of a headerless file.

## Implementation

`mzs/csv.go` + tests (a thin layer over `encoding/csv`; the lazy reader adapts a `seq` of
lines to an `io.Reader`), registration in `mzs/module.go`, error kind in `mzs/errors.go`,
SPEC §12.8 table + a new §12.19, `docs/modules/csv.md`, apidoc, and
`examples/12_csv_report.mzs` rewritten to use it (keeping the hand scanner as a comment on
why the module exists).
