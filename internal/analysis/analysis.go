// Package analysis holds reusable data-flow analyses over the IR.
//
// Analyses are pure functions of a function; they never mutate the IR. Passes
// consume them and are therefore free of ordering surprises.
//
// Every analysis here iterates to a fixed point, because a CFG may contain
// cycles — a `while` loop is a back edge.
package analysis

import "cmcf/internal/ir"

// Reachable returns the set of blocks reachable from the entry block.
func Reachable(fn *ir.Function) map[ir.BlockID]bool {
	seen := map[ir.BlockID]bool{}
	if len(fn.Blocks) == 0 {
		return seen
	}
	work := []ir.BlockID{0}
	for len(work) > 0 {
		id := work[len(work)-1]
		work = work[:len(work)-1]
		if seen[id] || int(id) >= len(fn.Blocks) {
			continue
		}
		seen[id] = true
		for _, s := range ir.Succs(fn.Blocks[id].Term) {
			if !seen[s] {
				work = append(work, s)
			}
		}
	}
	return seen
}

// Uses lists the locations an op reads.
//
// Arith, Add and Remove are READ-MODIFY-WRITE: `t -= 4` reads t as well as
// writing it. Omitting Dst here makes dead-store elimination delete the very
// instruction that initialises the accumulator, which produced output like
// `scoreboard players operation @s t0 -= #4` with no preceding assignment.
func Uses(op ir.Op) []ir.Loc {
	switch o := op.(type) {
	case ir.Copy:
		return []ir.Loc{o.Src}
	case ir.Arith:
		return []ir.Loc{o.Src, o.Dst}
	case ir.Add:
		return []ir.Loc{o.Dst}
	case ir.Remove:
		return []ir.Loc{o.Dst}
	case ir.Guarded:
		// A guarded write does NOT kill the previous value: where the guard
		// fails the old value survives. So the inner op's destinations are also
		// uses, exactly like a phi node. Omitting this let dead-store
		// elimination delete the `set 0` that initialises a boolean slot.
		uses := append(Uses(o.Inner), PredUses(o.Cond)...)
		return append(uses, Defs(o.Inner)...)
	}
	return nil
}

// Defs lists the locations an op writes.
func Defs(op ir.Op) []ir.Loc {
	switch o := op.(type) {
	case ir.Set:
		return []ir.Loc{o.Dst}
	case ir.Copy:
		return []ir.Loc{o.Dst}
	case ir.Arith:
		return []ir.Loc{o.Dst}
	case ir.Add:
		return []ir.Loc{o.Dst}
	case ir.Remove:
		return []ir.Loc{o.Dst}
	case ir.Guarded:
		return Defs(o.Inner)
	}
	return nil
}

// PredUses lists the locations a predicate reads.
func PredUses(p ir.Predicate) []ir.Loc {
	var out []ir.Loc
	for _, alt := range p.Alts {
		for _, c := range alt {
			out = append(out, ir.Loc{Holder: "@s", Obj: c.Obj})
		}
	}
	return out
}

// LiveOut computes, for each block, the locations that are live on exit.
//
// Liveness is the foundation for register reuse: two temporaries may share an
// objective exactly when their live ranges do not overlap.
//
// The transfer function is the usual one:
//
//	live_in  = use ∪ (live_out − def)
//	live_out = ⋃ live_in(successors)
//
// Iteration continues until nothing changes, which terminates because the
// lattice is finite and the transfer function is monotone.
func LiveOut(fn *ir.Function) map[ir.BlockID]map[ir.Loc]bool {
	liveIn := map[ir.BlockID]map[ir.Loc]bool{}
	liveOut := map[ir.BlockID]map[ir.Loc]bool{}
	for _, b := range fn.Blocks {
		liveIn[b.ID] = map[ir.Loc]bool{}
		liveOut[b.ID] = map[ir.Loc]bool{}
	}

	changed := true
	for changed {
		changed = false
		// iterate backwards: information flows against control flow
		for i := len(fn.Blocks) - 1; i >= 0; i-- {
			b := fn.Blocks[i]

			out := map[ir.Loc]bool{}
			for _, s := range ir.Succs(b.Term) {
				for l := range liveIn[s] {
					out[l] = true
				}
			}
			for _, l := range PredUses(predOf(b.Term)) {
				out[l] = true
			}

			in := map[ir.Loc]bool{}
			for l := range out {
				in[l] = true
			}
			for j := len(b.Ops) - 1; j >= 0; j-- {
				for _, d := range Defs(b.Ops[j]) {
					delete(in, d)
				}
				for _, u := range Uses(b.Ops[j]) {
					in[u] = true
				}
			}

			if !sameSet(out, liveOut[b.ID]) || !sameSet(in, liveIn[b.ID]) {
				liveOut[b.ID] = out
				liveIn[b.ID] = in
				changed = true
			}
		}
	}
	return liveOut
}

// LiveIn is the companion of LiveOut.
func LiveIn(fn *ir.Function) map[ir.BlockID]map[ir.Loc]bool {
	liveOut := LiveOut(fn)
	out := map[ir.BlockID]map[ir.Loc]bool{}
	for _, b := range fn.Blocks {
		in := map[ir.Loc]bool{}
		for l := range liveOut[b.ID] {
			in[l] = true
		}
		for j := len(b.Ops) - 1; j >= 0; j-- {
			for _, d := range Defs(b.Ops[j]) {
				delete(in, d)
			}
			for _, u := range Uses(b.Ops[j]) {
				in[u] = true
			}
		}
		out[b.ID] = in
	}
	return out
}

func predOf(t ir.Terminator) ir.Predicate {
	if b, ok := t.(ir.Branch); ok {
		return b.Cond
	}
	return ir.Predicate{}
}

func sameSet(a, b map[ir.Loc]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
