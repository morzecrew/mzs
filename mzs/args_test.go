package mzs

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// RFC 0017: the args module turns $ARGV into typed flags from one dict, and prints --help
// from the same dict.

// runArgs runs src with $ARGV bound to argv (nil leaves it unbound) and returns the value,
// what was written to stdout and stderr, and the error.
func runArgs(t *testing.T, argv []string, src string) (v Value, stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	var vars map[string]Value
	if argv != nil {
		elems := make([]Value, len(argv))
		for i, a := range argv {
			elems[i] = Str(a)
		}
		vars = map[string]Value{"$ARGV": Array(elems...)}
	}
	v, err = New(Options{Stdout: &out, Stderr: &errOut}).Eval(context.Background(), "include args\n"+src, vars)
	return v, out.String(), errOut.String(), err
}

const argsResizeSpec = `{
  width:   {default: 800,   help: "target width in pixels"},
  quality: {default: 0.85,  short: "q"},
  out:     {default: nil,   help: "output file"},
  force:   {default: false, short: "f"},
  verbose: {default: false, short: "v"},
  tag:     {default: [],    help: "a label"},
  dry_run: false,
  color:   true,
  name:    "anon",
}`

func TestArgsParse(t *testing.T) {
	t.Parallel()

	defaults := `{"width":800,"quality":0.85,"out":null,"force":false,"verbose":false,"tag":[],"dry_run":false,"color":true,"name":"anon","rest":[]}`
	cases := []struct {
		name string
		argv []string
		want string
	}{
		{"no arguments: the defaults", []string{}, defaults},
		{"an int, both spellings", []string{"--width", "640"}, `"width":640`},
		{"an int with =", []string{"--width=1_024"}, `"width":1024`},
		{"a negative int as the next argument", []string{"--width", "-5"}, `"width":-5`},
		{"a float", []string{"--quality=0.5"}, `"quality":0.5`},
		{"nil default: a string", []string{"--out", "x.png"}, `"out":"x.png"`},
		{"a string, empty", []string{"--name="}, `"name":""`},
		{"a bool", []string{"--force"}, `"force":true`},
		{"a bool turned off", []string{"--no-color"}, `"color":false`},
		{"a bool with =false", []string{"--force=false"}, `"force":false`},
		{"a bool with =true", []string{"--color=true"}, `"color":true`},
		{"underscores are dashes", []string{"--dry-run"}, `"dry_run":true`},
		{"a repeated flag", []string{"--tag", "a", "--tag=b"}, `"tag":["a","b"]`},
		{"a repeated value is last-wins otherwise", []string{"--width", "1", "--width", "2"}, `"width":2`},
		{"short bools combine", []string{"-fv"}, `"force":true,"verbose":true`},
		{"a short with the next argument", []string{"-q", "0.25"}, `"quality":0.25`},
		{"a short with its value attached", []string{"-q0.25"}, `"quality":0.25`},
		{"a short with =", []string{"-q=0.25"}, `"quality":0.25`},
		{"bools then a value in one cluster", []string{"-fvq", "0.1"}, `"force":true,"verbose":true,"tag":[]`},
		{"positionals around flags", []string{"a", "--force", "b"}, `"rest":["a","b"]`},
		{"-- ends the flags", []string{"--", "--force", "-q"}, `"force":false,"verbose":false,"tag":[],"dry_run":false,"color":true,"name":"anon","rest":["--force","-q"]`},
		{"a lone - is a positional", []string{"-"}, `"rest":["-"]`},
		{"a flag's value may look like a flag", []string{"--out", "--force"}, `"out":"--force","force":false`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v, _, errOut, err := runArgs(t, tc.argv, "args.parse("+argsResizeSpec+").json")
			if err != nil {
				t.Fatalf("%q: %v (stderr %q)", tc.argv, err, errOut)
			}
			if !strings.Contains(v.Str(), tc.want) {
				t.Errorf("%q: got %s, want it to contain %s", tc.argv, v.Str(), tc.want)
			}
		})
	}
}

// A float flag given an integer spelling is still a float.
func TestArgsFloatFromAnIntSpelling(t *testing.T) {
	t.Parallel()

	v, _, _, err := runArgs(t, []string{"--quality", "1"}, `type(args.parse(`+argsResizeSpec+`)["quality"])`)
	if err != nil || v.Str() != "float" {
		t.Errorf("type = %v, %v", v, err)
	}
}

// A bare value is the default; a dict is the entry. The result keeps the spec's order,
// with rest last.
func TestArgsBareDefaultsAndOrder(t *testing.T) {
	t.Parallel()

	v, _, _, err := runArgs(t, []string{"x", "--b", "2"}, `args.parse({b: 1, a: {default: "s"}, c: {}}).keys`)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Inspect(); got != `["b","a","c","rest"]` {
		t.Errorf("keys = %s", got)
	}
	// `{}` declares a flag with no default: a string, or nil.
	v, _, _, err = runArgs(t, []string{}, `args.parse({c: {}})["c"]`)
	if err != nil || !v.IsNil() {
		t.Errorf("an entry with no default = %v, %v; want nil", v, err)
	}
}

// With {exit: false} a usage error is an `argument` error with {flag, reason} in data,
// which is how a host or a test tells them apart.
func TestArgsUsageErrorsRaise(t *testing.T) {
	t.Parallel()

	cases := []struct {
		argv   []string
		opts   string
		reason string
		flag   string // "" for nil
		msg    string
	}{
		{[]string{"--widht", "3"}, `{}`, "unknown", "--widht", "unknown flag --widht (did you mean --width?)"},
		{[]string{"--dry-rnu"}, `{}`, "unknown", "--dry-rnu", "(did you mean --dry-run?)"},
		{[]string{"--nope"}, `{}`, "unknown", "--nope", "unknown flag --nope"},
		{[]string{"--no-width"}, `{}`, "unknown", "--no-width", "unknown flag --no-width"},
		{[]string{"-x"}, `{}`, "unknown", "-x", "unknown flag -x"},
		{[]string{"-fx"}, `{}`, "unknown", "-x", "unknown flag -x"},
		{[]string{"-5"}, `{}`, "unknown", "-5", "unknown flag -5"},
		{[]string{"--width", "8o0"}, `{}`, "bad_value", "--width", `flag --width is an int, got "8o0"`},
		{[]string{"--width=1.5"}, `{}`, "bad_value", "--width", `is an int, got "1.5"`},
		{[]string{"-q", "fast"}, `{}`, "bad_value", "-q", `flag -q is a number, got "fast"`},
		{[]string{"--force=yes"}, `{}`, "bad_value", "--force", `flag --force is true or false, got "yes"`},
		{[]string{"--no-color=false"}, `{}`, "takes_no_value", "--no-color", "takes no value"},
		{[]string{"--width"}, `{}`, "needs_value", "--width", "flag --width needs a value"},
		{[]string{"-q"}, `{}`, "needs_value", "-q", "flag -q needs a value"},
		{[]string{}, `{min: 1}`, "too_few", "", "expected at least 1 argument, got 0"},
		{[]string{"a", "b", "c"}, `{max: 2}`, "too_many", "", "expected at most 2 arguments, got 3"},
		{[]string{"--help"}, `{}`, "help", "--help", "--help was given"},
		{[]string{"-fh"}, `{}`, "help", "-h", "-h was given"},
	}
	for _, tc := range cases {
		opts := strings.TrimSuffix(tc.opts, "}")
		if opts != "{" {
			opts += ", "
		}
		opts += "exit: false}"
		src := `try args.parse(` + argsResizeSpec + `, ` + opts + `) else (e) -> [e["kind"], e["message"], e["data"]["reason"], e["data"]["flag"]]`
		v, out, errOut, err := runArgs(t, tc.argv, src)
		if err != nil {
			t.Errorf("%q: %v", tc.argv, err)
			continue
		}
		if out != "" || errOut != "" {
			t.Errorf("%q: with exit false nothing is written, got stdout %q stderr %q", tc.argv, out, errOut)
		}
		e := v.Elems()
		if len(e) != 4 {
			t.Errorf("%q: parsed with no error: %s", tc.argv, v.Inspect())
			continue
		}
		if e[0].Str() != "argument" || e[2].Str() != tc.reason || !strings.Contains(e[1].Str(), tc.msg) || !strings.HasPrefix(e[1].Str(), "args.parse: ") {
			t.Errorf("%q: got %s, want reason %q and a message containing %q", tc.argv, v.Inspect(), tc.reason, tc.msg)
		}
		if (tc.flag == "" && !e[3].IsNil()) || (tc.flag != "" && e[3].Str() != tc.flag) {
			t.Errorf("%q: data.flag = %s, want %q", tc.argv, e[3].Inspect(), tc.flag)
		}
	}
}

func TestArgsRequired(t *testing.T) {
	t.Parallel()

	spec := `{out: {required: true, help: "where"}}`
	v, _, _, err := runArgs(t, []string{"--out", "x"}, `args.parse(`+spec+`)["out"]`)
	if err != nil || v.Str() != "x" {
		t.Errorf("given = %v, %v", v, err)
	}
	v, _, _, err = runArgs(t, []string{}, `try args.parse(`+spec+`, {exit: false}) else (e) -> [e["message"], e["data"]]`)
	if err != nil || !strings.Contains(v.Inspect(), `flag --out is required`) || !strings.Contains(v.Inspect(), `"reason":"required"`) {
		t.Errorf("absent = %s, %v", v.Inspect(), err)
	}
	// --help wins over a missing required flag: asking how to call it is not calling it.
	_, out, _, err := runArgs(t, []string{"--help"}, `args.parse(`+spec+`)`)
	if code, ok := ExitCode(err); !ok || code != 0 || !strings.Contains(out, "--out <string>") {
		t.Errorf("--help with a required flag: code %d %v, stdout %q", code, err, out)
	}
}

// By default a usage error is the CLI's: the message and the usage on stderr, then exit 2.
// --help is the usage on stdout and exit 0.
func TestArgsExitMode(t *testing.T) {
	t.Parallel()

	src := `args.parse(` + argsResizeSpec + `, {usage: "resize [flags] <file>…"})
println("not reached")`
	_, out, errOut, err := runArgs(t, []string{"--widht", "3"}, src)
	if code, ok := ExitCode(err); !ok || code != 2 {
		t.Fatalf("a usage error: err = %v, want exit 2", err)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
	if !strings.HasPrefix(errOut, "resize: unknown flag --widht (did you mean --width?)\nusage: resize [flags] <file>…\n") {
		t.Errorf("stderr = %q", errOut)
	}
	_, out, errOut, err = runArgs(t, []string{"a", "--help"}, src)
	if code, ok := ExitCode(err); !ok || code != 0 {
		t.Fatalf("--help: err = %v, want exit 0", err)
	}
	if errOut != "" || !strings.HasPrefix(out, "usage: resize [flags] <file>…\n\nflags:\n") {
		t.Errorf("--help: stdout %q, stderr %q", out, errOut)
	}
	// With no name and no usage, the line is the message alone.
	_, _, errOut, err = runArgs(t, []string{"--x"}, `args.parse({})`)
	if _, ok := ExitCode(err); !ok || !strings.HasPrefix(errOut, "unknown flag --x\nflags:\n") {
		t.Errorf("no name: %v, stderr %q", err, errOut)
	}
	// A name of its own wins over the first word of usage.
	_, _, errOut, _ = runArgs(t, []string{"--x"}, `args.parse({}, {name: "tool", usage: "resize <f>"})`)
	if !strings.HasPrefix(errOut, "tool: unknown flag --x\nusage: resize <f>\n") {
		t.Errorf("name: stderr %q", errOut)
	}
}

func TestArgsUsageText(t *testing.T) {
	t.Parallel()

	v, _, _, err := runArgs(t, nil, `args.usage(`+argsResizeSpec+`, {usage: "resize [flags] <file>…"})`)
	if err != nil {
		t.Fatal(err)
	}
	want := `usage: resize [flags] <file>…

flags:
  -h, --help              show this help
      --width <int>       target width in pixels (default 800)
  -q, --quality <number>  (default 0.85)
      --out <string>      output file
  -f, --force
  -v, --verbose
      --tag <string>      a label (repeatable)
      --dry-run
      --[no-]color        (default true)
      --name <string>     (default "anon")
`
	if v.Str() != want {
		t.Errorf("usage =\n%s\nwant\n%s", v.Str(), want)
	}
	v, _, _, err = runArgs(t, nil, `args.usage({tag: ["a"], out: {required: true}}, {name: "t"})`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v.Str(), "usage: t [flags]\n") || !strings.Contains(v.Str(), `(repeatable, default ["a"])`) || !strings.Contains(v.Str(), "--out <string>  (required)") {
		t.Errorf("usage = %q", v.Str())
	}
}

// args reads $ARGV and nothing else: unbound, it parses nothing; opts.argv replaces it.
func TestArgsSource(t *testing.T) {
	t.Parallel()

	v, _, _, err := runArgs(t, nil, `args.parse({n: 1}).json`)
	if err != nil || v.Str() != `{"n":1,"rest":[]}` {
		t.Errorf("no $ARGV = %v, %v", v, err)
	}
	v, _, _, err = runArgs(t, []string{"--n", "2"}, `args.parse({n: 1}, {argv: ["--n", "3"]})["n"]`)
	if err != nil || v.Inspect() != "3" {
		t.Errorf("opts.argv = %v, %v", v, err)
	}
	_, _, _, err = runArgs(t, nil, `$ARGV = "--n 2"; args.parse({n: 1})`)
	if err == nil || !strings.Contains(err.Error(), "the arguments are an array of strings, got string") {
		t.Errorf("a string $ARGV: %v", err)
	}
	_, _, _, err = runArgs(t, nil, `args.parse({n: 1}, {argv: ["--n", 2]})`)
	if err == nil || !strings.Contains(err.Error(), "the arguments are strings, got int") {
		t.Errorf("an int in argv: %v", err)
	}
}

// A mistake in the spec or the options is the script's, so it is an ordinary error even
// when exit is on: nobody at the command line can fix it.
func TestArgsSpecErrors(t *testing.T) {
	t.Parallel()

	cases := []struct{ src, want string }{
		{`args.parse([])`, "expects a spec dict as argument 1, got array"},
		{`args.parse({}, [])`, "expects an options dict as argument 2, got array"},
		{`args.parse({"dry-run": false})`, `flag name "dry-run" is not letters`},
		{`args.parse({"_x": false})`, `flag name "_x" is not letters`},
		{`args.parse({rest: 1})`, "'rest' is the key of the positionals"},
		{`args.parse({help: false})`, "--help is always present"},
		{`args.parse({x: {defualt: 1}})`, "unknown key \"defualt\"; did you mean 'default'?"},
		{`args.parse({x: {colour: 1}})`, "unknown key \"colour\"; the keys are default, help, short, required"},
		{`args.parse({x: {short: "xy"}})`, "short is one letter or digit"},
		{`args.parse({x: {short: "-"}})`, "short is one letter or digit"},
		{`args.parse({x: {short: "h"}})`, "-h is always --help"},
		{`args.parse({x: {short: "a"}, y: {short: "a"}})`, `flags 'x' and 'y' both have short "a"`},
		{`args.parse({x: {help: 1}})`, "help is a string"},
		{`args.parse({x: {required: 1}})`, "required is true or false"},
		{`args.parse({x: {required: true, default: "a"}})`, "is required, so it has no default"},
		{`args.parse({x: {default: {a: 1}}})`, "the default is a bool, int, float, string, array or nil, got dict"},
		{`args.parse({x: [1]})`, "its default holds strings, got int"},
		{`args.parse({}, {mni: 1})`, "unknown option \"mni\"; did you mean 'min'?"},
		{`args.parse({}, {min: -1})`, "option 'min' is an int of 0 or more"},
		{`args.parse({}, {min: 2, max: 1})`, "option 'min' (2) is more than 'max' (1)"},
		{`args.parse({}, {exit: 0})`, "option 'exit' is true or false"},
		{`args.parse({}, {usage: 1})`, "option 'usage' is a string"},
	}
	for _, tc := range cases {
		_, _, _, err := runArgs(t, []string{}, tc.src)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.src, err, tc.want)
		}
		if _, ok := ExitCode(err); ok {
			t.Errorf("%s: a spec mistake exited", tc.src)
		}
	}
}

// The default of a repeated flag is copied into the result, so a script that pushes onto
// what it got back does not change what the next parse starts from.
func TestArgsListDefaultIsCopied(t *testing.T) {
	t.Parallel()

	v, _, _, err := runArgs(t, []string{}, `d = ["a"]; o = args.parse({tag: d}); o["tag"].push("b"); [d, o["tag"]]`)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Inspect(); got != `[["a"],["a","b"]]` {
		t.Errorf("got %s", got)
	}
}

func TestArgsIsUngated(t *testing.T) {
	t.Parallel()

	v, err := New(Options{}).Eval(context.Background(), `include args; args.keys`, nil)
	if err != nil || v.Inspect() != `["parse","usage"]` {
		t.Errorf("args.keys = %v, %v", v, err)
	}
}

// A host may bind a $ARGV longer than a script could build; the arrays the result holds
// are bounded by MaxCollection like any other, and the usage text by MaxStringBytes on the
// exit path as well as in args.usage.
func TestArgsBounds(t *testing.T) {
	t.Parallel()

	elems := make([]Value, 50)
	for i := range elems {
		elems[i] = Str("x")
	}
	in := New(Options{MaxCollection: 10})
	_, err := in.Eval(context.Background(), "include args\nargs.parse({})", map[string]Value{"$ARGV": Array(elems...)})
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("50 arguments under MaxCollection 10: err = %v", err)
	}
	var errOut bytes.Buffer
	in = New(Options{MaxStringBytes: 1000, Stderr: &errOut})
	spec := `{a: {help: "` + strings.Repeat("h", 400) + `"}, b: {help: "` + strings.Repeat("h", 400) + `"}, c: {help: "` + strings.Repeat("h", 400) + `"}}`
	_, err = in.Eval(context.Background(), "include args\nargs.parse("+spec+")", map[string]Value{"$ARGV": Array(Str("--nope"))})
	if _, exited := ExitCode(err); exited || err == nil || !strings.Contains(err.Error(), "limit") || errOut.Len() != 0 {
		t.Errorf("a usage text over MaxStringBytes: err = %v, stderr %d bytes", err, errOut.Len())
	}
}
