Regex: literals and flags, ~ matching, captures and named groups, matches, replace, Cyrillic, dynamic patterns.

# Regular expressions

`/pattern/flags` is a value. Match with `~` (bool) — never `==` (a compile error) and never
`=~`. A line `expr  # → value` shows the exact result.

## Matching

```mzs
"Привет!" ~ /привет/i  # → true
"заказ 123" !~ /\d/  # → false
nil ~ /a/  # → false
"Нужна ДОСТАВКА" ~ /доставк|курьер/i  # → true
"Купить" ~ /^(купить|заказать)/i  # → true
"   " ~ /^\s*$/  # → true
```

Flags: `i` case-insensitive (Cyrillic included, `Ё`/`ё` too), `m` — `.` also matches a newline,
`x` — spaces and `# comments` in the pattern are ignored, `u` accepted and ignored.
`^` and `$` are **line** anchors (as in Ruby); `\A` and `\z` anchor the whole string:

```mzs
"строка 1\nстрока 2" ~ /^строка 2$/  # → true
"строка 1\nстрока 2" ~ /\Aстрока 2\z/  # → false
"x\ny" ~ /x.y/  # → false
"x\ny" ~ /x.y/m  # → true
```

For a whole-input check (validation) use `\A…\z` — or trim the input and know that it has no
newlines.

## Cyrillic

`\w`, `\d`, `\s` are **ASCII** classes: `\w` does not match Cyrillic letters. Use `\p{L}` (any
letter), `\p{Cyrillic}`, `\p{N}` (any digit), or explicit classes — and remember `ё` is outside
`а-я`. `\b` *is* Unicode-aware.

```mzs
"привет" ~ /^\w+$/  # → false
"привет" ~ /^\p{L}+$/  # → true
"Ёлка" ~ /^[а-яё]+$/i  # → true
"хочу меню" ~ /\bменю\b/  # → true
"Анна-Мария" ~ /^[\p{L} \-]+$/  # → true
"Привет, как дела?".lower.replace(/[^\p{L}\p{N}\s]/, "")  # → "привет как дела"
```

## captures — the first match and its groups

```mzs
m = "2026-08-13".captures(/(\d+)-(\d+)-(\d+)/)
m  # → ["2026-08-13","2026","08","13"]
m[1].int  # → 2026
m.drop(1).map { it.int }  # → [2026,8,13]
"нет цифр".captures(/(\d+)/)  # → nil
n = "Иван Петров".captures(/(?<first>\p{L}+)\s+(?<last>\p{L}+)/)
n["last"]  # → "Петров"
n[1]  # → "Иван"
"цена 1500 ₽".captures(/(\d+)(?= ₽)/)[1]  # → "1500"
```

`captures` returns `nil` when nothing matches, so guard before indexing:

```mzs
include bot
m = $message.captures(/заказ\D{0,3}(\d{4,})/i)
if m == nil {
  bot.say("Напишите номер заказа, например: заказ 12345")
  bot.exit("not_found")
} else {
  bot.set("order_id", m[1])
  bot.exit("found")
}
id = $message.captures(/(\d+)/)?.dig(1) ?? ""
```

`m?.dig(1)` is the nil-safe index (there is no `m?[1]`).

## matches — every match

```mzs
"a1b22c333".matches(/\d+/)  # → ["1","22","333"]
"a=1, b=2".matches(/(\w+)=(\d+)/)  # → [["a","1"],["b","2"]]
"a=1, b=2".matches(/(\w+)=(\d+)/).dict  # → {"a":"1","b":"2"}
"тел 8-900-111-22-33 и +7 901 222 33 44".matches(/(?:\+7|8)[\s\-()]*\d{3}[\s\-()]*\d{3}[\s\-]*\d{2}[\s\-]*\d{2}/)  # → ["8-900-111-22-33","+7 901 222 33 44"]
"нет".matches(/\d/)  # → []
```

Without groups: the matched strings. With groups: one array of the **groups** per match (no
whole match in it).

## index, count, split

```mzs
"привет".index(/вет/)  # → 3
"abcabc".count(/[ab]/)  # → 4
"раз, два;три".split(/[,;]\s*/)  # → ["раз","два","три"]
```

Indices are in letters (runes), not bytes. An index of `0` is truthy — compare with `nil`.

## replace

```mzs
"+7 (999) 123-45-67".replace(/\D/, "")  # → "79991234567"
"a  b   c".replace(/\s+/, " ")  # → "a b c"
"Иван Петров".replace(/(\p{L}+) (\p{L}+)/, '\2 \1')  # → "Петров Иван"
"2 шт и 3 шт".replace(/(\d+) шт/) { (m, n) -> "${n.int * 2} шт" }  # → "4 шт и 6 шт"
"мама мыла".replace_first(/м/, "М")  # → "Мама мыла"
```

`replace` is all matches; `replace_first` the first. Group references `\1`, `\k<name>` must be in a
**raw `'…'` string** — in `"…"` the `\1` escape becomes plain `1`. A closure gets the whole match,
then each group, and returns the replacement string.

## Dynamic patterns

`regex(source, flags)` compiles a pattern from a string at run time (cached). Write the source
as a raw `'…'` string so backslashes stay single. There is no interpolation inside `/…/`.

```mzs
words = ["доставк", "курьер", "привез"]
re = regex(words.join("|"), "i")
"Когда привезут?" ~ re  # → true
re.source  # → "доставк|курьер|привез"
regex('\d{3}') ~ "код 123"  # → true
```

Escape visitor text before putting it into a pattern, or (simpler) compare with `.lower.has(x)`:

```mzs
fn escape_re(s) { s.replace(/[.*+?^${}()|\[\]\\\/]/) { "\\" + it } }
escape_re("1.5 (шт)")  # → "1\\.5 \\(шт\\)"
"цена 1.5 (шт)" ~ regex(escape_re("1.5 (шт)"))  # → true
```

## Ready patterns

```mzs
email = /\A[\w.+-]+@[\w-]+(\.[\w-]+)+\z/
" Ivan.Petrov@Mail.ru ".trim ~ email  # → true
"ivan@mail" ~ email  # → false
phone_digits = "8 (999) 123-45-67".replace(/\D/, "")
phone_digits ~ /\A[78]\d{10}\z/  # → true
"25.12.2026" ~ /\A\d{2}\.\d{2}\.\d{4}\z/  # → true
"14:30" ~ /\A([01]\d|2[0-3]):[0-5]\d\z/  # → true
"А123ВС77" ~ /\A[АВЕКМНОРСТУХ]\d{3}[АВЕКМНОРСТУХ]{2}\d{2,3}\z/i  # → true
"https://shop.ru/item?id=1" ~ /\Ahttps?:\/\/\S+\z/  # → true
```

## Supported syntax

Groups `( )`, `(?: )`, named `(?<name> )`; classes `[…]`, `[^…]`; `\d \D \w \W \s \S \b \B \A \z
\Z \p{L} \p{N} \p{Cyrillic}`; quantifiers `* + ? {n} {n,} {n,m}` and lazy `*? +?`; lookahead
`(?= )` `(?! )`, lookbehind `(?<= )` `(?<! )`; backreferences `\1`; inline `(?i)`. An inline
`(?x)` is refused — use the `/…/x` flag. A `/` inside a literal is written `\/`. A pattern with
`\b`, lookaround or backreferences runs on a backtracking engine with a step budget: avoid nested
quantifiers like `(a+)+`.
