package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// A [load] body must not call something that needs an entity, however deep.
//
// The lowerer refuses `x = 1` written directly in a [load] body, but the rule
// stopped at the call. `[load] fn on_load() { worker() }` with
// `fn worker() { N = 1 }` compiled cleanly, and 1.12.2 discarded the set
// because no entity is bound: the program did nothing at world load and said
// nothing about it.
func TestLoadBodyCannotReachASelfUser(t *testing.T) {
	direct := "namespace t:x\nobj N\n" +
		"[export]\nfn worker() { N = 1 }\n" +
		"[load]\nfn on_load() { worker() }\n"
	if got := diagCodes(direct); !contains(got, "E_NO_SELF_CONTEXT") {
		t.Fatalf("a [load] body reached @s through a call: %v", got)
	}

	// Transitive: the helper is two calls away.
	deep := "namespace t:y\nobj N\n" +
		"[export]\nfn deep() { N = 1 }\n" +
		"[export]\nfn mid() { deep() }\n" +
		"[load]\nfn on_load() { mid() }\n"
	if got := diagCodes(deep); !contains(got, "E_NO_SELF_CONTEXT") {
		t.Fatalf("the check did not follow two calls: %v", got)
	}

	// And a [load] body that stays on a fake player is still fine.
	ok := "namespace t:z\nextern G: int = seed.G\n" +
		"[load]\nfn on_load() { G = 1 }\n"
	if got := diagCodes(ok); contains(got, "E_NO_SELF_CONTEXT") {
		t.Fatalf("a [load] body that never touches @s was rejected: %v", got)
	}
}

// A failed call reports one diagnostic, not two.
//
// The call returned a zero Loc with a nil error, so the enclosing expression
// kept building ops around a location that does not exist. That produced a
// second message -- "op with invalid location {Holder: Obj:}" -- which names
// an internal struct instead of the author's mistake.
func TestUnknownCalleeReportsOnce(t *testing.T) {
	src := "namespace t:a\nobj X\n" +
		"[export]\nfn main() { X = math/sqrt(4) }\n"
	got := diagCodes(src)
	if !contains(got, "E_UNKNOWN_CALLEE") {
		t.Fatalf("an unknown callee was accepted: %v", got)
	}
	for _, c := range got {
		if c == "E_IR_INVARIANT" || c == "E_EXPR" {
			t.Fatalf("one mistake produced extra diagnostics %v", got)
		}
	}
}

// A bare path call is division, and must stay division.
//
// `math/sqrt(4)` and `a / b` are the same token sequence when `b` is a call.
// Teaching the parser to read `/` as a path separator silently turned every
// division into a call to a function named `a/b`, breaking the whole
// language to make one spelling work.
func TestDivisionIsNotAPath(t *testing.T) {
	src := "namespace t:d\nobj X\nobj A\nobj B\n" +
		"[export]\nfn f() { X = A / B }\n"
	files := compile(t, src, optimize.O2)
	body := files["t:d/f"]
	if !strings.Contains(body, "operation @s X /= @s B") {
		t.Fatalf("division did not compile to a division:\n%s", body)
	}
}
