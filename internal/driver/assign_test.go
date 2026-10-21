package driver_test

import (
	"strings"
	"testing"
)

// Assigning to a name nobody declared is a silent no-op: it gets a fresh
// local, and nothing reads it. The READ side already refuses the same name;
// the write side used to accept it, which is the shape this compiler exists
// to reject.
func TestUndeclaredAssignmentIsReported(t *testing.T) {
	src := "namespace t:wa\nobj X\n[export]\nfn f() { X = 1  ghost = 2 }\n"
	var found bool
	for _, c := range compileDiags(src) {
		if strings.HasPrefix(c, "E_UNDEFINED") && strings.Contains(c, "ghost") {
			found = true
		}
	}
	if !found {
		t.Fatal("an assignment to an undeclared name was accepted")
	}
}

// The check must not fire on a declared target, or every program fails.
func TestDeclaredAssignmentTargetsCompile(t *testing.T) {
	src := "namespace t:wb\nobj X\nextern E: int = @s.e\n[export]\nfn f(a: int) {\n" +
		"  let b = a\n  X = b\n  E = a\n  b += 1\n  X += b\n}\n"
	for _, c := range compileDiags(src) {
		if strings.HasPrefix(c, "E_") {
			t.Fatalf("a declared assignment target was rejected: %s", c)
		}
	}
}
