package lineedit

// Key decoding. A terminal in raw mode does not deliver keys, it delivers bytes: a
// control byte for the Ctrl-chords, a UTF-8 rune for anything printable, and for
// everything with a name on the keycap — the arrows above all — an escape sequence whose
// shape depends on the emulator and on the mode it happens to be in. The decoder below
// accepts every spelling in circulation for the keys the editor uses, and answers
// keyIgnore to everything else rather than letting an unknown sequence fall through as
// text: a stray "[B" appearing in the middle of a line is far worse than a key that does
// nothing.
//
// A lone Escape is the one key that cannot be decoded on arrival, since every sequence
// starts with it and nothing but the next byte — or its absence — tells them apart.
// Waiting for that byte is what every small line editor does, and it costs nothing here:
// Escape has no binding in this editor, so the only visible effect is that pressing it
// alone does nothing until the next key arrives.

type keyKind int

const (
	keyIgnore keyKind = iota
	keyRune
	keyEnter
	keyBackspace
	keyDelete
	keyLeft
	keyRight
	keyUp
	keyDown
	keyWordLeft
	keyWordRight
	keyHome
	keyEnd
	keyKillToEnd
	keyKillToStart
	keyKillWordLeft
	keyKillWordRight
	keyTranspose
	keyClear
	keyTab
	keyInterrupt
	keyEOF
)

type key struct {
	kind keyKind
	r    rune
}

// The control bytes the chords arrive as. Named because 0x17 is not obviously Ctrl-W.
const (
	ctrlA = 0x01
	ctrlB = 0x02
	ctrlC = 0x03
	ctrlD = 0x04
	ctrlE = 0x05
	ctrlF = 0x06
	ctrlH = 0x08
	tab   = 0x09
	ctrlK = 0x0b
	ctrlL = 0x0c
	ctrlN = 0x0e
	ctrlP = 0x10
	ctrlT = 0x14
	ctrlU = 0x15
	ctrlW = 0x17
	esc   = 0x1b
	del   = 0x7f
)

func (e *Editor) readKey() (key, error) {
	r, _, err := e.in.ReadRune()
	if err != nil {
		return key{}, err
	}
	switch r {
	case '\r', '\n':
		return key{kind: keyEnter}, nil
	case del, ctrlH:
		return key{kind: keyBackspace}, nil
	case ctrlA:
		return key{kind: keyHome}, nil
	case ctrlB:
		return key{kind: keyLeft}, nil
	case ctrlC:
		return key{kind: keyInterrupt}, nil
	case ctrlD:
		return key{kind: keyEOF}, nil
	case ctrlE:
		return key{kind: keyEnd}, nil
	case ctrlF:
		return key{kind: keyRight}, nil
	case tab:
		return key{kind: keyTab}, nil
	case ctrlK:
		return key{kind: keyKillToEnd}, nil
	case ctrlL:
		return key{kind: keyClear}, nil
	case ctrlN:
		return key{kind: keyDown}, nil
	case ctrlP:
		return key{kind: keyUp}, nil
	case ctrlT:
		return key{kind: keyTranspose}, nil
	case ctrlU:
		return key{kind: keyKillToStart}, nil
	case ctrlW:
		return key{kind: keyKillWordLeft}, nil
	case esc:
		return e.readEscape(), nil
	}
	if r < 0x20 || r == 0xfffd {
		// A control byte with no binding, or a byte that was not valid UTF-8. Neither
		// is something to put in the buffer.
		return key{kind: keyIgnore}, nil
	}
	return key{kind: keyRune, r: r}, nil
}

// readEscape decodes what follows an Escape. Two families matter: CSI ("\x1b[…"), which
// is what the arrows send in normal mode, and SS3 ("\x1bO…"), which is what the same keys
// send when the terminal is in application-cursor mode — a mode plenty of terminals and
// multiplexers turn on by themselves, which is why both spellings are read here rather
// than one of them being declared the right one.
func (e *Editor) readEscape() key {
	r, _, err := e.in.ReadRune()
	if err != nil {
		return key{kind: keyIgnore}
	}
	switch r {
	case '[':
		return e.readCSI()
	case 'O':
		r2, _, err := e.in.ReadRune()
		if err != nil {
			return key{kind: keyIgnore}
		}
		return cursorKey(r2, 0)
	case 'b': // Alt-B, the word motions readline puts on the meta key
		return key{kind: keyWordLeft}
	case 'f':
		return key{kind: keyWordRight}
	case 'd':
		return key{kind: keyKillWordRight}
	case del, ctrlH:
		return key{kind: keyKillWordLeft}
	}
	// Not a sequence: this was a bare Escape, and what followed is the next key. Putting
	// it back is what keeps Escape-then-Enter an Enter instead of a swallowed line.
	_ = e.in.UnreadRune()
	return key{kind: keyIgnore}
}

// readCSI reads the parameter bytes of a control sequence and dispatches on its final
// byte. The parameters are only consulted for the modifier — "\x1b[1;5C" is Ctrl-Right —
// and for the tilde family, where the number in front of the '~' *is* the key.
func (e *Editor) readCSI() key {
	var params []rune
	for {
		r, _, err := e.in.ReadRune()
		if err != nil {
			return key{kind: keyIgnore}
		}
		if r >= 0x40 && r <= 0x7e {
			return csiKey(r, params)
		}
		params = append(params, r)
		if len(params) > 16 {
			// Not a sequence this editor will understand, and reading further would be
			// reading the user's text.
			return key{kind: keyIgnore}
		}
	}
}

func csiKey(final rune, params []rune) key {
	if final == '~' {
		switch csiParam(params, 0) {
		case 1, 7:
			return key{kind: keyHome}
		case 3:
			return key{kind: keyDelete}
		case 4, 8:
			return key{kind: keyEnd}
		}
		return key{kind: keyIgnore}
	}
	return cursorKey(final, csiParam(params, 1))
}

// cursorKey maps the final byte of an arrow-key sequence. mod is the xterm modifier
// encoding: 5 and 6 carry Ctrl, 3 and 4 carry Alt, and either turns a character motion
// into a word motion — the binding every terminal user already has in their fingers.
func cursorKey(final rune, mod int) key {
	word := mod == 3 || mod == 4 || mod == 5 || mod == 6 || mod == 7 || mod == 8
	switch final {
	case 'A':
		return key{kind: keyUp}
	case 'B':
		return key{kind: keyDown}
	case 'C':
		if word {
			return key{kind: keyWordRight}
		}
		return key{kind: keyRight}
	case 'D':
		if word {
			return key{kind: keyWordLeft}
		}
		return key{kind: keyLeft}
	case 'H':
		return key{kind: keyHome}
	case 'F':
		return key{kind: keyEnd}
	}
	return key{kind: keyIgnore}
}

// csiParam returns the n-th semicolon-separated parameter, or 0 when it is absent or not
// a number — a private-use sequence ("\x1b[?1h") lands here too and must not be mistaken
// for a key.
func csiParam(params []rune, n int) int {
	field, val, digits := 0, 0, false
	for _, r := range params {
		switch {
		case r == ';':
			if field == n && digits {
				return val
			}
			field, val, digits = field+1, 0, false
		case r >= '0' && r <= '9':
			val, digits = val*10+int(r-'0'), true
		default:
			return 0
		}
	}
	if field == n && digits {
		return val
	}
	return 0
}
