package mzs

import (
	"fmt"
	"strings"
)

// The `args` module (§12.21) — a script's command-line flags, declared in one dict.
//
//	include args
//	opt = args.parse({width: 800, force: {default: false, short: "f"}},
//	                 {usage: "resize [flags] <file>…", min: 1})
//	opt["width"]                     # 800, or what --width said, as an int
//	opt["rest"]                      # the positionals
//
// The kind of each default decides how the flag's text is converted, so there is no type
// vocabulary to learn. `--help` is always there and prints the same text args.usage
// returns. A usage error writes the message and the usage to stderr and stops the Run
// with status 2, which is the CLI's own status for a usage error (§15); with
// `{exit: false}` it raises an `argument` error instead, for a host or a test.
//
// The module reads $ARGV and nothing else, so it needs no gate: a host that binds no
// $ARGV gets the defaults back.

func init() {
	// Registration order is `args.keys` order (§8.13).
	RegisterModuleFunc("args", "parse", 1, 2, argsvParse)
	RegisterModuleFunc("args", "usage", 1, 2, argsvUsage)
}

// The kinds a flag can have, decided by its default.
type argKind int

const (
	flagBool   argKind = iota // false / true: --x, --no-x, --x=true
	flagInt                   // an int: strict decimal, as parse_int
	flagFloat                 // a float: strict, as parse_float
	flagString                // a string
	flagList                  // an array: every occurrence appended
	flagMaybe                 // nil: a string, or nil when absent
)

// argFlag is one entry of the spec.
type argFlag struct {
	key      string // the spec key, and the result key: dry_run
	long     string // the command-line spelling: --dry-run
	short    byte   // 'q' for -q; 0 when there is none
	kind     argKind
	def      Value
	help     string
	required bool
}

// argSpec is the parsed spec plus the options that shape the text around it.
type argSpec struct {
	flags   []*argFlag
	byLong  map[string]*argFlag
	byShort map[byte]*argFlag
	argv    []string
	usage   string // the synopsis, after "usage: "
	name    string // the prefix of an error line
	min     int64
	max     int64 // -1: no maximum
	exit    bool
}

const (
	argsOptArgv  = "argv"
	argsOptUsage = "usage"
	argsOptName  = "name"
	argsOptMin   = "min"
	argsOptMax   = "max"
	argsOptExit  = "exit"
)

var argsOptKeys = []string{argsOptArgv, argsOptUsage, argsOptName, argsOptMin, argsOptMax, argsOptExit}

var argsEntryKeys = []string{"default", "help", "short", "required"}

// argsvParse is `args.parse(spec, opts = {})`.
func argsvParse(c *Ctx, args []Value) (Value, error) {
	sp, err := argsSpec(c, args)
	if err != nil {
		return Nil(), err
	}
	res, perr, err := sp.parse(c)
	if err != nil {
		return Nil(), err
	}
	if perr == nil {
		return res, nil
	}
	if !sp.exit {
		e := argErrorf("%s: %s", c.Name(), perr.msg)
		d := NewOrderedDictCap(2)
		flag := Nil()
		if perr.flag != "" {
			flag = Str(perr.flag)
		}
		_ = d.Set(Str("flag"), flag)
		_ = d.Set(Str("reason"), Str(perr.reason))
		e.Data = dictOf(d)
		return Nil(), c.position(e)
	}
	text := sp.text()
	if err := c.CheckString(len(text)); err != nil {
		return Nil(), err
	}
	if perr.reason == "help" {
		if err := writeOut(c, c.Out(), text); err != nil {
			return Nil(), err
		}
		return Nil(), c.position(exitError(0))
	}
	line := perr.msg
	if sp.name != "" {
		line = sp.name + ": " + line
	}
	if err := writeOut(c, c.rs.opts.errOut(), line+"\n"+text); err != nil {
		return Nil(), err
	}
	return Nil(), c.position(exitError(2))
}

// argsvUsage is `args.usage(spec, opts = {})`: the text --help prints.
func argsvUsage(c *Ctx, args []Value) (Value, error) {
	sp, err := argsSpec(c, args)
	if err != nil {
		return Nil(), err
	}
	s := sp.text()
	if err := c.CheckString(len(s)); err != nil {
		return Nil(), err
	}
	return Str(s), nil
}

// argsSpec reads both arguments. A mistake in them is the script's, not the person's at
// the command line, so it is an ordinary error whatever `exit` says.
func argsSpec(c *Ctx, args []Value) (*argSpec, error) {
	sp := &argSpec{byLong: map[string]*argFlag{}, byShort: map[byte]*argFlag{}, max: -1, exit: true}
	if err := sp.readOpts(c, argAt(args, 1)); err != nil {
		return nil, err
	}
	spec := args[0]
	if spec.Kind() != KDict {
		return nil, c.TypeErrorf("%s expects a spec dict as argument 1, got %s", c.Name(), spec.TypeName())
	}
	if err := c.Step(int64(spec.Len()) + 1); err != nil {
		return nil, err
	}
	var failed error
	spec.odict().Each(func(k, v Value) bool {
		f, err := argsEntry(c, k, v)
		if err != nil {
			failed = err
			return false
		}
		if f.short != 0 {
			if g, dup := sp.byShort[f.short]; dup {
				failed = c.ArgErrorf("%s: flags '%s' and '%s' both have short %q", c.Name(), g.key, f.key, string(f.short))
				return false
			}
			sp.byShort[f.short] = f
		}
		sp.byLong[f.long] = f
		sp.flags = append(sp.flags, f)
		return true
	})
	if failed != nil {
		return nil, failed
	}
	return sp, nil
}

// argsEntry reads one `key: entry` of the spec.
func argsEntry(c *Ctx, k, v Value) (*argFlag, error) {
	if k.Kind() != KString {
		return nil, c.TypeErrorf("%s: a flag name is a string, got %s", c.Name(), k.TypeName())
	}
	key := k.Str()
	if !argsKeyOK(key) {
		return nil, c.ArgErrorf("%s: flag name %s is not letters, digits and _, starting with a letter", c.Name(), quoteString(key))
	}
	switch key {
	case "rest":
		return nil, c.ArgErrorf("%s: 'rest' is the key of the positionals, so it cannot be a flag", c.Name())
	case "help":
		return nil, c.ArgErrorf("%s: --help is always present, so 'help' cannot be a flag", c.Name())
	}
	f := &argFlag{key: key, long: "--" + strings.ReplaceAll(key, "_", "-"), def: v}
	if v.Kind() == KDict {
		f.def = Nil()
		var failed error
		v.odict().Each(func(ek, ev Value) bool {
			name := ""
			if ek.Kind() == KString {
				name = ek.Str()
			}
			switch name {
			case "default":
				f.def = ev
			case "help":
				if ev.Kind() != KString {
					failed = c.TypeErrorf("%s: flag '%s': help is a string, got %s", c.Name(), key, ev.TypeName())
					return false
				}
				f.help = ev.Str()
			case "short":
				s := ""
				if ev.Kind() == KString {
					s = ev.Str()
				}
				if len(s) != 1 || !isASCIIAlnum(s[0]) {
					failed = c.ArgErrorf("%s: flag '%s': short is one letter or digit, got %s", c.Name(), key, ev.Inspect())
					return false
				}
				if s == "h" {
					failed = c.ArgErrorf("%s: flag '%s': -h is always --help", c.Name(), key)
					return false
				}
				f.short = s[0]
			case "required":
				if ev.Kind() != KBool {
					failed = c.TypeErrorf("%s: flag '%s': required is true or false, got %s", c.Name(), key, ev.TypeName())
					return false
				}
				f.required = ev.Truthy()
			default:
				msg := fmt.Sprintf("%s: flag '%s': unknown key %s; the keys are %s", c.Name(), key, ek.Inspect(), strings.Join(argsEntryKeys, ", "))
				if s := nearest(name, argsEntryKeys); s != "" {
					msg = fmt.Sprintf("%s: flag '%s': unknown key %s; did you mean '%s'?", c.Name(), key, ek.Inspect(), s)
				}
				failed = c.ArgErrorf("%s", msg)
				return false
			}
			return true
		})
		if failed != nil {
			return nil, failed
		}
		if f.required {
			if _, has := v.odict().Get(Str("default")); has {
				failed := c.ArgErrorf("%s: flag '%s' is required, so it has no default", c.Name(), key)
				return nil, failed
			}
		}
	}
	switch f.def.Kind() {
	case KBool:
		f.kind = flagBool
	case KInt:
		f.kind = flagInt
	case KFloat:
		f.kind = flagFloat
	case KString:
		f.kind = flagString
	case KNil:
		f.kind = flagMaybe
	case KArray:
		for _, e := range f.def.Elems() {
			if e.Kind() != KString {
				return nil, c.TypeErrorf("%s: flag '%s': a repeated flag collects strings, so its default holds strings, got %s", c.Name(), key, e.TypeName())
			}
		}
		f.kind = flagList
	default:
		return nil, c.TypeErrorf("%s: flag '%s': the default is a bool, int, float, string, array or nil, got %s", c.Name(), key, f.def.TypeName())
	}
	return f, nil
}

func argsKeyOK(s string) bool {
	if s == "" || !isASCIILetter(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isASCIIAlnum(s[i]) && s[i] != '_' {
			return false
		}
	}
	return true
}

func isASCIILetter(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }
func isASCIIAlnum(b byte) bool  { return isASCIILetter(b) || b >= '0' && b <= '9' }

// readOpts reads the second argument.
func (sp *argSpec) readOpts(c *Ctx, v Value) error {
	argv := c.Global("$ARGV")
	switch v.Kind() {
	case KNil:
	case KDict:
		var failed error
		v.odict().Each(func(k, val Value) bool {
			key := ""
			if k.Kind() == KString {
				key = k.Str()
			}
			switch key {
			case argsOptArgv:
				argv = val
			case argsOptUsage, argsOptName:
				if val.Kind() != KString {
					failed = c.TypeErrorf("%s: option '%s' is a string, got %s", c.Name(), key, val.TypeName())
					return false
				}
				if key == argsOptUsage {
					sp.usage = val.Str()
				} else {
					sp.name = val.Str()
				}
			case argsOptMin, argsOptMax:
				if val.Kind() != KInt || val.Int() < 0 {
					failed = c.TypeErrorf("%s: option '%s' is an int of 0 or more, got %s", c.Name(), key, val.Inspect())
					return false
				}
				if key == argsOptMin {
					sp.min = val.Int()
				} else {
					sp.max = val.Int()
				}
			case argsOptExit:
				if val.Kind() != KBool {
					failed = c.TypeErrorf("%s: option 'exit' is true or false, got %s", c.Name(), val.TypeName())
					return false
				}
				sp.exit = val.Truthy()
			default:
				msg := c.Name() + ": unknown option " + k.Inspect() + "; the options are " + strings.Join(argsOptKeys, ", ")
				if s := nearest(key, argsOptKeys); s != "" {
					msg = c.Name() + ": unknown option " + k.Inspect() + "; did you mean '" + s + "'?"
				}
				failed = c.ArgErrorf("%s", msg)
				return false
			}
			return true
		})
		if failed != nil {
			return failed
		}
	default:
		return c.TypeErrorf("%s expects an options dict as argument 2, got %s", c.Name(), v.TypeName())
	}
	if sp.max >= 0 && sp.min > sp.max {
		return c.ArgErrorf("%s: option 'min' (%d) is more than 'max' (%d)", c.Name(), sp.min, sp.max)
	}
	if sp.name == "" && sp.usage != "" {
		sp.name, _, _ = strings.Cut(strings.TrimSpace(sp.usage), " ")
	}
	switch argv.Kind() {
	case KNil:
	case KArray:
		for _, a := range argv.Elems() {
			if a.Kind() != KString {
				return c.TypeErrorf("%s: the arguments are strings, got %s in them", c.Name(), a.TypeName())
			}
			sp.argv = append(sp.argv, a.Str())
		}
	default:
		return c.TypeErrorf("%s: the arguments are an array of strings, got %s", c.Name(), argv.TypeName())
	}
	return nil
}

// argProblem is a usage error: the person at the command line typed something the spec
// does not allow. reason is one of the words §12.21 lists, for `data.reason`.
type argProblem struct {
	flag   string
	reason string
	msg    string
}

// parse walks the arguments. Flags and positionals may be mixed; `--` ends the flags.
func (sp *argSpec) parse(c *Ctx) (Value, *argProblem, error) {
	if err := c.Step(int64(len(sp.argv)) + 1); err != nil {
		return Nil(), nil, err
	}
	// The positionals and a repeated flag's values are each at most the whole argument
	// list, so bounding that once bounds every array the result holds. A host may bind a
	// $ARGV longer than a script could have built.
	if err := c.CheckCollection(len(sp.argv)); err != nil {
		return Nil(), nil, err
	}
	p := &argParse{vals: map[*argFlag]Value{}, lists: map[*argFlag][]Value{}, seen: map[*argFlag]bool{}}
	v, prob := sp.walk(p)
	return v, prob, nil
}

// argParse is what a walk of the arguments has found so far. A repeated flag's values
// are collected in lists and made an array once, at the end.
type argParse struct {
	vals  map[*argFlag]Value
	lists map[*argFlag][]Value
	seen  map[*argFlag]bool
}

func (sp *argSpec) walk(p *argParse) (Value, *argProblem) {
	vals, seen := p.vals, p.seen
	var rest []Value
	argv := sp.argv
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--":
			for _, r := range argv[i+1:] {
				rest = append(rest, Str(r))
			}
			i = len(argv)
		case strings.HasPrefix(a, "--"):
			name, val, hasVal := strings.Cut(a, "=")
			if name == "--help" {
				return Nil(), &argProblem{flag: "--help", reason: "help", msg: "--help was given"}
			}
			f, neg := sp.byLong[name], false
			if f == nil {
				if g := sp.byLong["--"+strings.TrimPrefix(name, "--no-")]; strings.HasPrefix(name, "--no-") && g != nil && g.kind == flagBool {
					f, neg = g, true
				}
			}
			if f == nil {
				return Nil(), sp.unknown(name)
			}
			if f.kind == flagBool {
				v := !neg
				if hasVal {
					if neg {
						return Nil(), &argProblem{flag: name, reason: "takes_no_value", msg: fmt.Sprintf("flag %s takes no value, got %s", name, quoteString(a))}
					}
					switch val {
					case "true":
					case "false":
						v = false
					default:
						return Nil(), &argProblem{flag: name, reason: "bad_value", msg: fmt.Sprintf("flag %s is true or false, got %s", name, quoteString(val))}
					}
				}
				vals[f], seen[f] = Bool(v), true
				continue
			}
			if !hasVal {
				if i+1 >= len(argv) {
					return Nil(), &argProblem{flag: name, reason: "needs_value", msg: fmt.Sprintf("flag %s needs a value", name)}
				}
				i++
				val = argv[i]
			}
			if prob := sp.set(p, f, name, val); prob != nil {
				return Nil(), prob
			}
		case len(a) > 1 && a[0] == '-':
			// A cluster of short flags: every one but the last is a bool, and the last
			// may take a value, attached (-q0.5, -q=0.5) or as the next argument.
			for j := 1; j < len(a); j++ {
				ch := a[j]
				if ch == 'h' {
					return Nil(), &argProblem{flag: "-h", reason: "help", msg: "-h was given"}
				}
				f := sp.byShort[ch]
				typed := "-" + string(ch)
				if f == nil {
					return Nil(), sp.unknown(typed)
				}
				if f.kind == flagBool {
					vals[f], seen[f] = Bool(true), true
					continue
				}
				val := strings.TrimPrefix(a[j+1:], "=")
				if j+1 == len(a) {
					if i+1 >= len(argv) {
						return Nil(), &argProblem{flag: typed, reason: "needs_value", msg: fmt.Sprintf("flag %s needs a value", typed)}
					}
					i++
					val = argv[i]
				}
				if prob := sp.set(p, f, typed, val); prob != nil {
					return Nil(), prob
				}
				break
			}
		default:
			rest = append(rest, Str(a))
		}
	}
	for _, f := range sp.flags {
		if f.required && !seen[f] {
			return Nil(), &argProblem{flag: f.long, reason: "required", msg: fmt.Sprintf("flag %s is required", f.long)}
		}
	}
	if n := int64(len(rest)); n < sp.min {
		return Nil(), &argProblem{reason: "too_few", msg: fmt.Sprintf("expected at least %s, got %d", plural(sp.min, "argument"), n)}
	}
	if n := int64(len(rest)); sp.max >= 0 && n > sp.max {
		return Nil(), &argProblem{reason: "too_many", msg: fmt.Sprintf("expected at most %s, got %d", plural(sp.max, "argument"), n)}
	}
	d := NewOrderedDictCap(len(sp.flags) + 1)
	for _, f := range sp.flags {
		v, ok := vals[f]
		switch {
		case f.kind == flagList && seen[f]:
			v = Array(p.lists[f]...)
		case f.kind == flagList:
			v = Array(append([]Value(nil), f.def.Elems()...)...)
		case !ok:
			v = f.def
		}
		_ = d.Set(Str(f.key), v)
	}
	_ = d.Set(Str("rest"), Array(rest...))
	return dictOf(d), nil
}

// set converts one value for a flag that takes one.
func (sp *argSpec) set(p *argParse, f *argFlag, typed, val string) *argProblem {
	var v Value
	switch f.kind {
	case flagInt:
		n, ok := parseStrictInt(val, 10)
		if !ok {
			return &argProblem{flag: typed, reason: "bad_value", msg: fmt.Sprintf("flag %s is an int, got %s", typed, quoteString(val))}
		}
		v = Int(n)
	case flagFloat:
		x, ok := parseStrictFloat(val)
		if !ok {
			return &argProblem{flag: typed, reason: "bad_value", msg: fmt.Sprintf("flag %s is a number, got %s", typed, quoteString(val))}
		}
		v = Float(x)
	case flagList:
		// Given on the command line, a repeated flag's values replace its default.
		p.lists[f] = append(p.lists[f], Str(val))
		p.seen[f] = true
		return nil
	default:
		v = Str(val)
	}
	p.vals[f], p.seen[f] = v, true
	return nil
}

// unknown is the error for a flag the spec does not have, with the near miss in it.
func (sp *argSpec) unknown(typed string) *argProblem {
	msg := "unknown flag " + typed
	if strings.HasPrefix(typed, "--") {
		// The near miss is looked for among the spec keys, which are identifiers — the
		// command-line spelling is not one, and nearest only offers what could be typed.
		names := make([]string, 0, len(sp.flags)+1)
		for _, f := range sp.flags {
			names = append(names, f.key)
		}
		names = append(names, "help")
		typo := strings.ReplaceAll(strings.TrimPrefix(typed, "--"), "-", "_")
		if s := nearest(typo, names); s != "" {
			msg += " (did you mean --" + strings.ReplaceAll(s, "_", "-") + "?)"
		}
	}
	return &argProblem{flag: typed, reason: "unknown", msg: msg}
}

func plural(n int64, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// text is the usage: the synopsis, then one row per flag, --help first.
func (sp *argSpec) text() string {
	type row struct{ left, right string }
	rows := []row{{"-h, --help", "show this help"}}
	for _, f := range sp.flags {
		left := "    "
		if f.short != 0 {
			left = "-" + string(f.short) + ", "
		}
		switch f.kind {
		case flagBool:
			if f.def.Truthy() {
				left += "--[no-]" + strings.TrimPrefix(f.long, "--")
			} else {
				left += f.long
			}
		case flagInt:
			left += f.long + " <int>"
		case flagFloat:
			left += f.long + " <number>"
		default:
			left += f.long + " <string>"
		}
		var notes []string
		if f.help != "" {
			notes = append(notes, f.help)
		}
		switch {
		case f.required:
			notes = append(notes, "(required)")
		case f.kind == flagList:
			if f.def.Len() > 0 {
				notes = append(notes, "(repeatable, default "+f.def.Inspect()+")")
			} else {
				notes = append(notes, "(repeatable)")
			}
		case f.kind == flagBool && f.def.Truthy(), f.kind == flagInt, f.kind == flagFloat:
			notes = append(notes, "(default "+f.def.Inspect()+")")
		case f.kind == flagString && f.def.Str() != "":
			notes = append(notes, "(default "+f.def.Inspect()+")")
		}
		rows = append(rows, row{left, strings.Join(notes, " ")})
	}
	width := 0
	for _, r := range rows {
		width = max(width, len(r.left))
	}
	var b strings.Builder
	switch {
	case sp.usage != "":
		b.WriteString("usage: " + sp.usage + "\n\n")
	case sp.name != "":
		b.WriteString("usage: " + sp.name + " [flags]\n\n")
	}
	b.WriteString("flags:\n")
	for _, r := range rows {
		if r.right == "" {
			b.WriteString("  " + r.left + "\n")
			continue
		}
		b.WriteString("  " + r.left + strings.Repeat(" ", width-len(r.left)+2) + r.right + "\n")
	}
	return b.String()
}
