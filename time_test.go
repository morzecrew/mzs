package mzs

import (
	"testing"
	"time"
)

// colClock is the fixed instant every time test measures against: 2025-03-12 14:30:15
// UTC, a Wednesday. Options.Now is the only clock mzs has (D15), so the whole suite is
// deterministic.
func colClock() time.Time { return time.Date(2025, 3, 12, 14, 30, 15, 0, time.UTC) }

func colTimeOpts() Options {
	o := DefaultOptions()
	o.EnableTime = true
	o.Now = colClock
	return o
}

// TestTimeModuleGating pins §12.8: without EnableTime the modules are absent, not
// broken, and time.now without a clock is an error even when they are present.
func TestTimeModuleGating(t *testing.T) {
	noClock := func() Options {
		o := DefaultOptions()
		o.EnableTime = true
		return o
	}

	tests := []struct {
		name       string
		opts       Options
		module     string
		wantModule bool
		member     string
		wantErr    bool
	}{
		{name: "time is absent without EnableTime", opts: DefaultOptions(), module: "time",
			member: "now", wantErr: true},
		{name: "date is absent without EnableTime", opts: DefaultOptions(), module: "date",
			member: "today", wantErr: true},
		{name: "time is present with EnableTime", opts: colTimeOpts(), module: "time",
			wantModule: true, member: "now"},
		{name: "date is present with EnableTime", opts: colTimeOpts(), module: "date",
			wantModule: true, member: "today"},
		{name: "time.now without a clock is an error", opts: noClock(), module: "time",
			wantModule: true, member: "now", wantErr: true},
		{name: "time.parse works without a clock", opts: noClock(), module: "time",
			wantModule: true, member: "parse"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := tt.opts.normalize()
			if _, ok := LookupModule(tt.module, &opts); ok != tt.wantModule {
				t.Fatalf("LookupModule(%q) present = %v; want %v", tt.module, ok, tt.wantModule)
			}
			c := colCtx(t, tt.opts)
			var args []Value
			if tt.member == "parse" {
				args = []Value{Str("2025-03-12")}
			}
			_, err := colModule(c, tt.module, tt.member, args...)
			if (err != nil) != tt.wantErr {
				t.Errorf("%s.%s error = %v; wantErr %v", tt.module, tt.member, err, tt.wantErr)
			}
		})
	}
}

func TestTimeParse(t *testing.T) {
	c := colCtx(t, colTimeOpts())

	tests := []struct {
		name    string
		module  string
		member  string
		args    []Value
		want    string // strftime("%Y-%m-%d %H:%M:%S")
		wantErr bool
	}{
		{name: "RFC3339", module: "time", member: "parse", args: []Value{Str("2025-03-12T14:30:15Z")},
			want: "2025-03-12 14:30:15"},
		{name: "date and time", module: "time", member: "parse", args: []Value{Str("2025-03-12 14:30:15")},
			want: "2025-03-12 14:30:15"},
		{name: "date and minutes", module: "time", member: "parse", args: []Value{Str("2025-03-12 14:30")},
			want: "2025-03-12 14:30:00"},
		{name: "date only", module: "time", member: "parse", args: []Value{Str("2025-03-12")},
			want: "2025-03-12 00:00:00"},
		{name: "day-first with slashes", module: "time", member: "parse", args: []Value{Str("12/03/25")},
			want: "2025-03-12 00:00:00"},
		{name: "day-first with dots", module: "time", member: "parse", args: []Value{Str("12.03.2025")},
			want: "2025-03-12 00:00:00"},
		{name: "surrounding whitespace is trimmed", module: "time", member: "parse",
			args: []Value{Str("  2025-03-12 ")}, want: "2025-03-12 00:00:00"},
		{name: "an explicit strftime layout", module: "time", member: "parse",
			args: []Value{Str("12|03|2025"), Str("%d|%m|%Y")}, want: "2025-03-12 00:00:00"},
		{name: "an explicit Go layout", module: "time", member: "parse",
			args: []Value{Str("12|03|2025"), Str("02|01|2006")}, want: "2025-03-12 00:00:00"},
		{name: "a layout that does not match", module: "time", member: "parse",
			args: []Value{Str("12/03/2025"), Str("%Y-%m-%d")}, wantErr: true},
		{name: "unparseable input", module: "time", member: "parse", args: []Value{Str("завтра")}, wantErr: true},
		{name: "time.at from unix seconds", module: "time", member: "at", args: []Value{Int(1741789815)},
			want: "2025-03-12 14:30:15"},
		{name: "date.parse truncates to midnight", module: "date", member: "parse",
			args: []Value{Str("2025-03-12 14:30:15")}, want: "2025-03-12 00:00:00"},
		{name: "date.parse reads the day-first dialogue forms", module: "date", member: "parse",
			args: []Value{Str("12/03/25")}, want: "2025-03-12 00:00:00"},
		{name: "date.parse rejects prose", module: "date", member: "parse",
			args: []Value{Str("вчера")}, wantErr: true},
		{name: "date.today is the clock at midnight", module: "date", member: "today",
			want: "2025-03-12 00:00:00"},
		{name: "time.now is the clock", module: "time", member: "now", want: "2025-03-12 14:30:15"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := colModule(c, tt.module, tt.member, tt.args...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("%s.%s error = %v; wantErr %v", tt.module, tt.member, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.Kind() != KTime {
				t.Fatalf("%s.%s returned %s; want time", tt.module, tt.member, got.Kind())
			}
			out, err := colInvoke(c, KTime, "strftime", got, Str("%Y-%m-%d %H:%M:%S"))
			if err != nil {
				t.Fatalf("strftime: %v", err)
			}
			if out.Str() != tt.want {
				t.Errorf("%s.%s = %s; want %s", tt.module, tt.member, out.Str(), tt.want)
			}
		})
	}
}

func TestTimeStrftime(t *testing.T) {
	c := colCtx(t, colTimeOpts())
	now := timeOf(colClock())

	tests := []struct {
		name   string
		layout string
		want   string
	}{
		{name: "four-digit year", layout: "%Y", want: "2025"},
		{name: "two-digit year", layout: "%y", want: "25"},
		{name: "padded month and day", layout: "%m/%d", want: "03/12"},
		{name: "unpadded month", layout: "%-m", want: "3"},
		{name: "unpadded day of a padded date", layout: "%-d.%-m.%Y", want: "12.3.2025"},
		{name: "time of day", layout: "%H:%M:%S", want: "14:30:15"},
		{name: "meridiem", layout: "%p", want: "PM"},
		{name: "short weekday", layout: "%a", want: "Wed"},
		{name: "long weekday", layout: "%A", want: "Wednesday"},
		{name: "short month", layout: "%b", want: "Mar"},
		{name: "long month", layout: "%B", want: "March"},
		{name: "day of year", layout: "%j", want: "071"},
		{name: "zone offset", layout: "%z", want: "+0000"},
		{name: "zone name", layout: "%Z", want: "UTC"},
		{name: "a literal percent", layout: "100%%", want: "100%"},
		{name: "an unknown directive is kept", layout: "%Q", want: "%Q"},
		{name: "cyrillic literal text", layout: "%d марта %Y", want: "12 марта 2025"},
		{name: "a trailing percent", layout: "abc%", want: "abc%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := colInvoke(c, KTime, "strftime", now, Str(tt.layout))
			if err != nil {
				t.Fatalf("strftime(%q) error = %v", tt.layout, err)
			}
			if got.Str() != tt.want {
				t.Errorf("strftime(%q) = %q; want %q", tt.layout, got.Str(), tt.want)
			}
		})
	}
}

func TestTimeValueMethods(t *testing.T) {
	c := colCtx(t, colTimeOpts())
	now := timeOf(colClock())

	tests := []struct {
		name    string
		method  string
		recv    Value
		args    []Value
		want    string
		wantErr bool
	}{
		{name: "year", method: "year", recv: now, want: "2025"},
		{name: "month", method: "month", recv: now, want: "3"},
		{name: "day", method: "day", recv: now, want: "12"},
		{name: "hour", method: "hour", recv: now, want: "14"},
		{name: "min", method: "min", recv: now, want: "30"},
		{name: "sec", method: "sec", recv: now, want: "15"},
		{name: "wday counts from Sunday", method: "wday", recv: now, want: "3"},
		{name: "yday", method: "yday", recv: now, want: "71"},
		{name: "in_time_zone shifts the wall clock", method: "in_time_zone", recv: now,
			args: []Value{Str("Europe/Moscow")}, want: "2025-03-12T17:30:15+03:00"},
		{name: "in_time_zone rejects an unknown zone", method: "in_time_zone", recv: now,
			args: []Value{Str("Mars/Olympus")}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := colInvoke(c, KTime, tt.method, tt.recv, tt.args...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("%s error = %v; wantErr %v", tt.method, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.Str() != tt.want {
				t.Errorf("%s = %s; want %s", tt.method, got.Str(), tt.want)
			}
		})
	}

	t.Run("to_date truncates to midnight", func(t *testing.T) {
		got, err := colInvoke(c, KTime, "to_date", now)
		if err != nil {
			t.Fatalf("to_date: %v", err)
		}
		out, err := colInvoke(c, KTime, "strftime", got, Str("%Y-%m-%d %H:%M:%S"))
		if err != nil {
			t.Fatalf("strftime: %v", err)
		}
		if out.Str() != "2025-03-12 00:00:00" {
			t.Errorf("to_date = %s; want 2025-03-12 00:00:00", out.Str())
		}
	})

	// `int` is not a KTime row: it is the universal conversion of §12.1, and Value.Int
	// already answers unix seconds. One operation, one implementation (D17).
	t.Run("int is the universal conversion", func(t *testing.T) {
		if HasMethod(KTime, "int") {
			t.Errorf("time answers 'int' from its own table; §12.1 owns that conversion")
		}
		if got := now.Int(); got != 1741789815 {
			t.Errorf("time.int = %d; want 1741789815", got)
		}
		if got := now.Float(); got != 1741789815 {
			t.Errorf("time.float = %v; want 1741789815", got)
		}
	})
}

// D17 for §12.8: `mon` is not a second spelling of `month`, and the Ruby conversions
// have no rows on a time value either.
func TestTimeHasNoOldNames(t *testing.T) {
	tests := []struct {
		old, use string
	}{
		{"mon", "month"},
		{"mday", "day"},
		{"to_i", "int"},
		{"to_f", "float"},
		{"to_s", "str"},
		{"to_time", "—"},
		{"strftime!", "strftime"},
		{"beginning_of_day", "to_date"},
	}

	for _, tt := range tests {
		t.Run(tt.old, func(t *testing.T) {
			if HasMethod(KTime, tt.old) {
				t.Errorf("time answers %q; D17 allows only %q", tt.old, tt.use)
			}
		})
	}

	// The string→date path is `date.parse(s)`; §12.2 has no to_date row.
	t.Run("string has no to_date", func(t *testing.T) {
		if HasMethod(KString, "to_date") {
			t.Errorf("string answers 'to_date'; §12.8 spells it date.parse(s)")
		}
	})
}

// TestTimeLocation pins that Options.Location, not the host's zone, decides how a
// naive timestamp is read.
func TestTimeLocation(t *testing.T) {
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	opts := colTimeOpts()
	opts.Location = msk
	c := colCtx(t, opts)

	got, err := colModule(c, "time", "parse", Str("2025-03-12 00:00:00"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if want := int64(1741726800); got.Int() != want {
		t.Errorf("parse in Europe/Moscow = %d; want %d", got.Int(), want)
	}
}
