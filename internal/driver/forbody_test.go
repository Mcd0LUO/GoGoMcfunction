package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// Each `for e in entities<...>` body must become its OWN function.
//
// The path was built from the enclosing function's block count, which is not
// unique: a for whose body is a single function emits no blocks, so the next
// loop in the same function saw the same count and got the same name. Three
// loops in one function all became `main$b1`, each NewFunc replaced the last,
// and the final body then ran for all three -- a wrong answer with no
// diagnostic, because a duplicate function name is a silent overwrite.
func TestEachForBodyGetsItsOwnFunction(t *testing.T) {
	src := "namespace t:f\n" +
		"obj a\nobj b\nobj c\n" +
		"[export]\nfn run() {\n" +
		"  for e in entities<@e[tag=x]> { a = 1 }\n" +
		"  for e in entities<@e[tag=y]> { b = 2 }\n" +
		"  for e in entities<@e[tag=z]> { c = 3 }\n" +
		"}\n"
	files := compile(t, src, optimize.O2)

	// Each body must exist once and assign its own objective.
	for _, want := range []struct {
		obj string
		val string
	}{
		{"a", "1"}, {"b", "2"}, {"c", "3"},
	} {
		found := false
		for _, body := range files {
			if strings.Contains(body, "set @s "+want.obj+" "+want.val) {
				found = true
			}
		}
		if !found {
			t.Fatalf("no function assigns %s = %s; files: %v", want.obj, want.val, keysOf(files))
		}
	}

	// And the three loop calls must name three DIFFERENT functions.
	main := files["t:f/run"]
	var targets []string
	for _, line := range strings.Split(main, "\n") {
		if i := strings.Index(line, "~ ~ ~ function "); i >= 0 {
			targets = append(targets, strings.TrimSpace(line[i+len("~ ~ ~ function "):]))
		}
	}
	if len(targets) != 3 {
		t.Fatalf("want 3 fan-out calls, got %d:\n%s", len(targets), main)
	}
	seen := map[string]bool{}
	for _, tg := range targets {
		if seen[tg] {
			t.Fatalf("two loops share the body %q:\n%s", tg, main)
		}
		seen[tg] = true
	}
}

// A selector's score can be read but not assigned to.
//
// 1.12.2 allows a selector on the right of a scoreboard operation and not on
// the left, so `@p[tag=t]."X" = 1` has no implementation. It must say so
// rather than parse the dot as something else.
func TestSelectorScoreIsReadOnly(t *testing.T) {
	src := "namespace t:f\n" +
		"obj X\n" +
		"[export]\nfn run() { @p[tag=t].\"X\" = 1 }\n"
	if got := diagCodes(src); !contains(got, "E_PARSE") {
		t.Fatalf("writing a selector score was accepted: %v", got)
	}
}

// The read direction works and renders the selector as the holder.
func TestSelectorScoreReads(t *testing.T) {
	src := "namespace t:f\n" +
		"obj X\nobj Y\n" +
		"[export]\nfn run() { X = @p[tag=t].\"Y\" }\n"
	files := compile(t, src, optimize.O2)
	body := files["t:f/run"]
	if !strings.Contains(body, "= @p[tag=t] Y") {
		t.Fatalf("the selector was not used as the holder:\n%s", body)
	}
}
