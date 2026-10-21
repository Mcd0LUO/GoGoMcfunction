// Package diag holds diagnostics shared by every compiler phase.
//
// Keeping this in its own package is what lets the front end, the middle end
// and the back end report errors in one uniform way without importing each
// other.
package diag

import (
	"fmt"
	"sort"
)

// Severity classifies a diagnostic.
type Severity int

const (
	Error Severity = iota
	Warning
)

func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// Pos is a 1-based source position.
//
// File is optional. A diagnostic about a whole file -- "this file has no
// namespace" -- has no line to point at, and without the name it printed as
// `-:`, which does not say WHICH file. Phases that know the file name set it;
// the front end does not, because it is handed text rather than a path.
type Pos struct {
	File string
	Line int
	Col  int
}

func (p Pos) String() string {
	switch {
	case p.Line == 0 && p.File == "":
		return "-"
	case p.Line == 0:
		return p.File
	case p.File == "":
		return fmt.Sprintf("%d:%d", p.Line, p.Col)
	default:
		return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
	}
}

// Diagnostic is a single compiler message.
type Diagnostic struct {
	Pos      Pos
	Code     string
	Msg      string
	Severity Severity
}

func (d Diagnostic) String() string {
	return fmt.Sprintf("%s: %s: %s [%s]", d.Pos, d.Severity, d.Msg, d.Code)
}

// Bag collects diagnostics in emission order.
type Bag struct {
	items []Diagnostic
}

func (b *Bag) Error(pos Pos, code, format string, args ...any) {
	b.items = append(b.items, Diagnostic{Pos: pos, Code: code, Msg: fmt.Sprintf(format, args...), Severity: Error})
}

func (b *Bag) Warn(pos Pos, code, format string, args ...any) {
	b.items = append(b.items, Diagnostic{Pos: pos, Code: code, Msg: fmt.Sprintf(format, args...), Severity: Warning})
}

func (b *Bag) Add(d Diagnostic) { b.items = append(b.items, d) }

func (b *Bag) Items() []Diagnostic { return b.items }

func (b *Bag) Len() int { return len(b.items) }

// HasErrors reports whether any diagnostic is fatal.
func (b *Bag) HasErrors() bool {
	for _, d := range b.items {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

// Sorted returns diagnostics ordered by position for stable output.
func (b *Bag) Sorted() []Diagnostic {
	out := append([]Diagnostic(nil), b.items...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Pos.Line != out[j].Pos.Line {
			return out[i].Pos.Line < out[j].Pos.Line
		}
		return out[i].Pos.Col < out[j].Pos.Col
	})
	return out
}
