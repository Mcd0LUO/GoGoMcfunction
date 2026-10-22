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

// A reserved word cannot name a function.
//
// `fn step()` parsed, and then every `step(...)` was a syntax error because the
// call position reads that word as the loop keyword. Declaring something
// uncallable is a trap: the author sees the declaration accepted and concludes
// the call syntax must be wrong.
func TestKeywordCannotNameAFunction(t *testing.T) {
	for _, word := range []string{"step", "if", "loop", "self"} {
		src := "namespace t:kw\nobj X\n[export]\nfn " + word + "() { X = 1 }\n"
		diags := compileDiags(src)
		var reported bool
		for _, d := range diags {
			if strings.HasPrefix(d, "E_PARSE") && strings.Contains(d, "reserved word") {
				reported = true
			}
		}
		if !reported {
			t.Fatalf("%q was accepted as a function name: %v", word, diags)
		}
	}
}

// A namespace segment may be a reserved word: the reference packs have
// `rpg:bug/else`, and a namespace never appears in an expression.
func TestKeywordMayNameANamespaceSegment(t *testing.T) {
	src := "namespace rpg:bug/else\nobj X\n[export]\nfn f() { X = 1 }\n"
	for _, d := range compileDiags(src) {
		if strings.HasPrefix(d, "E_") {
			t.Fatalf("a keyword namespace segment was rejected: %s", d)
		}
	}
}
