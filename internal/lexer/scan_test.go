package lexer

import "testing"

// Scan keeps what Lex throws away: the comments of §3.2 and the heredocs of §3.7. The
// token stream must not notice, which is the first thing asserted here.

func TestScanLeavesTheTokenStreamAlone(t *testing.T) {
	src := "a = 1 # tail\n# own\nb = <<~T\n  body\nT\n"
	plain, _ := Lex("t.mzs", src)
	toks, _, _, errs := Scan("t.mzs", src)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(plain) != len(toks) {
		t.Fatalf("Lex gave %d tokens, Scan gave %d", len(plain), len(toks))
	}
	for i := range plain {
		if plain[i] != toks[i] {
			t.Fatalf("token %d differs: %v vs %v", i, plain[i], toks[i])
		}
	}
}

func TestScanComments(t *testing.T) {
	src := "a = 1 # tail\n# own\n   # indented, still its own line\nb = 2\n"
	_, cmts, _, _ := Scan("t.mzs", src)
	want := []struct {
		text string
		own  bool
		line int
	}{
		{"# tail", false, 1},
		{"# own", true, 2},
		{"# indented, still its own line", true, 3},
	}
	if len(cmts) != len(want) {
		t.Fatalf("comments = %+v, want %d", cmts, len(want))
	}
	for i, w := range want {
		if cmts[i].Text != w.text || cmts[i].OwnLine != w.own || cmts[i].Pos.Line != w.line {
			t.Errorf("comment %d = %+v, want %q own=%v line=%d", i, cmts[i], w.text, w.own, w.line)
		}
	}
}

// A '#' inside a string is text, and §3.2 says so. The scanner never reaches the branch
// that records one, and a formatter that thought otherwise would move a hash out of a
// message.
func TestScanIgnoresHashesInsideText(t *testing.T) {
	for _, src := range []string{
		`x = "a # b"`,
		`x = 'a # b'`,
		"x = <<~T\n  # not a comment\nT\n",
	} {
		_, cmts, _, _ := Scan("t.mzs", src)
		if len(cmts) != 0 {
			t.Errorf("%q gave comments %+v, want none", src, cmts)
		}
	}
}

// The heredoc record is what points at a body the token stream cannot reach: STR_END is
// reported back up on the tag's own line, so nothing else knows where the lines below
// end.
func TestScanHeredocs(t *testing.T) {
	src := "a = <<~ONE\n  first\nONE\nb = 2\n"
	_, _, hds, _ := Scan("t.mzs", src)
	if len(hds) != 1 {
		t.Fatalf("heredocs = %+v, want one", hds)
	}
	h := hds[0]
	if h.Tag != "ONE" || h.Pos.Line != 1 {
		t.Errorf("heredoc = %+v, want ONE on line 1", h)
	}
	rs := []rune(src)
	if got := string(rs[h.Pos.Offset:h.Term]); got != "a = <<~ONE\n  first\nONE\n"[4:] {
		t.Errorf("src[Pos:Term] = %q, want the tag, the body and the terminator", got)
	}
}

func TestScanTwoHeredocsOnOneLine(t *testing.T) {
	src := "x = f(<<~A, <<~B)\n  a\nA\n  b\nB\ny = 1\n"
	_, _, hds, errs := Scan("t.mzs", src)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(hds) != 2 {
		t.Fatalf("heredocs = %+v, want two", hds)
	}
	if hds[0].Tag != "A" || hds[1].Tag != "B" {
		t.Errorf("tags = %q, %q", hds[0].Tag, hds[1].Tag)
	}
	if hds[0].Term >= hds[1].Term {
		t.Errorf("the second body must end after the first: %d, %d", hds[0].Term, hds[1].Term)
	}
}
