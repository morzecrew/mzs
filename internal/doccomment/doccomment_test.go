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

// A `fn` is hoisted, so `export name` may stand above it and carry its comment; a
// destructuring assignment documents every name it binds, except `_`.
func TestExportFirstAndDestructuring(t *testing.T) {
	src := "## Adds.\nexport add\n\nfn add(a, b) { a + b }\n\n## A pair.\nlo, _, *rest = [1, 2, 3]\n\n## Own doc wins.\nexport sub\n## Its own.\nfn sub(a, b) { a - b }\n"
	items, err := Parse("t.mzs", src)
	if err != nil {
		t.Fatal(err)
	}
	want := []Item{
		{Name: "add", Kind: KindFn, Sig: "fn add(a, b)", Doc: "Adds.", Exported: true, Line: 4},
		{Name: "lo", Kind: KindValue, Sig: "lo", Doc: "A pair.", Line: 7},
		{Name: "rest", Kind: KindValue, Sig: "rest", Doc: "A pair.", Line: 7},
		{Name: "sub", Kind: KindFn, Sig: "fn sub(a, b)", Doc: "Its own.", Exported: true, Line: 12},
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("items =\n%+v\nwant\n%+v", items, want)
	}
}

// The text goes out as it came in: two trailing spaces are a Markdown hard break.
func TestDocTextKeepsTrailingSpaces(t *testing.T) {
	items, err := Parse("t.mzs", "## First.  \n## Second.\nfn f() { 1 }\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := "First.  \nSecond."; len(items) != 1 || items[0].Doc != want {
		t.Errorf("items = %+v, want one with doc %q", items, want)
	}
}
