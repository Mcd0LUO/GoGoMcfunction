package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/driver"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/target"
)

// The objective an extern binds is checked where it is written.
//
// 1.12.2 truncates an objective name at 16 characters, so a 25-character one
// collides with anything sharing that prefix. Nothing checked the binding, and
// the first complaint came from the structural invariant -- with no line
// number, in the vocabulary of compiler internals, which reads as a fault in
// the compiler rather than in the program.
func TestExternBoundObjectiveIsCheckedAtItsSource(t *testing.T) {
	src := "namespace t:lo\nobj X\nextern LONG: int = num.SearchMax.math.operation1\n[export]\nfn f() { X = LONG }\n"
	_, diags := driver.Compile(src, "a.ggm", driver.Options{Target: target.V1_12_2, Level: optimize.O1})

	var found int
	for _, d := range diags {
		switch d.Code {
		case "E_OBJECTIVE_NAME":
			found++
			if d.Pos.Line != 3 {
				t.Fatalf("reported at line %d, want 3 (the extern declaration)", d.Pos.Line)
			}
			if !strings.Contains(d.Msg, "SearchMax.math.operation1") {
				t.Fatalf("the message does not name the objective: %s", d.Msg)
			}
		case "E_IR_INVARIANT":
			t.Fatalf("the same mistake was ALSO reported as a compiler invariant, "+
				"which carries no line and blames the compiler: %s", d.Msg)
		}
	}
	if found != 1 {
		t.Fatalf("got %d E_OBJECTIVE_NAME diagnostics, want 1", found)
	}
}

// A binding whose objective name fits must stay silent.
func TestLegalExternObjectiveIsSilent(t *testing.T) {
	src := "namespace t:lo\nobj X\nextern SHORT: int = num.Hits\n[export]\nfn f() { X = SHORT }\n"
	for _, d := range compileDiags(src) {
		if strings.HasPrefix(d, "E_") {
			t.Fatalf("a legal binding was rejected: %s", d)
		}
	}
}
