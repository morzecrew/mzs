package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"mzs/mzs"
)

// The output of `mzs --profile` and `--profile-out` (RFC 0019 §4).

// profileRows is how many functions the table shows; the rest are summed in one line.
const profileRows = 25

// reportProfile prints the table for --profile and writes --profile-out. It answers
// false when the file could not be written.
func reportProfile(cfg *config, p *mzs.Profile, stderr io.Writer) bool {
	if p == nil {
		return true
	}
	if cfg.profile {
		fmt.Fprint(stderr, profileTable(p))
	}
	if cfg.profileOut != "" {
		if err := writeFolded(cfg.profileOut, p); err != nil {
			fmt.Fprintf(stderr, "mzs: --profile-out: %v\n", err)
			return false
		}
	}
	return true
}

// profileTable is the table --profile prints, most steps of their own first:
//
//	   self      %    total   calls  function
//	1840212  61.3%  1840212   12000  score rank.mzs:14
func profileTable(p *mzs.Profile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%10s %6s %10s %7s  %s\n", "self", "%", "total", "calls", "function")
	shown := p.Funcs
	if len(shown) > profileRows {
		shown = shown[:profileRows]
	}
	for _, f := range shown {
		fmt.Fprintf(&b, "%10d %5.1f%% %10d %7d  %s\n", f.Self, percent(f.Self, p.Steps), f.Total, f.Calls, f.Label())
	}
	if rest := p.Funcs[len(shown):]; len(rest) > 0 {
		var self int64
		for _, f := range rest {
			self += f.Self
		}
		fmt.Fprintf(&b, "%10d %5.1f%% %10s %7s  (%d more)\n", self, percent(self, p.Steps), "", "", len(rest))
	}
	fmt.Fprintf(&b, "%10d steps\n", p.Steps)
	return b.String()
}

func percent(n, of int64) float64 {
	if of == 0 {
		return 0
	}
	return 100 * float64(n) / float64(of)
}

// writeFolded writes the folded-stack format flame-graph tools read: one line per call
// path, its frames joined by `;`, then a space and the steps taken there.
func writeFolded(path string, p *mzs.Profile) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, s := range p.Stacks {
		frames := make([]string, len(s.Frames))
		for i, fr := range s.Frames {
			// `;` separates frames, and the count follows the last space, so neither
			// may appear inside a frame's own name.
			frames[i] = strings.NewReplacer(";", ",", "\n", " ").Replace(fr)
		}
		fmt.Fprintf(w, "%s %d\n", strings.Join(frames, ";"), s.Steps)
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
