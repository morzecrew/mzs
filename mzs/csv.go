package mzs

import (
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Reading and writing CSV — the `csv` module (§12.19, RFC 0013).
//
// CSV is the commonest shape of the data a script is handed, and `split(",")` is not a
// parser of it: it breaks on the first quoted comma, on a `""` escape and on a line break
// inside quotes. The module reads and writes RFC 4180:
//
//	include csv
//	csv.parse("id,name\n1,Ann\n")                  # [["id","name"],["1","Ann"]]
//	csv.parse(text, {header: true})                # [{"id":"1","name":"Ann"}]
//	csv.rows(io.lines, {header: true})             # the same records, one at a time
//	csv.str(users)                                 # the document, header first
//	csv.line(["a", "b,c"])                         # a,"b,c"
//
// **Every value read is a string.** A field has no type, and guessing one — `"007"`,
// `"1e3"`, `"NULL"` — is the coercion §9.1 refuses; `int` and `parse_int` are the
// caller's. An empty field is `""`, not nil.
//
// The reader is this file's own rather than encoding/csv's, for three things that one
// does not do: trimming only around *unquoted* fields (`trim`), accepting spaces after a
// closing quote when trimming, and reading from a source of lines pulled one at a time,
// so `csv.rows(io.lines)` never holds more than the record it is on.

const (
	csvKSep     = "sep"
	csvKHeader  = "header"
	csvKStrict  = "strict"
	csvKTrim    = "trim"
	csvKComment = "comment"
)

// csvKeys is every option the module reads, in the order a diagnostic lists them.
var csvKeys = []string{csvKSep, csvKHeader, csvKStrict, csvKTrim, csvKComment}

func init() {
	// Registration order is `csv.keys` order: a module is a Dict and a Dict is
	// insertion-ordered (§8.13).
	RegisterModuleFunc("csv", "parse", 1, 2, csvvParse)
	RegisterModuleFunc("csv", "rows", 1, 2, csvvRows)
	RegisterModuleFunc("csv", "str", 1, 2, csvvStr)
	RegisterModuleFunc("csv", "line", 1, 2, csvvLine)
}

// ---------------------------------------------------------------------------
// Options
// ---------------------------------------------------------------------------

// csvOpts is the options dict, read once per call. header is the reading: 0 unset, 1
// true, -1 false; names is the array form, which names the columns itself.
type csvOpts struct {
	sep     rune
	header  int
	names   []string
	strict  bool
	trim    bool
	comment rune
}

// csvOptions reads the options argument. A key the module does not know is an error with
// the near miss in it: `{seperator: ";"}` answered with silence would read a
// semicolon-separated file as one column (§17).
func csvOptions(c *Ctx, args []Value, i int) (csvOpts, error) {
	o := csvOpts{sep: ',', strict: true}
	v := argAt(args, i)
	switch v.Kind() {
	case KNil:
		return o, nil
	case KDict:
	default:
		return o, c.TypeErrorf("%s expects an options dict as argument %d, got %s", c.Name(), i+1, v.TypeName())
	}
	var failed error
	v.odict().Each(func(k, val Value) bool {
		key := ""
		if k.Kind() == KString {
			key = k.Str()
		}
		switch key {
		case csvKSep:
			r, err := csvRune(c, key, val, false)
			if err != nil {
				failed = err
				return false
			}
			o.sep = r
		case csvKComment:
			r, err := csvRune(c, key, val, true)
			if err != nil {
				failed = err
				return false
			}
			o.comment = r
		case csvKHeader:
			switch val.Kind() {
			case KBool:
				o.header = -1
				if val.Truthy() {
					o.header = 1
				}
			case KArray:
				names, err := csvHeaderNames(c, val)
				if err != nil {
					failed = err
					return false
				}
				o.header, o.names = 1, names
			default:
				failed = c.TypeErrorf("%s: option 'header' is true, false or an array of names, got %s", c.Name(), val.TypeName())
				return false
			}
		case csvKStrict, csvKTrim:
			if val.Kind() != KBool {
				failed = c.TypeErrorf("%s: option '%s' is true or false, got %s", c.Name(), key, val.TypeName())
				return false
			}
			if key == csvKStrict {
				o.strict = val.Truthy()
			} else {
				o.trim = val.Truthy()
			}
		default:
			msg := c.Name() + ": unknown option " + k.Inspect() + "; the options are " + strings.Join(csvKeys, ", ")
			if s := suggest(key, csvKeys); s != "" {
				msg = c.Name() + ": unknown option " + k.Inspect() + "; did you mean '" + s + "'?"
			}
			failed = c.ArgErrorf("%s", msg)
			return false
		}
		return true
	})
	if failed == nil && o.comment != 0 && o.comment == o.sep {
		failed = c.ArgErrorf("%s: options 'sep' and 'comment' are both %s", c.Name(), quoteString(string(o.sep)))
	}
	return o, failed
}

// csvRune reads an option that is one character. The quote and the line breaks are the
// syntax every CSV shares, so none of them can be the separator or the comment mark.
func csvRune(c *Ctx, key string, v Value, nilOK bool) (rune, error) {
	if nilOK && v.IsNil() {
		return 0, nil
	}
	if v.Kind() != KString {
		return 0, c.TypeErrorf("%s: option '%s' is one character, got %s", c.Name(), key, v.TypeName())
	}
	s := v.Str()
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 || n != len(s) || r == utf8.RuneError {
		return 0, c.ArgErrorf("%s: option '%s' is one character, got %s", c.Name(), key, quoteString(s))
	}
	if r == '"' || r == '\r' || r == '\n' {
		return 0, c.ArgErrorf("%s: option '%s' cannot be %s, which every CSV already uses", c.Name(), key, quoteString(s))
	}
	return r, nil
}

// csvHeaderNames checks a header given as an array: strings, none empty, none twice.
func csvHeaderNames(c *Ctx, v Value) ([]string, error) {
	xs := *v.arr()
	names := make([]string, len(xs))
	for i, x := range xs {
		if x.Kind() != KString {
			return nil, c.TypeErrorf("%s: option 'header' names columns with strings, got %s at index %d", c.Name(), x.TypeName(), i)
		}
		names[i] = x.Str()
	}
	if msg := csvNamesProblem(names); msg != "" {
		return nil, c.ArgErrorf("%s: option 'header' %s", c.Name(), msg)
	}
	return names, nil
}

// csvNamesProblem says what makes a list of column names unusable as dict keys, or "".
// Two columns cannot share one key, and keeping one of them would drop data silently.
func csvNamesProblem(names []string) string {
	seen := make(map[string]int, len(names))
	for i, n := range names {
		if n == "" {
			return "has an empty name in column " + strconv.Itoa(i+1)
		}
		if j, ok := seen[n]; ok {
			return "names " + quoteString(n) + " twice, in columns " + strconv.Itoa(j+1) + " and " + strconv.Itoa(i+1)
		}
		seen[n] = i
	}
	return ""
}

// ---------------------------------------------------------------------------
// The reader
// ---------------------------------------------------------------------------

// csvSource hands the reader its text a piece at a time: the whole string for `parse`,
// one line per element for `rows`. Every piece the reader asks for is followed by a line
// break, which is what a line from `io.lines` has lost.
type csvSource func(c *Ctx) (string, bool, error)

// csvReader reads records from a source. line counts the input lines the way an editor
// numbers them, so an error points where a person looks.
type csvReader struct {
	// name is the member that made the reader. A seq is pulled by whatever terminal
	// row asks, so the call being made then is that row's, not `csv.rows`.
	name    string
	src     csvSource
	o       csvOpts
	buf     string
	pos     int
	eof     bool
	line    int
	col     int
	started bool
	// width is the field count every record must have under `strict`: the first
	// record's, or the header's.
	width int
}

func newCSVReader(name string, src csvSource, o csvOpts) *csvReader {
	return &csvReader{name: name, src: src, o: o, line: 1, width: -1}
}

// next is the next rune of the input, or ok == false at its end. A "\r\n" is one line
// break, as RFC 4180 writes it; a lone "\r" is a character like any other.
func (r *csvReader) next(c *Ctx) (rune, bool, error) {
	for r.pos >= len(r.buf) {
		if r.eof {
			return 0, false, nil
		}
		piece, ok, err := r.src(c)
		if err != nil {
			return 0, false, err
		}
		if !ok {
			r.eof = true
			return 0, false, nil
		}
		if err := c.Step(int64(len(piece))/64 + 1); err != nil {
			return 0, false, err
		}
		piece = strings.TrimSuffix(piece, "\r")
		if !r.started {
			// A byte-order mark is what Excel writes in front of a UTF-8 file. Kept, it
			// would sit invisibly inside the first header name.
			piece = strings.TrimPrefix(piece, "\uFEFF")
			r.started = true
		}
		r.buf, r.pos = strings.ReplaceAll(piece, "\r\n", "\n")+"\n", 0
	}
	ch, n := utf8.DecodeRuneInString(r.buf[r.pos:])
	r.pos += n
	if ch == '\n' {
		r.line++
		r.col = 0
	} else {
		r.col++
	}
	return ch, true, nil
}

// peek is the rune next would return, without taking it. It never reads the source: the
// reader only peeks after a quote, and every piece ends in a line break, so there is
// always one more rune in the buffer.
func (r *csvReader) peek() (rune, bool) {
	ch, n := utf8.DecodeRuneInString(r.buf[r.pos:])
	return ch, n > 0
}

// csvErr is a malformed input: kind `csv`, the line and column in the message and in
// `data` (§13.5).
func (r *csvReader) csvErr(c *Ctx, line, col int, format string, a ...any) error {
	e := newError(ErrKindCSV, "%s: line %d, column %d: "+format, append([]any{r.name, line, col}, a...)...)
	d := NewOrderedDictCap(2)
	_ = d.Set(Str("line"), Int(int64(line)))
	_ = d.Set(Str("col"), Int(int64(col)))
	e.Data = dictOf(d)
	return c.position(e)
}

func csvSpace(ch rune) bool { return ch == ' ' || ch == '\t' }

// space is what `trim` takes away: a space or a tab, unless it is the separator — a TSV
// read with `trim` would otherwise lose its empty fields.
func (r *csvReader) space(ch rune) bool { return ch != r.o.sep && csvSpace(ch) }

// record reads one record: its fields and the line it started on, or ok == false when
// the input is over. A blank line is no record (RFC 4180 has none), and a line starting
// with the comment mark is skipped whole.
func (r *csvReader) record(c *Ctx) ([]string, int, bool, error) {
	var (
		fields []string
		field  strings.Builder
		start  = r.line
	)
	atStart := true // at the start of the record
	for {
		// The start of a field.
		ch, ok, err := r.next(c)
		if err != nil {
			return nil, 0, false, err
		}
		if atStart {
			start = r.line
			if !ok {
				return nil, 0, false, nil
			}
			if ch == '\n' {
				continue
			}
			if r.o.comment != 0 && ch == r.o.comment {
				if err := r.skipLine(c); err != nil {
					return nil, 0, false, err
				}
				continue
			}
			atStart = false
		}
		if r.o.trim {
			for ok && r.space(ch) {
				if ch, ok, err = r.next(c); err != nil {
					return nil, 0, false, err
				}
			}
		}
		field.Reset()
		if ok && ch == '"' {
			ch, ok, err = r.quoted(c, &field)
		} else {
			ch, ok, err = r.unquoted(c, &field, ch, ok)
		}
		if err != nil {
			return nil, 0, false, err
		}
		if err := c.CheckString(field.Len()); err != nil {
			return nil, 0, false, err
		}
		fields = append(fields, field.String())
		if !ok || ch == '\n' {
			return fields, start, true, nil
		}
		// ch is the separator: another field follows.
	}
}

// unquoted reads the rest of a field that does not start with a quote, and returns the
// rune that ended it — the separator or a line break — or ok == false at the end.
func (r *csvReader) unquoted(c *Ctx, b *strings.Builder, ch rune, ok bool) (rune, bool, error) {
	for ok && ch != r.o.sep && ch != '\n' {
		if ch == '"' {
			return 0, false, r.csvErr(c, r.line, r.col, "bare '\"' in an unquoted field; a field with a quote in it is quoted, and the quote doubled: \"a \"\"b\"\"\"")
		}
		b.WriteRune(ch)
		var err error
		if ch, ok, err = r.next(c); err != nil {
			return 0, false, err
		}
	}
	if r.o.trim {
		s := strings.TrimRightFunc(b.String(), r.space)
		b.Reset()
		b.WriteString(s)
	}
	return ch, ok, nil
}

// quoted reads a field that starts with a quote, up to and past its closing one. A
// doubled quote is a quote; a line break is part of the field. What may follow the close
// is the separator, a line break or the end — and spaces first, under `trim`.
func (r *csvReader) quoted(c *Ctx, b *strings.Builder) (rune, bool, error) {
	line, col := r.line, r.col
	for {
		ch, ok, err := r.next(c)
		if err != nil {
			return 0, false, err
		}
		if !ok {
			return 0, false, r.csvErr(c, line, col, "the quoted field that starts here is never closed")
		}
		if ch != '"' {
			b.WriteRune(ch)
			if ch == '\n' {
				// A quoted field may span lines, and in `rows` every line is a piece of
				// its own: the field is bounded as it grows, or an unclosed quote would
				// pull the rest of the file into one string.
				if err := c.CheckString(b.Len()); err != nil {
					return 0, false, err
				}
			}
			continue
		}
		if nx, ok := r.peek(); ok && nx == '"' {
			_, _, _ = r.next(c)
			b.WriteRune('"')
			continue
		}
		// The closing quote.
		ch, ok, err = r.next(c)
		if err != nil {
			return 0, false, err
		}
		if r.o.trim {
			for ok && r.space(ch) {
				if ch, ok, err = r.next(c); err != nil {
					return 0, false, err
				}
			}
		}
		if ok && ch != r.o.sep && ch != '\n' {
			return 0, false, r.csvErr(c, r.line, r.col, "%s after the closing quote; a quote inside a quoted field is doubled", quoteString(string(ch)))
		}
		return ch, ok, nil
	}
}

// skipLine consumes a comment line.
func (r *csvReader) skipLine(c *Ctx) error {
	for {
		ch, ok, err := r.next(c)
		if err != nil || !ok || ch == '\n' {
			return err
		}
	}
}

// row reads the next record and shapes it: the header first when there is one, then each
// record as an array or as a dict of the header's names, checked for width under
// `strict` and padded with nil without it (§12.19).
func (r *csvReader) row(c *Ctx) (Value, bool, error) {
	if r.width < 0 {
		r.width = 0
		if r.o.header == 1 {
			if r.o.names == nil {
				names, line, ok, err := r.record(c)
				if err != nil || !ok {
					return Nil(), false, err
				}
				if msg := csvNamesProblem(names); msg != "" {
					return Nil(), false, r.csvErr(c, line, 1, "the header %s", msg)
				}
				r.o.names = names
			}
			r.width = len(r.o.names)
		}
	}
	fields, line, ok, err := r.record(c)
	if err != nil || !ok {
		return Nil(), false, err
	}
	if r.width == 0 && r.o.names == nil {
		r.width = len(fields)
	}
	if r.o.strict && len(fields) != r.width {
		what := "the first record"
		if r.o.names != nil {
			what = "the header"
		}
		return Nil(), false, r.csvErr(c, line, 1, "%d field(s), and %s has %d; pass {strict: false} to read records of any width", len(fields), what, r.width)
	}
	if err := c.allocString(csvBytes(fields)); err != nil {
		return Nil(), false, err
	}
	if r.o.names == nil {
		n := len(fields)
		if n < r.width {
			n = r.width
		}
		if err := c.allocCollection(n, n); err != nil {
			return Nil(), false, err
		}
		xs := make([]Value, n)
		for i := range xs {
			if i < len(fields) {
				xs[i] = Str(fields[i])
			}
		}
		return arrayOf(xs), true, nil
	}
	n := len(r.o.names)
	if len(fields) > n {
		n = len(fields)
	}
	if err := c.allocCollection(n, n); err != nil {
		return Nil(), false, err
	}
	d := NewOrderedDictCap(n)
	for i, name := range r.o.names {
		v := Nil()
		if i < len(fields) {
			v = Str(fields[i])
		}
		_ = d.Set(Str(name), v)
	}
	for i := len(r.o.names); i < len(fields); i++ {
		// Without `strict`, a field past the header keeps its place: its key is its
		// column index, so nothing read is dropped.
		_ = d.Set(Int(int64(i)), Str(fields[i]))
	}
	return dictOf(d), true, nil
}

func csvBytes(fields []string) int {
	n := 0
	for _, f := range fields {
		n += len(f)
	}
	return n
}

// csvvParse is csv.parse(s, opts): every record of s.
func csvvParse(c *Ctx, args []Value) (Value, error) {
	s, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	o, err := csvOptions(c, args, 1)
	if err != nil {
		return Nil(), err
	}
	given := false
	r := newCSVReader(c.Name(), func(*Ctx) (string, bool, error) {
		if given {
			return "", false, nil
		}
		given = true
		return s, true, nil
	}, o)
	out := []Value{}
	m := c.rootMark()
	defer c.release(m)
	c.holdGrowing(&out)
	for {
		v, ok, err := r.row(c)
		if err != nil {
			return Nil(), err
		}
		if !ok {
			return arrayOf(out), nil
		}
		out = append(out, v)
		if err := c.allocCollection(len(out), 1); err != nil {
			return Nil(), err
		}
	}
}

// csvvRows is csv.rows(src, opts): the records of src as a seq, read one at a time
// (§12.14). src is a sequence of lines — a seq, as io.lines gives, or an array — or one
// string. Each traversal reads the source again from its start, as every seq does.
func csvvRows(c *Ctx, args []Value) (Value, error) {
	src := args[0]
	var lines func() seqIter
	switch src.Kind() {
	case KString:
		s := src.Str()
		lines = func() seqIter {
			given := false
			return func(*Ctx) (Value, bool, error) {
				if given {
					return Nil(), false, nil
				}
				given = true
				return Str(s), true, nil
			}
		}
	case KArray:
		lines = seqOfArray(src).open
	case KSeq:
		lines = src.seq().open
	default:
		return Nil(), c.TypeErrorf("%s expects a seq of lines, an array of lines or a string, got %s", c.Name(), src.TypeName())
	}
	o, err := csvOptions(c, args, 1)
	if err != nil {
		return Nil(), err
	}
	name := c.Name()
	s := seqSource(func() func(*Ctx) (Value, bool, error) {
		next := lines()
		r := newCSVReader(name, func(c *Ctx) (string, bool, error) {
			v, ok, err := next(c)
			if err != nil || !ok {
				return "", ok, err
			}
			if v.Kind() != KString {
				return "", false, c.TypeErrorf("%s: a line is a string, got %s", name, v.TypeName())
			}
			return v.Str(), true, nil
		}, o)
		return r.row
	}).with(src)
	return seqOf(s), nil
}

// ---------------------------------------------------------------------------
// The writer
// ---------------------------------------------------------------------------

// csvWriter writes records. It quotes a field when the reader could not read it back
// otherwise: when it holds the separator, a quote or a line break, or begins or ends
// with a space, which `trim` would take away.
type csvWriter struct {
	o csvOpts
	b strings.Builder
}

func (w *csvWriter) field(s string) {
	quote := s != "" && (strings.ContainsRune(s, w.o.sep) || strings.ContainsAny(s, "\"\r\n") ||
		csvSpace(rune(s[0])) || csvSpace(rune(s[len(s)-1])) ||
		(w.o.comment != 0 && strings.HasPrefix(s, string(w.o.comment))))
	if !quote {
		w.b.WriteString(s)
		return
	}
	w.b.WriteByte('"')
	w.b.WriteString(strings.ReplaceAll(s, `"`, `""`))
	w.b.WriteByte('"')
}

// record writes one record, without the line break. A record of one empty field is
// written `""`: written as nothing, it would be a blank line, which the reader skips.
func (w *csvWriter) record(fields []string) {
	if len(fields) == 1 && fields[0] == "" {
		w.b.WriteString(`""`)
		return
	}
	for i, f := range fields {
		if i > 0 {
			w.b.WriteRune(w.o.sep)
		}
		w.field(f)
	}
}

// csvText is the text of one field value. A string is itself and nil is an empty field;
// a Decimal is written as decimal.str writes it, because `str` of one is the JSON of its
// dict, which no spreadsheet reads as money. Any other dict or array has no field form.
func csvText(c *Ctx, v Value, row int, col string) (string, error) {
	switch v.Kind() {
	case KString:
		return v.Str(), nil
	case KNil:
		return "", nil
	case KDict:
		if v.recordType() == decShape {
			n, err := decFromDict(c, v)
			if err != nil {
				return "", err
			}
			return decString(n), nil
		}
	case KArray, KSeq:
	default:
		return v.String(), nil
	}
	return "", c.ArgErrorf("%s: row %d, column %s is %s, which has no CSV form; write it as text first (json, join)", c.Name(), row, col, v.TypeName())
}

// csvRowsOf is the records `str` writes: an array, or a seq pulled to its end.
func csvRowsOf(c *Ctx, v Value) ([]Value, error) {
	switch v.Kind() {
	case KArray:
		return *v.arr(), nil
	case KSeq:
		out := []Value{}
		err := seqRun(c, v.seq(), func(x Value) (bool, error) {
			out = append(out, x)
			return true, c.CheckCollection(len(out))
		})
		return out, err
	}
	return nil, c.TypeErrorf("%s expects an array or a seq of rows, got %s", c.Name(), v.TypeName())
}

// csvvStr is csv.str(rows, opts): the document, every record on a line ending in "\n".
// Rows are all arrays or all dicts; dict rows are written in the key order of the first
// one, or of the header when it names the columns, and a header line comes first.
func csvvStr(c *Ctx, args []Value) (Value, error) {
	o, err := csvOptions(c, args, 1)
	if err != nil {
		return Nil(), err
	}
	m := c.rootMark()
	defer c.release(m)
	rows, err := csvRowsOf(c, args[0])
	if err != nil {
		return Nil(), err
	}
	c.holdVals(rows)
	w := &csvWriter{o: o}
	dicts := len(rows) > 0 && rows[0].Kind() == KDict
	names := o.names
	if dicts && names == nil {
		names = csvStrKeys(rows[0])
		// The header a reader would read back: two keys written alike (`1` and "1"), or
		// an empty one, would make columns it cannot tell apart.
		if msg := csvNamesProblem(names); msg != "" {
			return Nil(), c.ArgErrorf("%s: the first row's keys make a header that %s", c.Name(), msg)
		}
	}
	switch {
	case o.header == 1 && names == nil:
		return Nil(), c.ArgErrorf("%s: {header: true} needs dict rows, or the names as an array: {header: [\"id\", \"name\"]}", c.Name())
	case o.header == 1 || (o.header == 0 && dicts):
		w.record(names)
		w.b.WriteByte('\n')
	}
	for i, row := range rows {
		fields, err := csvFields(c, o, row, i+1, dicts, names)
		if err != nil {
			return Nil(), err
		}
		w.record(fields)
		w.b.WriteByte('\n')
		if err := c.Step(int64(len(fields)) + 1); err != nil {
			return Nil(), err
		}
		if err := c.CheckString(w.b.Len()); err != nil {
			return Nil(), err
		}
	}
	if err := c.allocString(w.b.Len()); err != nil {
		return Nil(), err
	}
	return Str(w.b.String()), nil
}

// csvStrKeys is the column order a dict row gives: its keys, as text.
func csvStrKeys(row Value) []string {
	keys := row.Keys()
	names := make([]string, len(keys))
	for i, k := range keys {
		names[i] = csvKeyText(k)
	}
	return names
}

// csvKeyText is a dict key as a column name: a string is itself, and any other key is
// its str, as a field value is — `{1: "a"}` has a column named "1".
func csvKeyText(k Value) string {
	if k.Kind() == KString {
		return k.Str()
	}
	return k.String()
}

// csvFields is one row as the text of its fields. A dict row with a key the column order
// does not have is always an error, because writing it would drop data; a row missing a
// key is an error under `strict` and an empty field without it.
func csvFields(c *Ctx, o csvOpts, row Value, n int, dicts bool, names []string) ([]string, error) {
	switch {
	case dicts && row.Kind() != KDict, !dicts && row.Kind() != KArray:
		want := "an array"
		if dicts {
			want = "a dict, as the first row is"
		}
		return nil, c.TypeErrorf("%s: row %d is %s; every row is %s", c.Name(), n, row.TypeName(), want)
	case !dicts:
		xs := *row.arr()
		fields := make([]string, len(xs))
		for i, x := range xs {
			s, err := csvText(c, x, n, strconv.Itoa(i+1))
			if err != nil {
				return nil, err
			}
			fields[i] = s
		}
		return fields, nil
	}
	have := make(map[string]bool, len(names))
	for _, name := range names {
		have[name] = true
	}
	// The row's entries by column name, so a key that is not a string is found by the
	// name its column was given.
	vals := make(map[string]Value, row.Len())
	var extra []string
	var twice string
	row.odict().Each(func(k, v Value) bool {
		key := csvKeyText(k)
		if _, dup := vals[key]; dup {
			twice = key
			return false
		}
		vals[key] = v
		if !have[key] {
			extra = append(extra, key)
		}
		return true
	})
	if twice != "" {
		return nil, c.ArgErrorf("%s: row %d has two keys written as the column %s; one of them would be lost", c.Name(), n, quoteString(twice))
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		return nil, c.ArgErrorf("%s: row %d has %s, which is not a column (%s); writing it would drop it",
			c.Name(), n, quoteString(extra[0]), strings.Join(names, ", "))
	}
	fields := make([]string, len(names))
	for i, name := range names {
		v, ok := vals[name]
		if !ok && o.strict {
			return nil, c.ArgErrorf("%s: row %d has no %s; pass {strict: false} to write an empty field", c.Name(), n, quoteString(name))
		}
		s, err := csvText(c, v, n, quoteString(name))
		if err != nil {
			return nil, err
		}
		fields[i] = s
	}
	return fields, nil
}

// csvvLine is csv.line(row, opts): one record, with no line break — for io.append and
// println.
func csvvLine(c *Ctx, args []Value) (Value, error) {
	o, err := csvOptions(c, args, 1)
	if err != nil {
		return Nil(), err
	}
	if args[0].Kind() != KArray {
		return Nil(), c.TypeErrorf("%s expects an array of fields, got %s; a dict row is written with csv.str, or as csv.line(row.values)", c.Name(), args[0].TypeName())
	}
	fields, err := csvFields(c, o, args[0], 1, false, nil)
	if err != nil {
		return Nil(), err
	}
	w := &csvWriter{o: o}
	w.record(fields)
	if err := c.allocString(w.b.Len()); err != nil {
		return Nil(), err
	}
	return Str(w.b.String()), nil
}
