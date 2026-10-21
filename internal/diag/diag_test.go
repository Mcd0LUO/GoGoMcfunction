package diag_test

import (
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/diag"
)

// A diagnostic about a whole file has no line to point at. It used to print
// as `-:`, which does not say which file the complaint is about -- and the
// author may have been compiling twenty of them.
func TestPositionPrintsTheFileWhenThereIsNoLine(t *testing.T) {
	cases := []struct {
		name string
		pos  diag.Pos
		want string
	}{
		{"nothing known", diag.Pos{}, "-"},
		{"file only", diag.Pos{File: "a.ggm"}, "a.ggm"},
		{"line only", diag.Pos{Line: 3, Col: 7}, "3:7"},
		{"file and line", diag.Pos{File: "a.ggm", Line: 3, Col: 7}, "a.ggm:3:7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.pos.String(); got != c.want {
				t.Fatalf("Pos.String() = %q, want %q", got, c.want)
			}
		})
	}
}

// Code that reads a diagnostic renders it with %s on the whole Diagnostic, so
// the file must not depend on the caller remembering to prepend it.
func TestDiagnosticCarriesTheFile(t *testing.T) {
	var bag diag.Bag
	bag.Error(diag.Pos{File: "lib.ggm"}, "E_NO_NAMESPACE", "file has no `namespace` declaration")
	d := bag.Items()[0]
	want := "lib.ggm: error: file has no `namespace` declaration [E_NO_NAMESPACE]"
	if got := d.String(); got != want {
		t.Fatalf("Diagnostic.String() = %q, want %q", got, want)
	}
}
