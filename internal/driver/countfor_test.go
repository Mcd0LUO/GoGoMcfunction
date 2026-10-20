package driver_test

import (
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// `for i in lo..hi` is the counted loop. It is bounded by construction, so it
// carries no budget clause -- and the bound is checked before the body, so an
// empty range runs zero times rather than once.
func TestCountForOnRealRuntime(t *testing.T) {
	const src = `namespace t:c

extern a0: int = @s.a0

fn sum_to(n: int) -> int {
  let acc = 0
  for i in 1..n {
    acc = acc + i
  }
  acc
}

fn backwards(n: int) -> int {
  let acc = 0
  for i in n..1 {
    acc = acc + 1
  }
  acc
}
`
	files := compile(t, src, optimize.O2)
	h := newHarness(t, "t:c", files)

	// 1..n sums to n(n+1)/2, computed here so the expectation does not come
	// from the code under test.
	for _, n := range []int{1, 2, 3, 10, 64} {
		got, _ := h.Call("t:c/sum_to", map[string]int{"a0": n})
		if want := n * (n + 1) / 2; got != want {
			t.Errorf("sum_to(%d) = %d, want %d", n, got, want)
		}
	}
	// `for i in n..1` walks n up to 1 inclusive, so it is empty only when
	// n > 1. Counting the iterations each way pins both the empty case and
	// the inclusive upper bound -- an exclusive bound would be off by one on
	// every one of these.
	for _, tc := range []struct{ n, want int }{
		{5, 0},  // 5 > 1: empty range, not one iteration
		{2, 0},  // 2 > 1: still empty
		{1, 1},  // 1..1: exactly one
		{0, 2},  // 0..1
		{-5, 7}, // -5..1
	} {
		got, _ := h.Call("t:c/backwards", map[string]int{"a0": tc.n})
		if got != tc.want {
			t.Errorf("backwards(%d) = %d iterations, want %d", tc.n, got, tc.want)
		}
	}
}

// A counted loop must not depend on the optimisation level, for the same
// reason any other construct must not.
func TestCountForAtEveryLevel(t *testing.T) {
	const src = `namespace t:c

extern a0: int = @s.a0

fn sum_to(n: int) -> int {
  let acc = 0
  for i in 1..n {
    acc = acc + i
  }
  acc
}
`
	for _, level := range []optimize.Level{optimize.O0, optimize.O1, optimize.O2, optimize.O3} {
		t.Run(level.String(), func(t *testing.T) {
			files := compile(t, src, level)
			h := newHarness(t, "t:c", files)
			for _, n := range []int{1, 4, 9} {
				got, _ := h.Call("t:c/sum_to", map[string]int{"a0": n})
				if want := n * (n + 1) / 2; got != want {
					t.Errorf("sum_to(%d) = %d, want %d", n, got, want)
				}
			}
		})
	}
}
