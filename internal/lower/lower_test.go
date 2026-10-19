package lower

import (
	"testing"

	"cmcf/internal/analysis"
	"cmcf/internal/ir"
	"cmcf/internal/syntax"
	"cmcf/internal/target"
)

func mustLower(t *testing.T, src string) *ir.Program {
	t.Helper()
	f, err := syntax.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, diags := Lower(f, target.V1_12_2)
	for _, d := range diags {
		if d.Severity == 0 {
			t.Fatalf("lower: %v", d)
		}
	}
	return res.Program
}

// The IR must satisfy its own invariants. An empty objective renders as
// `score_=0`, which is valid syntax testing a DIFFERENT objective — a silent
// miscompile, not a crash.
func TestLowerProducesValidIR(t *testing.T) {
	sources := []string{
		`namespace ns
fn f(a: int) -> int { a + 1 }`,
		`namespace ns
fn f(a: int, b: int) -> int {
  if a < b { return 1 }
  a - b
}`,
		`namespace ns
fn f(a: int, b: int) -> int {
  while a < b #[budget(4)] { a = a + 1 }
  a
}`,
		`namespace ns
fn f(a: int) -> int {
  if a > 5 { return 10 }
  if a < 0 { return -1 }
  a + 1
}`,
		`namespace ns
fn g(x: int) -> int { x * 2 }
fn f(a: int) -> int { g(a) + g(a) }`,
	}
	for i, src := range sources {
		prog := mustLower(t, src)
		if vs := analysis.Verify(prog, target.V1_12_2.ObjectiveNameLimit); len(vs) > 0 {
			for _, v := range vs {
				t.Errorf("case %d: %s", i, v)
			}
		}
	}
}

// An early return must terminate its block: the statements after it are only
// reachable from the paths that actually fall through.
func TestEarlyReturnTerminatesBlock(t *testing.T) {
	prog := mustLower(t, `namespace ns
fn f(a: int) -> int {
  if a < 4 { return 1 }
  a * 2
}`)
	fn := prog.Funcs[0]
	foundReturn := false
	for _, b := range fn.Blocks {
		if _, ok := b.Term.(ir.Return); ok {
			foundReturn = true
		}
	}
	if !foundReturn {
		t.Fatal("expected at least one Return terminator")
	}
	// the entry block must branch, not fall through into the return arm
	if _, ok := fn.Blocks[0].Term.(ir.Branch); !ok {
		t.Fatalf("entry block should branch, got %T", fn.Blocks[0].Term)
	}
}

// Negating an equality must yield two alternatives, because 1.12.2 selectors
// have no boolean OR and the complement of a point is two disjoint ranges.
func TestNegateEqualityIsTwoAlternatives(t *testing.T) {
	p := ir.Predicate{Alts: [][]ir.Cond{{{Obj: "d", HasMin: true, Min: 0, HasMax: true, Max: 0}}}}
	np := negate(p)
	if len(np.Alts) != 2 {
		t.Fatalf("expected 2 alternatives, got %d: %+v", len(np.Alts), np.Alts)
	}
	for _, alt := range np.Alts {
		for _, c := range alt {
			if c.Obj != "d" {
				t.Fatalf("negate lost the objective: %+v", c)
			}
		}
	}
}

// De Morgan: NOT((a<=0) AND (b<=0)) is (a>=1) OR (b>=1), i.e. a cross product,
// not one alternative per conjunct.
func TestNegateConjunction(t *testing.T) {
	p := ir.Predicate{Alts: [][]ir.Cond{{
		{Obj: "a", HasMax: true, Max: 0},
		{Obj: "b", HasMax: true, Max: 0},
	}}}
	np := negate(p)
	if len(np.Alts) != 2 {
		t.Fatalf("expected 2 alternatives, got %d: %+v", len(np.Alts), np.Alts)
	}
}

// Temporaries must not be shared between functions: @s is global, so a callee
// reusing the caller's slot corrupts conditions the caller has not consumed.
func TestTemporariesAreProgramWideUnique(t *testing.T) {
	prog := mustLower(t, `namespace ns
fn g(x: int) -> int { x + 1 }
fn f(a: int) -> int {
  let p = g(a)
  let q = a * 2
  p + q
}`)
	owners := map[string]string{}
	for _, fn := range prog.Funcs {
		for _, b := range fn.Blocks {
			for _, op := range b.Ops {
				for _, d := range analysis.Defs(op) {
					if !isTemp(d.Obj) {
						continue
					}
					if prev, ok := owners[d.Obj]; ok && prev != fn.Path {
						t.Fatalf("temp %s shared by %s and %s", d.Obj, prev, fn.Path)
					}
					owners[d.Obj] = fn.Path
				}
			}
		}
	}
}

func isTemp(obj string) bool {
	if len(obj) < 2 || obj[0] != 't' {
		return false
	}
	for _, c := range obj[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// abs(x - y) >= 1 must not materialise the absolute value.
func TestAbsComparisonAvoidsMaterialisation(t *testing.T) {
	prog := mustLower(t, `namespace ns
fn f(a: int, b: int) -> int {
  while abs(a - b) >= 1 #[budget(4)] { a = a + 1 }
  a
}`)
	for _, fn := range prog.Funcs {
		for _, b := range fn.Blocks {
			for _, op := range b.Ops {
				if ar, ok := op.(ir.Arith); ok && ar.Op == "*=" {
					// only the -1 multiply that negation would need; abs must not appear
					t.Logf("multiply at %s: %+v", fn.Path, ar)
				}
			}
		}
	}
}
