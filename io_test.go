package mzs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// The io module is the capability an embedder is most likely to be embedding mzs to
// withhold, so these tests pin both halves of §12.13: that a host which hands over an FS
// gets a script that can read and write, and that a host which does not cannot be made
// to give one up — not by an include, not by a member, not by a path.

// memFS is a filesystem in a map, which is all a host has to provide. It records the
// names it was asked for, so the "paths are the host's business" rule can be shown rather
// than asserted: what a script writes arrives here unchanged.
type memFS struct {
	files map[string]string
	dirs  map[string][]string
	asked []string
	fail  error
}

func newMemFS(files map[string]string) *memFS {
	if files == nil {
		files = map[string]string{}
	}
	return &memFS{files: files, dirs: map[string][]string{}}
}

func (m *memFS) Open(name string) (io.ReadCloser, error) {
	m.asked = append(m.asked, name)
	if m.fail != nil {
		return nil, m.fail
	}
	s, ok := m.files[name]
	if !ok {
		return nil, fmt.Errorf("no such file")
	}
	return io.NopCloser(strings.NewReader(s)), nil
}

func (m *memFS) Create(name string) (io.WriteCloser, error) {
	m.asked = append(m.asked, name)
	if m.fail != nil {
		return nil, m.fail
	}
	m.files[name] = ""
	return &memFile{fs: m, name: name}, nil
}

func (m *memFS) Append(name string) (io.WriteCloser, error) {
	m.asked = append(m.asked, name)
	if m.fail != nil {
		return nil, m.fail
	}
	return &memFile{fs: m, name: name}, nil
}

func (m *memFS) Stat(name string) (bool, int64, bool, error) {
	m.asked = append(m.asked, name)
	if m.fail != nil {
		return false, 0, false, m.fail
	}
	if s, ok := m.files[name]; ok {
		return true, int64(len(s)), false, nil
	}
	if _, ok := m.dirs[name]; ok {
		return true, 0, true, nil
	}
	return false, 0, false, nil
}

func (m *memFS) List(dir string) ([]string, error) {
	m.asked = append(m.asked, dir)
	if m.fail != nil {
		return nil, m.fail
	}
	names, ok := m.dirs[dir]
	if !ok {
		return nil, fmt.Errorf("no such directory")
	}
	return append([]string(nil), names...), nil
}

// memFile buffers a write and commits it on Close, which is also what makes a host that
// only reports a failure from Close worth testing against.
type memFile struct {
	fs   *memFS
	name string
	buf  bytes.Buffer
}

func (f *memFile) Write(p []byte) (int, error) { return f.buf.Write(p) }

func (f *memFile) Close() error {
	f.fs.files[f.name] += f.buf.String()
	return nil
}

// ioOpts is the host that hands everything over: a filesystem, a pipe and an environment.
func ioOpts(fs FileSystem, stdin string, env map[string]string) Options {
	o := Options{Timeout: 5 * time.Second, FS: fs}
	if stdin != "" {
		o.Stdin = strings.NewReader(stdin)
	}
	if env != nil {
		o.Env = func(name string) string { return env[name] }
	}
	return o
}

func evalIO(t *testing.T, o Options, src string) (Value, error) {
	t.Helper()
	return New(o).Eval(context.Background(), "include io\n"+src, nil)
}

// mustEvalIO runs a snippet that is expected to work and returns its rendered value.
func mustEvalIO(t *testing.T, o Options, src string) string {
	t.Helper()
	v, err := evalIO(t, o, src)
	if err != nil {
		t.Fatalf("%s: %v", src, err)
	}
	return v.Str()
}

// TestIOIsGatedOnTheHost is the whole sandbox promise in one test: with the zero Options
// the name does not exist, and the diagnostic names the field to install rather than
// leaving the reader with `unknown module`.
func TestIOIsGatedOnTheHost(t *testing.T) {
	t.Parallel()

	_, err := New(Options{}).Compile("t", "include io\nio.read(\"/etc/passwd\")")
	if err == nil {
		t.Fatal("a script reached the filesystem with no host option")
	}
	for _, want := range []string{"module 'io'", "Options.FS"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v; want it to mention %q", err, want)
		}
	}

	// Nor by the other spelling: without the include there is no `io` at all, and §12.8's
	// "add the include" hint must not appear for a module the host never enabled.
	_, err = New(Options{}).Compile("t", `io.read("/etc/passwd")`)
	if err == nil {
		t.Fatal("a bare member reached the filesystem with no host option")
	}
	if strings.Contains(err.Error(), "add `include io`") {
		t.Fatalf("error = %v; want it not to promise an include that would also fail", err)
	}
}

// TestIOReadWriteAppend is the round trip a script exists to do.
func TestIOReadWriteAppend(t *testing.T) {
	t.Parallel()
	fs := newMemFS(map[string]string{"/data/in.txt": "привет\n"})
	o := ioOpts(fs, "", nil)

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"read", `io.read("/data/in.txt").trim`, "привет"},
		{"write returns the byte count", `io.write("/data/out.txt", "привет")`, "12"},
		{"append returns the byte count", `io.append("/data/out.txt", "!")`, "1"},
		{"the file is what was written", `io.write("/tmp/x", "a"); io.append("/tmp/x", "b"); io.read("/tmp/x")`, "ab"},
		{"exists", `io.exists("/data/in.txt")`, "true"},
		{"exists is false, not an error", `io.exists("/data/nope.txt")`, "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mustEvalIO(t, o, tt.src); got != tt.want {
				t.Fatalf("%s = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestIOWriteTruncates pins the difference between the two writers: write replaces,
// append adds. It is the one thing a log-writing script must be able to rely on.
func TestIOWriteTruncates(t *testing.T) {
	t.Parallel()
	fs := newMemFS(map[string]string{"/log": "old\n"})
	got := mustEvalIO(t, ioOpts(fs, "", nil), `io.write("/log", "new\n"); io.read("/log")`)
	if got != "new\n" {
		t.Fatalf("read = %q; want write to have truncated", got)
	}
}

// TestIOPathsReachTheHostUnchanged pins §12.13's division of labour: the module does not
// clean, join or judge a path, so a host that means to confine a script sees exactly what
// the script asked for and can refuse it.
func TestIOPathsReachTheHostUnchanged(t *testing.T) {
	t.Parallel()
	fs := newMemFS(nil)
	_, _ = evalIO(t, ioOpts(fs, "", nil), `try io.read("../../etc/shadow") else ""`)
	if len(fs.asked) != 1 || fs.asked[0] != "../../etc/shadow" {
		t.Fatalf("the host was asked for %q; want the path the script wrote, verbatim", fs.asked)
	}
}

// TestIOFailuresAreCatchable pins that a missing file is an ordinary error (§8.11): it is
// the outside world saying no, not the script breaking its own rules, so `try … else` is
// the answer and the Run survives.
func TestIOFailuresAreCatchable(t *testing.T) {
	t.Parallel()
	o := ioOpts(newMemFS(nil), "", nil)

	if got := mustEvalIO(t, o, `try io.read("/nope") else "default"`); got != "default" {
		t.Fatalf("got %q; want the fallback", got)
	}
	if got := mustEvalIO(t, o, `try io.ls("/nope") else []`); got != "[]" {
		t.Fatalf("got %q; want the fallback", got)
	}

	// Uncaught, it is still an error the host can read, with the path in it.
	_, err := evalIO(t, o, `io.read("/nope")`)
	if err == nil || !strings.Contains(err.Error(), `io.read "/nope"`) {
		t.Fatalf("error = %v; want it to name the file", err)
	}

	// And its kind says which module failed, so a handler can tell a missing file from a
	// bad argument without reading the message (§13.5).
	if got := mustEvalIO(t, o, `try io.read("/nope") else (e) -> e["kind"]`); got != "io" {
		t.Errorf("kind = %q; want %q", got, "io")
	}
}

// TestIOStatFailureIsNotFalse pins the other half of io.exists: a filesystem that cannot
// answer is a failure, and folding that into `false` would tell a script that a file it
// has no permission to see is not there.
func TestIOStatFailureIsNotFalse(t *testing.T) {
	t.Parallel()
	fs := newMemFS(nil)
	fs.fail = fmt.Errorf("permission denied")
	_, err := evalIO(t, ioOpts(fs, "", nil), `io.exists("/root/x")`)
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("error = %v; want the host's failure to surface", err)
	}
}

// TestIOLsIsSorted pins §8.13 for the one member whose underlying order is the
// filesystem's: two runs of one script print the same list.
func TestIOLsIsSorted(t *testing.T) {
	t.Parallel()
	fs := newMemFS(nil)
	fs.dirs["/d"] = []string{"c.mzs", "a.mzs", "b.mzs"}
	fs.dirs["."] = []string{"z", "y"}

	if got := mustEvalIO(t, ioOpts(fs, "", nil), `io.ls("/d").join(",")`); got != "a.mzs,b.mzs,c.mzs" {
		t.Fatalf("io.ls = %q; want it sorted", got)
	}
	if got := mustEvalIO(t, ioOpts(fs, "", nil), `io.ls.join(",")`); got != "y,z" {
		t.Fatalf(`io.ls = %q; want the default "." listing`, got)
	}
}

// TestIOStdin covers the reader end: the text, the lines, and the fact that both are the
// same read.
func TestIOStdin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stdin string
		src   string
		want  string
	}{
		{"the whole of it", "привет\nмир\n", "io.stdin", "привет\nмир\n"},
		{"lines drop the terminator", "a\nb\n", "io.lines.len", "2"},
		{"a line in the middle may be empty", "a\n\nb\n", `io.lines.join("|")`, "a||b"},
		{"CRLF reads like LF", "a\r\nb\r\n", `io.lines.join("|")`, "a|b"},
		{"no trailing newline is still a line", "a\nb", "io.lines.len", "2"},
		{"read twice is one read", "x\n", "io.stdin + io.stdin", "x\nx\n"},
		{"stdin then lines", "a\nb\n", "io.stdin.len.str + io.lines.len.str", "42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := ioOpts(newMemFS(nil), tt.stdin, nil)
			if got := mustEvalIO(t, o, tt.src); got != tt.want {
				t.Fatalf("%s = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestIOStdinWithoutAReader pins that a pipe nobody connected is empty rather than fatal:
// the same script runs in a terminal and under `cat x |`.
func TestIOStdinWithoutAReader(t *testing.T) {
	t.Parallel()
	o := ioOpts(newMemFS(nil), "", nil)

	if got := mustEvalIO(t, o, `io.stdin`); got != "" {
		t.Fatalf("io.stdin = %q; want the empty string", got)
	}
	if got := mustEvalIO(t, o, `io.lines.len`); got != "0" {
		t.Fatalf("io.lines.len = %q; want 0", got)
	}
}

// TestIOStdinIsReadOncePerRun pins the cache, which is not an optimisation: a reader
// gives its bytes away once, so a second read that reached the reader would answer "".
// Two Runs are two reads of two readers, like every other per-Run resource (§10).
func TestIOStdinIsReadOncePerRun(t *testing.T) {
	t.Parallel()
	r := &countingReader{Reader: strings.NewReader("a\nb\n")}
	in := New(Options{Timeout: 5 * time.Second, FS: newMemFS(nil), Stdin: r})

	v, err := in.Eval(context.Background(), "include io\nio.lines.len.str + io.stdin.len.str + io.lines.len.str", nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.Str() != "242" {
		t.Fatalf("got %q; want 242 — three members, one read", v.Str())
	}
	if r.reads == 0 {
		t.Fatal("the reader was never read")
	}
	if got := r.eof; got != 1 {
		t.Fatalf("the reader was drained %d times; want exactly 1", got)
	}
}

// TestIOStdinIsSharedWithTasks pins that the cache lives on the half of the Run tasks
// share (§8.14): a task and the main program see one stdin, not one each.
func TestIOStdinIsSharedWithTasks(t *testing.T) {
	t.Parallel()
	o := ioOpts(newMemFS(nil), "данные\n", nil)
	src := `
		async fn peek() { io.stdin.trim }
		t = peek()
		io.stdin.trim + "/" + t.await
	`
	if got := mustEvalIO(t, o, src); got != "данные/данные" {
		t.Fatalf("got %q; want both halves of the Run to see one stdin", got)
	}
}

// TestIOEnv covers the environment, including the two shapes of "not set".
func TestIOEnv(t *testing.T) {
	t.Parallel()
	env := map[string]string{"HOME": "/home/ivan", "EMPTY": ""}

	tests := []struct {
		name string
		env  map[string]string
		src  string
		want string
	}{
		{"a set name", env, `io.env("HOME")`, "/home/ivan"},
		{"an unset name is nil", env, `io.env("NOPE") == nil`, "true"},
		{"an unset name takes the default", env, `io.env("NOPE", "/tmp")`, "/tmp"},
		{"an empty value counts as unset", env, `io.env("EMPTY", "/tmp")`, "/tmp"},
		{"no host environment at all", nil, `io.env("HOME", "/tmp")`, "/tmp"},
		{"no host environment, no default", nil, `io.env("HOME") == nil`, "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := ioOpts(newMemFS(nil), "", tt.env)
			if got := mustEvalIO(t, o, tt.src); got != tt.want {
				t.Fatalf("%s = %q; want %q", tt.src, got, tt.want)
			}
		})
	}
}

// TestIOLimits pins §14.2 over the module: a file bigger than MaxStringBytes is refused
// rather than truncated, and the reader stops at the limit instead of buffering the lot.
func TestIOLimits(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("я", 100) // 200 bytes
	o := ioOpts(newMemFS(map[string]string{"/big": big}), big, nil)
	o.MaxStringBytes = 64

	_, err := evalIO(t, o, `io.read("/big")`)
	if err == nil || !strings.Contains(err.Error(), "exceeds the 64 byte limit") {
		t.Fatalf("io.read error = %v; want the byte limit", err)
	}
	_, err = evalIO(t, o, `io.stdin`)
	if err == nil || !strings.Contains(err.Error(), "exceeds the 64 byte limit") {
		t.Fatalf("io.stdin error = %v; want the byte limit", err)
	}
	// It is the outside world saying "too big", so a script may decide what to do about
	// it — the same choice §12.11 gives an oversized HTTP response.
	if got := mustEvalIO(t, o, `try io.read("/big") else "too big"`); got != "too big" {
		t.Fatalf("got %q; want the fallback", got)
	}
}

// TestIOArgumentsAreChecked pins §9.1 across the module boundary: there is no coercion
// mode, so a number where a path belongs is a type error and not a filename.
func TestIOArgumentsAreChecked(t *testing.T) {
	t.Parallel()
	o := ioOpts(newMemFS(nil), "", nil)

	for _, src := range []string{`io.read(1)`, `io.write("/x", 1)`, `io.env(1)`} {
		if _, err := evalIO(t, o, src); err == nil {
			t.Fatalf("%s: no error; want a type error", src)
		}
	}
}

// TestIOIsNotCallable pins that io is a module like any other (§12.8): the name is a
// namespace, never a function, and the diagnostic says so at compile time.
func TestIOIsNotCallable(t *testing.T) {
	t.Parallel()
	_, err := evalIO(t, ioOpts(newMemFS(nil), "", nil), `io("/etc/hostname")`)
	if err == nil || !strings.Contains(err.Error(), "'io' is a module, not a function") {
		t.Fatalf("error = %v; want the module diagnostic", err)
	}
}

// TestIOUnregister pins the escape hatch a host has once it *has* installed an FS: the
// name can still be taken away, exactly as http can (§14.3).
func TestIOUnregister(t *testing.T) {
	t.Parallel()
	in := New(ioOpts(newMemFS(nil), "", nil))
	in.Unregister("io")
	if _, err := in.Compile("t", "include io"); err == nil {
		t.Fatal("Unregister did not take the module away")
	}
}

// countingReader reports how many times it was read to exhaustion, which is what
// "once per Run" is measured with.
type countingReader struct {
	io.Reader
	reads int
	eof   int
}

func (r *countingReader) Read(p []byte) (int, error) {
	r.reads++
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		r.eof++
	}
	return n, err
}
