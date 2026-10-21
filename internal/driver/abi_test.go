package driver_test

import (
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// A nested call marshals its arguments into a0..a(n-1), which are the
// same objectives this function's own parameters arrived in. Any
// parameter still needed after the call must therefore be moved out of
// the ABI file first, or the call silently overwrites it.
//
// The bug this locks: chance(scale, p) called rand(1, scale), which set
// a1 = scale, and the comparison then read a1 back as p -- answering
// rand <= scale.
const abiSrc = `namespace t:p
extern a0: int = @s.a0
extern a1: int = @s.a1
extern a2: int = @s.a2

fn add3(x: int, y: int, z: int) -> int {
  x + y + z
}

fn keep(a: int, b: int, c: int) -> int {
  let r = add3(1, 2, 3)
  (r * 1000000) + (a * 10000) + (b * 100) + c
}
`

// Each parameter lands in a distinguishable digit position, so a value
// that survives the call as the wrong parameter changes the answer
// instead of cancelling out.
func TestParametersSurviveNestedCall(t *testing.T) {
	files := compile(t, abiSrc, optimize.O2)
	h := newHarness(t, "t:p", files)

	const r = 6 // add3(1, 2, 3)
	cases := []struct{ a, b, c int }{
		{4, 5, 6},
		{0, 0, 0},
		{9, 9, 9},
		{100, 1, 1},
	}
	sets := make([]map[string]int, len(cases))
	for i, tc := range cases {
		sets[i] = map[string]int{"a0": tc.a, "a1": tc.b, "a2": tc.c}
	}
	got := h.Calls("t:p/keep", sets)
	for i, tc := range cases {
		want := r*1000000 + tc.a*10000 + tc.b*100 + tc.c
		if got[i] != want {
			t.Errorf("keep(%d, %d, %d) = %d, want %d", tc.a, tc.b, tc.c, got[i], want)
		}
	}
}

// The same property must hold at every level. This is the kind of bug an
// optimisation pass can reintroduce by coalescing a spilled local back
// into the ABI register it was copied from.
func TestParametersSurviveNestedCallAtEveryLevel(t *testing.T) {
	for _, level := range []optimize.Level{optimize.O0, optimize.O1, optimize.O2, optimize.O3} {
		t.Run(level.String(), func(t *testing.T) {
			files := compile(t, abiSrc, level)
			h := newHarness(t, "t:p", files)
			got, _ := h.Call("t:p/keep", map[string]int{"a0": 4, "a1": 5, "a2": 6})
			if want := 6*1000000 + 4*10000 + 5*100 + 6; got != want {
				t.Fatalf("keep(4, 5, 6) = %d, want %d", got, want)
			}
		})
	}
}
