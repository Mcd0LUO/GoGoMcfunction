package driver_test

import (
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/driver"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/target"
)

// A pre-parsed source must produce the SAME diagnostics as a source that is
// parsed inside the driver.
//
// The language server hands over its cached AST to avoid parsing twice; if that
// path behaved differently, the editor would show a different program than
// `ggm -check` does.
func TestParsedSourceMatchesTextSource(t *testing.T) {
	src := "namespace t:p\nobj X\n[export]\nfn one() { X = two() }\n"
	opts := driver.Options{Target: target.V1_12_2, Level: optimize.O2}

	_, fromText := driver.CompileFiles([]driver.Source{{Name: "p.ggm", Text: src}}, opts)

	f, err := syntax.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, fromAST := driver.CompileFiles([]driver.Source{{Name: "p.ggm", Text: src, Parsed: f}}, opts)

	if len(fromText) != len(fromAST) {
		t.Fatalf("text gave %d diagnostics, parsed AST gave %d", len(fromText), len(fromAST))
	}
	for i := range fromText {
		if fromText[i].Code != fromAST[i].Code {
			t.Fatalf("diagnostic %d: text %s, AST %s", i, fromText[i].Code, fromAST[i].Code)
		}
	}
}
