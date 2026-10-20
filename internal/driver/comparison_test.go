package driver_test

import (
	"fmt"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// All six comparison operators must produce the right answer for every
// ordering. != was silently unimplemented: it parsed, then stopped the
// build with "unsupported comparison" the moment anyone used it, because
// target.CondFor could only return ONE Condition and != needs two.
const opsSrc = `namespace t:o
extern var a0: int @ "a0"
extern var a1: int @ "a1"

fn lt(x: int, y: int) -> int { if x < y { return 1 } 0 }
fn le(x: int, y: int) -> int { if x <= y { return 1 } 0 }
fn eq(x: int, y: int) -> int { if x == y { return 1 } 0 }
fn ne(x: int, y: int) -> int { if x != y { return 1 } 0 }
fn ge(x: int, y: int) -> int { if x >= y { return 1 } 0 }
fn gt(x: int, y: int) -> int { if x > y { return 1 } 0 }
`

func TestEveryComparisonOperator(t *testing.T) {
	files := compile(t, opsSrc, optimize.O2)
	h := newHarness(t, "t:o", files)
	want := map[string]func(a, b int) int{
		"lt": func(a, b int) int { return btoi(a < b) },
		"le": func(a, b int) int { return btoi(a <= b) },
		"eq": func(a, b int) int { return btoi(a == b) },
		"ne": func(a, b int) int { return btoi(a != b) },
		"ge": func(a, b int) int { return btoi(a >= b) },
		"gt": func(a, b int) int { return btoi(a > b) },
	}
	for _, op := range []string{"lt", "le", "eq", "ne", "ge", "gt"} {
		for _, pair := range [][2]int{{3, 5}, {5, 5}, {7, 5}, {-1, 0}, {0, -1}} {
			t.Run(fmt.Sprintf("%s/%d,%d", op, pair[0], pair[1]), func(t *testing.T) {
				got, _ := h.Call("t:o/"+op, map[string]int{"a0": pair[0], "a1": pair[1]})
				if exp := want[op](pair[0], pair[1]); got != exp {
					t.Fatalf("%s(%d, %d) = %d, want %d", op, pair[0], pair[1], got, exp)
				}
			})
		}
	}
}

// The if/unless rendering keeps a single-conjunction branch to two
// commands. It must be a rendering change only: the answers above are the
// contract, and this checks the same table at every optimisation level so
// a change to the shape cannot slip past on one level.
func TestComparisonOperatorsAtEveryLevel(t *testing.T) {
	for _, level := range []optimize.Level{optimize.O0, optimize.O1, optimize.O2, optimize.O3} {
		t.Run(level.String(), func(t *testing.T) {
			files := compile(t, opsSrc, level)
			h := newHarness(t, "t:o", files)
			for _, pair := range [][2]int{{3, 5}, {5, 5}, {7, 5}} {
				if got, _ := h.Call("t:o/ne", map[string]int{"a0": pair[0], "a1": pair[1]}); got != btoi(pair[0] != pair[1]) {
					t.Fatalf("ne(%d, %d) = %d", pair[0], pair[1], got)
				}
				if got, _ := h.Call("t:o/eq", map[string]int{"a0": pair[0], "a1": pair[1]}); got != btoi(pair[0] == pair[1]) {
					t.Fatalf("eq(%d, %d) = %d", pair[0], pair[1], got)
				}
			}
		})
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
