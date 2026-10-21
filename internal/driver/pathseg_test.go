package driver_test

import (
	"strings"
	"testing"
)

// Function and namespace names whose segments 1.12.2 allows but a lexer
// does not hand over as single tokens.
//
// `random/1-10` is INT MINUS INT and `a/1_5` is INT IDENT, because a minus is
// an operator and an underscore starts an identifier. Reading one token per
// segment made two of the three reference corpora fail to parse.
func TestDigitAndHyphenLeadingPathSegments(t *testing.T) {
	cases := []string{
		"namespace t:seg\nfn random/1-10() { X = 1 }\n" + "obj X\n",
		"namespace t:seg\nobj X\nfn a/1_5() { X = 1 }\n",
		"namespace t:seg\nobj X\nfn 1a() { X = 1 }\n",
		"namespace t:seg/1\nobj X\n[export]\nfn f() { X = 1 }\n",
	}
	for i, src := range cases {
		// Warnings are fine -- an exported function nobody calls is normal
		// here. Only parse errors matter.
		for _, c := range compileDiags(src) {
			if strings.HasPrefix(c, "E_") {
				t.Fatalf("case %d did not parse: %s", i, c)
			}
		}
	}
}

// Adjacency is what makes the merge safe: a spaced minus is still an
// operator, so a malformed declaration stays malformed.
func TestSpacedMinusIsNotAPathSegment(t *testing.T) {
	src := "namespace t:seg\nobj X\nfn a/1 - 10() { X = 1 }\n"
	for _, c := range compileDiags(src) {
		if strings.HasPrefix(c, "E_") {
			return
		}
	}
	t.Fatal("a spaced minus was accepted as part of a function name")
}

// An objective named `return` is normal in 1.12.2 libraries, so a field
// access must accept it: the segment after a dot goes through expectName.
func TestReservedWordAsDottedName(t *testing.T) {
	src := "namespace t:seg\nobj arg1\n[export]\nfn f() { for e in entities<@e[tag=x]> { e.return = arg1 } }\n"
	for _, c := range compileDiags(src) {
		if strings.HasPrefix(c, "E_") {
			t.Fatalf("e.return did not parse: %s", c)
		}
	}
}
