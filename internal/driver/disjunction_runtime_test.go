package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// A disjunction with several alternatives must run its arm ONCE.
//
// 1.12.2 selector is a conjunction: there is no boolean OR, so an
// N-alternative predicate becomes N guarded calls -- and each of those runs
// the arm in full. When two alternatives hold at once the arm runs twice.
//
// Measured on the engine before the fix, with all four inputs true:
//
//	if (a>=10 || b>=10) && (c>=10 || d>=10) { r = r + 1 }
//
// gave r = 4 at -O0, -O1 and -O2 alike, where the author condition asks for
// one execution. It is the worst shape of bug: it compiles, it runs, and it
// is right for every body that happens to be idempotent.
//
// The back end now materialises the disjunction into a flag first, so the arm
// is entered once however many alternatives matched.
func TestDisjunctionRunsTheArmOnce(t *testing.T) {
	src := "namespace t:d4\n" +
		"obj a\nobj b\nobj c\nobj d\nobj r\n" +
		"[export]\nfn f() {\n" +
		"  r = 0\n" +
		"  if (a >= 10 || b >= 10) && (c >= 10 || d >= 10) { r = r + 1 }\n" +
		"}\n"
	for _, lvl := range []optimize.Level{optimize.O0, optimize.O1, optimize.O2, optimize.O3} {
		t.Run(lvl.String(), func(t *testing.T) {
			files := compile(t, src, lvl)
			body := files["t:d4/f"]
			var calls []string
			for _, line := range strings.Split(body, "\n") {
				if strings.Contains(line, "function t:d4/f$b1") {
					calls = append(calls, line)
				}
			}
			if len(calls) != 1 {
				t.Fatalf("the then arm is called %d times, so a multi-match runs it %d times:\n%s", len(calls), len(calls), body)
			}
			init := files["t:d4/init"]
			if !strings.Contains(init, "objectives add d.ggm") {
				t.Fatalf("the flag objective is used but never declared:\n%s", init)
			}
		})
	}
}
