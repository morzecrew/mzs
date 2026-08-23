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

## The two members

| Member | Signature | What it does |
|---|---|---|
| `case` | `(name: string) { … } -> bool` | runs the block, records the case, prints one line, answers whether it passed |
| `eq` | `(actual, expected) -> nil` | raises when the two differ, showing `inspect` of both |

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
    if !c.Passed() {
        log.Printf("%s: %v (%s)", c.Name, c.Err, c.Elapsed)
    }
}
```

`Tests` is empty for a program that never included the module, which is every program but a
test file.

## See also

- [../cli/tools.md](../cli/tools.md) — `mzs test`, its flags and its exit codes
- [../language/errors.md](../language/errors.md) — `raise`, `assert`, and what is never catchable
- [./custom.md](./custom.md) — `include … from` and `export`, for the code under test
- [../embedding/README.md](../embedding/README.md) — `RunResult` and the rest of the host API
