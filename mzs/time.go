package mzs

import (
	"strconv"
	"strings"
	"time"

	// The IANA database is embedded so `in_time_zone("Europe/Moscow")` works on a
	// scratch container with no /usr/share/zoneinfo. time/tzdata is standard library,
	// so go.mod still has zero requires (§12.8).
	_ "time/tzdata"
)

// The `time` and `date` modules and the methods of the time value itself (§12.8). Both
// modules are ordinary lowercase values in the root scope, gated on Options.EnableTime:
// a host that never enables time makes them simply absent, so `time.now` is then
// `undefined variable 'time'` with no special case anywhere (§9.3).
//
// No function here reads the wall clock: `now` comes from Options.Now through Ctx.Now,
// which is what keeps evaluation reproducible (D15, §8.13).

func init() {
	SetModuleGate("time", ModuleGate{NeedsTime: true})
	RegisterModuleFunc("time", "now", 0, 0, timevNow)
	RegisterModuleFunc("time", "parse", 1, 2, timevParse)
	RegisterModuleFunc("time", "at", 1, 1, timevAt)
	RegisterModuleFunc("time", "mono", 0, 0, timevMono)

	SetModuleGate("date", ModuleGate{NeedsTime: true})
	RegisterModuleFunc("date", "today", 0, 0, timevToday)
	RegisterModuleFunc("date", "parse", 1, 1, timevParseDate)

	// `int` is not a row here: it is the universal conversion of §12.1, and Value.Int
	// already answers unix seconds for a time. One operation, one implementation (D17).
	RegisterMethods(KTime,
		Method{Name: "strftime", Min: 1, Max: 1, Fn: timevStrftimeMethod},
		Method{Name: "ms", Fn: func(c *Ctx, recv Value, _ []Value) (Value, error) {
			t, err := timevOf(c, recv)
			if err != nil {
				return Nil(), err
			}
			return Int(t.UnixMilli()), nil
		}},
		Method{Name: "to_date", Fn: timevToDate},
		Method{Name: "in_time_zone", Min: 1, Max: 1, Fn: timevInZone},
		Method{Name: "year", Fn: timevPart("year")},
		Method{Name: "month", Fn: timevPart("month")},
		Method{Name: "day", Fn: timevPart("day")},
		Method{Name: "hour", Fn: timevPart("hour")},
		Method{Name: "min", Fn: timevPart("min")},
		Method{Name: "sec", Fn: timevPart("sec")},
		Method{Name: "wday", Fn: timevPart("wday")},
		Method{Name: "yday", Fn: timevPart("yday")},
	)
}

// ---------------------------------------------------------------------------
// Module members
// ---------------------------------------------------------------------------

func timevNow(c *Ctx, args []Value) (Value, error) {
	t, err := c.Now()
	if err != nil {
		return Nil(), err
	}
	return timeOf(t.In(c.Location())), nil
}

// timevMono is seconds on a monotonic clock, for measuring an interval: `t0 = time.mono()`
// ... `time.mono() - t0`. The origin is the first reading in the Run, so the first call
// answers 0.0. It reads the host clock like `now` (D15) and is gated on Options.Now the
// same way; the readings are monotonic when the host's clock carries a monotonic
// reading, as Go's time.Now does, so a wall-clock step does not bend the interval.
func timevMono(c *Ctx, args []Value) (Value, error) {
	if c.rs.opts.Now == nil {
		return Nil(), nameErrorf("undefined function 'time.mono' (the host did not enable a clock)")
	}
	t := c.rs.opts.Now()
	sh := c.rs.sh
	if !sh.monoSet {
		sh.monoBase, sh.monoSet = t, true
	}
	return Float(t.Sub(sh.monoBase).Seconds()), nil
}

func timevToday(c *Ctx, args []Value) (Value, error) {
	t, err := c.Now()
	if err != nil {
		return Nil(), err
	}
	return timeOf(timevMidnight(t.In(c.Location()))), nil
}

func timevAt(c *Ctx, args []Value) (Value, error) {
	secs, err := argInt(c, args[0])
	if err != nil {
		return Nil(), err
	}
	return timeOf(time.Unix(secs, 0).In(c.Location())), nil
}

func timevParse(c *Ctx, args []Value) (Value, error) {
	t, err := timevParseValue(c, args)
	if err != nil {
		return Nil(), err
	}
	return timeOf(t), nil
}

// timevParseDate is time.parse truncated to midnight: a date in mzs is a time, since
// §12.8 gives them one kind.
func timevParseDate(c *Ctx, args []Value) (Value, error) {
	t, err := timevParseValue(c, args)
	if err != nil {
		return Nil(), err
	}
	return timeOf(timevMidnight(t)), nil
}

func timevParseValue(c *Ctx, args []Value) (time.Time, error) {
	text, err := argStr(c, args[0])
	if err != nil {
		return time.Time{}, err
	}
	s := strings.TrimSpace(text)
	loc := c.Location()
	if len(args) == 2 {
		spec, err := argStr(c, args[1])
		if err != nil {
			return time.Time{}, err
		}
		if spec != "" {
			t, perr := time.ParseInLocation(timevGoLayout(spec), s, loc)
			if perr != nil {
				return time.Time{}, c.ArgErrorf("%s: %s", c.Name(), perr.Error())
			}
			return t, nil
		}
	}
	if t, ok := timevAuto(s, loc); ok {
		return t, nil
	}
	return time.Time{}, c.ArgErrorf("%s: cannot parse %s as a time", c.Name(), quoteString(s))
}

// timevAutoLayouts are the shapes §12.8 promises to accept without a layout, longest and
// most specific first. The dotted and slashed forms are day-first: they come from
// Russian-language dialogues, where 12/03/25 is 12 March.
var timevAutoLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"02.01.2006 15:04:05",
	"02.01.2006 15:04",
	"02.01.2006",
	"02.01.06",
	"02/01/2006 15:04:05",
	"02/01/2006 15:04",
	"02/01/2006",
	"02/01/06",
}

func timevAuto(s string, loc *time.Location) (time.Time, bool) {
	for _, layout := range timevAutoLayouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ---------------------------------------------------------------------------
// Time values
// ---------------------------------------------------------------------------

func timevOf(c *Ctx, v Value) (time.Time, error) {
	if v.Kind() != KTime {
		return time.Time{}, c.TypeErrorf("%s expects a time, got %s", c.Name(), v.TypeName())
	}
	return v.Time(), nil
}

func timevMidnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func timevToDate(c *Ctx, recv Value, args []Value) (Value, error) {
	t, err := timevOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	return timeOf(timevMidnight(t)), nil
}

func timevInZone(c *Ctx, recv Value, args []Value) (Value, error) {
	t, err := timevOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	name, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	loc, lerr := time.LoadLocation(name)
	if lerr != nil {
		return Nil(), c.ArgErrorf("in_time_zone: unknown time zone %s", quoteString(name))
	}
	return timeOf(t.In(loc)), nil
}

// timevPart builds the year/month/day/… rows from one table, since they differ only in
// which field they read.
func timevPart(part string) func(c *Ctx, recv Value, args []Value) (Value, error) {
	return func(c *Ctx, recv Value, args []Value) (Value, error) {
		t, err := timevOf(c, recv)
		if err != nil {
			return Nil(), err
		}
		switch part {
		case "year":
			return Int(int64(t.Year())), nil
		case "month":
			return Int(int64(t.Month())), nil
		case "day":
			return Int(int64(t.Day())), nil
		case "hour":
			return Int(int64(t.Hour())), nil
		case "min":
			return Int(int64(t.Minute())), nil
		case "sec":
			return Int(int64(t.Second())), nil
		case "wday":
			// Sunday is 0, which is both time.Weekday's numbering and strftime's.
			return Int(int64(t.Weekday())), nil
		case "yday":
			return Int(int64(t.YearDay())), nil
		}
		return Nil(), nil
	}
}

func timevStrftimeMethod(c *Ctx, recv Value, args []Value) (Value, error) {
	t, err := timevOf(c, recv)
	if err != nil {
		return Nil(), err
	}
	layout, err := argStr(c, args[0])
	if err != nil {
		return Nil(), err
	}
	out, bad := timevStrftime(t, layout, c.rs.opts.MaxStringBytes)
	if bad != "" {
		if bad == "%" {
			return Nil(), c.ArgErrorf("strftime: the layout ends in a lone %%; write %%%% for a percent sign")
		}
		return Nil(), c.ArgErrorf("strftime: unknown directive %s; write %%%% for a percent sign", quoteString(bad))
	}
	if err := c.CheckString(len(out)); err != nil {
		return Nil(), err
	}
	return Str(out), nil
}

// ---------------------------------------------------------------------------
// strftime (§12.8)
// ---------------------------------------------------------------------------

// timevStrftime renders the C-style directives §12.8 lists, plus the `-` flag for an
// unpadded number (`%-d`, `%-m`). An unknown directive is not rendered: it is returned as
// bad, and the row raises an `argument` error naming it (audit B6, D16), because echoing
// `%G` back hides a typo until someone reads the output. A `%` with nothing after it is
// bad as "%". Once the output passes max it stops growing, so a layout of repeated `%s` —
// two bytes that render as up to twenty — never builds much more than the cap it is
// checked against; it still reads the layout to its end, so a bad directive anywhere is
// the argument error, and only a layout that is all good is the caller's string limit.
func timevStrftime(t time.Time, layout string, max int) (out, bad string) {
	var sb strings.Builder
	sb.Grow(len(layout) + 16)
	rs := []rune(layout)
	// Past the cap the rest renders into a scratch builder that is emptied each time, so
	// the layout is still checked to its end without the output growing further.
	w := &sb
	var scratch strings.Builder
	for i := 0; i < len(rs); i++ {
		if w == &sb && sb.Len() > max {
			w = &scratch
		}
		scratch.Reset()
		if rs[i] != '%' {
			w.WriteRune(rs[i])
			continue
		}
		if i+1 >= len(rs) {
			return "", "%"
		}
		i++
		pad := true
		if rs[i] == '-' {
			if i+1 >= len(rs) {
				return "", "%-"
			}
			pad = false
			i++
		}
		switch rs[i] {
		case 'Y':
			w.WriteString(strconv.Itoa(t.Year()))
		case 'y':
			w.WriteString(timevNum(t.Year()%100, 2, pad))
		case 'm':
			w.WriteString(timevNum(int(t.Month()), 2, pad))
		case 'd':
			w.WriteString(timevNum(t.Day(), 2, pad))
		case 'H':
			w.WriteString(timevNum(t.Hour(), 2, pad))
		case 'M':
			w.WriteString(timevNum(t.Minute(), 2, pad))
		case 'S':
			w.WriteString(timevNum(t.Second(), 2, pad))
		case 's':
			w.WriteString(strconv.FormatInt(t.Unix(), 10))
		case 'L':
			w.WriteString(timevNum(t.Nanosecond()/1e6, 3, pad))
		case 'f':
			w.WriteString(timevNum(t.Nanosecond()/1e3, 6, pad))
		case 'j':
			w.WriteString(timevNum(t.YearDay(), 3, pad))
		case 'a':
			w.WriteString(t.Format("Mon"))
		case 'A':
			w.WriteString(t.Format("Monday"))
		case 'b':
			w.WriteString(t.Format("Jan"))
		case 'B':
			w.WriteString(t.Format("January"))
		case 'p':
			w.WriteString(t.Format("PM"))
		case 'z':
			w.WriteString(t.Format("-0700"))
		case 'Z':
			w.WriteString(t.Format("MST"))
		case '%':
			w.WriteByte('%')
		default:
			if !pad {
				return "", "%-" + string(rs[i])
			}
			return "", "%" + string(rs[i])
		}
	}
	return sb.String(), ""
}

func timevNum(n, width int, pad bool) string {
	s := strconv.Itoa(n)
	if !pad {
		return s
	}
	for len(s) < width {
		s = "0" + s
	}
	return s
}

// timevGoLayout accepts either a strftime layout (`"%d.%m.%Y"`, the same spelling
// `strftime` renders) or a Go reference layout, and returns the Go form. A layout with
// no `%` is already Go's.
func timevGoLayout(layout string) string {
	if !strings.ContainsRune(layout, '%') {
		return layout
	}
	repl := strings.NewReplacer(
		"%Y", "2006", "%y", "06", "%m", "01", "%-m", "1",
		"%d", "02", "%-d", "2", "%H", "15", "%M", "04", "%S", "05",
		"%a", "Mon", "%A", "Monday", "%b", "Jan", "%B", "January",
		"%p", "PM", "%z", "-0700", "%Z", "MST", "%%", "%",
	)
	return repl.Replace(layout)
}
