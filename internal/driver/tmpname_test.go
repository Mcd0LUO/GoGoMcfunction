package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// An author's objective must not be mistaken for a compiler temporary.
//
// Temporaries were named t0, t1, ... and isTempName recognised exactly that
// shape. So `obj t1` was treated as scratch: the coalescer renamed it and
// merged it with a real temporary, and at -O2 the function that assigned it
// was dropped entirely -- no diagnostic, no crash.
//
// Temporaries now carry a prefix no identifier can begin with.
func TestAuthorObjectiveNamedTNIsNotATemporary(t *testing.T) {
	src := "namespace t:v\n" +
		"obj t1\nobj qq\n" +
		"[export]\nfn write() { t1 = 5 }\n" +
		"[export]\nfn read() { qq = t1 }\n"
	files := compile(t, src, optimize.O2)
	write, ok := files["t:v/write"]
	if !ok {
		t.Fatalf("the function assigning t1 was dropped; files: %v", keysOf(files))
	}
	if !strings.Contains(write, "set @s t1 5") {
		t.Fatalf("t1 was renamed away from the author's objective:\n%s", write)
	}
	init := files["t:v/init"]
	if !strings.Contains(init, "objectives add t1 ") {
		t.Fatalf("t1 is not declared under its own name:\n%s", init)
	}
}

// `decode` into an objective reached only through a guard still declares it.
func TestStatsClearTakesThreeArguments(t *testing.T) {
	src := "namespace t:v\n" +
		"[export]\nfn f() { stats(@s, \"clear\", \"AffectedItems\") }\n"
	files := compile(t, src, optimize.O2)
	body := files["t:v/f"]
	want := "stats entity @s clear AffectedItems"
	if strings.TrimSpace(body) != want {
		t.Fatalf("clear must not name a receiving objective:\n got  %s\n want %s", strings.TrimSpace(body), want)
	}
}

// `stats set` still needs the receiving objective, or the command is a no-op.
func TestStatsSetStillNeedsAnObjective(t *testing.T) {
	src := "namespace t:v\n" +
		"[export]\nfn f() { stats(@s, \"set\", \"SuccessCount\") }\n"
	if got := diagCodes(src); !contains(got, "E_ARG") {
		t.Fatalf("stats set without an objective was accepted: %v", got)
	}
}
