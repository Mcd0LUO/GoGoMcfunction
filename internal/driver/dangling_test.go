package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// Every `function X` the compiler emits must name a file it emitted.
//
// A block that emits nothing is dropped, and the calls to it are supposed to
// be dropped with it. That pruning ran ONCE, with the silent set computed
// before any call was removed -- so a block that only became empty as a
// result of pruning was still called by its parent while its own file was
// gone:
//
//	function system:果实/main$b2 unless @a[team=死亡之森]
//
// 1.12.2 skips an unknown function silently, so each of these is a command
// that costs a call and does nothing. A port produced eleven of them at -O0.
//
// The optimisation levels hid it: SimplifyCFG removes the empty blocks, so
// the bug only showed at the level a user reaches for when debugging.
func TestNoCallToAMissingFunction(t *testing.T) {
	cases := map[string]string{
		"empty arm": "namespace t:d\nobj A\nobj X\n" +
			"fn empty() { }\n" +
			"[export]\nfn f() { X = 0  if A == 1 { empty() } }\n",

		"empty arm with a guard": "namespace t:e\nobj A\nobj X\n" +
			"fn empty() { }\n" +
			"[export]\nfn f() { if A >= 1 && A <= 5 { empty() } else { empty() } }\n",

		"nested empty arms": "namespace t:n\nobj A\nobj X\n" +
			"fn empty() { }\n" +
			"[export]\nfn f() { if A == 1 { if A == 2 { empty() } } }\n",
	}
	for name, src := range cases {
		for _, lvl := range []optimize.Level{optimize.O0, optimize.O1, optimize.O2, optimize.O3} {
			t.Run(name+"/"+lvl.String(), func(t *testing.T) {
				files := compile(t, src, lvl)
				for path, body := range files {
					for _, line := range strings.Split(body, "\n") {
						if !strings.HasPrefix(line, "function ") {
							continue
						}
						name := strings.Fields(line)[1]
						if _, ok := files[name]; !ok {
							t.Errorf("%s calls %s, which was not emitted", path, name)
						}
					}
				}
			})
		}
	}
}
