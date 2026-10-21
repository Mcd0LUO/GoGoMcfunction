package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// `self.<objective>` reads and writes the executing entity's score.
//
// The lowerer has accepted Field{X: SelfExpr} since it was written, but
// nothing ever built one: the parser returned a bare SelfExpr and the dot
// after it was a syntax error. So the spelling the specification used did not
// parse, while three sites in internal/lower waited for it.
//
// The whole dotted name is the objective, as it is for `seed.math`, because
// `self` can never itself be an objective name -- so the dots cannot be split
// two ways and no quoting is needed.
func TestSelfFieldReadsTheExecutingEntity(t *testing.T) {
	src := "namespace t:sf\n" +
		"obj sort.key\nobj X\n" +
		"[export]\nfn read() { X = self.sort.key }\n" +
		"[export]\nfn write() { self.sort.key = 7 }\n"
	files := compile(t, src, optimize.O2)
	if got := files["t:sf/read"]; !strings.Contains(got, "@s sort.key") {
		t.Fatalf("self.sort.key did not read the entity's objective:\n%s", got)
	}
	if got := files["t:sf/write"]; !strings.Contains(got, "@s sort.key 7") {
		t.Fatalf("self.sort.key = 7 did not write the entity's objective:\n%s", got)
	}
}

// A name that no `let`, parameter, `for` or declaration introduced is not a
// new variable.
//
// It used to be: slot() invents a binding on demand, so `while y < x` in a body
// that never declared y compiled, bound y to a fresh objective, and 1.12.2
// reads an unwritten score as 0. The condition became `0 < x` -- measured on
// the engine, and nothing said a word. It is the same shape of bug that the
// specification's own sqrt example carried.
func TestUndeclaredNameIsReported(t *testing.T) {
	src := "namespace t:ud\n" +
		"obj X\n" +
		"[export]\nfn f() {\n" +
		"  let x = X\n" +
		"  while y < x, budget 8 { x = x - 1 }\n" +
		"}\n"
	if codes := compileDiags(src); !hasCode(codes, "E_UNDEFINED") {
		t.Fatalf("an undeclared name was accepted silently; got %v", codes)
	}
}

// The check must not fire on a name that IS declared, or every program fails.
func TestDeclaredNamesCompile(t *testing.T) {
	src := "namespace t:ok\n" +
		"obj X\n" +
		"fn g(a: int) -> int { a * 2 }\n" +
		"[export]\nfn f() {\n" +
		"  let x = X\n" +
		"  let n = g(x)\n" +
		"  for i in 0..n { x = x + i }\n" +
		"  X = x\n" +
		"}\n"
	if codes := compileDiags(src); len(codes) != 0 {
		t.Fatalf("a fully declared body was rejected: %v", codes)
	}
}
