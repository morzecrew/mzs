Recipes: complete «Скрипт» blocks — phone/email validation, order status via API, price and discount, business hours, dates, keyword routing, auth API calls, saving variables, retries.

# Recipes

Each block is a whole script. Exit names (`yes`, `no`, `found`, `paid`, `open`…) must be the
block's own exits — rename them to match the block you are writing.

## Validate and normalise a phone (exits `yes` / `no`)

```mzs
include bot
m = $message.captures(/(?:\+7|8)?[\s\-(]*9\d{2}[\s\-)]*\d{3}[\s\-]*\d{2}[\s\-]*\d{2}/)
digits = (m?.dig(0) ?? $message).replace(/\D/, "")
digits = "7" + digits.drop(1) if digits.len == 11 && digits.starts_with("8")
digits = "7" + digits if digits.len == 10
if digits ~ /\A7\d{10}\z/ {
  pretty = "+7 (${digits[1, 3]}) ${digits[4, 3]}-${digits[7, 2]}-${digits[9, 2]}"
  bot.set("phone", "+" + digits)
  bot.set("phone_pretty", pretty)
  bot.say("Записал номер: ${pretty}")
  bot.exit("yes")
} else {
  bot.say("Не похоже на номер телефона. Пример: +7 999 123-45-67")
  bot.exit("no")
}
```

## Validate an email (exits `yes` / `no`)

```mzs
include bot
m = $message.captures(/[\w.+-]+@[\w-]+(?:\.[\w-]+)*\.\p{L}{2,}/)
if m == nil {
  bot.say("Пожалуйста, пришлите email, например: name@mail.ru")
  bot.exit("no")
} else {
  email = m[0].lower
  bot.set("email", email)
  bot.say("Спасибо! Отправим письмо на ${email}")
  bot.exit("yes")
}
```

## Order status via an HTTP API (exits `paid` / `unpaid` / `not_found`)

```mzs
include bot
include json
id = $message.captures(/(\d{4,10})/)?.dig(1) ?? (bot.get("order_id") ?? "")
if id.blank {
  bot.say("Напишите номер заказа — это цифры из письма.")
  bot.exit("not_found")
  return
}
r = bot.http({url: "https://shop.example.com/api/orders/${id}", headers: {"Accept": "application/json"}})
order = r["ok"] ? (try json.parse(r["body"]) else nil) : nil
if order == nil {
  bot.say(r["status"] == 404 ? "Заказ №${id} не найден." : "Не удалось проверить заказ, попробуйте позже.")
  bot.set("order_error", r["error"].blank ? "HTTP ${r["status"]}" : r["error"])
  bot.exit("not_found")
  return
}
status = order["status"] ?? "unknown"
bot.set("order_id", id)
bot.set("order_status", status)
bot.set("order_total", (order["total"] ?? 0).str)
labels = {paid: "оплачен", pending: "ожидает оплаты", shipped: "отправлен"}
bot.say("Заказ №${id}: **${labels[status] ?? status}**")
bot.exit(status in ["paid", "shipped"] ? "paid" : "unpaid")
```

## Price with a discount, said to the visitor (exits `yes` / `no`)

```mzs
include bot
price = (bot.get("price") ?? "0").int
qty = max((bot.get("qty") ?? "1").int, 1)
promo = ($message.captures(/\b([A-ZА-Я0-9]{4,12})\b/)?.dig(1) ?? "").upper
codes = {"SALE10" -> 10, "VIP20" -> 20}
pct = codes[promo] ?? 0
pct = max(pct, 5) if qty >= 3
subtotal = price * qty
discount = (subtotal * pct / 100.0).round
total = subtotal - discount
fn rub(n) { n.str.replace(/\B(?=(\d{3})+(?!\d))/, " ") + " ₽" }
lines = ["Товар: ${rub(price)} × ${qty} = ${rub(subtotal)}"]
lines.push("Скидка ${pct}%: −${rub(discount)}") if pct > 0
lines.push("**Итого: ${rub(total)}**")
bot.say(lines.join("\n"))
bot.set("total", total)
bot.set("discount_pct", pct)
bot.exit(pct > 0 ? "yes" : "no")
```

## Business hours (exits `open` / `closed`)

```mzs
include bot
include time
now = time.now.in_time_zone("Europe/Moscow")
weekend = now.wday == 0 || now.wday == 6
open_from = weekend ? 10 : 9
open_to = weekend ? 18 : 21
holidays = ["01-01", "01-02", "05-09"]
if !(now.strftime("%m-%d") in holidays) && now.hour >= open_from && now.hour < open_to {
  bot.exit("open")
} else {
  bot.say("Мы сейчас не работаем. Будни 9:00–21:00, выходные 10:00–18:00 (МСК). Оставьте сообщение — ответим утром.")
  bot.exit("closed")
}
```

## Parse a date the visitor typed (exits `yes` / `no`)

```mzs
include bot
include time
text = $message.lower
today = time.parse(time.now.in_time_zone("Europe/Moscow").strftime("%Y-%m-%d"))
day = nil
if text.has("послезавтра") { day = today + 2.days }
else if text.has("завтра") { day = today + 1.days }
else if text.has("сегодня") { day = today }
else {
  m = text.captures(/(\d{1,2})[.\/](\d{1,2})(?:[.\/](\d{2,4}))?/)
  if m != nil {
    y = m[3] ?? today.year.str
    y = "20${y}" if y.len == 2
    day = try time.parse("${y}-${m[2].rjust(2, "0")}-${m[1].rjust(2, "0")}") else nil
  }
}
hm = text.captures(/\b([01]?\d|2[0-3])[:.]([0-5]\d)\b(?![.\/]\d)/)
if day == nil || day < today {
  bot.say("Не понял дату. Напишите, например: 25.12 или «завтра в 15:00».")
  bot.exit("no")
} else {
  bot.set("visit_date", day.strftime("%d.%m.%Y"))
  bot.set("visit_time", hm == nil ? "" : "${hm[1].rjust(2, "0")}:${hm[2]}")
  bot.exit("yes")
}
```

## Pick a branch by keywords (exits `yes` / `no` / `other`)

```mzs
include bot
s = $message.lower.replace(/ё/, "е").trim
intent = match {
  s.blank -> "other"
  s ~ /\A(да|ага|угу|конечно|верно|ок|yes)\b/ -> "yes"
  s ~ /\A(нет|не надо|неа|no)\b/ -> "no"
  else -> "other"
}
topic = match s {
  /цен|стоит|стоим|прайс/ -> "price"
  /достав|курьер|привез/ -> "delivery"
  /оператор|менеджер|человек/ -> "human"
  else -> "unknown"
}
bot.set("topic", topic)
bot.exit(intent)
```

## A JSON API with an auth header (exits `found` / `not_found`)

```mzs
include bot
include json
include url
api_key = "PUT_THE_KEY_HERE"
query = url.query({q: $message.trim.take(100), limit: 3})
r = bot.http({
  url: "https://api.example.com/v1/products?${query}",
  headers: {"Authorization": "Bearer ${api_key}", "Accept": "application/json"},
  timeout: 4,
})
data = r["ok"] ? (try json.parse(r["body"]) else {}) : {}
items = data.dig("items") ?? []
if items.empty {
  bot.say("Ничего не нашлось. Попробуйте другое название.")
  bot.exit("not_found")
} else {
  list = items.take(3).map { "• ${it["name"] ?? "?"} — ${(it["price"] ?? 0).round} ₽" }
  bot.say("Нашёл:\n" + list.join("\n"))
  bot.set("found_count", items.len)
  bot.set("first_item", items[0]["name"] ?? "")
  bot.exit("found")
}
```

## Store several variables for later blocks

The block after a «Как вас зовут?» question:

```mzs
include bot
skip = ["здравствуйте", "привет", "добрый", "меня", "зовут", "это", "я"]
words = $message.matches(/\p{L}+/).filter { !(it.lower in skip) }
name = (words.find { it ~ /\A\p{Lu}/ } ?? words.first ?? "").capitalize
fields = {
  client_name: name,
  client_channel: $client,
  first_message: $message.take(200),
}
fields.each { (k, v) -> bot.set(k, v) }
bot.set("profile_json", fields.json)
bot.say(name.blank ? "Как к вам обращаться?" : "Приятно познакомиться, ${name}!")
```

Later blocks read `$client_name`, `$client_channel`, … Each value is stored as a string
(≤ 4000 bytes; ≤ 50 `bot.set` per run). Save lists/dicts as JSON and `json.parse` them later.

## Count attempts (exits `yes` / `no` / `other`)

```mzs
include bot
tries = (bot.get("code_tries") ?? "0").int + 1
bot.set("code_tries", tries)
if $message.trim ~ /\A\d{4}\z/ {
  bot.set("code_tries", 0)
  bot.exit("yes")
} else if tries >= 3 {
  bot.say("Код не подошёл 3 раза. Соединяю с менеджером.")
  bot.exit("other")
} else {
  bot.say("Код — 4 цифры из SMS. Осталось попыток: ${3 - tries}")
  bot.exit("no")
}
```

## Render a menu from a variable (no exits)

```mzs
include bot
include json
menu = try json.parse(bot.get("menu_json") ?? "") else nil
menu = menu ?? [{name: "Капучино", price: 220}, {name: "Латте", price: 250}]
lines = []
menu.each_with_index { (x, i) -> lines.push("${i + 1}. ${x["name"]} — ${x["price"]} ₽") }
bot.say("**Меню:**\n" + lines.join("\n") + "\n\nНапишите номер позиции.")
```
