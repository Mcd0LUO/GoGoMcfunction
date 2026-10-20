package driver_test

import (
	"testing"

	"github.com/Mcd0LUO/CelesteaMcfunction/internal/optimize"
)

// A scoreboard value is a signed 32-bit integer that wraps silently.
// The compiler must therefore emit arithmetic that agrees with 32-bit
// wrapping at every intermediate step, not with exact integer maths and
// not with 64-bit maths.
//
// The LCG is the case that exposes the difference: A*s alone exceeds
// int32, so the order of the wrap and the modulo decides the result.
const lcgSrc = `namespace t:r
extern var a0: int @ "a0"

const LCG_A = 1103515245
const LCG_C = 1234
const LCG_M = 67108864

#[wrapping]
fn advance(s: int) -> int {
  (LCG_A * s + LCG_C) % LCG_M
}
`

// refStep is the same recurrence evaluated in 32-bit arithmetic.
//
// Written with explicit int32 conversions so the wrap happens exactly
// where the runtime's does: after the multiply, after the add, and the
// modulo truncates toward zero rather than flooring.
func refStep(s int32) int32 {
	const A, C, M int32 = 1103515245, 1234, 67108864
	return (A*s + C) % M
}

func TestWrapArithmeticMatchesInt32(t *testing.T) {
	files := compile(t, lcgSrc, optimize.O2)
	h := newHarness(t, "t:r", files)
	seeds := []int{0, 1, 2, 7, 12345, 67108863, 1103515245, 2147483647, -1, -12345, -2147483648}
	for _, s := range seeds {
		got, _ := h.Call("t:r/advance", map[string]int{"a0": s})
		want := int(refStep(int32(s)))
		if got != want {
			t.Errorf("step(%d) = %d, want %d", s, got, want)
		}
	}
}

// 1.12.2's % truncates toward zero, so a modulus can return a negative
// value. That is why the hand-written luo:math library negates the
// result by hand before mapping it into a range, and why CMCF's rand
// uses rem_euclid instead of %.
//
// Without this, a test that only ever saw non-negative results would
// pass just as happily against a flooring modulo.
func TestModuloTruncatesTowardZero(t *testing.T) {
	files := compile(t, lcgSrc, optimize.O2)
	h := newHarness(t, "t:r", files)

	got, _ := h.Call("t:r/advance", map[string]int{"a0": 12345})
	want := int(refStep(12345))
	if want >= 0 {
		t.Fatalf("the reference value for seed 12345 is %d; the case is no longer interesting", want)
	}
	if got != want {
		t.Fatalf("advance(12345) = %d, want %d (truncated, not floored)", got, want)
	}
	if floored := want - 67108864; got == floored {
		t.Fatalf("advance(12345) = %d: the modulo floors, but 1.12.2 truncates", got)
	}
}

// The same source must compute the same thing at every level. An
// optimisation that reassociates or folds wrapping arithmetic would show
// up here and nowhere else.
func TestWrapArithmeticAtEveryLevel(t *testing.T) {
	for _, level := range []optimize.Level{optimize.O0, optimize.O1, optimize.O2, optimize.O3} {
		t.Run(level.String(), func(t *testing.T) {
			files := compile(t, lcgSrc, level)
			h := newHarness(t, "t:r", files)
			for _, s := range []int{0, 12345, 67108863, 2147483647, -2147483648} {
				got, _ := h.Call("t:r/advance", map[string]int{"a0": s})
				if want := int(refStep(int32(s))); got != want {
					t.Errorf("step(%d) = %d, want %d", s, got, want)
				}
			}
		})
	}
}
