package driver_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Mcd0LUO/CelesteaMcfunction/internal/driver"
	"github.com/Mcd0LUO/CelesteaMcfunction/internal/optimize"
	"github.com/Mcd0LUO/CelesteaMcfunction/internal/target"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden baseline files in testdata/golden")

// The compiler's only product is its output, so the baseline IS the
// output. Checking it in is what makes a change to the code generator
// reviewable: without it, "the output got smaller" and "the output
// silently changed meaning" look the same in a diff.
//
// Re-bless with:
//
//	go test ./internal/driver -run GoldenBaseline -update
//
// and then READ the diff. A baseline refreshed without being read is
// not a baseline.
func TestGoldenBaseline(t *testing.T) {
	examples, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.cmcf"))
	if err != nil || len(examples) == 0 {
		t.Skipf("no examples found: %v", err)
	}
	sort.Strings(examples)

	for _, src := range examples {
		name := strings.TrimSuffix(filepath.Base(src), ".cmcf")
		body, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read %s: %v", src, err)
		}
		for _, level := range []optimize.Level{optimize.O0, optimize.O1, optimize.O2, optimize.O3} {
			t.Run(fmt.Sprintf("%s/%s", name, level), func(t *testing.T) {
				got := renderBaseline(t, string(body), src, level)
				wantPath := filepath.Join("testdata", "golden",
					fmt.Sprintf("%s.%s.txt", name, strings.ToLower(level.String())))
				if *updateGolden {
					if err := os.MkdirAll(filepath.Dir(wantPath), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(wantPath, []byte(got), 0o644); err != nil {
						t.Fatal(err)
					}
					return
				}
				want, err := os.ReadFile(wantPath)
				if err != nil {
					t.Fatalf("no baseline at %s; run with -update to create it: %v", wantPath, err)
				}
				if got != string(want) {
					t.Errorf("output differs from the baseline in %s.\n%s",
						wantPath, firstDifference(string(want), got))
				}
			})
		}
	}
}

// renderBaseline compiles one source and renders every generated file in
// a stable, diffable form.
func renderBaseline(t *testing.T, src, filename string, level optimize.Level) string {
	t.Helper()
	out, diags := driver.Compile(src, filename, driver.Options{Target: target.V1_12_2, Level: level})
	for _, d := range diags {
		if d.Severity == 0 {
			t.Fatalf("compile %s at %s: %v", filename, level, d)
		}
	}
	if out == nil {
		t.Fatalf("compile %s at %s produced no output", filename, level)
	}
	paths := make([]string, 0, len(out.Files))
	for p := range out.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	fmt.Fprintf(&b, "# files=%d commands=%d\n\n", len(out.Files), out.CountLines())
	for _, p := range paths {
		fmt.Fprintf(&b, "=== %s ===\n%s\n", p, strings.TrimRight(out.Files[p], "\n"))
	}
	return b.String()
}

// firstDifference reports the first differing line, which is almost
// always more useful than "the strings are not equal".
func firstDifference(want, got string) string {
	wl := strings.Split(want, "\n")
	gl := strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return fmt.Sprintf("first difference at line %d:\n  baseline: %q\n  current:  %q", i+1, w, g)
		}
	}
	return "the rendered strings differ but no line does"
}
