package driver_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/sim"
)

// Stepping the engine: the module's cycle must advance a counter once per call.
//
// The test calls <ns>/cycle BARE, which is exactly how gameLoopFunction calls
// it: no executor. That is the whole point -- an earlier version of this test
// wrapped the call in `execute @e[tag=TICK]` and so supplied the @s the real
// game does not, hiding that every @s write in the entry was being discarded.
//
// cycle therefore does no arithmetic itself. It summons the clock entity and
// runs the body inside `execute @e[tag=ggm.clock]`, where @s exists.
func TestCycleAdvancesOnRealRuntime(t *testing.T) {
	const src = `namespace t:s

obj ticks
on every 1tick { ticks = ticks + 1 }
`
	files := compile(t, src, optimize.O2)
	w, err := sim.NewWorld(files)
	if err != nil {
		t.Skipf("gomc runtime unavailable: %v", err)
	}
	defer w.Close()

	run := []string{"function t:s/boot"}
	for i := 1; i <= 3; i++ {
		run = append(run, "function t:s/cycle")
		run = append(run, fmt.Sprintf("scoreboard players operation R%d ticks = @e[tag=ggm.clock] ticks", i))
	}
	st, err := w.Run([]string{"function t:s/init"}, run)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for want := 1; want <= 3; want++ {
		if got := st.Get("ticks", fmt.Sprintf("R%d", want)); got != want {
			t.Errorf("after %d tick(s) the counter is %d, want %d", want, got, want)
		}
	}
}

// A body that has no executor must be refused, not silently emitted.
//
// 1.12.2 has no load event: the world-load function is reached from a command
// block, where @s names nothing. `scoreboard players set @s counter 7` is
// valid syntax that does nothing, and nothing reports it -- so the compiler
// has to.
func TestOnLoadRefusesEntityScores(t *testing.T) {
	msgs := compileDiags(`namespace t:s

obj counter
on load { counter = 7 }
`)
	if !hasCode(msgs, "E_NO_SELF_CONTEXT") {
		t.Fatalf("want E_NO_SELF_CONTEXT, got %v", msgs)
	}
}

// The same body is fine against a fake player: that holder exists in every
// context, which is what makes `on load` able to do anything at all.
func TestOnLoadAcceptsFakePlayerGlobals(t *testing.T) {
	files := compile(t, `namespace t:s

extern SEED: int = seed.ticks
on load { SEED = 12345 }
`, optimize.O2)

	body := files["t:s/load"]
	if !strings.Contains(body, "scoreboard players set seed ticks 12345") {
		t.Fatalf("the fake-player write is missing:\n%s", body)
	}
}

// boot is the one function the user wires. It has to reach the file's on-load
// body -- a library's initialisation is otherwise never run, and nothing
// would say so.
func TestBootReachesTheOnLoadBody(t *testing.T) {
	files := compile(t, `namespace t:app
on load { seed.a = 1 }
`, optimize.O2)
	if got := files["t:app/boot"]; !strings.Contains(got, "function t:app/load") {
		t.Fatalf("boot does not call the on-load body:\n%s", got)
	}
}
