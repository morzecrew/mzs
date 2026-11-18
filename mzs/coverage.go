package mzs

import (
	"crypto/sha256"
	"sort"
	"sync"

	"mzs/internal/ast"
	"mzs/internal/token"
)

// Coverage records which statements and branches of a host's programs ran (RFC 0019 §3).
// A host passes one in as Options.Coverage, runs, and reads it with Files afterwards;
// the counts of every Run that used it add up. It is safe to share between Runs and
// between Interps.
//
// The unit is the statement: every statement of a statement list — a file, a block, a
// function body, a `( … )` group, a `match` arm. A branch is each side of an `if`
// (whether it has an `else` or not), of a ternary and of `&&`, `||` and `??`, and each
// arm of a `match`. A file is registered the first time a Run starts it, with every
// statement and branch at zero, so what never ran is counted too.
//
// A file is the pair of its name and its source text. The same text run again under
// the same name adds to its counts, however many times it was compiled; different text
// under one name — every Interp.Eval runs as "eval" — is a file of its own.
type Coverage struct {
	mu    sync.Mutex
	files map[fileKey]*fileCover
	// slots maps each statement and branch node of every registered tree to its
	// counter. A hit is counted by node, never by position, so two texts that share a
	// name and a line number cannot count for each other.
	slots map[slotKey]*int
}

// NewCoverage returns an empty Coverage.
func NewCoverage() *Coverage { return &Coverage{} }

type fileKey struct {
	name string
	src  [sha256.Size]byte
}

type slotKey struct {
	node ast.Node
	arm  int // -1 for a statement
}

type fileCover struct {
	name     string
	stmts    map[posKey]*int
	branches map[branchKey]*int
}

type posKey struct{ line, col int }

// branchKey names one branch: the node it belongs to, by kind and position — a ternary
// and the `&&` that is its condition start at the same place — and which side it is.
type branchKey struct {
	pos  posKey
	kind byte
	arm  int
}

// The kinds of a branchKey.
const (
	branchIf      = 'i'
	branchTernary = 't'
	branchLogical = 'l'
	branchMatch   = 'm'
)

func keyOf(p token.Pos) posKey { return posKey{p.Line, p.Col} }

// register records every statement and branch of tree, compiled from src, under file
// with a count of zero, keeping counts that are already there: a second Run of the same
// text adds to the first.
func (c *Coverage) register(file, src string, tree *ast.Program) {
	if tree == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.files == nil {
		c.files = map[fileKey]*fileCover{}
		c.slots = map[slotKey]*int{}
	}
	key := fileKey{file, sha256.Sum256([]byte(src))}
	fc := c.files[key]
	if fc == nil {
		fc = &fileCover{name: file, stmts: map[posKey]*int{}, branches: map[branchKey]*int{}}
		c.files[key] = fc
	}
	stmts := func(list []ast.Stmt) {
		for _, s := range list {
			if s == nil {
				continue
			}
			k := keyOf(s.Pos())
			slot := fc.stmts[k]
			if slot == nil {
				slot = new(int)
				fc.stmts[k] = slot
			}
			c.slots[slotKey{s, -1}] = slot
		}
	}
	branch := func(n ast.Node, p token.Pos, kind byte, arms int) {
		for i := range arms {
			k := branchKey{keyOf(p), kind, i}
			slot := fc.branches[k]
			if slot == nil {
				slot = new(int)
				fc.branches[k] = slot
			}
			c.slots[slotKey{n, i}] = slot
		}
	}
	ast.Walk(tree, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Program:
			stmts(x.Stmts)
		case *ast.BlockStmt:
			stmts(x.Stmts)
		case *ast.GroupExpr:
			stmts(x.Stmts)
		case *ast.IfExpr:
			branch(x, x.Pos(), branchIf, 2)
		case *ast.TernaryExpr:
			branch(x, x.Pos(), branchTernary, 2)
		case *ast.LogicalExpr:
			branch(x, x.OpPos, branchLogical, 2)
		case *ast.MatchExpr:
			branch(x, x.Pos(), branchMatch, len(x.Arms))
		}
		return true
	})
}

// hit counts one statement (arm -1) or one side of a branch node. A node no Run
// registered — a tree compiled while the Coverage was not installed — counts nothing.
func (c *Coverage) hit(n ast.Node, arm int) {
	c.mu.Lock()
	if slot := c.slots[slotKey{n, arm}]; slot != nil {
		*slot++
	}
	c.mu.Unlock()
}

// FileCoverage is what Coverage knows about one file.
type FileCoverage struct {
	// File is the name the file ran under: the program's name, or the resolved name a
	// module loader gave an `include … from`.
	File string
	// Statements and StatementsRun count every statement and those that ran at least
	// once; Branches and BranchesTaken do the same for branches.
	Statements, StatementsRun int
	Branches, BranchesTaken   int
	// Lines has one entry per line that holds a statement, in order. Hits is the count
	// of the line's least-run statement, so a line is covered only when all of it ran.
	Lines []LineCoverage
	// BranchHits has one entry per branch, in source order. Block numbers the branch
	// point within its line, Branch the side.
	BranchHits []BranchCoverage
}

// LineCoverage is one line's statement count.
type LineCoverage struct {
	Line, Hits int
}

// BranchCoverage is one branch's count.
type BranchCoverage struct {
	Line, Block, Branch, Hits int
}

// Percent is the share of statements that ran, from 0 to 100. A file with no
// statements is fully covered.
func (fc FileCoverage) Percent() float64 {
	if fc.Statements == 0 {
		return 100
	}
	return 100 * float64(fc.StatementsRun) / float64(fc.Statements)
}

// MissedLines lists the lines with a statement that never ran.
func (fc FileCoverage) MissedLines() []int {
	var out []int
	for _, l := range fc.Lines {
		if l.Hits == 0 {
			out = append(out, l.Line)
		}
	}
	return out
}

// Files reports every file the Coverage has seen, sorted by name. A name that ran with
// more than one text is reported once for each.
func (c *Coverage) Files() []FileCoverage {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]fileKey, 0, len(c.files))
	for k := range c.files {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].name != keys[j].name {
			return keys[i].name < keys[j].name
		}
		return string(keys[i].src[:]) < string(keys[j].src[:])
	})
	out := make([]FileCoverage, 0, len(keys))
	for _, k := range keys {
		out = append(out, c.files[k].report())
	}
	return out
}

func (fc *fileCover) report() FileCoverage {
	r := FileCoverage{File: fc.name}
	lines := map[int]int{}
	for k, slot := range fc.stmts {
		n := *slot
		r.Statements++
		if n > 0 {
			r.StatementsRun++
		}
		if h, ok := lines[k.line]; !ok || n < h {
			lines[k.line] = n
		}
	}
	for l, h := range lines {
		r.Lines = append(r.Lines, LineCoverage{Line: l, Hits: h})
	}
	sort.Slice(r.Lines, func(i, j int) bool { return r.Lines[i].Line < r.Lines[j].Line })

	keys := make([]branchKey, 0, len(fc.branches))
	for k, slot := range fc.branches {
		keys = append(keys, k)
		r.Branches++
		if *slot > 0 {
			r.BranchesTaken++
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.pos != b.pos {
			if a.pos.line != b.pos.line {
				return a.pos.line < b.pos.line
			}
			return a.pos.col < b.pos.col
		}
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		return a.arm < b.arm
	})
	block, line, prev := -1, 0, branchKey{}
	for i, k := range keys {
		if i == 0 || k.pos != prev.pos || k.kind != prev.kind {
			if k.pos.line != line {
				block, line = 0, k.pos.line
			} else {
				block++
			}
		}
		prev = k
		r.BranchHits = append(r.BranchHits, BranchCoverage{Line: k.pos.line, Block: block, Branch: k.arm, Hits: *fc.branches[k]})
	}
	return r
}
