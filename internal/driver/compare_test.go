package driver_test

import (
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// A comparison used as a value is materialised as: compute the
// difference into one slot, then conditionally write 1 into another.
// The guard reads the difference while the conditional write targets the
// result, so the two slots must never be the same objective.
//
// This is what coalescing got wrong. The interference graph was built
// with one-directional edges, so colouring could not see that the result
// slot had already claimed the difference slot's objective. In the
// shipped luo:math example that turned "rand <= p" into "always true".
const compareSrc = `namespace t:v
extern a0: int = @s.a0
extern a1: int = @s.a1

fn pick(lo: int, hi: int) -> int {
  hi - lo
}

fn le(x: int, y: int) -> int {
  x <= y
}

/// The call pushes the temporaries past the single-digit range, which is
/// the shape that exposed the asymmetry in the luo:math example.
fn le_after_call(scale: int, p: int) -> int {
  let r = pick(1, scale)
  r <= p
}
`

func TestComparisonAsValue(t *testing.T) {
	files := compile(t, compareSrc, optimize.O2)
	h := newHarness(t, "t:v", files)

	// One Calls per function: the results are independent, so they share a
	// process instead of paying a subprocess start each.
	boolCase := func(name string, sets []map[string]int, wants []int) {
		t.Helper()
		got := h.Calls(name, sets)
		for i := range wants {
			if got[i] != wants[i] {
				t.Errorf("%s%v = %d, want %d", name, sets[i], got[i], wants[i])
			}
		}
	}

	// le(x, y)
	boolCase("t:v/le", []map[string]int{
		{"a0": 3, "a1": 5}, {"a0": 5, "a1": 5}, {"a0": 6, "a1": 5}, {"a0": -1, "a1": 0},
	}, []int{1, 1, 0, 1})

	// le_after_call(scale, p) = (scale - 1) <= p
	boolCase("t:v/le_after_call", []map[string]int{
		{"a0": 10, "a1": 5}, {"a0": 3, "a1": 5}, {"a0": 6, "a1": 5},
	}, []int{0, 1, 1})
}

// The same expression must mean the same thing at every level, which is
// how the bug should have been caught in the first place.
func TestComparisonAsValueAtEveryLevel(t *testing.T) {
	for _, level := range []optimize.Level{optimize.O0, optimize.O1, optimize.O2, optimize.O3} {
		t.Run(level.String(), func(t *testing.T) {
			files := compile(t, compareSrc, level)
			h := newHarness(t, "t:v", files)
			cases := []struct {
				scale, p, want int
			}{
				{10, 5, 0},
				{3, 5, 1},
				{6, 5, 1},
			}
			sets := make([]map[string]int, len(cases))
			for i, tc := range cases {
				sets[i] = map[string]int{"a0": tc.scale, "a1": tc.p}
			}
			got := h.Calls("t:v/le_after_call", sets)
			for i, tc := range cases {
				if got[i] != tc.want {
					t.Errorf("le_after_call(%d, %d) = %d, want %d", tc.scale, tc.p, got[i], tc.want)
				}
			}
		})
	}
}
