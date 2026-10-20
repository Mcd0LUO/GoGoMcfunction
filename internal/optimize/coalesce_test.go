package optimize

import (
	"testing"

	"github.com/Mcd0LUO/CelesteaMcfunction/internal/analysis"
	"github.com/Mcd0LUO/CelesteaMcfunction/internal/ir"
)

// The shape a comparison-as-value lowers to: a difference slot, then a
// conditional write into a result slot, then a copy out. The guard reads
// the difference while the write targets the result, so the two must
// never be the same objective.
//
// The names are chosen to match what the compiler actually produced in
// luo:math/chance. That matters: the bug was an ordering problem in the
// greedy colouring, and it only appears when the sorted order of the
// names puts the blocking neighbour after the blocked node.
func comparisonShape() *ir.Program {
	p := ir.NewProgram("t")
	fb := p.NewFunc("t/f")
	diff := ir.Loc{Holder: "@s", Obj: "t8"}
	res := ir.Loc{Holder: "@s", Obj: "t10"}
	carried := ir.Loc{Holder: "@s", Obj: "t9"}

	fb.Emit(0,
		ir.Copy{Dst: carried, Src: ir.Loc{Holder: "@s", Obj: "a1"}},
		ir.Copy{Dst: diff, Src: carried},
		ir.Arith{Dst: diff, Op: "-=", Src: ir.Loc{Holder: "@s", Obj: "a0"}},
		ir.Set{Dst: res, Value: 0},
		ir.Guarded{
			Inner: ir.Set{Dst: res, Value: 1},
			Cond:  ir.Predicate{Alts: [][]ir.Cond{{{Obj: diff.Obj, HasMin: true, Min: 0, HasMax: true, Max: 0}}}},
		},
		ir.Copy{Dst: ir.Loc{Holder: "@s", Obj: "return"}, Src: res},
	)
	return p
}

// assertDistinctLive checks the property coalescing exists to preserve:
// two temporaries that are live at the same time never share an
// objective.
//
// It recomputes interference from the renamed function with the same
// analysis the pass is supposed to obey, so it tests the result rather
// than restating the implementation.
func assertDistinctLive(t *testing.T, p *ir.Program) {
	t.Helper()
	for _, fn := range p.Funcs {
		out := analysis.LiveOut(fn)
		for _, b := range fn.Blocks {
			live := map[ir.Loc]bool{}
			for _, s := range ir.Succs(b.Term) {
				for l := range out[s] {
					live[l] = true
				}
			}
			for j := len(b.Ops) - 1; j >= 0; j-- {
				op := b.Ops[j]
				liveNow := map[ir.Loc]bool{}
				for l := range live {
					liveNow[l] = true
				}
				for _, u := range analysis.Uses(op) {
					liveNow[u] = true
				}
				for _, d := range analysis.Defs(op) {
					for l := range liveNow {
						if l == d || !isTempName(l.Obj) || !isTempName(d.Obj) {
							continue
						}
						if l.Obj == d.Obj {
							t.Fatalf("%s: block %d op %d writes %s while it is still live: the guard and the write share a slot",
								fn.Path, b.ID, j, d.Obj)
						}
					}
				}
				for _, d := range analysis.Defs(op) {
					delete(live, d)
				}
				for _, u := range analysis.Uses(op) {
					live[u] = true
				}
			}
		}
	}
}

// The difference, the carried value and the result are all live at the
// same moment in this shape, so coalescing must give them three slots.
//
// With one-directional interference edges the pass assigned all three to
// t0, and the guard then tested the result slot it had just cleared --
// turning "rand <= p" into "always true" in the shipped example.
func TestCoalesceKeepsSimultaneousTempsApart(t *testing.T) {
	p := comparisonShape()
	assertDistinctLive(t, p)

	p = CoalesceTemps{}.Run(p)
	assertDistinctLive(t, p)

	// Two objectives is the right answer here: the carried value and the
	// result never overlap, so they may share. Only the difference needs
	// a slot of its own.
	seen := map[string]bool{}
	for _, op := range p.Funcs[0].Ops() {
		for _, l := range append(analysis.Defs(op), analysis.Uses(op)...) {
			if isTempName(l.Obj) {
				seen[l.Obj] = true
			}
		}
	}
	if len(seen) != 2 {
		t.Fatalf("coalescing produced %d objective(s) %v, want 2", len(seen), seen)
	}
}
