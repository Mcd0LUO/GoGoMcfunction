package driver_test

import (
	"reflect"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// Compound assignment is not a separate operation: `x += e` means `x = x + e`.
// The parser records the operator in AssignStmt.Op, and lowering folds it into
// the binary expression it abbreviates so both spellings take one path.
//
// That fold is what this file pins down. Without it the operator was dropped
// outright and every compound assignment compiled to a plain overwrite, so
// `y += 3` left 3 in y instead of adding 3 to it -- a silent wrong answer,
// not an error, which is the failure mode this compiler exists to prevent.
const compoundSrc = `namespace t:as
extern var a0: int @ "a0"

fn add(x: int) -> int {
  let y = x
  y += 3
  y
}

fn sub(x: int) -> int {
  let y = x
  y -= 3
  y
}

fn mul(x: int) -> int {
  let y = x
  y *= 3
  y
}

fn div(x: int) -> int {
  let y = x
  y /= 3
  y
}

fn mod(x: int) -> int {
  let y = x
  y %= 3
  y
}
`

// refCompound is what each operator must produce, computed without GGM.
//
// Division and modulo truncate toward zero, matching 1.12.2 rather than Go's
// floor semantics for negative operands: -7 / 3 is -2 and -7 % 3 is -1.
func refCompound(fn string, x int) int {
	switch fn {
	case "add":
		return x + 3
	case "sub":
		return x - 3
	case "mul":
		return x * 3
	case "div":
		return int(int32(x) / 3)
	case "mod":
		return int(int32(x) % 3)
	}
	panic("unknown fn " + fn)
}

func TestCompoundAssignmentComputes(t *testing.T) {
	files := compile(t, compoundSrc, optimize.O2)
	h := newHarness(t, "t:as", files)

	// 7 exercises non-zero quotient and remainder; -7 checks the sign, which
	// is where "the operator was dropped" and "the operator is inverted"
	// would look the same on non-negative inputs alone.
	for _, x := range []int{7, -7, 0, 1, 2, 100, -100} {
		for _, fn := range []string{"add", "sub", "mul", "div", "mod"} {
			got, _ := h.Call("t:as/"+fn, map[string]int{"a0": x})
			if want := refCompound(fn, x); got != want {
				t.Errorf("%s(%d) = %d, want %d", fn, x, got, want)
			}
		}
	}
}

func TestCompoundAssignmentAtEveryLevel(t *testing.T) {
	for _, level := range []optimize.Level{optimize.O0, optimize.O1, optimize.O2, optimize.O3} {
		t.Run(level.String(), func(t *testing.T) {
			files := compile(t, compoundSrc, level)
			h := newHarness(t, "t:as", files)
			for _, x := range []int{7, -7, 12345} {
				for _, fn := range []string{"add", "sub", "mul", "div", "mod"} {
					got, _ := h.Call("t:as/"+fn, map[string]int{"a0": x})
					if want := refCompound(fn, x); got != want {
						t.Errorf("%s(%d) = %d, want %d", fn, x, got, want)
					}
				}
			}
		})
	}
}

// The two spellings must be indistinguishable, not merely both correct: an
// optimisation that treats them differently would be free to diverge from
// there on. Comparing rendered output catches that at every level, including
// the redundant self-copy -O0 emits for both.
func TestCompoundAssignmentMatchesLonghand(t *testing.T) {
	pairs := []struct{ name, compound, longhand string }{
		{"add", "y += 3", "y = y + 3"},
		{"sub", "y -= 3", "y = y - 3"},
		{"mul", "y *= 3", "y = y * 3"},
		{"div", "y /= 3", "y = y / 3"},
		{"mod", "y %= 3", "y = y % 3"},
	}
	for _, p := range pairs {
		for _, level := range []optimize.Level{optimize.O0, optimize.O1, optimize.O2, optimize.O3} {
			t.Run(p.name+"/"+level.String(), func(t *testing.T) {
				mk := func(stmt string) string {
					return "namespace t:as\nextern var a0: int @ \"a0\"\n\nfn f(x: int) -> int {\n  let y = x\n  " +
						stmt + "\n  y\n}\n"
				}
				got := compile(t, mk(p.compound), level)
				want := compile(t, mk(p.longhand), level)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s and %s compiled differently\ngot:  %v\nwant: %v",
						p.compound, p.longhand, got, want)
				}
			})
		}
	}
}
