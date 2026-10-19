package driver_test

import (
	"fmt"
	"testing"

	"cmcf/internal/optimize"
)

func isqrt(n int) int {
	x := 0
	for (x+1)*(x+1) <= n {
		x++
	}
	return x
}

// sqrtSrc is the loop shape the hand-written luo:math library uses,
// with the termination test the library gets wrong: comparing against
// the previous estimate stops before the fixed point.
const sqrtSrc = `namespace t:x
fn sqrt(n: int) -> int {
  if n < 2 { return n }
  let x = n
  let y = (x + 1) / 2
  while y < x #[budget(64)] {
    x = y
    y = (x + n / x) / 2
  }
  x
}`

func TestSqrtMatchesMath(t *testing.T) {
	files := compile(t, sqrtSrc, optimize.O2)
	h := newHarness(t, "t:x", files)
	for _, n := range []int{0, 1, 2, 3, 4, 5, 8, 15, 24, 100, 999, 10000, 40000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			got, _ := h.Call("t:x/sqrt", map[string]int{"a0": n})
			if want := isqrt(n); got != want {
				t.Fatalf("sqrt(%d) = %d, want %d", n, got, want)
			}
		})
	}
}

// Behaviour must not depend on the optimisation level.
//
// This is the property that makes an optimisation pass safe to enable at
// all: a pass that changes what a function computes is a bug, however
// many commands it saves.
func TestSqrtAllLevels(t *testing.T) {
	for _, level := range []optimize.Level{optimize.O0, optimize.O1, optimize.O2, optimize.O3} {
		t.Run(level.String(), func(t *testing.T) {
			files := compile(t, sqrtSrc, level)
			h := newHarness(t, "t:x", files)
			for _, n := range []int{0, 4, 8, 24, 999} {
				got, _ := h.Call("t:x/sqrt", map[string]int{"a0": n})
				if want := isqrt(n); got != want {
					t.Fatalf("sqrt(%d) = %d, want %d", n, got, want)
				}
			}
		})
	}
}
