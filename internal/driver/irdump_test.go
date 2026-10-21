package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/driver"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/target"
)

// The IR dump must show the shape the back end was given.
//
// It exists because a wrong answer read out of generated .mcfunction is
// guesswork: a merged block and an inlined one produce similar commands from
// very different CFGs, and which one happened decides whether an optimisation
// is a bug.
func TestIRDumpShowsBlocksAndOps(t *testing.T) {
	src := "namespace t:a\nobj x\nobj y\n[export]\nfn f() {\n  if x == 1 { y = 1 }\n}\n"
	out, diags := driver.CompileFiles([]driver.Source{{Name: "a.ggm", Text: src}}, driver.Options{
		Target: target.V1_12_2,
		Level:  optimize.O0,
	})
	for _, d := range diags {
		if d.Severity == 2 {
			t.Fatalf("compile: %s", d.String())
		}
	}
	if out == nil || out.IR == nil {
		t.Fatal("no IR retained on the output")
	}
	dump := out.IR.DumpString()
	for _, want := range []string{
		"fn t:a/f",
		"b0:",
		"branch",
		"set @s y = 1",
	} {
		if !strings.Contains(dump, want) {
			t.Fatalf("dump is missing %q:\n%s", want, dump)
		}
	}
}

// A run of independent one-line ifs must not cost a file each.
//
// Inlining a then-arm left `branch c then=b else=b`, which the back end
// rendered as `function b if c` AND `function b unless c` -- calling the join
// twice and costing an extra line per if. Three ifs came to 7 files and 23
// lines against the data pack's 9 lines.
func TestIndependentIfsCollapse(t *testing.T) {
	src := "namespace t:a\nobj x\nobj y\n[export]\nfn f() {\n" +
		"  if x == 1 { y = 1 }\n" +
		"  if x == 2 { y = 2 }\n" +
		"  if x == 3 { y = 3 }\n" +
		"}\n"
	files := compile(t, src, optimize.O2)
	if len(files) > 4 {
		t.Fatalf("3 independent ifs produced %d files: %v", len(files), keysOf(files))
	}
	total := 0
	for _, b := range files {
		total += strings.Count(b, "\n")
	}
	// 2 lines to build the condition and 1 to act on it, per if, plus the
	// constant pool. The data pack writes each as a single
	// `execute … if …`, which 1.12.2 allows only for a function call, so three
	// lines per if is the floor for a body that is not a whole function.
	if total > 18 {
		t.Fatalf("3 independent ifs produced %d lines", total)
	}
	// And no block may be called twice for one condition.
	for name, body := range files {
		if strings.Contains(body, "if @s[score_t0") && strings.Contains(body, "unless @s[score_t0") {
			t.Fatalf("%s calls the same join both ways:\n%s", name, body)
		}
	}
}
