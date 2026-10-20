package driver_test

import (
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// The lcg/rand chain from the luo:math port, which is the densest
// exercise of the calling convention the compiler has:
//
//   - lcg_next takes no arguments but its body passes one to lcg_step, so
//     it writes a0 through a nested call;
//   - rand holds span and lo across that call, one in an ABI register and
//     one in a local;
//   - lcg_step multiplies past int32 and must wrap.
//
// Each of those was a separate silent miscompile, and each is invisible
// in isolation.
const randSrc = `namespace t:r
extern var a0: int @ "a0"
extern var a1: int @ "a1"
extern global SEED: int @ "math" @holder "seed"

const LCG_A = 1103515245
const LCG_C = 1234
const LCG_M = 67108864

#[wrapping]
fn lcg_step(s: int) -> int {
  (LCG_A * s + LCG_C) % LCG_M
}

fn lcg_next() -> int {
  let x = lcg_step(SEED)
  SEED = SEED + LCG_C
  x
}

fn rand(lo: int, hi: int) -> int {
  let span = hi - lo + 1
  let x = lcg_next()
  lo + rem_euclid(x, span)
}

on load {
  SEED = 12345
}
`

// refRand reproduces the same recurrence in explicit 32-bit arithmetic,
// independently of the compiler: the wrap happens after the multiply and
// after the add, and the modulo truncates toward zero.
func refRand(seed, lo, hi, n int) []int {
	const A, C, M int32 = 1103515245, 1234, 67108864
	s := int32(seed)
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		step := (A*s + C) % M
		s += C
		span := int32(hi - lo + 1)
		e := step % span
		if e < 0 {
			e += span
		}
		out = append(out, lo+int(e))
	}
	return out
}

func TestRandMatchesReference(t *testing.T) {
	files := compile(t, randSrc, optimize.O2)
	h := newHarness(t, "t:r", files)

	const n = 8
	argSets := make([]map[string]int, n)
	for i := range argSets {
		argSets[i] = map[string]int{"a0": 1, "a1": 100}
	}
	got := h.CallSeq("t:r/load", "t:r/rand", argSets)
	want := refRand(12345, 1, 100, n)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rand #%d = %d, want %d\ngot  %v\nwant %v", i, got[i], want[i], got, want)
		}
	}
}

// The whole chain must behave identically at every level, which is the
// property that makes enabling an optimisation pass safe at all.
func TestRandMatchesReferenceAtEveryLevel(t *testing.T) {
	const n = 6
	for _, level := range []optimize.Level{optimize.O0, optimize.O1, optimize.O2, optimize.O3} {
		t.Run(level.String(), func(t *testing.T) {
			files := compile(t, randSrc, level)
			h := newHarness(t, "t:r", files)
			argSets := make([]map[string]int, n)
			for i := range argSets {
				argSets[i] = map[string]int{"a0": 1, "a1": 1000}
			}
			got := h.CallSeq("t:r/load", "t:r/rand", argSets)
			want := refRand(12345, 1, 1000, n)
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("rand #%d = %d, want %d\ngot  %v\nwant %v", i, got[i], want[i], got, want)
				}
			}
		})
	}
}
