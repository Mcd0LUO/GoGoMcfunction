// Package analysis holds reusable data-flow analyses over the IR.
//
// Analyses are pure functions of a function; they never mutate the IR. Passes
// consume them and are therefore free of ordering surprises.
//
// Every analysis here iterates to a fixed point, because a CFG may contain
// cycles — a `while` loop is a back edge.
package analysis

import (
	"fmt"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/ir"
)

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
	case ir.Raw:
		// A raw command declares its reads; the text is not parsed.
		//
		// Its declared writes are also USES, for the same reason a guarded
		// write's are: a command inside an `execute` may not run at all, and
		// where it does not the previous value survives. Treating the write as
		// an unconditional kill let dead-store elimination delete the `set 0`
		// that initialises a probe flag -- the flag then kept whatever it held
		// last tick, and the branch read a stale answer.
		out := append([]ir.Loc{}, o.Reads...)
		return append(out, o.Writes...)
	case ir.Guarded:
		// A guarded write does NOT kill the previous value: where the guard
		// fails the old value survives. So the inner op's destinations are also
		// uses, exactly like a phi node. Omitting this let dead-store
		// elimination delete the `set 0` that initialises a boolean slot.
		uses := append(Uses(o.Inner), PredUses(o.Cond)...)
		return append(uses, Defs(o.Inner)...)
	case ir.Set:
		// `scoreboard players set` writes a literal. It reads nothing.
		return nil
	case ir.Call, ir.FanOut:
		// A call reads the arguments the caller marshalled into the ABI
		// registers. Those are ordinary Locs that already appear as uses of
		// the ops that wrote them, so there is nothing extra to report here.
		return nil
	default:
		// An op this function does not know about.
		//
		// Returning nil would claim it reads nothing, and liveness would then
		// let dead-store elimination delete whatever it depends on -- a silent
		// wrong answer the moment a new ir.Op is added. Panicking turns that
		// into a compile-time failure that names the missing case.
		//
		// This is deliberately not "return everything": the caller would have
		// to invent a location set, and there is no honest one. The switch must
		// be extended, and that is a code change, not a runtime decision.
		panic(fmt.Sprintf("analysis.Uses: unhandled ir.Op %T; add a case, because assuming it reads nothing lets liveness delete its inputs", op))
	}
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
	case ir.Raw:
		return o.Writes
	case ir.Guarded:
		return Defs(o.Inner)
	case ir.Call:
		// A call overwrites the slot the callee leaves its result in.
		//
		// Not listing it made that slot look untouched, and liveness then
		// treated the `copy dst = ret` that follows as reading a value nothing
		// defines -- so the coalescer was free to give the return slot to a
		// temporary still live across the call.
		//
		// The slot is carried on the op because only the lowering knows the
		// holder: a function with no executor keeps its registers on a fake
		// player.
		if !o.Ret.Valid() {
			return nil
		}
		return []ir.Loc{o.Ret}
	case ir.FanOut:
		// A fan-out runs a function once per matching entity and discards its
		// result. Nothing is defined.
		return nil
	default:
		// Same reasoning as Uses: claiming an unknown op writes nothing is a
		// silent miscompile waiting for the next ir.Op.
		panic(fmt.Sprintf("analysis.Defs: unhandled ir.Op %T; add a case, because assuming it writes nothing makes its result look dead", op))
	}
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

	// Two scratch maps, reused across blocks and iterations.
	//
	// This loop used to allocate a fresh pair per block per iteration, and the
	// iteration count is bounded by the block count -- so a function with N
	// blocks allocated O(N^2) maps. 3200 independent ifs took 4.15 s and 688
	// GC cycles while the peak heap stayed at 7 MB: the cost was total
	// allocation, not residency.
	out := make(map[ir.Loc]bool, 16)
	in := make(map[ir.Loc]bool, 16)

	changed := true
	for changed {
		changed = false
		// iterate backwards: information flows against control flow
		for i := len(fn.Blocks) - 1; i >= 0; i-- {
			b := fn.Blocks[i]

			clearSet(out)
			for _, s := range ir.Succs(b.Term) {
				for l := range liveIn[s] {
					out[l] = true
				}
			}
			for _, l := range PredUses(predOf(b.Term)) {
				out[l] = true
			}

			clearSet(in)
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

			// Copy into the result: `out` and `in` are scratch and the next
			// block overwrites them.
			if !sameSet(out, liveOut[b.ID]) || !sameSet(in, liveIn[b.ID]) {
				liveOut[b.ID] = cloneSet(out)
				liveIn[b.ID] = cloneSet(in)
				changed = true
			}
		}
	}
	return liveOut
}

// clearSet empties a set without reallocating it.
func clearSet(s map[ir.Loc]bool) {
	for k := range s {
		delete(s, k)
	}
}

// cloneSet copies a set.
func cloneSet(s map[ir.Loc]bool) map[ir.Loc]bool {
	out := make(map[ir.Loc]bool, len(s))
	for k := range s {
		out[k] = true
	}
	return out
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
