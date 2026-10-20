package analysis

import (
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/ir"
)

// A read-modify-write op must report its destination as a use as well as a
// definition. Otherwise liveness believes the accumulator is dead on entry and
// dead-store elimination removes the instruction that initialises it.
func TestArithIsReadModifyWrite(t *testing.T) {
	dst := ir.Loc{Holder: "@s", Obj: "t0"}
	src := ir.Loc{Holder: "@s", Obj: "a0"}
	op := ir.Arith{Dst: dst, Op: "-=", Src: src}

	uses := Uses(op)
	if len(uses) != 2 {
		t.Fatalf("expected 2 uses (src and dst), got %d: %+v", len(uses), uses)
	}
	found := false
	for _, u := range uses {
		if u == dst {
			found = true
		}
	}
	if !found {
		t.Fatal("destination is not reported as a use")
	}
}

// A temporary live only because a branch reads it must not be considered dead.
func TestBranchPredicateKeepsSlotLive(t *testing.T) {
	t0 := ir.Loc{Holder: "@s", Obj: "t0"}
	fn := &ir.Function{
		Path: "ns:f",
		Blocks: []*ir.Block{
			{ID: 0,
				Ops: []ir.Op{
					ir.Copy{Dst: t0, Src: ir.Loc{Holder: "@s", Obj: "a0"}},
					ir.Arith{Dst: t0, Op: "-=", Src: ir.Loc{Holder: "#4", Obj: ir.ConstObjective}},
				},
				Term: ir.Branch{
					Cond: ir.Predicate{Alts: [][]ir.Cond{{{Obj: "t0", HasMax: true, Max: -1}}}},
					Then: 1, Else: 2,
				},
			},
			{ID: 1, Term: ir.Return{}},
			{ID: 2, Term: ir.Return{}},
		},
	}
	liveOut := LiveOut(fn)
	if !liveOut[0][t0] {
		t.Fatalf("t0 should be live at the end of block 0: %+v", liveOut[0])
	}
}
