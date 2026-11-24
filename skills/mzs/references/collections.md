Collections: arrays, dicts, ranges, sets and lazy seq — every useful method with exact results.

# Arrays, dicts, ranges, seq

Arrays and dicts are mutable and passed by reference. A line `expr  # → value` shows the
exact result. Methods that take a closure: `{ it … }` for one argument, `{ (a, b) -> … }` for more.

## Arrays

```mzs
xs = [3, 1, 2]
xs.len  # → 3
xs[0]  # → 3
xs[-1]  # → 2
xs[10] == nil  # → true
xs.first  # → 3
xs.last  # → 2
xs.first(2)  # → [3,1]
xs.has(2)  # → true
2 in xs  # → true
xs.index(1)  # → 1
xs.sort  # → [1,2,3]
xs.sort { (a, b) -> b <=> a }  # → [3,2,1]
xs.reverse  # → [2,1,3]
xs.sum  # → 6
xs.min  # → 1
xs.max  # → 3
xs.mean  # → 2.0
xs.join(", ")  # → "3, 1, 2"
xs  # → [3,1,2]
```

`sort`, `reverse`, `map`, `filter`… return **new** arrays. These change the array in place:

```mzs
xs = [1, 2]
xs.push(3)  # → [1,2,3]
xs.pop  # → 3
xs.unshift(0)  # → [0,1,2]
xs.shift  # → 0
xs.insert(1, 9)  # → [1,9,2]
xs.delete_at(1)  # → 9
xs.delete(2)  # → [1]
xs[3] = "x"
xs  # → [1,null,null,"x"]
```

`.append`, `<<`, `.length`, `.size`, `.includes` do not exist: `push`, `len`, `has`.

## Transforming

```mzs
nums = [1, 2, 3, 4, 5]
nums.map { it * 10 }  # → [10,20,30,40,50]
nums.filter { it % 2 == 0 }  # → [2,4]
nums.reject { it > 3 }  # → [1,2,3]
nums.find { it > 3 }  # → 4
nums.find { it > 9 }  # → nil
nums.any { it > 4 }  # → true
nums.all { it > 0 }  # → true
nums.none { it > 9 }  # → true
nums.count { it > 2 }  # → 3
nums.reduce(0) { (acc, x) -> acc + x }  # → 15
nums.sum { it * 2 }  # → 30
nums.take(2)  # → [1,2]
nums.drop(3)  # → [4,5]
nums.take_while { it < 3 }  # → [1,2]
nums.slice(1, 2)  # → [2,3]
nums.each_slice(2)  # → [[1,2],[3,4],[5]]
nums.partition { it > 2 }  # → [[3,4,5],[1,2]]
nums.group_by { it % 2 == 0 ? "чёт" : "нечет" }  # → {"нечет":[1,3,5],"чёт":[2,4]}
[1, nil, 2].compact  # → [1,2]
[1, 1, 2, 1].uniq  # → [1,2]
[[1, 2], [3, [4]]].flatten  # → [1,2,3,4]
["да", "нет", "да"].tally  # → {"да":2,"нет":1}
[1, 2].zip(["a", "b"])  # → [[1,"a"],[2,"b"]]
[1, 2] + [3]  # → [1,2,3]
[1, 2, 2, 3] - [2]  # → [1,3]
[1, 2].union([2, 3])  # → [1,2,3]
[1, 2, 3].intersect([2, 3, 4])  # → [2,3]
```

`each` runs a closure for its effect; `each_with_index { (x, i) -> … }` adds the index:

```mzs
include bot
items = ["Чай", "Кофе"]
lines = []
items.each_with_index { (name, i) -> lines.push("${i + 1}. ${name}") }
bot.say(lines.join("\n"))
```

## Arrays of dicts (typical API data)

```mzs
items = [{name: "Чай", price: 150, qty: 2}, {name: "Торт", price: 900, qty: 1}]
items.map { it["name"] }  # → ["Чай","Торт"]
items.sum { it["price"] * it["qty"] }  # → 1200
items.sort_by { it["price"] }.last["name"]  # → "Торт"
items.max_by { it["price"] }["name"]  # → "Торт"
items.filter { it["price"] < 500 }.len  # → 1
items.find { it["name"] == "Торт" }["qty"]  # → 1
items.map { "${it["name"]} × ${it["qty"]}" }.join("\n")  # → "Чай × 2\nТорт × 1"
```

## Dicts

Insertion-ordered; keys are usually strings. Read with `d["key"]` — **never `d.key`**.

```mzs
d = {name: "Анна", city: "Москва"}
d["name"]  # → "Анна"
d["age"] == nil  # → true
d.get("age", 18)  # → 18
d["age"] = 30
d.keys  # → ["name","city","age"]
d.values.len  # → 3
d.has("city")  # → true
"city" in d  # → true
d.len  # → 3
d.delete("city")  # → "Москва"
d  # → {"name":"Анна","age":30}
d.merge({age: 31, vip: true})  # → {"name":"Анна","age":31,"vip":true}
{**d, age: 0}  # → {"name":"Анна","age":0}
d.pick("name")  # → {"name":"Анна"}
d.omit("name")  # → {"age":30}
d.map { (k, v) -> "${k}=${v}" }.join("&")  # → "name=Анна&age=30"
d.filter { (k, v) -> k != "age" }  # → {"name":"Анна"}
d.map_values { it.str }  # → {"name":"Анна","age":"30"}
d.find { (k, v) -> v == 30 }  # → ["age",30]
d.sort_by { (k, v) -> k }  # → [["age",30],["name","Анна"]]
d.invert  # → {"Анна":"name","30":"age"}
```

```mzs
counts = {}
counts["чай"] ??= 0
counts["чай"] += 1
counts  # → {"чай":1}
key = "dynamic"
{(key): 1}  # → {"dynamic":1}
{"content-type": "application/json"}["content-type"]  # → "application/json"
{1 -> "один", 2 -> "два"}[2]  # → "два"
```

Loops over a dict: `for k, v in d { … }` or `d.each { (k, v) -> … }`.
`d.fetch("k")` raises a `key` error when the key is absent (use it when absence is a bug).
`{a: 1}.json` → `{"a":1}` works with or without `include json`.

## Nested data and nil-safety

```mzs
order = {id: 7, client: {name: "Олег", phones: ["+7 900 000-00-00"]}}
order.dig("client", "name")  # → "Олег"
order.dig("client", "phones", 0)  # → "+7 900 000-00-00"
order.dig("client", "email", "x")  # → nil
order.dig("delivery", "address") ?? "самовывоз"  # → "самовывоз"
none = nil
none?.get("x")  # → nil
```

`nil["x"]` is an error (`cannot index nil`); `d.dig(…)`, `x?.get(k)` and `??` are the safe forms.

## Ranges

```mzs
(1..5).array  # → [1,2,3,4,5]
(0..<3).array  # → [0,1,2]
(1..5).sum  # → 15
(1..10).step(3).array  # → [1,4,7,10]
7 in 1..10  # → true
(1..3).map { it * it }  # → [1,4,9]
3.times  # → [0,1,2]
range(3)  # → [0,1,2]
"привет"[0..2]  # → "при"
```

`a..b` includes `b`, `a..<b` excludes it (there is no `...`). `(0..5).map`, not `0..5.map`.

## Sets

A set is a dict whose values are `true`:

```mzs
seen = ["a", "b", "a"].to_set
seen  # → {"a":true,"b":true}
seen.has("a")  # → true
["чай", "кофе"].subset(["чай", "кофе", "сок"])  # → true
```

## Lazy seq

`xs.seq` / `(a..b).seq` builds a lazy pipeline; nothing runs until `.array`, `.first`, `.sum`…

```mzs
(1..1000000).seq.filter { it % 7 == 0 }.take(3).array  # → [7,14,21]
```

A seq is not an array (`.array` it before `sort`/`json`). Collections are capped at 50 000
elements, so a big range must stay a seq.

## Destructuring

```mzs
a, b = [1, 2]
first, *rest = [1, 2, 3]
rest  # → [2,3]
{name, age: years} = {name: "Ира", age: 25}
"${name} ${years}"  # → "Ира 25"
```

Lengths must match exactly (`a, b = [1]` raises); a dict pattern needs every named key.
