JSON and bot.http: parsing and building JSON, GET/POST requests, auth headers, query strings, error handling and exits.

# JSON and HTTP

## json

```mzs
include json
d = json.parse('{"order": {"id": 7, "items": [{"name": "Чай", "price": 150.5}]}, "paid": true, "note": null}')
d["paid"]  # → true
d["note"]  # → nil
d.dig("order", "id")  # → 7
d.dig("order", "items", 0, "name")  # → "Чай"
d.dig("order", "items", 5, "name")  # → nil
d.dig("order", "items").map { it["price"] }.sum  # → 150.5
json.parse("[1, 2]").len  # → 2
json.parse("1.5e3")  # → 1500
{name: "Анна", tags: ["vip"], phone: nil}.json  # → "{\"name\":\"Анна\",\"tags\":[\"vip\"],\"phone\":null}"
[1, "a", true].json  # → "[1,\"a\",true]"
```

- `include json`, then `json.parse(text)` → dicts (key order kept), arrays, ints (when integral),
  floats, strings, `true`/`false`, `nil` for `null`. Malformed text raises kind `json`.
- Encoding is the method `x.json` (compact). `json.pretty(x)` indents. After `include json` the
  name `json` is the module: `json(x)` and `JSON.stringify` do not exist.
- Use `d.dig(…)` for nested reads: it is `nil`-safe at every step, where `d["a"]["b"]` raises
  `cannot index nil` if `a` is missing.
- Values you save with `bot.set` become strings; save structured data as JSON and parse it back:

```mzs
include bot
include json
cart = [{sku: "A1", qty: 2}]
bot.set("cart", cart.json)
back = json.parse(bot.get("cart") ?? "[]")
back[0]["qty"]  # → 2
saved = try json.parse(bot.get("no_such_var") ?? "") else []
saved  # → []
```

## bot.http

```
r = bot.http({method: "GET", url: "https://…", headers: {"Name": "value"}, body: "text", timeout: 5})
r == {ok: true|false, status: 200, body: "…", error: ""}
```

| Field | Notes |
|---|---|
| `method` | `GET` (default), `POST`, `PUT`, `PATCH`, `DELETE` (any case) |
| `url` | must start with `http://` or `https://`; internet only — localhost and private networks are refused |
| `headers` | dict of strings; `User-Agent: MorzeBot/1.0` is set for you |
| `body` | a **string** (≤ 64 KB) — send JSON as `payload.json`; ignored for GET. `Content-Type` defaults to `application/json` when the body starts with `{` or `[`, else `text/plain` |
| `timeout` | seconds, at most 5 (the default) |

The answer: `ok` is true for status 200–299; `status` is 0 when no answer came; `body` is the
response text (as a string, up to 512 KB); `error` is `""` or why the request failed. Response
headers are not available. **`bot.http` never raises for a failed request** — always check
`r["ok"]`. Note `""` is truthy, so test `r["ok"]`, not `r["error"]`. At most 3 calls per run;
more raise. Passing anything but a dict raises.

## GET a JSON API and branch by exit

```mzs
include bot
include json
id = $message.captures(/(\d{4,})/)?.dig(1) ?? (bot.get("order_id") ?? "")
if id.blank {
  bot.say("Пришлите номер заказа цифрами.")
  bot.exit("not_found")
  return
}
r = bot.http({url: "https://api.example.com/orders/${id}"})
if r["status"] == 404 {
  bot.say("Заказ ${id} не найден.")
  bot.exit("not_found")
  return
}
data = r["ok"] ? (try json.parse(r["body"]) else nil) : nil
if data == nil {
  bot.say("Сервис заказов не отвечает, попробуйте позже.")
  bot.exit("not_found")
  return
}
bot.set("order_status", data["status"] ?? "")
bot.exit(data["status"] == "paid" ? "paid" : "unpaid")
```

## POST JSON with an auth header

```mzs
include bot
include json
token = "sk_live_xxx"
payload = {name: bot.get("name") ?? "", phone: bot.get("phone") ?? "", source: "chatbot"}
r = bot.http({
  method: "POST",
  url: "https://crm.example.com/api/leads",
  headers: {"Authorization": "Bearer ${token}", "Accept": "application/json"},
  body: payload.json,
})
if r["ok"] {
  lead = try json.parse(r["body"]) else {}
  bot.set("lead_id", lead["id"] ?? "")
  bot.say("Заявка принята ✅")
  bot.exit("yes")
} else {
  bot.set("lead_error", r["error"].blank ? "HTTP ${r["status"]}" : r["error"])
  bot.say("Не удалось отправить заявку. Менеджер свяжется с вами.")
  bot.exit("no")
}
```

Other auth shapes: `{"X-API-Key": key}`; Basic —
`include crypto` then `{"Authorization": "Basic ${crypto.base64("user:pass")}"}`.
The token is in the script's code: the visitor never sees the code, but the bot owner does.

## Query strings and URL parts

```mzs
include url
q = url.query({city: "Санкт-Петербург", page: 2})
q  # → "city=%D0%A1%D0%B0%D0%BD%D0%BA%D1%82-%D0%9F%D0%B5%D1%82%D0%B5%D1%80%D0%B1%D1%83%D1%80%D0%B3&page=2"
"https://api.example.com/search?${q}".starts_with("https://")  # → true
url.encode("a b/в")  # → "a%20b%2F%D0%B2"
url.decode("%D0%B4%D0%B0")  # → "да"
u = url.parse("https://shop.ru/item?id=42#top")
u["host"]  # → "shop.ru"
u["query"]["id"]  # → "42"
url.parse_query("a=1&b=x+y")  # → {"a":"1","b":"x y"}
```

Always encode visitor text before putting it in a URL: `"…/search?q=${url.encode($message.trim)}"`
or build the whole query with `url.query({…})`. An array value repeats the key
(`{tag: ["a", "b"]}` → `tag=a&tag=b`).

## Form-encoded POST

```mzs
include bot
include url
r = bot.http({
  method: "POST",
  url: "https://forms.example.com/submit",
  headers: {"Content-Type": "application/x-www-form-urlencoded"},
  body: url.query({name: "Анна", msg: $message}),
})
bot.exit(r["ok"] ? "yes" : "no")
```

## Signing a webhook request

```mzs
include crypto
include json
body = {event: "lead", id: 7}.json
sig = crypto.hmac("secret", body)
sig.len  # → 64
crypto.sha256("abc").take(8)  # → "ba7816bf"
crypto.base64("Hi")  # → "SGk="
crypto.unbase64("SGk=")  # → "Hi"
crypto.hex("Hi")  # → "4869"
crypto.equal(sig, crypto.hmac("secret", body))  # → true
```

Then `headers: {"X-Signature": sig}`. `crypto` has `hex unhex base64 unbase64 sha256 sha512
sha384 sha1 md5 hmac crc32 equal`; `crypto.random` is not available (no randomness).

## Rules of thumb

- Plan for failure: the API may be down, slow (5 s cap) or answer HTML. Every path should
  `bot.say` something and pick an exit.
- Keep within 3 requests: fetch a list once rather than item by item.
- Save what later blocks need with `bot.set` (strings, ≤ 4000 bytes each) — not the whole body.
- Big bodies: take only the fields you need right after `json.parse`.
