---
name: mzs
description: Write correct mzs code for sandboxed chat-bot scripts — the «Скрипт» block of Morze Assistant (the `bot` module, $message, exits, limits) and the mzs language itself (strings, collections, regex, errors, JSON, time). Use when writing, fixing or reviewing mzs scripts, especially ones that run on a host-restricted interpreter.
---

# mzs in the «Скрипт» block

The block runs the script once, top to bottom, each time the dialogue reaches it. The script
talks to the bot only through `include bot`. It leaves by the exit it chose with
`bot.exit(name)`, by «дальше» (`"next"`, the default) when it chose none, and by «ошибка»
(`"error"`) when it fails: a `raise`, a runtime error or a limit. On failure the scenario
variable `$script_error` holds the text, e.g. `script:4:1: raise: заказ не найден`.

## The `bot` module (`include bot` first)

| Call | What it does |
|---|---|
| `bot.get("name")` | the visitor's variable as a **string**, or `nil` if unset. Not `$name`! |
| `bot.vars()` | dict of all visible variables (strings); studio `__*` vars hidden, except `__sent` |
| `bot.set("name", v)` | saves `v` as a string (`300` → `"300"`, `true` → `"true"`, array/dict → JSON, `nil` → `""`). Name `[A-Za-z_][A-Za-z0-9_]*`, not `__…`; ≤ 4000 bytes; ≤ 50 calls per run. Later blocks read it as `$name` |
| `bot.say(text)` | sends a bot message (markdown). ≤ 10 per run, ≤ 4000 bytes (≈ 2000 Cyrillic letters). Blank text is skipped |
| `bot.exit("name")` | picks the exit: one of the block's own exit names, `"next"` or `"error"`. An unknown name raises. The last call wins; the script keeps running after it |
| `bot.http({method, url, headers, body, timeout})` | internet request → `{ok, status, body, error}`; `body` is a string (send JSON as `payload.json`). ≤ 3 per run, ≤ 5 s each. **Never raises** for a failed request: check `r["ok"]` |

Globals: `$message` — what the visitor just wrote (a string, maybe `""`); `$client` — `"web"`
or `"telegram"`. No other `$var` is bound: scenario variables are read with `bot.get`.
`bot.say`/`bot.set` take effect immediately and are not undone if the script fails later.
`exit()` (or top-level `return`) stops the script cleanly, keeping the chosen exit.

```mzs
include bot
qty = (bot.get("qty") ?? "1").int          # variables are strings: convert
price = bot.get("price").int               # nil.int == 0, "1500".int == 1500
total = price * qty
bot.set("total", total)                    # later blocks see $total == "3000"
bot.say("Итого: **${total} ₽**")
if total > 2000 { bot.exit("yes") } else { bot.exit("no") }
```

## Syntax essentials

- A statement ends at a newline or `;`. Comments start with `#`. **`//` is not a comment.**
- Put every `include` at the top: a name is usable only below its `include` (functions too).
- No `let`/`var`/`const`: `x = 1`. `x += 1` (no `x++`). `nil`, `true`, `false` (no null/None).
- `if c { … } else if d { … } else { … }` — no parens needed, no `elif`/`elsif`/`unless`,
  no `do`/`end`, no `switch` (use `match`). `while c { }`, `for x in xs { }`, `for k, v in d { }`,
  `for i in 0..<n { }`. Loop control: `break`, `next` (not `continue`). `x = 1 if c` works.
- `&&`, `||`, `!` (not and/or/not). Ternary `c ? a : b`. `if`/`match`/`try` are expressions.
- **Every `{ }` is a scope**: a name first assigned inside an `if`/`for` body is gone after it.
  Assign it before: `label = "нет"; if ok { label = "да" }`.
- A call with a trailing closure inside an `if`/`while`/`match` header needs parens:
  `if (xs.any { it > 5 }) { … }`.
- Methods with no arguments need no parens: `s.trim`, `xs.len`, `d.keys` (`s.trim()` also works).
  A method binds tighter than unary minus: `-5.abs` is `-5`; write `(-5).abs`.
  `f(x, y)` ≡ `x.f(y)`, so `len(s)`, `str(5)`, `int("7")` work too.
- Dicts have **no fields**: `d["key"]`, never `d.key`. Literal `{name: "Ann", "content-type": "x"}`
  (keys are strings); empty dict `{}`; dynamic key `{(k): v}`.

## Values and truthiness

Only `nil` and `false` are falsy. **`""`, `0`, `[]`, `{}` are truthy.** So `if $message { }` is
always true — test `$message.blank`, `xs.empty`, `n == 0`, `x == nil` instead.
`==` never coerces: `"5" == 5` is false; `"5" + 1` and `"5" > 3` are errors. Convert:
`.int` (never raises: `"12abc".int == 12`, `"".int == 0`, `"1 500".int == 1`), `.float`,
`.str`; strict parse `"12".parse_int` → `12` or `nil`. `7 / 2 == 3` (int division), `7 / 2.0 == 3.5`.
Floats print as `1500.0`: round before showing (`(price * 1.2).round`, `"%.2f" % [x]`).

## Strings

- `"…"` interpolates `${expr}`: `"Привет, ${name}!"`. **`"$x"` is the global `$x`, not the
  local `x`** — locals always need `${x}`. `"$message"` works (it is a global).
- `'…'` is raw: no escapes, no interpolation (`'\d+'`). `"100 $"` is a literal dollar.
- `"a" + 1` is an error: `"a" + 1.str` or `"a${1}"`. `"ab" * 3`. Multi-line: heredoc `<<~T … T`.
- Rune-based: `s.len`, `s[0]`, `s[-1]`, `s[1, 3]` (start, count), `s.first(3)`, `s.last(2)`.
- Common: `.trim .lower .upper .capitalize .blank .empty .has("x") .starts_with("a", "b")
  .ends_with .index("x") .split(",") .lines .chars .replace(a, b)` (all) `.replace_first
  .ljust(n) .rjust(n, "0") .truncate(n) .count("x") .reverse .squeeze`. `"x" in s` ≡ `s.has("x")`.
- Regex: `s ~ /re/i` → bool (`!~` negates); `s.captures(/(\d+)/)` → `nil` or `[whole, g1, …]`;
  `s.matches(/\d+/)` → all matches; `s.replace(/\D/, "")`. `\w` is ASCII only — use `\p{L}`
  for Cyrillic letters, `[а-яё]` (ё is outside а-я). `\b` is Unicode-aware. `/i` folds Cyrillic.

## Arrays, dicts

- Arrays: `.len .first .last .push(x) .pop .has(x) .index(x) .join(", ") .sort .sort_by { }
  .reverse .uniq .sum .min .max .take(n) .drop(n) .compact .map { } .filter { } .reject { }
  .find { } .any { } .all { } .count { } .reduce(0) { (acc, x) -> … } .group_by { } .tally`.
  `xs[99]` is `nil`. `[1] + [2]`. `"a" in ["a", "b"]`.
- Dicts (insertion-ordered): `d["k"]` (`nil` if absent), `d["k"] = v`, `.keys .values .has("k")
  .get("k", default) .delete("k") .merge(other) .dig("a", 0, "b") .each { (k, v) -> } .map { (k, v) -> }
  .filter { (k, v) -> }`. `d.fetch("k")` raises when absent.

## nil-safety

`x ?? "default"` (fires on nil only). `nil["k"]` and `nil.trim` are **errors**: use
`x?.trim` (nil if x is nil), `d.dig("a", "b")` (nil-safe path), `(bot.get("city") ?? "").trim`.
`try expr else fallback`; `try { … } else (e) { e["message"] }` catches script errors
(`e` has `message`, `kind`, `line`). Limits (time, steps, memory) are not catchable.
`raise("текст")` fails the block → «ошибка», `$script_error` gets the text.

## match, functions, closures

```mzs
include bot
fn intent(text) {
  match text.lower.trim {
    "да", "ок", "yes"         -> "yes"
    in ["нет", "не надо"]     -> "no"
    /оператор|человек/        -> "other"
    else                      -> "other"
  }
}
level = match { 5 > 3 -> "high"; else -> "low" }   # no subject: conditions
double = { it * 2 }                                 # closure; `it` is the one argument
add = { (a, b) -> a + b }
fn area(w, h = 2) { w * h }                         # last expression is the value
bot.set("intent", intent($message))
bot.set("calc", "${[1, 2].map(double)} ${add(1, 2)} ${area(3, h = 5)} ${level}")
```

Arms: a literal compares `==`, a regex matches, `in xs` is membership, `a, b` is “or”,
`if cond` after the pattern is a guard, `else` last. No arm and no `else` → `nil`.
`fn` checks its arity; a closure `{ }` does not. `return` leaves the nearest `fn` — at the top
level, even inside `xs.each { }`, it ends the whole script (use `next` to skip an element).

## The sandbox

Limits per run: 10 s, 1 000 000 steps, 16 MB, call depth 64, a string ≤ 256 KB, a collection
≤ 50 000 elements, the code ≤ 20 000 characters, 3 `bot.http`, 10 `bot.say`, 50 `bot.set`.
**Not available:** the `http` and `io` modules (use `bot.http`; no files, no env),
`include x from "…"`, `rand`/`uuid`/`shuffle`/`sample`/`crypto.random`, `async fn`/tasks,
`eval`. `println`/`print` only show in the studio's trial run, never to the visitor — talk
with `bot.say`. **Available modules:** `json`, `time`/`date` (the clock is UTC: use
`.in_time_zone("Europe/Moscow")`), `decimal`, `math`, `url`, `crypto` (digests, hmac, base64,
hex), `csv`, `path`. Each needs its `include` (`include json`), then `json.parse(s)`; encoding is
`x.json` (after `include json`, `json(x)` is an error).

## Habits from other languages that break

| Wrong | Right |
|---|---|
| `s.length`, `xs.size`, `len` property | `s.len`, `xs.len` |
| `.strip`, `.lstrip`, `.rstrip` | `.trim`, `.trim_start`, `.trim_end` |
| `.downcase`/`.toLowerCase()`, `.upcase` | `.lower`, `.upper` |
| `.include?(x)`, `.includes(x)`, `.contains(x)`, `.has_key?` | `.has(x)` or `x in s` |
| `.empty?`, `.blank?`, `.any?`, `isEmpty` | `.empty`, `.blank`, `.any` (no `?` in names) |
| `.to_i`, `.to_f`, `.to_s`, `parseInt(s)`, `toString()` | `.int`, `.float`, `.str`, `s.parse_int` |
| `.gsub(a, b)`, `.sub(a, b)` | `.replace(a, b)` (all), `.replace_first(a, b)` |
| `.match(re)`, `.scan(re)`, `=~`, `s == /re/` | `.captures(re)`, `.matches(re)`, `s ~ /re/` |
| `.select`, `.collect`, `.detect`, `.inject`, `.forEach` | `.filter`, `.map`, `.find`, `.reduce`, `.each` |
| `.append(x)`, `arr << x` | `.push(x)` |
| `.substring(a, b)`, `s[a:b]` | `s[a, count]`, `s.slice(a, count)` |
| `"#{x}"`, `f"{x}"`, `` `${x}` `` | `"${x}"` |
| `{ \|x\| … }`, `x => …`, `lambda` | `{ (x) -> … }`, `{ it … }` |
| `null`, `None`, `undefined`, `True` | `nil`, `true` |
| `d.key`, `d.get("k")` on nil | `d["key"]`, `d?.get("k")`, `d.dig("k")` |
| `try … catch (e)`, `rescue`, `throw` | `try … else (e) { … }`, `raise("…")` |
| `JSON.parse(s)`, `JSON.stringify(x)` | `include json` then `json.parse(s)`, `x.json` |
| `$price` for a scenario variable | `bot.get("price")` (and `.int` for a number) |
| `if bot.get("x")` meaning “non-empty” | `if !(bot.get("x") ?? "").blank` |
| `r["error"]` as a failure test (`""` is truthy!) | `if !r["ok"] { … }` |

## More of the language

Load a reference when the task needs it; every ```mzs example in them compiles and runs in the
sandbox described above.

- [`references/collections.md`](references/collections.md) — Collections: arrays, dicts, ranges, sets and lazy seq — every useful method with exact results.
- [`references/control.md`](references/control.md) — Control flow: if/else, ternary, match patterns, loops, functions, closures and it, scopes, destructuring, records.
- [`references/errors.md`](references/errors.md) — Errors: raise, try/else/ensure, the error dict and kinds, $script_error, the «ошибка» exit, uncatchable limits.
- [`references/json-http.md`](references/json-http.md) — JSON and bot.http: parsing and building JSON, GET/POST requests, auth headers, query strings, error handling and exits.
- [`references/numbers.md`](references/numbers.md) — Numbers: int vs float, conversions from variables, rounding, formatting prices, decimal for exact money, math.
- [`references/recipes.md`](references/recipes.md) — Recipes: complete «Скрипт» blocks — phone/email validation, order status via API, price and discount, business hours, dates, keyword routing, auth API calls, saving variables, retries.
- [`references/regex.md`](references/regex.md) — Regex: literals and flags, ~ matching, captures and named groups, matches, replace, Cyrillic, dynamic patterns.
- [`references/strings.md`](references/strings.md) — Strings: quotes, interpolation, heredoc, every useful string method, formatting, converting to numbers.
- [`references/time.md`](references/time.md) — Time and dates: time.now in a time zone, parsing visitor input, strftime, durations, date math, business hours.
