Time and dates: time.now in a time zone, parsing visitor input, strftime, durations, date math, business hours.

# time and date

`include time` (and `include date` for `date.today`/`date.parse`). One value kind, `time`.
**The clock is UTC**: always convert with `in_time_zone("Europe/Moscow")` (any IANA name:
`Asia/Novosibirsk`, `Asia/Yekaterinburg`, `Europe/Kaliningrad`…) before reading hours or weekdays.
A line `expr  # → value` shows the exact result.

## Now

```mzs
include time
include date
now = time.now.in_time_zone("Europe/Moscow")
hour = now.hour
weekday = now.wday
today = now.strftime("%d.%m.%Y")
stamp = now.strftime("%Y-%m-%d %H:%M")
midnight = date.today
now.strftime("%z")  # → "+0300"
```

`now()` is the same as `time.now`. `date.today` is today 00:00 **UTC** — for “today in Moscow”
use `time.now.in_time_zone("Europe/Moscow").to_date`.

## Fields

```mzs
include time
t = time.parse("2026-03-05 14:30:15")
[t.year, t.month, t.day, t.hour, t.min, t.sec]  # → [2026,3,5,14,30,15]
t.wday  # → 4
t.yday  # → 64
t.int  # → 1772721015
t.to_date.str  # → "2026-03-05T00:00:00Z"
t.str  # → "2026-03-05T14:30:15Z"
type(t)  # → "time"
```

`wday`: Sunday = 0, Monday = 1 … Saturday = 6. `t.int` is unix seconds, `t.ms` milliseconds,
`time.at(unix)` goes back.

## Formatting

```mzs
include time
t = time.parse("2026-03-05 09:05")
t.strftime("%d.%m.%Y %H:%M")  # → "05.03.2026 09:05"
t.strftime("%-d.%-m.%y")  # → "5.3.26"
t.strftime("%A, %d %B")  # → "Thursday, 05 March"
months = ["января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"]
days = ["воскресенье", "понедельник", "вторник", "среда", "четверг", "пятница", "суббота"]
"${t.day} ${months[t.month - 1]}, ${days[t.wday]}"  # → "5 марта, четверг"
```

Directives: `%Y %y %m %d %H %M %S %j %a %A %b %B %p %z %Z %s %L %f %%`; `%-d`/`%-m` drop the zero.
Month and day names are **English only** — use your own arrays for Russian (above).

## Parsing

```mzs
include time
time.parse("2026-03-05").str  # → "2026-03-05T00:00:00Z"
time.parse("2026-03-05 14:30").str  # → "2026-03-05T14:30:00Z"
time.parse("05.03.2026").str  # → "2026-03-05T00:00:00Z"
time.parse("25.12.2026 14:30").str  # → "2026-12-25T14:30:00Z"
time.parse("25/12/26").str  # → "2026-12-25T00:00:00Z"
time.parse("2026-03-05T14:30:00+03:00").hour  # → 14
time.parse("5.3.2026", "2.1.2006").str  # → "2026-03-05T00:00:00Z"
time.parse("25.12.2026 9:05", "2.1.2006 15:04").str  # → "2026-12-25T09:05:00Z"
time.parse("05 March 2026", "%d %B %Y").str  # → "2026-03-05T00:00:00Z"
try time.parse("31.02.2026") else "нет такой даты"  # → "нет такой даты"
try time.parse("завтра") else "не дата"  # → "не дата"
```

- Without a layout: RFC3339, `YYYY-MM-DD[ HH:MM[:SS]]`, `DD.MM.YYYY[ HH:MM]`, `DD/MM/YY(YY)` —
  day first, two-digit day and month. `5.3.2026` needs a layout.
- A layout is strftime (`"%d.%m.%Y"`, two digits required) or Go reference style
  (`"2.1.2006 15:04"`, one or two digits accepted).
- Bad input raises (kind `argument`): wrap in `try`.
- A parsed time without an offset is read as UTC wall-clock. That is fine for comparing with a
  wall-clock “now” built the same way (see below), or add the offset to the text:
  `time.parse("${iso}T${hm}:00+03:00")`.

Robust parsing of what a visitor typed — normalise with a regex first:

```mzs
include time
fn parse_ru_date(text) {
  m = text.captures(/(\d{1,2})[.\/\-](\d{1,2})(?:[.\/\-](\d{2,4}))?/)
  return nil if m == nil
  y = m[3] ?? time.now.in_time_zone("Europe/Moscow").year.str
  y = "20${y}" if y.len == 2
  try time.parse("${y}-${m[2].rjust(2, "0")}-${m[1].rjust(2, "0")}") else nil
}
parse_ru_date("приду 5.3.2026").str  # → "2026-03-05T00:00:00Z"
parse_ru_date("25/12/26").str  # → "2026-12-25T00:00:00Z"
parse_ru_date("31.02.2026")  # → nil
parse_ru_date("когда-нибудь")  # → nil
```

## Durations and arithmetic

```mzs
include time
t = time.parse("2026-03-05 14:30")
(t + 90.minutes).strftime("%H:%M")  # → "16:00"
(t - 2.hours).strftime("%H:%M")  # → "12:30"
(t + 3.days).strftime("%d.%m")  # → "08.03"
(t + 1.weeks).strftime("%d.%m")  # → "12.03"
t + 60 == t + 1.minutes  # → true
1.days  # → 86400
d = time.parse("2026-03-10") - time.parse("2026-03-05")
d  # → 432000
d / 86400  # → 5
time.parse("2026-01-01") < t  # → true
```

A duration is plain int seconds (`seconds minutes hours days weeks`, after `include time`). Use
whole numbers: `1.5.hours` is wrong — write `90.minutes`. `time - time` is seconds; `time + int`
adds seconds. Times compare with `<`, `==`, and sort.

## Comparing with “now” in the bot's zone

Build “now” as a wall-clock time in the same form as parsed visitor input:

```mzs
include time
include bot
local_now = time.parse(time.now.in_time_zone("Europe/Moscow").strftime("%Y-%m-%d %H:%M"))
wanted = try time.parse("25.12.2026 14:30") else nil
if wanted == nil {
  bot.say("Не понял дату. Напишите, например: 25.12.2026 14:30")
  bot.exit("no")
} else if wanted < local_now {
  bot.say("Это время уже прошло, выберите другое.")
  bot.exit("no")
} else {
  bot.set("visit_at", wanted.strftime("%d.%m.%Y %H:%M"))
  bot.set("days_left", ((wanted - local_now) / 86400).str)
  bot.exit("yes")
}
```

## Business hours (exits `open` / `closed`)

```mzs
include time
include bot
schedule = {1 -> [9, 0, 20, 0], 2 -> [9, 0, 20, 0], 3 -> [9, 0, 20, 0], 4 -> [9, 0, 20, 0], 5 -> [9, 0, 19, 0], 6 -> [10, 0, 16, 0]}
now = time.now.in_time_zone("Europe/Moscow")
hours = schedule[now.wday]
minute = now.hour * 60 + now.min
is_open = hours != nil && minute >= hours[0] * 60 + hours[1] && minute < hours[2] * 60 + hours[3]
if is_open {
  bot.exit("open")
} else {
  bot.say("Сейчас мы закрыты. Пн–Чт 9:00–20:00, Пт 9:00–19:00, Сб 10:00–16:00.")
  bot.exit("closed")
}
```

Holidays: keep a list of `"%m-%d"` strings and check `now.strftime("%m-%d") in holidays`.

## Relative words

```mzs
include time
fn day_from_word(text, today) {
  s = text.lower
  match {
    s.has("послезавтра") -> today + 2.days
    s.has("завтра") -> today + 1.days
    s.has("сегодня") -> today
    else -> nil
  }
}
base = time.parse("2026-03-05")
day_from_word("Можно завтра?", base).strftime("%d.%m")  # → "06.03"
day_from_word("послезавтра утром", base).strftime("%d.%m")  # → "07.03"
day_from_word("когда удобно", base)  # → nil
```

Check “послезавтра” before “завтра” — it contains it.
