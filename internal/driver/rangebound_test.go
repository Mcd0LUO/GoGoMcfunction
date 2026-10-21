package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// A two-sided range must keep both bounds through -O2.
//
// `inlineLeafArms` handed the branch's own predicate to the Guarded it
// created, so one Alts array was reachable from two ops. applyRename
// rewrote the condition once for each of them, and the second pass saw
// names the first had already replaced:
//
//	[t1 <= 0 and t3 >= 0]
//	-> [t0 <= 0 and t1 >= 0]      correct
//	-> [t0 <= 0 and t0 >= 0]      t1 rewritten as if it were t3
//
// The range collapsed to `t0 == 0`, which is a different condition.
// Measured on the engine, `W += 1; if W <= -9 && W >= -100` set X only when
// W was exactly -9, where the author's condition also accepts -50 and -10.
func TestRangeKeepsBothBounds(t *testing.T) {
	src := "namespace t:rt\n" +
		"extern W: int = w.计算\n" +
		"obj X\n" +
		"[export]\nfn f() {\n" +
		"  W += 1\n" +
		"  if W <= -9 && W >= -100 { X = 1 }\n" +
		"}\n"
	for _, lvl := range []optimize.Level{optimize.O0, optimize.O1, optimize.O2, optimize.O3} {
		t.Run(lvl.String(), func(t *testing.T) {
			body := compile(t, src, lvl)["t:rt/f"]
			seen := false
			for _, line := range strings.Split(body, "\n") {
				if !strings.Contains(line, "@s[") {
					continue
				}
				seen = true
				var objs []string
				inside := line[strings.Index(line, "[")+1:]
				inside = inside[:strings.Index(inside, "]")]
				for _, cond := range strings.Split(inside, ",") {
					cond = strings.TrimSpace(cond)
					cond = strings.TrimPrefix(cond, "score_")
					if i := strings.Index(cond, "_min="); i >= 0 {
						objs = append(objs, cond[:i])
					} else if i := strings.LastIndex(cond, "="); i >= 0 {
						objs = append(objs, cond[:i])
					}
				}
				if len(objs) != 2 {
					t.Fatalf("expected a two-sided guard, got %d conditions:\n  %s", len(objs), line)
				}
				if objs[0] == objs[1] {
					t.Fatalf("both bounds collapsed onto %s, so the range became equality:\n  %s", objs[0], line)
				}
			}
			if !seen {
				t.Fatalf("no guard was emitted:\n%s", body)
			}
		})
	}
}
