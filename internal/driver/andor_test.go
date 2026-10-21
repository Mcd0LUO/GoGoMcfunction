package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// `a && b` must become ONE selector, not nested blocks.
//
// 1.12.2 selectors express a conjunction as comma-separated arguments, which
// is exactly the shape the reference data pack writes:
// @s[score_menu_min=1,score_menu=99] IS menu >= 1 && menu <= 99.
//
// Without this every interval condition had to be nested ifs, which cost a
// block each and could silently invert before the guard fix.
func TestAndBecomesOneSelector(t *testing.T) {
	src := "namespace t:v\n" +
		"obj menu\nobj X\n" +
		"[export]\nfn f() {\n" +
		"  if menu >= 1 && menu <= 99 { X = 1 }\n" +
		"}\n"
	files := compile(t, src, optimize.O2)
	body := files["t:v/f"]
	// Temporaries carry the compiler's suffix: t0.ggm, t1.ggm.
	if !strings.Contains(body, "score_t0.ggm_min=0") || !strings.Contains(body, "score_t1.ggm=0") {
		t.Fatalf("both conditions must be in one selector:\n%s", body)
	}
	// One guarded command, not two calls.
	if n := strings.Count(body, "scoreboard players set @s X 1"); n != 1 {
		t.Fatalf("the body must appear once, got %d:\n%s", n, body)
	}
}

// `a || b` must become two guarded calls, one per alternative.
func TestOrBecomesTwoAlternatives(t *testing.T) {
	src := "namespace t:v\n" +
		"obj menu\nobj X\n" +
		"[export]\nfn g() {\n" +
		"  if menu == 1 || menu == 2 { X = 2 }\n" +
		"}\n"
	files := compile(t, src, optimize.O2)
	body := files["t:v/g"]
	if n := strings.Count(body, "scoreboard players set @s X 2"); n != 2 {
		t.Fatalf("each alternative needs its own guarded call, got %d:\n%s", n, body)
	}
}
