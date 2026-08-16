# Examples

Forty complete programs, in order of difficulty. Each one runs on its own, prints a
readable report, and is written to be read as much as to be executed:

```sh
mzs examples/01_values_and_operators.mzs
```

Two of them need a capability the host has to grant, which is the point of those files:

```sh
mzs --time examples/29_time_scheduling.mzs
mzs -v '__sent=I need an operator' examples/09_host_variables.mzs
```

Two more use the network, which needs no flag — but do need a deadline longer than the
default one second, because a request on the wire spends the Run's own time:

```sh
mzs examples/30_http_service.mzs               # serves on :8080 until GET /shutdown
mzs -t 30 examples/31_api_pipeline.mzs         # fetches a public API and reports on it
```

One reads files, and works because the CLI is a host that hands the filesystem over —
inside an embedder that does not, the same `include io` is a compile error naming the
option:

```sh
mzs examples/32_io_files.mzs                          # writes into $TMPDIR
printf 'yes\nno\nyes\n' | mzs examples/32_io_files.mzs # …and reads the pipe as data
```

Everything else runs with no flags, inside the default sandbox: one second, five million
steps, no clock, no randomness. The `http` module is the one capability that is simply
there for everyone — no flag, and still no `http` in a program that does not `include` it;
`io` is there because the *CLI* installs a filesystem, and an embedder that installs none
does not have it at all.

## The language

| # | File | What it is about |
|---|---|---|
| 01 | `01_values_and_operators.mzs` | the value model as a printed table: integer vs float division, `%` sign, overflow promotion, truthiness, `<=>`, nil |
| 02 | `02_control_flow.mzs` | Collatz trajectories: `while` as an expression, `break v`, `next`, statement modifiers, loops that report why they stopped |
| 03 | `03_match_dispatch.mzs` | both `match` forms — subject and predicate — with literal, list, range and regex arms, and a table of closures |
| 04 | `04_strings_unicode.mzs` | a contact cleaner: runes vs bytes, case and trim in Unicode, slicing, `replace` by closure, layout widths |
| 05 | `05_arrays_pipeline.mzs` | a month of sales through `map`/`filter`/`reduce`/`group_by`/`partition`/`each_cons`/`zip` |
| 06 | `06_dicts_records.mzs` | records, indexes and config: `dig`, `fetch`, `merge`, a recursive deep merge, insertion order |
| 07 | `07_functions_closures.mzs` | defaults, rest args, closures over state, `compose`, partial application, recursion, UFCS |
| 08 | `08_errors_and_validation.mzs` | `raise` with data, `try … else (e) ->`, the kind of every failure, validation that collects instead of raising, retry, `assert` |
| 09 | `09_host_variables.mzs` | `$vars`: unbound reads as nil, explicit conversion, writing values back for the host |
| 33 | `33_destructuring.mzs` | `a, b = pair`, nested patterns, `for k, v`, binding `match` arms, and what a mismatch does |
| 34 | `34_bits_and_bytes.mzs` | flags and masks with `band`/`bor`/`shl`, an IPv4 subnet test, `bytes`/`pack_bytes`, CRC-32, and why they are functions |
| 35 | `35_named_args_and_in.mzs` | `name = value` at a call site, defaults skipped rather than shifted, and `in` as an operator over ranges, arrays, dicts and strings |
| 36 | `36_ensure_and_error_kinds.mzs` | the braced `try { … } else (e) { … } ensure { … }`, a release that runs on every way out that leaves the run alive, and `match e["kind"]` over the runtime's kinds plus the ones a script names |
| 37 | `37_records.mzs` | `record` — a name for a shape over the dict you already had: fields by name, `type(m)`, a `match` arm on the shape, and what the label does and does not travel with. The one file that prints a warning on purpose |
| 38 | `38_heredoc.mzs` | `<<~TAG` — multi-line text with the common indentation shed, the raw `<<~'TAG'`, two on one line, and a template applied per row |
| 39 | `39_sets.mzs` | `union`, `intersect`, `difference`, `subset` and `to_set` — what tells a set operation from `+` and `-`, and the visited-set a graph walk always needed |
| 40 | `40_lazy_sequences.mzs` | `seq` — a billion-element range walked in three steps, a generator ended by `nil`, laziness counted rather than promised, and why a seq is not an array |

## Text, patterns and data

| # | File | What it is about |
|---|---|---|
| 10 | `10_regex_toolkit.mzs` | `~`, captures with named groups, `matches`, `replace` by closure, flags, `\b`, lookaround, a tokenizer |
| 11 | `11_log_parser.mzs` | access logs → error rate, latency percentiles, per-endpoint table, the client to block |
| 12 | `12_csv_report.mzs` | a real CSV scanner (quoted fields, doubled quotes), typed records, a totalled report, CSV back out |
| 13 | `13_word_frequency.mzs` | tokenising two scripts at once, stop words, bigrams, hapax legomena, a concordance |
| 14 | `14_text_layout.mzs` | word wrap, full justification, boxes, self-measuring tables, sparklines |
| 15 | `15_json_shaping.mzs` | parse a nested payload, `dig` past what is missing, reshape it, emit compact and pretty |

## Programs

| # | File | What it is about |
|---|---|---|
| 16 | `16_intent_router.mzs` | a bot NLU: scored rules, entity extraction, replies and an inline keyboard |
| 17 | `17_state_machine.mzs` | a booking dialogue as data: states, guards, retries, a handoff, and a driver that knows nothing |
| 18 | `18_order_pipeline.mzs` | validate every problem, price through a rule table, print a receipt and a manager's summary |
| 19 | `19_inventory_ledger.mzs` | stock movements as the source of truth: running balances, FIFO cost of sales, reorder alerts |
| 20 | `20_leaderboard.mzs` | competition, dense and ordinal ranking, movement between rounds, percentiles, head to head |

## Algorithms

| # | File | What it is about |
|---|---|---|
| 21 | `21_matrix_ops.mzs` | transpose, multiply, determinant by minors, Gaussian elimination, a rotation |
| 22 | `22_game_of_life.mzs` | a wrapping grid, twelve generations, cycle detection, and the classic patterns checked |
| 23 | `23_maze_bfs.mzs` | breadth-first search, a parent map, the path drawn on the maze, the distance field |
| 24 | `24_fuzzy_search.mzs` | Levenshtein with the table printed, "did you mean", catalogue search, name clustering |
| 25 | `25_roman_numerals.mzs` | Roman numerals both ways with validation, thousands separators, ordinals, bases |
| 26 | `26_memoization.mzs` | a cache in a closure, measured; a reusable `memoize`; FIFO eviction; when caching is wrong |

## Capabilities

| # | File | What it is about |
|---|---|---|
| 27 | `27_modules_main.mzs` | `include`/`export` across three files (`27_lib_money.mzs`, `27_lib_text.mzs`), private names, a diamond include |
| 28 | `28_async_tasks.mzs` | `async fn`, `await`, `done`, fan-out, errors at the await, shared state without locks |
| 29 | `29_time_scheduling.mzs` | **`--time`** · parsing four formats, durations, a slot grid, the first free gap, reminders |
| 30 | `30_http_service.mzs` | an http client call and a five-route JSON server with validation and shutdown |
| 31 | `31_api_pipeline.mzs` | **`-t 30`** · fetch a public JSON API, normalise the hits, then group, rank and tabulate them — with a sample to fall back on when the network is down |
| 32 | `32_io_files.mzs` | **`Options.FS`** · stdin as data, read/write/append/exists/ls/env, a missing file caught by `try`, and why the host resolves the path |
