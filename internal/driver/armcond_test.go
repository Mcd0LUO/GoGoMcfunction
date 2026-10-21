package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// A branch whose two arms both stay separate must not give either arm the
// slot its own condition is tested on.
//
// renderBranch emits the else call before the then call -- the `unless` has to
// run while the condition still holds. Both name the same condition slot, so
// if the else arm writes it, the then call tests a value the arm just made:
//
//	copy t0 = a; t0 -= 1
//	function f$b2 unless @s[score_t0_min=0]   <- else arm runs, writes t0
//	function f$b1 if @s[score_t0_min=0]       <- tests the NEW t0
//
// Measured on the engine before the fix: `if a >= 1 { r = helper(a) } else
// { r = helper(a + 1) }` gave r = 2 at -O0 and r = 0 at -O2 for a = 0. The
// else arm computed 2, then the then call fired as well and overwrote it.
//
// The premise is that BOTH arms stay separate. When an arm can be inlined its
// ops move inside the guard and run after the condition was consumed, which
// is why a nested `if` may legitimately write the outer condition slot.
func TestSeparateArmsDoNotShareTheConditionSlot(t *testing.T) {
	src := "namespace t:ibc\n" +
		"obj a\nobj r\n" +
		"fn helper(x: int) -> int { x * 2 }\n" +
		"[export]\nfn f() {\n" +
		"  r = 0\n" +
		"  if a >= 1 { r = helper(a) } else { r = helper(a + 1) }\n" +
		"}\n"
	for _, lvl := range []optimize.Level{optimize.O2, optimize.O3} {
		t.Run(lvl.String(), func(t *testing.T) {
			files := compile(t, src, lvl)
			// The condition slot is whatever the branch block computes last
			// before the two calls. Read it from the guard on the calls.
			body := files["t:ibc/f"]
			var guardObjs []string
			for _, line := range strings.Split(body, "\n") {
				if !strings.Contains(line, "function t:ibc/f$b") {
					continue
				}
				inside := line[strings.Index(line, "[")+1:]
				inside = inside[:strings.Index(inside, "]")]
				for _, cond := range strings.Split(inside, ",") {
					cond = strings.TrimPrefix(strings.TrimSpace(cond), "score_")
					if i := strings.Index(cond, "_min="); i >= 0 {
						guardObjs = append(guardObjs, cond[:i])
					} else if i := strings.LastIndex(cond, "="); i >= 0 {
						guardObjs = append(guardObjs, cond[:i])
					}
				}
			}
			if len(guardObjs) == 0 {
				t.Fatalf("no guarded call to the arms:\n%s", body)
			}
			condObj := guardObjs[0]
			for _, arm := range []string{"t:ibc/f$b1", "t:ibc/f$b2"} {
				armBody, ok := files[arm]
				if !ok {
					continue
				}
				for _, line := range strings.Split(armBody, "\n") {
					// An unconditional write to the condition slot in an arm
					// is exactly the bug: it lands before the other call
					// reads that slot.
					if strings.Contains(line, " "+condObj+" ") &&
						(strings.Contains(line, " set ") || strings.Contains(line, " operation ")) &&
						!strings.Contains(line, "execute ") {
						t.Fatalf("%s writes the condition slot %s unconditionally:\n  %s\nfull f:\n%s",
							arm, condObj, line, body)
					}
				}
			}
		})
	}
}
