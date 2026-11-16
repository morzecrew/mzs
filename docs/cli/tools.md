# `mzs fmt`, `mzs test`, `mzs lsp`

Three subcommands that do not run a program: one formats source, one runs the tests written
in mzs, one answers an editor. They are words rather than flags for that reason, and they
are matched as a bare first argument — a script of your own is never shadowed, because a
file is `fmt.mzs` and not `fmt`.

## `mzs fmt`

One spelling of a program, chosen by the tool so that nobody has to choose it in review.

```sh
mzs fmt a.mzs             # print the formatted text
mzs fmt -w src/           # write it back to every .mzs file under src/
mzs fmt -l .              # name the files that are not already formatted
mzs fmt < a.mzs           # a filter: stdin to stdout, for format-on-save
```

| Flag | Meaning |
|---|---|
| `-w, --write` | write the result back to each file instead of printing it |
| `-l, --list` | print the name of each file that is not already formatted |
| `-h, --help` | the usage text |

A path that is a directory is walked for `.mzs` files, in a stable order, skipping dot
directories. A path that is a file is formatted whatever it is called: the person typing it
has already decided.

### What it decides

Four spaces per level. Spaces around every binary operator. One space after a comma and
none before it. A collection, an argument list or a method chain that does not fit inside
**96 columns** breaks — the chain by its dots, everything else one element to a line:

```ruby
report = orders.filter { it["total"] > 1000 }
    .group_by { it["user"] }
    .map { (user, rows) -> {user: user, total: rows.sum { it["total"] }} }
    .sort_by { -it["total"] }
```

There is nothing to configure, for the reason `gofmt` has nothing: a formatter with a knob
is a formatter you argue about.

### What it leaves alone

* **The text of a literal**, which is copied from the source. `0xff` does not become `255`,
  `1.50` does not become `1.5`, and `'raw \n string'` gains no escapes.
* **A body written across lines.** `fn f(a) { a }` keeps its line, and a body opened onto
  several keeps its shape. The formatter breaks a brace that has grown too long for one line
  and never closes one the author opened: where a program is cut into steps is a claim about
  the program, not about spacing.
* **A statement whose text includes a heredoc**, which is moved sideways with the code
  around it and otherwise reproduced as written. The body's lines are the program's data,
  and re-flowing data is not formatting.
* **Parentheses**, which say what the author meant to say.

The one spelling it does not keep is the arrow function: `(a, b) -> { a + b }` and
`fn(a, b) { a + b }` are the same node in the tree, so both come out as the second. It is
the only place where two ways of writing a thing reach the formatter as one.
* **Comments**, which keep the line they were written on, or the line above the statement
  they were written above. Nothing is ever re-wrapped, and a blank line the author left
  survives as exactly one blank line. A comment inside a bracket keeps its place as well —
  the broken form gives every element a line and adds one before the closing bracket — and
  a comment written where the output has no line for it, between an operator and its
  operand for one, is written out at the next place that does. It moves; it is never
  dropped.

The one thing it normalises without being asked is the line ending: the output is `\n`
throughout, heredoc bodies included. The lexer drops a carriage return either way, so the
text a program holds is the same before and after.

### It refuses rather than rewrites

Every run reparses its own output and compares the tree *and* the comment text with what it
started from. A mismatch leaves the file alone and says so:

```
mzs fmt: formatting a.mzs would change the program (this is a bug in mzs fmt)
```

A file that does not parse is reported and skipped, and `mzs fmt` over a directory keeps
going: everything it has to say about every file, rather than a stop at the first typo.

Exit codes: `0` ok, `1` a file that could not be read, parsed or written, `2` usage.

## `mzs test`

Finds the `*_test.mzs` files, runs each one, and answers with a status.

```sh
mzs test                  # every *_test.mzs under the working directory
mzs test lib/             # …under one directory
mzs test money_test.mzs   # one file, whatever it is called
```

A test file names its cases with the [`test`](../modules/test.md) module:

```ruby
include test

test.case("нормализация") { assert("  ДА ".lower.trim == "да") }
test.case("суммы")        { test.eq([1, 2, 3].sum, 6) }
```

```
demo_test.mzs
ok   нормализация
FAIL суммы
     expected 7, got 6 (demo_test.mzs:4:29)

1 file, 2 cases, 1 failed  (1ms)
```

| Flag | Meaning |
|---|---|
| `-t, --timeout <d>` | wall clock per file (default `1s`; `0` disables) |
| `--steps <n>` | step budget per file |
| `--mem <size>` | live memory budget per file (`64m`; `0` disables) |
| `--tasks <n>` | tasks running at once, for `async fn` |
| `--time` | enable the `time`/`date` modules and a real clock |
| `--rand [seed]` | enable `rand()`/`uuid()` |
| `--no-io` | withhold the `io` module |
| `--no-trace` | a file that fails outside a case prints its three lines without the call stack |
| `--now <t>` | `--time` with a fixed clock at `t`, an RFC 3339 time such as `2026-01-31T09:00:00Z` |
| `--filter <re>` | run only the cases whose full name (`group › case`) matches the regular expression |
| `--fail-fast` | stop at the first failed case or broken file |
| `--json` | one JSON object per case on stdout, then a summary; what the scripts print goes to stderr |
| `-v, --verbose` | print what each case printed under it, passing cases included |

`--filter` narrows a file to the cases it names; a case that does not match is not run and
not counted, but the file's own code still runs, since it is what declares the cases:

```sh
mzs test --filter 'rounding'          # every case in the rounding group
mzs test --filter '^totals › with '   # a prefix of one group
```

`--json` is for CI. Every case is one line, and a last line sums them up:

```
{"col":6,"file":"money_test.mzs","group":["rounding"],"line":4,"message":null,"ms":0,"name":"half up","status":"pass"}
{"col":6,"file":"money_test.mzs","group":["totals"],"line":7,"message":"expected 30.00, got 29.99 (money_test.mzs:8:5)","ms":0,"name":"with discount","status":"fail"}
{"broken":0,"cases":2,"failed":1,"files":1,"ms":1,"skipped":0,"summary":true}
```

`status` is `pass`, `fail` or `skip`, and `error` for a file that did not finish. `name` is
`null` in two rows that are not cases: an `error` row, and the `skip` row of a `test.skip`
outside every case, which ended the file. `line` and `col` are where the case was declared —
where `test.skip` was called, for that row, and where the error was raised, for an `error`
row; a failure's own position is in its `message`, as it is in the human report.

`--now` fixes the clock, so code that asks what day it is gets the same answer on every
run — the only way a test of "is this invoice overdue" can mean anything tomorrow.

A failed case prints, under its message, the calls inside the case that led to the failure —
`in must, called from money_test.mzs:4:11` — and nothing of the case's own block or the
runner.

Each file is its own **run**: its globals, its includes and its budget reach no other file,
so a test that leaves `$state` behind cannot make the next file pass, and the order the
files run in is not something anybody has to know.

Under a failed case also comes what it printed, each line after `     | `; a passing case's
output is shown only under `-v`. A skipped case is reported as `skip name  (reason)` and
counted in the summary, `1 file, 3 cases, 1 failed, 1 skipped`, and it is not a failure.

A file that raises **outside** a case did not finish, and the summary counts that apart from
a case that failed — the fixes are different. Exit codes: `0` every case passed or was
skipped, `1` a case failed or a file did not finish, `2` usage.

## `mzs lsp`

The language server. It is started by an editor, not by a person, and it speaks the
Language Server Protocol on stdin and stdout.

| What the editor asks | What it gets |
|---|---|
| diagnostics | the compiler's errors and warnings, on open, on change and on save |
| hover | the signature and the meaning of the name under the cursor |
| go to definition | the `fn`, `record` or `include` that bound the name; the file itself for `include … from` |
| completion | members after a `.`, and names, modules and keywords elsewhere |
| format document | the whole file through `mzs fmt` |
| outline | what the file declares, in source order |

The [VS Code extension](../../editors/vscode/README.md) starts it for you. Any other editor
needs one line of configuration naming the command `mzs lsp` for the `mzs` language.

Two things worth knowing. **Every capability is installed while checking**, so `include
time` is not an error in an editor: which flags the program will be run with is not
something a buffer knows, and a diagnostic that depends on a flag is a diagnostic about a
command line. And **the server never runs a program** — it compiles — so an `include … from`
is not read and nothing in the file has an effect.

## See also

- [./README.md](./README.md) — every flag of the `mzs` command itself
- [./diagnostics.md](./diagnostics.md) — the shape of the messages the server forwards
- [../modules/test.md](../modules/test.md) — `test.case`, `test.raises` and the rest of the module
- [../reference/verification.md](../reference/verification.md) — the checks that run on every push
