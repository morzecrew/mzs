# The `csv` module

Reading and writing CSV: RFC 4180 quoting, a header that makes records dicts, and a lazy
reader for a file that does not fit in memory.

```
include csv

csv.parse("id,name\n1,Ann\n")                   # [["id","name"],["1","Ann"]]
csv.parse(text, {header: true})                 # [{"id":"1","name":"Ann"}]
csv.rows(io.lines, {header: true})              # the same records, one at a time
csv.str(users)                                  # "id,name\n1,Ann\n"
csv.line(["a", "b,c"])                          # a,"b,c"
```

`split(",")` is not a CSV parser: it breaks on a quoted comma, on a doubled quote and on a
line break inside quotes. The module needs no host capability — it reads and writes text —
so `include csv` is the whole of it ([sandbox](../reference/sandbox.md)).

## Members

| Call | Signature | Result |
|---|---|---|
| `csv.parse(s, opts)` | `(string, dict = {}) -> array` | every record: arrays, or dicts under `header` |
| `csv.rows(src, opts)` | `(seq \| array \| string, dict = {}) -> seq` | the same records, read as they are pulled |
| `csv.str(rows, opts)` | `(array \| seq, dict = {}) -> string` | the document, each record ending in `"\n"` |
| `csv.line(row, opts)` | `(array, dict = {}) -> string` | one record, no line break |

```
include csv
csv.keys.json      # ["parse","rows","str","line"]
```

## Options

One dict, the second argument of every member:

| Key | Default | |
|---|---|---|
| `sep` | `","` | one character: `"\t"` for TSV, `";"` for a European Excel |
| `header` | `false` | read the first record as the field names; or an array naming the columns |
| `strict` | `true` | every record has the same number of fields |
| `trim` | `false` | drop spaces around unquoted fields |
| `comment` | `nil` | skip lines that start with this character |

A key the module does not know is an error, with the near miss in it:

```
include csv
try csv.parse("a;b", {sepp: ";"}) else (e) -> e["message"]
# csv.parse: unknown option "sepp"; did you mean 'sep'?
```

## Reading

**Every value is a string.** A field has no type, so `"007"`, `"1e3"` and `"NULL"` come out
as written, and an empty field is `""`. Converting a column is one more step:

```
include csv
orders = csv.parse("qty,price\n2,1500\n1,3200", {header: true})
orders.sum { it["qty"].int * it["price"].int }     # 6200
```

`{header: true}` turns each record into a dict in the header's order. A header that names a
column twice or leaves one unnamed is an error, because two columns cannot share a key. For
a file with no header line, name the columns yourself:

```
include csv
csv.parse("1,Ann\n2,Bo", {header: ["id", "name"]})   # [{"id":"1","name":"Ann"},{"id":"2","name":"Bo"}]
```

A quoted field may hold the separator, a doubled quote and a line break. A `"\r\n"` file
reads like a `"\n"` one, a blank line is no record, and the byte-order mark Excel writes in
front of a UTF-8 file is dropped rather than left inside the first column name.

Under `strict` (the default) a record with a different number of fields than the first one
is an error. `{strict: false}` reads it anyway: a short record is padded with `nil`, and a
long one keeps its extra fields — under their column index, in a dict.

## A file that does not fit: `rows`

`csv.rows` takes a sequence of **lines** — `io.lines`, an array, or a string — and gives a
seq ([seq](../stdlib/sequences.md)) that reads one record each time it is pulled. Nothing is read
until a terminal row asks, and a quoted field that spans lines takes as many as it needs:

```
include io
include csv
paid = csv.rows(io.lines, {header: true}).filter { it["paid"] == "yes" }.count
```

`MaxCollection` bounds what `parse` builds; `rows` holds one record at a time, so a file of
any length goes through it, bounded by the step budget and the deadline.

## Writing

`csv.str` writes rows that are all arrays or all dicts. Dict rows get a header line, in the
key order of the first row:

```
include csv
csv.str([{id: 1, who: "Petrov, Ivan"}, {id: 2, who: "Ann"}])
# id,who
# 1,"Petrov, Ivan"
# 2,Ann
```

A field is quoted only when it has to be: it holds the separator, a quote (doubled), a line
break, or a space at either end. `nil` is an empty field, a Decimal is written as
`decimal.str` writes it, and other values go through `str`. A dict or an array inside a field
has no CSV form and is an error naming the row and the column.

`{header: false}` leaves the header out, and `{header: ["id", "who"]}` sets the column order
— which also picks columns out of wider dicts by refusing the rest: a row with a key that
is not a column is **always** an error, since writing it would drop data. Under `strict` a
row *missing* a column is an error too; `{strict: false}` writes an empty field.

`csv.line(row)` is one record without the line break, for appending to a file as you go:

```
include io
include csv
io.append("out.csv", csv.line([id, name]) + "\n")
```

## Errors

Malformed input is a `csv` error that names the line and the column, which `data` carries
too:

```
include csv
try csv.parse("a,b\n\"unclosed") else (e) -> [e["kind"], e["message"], e["data"]]
# ["csv", "csv.parse: line 2, column 1: the quoted field that starts here is never closed", {"line":2,"col":1}]
```

| Message | Cause |
|---|---|
| `bare '"' in an unquoted field` | a quote in a field that does not start with one — quote the field and double it |
| `the quoted field that starts here is never closed` | the input ended inside quotes |
| `"x" after the closing quote` | text between a closing quote and the separator |
| `N field(s), and the first record has M` | a record of another width, under `strict` |
| `the header names "id" twice` | two columns with one name, under `header` |

A misused option or a row that cannot be written is an `argument` or `type` error. All of
them are caught by `try`; a size limit is a `limit` error, which is not.

## See also

- [./io.md](./io.md) — `io.lines` and `io.append`, the stream `csv.rows` reads and the file `csv.line` writes
- [../stdlib/sequences.md](../stdlib/sequences.md) — what a lazy seq can do
- [./decimal.md](./decimal.md) — exact money, which `csv.str` writes as text
- [./README.md](./README.md) — why `include` is required
