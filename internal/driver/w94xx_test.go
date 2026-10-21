package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// `where` on a range for must filter the ITERATION, not be dropped.
//
// It was parsed and never read, so `for i in 0..n where c { }` ran its body
// n times with the author's filter simply absent -- no diagnostic, because a
// dropped condition is not a malformed program, it is a different one.
func TestRangeForWhereFilters(t *testing.T) {
	src := "namespace t:r\n" +
		"obj N\n" +
		"[export]\nfn f() {\n" +
		"  N = 0\n" +
		"  for i in 0..5 where i > 2 { N = N + 1 }\n" +
		"}\n"
	files := compile(t, src, optimize.O2)
	joined := ""
	for _, b := range files {
		joined += b + "\n"
	}
	// The guard must test i, and the body must be conditional.
	if !strings.Contains(joined, "N += ") && !strings.Contains(joined, "N = @s N") {
		t.Fatalf("the body is missing:\n%s", joined)
	}
	if !strings.Contains(joined, "execute @s[") {
		t.Fatalf("the body is not guarded by the where condition:\n%s", joined)
	}
}

// `where` on a fanned-out for must guard the body too.
func TestFanOutForWhereFilters(t *testing.T) {
	src := "namespace t:r\n" +
		"obj N\n" +
		"[export]\nfn f() {\n" +
		"  for e in entities<@e[tag=mob]> where N == 1 { N = N + 1 }\n" +
		"}\n"
	files := compile(t, src, optimize.O2)
	joined := ""
	for _, b := range files {
		joined += b + "\n"
	}
	if !strings.Contains(joined, "unless") && !strings.Contains(joined, "execute @s[") {
		t.Fatalf("the where condition is not applied:\n%s", joined)
	}
}

// A negated literal becomes one negative constant, not magnitude-times-minus-one.
//
// The old lowering emitted `copy x; x *= -1` for every operand, so the
// magnitude went into the constant pool. `-2147483648` then needed a
// `#2147483648` entry that no score can hold.
func TestNegatedLiteralIsOneConstant(t *testing.T) {
	src := "namespace t:n\n" +
		"obj X\nobj y\n" +
		"[export]\nfn f() { X = y * -2147483648 }\n"
	files := compile(t, src, optimize.O2)
	init := files["t:n/init"]
	if strings.Contains(init, "2147483648") {
		t.Fatalf("the constant pool holds a value no score can hold:\n%s", init)
	}
	joined := ""
	for _, b := range files {
		joined += b + "\n"
	}
	if !strings.Contains(joined, "-2147483648") {
		t.Fatalf("the value itself is gone:\n%s", joined)
	}
}

// A guarded self-assignment must be removed.
//
// SelfAssign only looked at top-level ops, and inlining wraps an arm in
// Guarded -- so `x = x` inside a guard survived as a command per iteration.
func TestGuardedSelfAssignIsRemoved(t *testing.T) {
	src := "namespace t:r\n" +
		"obj X\n" +
		"[export]\nfn f() {\n" +
		"  for i in 0..3 where X == 1 { X = X + 1 }\n" +
		"}\n"
	files := compile(t, src, optimize.O2)
	for name, b := range files {
		for _, line := range strings.Split(b, "\n") {
			if strings.Contains(line, "X = @s X") {
				t.Fatalf("%s keeps a self-assignment: %s", name, line)
			}
		}
	}
}
