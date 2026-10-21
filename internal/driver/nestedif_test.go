package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// A nested if must not let the inner condition overwrite the outer one.
//
// Both conditions compile to the same temporary, and inlining folded the
// inner if into a guard on the OUTER branch's block:
//
//	guarded [t0 >= 0] copy t0 = a
//	guarded [t0 >= 0] arith t0 -= 1
//	guarded [t0 >= 0] guarded [t0 == 0] copy b = c
//
// The first guard runs with the outer value and then overwrites t0, so the
// ops after it test the inner value while still claiming to be guarded by the
// outer. At runtime the answer inverted: a = 0 took the branch and a = 1 did
// not.
func TestNestedIfKeepsTheOuterCondition(t *testing.T) {
	src := "namespace t:n\n" +
		"obj a\nobj b\nobj c\n" +
		"[export]\nfn f() {\n" +
		"  if a >= 1 {\n" +
		"    if a == 1 { b = c }\n" +
		"  }\n" +
		"}\n"
	for _, lvl := range []optimize.Level{optimize.O1, optimize.O2, optimize.O3} {
		t.Run(lvl.String(), func(t *testing.T) {
			files := compile(t, src, lvl)
			joined := ""
			for _, b := range files {
				joined += b + "\n"
			}
			// No line may both test t0 and write it.
			for _, line := range strings.Split(joined, "\n") {
				if !strings.Contains(line, "score_#t0") {
					continue
				}
				if strings.Contains(line, " #t0 ") || strings.HasSuffix(line, " #t0") {
					t.Fatalf("a guarded command writes the register its own guard reads:\n  %s\n\n%s", line, joined)
				}
			}
		})
	}
}

// Every objective a Guarded op touches must still be declared.
//
// usedObjectives had `case ir.Guarded: // handled below by recursion` and no
// recursion. An objective that appeared only inside a guarded op was never
// declared, and 1.12.2 discards a command that writes an undeclared one.
func TestGuardedObjectivesAreDeclared(t *testing.T) {
	// b and c are DECLARED here on purpose. An undeclared name is a local,
	// which is a different (and correct) path -- the bug was that a declared
	// objective reached only through a guard never got its declaration.
	src := "namespace t:n\n" +
		"obj a\nobj b\nobj c\n" +
		"[export]\nfn f() {\n" +
		"  if a >= 1 { b = c }\n" +
		"}\n"
	files := compile(t, src, optimize.O2)
	init := files["t:n/init"]
	for _, want := range []string{" b ", " c "} {
		if !strings.Contains(init, want) {
			t.Fatalf("objective %q is never declared:\n%s", strings.TrimSpace(want), init)
		}
	}
}
