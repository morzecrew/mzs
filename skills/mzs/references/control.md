Control flow: if/else, ternary, match patterns, loops, functions, closures and it, scopes, destructuring, records.

# Control flow, functions, records

Every construct is an expression with a value. A line `expr  # → value` shows the exact result.

## if / else, ternary, modifiers

```mzs
n = 7
size = if n > 5 { "большой" } else if n > 2 { "средний" } else { "малый" }
size  # → "большой"
n > 5 ? "да" : "нет"  # → "да"
label = "мало"
label = "много" if n > 5
label  # → "много"
n += 1 while n < 10
n  # → 10
```

- No parentheses needed around conditions; braces are required. `else if` (no `elif`/`elsif`).
- No `unless`/`until`: `if !(c) { }`, `while !(c) { }`. Infinite loop: `while true { … break … }`.
- An `if` without `else` whose condition fails has the value `nil`.
- `x = v if c` skips the whole assignment when `c` is false: `x` must exist before.
- A call with a trailing closure in a header needs parentheses (the first `{` opens the body):

```mzs
xs = [1, 8]
if (xs.any { it > 5 }) { "есть большие" } else { "нет" }  # → "есть большие"
big = xs.any { it > 5 }
if big { "ok" }  # → "ok"
```

A body that should *produce a dict* needs inner braces: `if ok { {code: 1} } else { {code: 0} }`.

## Scopes

Every `{ }` is a scope. `=` updates an existing variable from an outer scope, or creates a new one
in the current scope. So assign defaults **before** the `if`:

```mzs
reply = "Не понял вопрос"
if "цена" in $message.lower { reply = "Цены в каталоге" }
reply  # → "Не понял вопрос"
```

A name first assigned inside an `if`/`for`/`match` body does not exist after it
(`undefined variable`). `:=` always creates a new local (shadowing).

## match

```mzs
fn classify(text) {
  match text.lower.trim {
    "да", "ага", "yes"            -> "yes"
    in ["нет", "не", "no"]        -> "no"
    /оператор|менеджер|человек/   -> "other"
    "" -> "empty"
    else                          -> "other"
  }
}
classify("  ДА ")  # → "yes"
classify("позовите менеджера")  # → "other"
classify("")  # → "empty"
```

| Pattern | Fires when |
|---|---|
| `"text"`, `42`, `nil`, `true` | `subject == pattern` |
| `/re/` | `subject ~ /re/` |
| `in xs` | `xs.has(subject)` — array, range `1..5`, dict keys, substring of a string |
| `p1, p2` | either fires |
| `p if cond` | the pattern fires and `cond` is truthy (guard) |
| `[a, b]`, `[x, *rest]` | an array of that shape; names bind |
| `{status: "paid", total: t}` | a dict with those keys and values; `t` binds |
| `else` | always (last arm only) |

```mzs
total = 4200
match total { in 0..999 -> "мелкий"; in 1000..4999 -> "средний"; else -> "крупный" }  # → "средний"
order = {status: "paid", total: 4200}
match order { {status: "paid", total: t} if t > 3000 -> "VIP ${t}"; {status: "paid"} -> "оплачен"; else -> "ждём" }  # → "VIP 4200"
match [1, 2, 3] { [first, *rest] -> rest.len; else -> 0 }  # → 2
match "x" { "y" -> 1 }  # → nil
```

`match` with no subject is an `if`-ladder — each arm is a condition:

```mzs
h = 14
match { h < 12 -> "Доброе утро"; h < 18 -> "Добрый день"; else -> "Добрый вечер" }  # → "Добрый день"
```

Arms are separated by newlines or `;`. An arm body can be a block: `"да" -> { a = 1; a + 1 }`.
To compare with a variable's value inside an array/dict pattern, parenthesise it: `{id: (wanted)}`.

## Loops

```mzs
out = []
for x in [1, 2, 3, 4, 5] {
  next if x == 2
  break if x == 5
  out.push(x * 10)
}
out  # → [10,30,40]
for i in 0..<3 { out.push(i) }
for k, v in {a: 1, b: 2} { out.push("${k}${v}") }
out.len  # → 8
i = 0
while i < 3 { i += 1 }
i  # → 3
```

`next` (not `continue`), `break`. `for k, v in dict`; `for i, x in xs` does **not** give the index (it takes
each element apart as a pair) —
use `xs.each_with_index { (x, i) -> … }` or `for i in 0..<xs.len`. Loops count against the step
budget (1 000 000), so a `while true` without a `break` fails with a limit error.

## Functions

```mzs
fn greet(name, greeting = "Здравствуйте") { "${greeting}, ${name}!" }
greet("Анна")  # → "Здравствуйте, Анна!"
greet("Анна", "Привет")  # → "Привет, Анна!"
greet("Олег", greeting = "Добрый день")  # → "Добрый день, Олег!"
fn first_even(xs) {
  for x in xs { return x if x % 2 == 0 }
  nil
}
first_even([1, 3, 4, 6])  # → 4
```

- The last expression is the return value; `return` is for early exit.
- Named `fn`s are hoisted (callable above their definition). Arity is checked.
- Named arguments: `f(1, b = 2)` (not `b: 2` — that is a dict). Defaults are evaluated per call.
- Put every `include` at the **top** of the script: a function that uses `bot` defined above
  `include bot` fails to compile.
- A user function is also a method: `fn shout(s) { s.upper + "!" }` then `"да".shout` → `"ДА!"`.
- Recursion depth is limited to 64.

## Closures and `it`

```mzs
double = { it * 2 }
add = { (a, b) -> a + b }
f = fn(x) { x + 1 }
g = (x) -> { x * 10 }
[1, 2].map(double)  # → [2,4]
add(2, 3)  # → 5
f(1) + g(2)  # → 22
[1, 2, 3].map { (x) -> x * x }  # → [1,4,9]
{a: 1}.map { (k, v) -> "${k}:${v}" }  # → ["a:1"]
counter = 0
[5, 6].each { counter += it }
counter  # → 11
fn make_counter() { c = 0; { c += 1; c } }
next_id = make_counter()
next_id()
next_id()  # → 2
"слово".pipe { it.upper }  # → "СЛОВО"
```

A closure with no parameter list gets one parameter `it`. Parameters are always parenthesised:
`{ (x) -> … }`, never `{ |x| … }` or `x => …`. Closures capture variables by reference.
`return` inside a closure returns from the enclosing `fn` — at the top level it **ends the
script**; inside `each` use `next` to skip an element.

## Leaving the script early

```mzs
include bot
code = $message.trim
if code.blank {
  bot.say("Пришлите код из SMS.")
  bot.exit("no")
  return
}
bot.set("code", code)
bot.exit("yes")
```

`return` at the top level and `exit()` (from anywhere, also inside a function) stop the script
cleanly; the exit chosen with `bot.exit` stays. `raise("…")` stops it with an error (exit «ошибка»).

## Destructuring

```mzs
a, b = [1, 2]
a, b = [b, a]
"${a}${b}"  # → "21"
head, *tail = [1, 2, 3]
tail  # → [2,3]
{name, city: town} = {name: "Ира", city: "Казань", age: 30}
"${name}, город ${town}"  # → "Ира, город Казань"
user, host = "ivan@mail.ru".split("@")
host  # → "mail.ru"
```

Counts must match exactly (`a, b = [1, 2, 3]` raises); a dict pattern raises on a missing key.
Use `split("@", 2)` and check `.len` when the input may be malformed.

## Records

A named dict shape with a constructor:

```mzs
record Item(name, price, qty = 1)
tea = Item("Чай", 150)
tea.name  # → "Чай"
tea.qty  # → 1
tea.qty = 3
tea["price"] * tea.qty  # → 450
type(tea)  # → "Item"
tea.json  # → "{\"name\":\"Чай\",\"price\":150,\"qty\":3}"
match tea { Item {price} if price > 100 -> "дорого"; else -> "дёшево" }  # → "дорого"
```

`rec.field` works only for fields of a `record` declared in the script; plain dicts use `d["k"]`.

## Types

```mzs
type(1)  # → "int"
type("a")  # → "string"
type([1])  # → "array"
type({})  # → "dict"
type(nil)  # → "nil"
[1].is("array")  # → true
defined($message)  # → true
defined($nope)  # → false
```
