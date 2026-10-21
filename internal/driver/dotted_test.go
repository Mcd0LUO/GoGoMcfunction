package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// A dotted name is an objective if it was declared, and a holder.objective
// shorthand if it was not.
//
// The two shapes are identical, so one of them has to win. Declaration wins,
// because the reference data pack declares dozens of objectives whose names
// contain dots -- Ap.Amount, Count.残照花魄, Time.day, timer.tick -- while the
// shorthand appears twice in this repository's own examples.
//
// Before this, the shorthand was checked FIRST in both the read and the write
// path, so every one of those objectives was silently split into a holder and
// a shorter objective name. The command was valid, named a score nothing
// writes, and nothing reported a problem.
func TestDeclaredObjectiveBeatsHolderShorthand(t *testing.T) {
	src := "namespace t:d\n" +
		"obj Ap.Amount\n" +
		"obj Ap.Defence\n" +
		"obj Ap.HealthMin\n" +
		"[every(\"1tick\")]\n" +
		"fn tick() {\n" +
		"  Ap.Amount = Ap.Amount - Ap.Defence\n" +
		"  Ap.HealthMin = Ap.HealthMin - Ap.Amount\n" +
		"}\n"
	files := compile(t, src, optimize.O2)
	var body string
	for name, b := range files {
		if strings.HasSuffix(name, "/tick") {
			body = b
		}
	}
	for _, want := range []string{
		"@s Ap.Amount",
		"@s Ap.Defence",
		"@s Ap.HealthMin",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("objective %q was not kept whole:\n%s", want, body)
		}
	}
	if strings.Contains(body, " Ap Amount") {
		t.Fatalf("Ap.Amount was split into holder Ap and objective Amount:\n%s", body)
	}
}

// An UNDECLARED dotted name is still the holder.objective shorthand, so
// existing code that writes to a fake player keeps working.
func TestUndeclaredDottedNameIsTheShorthand(t *testing.T) {
	src := "namespace t:d\n" +
		"obj t\n" +
		"[every(\"1tick\")]\n" +
		"fn tick() { seed.t = 9 }\n"
	files := compile(t, src, optimize.O2)
	if body := files["t:d/tick"]; !strings.Contains(body, "scoreboard players set seed t 9") {
		t.Fatalf("seed.t did not reach the named holder:\n%s", body)
	}
}
