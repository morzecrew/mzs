package mzs

import (
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"mzs/internal/rx"
)

// The memory budget of RFC 0011 (§14.2). A Run may hold at most Options.MaxMemory bytes
// live in script values. The bound is enforced by accounting, not by asking the Go
// runtime — the process is shared, and the answer has to be the same on every run of the
// same program (D15).
//
// The scheme has two halves.
//
//   - Charging is cheap and deliberately pessimistic. Every interpreter step is charged
//     a fixed allowance, because the stdlib already charges steps in proportion to the
//     work it does (an element per step, 64 bytes of string per step, …), and the work
//     is what allocates. The operations that can build something large in one go are
//     charged their exact size where they already check it: checkString and
//     checkCollection run before the string or the collection exists.
//   - Release is a reachability walk. When the charged total crosses the threshold, the
//     Run measures what its roots can actually reach and replaces the total with that.
//     Garbage is never charged twice, and a program that allocates and drops runs
//     forever under the budget.
//
// The walk happens only when the total crosses a threshold set with slack: after a walk
// that finds L live bytes, the next one waits until another max(M−L, M/8) has been
// charged. So the live set may overshoot M by up to an eighth before the walk that ends
// the Run, and every walk is paid for by at least M/8 charged bytes.

// DefaultMaxMemory is the live-memory budget a Run gets when Options.MaxMemory is 0.
const DefaultMaxMemory int64 = 64 << 20

const (
	// memPerStep is what one interpreter step is charged. It is an allowance, not a
	// measurement: most steps allocate nothing, and the walk corrects the total.
	memPerStep = 64

	valueBytes  = int64(unsafe.Sizeof(Value{}))
	stringBytes = int64(unsafe.Sizeof(""))

	// The fixed costs below are estimates of the Go objects behind each kind. They are
	// stated as estimates in SPEC §14.2: what matters is that they are never zero, so a
	// million small values cannot be free.
	sliceHeader = 24
	envHeader   = 64
	dictHeader  = 96
	dictEntry   = 48 // one slot of the index map, on top of the key and value slots
	funcBytes   = 128
	taskBytes   = 128
	seqBytes    = 64
	scalarBox   = 32 // a time, a range
	regexPerRun = 64 // the compiled program, per byte of pattern
)

// memRoot is one thing the walk starts from: a slice of values, or a scope or a growing
// slice behind ref (an *Env or a *[]Value). A single held value goes on the held stack
// instead, so the common push copies forty bytes, not a whole Value more.
type memRoot struct {
	vals []Value // the arguments of a call in progress, or a result being built in place
	ref  any     // *Env: a scope that is executing; *[]Value: a result a row appends to
}

// memMark is a position on both root stacks.
type memMark struct{ roots, held int }

// memOn reports whether this Run enforces a budget at all.
func (rs *runState) memOn() bool { return rs.opts.MaxMemory > 0 }

// rootMark and rootReset bracket a region that pushes roots. Every push is undone by
// resetting to the mark taken before it, on the error path as well as the normal one.
func (rs *runState) rootMark() memMark { return memMark{len(rs.roots), len(rs.held)} }

func (rs *runState) rootReset(m memMark) {
	if m.roots < len(rs.roots) {
		clear(rs.roots[m.roots:])
		rs.roots = rs.roots[:m.roots]
	}
	if m.held < len(rs.held) {
		clear(rs.held[m.held:])
		rs.held = rs.held[:m.held]
	}
}

// pushEnv roots a scope and, through its parents, every scope it can see.
func (rs *runState) pushEnv(e *Env) {
	if rs.memOn() && e != nil {
		rs.roots = append(rs.roots, memRoot{ref: e})
	}
}

// pushVals roots a slice of values. The root shares the slice's backing array, so a
// result filled in place (out[i] = v) is visible to the walk as it is written; a slice
// that is appended to past its capacity needs holdGrowing instead.
func (rs *runState) pushVals(vs []Value) {
	if rs.memOn() && len(vs) > 0 {
		rs.roots = append(rs.roots, memRoot{vals: vs})
	}
}

// holdGrowing roots a slice that is appended to: the root reads it through the pointer,
// so a reallocation is followed. The slice's spare capacity is charged too, since it is
// memory the row holds.
func (rs *runState) holdGrowing(p *[]Value) {
	if rs.memOn() {
		rs.roots = append(rs.roots, memRoot{ref: p})
	}
}

// hold roots one value. Scalars are skipped: they own no memory.
func (rs *runState) hold(v Value) {
	if rs.memOn() && heapBacked(v) {
		rs.held = append(rs.held, v)
	}
}

// heapBacked reports whether v can hold memory worth rooting. Scalars never allocate,
// and the evaluator's hottest paths only pay for a hold when it matters.
func heapBacked(v Value) bool {
	switch v.k {
	case KNil, KBool, KInt, KFloat:
		return false
	}
	return true
}

// charge adds n bytes to the Run's total and walks when the total crosses the
// threshold. It is called before the allocation it pays for, so a single charge larger
// than the slack walks first and is refused without allocating.
func (rs *runState) charge(n int64) error {
	if !rs.memOn() || n <= 0 {
		return nil
	}
	sh := rs.sh
	sh.mem += n
	if sh.mem <= sh.memNext {
		return nil
	}
	return rs.collect(n)
}

// allocString checks a string about to be built against MaxStringBytes and charges it.
// It is for the operations that make a new string of that size in one go; a check of a
// running total (a builder that grows) stays checkString, and the step allowance pays
// for it.
func (rs *runState) allocString(n int) error {
	if err := rs.checkString(n); err != nil {
		return err
	}
	return rs.charge(int64(n))
}

// allocCollection is allocString for n new elements: it checks the total the collection
// will have and charges only the elements being added.
func (rs *runState) allocCollection(total, added int) error {
	if err := rs.checkCollection(total); err != nil {
		return err
	}
	return rs.charge(int64(added) * valueBytes)
}

// collect is the walk. pending is the part of the total that pays for an allocation
// that has not happened yet; it is added to what the walk finds, because the walk cannot
// see it.
func (rs *runState) collect(pending int64) error {
	sh := rs.sh
	max := rs.opts.MaxMemory
	live := rs.measure()
	total := live + pending
	if total > sh.memPeak {
		sh.memPeak = total
	}
	if total > max {
		return rs.limitErr(ErrMemory, "memory budget exceeded (%s live, the limit is %s)",
			byteSize(total), byteSize(max))
	}
	sh.mem = total
	slack := max - total
	if slack < max/8 {
		slack = max / 8
	}
	sh.memNext = total + slack
	return nil
}

// stepCharge charges the allowance for the steps the Run has taken since the last
// check. It runs at the sampling interval (§14.1) rather than on every step, so the
// common path of step costs nothing more than it did.
func (rs *runState) stepCharge() error {
	if !rs.memOn() {
		return nil
	}
	sh := rs.sh
	n := sh.steps - sh.memSteps
	sh.memSteps = sh.steps
	if n <= 0 {
		return nil
	}
	sh.mem += n * memPerStep
	if sh.mem <= sh.memNext {
		return nil
	}
	return rs.collect(0)
}

// startMemory arms the budget for a new Run. It allocates nothing: a condition that
// never comes near the budget must cost what it cost before the budget existed (A4), so
// the root stack comes from a pool and the host's baseline is measured by the first
// walk, not here.
func (rs *runState) startMemory(hostVars map[string]Value) {
	if !rs.memOn() {
		return
	}
	sh := rs.sh
	sh.main = rs
	sh.hostVars = hostVars
	sh.memNext = rs.opts.MaxMemory
	sh.memSteps = sh.steps
	if b, ok := rootPool.Get().(*rootStacks); ok {
		sh.rootBox = b
		rs.roots, rs.held = b.roots[:0], b.held[:0]
	}
}

// stopMemory hands the root stack back to the pool at the end of the Run.
func (rs *runState) stopMemory() {
	box := rs.sh.rootBox
	if box == nil {
		return
	}
	// Every push was popped by its own reset, which cleared it, so the stacks go back
	// empty and hold nothing the pool would keep alive. A Run that ended on a panic is
	// the exception, and is cleared here.
	clear(rs.roots)
	clear(rs.held)
	box.roots, box.held = rs.roots[:0], rs.held[:0]
	rs.roots, rs.held, rs.sh.rootBox = nil, nil, nil
	rootPool.Put(box)
}

// rootStacks is what the pool keeps between Runs: the two stacks, already grown.
type rootStacks struct {
	roots []memRoot
	held  []Value
}

var rootPool = sync.Pool{New: func() any {
	return &rootStacks{roots: make([]memRoot, 0, 32), held: make([]Value, 0, 16)}
}}

// measure sums what the Run can reach: every executing scope, every call in progress
// and every value held in a Go variable, across the main program and every live task,
// plus the globals and the loaded modules. What the host bound is subtracted: the
// first walk measures it, before anything the script did can be mistaken for it.
func (rs *runState) measure() int64 {
	sh := rs.sh
	if !sh.memBaseDone {
		b := newMemWalker()
		for _, v := range rs.in.globals {
			b.value(v)
		}
		for _, v := range sh.hostVars {
			b.value(v)
		}
		sh.memBase, sh.memBaseDone = b.total, true
	}
	w := newMemWalker()
	if sh.main != nil {
		w.state(sh.main)
	}
	for _, s := range sh.states {
		w.state(s)
	}
	for _, v := range rs.globals {
		w.value(v)
	}
	for _, v := range rs.modules {
		w.value(v)
	}
	n := w.total - sh.memBase
	if n < 0 {
		n = 0
	}
	return n
}

// trackState adds a task's runState to the set the walk visits, and the returned
// function removes it. The main program is not in the list: it is sh.main.
func (sh *runShared) trackState(s *runState) func() {
	sh.states = append(sh.states, s)
	return func() {
		for i, x := range sh.states {
			if x == s {
				sh.states = append(sh.states[:i], sh.states[i+1:]...)
				return
			}
		}
	}
}

// ownStr is how an operation that cuts a string hands back its piece (RFC 0011). A Go
// substring shares its source's bytes, and the walk can only count the bytes a string
// shows: a one-byte piece of a megabyte would be charged one byte and keep the megabyte
// alive. So a cut copies, unless it is the whole source — what is charged is then what is
// held, and there is no threshold to game.
func ownStr(sub, src string) string {
	if len(sub) == len(src) || sub == "" {
		return sub
	}
	return strings.Clone(sub)
}

// ownStrs is ownStr over the pieces of one source, in place.
func ownStrs(parts []string, src string) []string {
	for i, p := range parts {
		parts[i] = ownStr(p, src)
	}
	return parts
}

// memWalker sums the bytes behind a graph of values, each object once. Strings are
// identified by their data pointer, everything else by its payload pointer, so a cycle
// (`d["self"] = d`) terminates and an alias is not counted twice.
type memWalker struct {
	seen  map[unsafe.Pointer]struct{}
	strs  map[*byte]int
	total int64

	// The walk is iterative: a visit only pushes what it found onto these two stacks, and
	// drain takes it from there. A chain built with `a = [a]` in a loop is as deep as the
	// budget allows, and a recursive walk over it would take the Go stack — and with it
	// the host — down before the budget could answer.
	slices [][]Value
	envs   []*Env
}

func newMemWalker() *memWalker {
	return &memWalker{seen: map[unsafe.Pointer]struct{}{}, strs: map[*byte]int{}}
}

func (w *memWalker) first(p unsafe.Pointer) bool {
	if p == nil {
		return false
	}
	if _, ok := w.seen[p]; ok {
		return false
	}
	w.seen[p] = struct{}{}
	return true
}

func (w *memWalker) state(s *runState) {
	for _, r := range s.roots {
		switch p := r.ref.(type) {
		case *Env:
			w.envs = append(w.envs, p)
		case *[]Value:
			w.total += int64(cap(*p)) * valueBytes
			w.push(*p)
		default:
			w.push(r.vals)
		}
	}
	w.push(s.held)
	w.drain()
}

// values weighs a slice of values and everything they reach.
func (w *memWalker) values(vs []Value) {
	w.push(vs)
	w.drain()
}

// value weighs one value and everything it reaches.
func (w *memWalker) value(v Value) {
	w.visit(v)
	w.drain()
}

func (w *memWalker) push(vs []Value) {
	if len(vs) > 0 {
		w.slices = append(w.slices, vs)
	}
}

// drain works the two stacks until both are empty.
func (w *memWalker) drain() {
	for len(w.slices) > 0 || len(w.envs) > 0 {
		if n := len(w.envs); n > 0 {
			e := w.envs[n-1]
			w.envs = w.envs[:n-1]
			w.env(e)
			continue
		}
		n := len(w.slices)
		vs := w.slices[n-1]
		w.slices = w.slices[:n-1]
		for _, v := range vs {
			w.visit(v)
		}
	}
}

func (w *memWalker) str(s string) {
	if len(s) == 0 {
		return
	}
	p := unsafe.StringData(s)
	if n, ok := w.strs[p]; ok && n >= len(s) {
		return
	} else if ok {
		w.total -= int64(n)
	}
	w.strs[p] = len(s)
	w.total += int64(len(s))
}

// env weighs a scope and its parents, which is a loop, and pushes their slots.
func (w *memWalker) env(e *Env) {
	for ; e != nil; e = e.parent {
		if !w.first(unsafe.Pointer(e)) {
			return
		}
		w.total += envHeader + int64(cap(e.slots))*valueBytes + int64(cap(e.names))*stringBytes
		w.push(e.slots)
	}
}

// visit weighs one value's own payload and pushes what it references. It never calls
// itself, so its depth is constant whatever the shape of the data.
func (w *memWalker) visit(v Value) {
	switch v.k {
	case KNil, KBool, KInt, KFloat:
		return
	case KString:
		w.str(v.s)
	case KArray:
		switch p := v.p.(type) {
		case *[]Value:
			if w.first(unsafe.Pointer(p)) {
				w.total += sliceHeader + int64(cap(*p))*valueBytes
				w.push(*p)
			}
		case *namedArray:
			if w.first(unsafe.Pointer(p)) {
				w.total += sliceHeader + int64(cap(p.elems))*valueBytes + int64(len(p.names))*(stringBytes+dictEntry)
				w.push(p.elems)
			}
		}
	case KDict:
		if d := v.odict(); d != nil && w.first(unsafe.Pointer(d)) {
			w.total += dictHeader + int64(cap(d.keys)+cap(d.vals))*valueBytes + int64(len(d.idx))*dictEntry
			w.push(d.keys)
			w.push(d.vals)
		}
	case KFunc:
		if f := v.fn(); f != nil && w.first(unsafe.Pointer(f)) {
			w.total += funcBytes
			if f.Env != nil {
				w.envs = append(w.envs, f.Env)
			}
		}
	case KTask:
		if t, ok := v.p.(*task); ok && w.first(unsafe.Pointer(t)) {
			w.total += taskBytes
			if heapBacked(t.val) {
				w.push([]Value{t.val})
			}
		}
	case KSeq:
		if s, ok := v.p.(*Seq); ok && w.first(unsafe.Pointer(s)) {
			w.total += seqBytes
			w.push(s.refs)
		}
	case KRegex:
		if r, ok := v.p.(*rx.Regexp); ok && w.first(unsafe.Pointer(r)) {
			w.total += int64(len(r.String())) * regexPerRun
		}
	default:
		w.total += scalarBox
	}
}

// byteSize renders a byte count the way the diagnostic states the budget: exact for a
// whole number of MiB or GiB, two decimals otherwise, so a total just past the limit
// does not print as the limit itself.
func byteSize(n int64) string {
	const (
		kib = 1 << 10
		mib = 1 << 20
		gib = 1 << 30
	)
	one := func(x float64) string { return strconv.FormatFloat(x, 'f', 2, 64) }
	switch {
	case n >= gib && n%gib == 0:
		return strconv.FormatInt(n/gib, 10) + " GiB"
	case n >= mib && n%mib == 0:
		return strconv.FormatInt(n/mib, 10) + " MiB"
	case n >= mib:
		return one(float64(n)/mib) + " MiB"
	case n >= kib:
		return one(float64(n)/kib) + " KiB"
	}
	return strconv.FormatInt(n, 10) + " B"
}
