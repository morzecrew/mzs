package mzs

import (
	"sort"
	"strconv"
)

// The step profile of a Run (RFC 0019 §4): where the interpreter's steps went, by
// function. Steps rather than nanoseconds, because the step counter is what the budget
// charges (§14.1) and it is deterministic: a run that takes the same path through the
// same input gives the same profile on any machine (D15), so two profiles can be diffed
// and one can be checked in CI.
//
// The Run keeps a tree of the call paths it took. Entering a function moves to that
// function's child of the current node, leaving moves back, and every step is added to
// the node the Run stands on. The report folds the tree into functions, with their own
// steps and the steps of everything they called, and into the stacks a flame graph is
// drawn from.

// Profile is the step profile of one Run, read from Result.Profile when Options.Profile
// is set.
type Profile struct {
	// Steps is every step the Run took, the sum of Funcs' Self.
	Steps int64
	// Funcs has one entry per function that ran, most steps of its own first.
	Funcs []ProfileFunc
	// Stacks has one entry per call path that took a step of its own, outermost frame
	// first: the folded-stack form flame-graph tools read.
	Stacks []ProfileStack
}

// ProfileFunc is what one function cost.
type ProfileFunc struct {
	// Name is the function's name: `{}` for a closure, a builtin's or a method's name,
	// `(top level)` for the program's own statements and `(include)` for a module's.
	Name string
	// File and Line are where a script function's body starts, and where a top-level
	// program or module is; both are empty for a builtin, a method or a host function.
	// Col is the body's column, which tells apart closures that share a line.
	File      string
	Line, Col int
	// Self is the steps taken in the function's own statements; Total adds every step
	// of what it called. A recursive call is counted once in Total.
	Self, Total int64
	// Calls is how many times the function was entered.
	Calls int64
}

// Label is the function's name with where it is defined, the way the CLI prints it and
// the folded stacks name a frame: `score rank.mzs:14`, or the bare name of a builtin. A
// closure, which has no name to tell it from another on its line, adds the column:
// `{} rank.mzs:14:20`.
func (f ProfileFunc) Label() string {
	if f.File == "" {
		return f.Name
	}
	l := f.Name + " " + f.File + ":" + strconv.Itoa(f.Line)
	if f.Name == "{}" {
		l += ":" + strconv.Itoa(f.Col)
	}
	return l
}

// ProfileStack is one call path and the steps taken at its innermost frame.
type ProfileStack struct {
	Frames []string
	Steps  int64
}

// profKey is a function's identity in the profile.
type profKey struct {
	name, file string
	line, col  int
}

type profNode struct {
	key      profKey
	parent   *profNode
	children map[profKey]*profNode
	self     int64
	calls    int64
	depth    int // frames between this node and the root
}

// profMaxDepth bounds a call path in the profile, in frames. MaxDepth bounds one
// stack, but not a chain of tasks each started by the last, and a deep path costs its
// depth once for every stack under it when the report is built. Calls past the bound
// are counted under one `(deeper)` frame, the path's last.
const profMaxDepth = 256

// profDeeper names the frame that stands for every call below profMaxDepth.
var profDeeper = profKey{name: "(deeper)"}

func (n *profNode) child(k profKey) *profNode {
	if c := n.children[k]; c != nil {
		return c
	}
	if n.children == nil {
		n.children = map[profKey]*profNode{}
	}
	c := &profNode{key: k, parent: n, depth: n.depth + 1}
	n.children[k] = c
	return c
}

// profEnter moves the Run into a call of the function k. It is a no-op when the Run
// does not profile.
func (rs *runState) profEnter(k profKey) {
	if rs.pnode == nil {
		return
	}
	if rs.pdeep > 0 {
		// Past the bound every call is one more entry of the (deeper) node.
		rs.pdeep++
		rs.pnode.calls++
		return
	}
	if rs.pnode.depth+2 >= profMaxDepth {
		// The (deeper) node is the last of profMaxDepth frames, the root counted.
		k, rs.pdeep = profDeeper, 1
	}
	rs.pnode = rs.pnode.child(k)
	rs.pnode.calls++
}

// profLeave moves back out of the call profEnter made.
func (rs *runState) profLeave() {
	if rs.pdeep > 1 {
		rs.pdeep--
		return
	}
	rs.pdeep = 0
	if rs.pnode != nil && rs.pnode.parent != nil {
		rs.pnode = rs.pnode.parent
	}
}

// funcKey is the profile identity of a script function or closure: its name and where
// its body starts. A host function has no place in a script.
func funcKey(f *Func) profKey {
	k := profKey{name: f.Name}
	if k.name == "" {
		k.name = "{}"
	}
	if f.Host == nil && f.Body != nil {
		k.file, k.line, k.col = f.file, f.Body.Start.Line, f.Body.Start.Col
	}
	return k
}

// report folds the tree under root into a Profile.
func (root *profNode) report() *Profile {
	p := &Profile{}
	funcs := map[profKey]*ProfileFunc{}
	order := []profKey{}
	fn := func(k profKey) *ProfileFunc {
		f := funcs[k]
		if f == nil {
			f = &ProfileFunc{Name: k.name, File: k.file, Line: k.line, Col: k.col}
			funcs[k] = f
			order = append(order, k)
		}
		return f
	}
	open := map[profKey]int{} // how many times each function is on the path being walked
	var path []string
	// walk answers the steps of n's whole subtree, adding each node's steps to its
	// function and each subtree's to the function at its root, once per path even when
	// the function recurses.
	var walk func(n *profNode) int64
	walk = func(n *profNode) int64 {
		f := fn(n.key)
		f.Self += n.self
		f.Calls += n.calls
		p.Steps += n.self
		path = append(path, f.Label())
		if n.self > 0 {
			p.Stacks = append(p.Stacks, ProfileStack{Frames: append([]string(nil), path...), Steps: n.self})
		}
		open[n.key]++
		total := n.self
		keys := make([]profKey, 0, len(n.children))
		for k := range n.children {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keyLess(keys[i], keys[j]) })
		for _, k := range keys {
			total += walk(n.children[k])
		}
		open[n.key]--
		if open[n.key] == 0 {
			f.Total += total
		}
		path = path[:len(path)-1]
		return total
	}
	walk(root)
	for _, k := range order {
		p.Funcs = append(p.Funcs, *funcs[k])
	}
	sort.SliceStable(p.Funcs, func(i, j int) bool {
		a, b := p.Funcs[i], p.Funcs[j]
		if a.Self != b.Self {
			return a.Self > b.Self
		}
		return a.Total > b.Total
	})
	return p
}

func keyLess(a, b profKey) bool {
	if a.file != b.file {
		return a.file < b.file
	}
	if a.line != b.line {
		return a.line < b.line
	}
	if a.col != b.col {
		return a.col < b.col
	}
	return a.name < b.name
}
