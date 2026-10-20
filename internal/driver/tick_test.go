package driver_test

import (
	"fmt"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/sim"
)

// Stepping the engine: a tick-driven counter must advance once per call.
//
// The function is entered through an executor, because that is the contract:
// every expression temporary lives on @s, so even a function that only touches
// a fake-player global needs an @s to compute in. Calling it straight from the
// console makes @s undefined and every operation silently no-ops.
//
// `run` executes in one world, so calling the tick function repeatedly inside
// it is genuine elapsed state, not independent snapshots.
func TestOnTickCounterAdvancesOnRealRuntime(t *testing.T) {
	const src = `namespace t:s

obj ticks
on load { seed.ticks = 0 }
on tick { seed.ticks = seed.ticks + 1 }
`
	files := compile(t, src, optimize.O2)
	w, err := sim.NewWorld(files)
	if err != nil {
		t.Skipf("gomc runtime unavailable: %v", err)
	}
	defer w.Close()

	tick := []string{
		"execute @e[tag=TICK] ~ ~ ~ function t:s/tick",
	}
	run := []string{
		"execute @e[tag=TICK] ~ ~ ~ function t:s/load",
	}
	for i := 1; i <= 3; i++ {
		run = append(run, tick...)
		run = append(run, fmt.Sprintf("scoreboard players operation R%d ticks = seed ticks", i))
	}
	st, err := w.Run([]string{
		"function t:s/init",
		"kill @e[tag=TICK]",
		"summon armor_stand 0 0 0 {Marker:1b}",
		"scoreboard players tag @e[type=armor_stand] add TICK",
	}, run)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for want := 1; want <= 3; want++ {
		if got := st.Get("ticks", fmt.Sprintf("R%d", want)); got != want {
			t.Errorf("after %d tick(s) the counter is %d, want %d", want, got, want)
		}
	}
}
