# The `time` and `date` modules

The clock capability, the `time` value kind, parsing and `strftime`, and arithmetic with
durations.

```sh
mzs --time -e 'include time; time.parse("2026-03-05 14:30:15").strftime("%A %d %B, %H:%M")'
```

```
Thursday 05 March, 14:30
```

## Both modules are gated on a clock

`--time` sets `Options.EnableTime` and installs `Options.Now`. Without it the modules do
not exist, and the include says which option is missing:

```sh
mzs -e 'include time'
```

```
-e:1:9: name: module 'time' needs a clock: the host did not set EnableTime (mzs --time)
  include time
          ^
```

With the flag but without the include it is the ordinary missing-include message:

```sh
mzs --time -e 'time.now'
```

```
-e:1:1: name: undefined variable 'time' (add `include time` at the top of the file)
```

An embedding host needs `EnableTime` for the modules to appear at all, and `Now` on top of
it for `time.now` and `date.today`; `time.parse`, `time.at` and `date.parse` read no clock.

| Member | Signature | Result |
|---|---|---|
| `time.now` | `-> time` | the host clock, in `Options.Location` |
| `time.parse` | `(s, layout = auto) -> time` | raises when it cannot parse |
| `time.at` | `(unix: int) -> time` | |
| `date.today` | `-> time` | today at midnight |
| `date.parse` | `(s) -> time` | `time.parse` truncated to midnight |

A date is a time: there is one kind, `type(t) == "time"`, printed as RFC3339 and defaulting
to UTC unless the host set `Options.Location`.

```
include time
t = time.parse("2026-03-05")
[t.str, t.int, type(t)].json      # ["2026-03-05T00:00:00Z",1772668800,"time"]
```

## Parsing

Without a layout these shapes are accepted, dotted and slashed ones day-first:

```
include time
["2026-03-05T14:30:00Z", "2026-03-05 14:30", "2026-03-05", "05/03/26", "05.03.2026"]
  .map { time.parse(it).str }.json
# ["2026-03-05T14:30:00Z","2026-03-05T14:30:00Z","2026-03-05T00:00:00Z",
#  "2026-03-05T00:00:00Z","2026-03-05T00:00:00Z"]
```

A second argument is a layout — either strftime spelling or a Go reference layout:

```
include time
time.parse("05 March 2026", "%d %B %Y")     # 2026-03-05T00:00:00Z
time.parse("Mar 5 2026", "Jan 2 2006")      # 2026-03-05T00:00:00Z
try time.parse("yesterday") else (e) -> e["message"]
# time.parse: cannot parse "yesterday" as a time
```

## Formatting

`strftime` renders C-style directives; `%-d` and `%-m` drop the zero padding, and an
unknown directive is copied through verbatim (`%Q` stays `%Q`).

| | | | |
|---|---|---|---|
| `%Y` year | `%y` 2-digit year | `%m` month | `%d` day |
| `%H` hour | `%M` minute | `%S` second | `%j` day of year |
| `%a` Mon | `%A` Monday | `%b` Jan | `%B` January |
| `%p` AM/PM | `%z` +0000 | `%Z` UTC | `%%` a literal `%` |

```
include time
time.parse("2026-03-05 14:30:15").strftime("%Y-%m-%d %H:%M:%S %A %b %-d %j %z %Z")
# 2026-03-05 14:30:15 Thursday Mar 5 064 +0000 UTC
```

## Fields, arithmetic and zones

| Method | Result |
|---|---|
| `year month day hour min sec` | int |
| `wday` `yday` | int; Sunday is 0 |
| `to_date` | the same day at midnight |
| `int` | unix seconds |
| `strftime(layout)` | string |
| `in_time_zone(tz)` | the same instant in another zone |

```
include time
t = time.parse("2026-03-05 14:30:15")
[t.year, t.month, t.day, t.hour, t.min, t.sec, t.wday, t.yday].json
# [2026,3,5,14,30,15,4,64]

(t + 90.minutes).strftime("%H:%M")               # 16:00
(t - 2.hours).strftime("%H:%M")                  # 12:30
(t + 3.days).strftime("%Y-%m-%d")                # 2026-03-08
(t + 1.weeks).strftime("%Y-%m-%d")               # 2026-03-12
(t + 30.seconds).strftime("%H:%M:%S")            # 14:30:45
time.parse("2026-03-06") - time.parse("2026-03-05")   # 86400
t.in_time_zone("Asia/Tokyo").strftime("%H:%M %Z")     # 23:30 JST
```

The five duration methods are `seconds` `minutes` `hours` `days` `weeks`, on an int or a
float. They are number methods the `time` module installs, so without `--time` they do not
exist — `1.days` is `name: undefined method 'days' for int`.

```
include time
[1.seconds, 90.minutes, 2.hours, 3.days, 1.weeks].json   # [1,5400,7200,259200,604800]
```

A duration is plain int seconds — `1.days.int` is `86400` — so `time + int` adds seconds
and `time - time` yields seconds. Times compare and sort with the ordinary operators, and
`time.at(t.int) == t`. Zone names come from the embedded IANA database; an unknown one
raises `in_time_zone: unknown time zone "Mars/Olympus"`.

## A runnable script

```
include time

start = time.parse("2026-03-05 09:00")
(0..<4).each { (i) ->
  s = start + (i * 45).minutes
  say("${s.strftime("%a %d.%m %H:%M")} – ${(s + 45.minutes).strftime("%H:%M")}")
}
say("${start.to_date.strftime("%Y-%m-%d")}: weekday ${start.wday}, day ${start.yday} of the year")
say("in Tokyo that is ${start.in_time_zone("Asia/Tokyo").strftime("%H:%M %Z")}")
```

```sh
mzs --time slots.mzs
```

```
Thu 05.03 09:00 – 09:45
Thu 05.03 09:45 – 10:30
Thu 05.03 10:30 – 11:15
Thu 05.03 11:15 – 12:00
2026-03-05: weekday 4, day 64 of the year
in Tokyo that is 18:00 JST
```

A larger one is [`examples/29_time_scheduling.mzs`](../../examples/29_time_scheduling.mzs),
run with `mzs --time`.

## See also

- [./README.md](./README.md) — module gates and the diagnostics behind them
- [../reference/sandbox.md](../reference/sandbox.md) — why the clock is a capability
- [../cli/README.md](../cli/README.md) — `--time` and the other flags
