package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// `if any<...> { }` must not lose its body.
//
// The existence test lowers to a Predicate with Exist set and Alts empty.
// Inlining folded the arm into `guarded [exists …] cmd`, and the renderer
// looked only at Cond.Alts -- so it emitted NOTHING and the body vanished,
// with no diagnostic at all. Optimisation is what made it reachable: before
// inlining, the existence test stayed a Branch and rendered correctly.
//
// An existence test also cannot become a Guarded op, because the only
// command that runs at most once is `function F if <sel>`, and an inlined op
// is not a function.
func TestAnyKeepsItsBody(t *testing.T) {
	src := "namespace t:q\n" +
		"obj x\n" +
		"[export]\nfn f() {\n" +
		"  if any<@e[tag=foo]> { x = 1 }\n" +
		"  x = 2\n" +
		"}\n"
	for _, lvl := range []optimize.Level{optimize.O0, optimize.O1, optimize.O2, optimize.O3} {
		t.Run(lvl.String(), func(t *testing.T) {
			files := compile(t, src, lvl)
			joined := ""
			for _, b := range files {
				joined += b + "\n"
			}
			if !strings.Contains(joined, "set @s x 1") {
				t.Fatalf("the body of if any<...> was dropped:\n%s", joined)
			}
			if !strings.Contains(joined, "set @s x 2") {
				t.Fatalf("the statement after the if was dropped:\n%s", joined)
			}
			// And it must be an existence test, not a fan-out.
			if !strings.Contains(joined, "if @e[tag=foo]") {
				t.Fatalf("no existence test:\n%s", joined)
			}
		})
	}
}
