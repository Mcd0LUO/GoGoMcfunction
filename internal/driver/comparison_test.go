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
extern a0: int = @s.a0
extern a1: int = @s.a1

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
	pairs := [][2]int{{3, 5}, {5, 5}, {7, 5}, {-1, 0}, {0, -1}}
	for _, op := range []string{"lt", "le", "eq", "ne", "ge", "gt"} {
		sets := make([]map[string]int, len(pairs))
		for i, p := range pairs {
			sets[i] = map[string]int{"a0": p[0], "a1": p[1]}
		}
		got := h.Calls("t:o/"+op, sets)
		for i, pair := range pairs {
			t.Run(fmt.Sprintf("%s/%d,%d", op, pair[0], pair[1]), func(t *testing.T) {
				if exp := want[op](pair[0], pair[1]); got[i] != exp {
					t.Fatalf("%s(%d, %d) = %d, want %d", op, pair[0], pair[1], got[i], exp)
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
			pairs := [][2]int{{3, 5}, {5, 5}, {7, 5}}
			sets := make([]map[string]int, len(pairs))
			for i, p := range pairs {
				sets[i] = map[string]int{"a0": p[0], "a1": p[1]}
			}
			ne := h.Calls("t:o/ne", sets)
			eq := h.Calls("t:o/eq", sets)
			for i, pair := range pairs {
				if ne[i] != btoi(pair[0] != pair[1]) {
					t.Fatalf("ne(%d, %d) = %d", pair[0], pair[1], ne[i])
				}
				if eq[i] != btoi(pair[0] == pair[1]) {
					t.Fatalf("eq(%d, %d) = %d", pair[0], pair[1], eq[i])
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
