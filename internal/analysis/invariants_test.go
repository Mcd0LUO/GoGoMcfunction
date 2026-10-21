package analysis

import (
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/ir"
)

// A name the AUTHOR declared is reported by the lowerer, which has a source
// position. Verify must not report it again: doing so produced two diagnostics
// for one mistake, and the one without a line came first.
func TestVerifySkipsADeclaredObjective(t *testing.T) {
	long := "AVeryLongObjectiveNameThatExceedsSixteen"
	p := ir.NewProgram("t:x")
	p.Objs = append(p.Objs, ir.ObjDecl{Name: long})
	fn := p.NewFunc("t:x/f")
	fn.Emit(0, ir.Set{Dst: ir.Loc{Holder: "@s", Obj: long}, Value: 1})

	if got := Verify(p, 16); len(got) != 0 {
		t.Fatalf("a declared objective was reported by Verify as well as by the lowerer: %v", got)
	}
}

// The check still has to catch what the lowerer cannot see: a name the
// COMPILER put in a location, which no declaration covers. Without this the
// skip above could hide a genuinely over-long generated slot.
func TestVerifyStillCatchesAnUndeclaredObjective(t *testing.T) {
	long := "AVeryLongObjectiveNameThatExceedsSixteen"
	p := ir.NewProgram("t:x")
	fn := p.NewFunc("t:x/f")
	fn.Emit(0, ir.Set{Dst: ir.Loc{Holder: "@s", Obj: long}, Value: 1})

	got := Verify(p, 16)
	if len(got) != 1 {
		t.Fatalf("expected one violation for an over-long undeclared objective, got %d: %v", len(got), got)
	}
}
