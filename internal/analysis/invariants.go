package analysis

import (
	"fmt"
	"unicode/utf8"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/ir"
)

// Violation is a broken IR invariant.
type Violation struct {
	Func string
	Msg  string
}

func (v Violation) String() string { return fmt.Sprintf("%s: %s", v.Func, v.Msg) }

// Verify checks the structural invariants documented in package ir.
//
// This runs in tests AND in the driver, because several of these failures are
// silent miscompiles rather than crashes. In particular an empty objective
// renders as `score_=0`, which is valid syntax that tests the wrong thing.
func Verify(p *ir.Program, maxObjectiveLen int) []Violation {
	var out []Violation
	add := func(fn string, format string, args ...any) {
		out = append(out, Violation{Func: fn, Msg: fmt.Sprintf(format, args...)})
	}

	// An objective the AUTHOR declared is checked by the lowerer, which has a
	// source position and can say where it was written. Repeating it here
	// reported one mistake twice:
	//
	//	-: error: t:lo/f: block 0: objective "AVeryLong..." exceeds 16 characters
	//	2:1: error: objective "AVeryLong..." is 40 characters; ...
	//
	// The second is the useful one -- it has a line, and it says what the
	// truncation DOES. This check still catches what the lowerer cannot see: a
	// name the compiler invented or copied into a location, which no
	// declaration covers.
	declared := make(map[string]bool, len(p.Objs))
	for _, od := range p.Objs {
		declared[od.Name] = true
	}

	for _, fn := range p.Funcs {
		if len(fn.Blocks) == 0 {
			add(fn.Path, "function has no blocks")
			continue
		}
		for _, b := range fn.Blocks {
			if b.Term == nil {
				add(fn.Path, "block %d has no terminator", b.ID)
			}
			for _, s := range ir.Succs(b.Term) {
				if int(s) >= len(fn.Blocks) {
					add(fn.Path, "block %d branches to out-of-range block %d", b.ID, s)
				}
			}
			for _, op := range b.Ops {
				for _, l := range append(Defs(op), Uses(op)...) {
					if !l.Valid() {
						add(fn.Path, "block %d: op with invalid location %+v", b.ID, l)
					}
					// Characters, not bytes: 1.12.2 truncates a Java String by
					// code unit, and a Chinese objective name is legal at 10
					// characters while measuring 18 bytes.
					if maxObjectiveLen > 0 && utf8.RuneCountInString(l.Obj) > maxObjectiveLen && !declared[l.Obj] {
						add(fn.Path, "block %d: objective %q exceeds %d characters", b.ID, l.Obj, maxObjectiveLen)
					}
				}
			}
			if br, ok := b.Term.(ir.Branch); ok {
				for _, c := range allConds(br.Cond) {
					if c.Obj == "" {
						add(fn.Path, "block %d: branch condition with empty objective", b.ID)
					}
				}
			}
		}
		// The entry block must never be a branch/jump target from a later block.
		//
		// Entering block 0 re-runs the function prologue. For a loop that means
		// resetting the counter and budget every iteration, which turns a
		// bounded loop into an infinite one — and the resulting output is still
		// syntactically valid.
		for _, b := range fn.Blocks {
			if b.ID == 0 {
				continue
			}
			for _, s := range ir.Succs(b.Term) {
				if s == 0 {
					add(fn.Path, "block %d jumps back to the entry block", b.ID)
				}
			}
		}
		// every non-entry block must be reachable
		reach := Reachable(fn)
		for _, b := range fn.Blocks {
			if !reach[b.ID] {
				add(fn.Path, "block %d is unreachable", b.ID)
			}
		}
	}
	return out
}

func allConds(p ir.Predicate) []ir.Cond {
	var out []ir.Cond
	for _, alt := range p.Alts {
		out = append(out, alt...)
	}
	return out
}
