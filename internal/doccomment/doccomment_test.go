package doccomment

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	src := `# an ordinary comment
## Total of a cart.
##
## Raises kind "billing" when currencies are mixed.
export fn total(items, cur = "USD", *rest) { items.len }

fn helper(x) { x } # not a doc comment

## A sum of money.
record Money(amount, cur = "USD")

## The retry budget.
RETRIES = 3

undocumented = 1

export LIMIT = 10

### a banner, not documentation
fn bannered() { 0 }

## Separated by a blank line.

fn detached() { 0 }

fn later() { 0 }
## Exports later.
export later

## Starts a task.
async fn job() { 1 }

## Counts.
counter = 0
## Not a declaration.
counter += 1

fn multi(d = {
  a: 1,
}) { d }
`
	items, err := Parse("t.mzs", src)
	if err != nil {
		t.Fatal(err)
	}
	want := []Item{
		{Name: "total", Kind: KindFn, Sig: `fn total(items, cur = "USD", *rest)`, Doc: "Total of a cart.\n\nRaises kind \"billing\" when currencies are mixed.", Exported: true, Line: 5},
		{Name: "helper", Kind: KindFn, Sig: "fn helper(x)", Line: 7},
		{Name: "Money", Kind: KindRecord, Sig: `record Money(amount, cur = "USD")`, Doc: "A sum of money.", Line: 10},
		{Name: "RETRIES", Kind: KindValue, Sig: "RETRIES", Doc: "The retry budget.", Line: 13},
		{Name: "LIMIT", Kind: KindValue, Sig: "LIMIT", Exported: true, Line: 17},
		{Name: "bannered", Kind: KindFn, Sig: "fn bannered()", Line: 20},
		{Name: "detached", Kind: KindFn, Sig: "fn detached()", Line: 24},
		{Name: "later", Kind: KindFn, Sig: "fn later()", Doc: "Exports later.", Exported: true, Line: 26},
		{Name: "job", Kind: KindFn, Sig: "async fn job()", Doc: "Starts a task.", Line: 31},
		{Name: "counter", Kind: KindValue, Sig: "counter", Doc: "Counts.", Line: 34},
		{Name: "multi", Kind: KindFn, Sig: "fn multi(d = …)", Line: 38},
	}
	if !reflect.DeepEqual(items, want) {
		for i := range max(len(items), len(want)) {
			var got, exp Item
			if i < len(items) {
				got = items[i]
			}
			if i < len(want) {
				exp = want[i]
			}
			if got != exp {
				t.Errorf("item %d:\n got %+v\nwant %+v", i, got, exp)
			}
		}
	}
	if s := want[0].Summary(); s != "Total of a cart." {
		t.Errorf("Summary = %q", s)
	}
}

func TestParseReportsTheSyntaxError(t *testing.T) {
	if _, err := Parse("t.mzs", "fn f( {"); err == nil {
		t.Fatal("want an error for a file that does not parse")
	}
	if _, err := Parse("t.mzs", "x = \"open"); err == nil {
		t.Fatal("want an error for a file that does not lex")
	}
}
