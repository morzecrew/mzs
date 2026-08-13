package bt

import "unicode"

// frame is one backtrack point: the alternative branch of a split, the position to
// rewind to, and how much of the capture journal to undo first.
type frame struct{ pc, pos, jbase int32 }

// jentry records a slot's previous value so backtracking can restore captures
// without copying the whole slot array at every choice point.
type jentry struct{ slot, old int32 }

type machine struct {
	re      *Regexp
	src     []rune
	slots   []int32
	journal []jentry
	stack   []frame
	steps   int
	budget  int
}

func (m *machine) reset(src []rune, budget int) {
	m.src = src
	m.budget = budget
	m.steps = 0
	m.journal = m.journal[:0]
	m.stack = m.stack[:0]
}

func (m *machine) clearSlots() {
	for i := range m.slots {
		m.slots[i] = -1
	}
	m.journal = m.journal[:0]
	m.stack = m.stack[:0]
}

func (m *machine) setSlot(i int32, v int32) {
	m.journal = append(m.journal, jentry{i, m.slots[i]})
	m.slots[i] = v
}

func (m *machine) unwind(n int) {
	for len(m.journal) > n {
		e := m.journal[len(m.journal)-1]
		m.journal = m.journal[:len(m.journal)-1]
		m.slots[e.slot] = e.old
	}
}

func eqFold(a, b rune, fold bool) bool {
	if a == b {
		return true
	}
	if !fold {
		return false
	}
	for f := unicode.SimpleFold(a); f != a; f = unicode.SimpleFold(f) {
		if f == b {
			return true
		}
	}
	return false
}

func (m *machine) assert(kind uint8, pos int) bool {
	switch kind {
	case assertBOL:
		return pos == 0 || m.src[pos-1] == '\n'
	case assertEOL:
		return pos == len(m.src) || m.src[pos] == '\n'
	case assertBOS:
		return pos == 0
	case assertEOS:
		return pos == len(m.src)
	case assertEOSNL:
		return pos == len(m.src) || (pos == len(m.src)-1 && m.src[pos] == '\n')
	case assertWordB, assertNotWordB:
		before := pos > 0 && isWordRune(m.src[pos-1])
		after := pos < len(m.src) && isWordRune(m.src[pos])
		if kind == assertWordB {
			return before != after
		}
		return before == after
	}
	return false
}

// run executes one program from start. require, when >= 0, forces the match to end
// exactly there — that is how lookbehind pins a candidate start position.
//
// The backtrack stack and the capture journal are shared with the caller (both are
// truncated back on the way out), so a successful positive lookaround keeps the
// groups it captured, exactly as Ruby does.
func (m *machine) run(progIdx, start, require int) (int, bool, error) {
	code := m.re.progs[progIdx].code
	base := len(m.stack)
	jbase := len(m.journal)
	pc := 0
	pos := start

Loop:
	for {
		if m.budget > 0 {
			if m.steps++; m.steps > m.budget {
				m.stack = m.stack[:base]
				m.unwind(jbase)
				return 0, false, ErrBudget
			}
		}
		in := &code[pc]
		switch in.op {
		case opChar:
			if pos < len(m.src) && eqFold(m.src[pos], in.r, in.fold) {
				pos++
				pc++
				continue Loop
			}

		case opClass:
			if pos < len(m.src) && in.cls.match(m.src[pos]) {
				pos++
				pc++
				continue Loop
			}

		case opSplit:
			m.stack = append(m.stack, frame{in.y, int32(pos), int32(len(m.journal))})
			pc = int(in.x)
			continue Loop

		case opJmp:
			pc = int(in.x)
			continue Loop

		case opSave:
			m.setSlot(in.x, int32(pos))
			pc++
			continue Loop

		case opMark:
			m.setSlot(in.x, int32(pos))
			pc++
			continue Loop

		case opProgress:
			if int(m.slots[in.x]) != pos {
				pc++
				continue Loop
			}
			// An iteration that consumed nothing ends the loop: skip the jump back to
			// the top and carry on after it, keeping the iteration's captures. This is
			// Onigmo's OP_EMPTY_CHECK_END, and it is why `(a*)*` terminates.
			pc = int(in.y)
			continue Loop

		case opAssert:
			if m.assert(uint8(in.x), pos) {
				pc++
				continue Loop
			}

		case opBackref:
			s, e := m.slots[2*in.x], m.slots[2*in.x+1]
			if s >= 0 && e >= s {
				n := int(e - s)
				if pos+n <= len(m.src) && m.matchRunes(int(s), pos, n, in.fold) {
					pos += n
					pc++
					continue Loop
				}
			}

		case opLook:
			ok, err := m.look(in, pos)
			if err != nil {
				m.stack = m.stack[:base]
				return 0, false, err
			}
			if ok {
				pc++
				continue Loop
			}

		case opAtomic:
			end, ok, err := m.run(int(in.x), pos, -1)
			if err != nil {
				m.stack = m.stack[:base]
				return 0, false, err
			}
			if ok {
				pos = end
				pc++
				continue Loop
			}

		case opMatch:
			if require < 0 || pos == require {
				m.stack = m.stack[:base]
				return pos, true, nil
			}
		}

		// Falling out of the switch means this path failed: rewind to the newest
		// choice point, or report failure when there is none left in this program.
		if len(m.stack) == base {
			m.unwind(jbase)
			return 0, false, nil
		}
		f := m.stack[len(m.stack)-1]
		m.stack = m.stack[:len(m.stack)-1]
		m.unwind(int(f.jbase))
		pc = int(f.pc)
		pos = int(f.pos)
	}
}

func (m *machine) matchRunes(from, at, n int, fold bool) bool {
	for i := 0; i < n; i++ {
		if !eqFold(m.src[at+i], m.src[from+i], fold) {
			return false
		}
	}
	return true
}

// look runs a lookaround. Negative forms roll back any captures the sub-pattern made
// before reporting; lookarounds never offer a second alternative, which is why the
// caller records no backtrack point for them.
func (m *machine) look(in *inst, pos int) (bool, error) {
	neg := in.y&1 != 0
	behind := in.y&2 != 0
	jb := len(m.journal)

	var ok bool
	if behind {
		sp := &m.re.progs[in.x]
		lo := pos - sp.max
		if lo < 0 {
			lo = 0
		}
		hi := pos - sp.min
		for s := lo; s <= hi; s++ {
			end, o, err := m.run(int(in.x), s, pos)
			if err != nil {
				return false, err
			}
			if o && end == pos {
				ok = true
				break
			}
		}
	} else {
		_, o, err := m.run(int(in.x), pos, -1)
		if err != nil {
			return false, err
		}
		ok = o
	}

	if neg {
		if ok {
			m.unwind(jb)
			return false, nil
		}
		return true, nil
	}
	return ok, nil
}
