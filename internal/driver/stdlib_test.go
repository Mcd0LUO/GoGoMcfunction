package driver_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/driver"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/target"
)

// compileModule compiles several sources into one module, the way the CLI does.
func compileModule(t *testing.T, srcs []driver.Source, level optimize.Level) map[string]string {
	t.Helper()
	out, diags := driver.CompileFiles(srcs, driver.Options{Target: target.V1_12_2, Level: level})
	for _, d := range diags {
		if d.Severity == 0 {
			t.Fatalf("compile: %v", d)
		}
	}
	if out == nil {
		t.Fatal("no output")
	}
	return out.Files
}

// readStdlib loads stdlib/math.ggm from the repository.
func readStdlib(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "stdlib", "math.ggm"))
	if err != nil {
		t.Fatalf("read stdlib: %v", err)
	}
	return string(b)
}

// A standard library is only a library if a caller in another namespace can
// reach it. That needs two things at once: the call must resolve, and the
// callee must survive into the output -- a library compiled on its own looks
// unreachable from the inside.
func TestStdlibLinksAcrossNamespaces(t *testing.T) {
	files := compileModule(t, []driver.Source{
		{Name: "app.ggm", Text: `namespace t:app
fn run() -> int { std:math/sqrt(144) }
`},
		{Name: "stdlib/math.ggm", Text: readStdlib(t)},
	}, optimize.O2)

	var sawCall, sawSqrt bool
	for name, body := range files {
		if name == "std:math/sqrt" {
			sawSqrt = true
		}
		if strings.Contains(body, "function std:math/sqrt") {
			sawCall = true
		}
	}
	if !sawSqrt {
		t.Errorf("std:math/sqrt is missing from the output; the library was pruned")
	}
	if !sawCall {
		t.Errorf("no function calls std:math/sqrt; files: %v", keysOf(files))
	}
}

// sqrt is the one function whose answer can be checked exactly against Go's
// integer arithmetic, so it anchors the library on the runtime.
func TestStdlibSqrtOnRealRuntime(t *testing.T) {
	files := compileModule(t, []driver.Source{
		{Name: "app.ggm", Text: `namespace t:app

extern a0: int = @s.a0

fn run() -> int {
  std:math/sqrt(a0)
}
`},
		{Name: "stdlib/math.ggm", Text: readStdlib(t)},
	}, optimize.O2)
	h := newHarness(t, "t:app", files)

	for _, n := range []int{0, 1, 2, 3, 4, 15, 16, 17, 99, 100, 1000, 9999, 1000000, 2147483647} {
		n := n
		want := isqrt(n)
		got, _ := h.Call("t:app/run", map[string]int{"a0": n})
		if got != want {
			t.Errorf("sqrt(%d) = %d, want %d", n, got, want)
		}
	}
}

// rand must stay inside the requested range and must actually vary: a broken
// seed advance returns the same value forever, which a range check alone would
// not catch.
func TestStdlibRandOnRealRuntime(t *testing.T) {
	files := compileModule(t, []driver.Source{
		{Name: "app.ggm", Text: `namespace t:app

extern a0: int = @s.a0
extern a1: int = @s.a1

fn run() -> int {
  std:math/rand(a0, a1)
}
`},
		{Name: "stdlib/math.ggm", Text: readStdlib(t)},
	}, optimize.O2)
	h := newHarness(t, "t:app", files)

	lo, hi := 1, 6
	seen := map[int]bool{}
	results := h.CallSeq("", "t:app/run", []map[string]int{
		{"a0": lo, "a1": hi}, {"a0": lo, "a1": hi}, {"a0": lo, "a1": hi},
		{"a0": lo, "a1": hi}, {"a0": lo, "a1": hi}, {"a0": lo, "a1": hi},
		{"a0": lo, "a1": hi}, {"a0": lo, "a1": hi},
	})
	for _, v := range results {
		if v < lo || v > hi {
			t.Errorf("rand(%d,%d) = %d, outside the range", lo, hi, v)
		}
		seen[v] = true
	}
	if len(seen) < 2 {
		t.Errorf("rand returned %v eight times; the seed is not advancing", results)
	}
}

// The example libraries are shipped code, so they get the same treatment as
// stdlib: run on the engine and compared against an independent answer.
//
// This test exists because the sqrt these examples used to contain was wrong
// in two ways that nothing caught -- the golden baseline only compares output
// against output, and the semantic test next door exercised a different
// spelling of the same function. A baseline agrees with whatever it was
// blessed from; only an independent expectation can disagree with it.
func TestExampleSqrtOnRealRuntime(t *testing.T) {
	for _, name := range []string{"luo-math.ggm", "luo-math-full.ggm"} {
		t.Run(name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("..", "..", "examples", name))
			if err != nil {
				t.Skipf("read example: %v", err)
			}
			files := compile(t, string(body), optimize.O2)
			h := newHarness(t, "luo:math", files)

			// 0 exercises the case the old guard returned 1 for; 99 the one
			// the oscillating test settled on 10 for; MaxInt32 the one the
			// (x+1)/2 seed wrapped on.
			for _, n := range []int{0, 1, 2, 3, 4, 8, 15, 16, 99, 100, 999, 10000, 46339, 46340, 2147483647} {
				got, _ := h.Call("luo:math/sqrt", map[string]int{"a0": n})
				if want := isqrt(n); got != want {
					t.Errorf("sqrt(%d) = %d, want %d", n, got, want)
				}
			}
		})
	}
}
