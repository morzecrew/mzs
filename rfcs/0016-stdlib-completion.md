# RFC 0016 — Standard-library completion

**Status:** Partly landed: §1–§5 (#32, #33, #34, #35) · **Area:** stdlib · **Depends on:** — · **Audit:** S5–S8

## Summary

A batch of rows whose absence is conspicuous *given their neighbours*: dict has `filter`
but not `reduce`, seq has `map` but not `zip`, math has `atan` but not `asin`. Each row
below is a distinct operation (D17) — nothing here renames or aliases anything.

The list was produced by diffing the receivers in `internal/apidoc/api.json` against each
other and probing the binary. It is deliberately not "everything Ruby has".

## Motivation

A library with holes teaches distrust: after `{a: 1}.reduce(…)` fails with `undefined
method 'reduce' for dict`, the author stops guessing and writes loops. The value of UFCS
and one flat namespace (D18) is that a name learned on one kind works on the next; that
only holds if the table is rectangular where it can be.

## Design

### 1. Dicts — the collection rows arrays already have

A dict closure takes `(k, v)`, as `filter`/`map`/`each` on dicts already do.

| Row | Signature | Note |
|---|---|---|
| `reduce` | `(d, init) { (acc, k, v) -> … } -> any` | observed: `undefined method 'reduce' for dict` |
| `count` | `(d) { (k, v) -> bool } -> int` | |
| `none` | `(d) { (k, v) -> bool } -> bool` | `all` / `any` exist |
| `group_by` | `(d) { (k, v) -> key } -> dict of dicts` | |
| `partition` | `(d) { (k, v) -> bool } -> [dict, dict]` | |
| `min_by` / `max_by` | `(d) { (k, v) -> … } -> [k, v] \| nil` | |
| `sum` | `(d) { (k, v) -> number } -> number` | |
| `map_values` | `(d) { (v) -> … } -> dict` | same keys, same order, **keeps a record label** (§7.8: same shape). `d.map { … }.dict` rebuilds through pairs and drops it |
| `pick` | `(d, *keys) -> dict` | the named entries that exist, in `d`'s order |
| `omit` | `(d, *keys) -> dict` | the rest |

### 2. Arrays and ranges

| Row | Signature | Note |
|---|---|---|
| `mean` | `(xs) -> float \| nil` | `nil` when empty; observed `[1,2,3].avg` undefined (and `sum / len` is integer division, D9) |
| `median` | `(xs) -> number \| nil` | mean of the middle two for an even count |
| `product` | `(xs) -> number` | empty → `1` |
| `transpose` | `(rows) -> array` | error on ragged input |
| `rotate` | `(xs, n = 1) -> array` | new array |
| `chunk_while` | `(xs) { (a, b) -> bool } -> array of arrays` | runs of neighbours; `each_slice` is by count only |
| `scan` | `(xs, init) { (acc, x) -> … } -> array` | the running `reduce` |
| `bsearch` | `(xs) { (x) -> int } -> int \| nil` | index in a sorted array |

### 3. Seq — what streaming pipelines need

| Row | Note |
|---|---|
| `zip(other)` | stops with the shorter |
| `each_slice(n)`, `each_cons(n)` | batches and windows over a stream: the reason to have a seq |
| `uniq` | remembers seen keys — charged to `MaxCollection`, documented as the one non-constant-memory row |
| `concat(other)` | one seq after another |
| `tally`, `group_by`, `min_by`, `max_by`, `last` | terminals; `tally`/`group_by` charge `MaxCollection` |

`sort`, `reverse`, `shuffle`, `sample` stay array-only: they need the whole input, and
`.array` is the honest spelling.

### 4. Strings

| Row | Signature | Note |
|---|---|---|
| `trim` / `trim_start` / `trim_end` | gain `(s, cutset: string = nil)` | observed: `trim expects at most 0 argument(s)`. A cutset is a set of runes |
| `drop` / `take` | `(s, n) -> string` | arrays have both; `"hello".drop(1)` is undefined |
| `truncate` | `(s, width: int, tail = "…") -> string` | by display `width` (§12.2), so CJK and emoji fit a column |
| `wrap` | `(s, width: int) -> array` | word wrap to lines; `examples/14_text_layout.mzs` hand-rolls it |
| `indent` | `(s, prefix: string) -> string` | every non-empty line |

`title`, `snake_case` and friends are not proposed: each has several defensible
definitions and a regex `replace` with a closure is one line.

### 5. Numbers and `math`

| Row | Note |
|---|---|
| `div(a, b) -> int` | **floored** division, the partner of `%`: `div(-7, 3) == -3`, so `div(a, b) * b + a % b == a`. `/` stays truncating (§8.3) |
| `sign(x) -> int` | -1, 0, 1 |
| `gcd(a, b)`, `lcm(a, b)` | ints |
| `is_nan(x)`, `is_inf(x)` | `x != x` is the only test today |
| `math.asin acos sinh cosh tanh` | complete the set |
| `math.inf`, `math.nan` | constants; today spelled `1.0 / 0` |

### 6. `time`

| Row | Note |
|---|---|
| `strftime`: `%s` (unix seconds), `%f` (microseconds), `%L` (milliseconds) | and an **unknown directive becomes an `argument` error** instead of echoing itself (audit B6) |
| `t.ms -> int` | unix milliseconds; `t.int` is seconds |
| `time.mono() -> float` | seconds from a monotonic clock with an arbitrary origin, for measuring intervals. Gated by `Options.Now` like `now`; wall-clock subtraction is wrong across clock steps |

### 7. `crypto`

| Row | Note |
|---|---|
| `sha512`, `sha384` | observed: `undefined member 'sha512'` |
| `hmac(…, "sha512")` / `"sha384"` | `hmac` already takes an algorithm name; it learns the two new ones |
| `random(n: int) -> string` | `n` cryptographically random bytes, hex-encoded. Gated: exists only when the host sets a new `Options.CryptoRand io.Reader` — **not** `Options.Rand`, which may be a seeded PRNG |

## Compatibility

Additive throughout (§21.2), with two points to state:

* `trim(s, cutset)` adds an *optional* parameter to an existing row.
  §21.1 says a name "keeps its arity"; a call that worked keeps working and means the
  same, which is the substance of that promise. The wording should become "a call that is
  valid stays valid".
* The `strftime` change turns silent output into an error — a §21.4 break, minor and
  deliberate.

**A new row is not free in method position.** SPEC §21.2 says a new name can only collide
with a local and that locals win. That holds for a call (`mean(xs)` calls the program's
`fn mean`) and not for a method: dispatch tries the receiver's stdlib table before a
lexical function (§8.7, `dispatch` in `mzs/call.go`), so today `fn sum(xs) { 99 };
[1, 2].sum` answers `3`. Every row this RFC adds would, the same way, take `xs.mean` away
from a program's own `fn mean` (audit B9). Two ways out, and this RFC takes the first —
which has since landed on its own, in #18:

1. **Program and host functions win in method position too** — dispatch checks a
   function visible in scope, and then a host function installed with `Register`, before
   the stdlib table. Today a registered `mean` answers `[1, 2].mean` because no row has
   that name; once a row does, the host's function must keep answering, or adding the row
   is a break for every host that registered one. That makes §21.2 true as written and every addition
   safe, and it is itself a change for a program — or a host — that relies on today's
   order. It lands first, on its own, as a §21.4 break whose changelog entry names both
   cases: a program function or parameter spelled like a row and called in method form,
   and a host function registered under a row's name (`Register("len", …)` now answers
   `"abc".len`). There is no warning for either: a deliberate shadow would warn on every
   compile for as long as it exists, and under `StrictWarnings` it would stop compiling,
   which is a worse break than the one being announced.
2. Treat every new row as a §21.4 break and list it in the changelog.

## Alternatives

* **Land rows one by one as asked for.** The pattern so far (T9–T15). The cost is the
  distrust described above; a batch with one review of naming is cheaper.
* **A `stats` module** for `mean`/`median`. Two rows do not make a module.
* **`avg`.** `mean` is the name `median` sits next to; one of them, per D17.

## Open questions

* `map_values` vs teaching `map` on a dict to return a dict when the closure returns a
  pair. The latter is a break; the former is a name.
* Does `div` earn a place beside `/`, or should `%` have been truncating? `%` is stable
  and used by `even`/wraparound code; adding the partner is the non-breaking fix.
* `time.mono` under a fixed test clock (`--now`, RFC 0014). Real elapsed time would make a
  test's answers vary between runs, which is what the fixed clock exists to stop. Proposed:
  the clock is injectable (`Options.Mono func() time.Duration`), and the fixed test clock
  is a *virtual* one — it starts at `0.0` and advances by exactly the durations the Run
  `sleep`s, so a test that sleeps 0.2 and measures sees 0.2 every time.

## Implementation

`mzs/dict.go`, `array.go`, `seq.go`, `str.go`, `num.go`, `time.go`, `crypto.go`,
`mzs/options.go` (`CryptoRand`), `renameTable` entries for the obvious near-misses
(`avg → mean`, `average → mean`, beside the existing `sub → replace_first`), SPEC §12, `docs/stdlib`,
apidoc regeneration, one corpus row per new function.

## As landed

### Part 1: dicts and arrays (#32)

§1 and §2 landed together, in `mzs/dict.go` and `mzs/array.go`, with SPEC §12.3 and
§12.4, `docs/stdlib`, and apidoc. Decisions:

* **`scan` is `accumulate`.** `scan` is in the §19.2 rename table as Ruby's spelling of
  `matches`. D17, which `TestNoAliasesForRenamedNames` enforces, keeps every name in that
  table out of the library, so a string's `scan` keeps saying `matches`. `accumulate` is
  Python's name for the same running fold (`itertools.accumulate`). The answer has one
  element per input, with or without an initial value, and its last element is the
  `reduce`.
* **`dict.reduce` requires `init`.** A dict has no first element to seed the fold with.
* **`dict.count`, `none` and `sum` require a closure.** The entry count is `len`, and a
  dict entry is a pair, so there is no element truthiness to fall back on. This is the
  rule `any` and `all` already follow. `TestDictHasNoOldNames` had listed `count` and
  `none` as forbidden aliases of `len` and `all`. They are distinct operations, the ones
  arrays have, so that table now says so, and `transform_values` points at `map_values`.
* **`map_values` passes the value alone**, as proposed, and keeps the record label.
  `pick` and `omit` do not keep it, because fewer fields is another shape. They keep the
  receiver's order, not the order the keys are named in.
* **`min_by` / `max_by` on a dict** give the `[k, v]` pair that `find` gives, the first
  on a tie, and `nil` for `{}`.
* **`median`** sorts numerically. An odd count gives the middle element itself, so an int
  stays an int, and an even count gives the float mean of the two middle elements. That
  mean is taken from the exact sum, an int as its int64, and rounded once, so ints past
  2^53, sums past `MaxFloat64` and subnormals all come out right.
* **`bsearch`** takes a closure in the `x <=> target` shape, since the language has
  `<=>`. It costs a step per probe. Which index it gives when several elements match is
  unspecified.
* **`transpose`** refuses a ragged table with an `argument` error naming the row, and
  charges a step per cell.

### Part 2: seq (#33)

§3 landed in `mzs/seq.go`, with SPEC §12.14, `docs/stdlib/sequences.md` and apidoc.
Decisions:

* **Lazy or terminal.** `zip`, `concat`, `uniq`, `each_slice` and `each_cons` are lazy
  and return a seq. `last`, `min_by`, `max_by`, `tally` and `group_by` are terminals.
  `each_slice` and `each_cons` given a closure walk the sequence and return the receiver,
  as the array rows do. Without one they are a seq of arrays, which is what a stream in
  batches wants.
* **`zip` stops with the shortest**, as proposed. The array row pads with `nil`, which
  over an endless argument would never end. The receiver is pulled first, and an
  argument only after the receiver has given an element, so a stateful argument loses
  nothing to a receiver that has run out. Each argument is converted as `seq(x)`
  converts, when the row is built.
* **`concat` makes a new seq.** The array row appends in place; a seq is a recipe, and
  recipes are not edited. Each part is opened only when the one before it ends.
* **What a row keeps is charged as it grows.** `uniq` charges its keys to
  `MaxCollection`, as the RFC asks, and so do `tally`'s dict, `group_by`'s dict and each
  bucket, `last(n)`'s window, and the batch of `each_slice` or `each_cons`. `uniq` over
  an endless source of distinct keys ends on the cap.
* **The memory walk sees what a lazy row keeps.** A lazy row's state lives in a Go
  closure, which the walk of RFC 0011 cannot look inside. So every traversal has an
  owner, made and rooted by the frame that pulls it, and a cursor registers what it keeps
  with the owner it is opened under. A cursor opened mid-traversal — each inner sequence
  of a `flat_map`, each part of a `concat` — gets a child owner, which is forgotten when
  the cursor ends, so `(1..10000).seq.flat_map { s.uniq }` keeps one inner `uniq`'s keys
  at a time. The owners form a tree rather than a stack: a cursor that ends takes only
  what it kept, and its siblings under a `zip` keep theirs. A child owner also holds the
  value the `flat_map` closure returned, which nothing else holds while it is pulled.
* **`sort`, `reverse`, `shuffle` and `sample` stay array-only**, as proposed. They cannot
  give their first element until they have seen the last.

### Part 3: strings (#34)

§4 landed in `mzs/str.go`, with SPEC §12.2 and §21.1, `docs/stdlib/strings.md`,
`examples/14_text_layout.mzs` and apidoc. Decisions:

* **A cutset is a set of runes**, as proposed. `trim(nil)` is the whitespace trim, so a
  script can pass an optional cutset through unchanged, and `trim("")` trims nothing.
* **§21.1 now says "a call that is valid stays valid"**, as the RFC asks. A name may gain
  an optional parameter, but it may not lose one or make one required.
* **`take` and `drop` count runes and refuse a negative count**, as the array rows do.
  `first(n)` and `last(n)` keep their own rule, a negative `n` giving `""`, because they
  already shipped with it.
* **`truncate` counts the tail toward the width.** A tail wider than the width is an
  `argument` error, not a result that overruns its column. A combining mark is kept or
  dropped with the letter it follows.
* **`wrap` reflows the text as one paragraph**, the way Python's `textwrap.wrap` does: a
  newline is whitespace like any other. That is what the hand-rolled `wrap` in example 14
  did, so the example now calls the row and prints exactly what it printed before. A word
  wider than the width gets a line of its own and is never split, so no text is lost.
  Text with no words gives `[]`. The example's hand-rolled `ellipsis` is `truncate(n)`
  now too, with the same output.
* **`indent` leaves empty and whitespace-only lines alone**, as Python's `textwrap.indent`
  does, so it adds no trailing spaces, and it keeps `\r\n`. That is what "every
  non-empty line" means here.

### Part 4: numbers and math (#35)

§5 landed in `mzs/num.go` and the `math` module in `mzs/json.go`, with SPEC §12.5 and
§12.8, `docs/stdlib/numbers.md`, `docs/modules/math.md` and apidoc. §6 (time) and §7
(crypto) are the part still to come. Decisions:

* **`div` follows `/` everywhere but the rounding.** Two ints give an int, an int divisor
  of 0 is the same `zero-division` error, `div(MinInt64, -1)` promotes to a float as `/`
  does (D9), and a float operand gives the float floor with IEEE on a zero divisor. The
  only difference is the direction of rounding, which is what makes it `%`'s partner.
* **`sign` is an int for either kind**, so it can multiply anything. `sign(-0.0)` is `0`.
  NaN has no sign, and any answer would be a guess, so it is an `argument` error.
* **`gcd` and `lcm` take ints only.** A float has no divisors, and truncating one would be
  a guess. Both are non-negative, `gcd(0, 0)` is `0`, and `lcm` with a `0` is `0`. They
  are computed on the magnitudes as unsigned ints, so `MinInt64` works, and a result past
  `MaxInt64` — `gcd(MinInt64, 0)`, or an `lcm` that overflows — promotes to a float as
  any int arithmetic does (D9).
* **`is_nan` and `is_inf` are false on an int**, rather than undefined on one, so a test
  does not need to know which kind it holds. `is_inf` is true for both infinities; the
  sign is `sign`.
* **`gcd` and `lcm` leave `TestNumberHasNoOldNames`.** That table had them as Ruby names
  the library dropped with no replacement. They are now the operation itself, under the
  name everyone uses, and the table's `nan`, `finite` and `infinite` rows point at
  `is_nan` and `is_inf`.
* **`math.inf` and `math.nan` are constants**, next to `pi` and `e`. The lexer still has
  no `Inf` or `NaN` literal (SPEC §3).
