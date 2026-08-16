# The Value API

Building `mzs.Value`s from Go, reading them back, and the rule that no accessor ever
panics.

## Kinds

`v.Kind()` returns a `mzs.Kind`; `k.String()` is what the script's `type()` reports.

| Constant | `String()` |
|---|---|
| `KNil` | `nil` |
| `KBool` | `bool` |
| `KInt` | `int` |
| `KFloat` | `float` |
| `KString` | `string` |
| `KRegex` | `regex` |
| `KArray` | `array` |
| `KDict` | `dict` |
| `KFunc` | `function` |
| `KTime` | `time` |
| `KRange` | `range` |
| `KTask` | `task` |
| `KSeq` | `seq` |
| `KAny` | `any` |

`KAny` is not a value kind — it is the key of the method table shared by every receiver.

`KRange` and `KSeq` are the two lazy kinds, and they differ where it matters to a host:
`is("array")` is true for a range and **false** for a seq. A range materialises on demand
under `MaxCollection`, so `v.Elems()` answers for it; a seq holds nothing until a script
pulls it, so `Elems()` is empty and `Len()` is 0. Take the array a script hands you
(`s.array` on the script side) rather than a sequence.

## Constructors

```go
mzs.Nil()
mzs.Bool(true)
mzs.Int(42)
mzs.Float(1.5)
mzs.Str("s")
mzs.Array(mzs.Int(1), mzs.Int(2))
mzs.Dict(mzs.Str("a"), mzs.Int(1))            // k1, v1, k2, v2 …
mzs.Regex(`\d+`, "i")                          // (Value, error)
mzs.Fn("upper2", 1, hostFunc)                  // arity -1 = variadic
```

```go
re, _ := mzs.Regex(`\d+`, "i")   // re.Inspect() == "/\\d+/i"
_, err := mzs.Regex(`(`, "")     // err: regex: cannot compile /(/: missing closing ): `(?m)(`
```

`mzs.Dict` panics on an odd argument count — a host bug, unreachable from a script.

Array, dict and function values are references. A Go-side handle aliases what the script
mutates:

```go
arr := mzs.Array(mzs.Int(1))
in.Eval(ctx, `$xs.push(2)`, map[string]mzs.Value{"xs": arr})
// arr.Inspect() == "[1,2]"
```

## Accessors are total

Every accessor returns a zero value off-kind. None of them panics, none returns an error.

```go
for _, v := range []mzs.Value{mzs.Nil(), mzs.Str("12abc"), mzs.Bool(true),
	mzs.Array(mzs.Int(1), mzs.Int(2)), mzs.Dict(mzs.Str("a"), mzs.Int(1))} {
	fmt.Printf("%-8s Int=%-3d Float=%-3g Len=%d Index(0)=%-4s Get(\"a\")=%-4s Keys=%v\n",
		v.Inspect(), v.Int(), v.Float(), v.Len(),
		v.Index(0).Inspect(), v.Get(mzs.Str("a")).Inspect(), v.Keys())
}
```

```
nil      Int=0   Float=0   Len=0 Index(0)=nil  Get("a")=nil  Keys=[]
"12abc"  Int=12  Float=12  Len=5 Index(0)="1"  Get("a")=nil  Keys=[]
true     Int=1   Float=1   Len=0 Index(0)=nil  Get("a")=nil  Keys=[]
[1,2]    Int=0   Float=0   Len=2 Index(0)=1    Get("a")=nil  Keys=[]
{"a":1}  Int=0   Float=0   Len=1 Index(0)=nil  Get("a")=1    Keys=[a]
```

Mutators off-kind are no-ops: `mzs.Nil().Append(…)` and `mzs.Nil().Set(k, v)` leave the
value `nil`.

| Accessor | Meaning |
|---|---|
| `Kind()` `IsNil()` `TypeName()` `IsNum()` | the tag |
| `Truthy()` | script truthiness — only `nil` and `false` are falsy |
| `Bool()` | the boolean payload; `false` for every other kind |
| `Int()` `Float()` `Str()` `Time()` | `int` / `float` / `str` semantics, never panic |
| `String()` | `== Str()`; implements `fmt.Stringer` |
| `Inspect()` | `Str()` with strings quoted and nil spelled `nil` |
| `Len()` | runes of a string, elements of array/dict/range, else 0 |
| `Equal(o)` | the script's `==` |
| `Index(i)` `Get(k)` `Keys()` `Elems()` | reads; negative index counts from the end |
| `Set(k, v)` `Append(vs…)` | mutations, in place |
| `Clone()` | `dup`: shallow copy of array/dict, identity otherwise |

`Elems()` returns the array's backing slice, not a copy — do not retain it past the next
mutation.

## From and Interface

`From(x)` converts a Go value. Scalars, `[]T`, `map[string]T`, `[]byte`, `time.Time` and
`json.RawMessage` are direct; anything else goes through `encoding/json`, so any
JSON-marshalable struct is accepted. `MustFrom` panics instead of returning an error.

```
From(<nil>)             = nil
From(bool)              = true
From(int)               = 7
From(uint64)            = 18446744073709552000.0
From(float64)           = 1.5
From(string)            = "s"
From([]int)             = [1,2]
From(map[string]int)    = {"a":1,"b":2}
From(main.user)         = {"name":"Ann","age":30}
From(json.RawMessage)   = {"k":[1,2]}
From(chan int)          = nil    err: mzs: cannot convert chan int to a value
```

Limits worth knowing: a `uint`/`uint64` above `math.MaxInt64` becomes a `Float`, never a
wrapped negative `Int`; Go map keys must be strings, and Go maps are converted with their
keys sorted, since Go map order is randomised. Channels, funcs and unsupported types are an
error.

`v.Interface()` goes the other way — `nil`, `bool`, `int64`, `float64`, `string`, `[]any`
(array and range), `map[string]any`, `time.Time`, and an opaque handle for a regex or a
function. Dict key order is lost in the Go map; use `MarshalJSON` when order matters.

## JSON

`Value` implements `json.Marshaler` and `json.Unmarshaler`, so it drops into any struct:

```go
v := mzs.MustFrom(map[string]any{"n": 1, "xs": []any{"a", true}})
b, _ := v.MarshalJSON()          // {"n":1,"xs":["a",true]}
out, _ := json.Marshal(struct {
	Payload mzs.Value `json:"payload"`
}{v})                            // {"payload":{"n":1,"xs":["a",true]}}

var back mzs.Value
json.Unmarshal([]byte(`{"z":1,"a":2}`), &back)   // back.Inspect() == {"z":1,"a":2}
```

Unmarshalling preserves object key order.

## See also

- [./README.md](./README.md) — `New`, `Compile`, `Run`, `RunResult`
- [./functions.md](./functions.md) — returning these values from a host function
- [../language/values.md](../language/values.md) — the same kinds seen from a script
- [../modules/json.md](../modules/json.md) — the script-side `json` module
