package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// A constant's materialisation must not be guarded.
//
// `x *= -1` lowers to `t = -1; x *= t`, and inlining wrapped BOTH ops in the
// branch guard. When the guard failed, `t` kept whatever it held from an
// earlier iteration and the multiply used a stale value.
//
// Measured on the engine: the reference data pack's
// rpg/monster/hurt/operation/light gives Ap.Amount = 3 for (Defence 10,
// Amount 3); -O2 gave 7. Both now agree, which is what this checks.
func TestConstantIsNotGuarded(t *testing.T) {
	src := "namespace t:l\n" +
		"obj Ap.Amount\nobj Ap.Defence\n" +
		"[export]\nfn light() {\n" +
		"  Ap.Defence -= Ap.Amount\n" +
		"  if Ap.Defence <= -1 { Ap.Amount = Ap.Defence }\n" +
		"  if Ap.Amount <= -1 { Ap.Amount *= -1 }\n" +
		"}\n"
	for _, lvl := range []optimize.Level{optimize.O2, optimize.O3} {
		t.Run(lvl.String(), func(t *testing.T) {
			files := compile(t, src, lvl)
			for name, body := range files {
				for _, line := range strings.Split(body, "\n") {
					// A guarded `set ... = -1` is the bug: the value is needed
					// unconditionally by the op that follows it.
					if strings.Contains(line, "execute @s[") && strings.Contains(line, "set @s t") && strings.Contains(line, " -1") {
						t.Fatalf("%s guards a constant materialisation:\n  %s", name, line)
					}
				}
			}
		})
	}
}
