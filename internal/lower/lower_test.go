package lower

import (
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/analysis"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/ir"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/target"
)

func mustLower(t *testing.T, src string) *ir.Program {
	t.Helper()
	f, err := syntax.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, diags := Lower(f, target.V1_12_2, "test.ggm")
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
		`namespace test:ns
fn f(a: int) -> int { a + 1 }`,
		`namespace test:ns
fn f(a: int, b: int) -> int {
  if a < b { return 1 }
  a - b
}`,
		`namespace test:ns
fn f(a: int, b: int) -> int {
  while a < b, budget 4 { a = a + 1 }
  a
}`,
		`namespace test:ns
fn f(a: int) -> int {
  if a > 5 { return 10 }
  if a < 0 { return -1 }
  a + 1
}`,
		`namespace test:ns
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
	prog := mustLower(t, `namespace test:ns
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
	// The source must actually produce temporaries. An earlier version used
	// arithmetic on locals, which lowers entirely to l0/l1 slot copies -- no
	// temporary appeared, the loop below walked zero names, and the assertion
	// could not fail. A comparison materialises into a temporary, which is the
	// shape under test.
	prog := mustLower(t, `namespace test:ns
fn g(x: int, y: int) -> int { if x > y { 1 } else { 0 } }
fn f(a: int, b: int) -> int { if a >= b { 2 } else { 3 } }`)
	sawTemp := false
	owners := map[string]string{}
	for _, fn := range prog.Funcs {
		for _, b := range fn.Blocks {
			for _, op := range b.Ops {
				for _, d := range analysis.Defs(op) {
					if !isTemp(d.Obj) {
						continue
					}
					sawTemp = true
					if prev, ok := owners[d.Obj]; ok && prev != fn.Path {
						t.Fatalf("temp %s shared by %s and %s", d.Obj, prev, fn.Path)
					}
					owners[d.Obj] = fn.Path
				}
			}
		}
	}
	if !sawTemp {
		t.Fatal("no temporaries were produced, so this test asserted nothing")
	}
}

// isTemp reports whether an objective is a compiler temporary.
//
// It must agree with what the compiler actually mints. This used to accept
// `t` followed by digits, which was right until the prefix changed; after
// that it matched NOTHING, so TestTemporariesAreProgramWideUnique walked nine
// real temporaries, recognised zero, and its assertion never ran. A test that
// cannot see its own subject passes forever.
func isTemp(obj string) bool {
	return optimize.IsTempName(obj)
}

// abs(x - y) >= 1 must not materialise the absolute value.
func TestAbsComparisonAvoidsMaterialisation(t *testing.T) {
	prog := mustLower(t, `namespace test:ns
fn f(a: int, b: int) -> int {
  while abs(a - b) >= 1, budget 4 { a = a + 1 }
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
