package mzs

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// RFC 0013: the csv module.

func TestCSVParse(t *testing.T) {
	spreadCases(t, [][2]string{
		{`include csv; csv.parse("a,b\n1,2\n")`, `[["a","b"],["1","2"]]`},
		// No final line break, a "\r\n" document, and a blank line between records.
		{`include csv; csv.parse("a,b\r\n1,2")`, `[["a","b"],["1","2"]]`},
		{`include csv; csv.parse("a\n\n\nb\n")`, `[["a"],["b"]]`},
		{`include csv; csv.parse("")`, `[]`},
		// RFC 4180 quoting: the separator, a doubled quote and a line break inside quotes.
		{`include csv; csv.parse("\"a,b\",\"say \"\"hi\"\"\",\"x\ny\"")`, `[["a,b","say \"hi\"","x\ny"]]`},
		{`include csv; csv.parse("\"x\r\ny\"")`, `[["x\ny"]]`},
		// Values are strings, and an empty field is "" — never a guessed number or nil.
		{`include csv; csv.parse("007,1e3,NULL,,")`, `[["007","1e3","NULL","",""]]`},
		{`include csv; csv.parse("\"\"")`, `[[""]]`},
		// A header names the fields, in its order (D11).
		{`include csv; csv.parse("id,name\n1,Ann\n2,Bo", {header: true})`, `[{"id":"1","name":"Ann"},{"id":"2","name":"Bo"}]`},
		{`include csv; csv.parse("id,name\n", {header: true})`, `[]`},
		{`include csv; csv.parse("1,Ann", {header: ["id", "name"]})`, `[{"id":"1","name":"Ann"}]`},
		// A byte-order mark is stripped, so it does not sit inside the first name.
		{`include csv; csv.parse("\uFEFFid\n1", {header: true})`, `[{"id":"1"}]`},
		// Options: another separator, trimming around unquoted fields, comment lines.
		{`include csv; csv.parse("a;b\n1;2", {sep: ";"})`, `[["a","b"],["1","2"]]`},
		{`include csv; csv.parse("a\tb", {sep: "\t"})`, `[["a","b"]]`},
		{`include csv; csv.parse(" a , \" b \" ,c ", {trim: true})`, `[["a"," b ","c"]]`},
		{`include csv; csv.parse(" a , b ")`, `[[" a "," b "]]`},
		{`include csv; csv.parse("# note\na\n#b\n", {comment: "#"})`, `[["a"]]`},
		{`include csv; csv.parse("a,#b", {comment: "#"})`, `[["a","#b"]]`},
		// Without strict, short records are padded with nil and long ones keep their extras.
		{`include csv; csv.parse("a,b\n1\n1,2,3", {strict: false})`, `[["a","b"],["1",null],["1","2","3"]]`},
		{`include csv; csv.parse("a,b\n1\n1,2,3", {strict: false, header: true})`, `[{"a":"1","b":null},{"a":"1","b":"2","2":"3"}]`},
		// A non-ASCII separator and fields.
		{`include csv; csv.parse("имя·город\nАня·Рига", {sep: "·", header: true})`, `[{"имя":"Аня","город":"Рига"}]`},
	})
}

func TestCSVRows(t *testing.T) {
	spreadCases(t, [][2]string{
		// A seq of lines, an array of lines, or one string.
		{`include csv; csv.rows("a,b\n1,2".lines.seq).array`, `[["a","b"],["1","2"]]`},
		{`include csv; csv.rows(["a,b", "1,2"], {header: true}).array`, `[{"a":"1","b":"2"}]`},
		{`include csv; csv.rows("a\nb").map { it[0] }.array`, `["a","b"]`},
		// A quoted field that holds a line break consumes the lines it spans.
		{`include csv; csv.rows(["id,note", "1,\"two", "lines\"", "2,x"], {header: true}).map { it["note"] }.array`, `["two\nlines","x"]`},
		// Lazy: nothing is read until a terminal pulls, and only as far as it pulls.
		{`include csv; n = 0; src = seq { (i) -> n += 1; i < 100 ? "r${i}" : nil }; csv.rows(src).take(2).array; n`, `2`},
		// A seq is a recipe: a second traversal reads again, header and all.
		{`include csv; s = csv.rows(["k", "1", "2"], {header: true}); [s.count, s.count]`, `[2,2]`},
	})
}

func TestCSVWrite(t *testing.T) {
	spreadCases(t, [][2]string{
		{`include csv; csv.str([["a", "b"], ["1", "2"]])`, `"a,b\n1,2\n"`},
		// Dict rows write a header, in the first row's key order.
		{`include csv; csv.str([{id: 1, name: "Ann"}, {name: "Bo", id: 2}])`, `"id,name\n1,Ann\n2,Bo\n"`},
		{`include csv; csv.str([{id: 1}], {header: false})`, `"1\n"`},
		{`include csv; csv.str([[1, 2]], {header: ["x", "y"]})`, `"x,y\n1,2\n"`},
		{`include csv; csv.str([{b: 2, a: 1}], {header: ["a", "b"]})`, `"a,b\n1,2\n"`},
		{`include csv; csv.str([])`, `""`},
		{`include csv; csv.str([["a"]].seq)`, `"a\n"`},
		// Quoting: the separator, a quote, a line break, and spaces at either end.
		{`include csv; csv.line(["a,b", "say \"hi\"", "x\ny", " lead", "trail ", "mid dle", ""])`, `"\"a,b\",\"say \"\"hi\"\"\",\"x\ny\",\" lead\",\"trail \",mid dle,"`},
		{`include csv; csv.line(["a;b", "c,d"], {sep: ";"})`, `"\"a;b\";c,d"`},
		{`include csv; csv.line(["#x"], {comment: "#"})`, `"\"#x\""`},
		// A record of one empty field is "", or it would read back as a blank line.
		{`include csv; csv.str([[""]])`, `"\"\"\n"`},
		// Non-strings go through str; nil is an empty field; a Decimal is its decimal text.
		{`include csv; csv.line([1, 1.5, true, nil])`, `"1,1.5,true,"`},
		{`include csv; include decimal; csv.line([decimal.of("12.50"), decimal.of("-3")])`, `"12.5,-3"`},
		// A key that is not a string is a column named by its str.
		{`include csv; csv.str([{1 -> "a", 2 -> "b"}])`, `"1,2\na,b\n"`},
		// No comment mark is the same as none given.
		{`include csv; csv.parse("#a", {comment: nil})`, `[["#a"]]`},
		// Without strict, a dict row missing a key writes an empty field.
		{`include csv; csv.str([{a: 1, b: 2}, {a: 3}], {strict: false})`, `"a,b\n1,2\n3,\n"`},
	})
}

// What is written reads back as it was.
func TestCSVRoundTrip(t *testing.T) {
	spreadCases(t, [][2]string{
		{`include csv; rows = [["a,b", "say \"hi\"", "x\ny", " pad ", "", "\r"]]; csv.parse(csv.str(rows)) == rows`, `true`},
		{`include csv; rows = [{id: "1", note: "a, \"b\"\nc"}]; csv.parse(csv.str(rows), {header: true}) == rows`, `true`},
		{`include csv; rows = [[" x ", "y"]]; csv.parse(csv.str(rows), {trim: true}) == rows`, `true`},
		{`include csv; rows = [["a\tb", "c"]]; csv.parse(csv.str(rows, {sep: "\t"}), {sep: "\t"}) == rows`, `true`},
	})
}

func TestCSVErrors(t *testing.T) {
	spreadErrors(t, [][3]string{
		// Malformed input is a `csv` error with the line and column.
		{`include csv; csv.parse("a,b\n1,2,3")`, ErrKindCSV, "csv.parse: line 2, column 1: 3 field(s), and the first record has 2; pass {strict: false}"},
		{`include csv; csv.parse("id\n1,2", {header: true})`, ErrKindCSV, "line 2, column 1: 2 field(s), and the header has 1"},
		{`include csv; csv.parse("ab\"c")`, ErrKindCSV, "line 1, column 3: bare '\"' in an unquoted field"},
		{`include csv; csv.parse("x\n\"abc")`, ErrKindCSV, "line 2, column 1: the quoted field that starts here is never closed"},
		{`include csv; csv.parse("\"a\"b")`, ErrKindCSV, "line 1, column 4: \"b\" after the closing quote"},
		{`include csv; csv.parse("\"a\" ,b")`, ErrKindCSV, "\" \" after the closing quote"},
		{`include csv; csv.parse("id,id\n1,2", {header: true})`, ErrKindCSV, "the header names \"id\" twice, in columns 1 and 2"},
		{`include csv; csv.parse("id,\n1,2", {header: true})`, ErrKindCSV, "the header has an empty name in column 2"},
		{`include csv; csv.rows(["a", "b,c"]).array`, ErrKindCSV, "csv.rows: line 2, column 1: 2 field(s)"},
		// Options are checked, and a misspelt one is named.
		{`include csv; csv.parse("x", {sepp: ";"})`, ErrKindArgument, "unknown option \"sepp\"; did you mean 'sep'?"},
		{`include csv; csv.parse("x", {colour: 1})`, ErrKindArgument, "unknown option \"colour\"; the options are sep, header, strict, trim, comment"},
		{`include csv; csv.parse("x", {sep: ";;"})`, ErrKindArgument, "option 'sep' is one character"},
		{`include csv; csv.parse("x", {sep: "\""})`, ErrKindArgument, "option 'sep' cannot be"},
		{`include csv; csv.parse("x", {sep: 1})`, ErrKindType, "option 'sep' is one character, got int"},
		{`include csv; csv.parse("x", {comment: "#", sep: "#"})`, ErrKindArgument, "options 'sep' and 'comment' are both"},
		{`include csv; csv.parse("x", {comment: "##"})`, ErrKindArgument, "option 'comment' is one character"},
		{`include csv; csv.parse("x", {1 -> 2})`, ErrKindArgument, "unknown option 1; the options are"},
		{`include csv; csv.str([], {sep: ""})`, ErrKindArgument, "csv.str: option 'sep' is one character"},
		{`include csv; csv.line([], {trim: "no"})`, ErrKindType, "csv.line: option 'trim' is true or false"},
		{`include csv; csv.rows("a", {strict: nil})`, ErrKindType, "csv.rows: option 'strict' is true or false"},
		{`include csv; csv.parse("x", {header: "yes"})`, ErrKindType, "option 'header' is true, false or an array of names"},
		{`include csv; csv.parse("x", {header: ["a", 1]})`, ErrKindType, "names columns with strings, got int at index 1"},
		{`include csv; csv.parse("x", {header: ["a", "a"]})`, ErrKindArgument, "option 'header' names \"a\" twice"},
		{`include csv; csv.parse("x", {strict: 1})`, ErrKindType, "option 'strict' is true or false"},
		{`include csv; csv.parse("x", 5)`, ErrKindType, "expects an options dict as argument 2, got int"},
		{`include csv; csv.parse(5)`, ErrKindType, "expects a string, got int"},
		{`include csv; csv.rows(5)`, ErrKindType, "expects a seq of lines, an array of lines or a string, got int"},
		{`include csv; csv.rows([1]).array`, ErrKindType, "csv.rows: a line is a string, got int"},
		// Writing refuses what would lose data or has no field form.
		{`include csv; csv.str([{a: 1}, {a: 2, b: 3}])`, ErrKindArgument, "row 2 has \"b\", which is not a column (a); writing it would drop it"},
		{`include csv; csv.str([{a: 1, b: 2}], {header: ["a"]})`, ErrKindArgument, "row 1 has \"b\", which is not a column (a)"},
		{`include csv; csv.str([{a: 1, b: 2}, {a: 3}])`, ErrKindArgument, "row 2 has no \"b\"; pass {strict: false}"},
		{`include csv; csv.str([[1, [2]]])`, ErrKindArgument, "row 1, column 2 is array, which has no CSV form"},
		{`include csv; csv.str([{a: {b: 1}}])`, ErrKindArgument, "row 1, column \"a\" is dict"},
		{`include csv; csv.str([[1], {a: 1}])`, ErrKindType, "row 2 is dict; every row is an array"},
		{`include csv; csv.str([{a: 1}, [1]])`, ErrKindType, "row 2 is array; every row is a dict, as the first row is"},
		{`include csv; csv.str([[1]], {header: true})`, ErrKindArgument, "{header: true} needs dict rows, or the names as an array"},
		{`include csv; csv.str(5)`, ErrKindType, "expects an array or a seq of rows, got int"},
		{`include csv; csv.line({a: 1})`, ErrKindType, "expects an array of fields, got dict; a dict row is written with csv.str"},
		{`include csv; csv.line([[1]])`, ErrKindArgument, "row 1, column 1 is array"},
	})
}

// A malformed record carries where it is in `data`, and the error is catchable.
func TestCSVErrorData(t *testing.T) {
	spreadCases(t, [][2]string{
		{`include csv; try csv.parse("a\n\"b") else (e) { [e["kind"], e["data"]] }`, `["csv",{"line":2,"col":1}]`},
	})
}

// csv.parse charges what it builds: a document over MaxCollection records is the limit
// error, and a field over MaxStringBytes too.
func TestCSVLimits(t *testing.T) {
	o := DefaultOptions()
	o.MaxCollection = 3
	_, err := New(o).Eval(context.Background(), `include csv; csv.parse("1\n2\n3\n4\n")`, nil)
	if !errors.Is(err, ErrBudget) || !strings.Contains(err.Error(), "collection too large") {
		t.Errorf("rows over MaxCollection: %v, want the collection limit", err)
	}
	_, err = New(o).Eval(context.Background(), `include csv; csv.parse("1,2,3,4")`, nil)
	if !errors.Is(err, ErrBudget) || !strings.Contains(err.Error(), "collection too large") {
		t.Errorf("fields over MaxCollection: %v, want the collection limit", err)
	}
	// rows holds one record at a time, so the same document streams through.
	v, err := New(o).Eval(context.Background(), `include csv; csv.rows("1\n2\n3\n4\n5\n").count`, nil)
	if err != nil || v.Inspect() != "5" {
		t.Errorf("rows under MaxCollection = %v, %v; want 5", v.Inspect(), err)
	}
	o = DefaultOptions()
	o.MaxStringBytes = 8
	_, err = New(o).Eval(context.Background(), `include csv; csv.parse("\"`+strings.Repeat("x", 20)+`\"")`, nil)
	if !errors.Is(err, ErrBudget) || !strings.Contains(err.Error(), "string too large") {
		t.Errorf("field over MaxStringBytes: %v, want the string limit", err)
	}
	o = DefaultOptions()
	o.StepBudget = 50
	_, err = New(o).Eval(context.Background(), `include csv; csv.parse("a,b\n" * 100000)`, nil)
	if !errors.Is(err, ErrBudget) || !strings.Contains(err.Error(), "step") {
		t.Errorf("a long document: %v, want the step budget", err)
	}
	_, err = New(o).Eval(context.Background(), `include csv; csv.rows(seq { (i) -> "a,b" }).count`, nil)
	if !errors.Is(err, ErrBudget) || !strings.Contains(err.Error(), "step") {
		t.Errorf("an endless source: %v, want the step budget", err)
	}
}
