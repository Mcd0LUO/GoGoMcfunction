package driver_test

import (
	"fmt"
	"testing"

	"cmcf/internal/driver"
	"cmcf/internal/optimize"
	"cmcf/internal/sim"
	"cmcf/internal/target"
)

func compile(t *testing.T, src string, level optimize.Level) map[string]string {
	t.Helper()
	out, diags := driver.Compile(src, "test.cmcf", driver.Options{Target: target.V1_12_2, Level: level})
	for _, d := range diags {
		if d.Severity == 0 {
			t.Fatalf("compile: %v", d)
		}
	}
	if out == nil {
		t.Fatal("no output")
	}
	return out.Files
}

func newWorld(t *testing.T, files map[string]string) *sim.World {
	t.Helper()
	w, err := sim.NewWorld(files)
	if err != nil {
		t.Skipf("gomc runtime unavailable: %v", err)
	}
	t.Cleanup(w.Close)
	return w
}

func isqrt(n int) int {
	x := 0
	for (x+1)*(x+1) <= n {
		x++
	}
	return x
}

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

// sqrt evaluated one input per world, so nothing can clobber the result.
func TestSqrtMatchesMath(t *testing.T) {
	files := compile(t, sqrtSrc, optimize.O2)
	for _, n := range []int{0, 1, 2, 3, 4, 5, 8, 15, 24, 100, 999, 10000, 40000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			w := newWorld(t, files)
			st, err := w.Run(
				[]string{
					"function t:x/init",
					fmt.Sprintf("scoreboard players set P a0 %d", n),
				},
				[]string{"function t:x/sqrt"},
			)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			got := st.Get("return", "P")
			want := isqrt(n)
			if got != want {
				t.Fatalf("sqrt(%d) = %d, want %d\nstate: %v", n, got, want, st.Keys())
			}
		})
	}
}

// Behaviour must not depend on the optimisation level.
func TestSqrtAllLevels(t *testing.T) {
	for _, level := range []optimize.Level{optimize.O0, optimize.O1, optimize.O2, optimize.O3} {
		t.Run(level.String(), func(t *testing.T) {
			files := compile(t, sqrtSrc, level)
			for _, n := range []int{0, 4, 8, 24, 999} {
				w := newWorld(t, files)
				st, err := w.Run(
					[]string{
						"function t:x/init",
						fmt.Sprintf("scoreboard players set P a0 %d", n),
					},
					[]string{"function t:x/sqrt"},
				)
				if err != nil {
					t.Fatalf("run: %v", err)
				}
				if got, want := st.Get("return", "P"), isqrt(n); got != want {
					t.Fatalf("sqrt(%d) = %d, want %d", n, got, want)
				}
			}
		})
	}
}