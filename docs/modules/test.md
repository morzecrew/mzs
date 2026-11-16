# The `test` module

Cases with names, a failure that does not stop the file, and a report a command can count.

```ruby
include test

test.case("нормализация") { assert("  ДА ".lower.trim == "да") }
test.case("суммы")        { test.eq([1, 2, 3].sum, 6) }
```

```sh
mzs test
```

```
demo_test.mzs
ok   нормализация
ok   суммы

1 file, 2 cases, 0 failed  (1ms)
```

`assert` has been in the language from the beginning and was never a runner: a file of
assertions stops at the first one that fails, says nothing about the ones after it, and has
no name to put in a report. This module is the missing half; [`mzs test`](../cli/tools.md#mzs-test)
is the command that finds the files and runs them.

## The members

| Member | Signature | What it does |
|---|---|---|
| `case` | `(name: string) { … } -> bool` | runs the block, records the case, prints one line, answers whether it passed (`true` for a skip) |
| `eq` | `(actual, expected) -> nil` | raises when the two differ, showing `inspect` of both |
| `ne` | `(actual, unexpected) -> nil` | raises when the two are equal: `expected a value other than 3` |
| `near` | `(actual, expected, eps = 1e-9) -> nil` | raises unless two numbers are within `eps` of each other |
| `raises` | `(kind = nil) { … } -> dict` | raises unless the block fails — with an error of `kind`, when given — and answers the error |
| `skip` | `(reason) -> never` | ends the case as skipped; outside a case, skips the rest of the file |
| `group` | `(name) { … } -> nil` | names the cases in its block `name › case`; groups nest |

Nothing here needs a host capability: like [`json`](./json.md) and
[`decimal`](./decimal.md), the include is all of it.

## A case catches; the file keeps going

```ruby
include test

test.case("first")  { raise("boom") }
test.case("second") { assert(true) }
```

```
FAIL first
     boom (demo_test.mzs:3:22)
ok   second
```

The second case runs, which is the whole point of naming them. What a case does **not**
catch is what [`try`](../language/errors.md) does not catch either — a timeout, a step
budget, `exit`. A run that ran out of time has not failed a test; it has stopped, and the
file ends there with the usual exit code.

## `eq` exists for its message

`assert(a == b)` can only say `assertion failed`. `test.eq(a, b)` says what it wanted and
what it got:

```ruby
test.case("case folding") { test.eq("Да".lower, "нет") }
```

```
FAIL case folding
     expected "нет", got "да" (demo_test.mzs:1:33)
```

Equality is the language's own, so `test.eq(1, 1.0)` passes exactly as `1 == 1.0` does, and
a dict compares by contents rather than by identity — see
[values](../language/values.md#equality). A test that disagreed with `==` would be testing
something no program can observe.

The argument order is `actual, expected`, the way the sentence reads: *expected 30.00, got
29.99*.

## Testing that something fails

mzs gives errors kinds so that programs can branch on them, and `test.raises` is how a test
says which one it expects. It answers the same dict a `try` handler binds, so the test goes
on to the message and the data:

```ruby
include test
include json

test.case("rejects broken input") {
  e = test.raises("json") { json.parse("{") }
  test.ne(e["message"], "")
}

test.case("says how short") {
  e = test.raises("billing") { raise({message: "short", kind: "billing", data: {short_by: 30}}) }
  test.eq(e["data"]["short_by"], 30)
}
```

```
FAIL wrong kind
     expected kind "json", got "zero-division": divided by 0 (demo_test.mzs:8:34)
FAIL no error
     expected an error, got 42 (demo_test.mzs:9:32)
```

Without a kind, any error will do. Like `try`, it does not catch a timeout, a budget or
`exit`: those still end the file.

## Floats: `near`

`0.1 + 0.2 == 0.3` is false wherever floats are IEEE, so `test.eq(0.1 + 0.2, 0.3)` fails.
`test.near` takes a tolerance, `1e-9` unless you give one:

```ruby
test.case("float sum") { test.near(0.1 + 0.2, 0.3) }
test.case("roughly")   { test.near(measured, 9.81, 0.05) }
```

Equal values are always near, so an infinity is near itself, and NaN is near nothing. Money
is a [Decimal](./decimal.md), whose arithmetic is exact: compare it with `eq`, which is
why `near` does not take one.

## Skipping

```ruby
test.case("bankers rounding") { test.skip("not implemented") }

include io
test.skip("needs DATABASE_URL") if io.env("DATABASE_URL") == nil
test.case("talks to the database") { … }
```

```
skip bankers rounding  (not implemented)
skip (rest of file)  (needs DATABASE_URL)
```

A skipped case is not a failure — a run of passes and skips is green — and it leaves its
block the way `return` leaves a function: an `ensure` on the way out runs, and no `try`
catches the skip. Called outside any case, `test.skip` ends the file, which is how a file
that needs something the host did not give it says so once instead of failing every case.

## Groups

```ruby
test.group("rounding") {
  test.case("half up") { … }
  test.case("bankers") { … }
}
```

```
ok   rounding › half up
ok   rounding › bankers
```

A group only names its cases. There are no setup or teardown hooks: shared setup is a
function the cases call, and cleanup is [`ensure`](../language/errors.md). An error in the
block outside a case ends the file just as it would without the group.

## What a case prints

`mzs test` holds what a case prints until the case is over. A passing case's output is
dropped, a failed case's is printed under it — it is evidence — and `mzs test -v` prints
every case's:

```
FAIL totals
     expected 30.00, got 29.99 (money_test.mzs:7:5)
     | subtotal 29.99
```

## What a test file looks like

Anything else a file needs is an ordinary include, and the code under test is an ordinary
[module of your own](./custom.md):

```ruby
# money_test.mzs
include test
include decimal
include money from "./money.mzs"

test.case("a sum stays exact") {
  test.eq(money.total([decimal.of("10.10"), decimal.of("20.20")]), decimal.of("30.30"))
}

test.case("a share rounds half up") {
  test.eq(money.share(decimal.of("10.00"), 3), decimal.of("3.33"))
}
```

Each file is its own run: its globals, its includes and its budget reach no other file, so
a test that leaves `$state` behind cannot make the next file pass.

## Reading the report from Go

Every case is recorded on the run and comes back in `Result.Tests`, which is what
`mzs test` counts and what an embedder reads to run its own scripts' tests:

```go
res, err := in.RunResult(ctx, prog, nil)
for _, c := range res.Tests {
    switch c.Status {
    case mzs.TestFail:
        log.Printf("%s: %v (%s)", c.FullName(), c.Err, c.Elapsed)
    case mzs.TestSkip:
        log.Printf("%s skipped: %s", c.FullName(), c.Reason)
    }
}
```

Each entry also has the case's `Group`, the `Line` and `Col` of its `test.case`, and, when
the host asked for it, its `Output`. An entry with an empty `Name` is the rest of a file
skipped. `Tests` is empty for a program that never included the module, which is every
program but a test file.

`Options.Test` is how a runner of your own asks for what `mzs test` does:

| Field | Effect |
|---|---|
| `Filter func(name string) bool` | run only the cases whose full name (`group › case`) it accepts; the others are not run or recorded |
| `FailFast bool` | end the run at the first failed case; `RunResult` answers an error that wraps `mzs.ErrFailFast` |
| `Quiet bool` | no report lines on `Stdout` |
| `Capture bool` | a case's output goes to its `Output`, and is printed under its report line when it failed |
| `Verbose bool` | with `Capture`, print a passing case's output too |

The zero value is the module as a script sees it on its own.

## See also

- [../cli/tools.md](../cli/tools.md) — `mzs test`, its flags and its exit codes
- [../language/errors.md](../language/errors.md) — `raise`, `assert`, and what is never catchable
- [./custom.md](./custom.md) — `include … from` and `export`, for the code under test
- [../embedding/README.md](../embedding/README.md) — `RunResult` and the rest of the host API
