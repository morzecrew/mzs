Errors: raise, try/else/ensure, the error dict and kinds, $script_error, the «ошибка» exit, uncatchable limits.

# Errors

A script that fails — `raise`, a runtime error (`nil.upper`, `"a" + 1`, `1 / 0`), a compile error
or a limit — leaves the block by **«ошибка»** (`"error"`), and the scenario variable
`$script_error` gets the text, e.g. `script:7:3: raise: заказ не найден`
(`script:LINE:COL: KIND: MESSAGE`). Messages already sent with `bot.say` and variables already
saved with `bot.set` stay. A line `expr  # → value` shows the exact result.

## raise

```mzs-error
include bot
order = $message.captures(/(\d{5,})/)
raise("в сообщении нет номера заказа") if order == nil
raise("не удалось связаться с магазином", "network")
```

`raise(msg)` has kind `"raise"`; `raise(msg, kind)` names your own kind (any word except
`syntax`, `limit`, `exit`, `internal`). `raise({message: "…", kind: "…", data: {…}})` carries a
payload. `assert(cond, "msg")` raises when `cond` is falsy. There is no `throw`.

Use `raise` when the scenario should go down the «ошибка» branch; when you want your own
branch, choose an exit instead:

```mzs
include bot
if $message.blank {
  bot.say("Вы ничего не написали 🙂")
  bot.exit("no")
  return
}
bot.exit("yes")
```

`bot.exit("error")` also takes the «ошибка» exit, but leaves `$script_error` empty.

## try … else

`try` is an expression: the value of the body, or of `else` when the body raised.

```mzs
include json
try 10 / 0 else 0  # → 0
try json.parse("{oops") else {}  # → {}
n = try { "x".int + nil } else { -1 }
n  # → -1
kind = try nil.upper else (e) -> e["kind"]
kind  # → "name"
msg = try { raise("нет денег", "billing") } else (e) { "${e["kind"]}: ${e["message"]}" }
msg  # → "billing: нет денег"
p = try raise({message: "мало", kind: "stock", data: {left: 2}}) else (e) -> e["data"]["left"]
p  # → 2
```

Forms: `try X else Y`, `try X else (e) -> Y`, `try { … } else { … }`, `try { … } else (e) { … }`.
There is no `catch`, `except` or `rescue`. Braces are a scope: a variable first set inside a
`try { }` is gone after it, so take the value of the whole `try` instead.
In a header, parenthesise a braced try: `if (try { f() } else { false }) { … }`.

## The error dict

`e["message"]` (text without position), `e["kind"]`, `e["line"]`, `e["col"]`, `e["file"]`
(`"script"`), `e["stack"]` (array of `{fn, file, line, col}`), `e["data"]` (only with a payload).

```mzs
e = try [1, 2].fetch(5) else (err) -> err
e["kind"]  # → "name"
e["line"]  # → 1
e["message"]  # → "undefined method 'fetch' for array"
```

| kind | typical cause |
|---|---|
| `name` | undefined method/variable, method on `nil` (`nil.trim`) |
| `type` | `"a" + 1`, `"5" > 3`, `nil["k"]`, wrong argument type |
| `argument` | wrong number of arguments, `time.parse` of bad text |
| `key` | `d.fetch("missing")`, destructuring a missing key |
| `index` | destructuring the wrong number of values |
| `zero-division` | `1 / 0` with ints |
| `json` | `json.parse` of malformed text |
| `decimal` | `decimal.of("abc")`, inexact `decimal.div` |
| `regex` | bad pattern in `regex(…)` |
| `raise` | `raise("…")`, and errors from `bot.*` (bad exit name, too many says…) |

Re-raise what you do not handle: `try f() else (e) { match e["kind"] { "json" -> {}; else -> raise(e) } }`
— `raise(e)` keeps the original line.

## ensure

`ensure { … }` runs however the `try` is left (value, caught error, uncaught error, `return`):

```mzs
log = []
r = try { raise("x") } else { "fallback" } ensure { log.push("done") }
r  # → "fallback"
log  # → ["done"]
```

## What `try` cannot catch

The limits end the run and go straight to «ошибка», whatever `try` surrounds them: the 10 s
timeout, the 1 000 000-step budget, 16 MB of memory, call depth 64, a string over 256 KB, a
collection over 50 000 elements. `exit()` is not an error and is not caught either. Compile
errors (syntax, unknown function or method, `include` of a missing module) stop the script
before it starts.

```mzs-error
r = try { while true { } } else "never"
```

## Failing gently

A visitor should never see silence. Typical shape: do the risky part in `try`, tell the
visitor, then pick the exit.

```mzs
include bot
include json
r = bot.http({url: "https://api.example.com/orders/${bot.get("order_id")}", timeout: 3})
data = try json.parse(r["body"]) else nil
if !r["ok"] || data == nil {
  bot.say("Не получилось проверить заказ, попробуйте позже.")
  bot.set("lookup_error", r["error"].blank ? "HTTP ${r["status"]}" : r["error"])
  bot.exit("not_found")
  return
}
bot.set("status", data["status"] ?? "unknown")
bot.exit("found")
```

## Common runtime errors and their fix

| Message | Fix |
|---|---|
| `undefined method 'trim' for nil` | the value is `nil`: `(x ?? "").trim`, `x?.trim` |
| `cannot index nil` | `d.dig("a", "b")`, `d?.get("k")`, check `!= nil` first |
| `cannot add int to string` | `"${n}"` or `s + n.str` |
| `cannot compare string with int` | `bot.get("x").int > 5` |
| `undefined method 'x'; did you mean 'y'?` | use the suggested name |
| `undefined variable 'v'` | assigned only inside a `{ }` body — assign before it; or a missing `include` |
| `bot.exit: the block has no exit "x"` | use one of the block's exits, `"next"` or `"error"` |
| `bot.say: more than 10 messages` | join lines: `bot.say(lines.join("\n"))` |
| `bot.http: more than 3 requests` | fewer requests per run |
| `limit: step budget exceeded` | an endless loop, or too much work |
