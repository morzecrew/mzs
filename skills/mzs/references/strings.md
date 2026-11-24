Strings: quotes, interpolation, heredoc, every useful string method, formatting, converting to numbers.

# Strings

Strings are immutable UTF-8; `len`, indexing, `first`, `reverse` count **runes** (letters), not
bytes. Every method returns a new string. A line `expr  # → value` below shows the exact result.

## Literals and interpolation

```mzs
name = "Анна"
n = 3
"Привет, ${name}!"  # → "Привет, Анна!"
"Товаров: ${n * 2} шт."  # → "Товаров: 6 шт."
"${n}00"  # → "300"
'raw ${name} \n'  # → "raw ${name} \\n"
"Цена 100 $"  # → "Цена 100 $"
"10\$ off"  # → "10$ off"
"tab\there"  # → "tab\there"
"a" + "b" + 1.str  # → "ab1"
"=" * 5  # → "====="
"${[1, 2]} ${ {a: 1} } ${nil}|"  # → "[1,2] {\"a\":1} |"
```

- `"…"`: escapes `\n \t \" \\ \$ \u{1F600}` and interpolation `${expr}` (any expression).
- `$name` inside `"…"` is the **global** `$name`. Only `$message` and `$client` exist in the
  block, so `"$x"` with a local `x` gives `""`. Always write `${x}` for locals.
- `'…'`: raw — no escapes but `\'` and `\\`, no interpolation. Use it for regex source text.
- `"a" + 1` is an error (`cannot add int to string`): convert with `.str` or interpolate.
- Interpolation converts with `str`: `nil` → `""`, arrays/dicts → JSON, `1.5` → `"1.5"`, `2.0` → `"2.0"`.

Heredoc for long messages (indentation is stripped, `${}` works, ends with a newline):

```mzs
include bot
name = bot.get("name") ?? "гость"
text = <<~MSG
  Здравствуйте, ${name}!
  Ваша заявка принята. Мы перезвоним в течение часа.
MSG
bot.say(text)
```

`<<~'MSG'` (quoted tag) is the raw form.

## Cleaning and case

```mzs
"  Привет \n".trim  # → "Привет"
"--x--".trim("-")  # → "x"
"  x ".trim_start  # → "x "
"  x ".trim_end  # → "  x"
"ПрИвЕт".lower  # → "привет"
"да".upper  # → "ДА"
"иван петров".capitalize  # → "Иван петров"
"иван петров".split(" ").map { it.capitalize }.join(" ")  # → "Иван Петров"
"a   b    c".squeeze(" ")  # → "a b c"
"Straße".casefold == "STRASSE".casefold  # → true
```

`trim` also removes NBSP and zero-width spaces that messengers insert.

## Testing

```mzs
s = "Хочу заказать доставку"
s.has("доставк")  # → true
"доставк" in s  # → true
s.lower.starts_with("хочу", "хотим")  # → true
s.ends_with("у")  # → true
"".empty  # → true
"   ".blank  # → true
" a ".blank  # → false
s.len  # → 22
s.lower.count("о")  # → 2
s.index("заказ")  # → 5
s.index("нет") == nil  # → true
```

`""` is **truthy**: `if s { }` does not test emptiness, use `s.blank` / `s.empty`.

## Pieces

```mzs
w = "привет"
w[0]  # → "п"
w[-1]  # → "т"
w[1, 3]  # → "рив"
w.slice(2, 2)  # → "ив"
w[10] == nil  # → true
w.first(3)  # → "при"
w.last(2)  # → "ет"
w.take(2)  # → "пр"
w.drop(4)  # → "ет"
w.chars  # → ["п","р","и","в","е","т"]
w.reverse  # → "тевирп"
"Очень длинное описание товара".truncate(12)  # → "Очень длинн…"
```

`s[a, n]` is *start and count*, not start and end. There is no `substring`.

## Splitting and joining

```mzs
"a,b,,c".split(",")  # → ["a","b","","c"]
"  раз  два   три ".split(" ")  # → ["раз","два","три"]
"a1b22c".split(/\d+/)  # → ["a","b","c"]
"abc".split("")  # → ["a","b","c"]
"k=v=w".split("=", 2)  # → ["k","v=w"]
"строка 1\nстрока 2".lines  # → ["строка 1","строка 2"]
["чай", "кофе"].join(", ")  # → "чай, кофе"
[1, 2, 3].join("-")  # → "1-2-3"
```

`split(" ")` splits on runs of whitespace and drops empty pieces.

## Replacing

```mzs
"a-b-c".replace("-", "+")  # → "a+b+c"
"a-b-c".replace_first("-", "+")  # → "a+b-c"
"+7 (999) 123-45-67".replace(/\D/, "")  # → "79991234567"
"Цена: 100".replace(/(\d+)/, '<\1>')  # → "Цена: <100>"
"2 и 3".replace(/\d+/) { (it.int * 10).str }  # → "20 и 30"
"ab".replace(/(a)(b)/) { (m, a, b) -> b + a }  # → "ba"
```

`replace` replaces **all** matches (it is Ruby's `gsub`); `replace_first` only the first.
`\1` / `\k<name>` in a raw `'…'` replacement refer to groups; a closure gets the match and groups.

## Padding and layout

```mzs
"7".rjust(3, "0")  # → "007"
"Чай".ljust(6, ".") + "150 ₽"  # → "Чай...150 ₽"
"ок".center(6, "*")  # → "**ок**"
"первая\nвторая".indent("> ")  # → "> первая\n> вторая"
"раз два три четыре".wrap(9)  # → ["раз два","три","четыре"]
```

## Formatting numbers into text

```mzs
"%.2f" % [3.14159]  # → "3.14"
"%05d" % [42]  # → "00042"
"%s — %d шт." % ["Чай", 3]  # → "Чай — 3 шт."
"%{n} шт." % {n: 3}  # → "3 шт."
format("%.1f%%", 12.345)  # → "12.3%"
"%-5s|" % ["ab"]  # → "ab   |"
1234567.str.replace(/\B(?=(\d{3})+(?!\d))/, " ")  # → "1 234 567"
```

Verbs: `%s %d %f %e %g %x %o %b %c %j(JSON) %%`, flags `- + 0 space #`, width, `.precision`.

## Strings to numbers

```mzs
"42".int  # → 42
" 42 руб".int  # → 42
"abc".int  # → 0
"".int  # → 0
"1 500".int  # → 1
"1500.75".int  # → 1500
"1500.75".float  # → 1500.75
"3,5".replace(",", ".").float  # → 3.5
"42".parse_int  # → 42
"42abc".parse_int  # → nil
"1,5".parse_float  # → nil
"ff".parse_int(16)  # → 255
```

`.int`/`.float` never raise (bad text → `0`), so they are safe for unset variables
(`nil.int == 0`). To ask *“is this a number?”* use `parse_int`/`parse_float`, which give `nil`.
Strip spaces first for “1 500”: `s.replace(/\s/, "").int`.

## Comparing

```mzs
"Да".lower == "да"  # → true
"abc" < "abd"  # → true
"5" == 5  # → false
"10".int > 9  # → true
```

`"5" > 3` is an error (`cannot compare string with int`) — convert first. `==` is exact:
normalise with `.lower.trim` before comparing visitor text.

## Odds and ends

`s.ord` (code point), `65.chr` → `"A"`, `s.bytes` (byte values), `s.width` (display columns),
`s.chomp`, `s.chop`, `s.each_char { }`, `s.swapcase`, `s.first_and_last`. `s.json` quotes a string
for JSON; `s.inspect` shows it with escapes.
