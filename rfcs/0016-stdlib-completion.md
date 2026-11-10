# RFC 0016 — Standard-library completion

**Status:** Draft · **Area:** stdlib · **Depends on:** — · **Audit:** S5–S8

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

A new name shadows nothing a program defined: locals win over the library (§8.7).

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
