# 0000 — Audit: what mzs 0.1.0 does not have

**Audited:** `main` at `84422f9` (0.1.0 + Unreleased), by reading `SPEC.md`, `docs/`,
`internal/apidoc/api.json` and by running the built binary. Every "observed" line below is
real output of `mzs -e '…'`, not a reading of the source.

**Scope.** What is absent, what is present but thin, and what is specified but not
delivered. Things SPEC §20 rules out *permanently* (classes, operator overloading, `eval`,
process spawning, parallelism, external dependencies) are not gaps and are not listed.

## 1. Summary

mzs is unusually complete for a 0.1: one grammar, real diagnostics, a sandbox, a formatter,
a test runner and a language server. The holes are not in the foundations. They cluster in
four places:

1. **Shapes that are half-finished.** Destructuring exists for arrays only; records can be
   read with `.field` but not written; there is no rest element, no spread, no dict
   pattern. A program that models data with `record` falls back to `m["amount"]` as soon as
   it mutates or crosses a module boundary.
2. **Silent answers where a diagnostic was the house style.** `"abc".int` is `0`,
   `2 ** 64` is a float, a `match` with no arm that fits is `nil`, an unknown `strftime`
   directive prints itself. Each is specified; each is the kind of quiet reading D16
   exists to prevent elsewhere.
3. **A sandbox with one unlocked window.** Every limit is per-operation. A script that
   stays inside all of them can still hold gigabytes (§3.1 below).
4. **A scripting language without the scripting modules.** No CSV, no stderr, no path
   handling, no file removal, no flag parsing; the `test` module has two members.

## 2. Language

| # | Gap | Observed today | RFC |
|---|---|---|---|
| L1 | Rest element in destructuring | `a, *rest = [1,2,3]` → `syntax: unexpected '*'`; and `a, b = [1,2,3]` → `index: destructuring expects 2 values, got 3`, so there is no way to take a head | [0001](0001-rest-and-spread.md) |
| L2 | Splat at a call site | `f(*xs)` → `syntax: unexpected '*'` | [0001](0001-rest-and-spread.md) |
| L3 | Spread in literals | `{**d, b: 2}` → `syntax: unexpected '**'` | [0001](0001-rest-and-spread.md) |
| L4 | Dict destructuring | `{a, b} = {a: 1, b: 2}` → `syntax: unexpected ','` | [0002](0002-dict-and-record-patterns.md) |
| L5 | Dict / record patterns in `match` | `match d { {a: x} -> x }` → `expected '->' in match arm, found ':'`; a record arm can test the shape but not bind its fields | [0002](0002-dict-and-record-patterns.md) |
| L6 | Patterns in a `for` header | `for [a, b] in pairs` → `expected a loop variable, found '['` | [0002](0002-dict-and-record-patterns.md) |
| L7 | Record field write | `p.x = 5` → `syntax: cannot assign to this expression` (reading `p.x` works) | [0003](0003-record-field-assignment.md) |
| L8 | Record `.field` across modules | by SPEC §7.8 a shape from another module is read with `m["amount"]` | [0003](0003-record-field-assignment.md) |
| L9 | Constants | `const X = 1` → `syntax: unexpected 'X' after statement`. `=` writes through to any enclosing scope, and `fn f() { 1 }; f = 2` rebinds a function silently | [0004](0004-const-bindings.md) |
| L10 | Closed sets of variants | `enum …` → syntax error; `match` over record shapes is unchecked | [0005](0005-enum.md) |
| L11 | Non-exhaustive `match` is silent | `match 50 { 1 -> "a" }` → `nil` | [0005](0005-enum.md) (for enums) |
| L12 | Open-ended ranges / slices | `xs[1..]` → `syntax: unexpected ']'` | [0006](0006-open-ended-ranges.md) |
| L13 | Leaving a nested loop | `outer: for …` → syntax error; no labelled `break` | [0007](0007-labelled-loops.md) |
| L14 | Strict numeric parsing | `"abc".int` → `0`, `"x".float` → `0.0`, `"0x1f".int` → `0`; no way to parse hex, no way to learn the string was not a number | [0008](0008-strict-number-parsing.md) |
| L15 | **Landed (#20).** Error traces | SPEC §8.11: "errors carry … a short call stack". `*Error.Stack` is filled, but the CLI prints one position and `else (e)` gets `{message, kind, line}` — no file, column or stack | [0009](0009-error-traces.md) |
| L16 | Contracts on parameters | `fn f(x: int)` → `expected ')' in parameter list, found ':'` | [0010](0010-parameter-contracts.md) |
| L17 | Integers past 2⁶³ | `2 ** 100` → `1.2676506002282294e+30`; `fact(25)` → a float; `decimal` holds 19 digits | [0018](0018-big-integers.md) |

### Noted, not proposed

* **`is` and `type` disagree about tasks.** `type(t)` is `"task"`, yet `t.is("task")` is
  `argument: is: unknown type name "task"`. RFC 0010 fixes it, since contracts read the
  same table.

* **Capturing the subject in a scalar arm** — `match n { x if x > 3 -> … }` is
  `undefined variable 'x'`, because a bare identifier in an arm is an expression. The
  subject is almost always already a name, and `match { if n > 3 -> … }` covers it. Left
  as an open question of RFC 0002.
* **`${x:.2f}` format specs in interpolation.** `"%.2f" % x` and `x.round(2)` exist, and
  `:` inside `${ }` collides with the ternary. Not worth a second formatting language.
* **Generators / `yield`.** `seq { (i) -> … }` is a generator with a closure for state. A
  coroutine-style `yield` needs a second evaluation strategy in a tree walker; revisit
  only if `seq` proves too weak.
* **`|>`.** UFCS (D18) and `pipe` already are the pipeline.
* **Function composition / partial application.** A closure is one line.
* **Truncating `/` beside floored `%`.** `-7 / 3 == -2` and `-7 % 3 == 2`, so
  `(a / b) * b + a % b == a` does not hold for mixed signs. It is specified (§8.3) and
  stable; RFC 0016 adds `div` so a floored pair exists.

## 3. Embedding and safety

### 3.1 There is no aggregate memory limit — P1

**Fixed (#19)** by RFC 0011: `Options.MaxMemory`, default 64 MiB; the program below now ends
with `limit: memory budget exceeded` in about 60 ms.

`MaxCollection` and `MaxStringBytes` bound **one operation** (SPEC §14.2). Nothing bounds
their sum. With every option at its default:

```sh
mzs -e 'xs = []; s = "x" * 1000000; i = 0
        while true { xs.push(s + i.str); i += 1 }'
# -e:1:1: limit: execution timed out after 1s      — peak RSS ≈ 1.5 GB
```

About 1500 iterations fit in the second — a few thousand of the 5,000,000 default steps —
so the timeout is the only bound, and it scales with whatever the host sets. For a library whose
README says "a script cannot bring its host down", this is the one way a script can: the
host's OOM killer does not return an `error` at the `Run` boundary. → [0011](0011-memory-budget.md)

### 3.2 Other embedding gaps (no RFC yet)

* No per-Run hook for stepping or tracing (a debugger or a profiler has nothing to attach
  to); RFC 0019 proposes the profiler half.
* `http` is on by default and removed with `Unregister("http")` — the single exception to
  D15. It is deliberate and documented; an embedder who misses the sentence ships an SSRF
  primitive. Worth a louder note in `docs/embedding`, not a design change.

## 4. Standard library

| # | Gap | Evidence | RFC |
|---|---|---|---|
| S1 | **Landed (#PR).** No stderr | `eprintln` / `warn` are undefined; `Options.Stderr` exists but only warnings reach it. A filter in a pipeline cannot report without corrupting its output | [0012](0012-stderr.md) |
| S2 | No CSV | `include csv` → unknown module. `examples/12_csv_report.mzs` carries its own quote-aware scanner — commas and doubled quotes inside quotes — because `split(",")` is wrong for any quoted field; it still cannot read a quoted field that spans lines, and every script that needs CSV has to copy it | [0013](0013-csv-module.md) |
| S3 | `test` has two members | `include test; test.keys` → `["case","eq"]`; `test.raises` is undefined, so an error path cannot be asserted without a hand-written `try` | [0014](0014-test-module.md) |
| S4 | No path handling; `io` cannot remove, create a directory or stat | `include io; io.keys` → `stdin lines read write append exists ls env`; SPEC §12.13 says so on purpose | [0015](0015-path-and-io.md) |
| S5 | Uneven collection rows | dict has no `reduce`/`count`/`group_by`; array has no `mean`/`product`/`transpose`; seq has no `zip`/`uniq`/`each_slice`; `trim` takes no cutset | [0016](0016-stdlib-completion.md) |
| S6 | `math` stops at `atan` | no `asin acos sinh cosh tanh`, no `gcd`, no NaN/Inf test | [0016](0016-stdlib-completion.md) |
| S7 | `time` has no sub-second, no monotonic clock | `strftime("%s %f")` prints `%s %f` unchanged; nothing measures an interval | [0016](0016-stdlib-completion.md) |
| S8 | `crypto` has one SHA-2 | `crypto.sha512` undefined; no random bytes | [0016](0016-stdlib-completion.md) |
| S9 | No flag parsing | `$ARGV` is the raw array and that is all | [0017](0017-args-module.md) |

**Unicode normalisation** (`"é".normalize`) is also absent, and is *not* proposed: Go's
standard library has no NFC/NFD tables, so it would mean bundling them or breaking I1.

## 5. Tools

| # | Gap | Evidence | RFC |
|---|---|---|---|
| T1 | The linter is thin | `mzs --check -e 'x = 1; y = 2; x'` → `ok`: an unused local, a shadowed stdlib name and a write to an outer variable are all silent | [0019](0019-tooling.md) |
| T2 | LSP stops at five requests | hover, definition, completion, documentSymbol, formatting. No references, rename, signature help or code actions — although every diagnostic already carries a fix-it | [0019](0019-tooling.md) |
| T3 | No doc comments | nothing a hover or a generated page can show for a user's own `fn` | [0019](0019-tooling.md) |
| T4 | No coverage, no profile | `--stats` prints one step count for the whole run | [0019](0019-tooling.md) |
| T5 | `mzs test` has no filter, no machine output | flags are the run limits and nothing else | [0014](0014-test-module.md) |
| T6 | A library has a search path but no manifest | `$MZSPATH` (T19) finds a library; nothing records which one, or fetches it | [0020](0020-library-manifest.md) |
| T7 | No release | `CHANGELOG.md`: "no tag has been cut yet"; install is `git clone` + a Go toolchain | — (process, not design) |

## 6. Defects found on the way

These need a fix, not an RFC.

| # | Defect | Reproduce |
|---|---|---|
| B1 | **Fixed (#17).** A did-you-mean suggests the name that was just refused | `mzs -e 'now()'` → `undefined function 'now' (did you mean 'now'?)`. The gated builtin is in the suggestion table; the message should name the gate, as `include time` does (`needs a clock: … mzs --time`). Same for `rand`/`uuid` |
| B2 | **Fixed (#17).** `mzs test --help` prints an example that does not compile | it shows `test.eq(decimal("10.00") * 3, decimal("30.00"))`; `decimal` is a module, not a function, and `*` on a Decimal is an error. SPEC §12.18 has the right line (`decimal.times(decimal.of("10.00"), 3)`) |
| B3 | **Fixed (#17).** SPEC §4 still gives dicts square brackets | `Collection = "[" ( ArrayBody \| DictBody \| ":" ) "]"`, while D3 and the implementation say `{a: 1}` and `{}` (`[a: 1]` → `a dict is written {a: 1}`) |
| B4 | "Reserved" words are not reserved | SPEC §20 lists `class`, `module`, `yield`, `defer` as reserved syntax; `class = 1; yield = 2; defer = 3; module = 4; class + yield + defer + module` → `10`, and `defer { 1 }` → `undefined function 'defer'`. Either reserve them in the lexer or drop them from §20 — as written, adding any of them later breaks programs §21.1 promises to keep |
| B5 | **Fixed (#20).** The specified call stack is not shown | see L15; §8.11 promises it, nothing prints it |
| B6 | An unknown `strftime` directive is echoed | `mzs --time -e 'include time; time.parse("2026-01-01").strftime("%s %f %G")'` → `%s %f %G`. By D16 it should be an `argument` error naming the directive |
| B7 | Range indexing is unspecified, and a negative end is inconsistent | SPEC §8.8 lists `x[i]` and `x[i, n]` only; `x[a..b]` works but is not in the table. `[1,2,3][-2..-1]` → `[2,3]`, yet `[1,2,3][0..-1]` → `[]` and `"hello"[1..-1]` → `""` — the range is materialised before the bounds are resolved. RFC 0006 specifies the rule |
| B8 | A script cannot receive a flag without `--` | `mzs a.mzs --x 1` → `mzs: unknown flag --x`; only `mzs a.mzs -- --x 1` reaches `$ARGV`. Arguments after the script name should belong to the script (RFC 0017) |
| B9 | **Fixed (#18).** A local function does not shadow a stdlib row in method position | `fn sum(xs) { 99 }; [1, 2].sum` → `3`, while `sum([1, 2])` → `99`: dispatch tries the stdlib table before a lexical function (§8.7). So SPEC §21.2's "a new name can only collide with a local, and locals shadow the library" is false for method calls, and every new stdlib row can change a working program. RFC 0016 proposes the fix; until it lands, every RFC that adds a row carries this break |

## 7. Suggested order

1. **0011** (memory budget) — it is a hole in a promise the README makes.
2. **B9, then 0009, 0012, 0008** — small, and each removes a daily annoyance. Not all
   additive: 0009 changes the error dict a script observes and the shape of `mzs.Frame`,
   so it lands with a MINOR bump under §21.4. B9 (a local
   function wins over a stdlib row in method position, RFC 0016 §"A new row is not free")
   goes first because 0012, 0008 and 0016 add rows, and without it each addition can
   change a working program.
3. **0001 → 0002 → 0003** — one arc: finish destructuring and records.
4. **0013, 0014, 0015, 0017** — the scripting modules.
5. **0016, 0019** — breadth.
6. **0004, 0005, 0006, 0007** — language polish before 1.0 freezes the syntax.
7. **0010, 0018, 0020** — larger, and each has a real "or not at all" alternative.

B4 should be settled before any of this ships as 0.2: it decides whether RFC 0005's `enum`
and RFC 0004's `const` are *positional* keywords (like `record`) or true reserved words.
The RFCs assume positional.
