package driver_test

import (
	"fmt"
	"strings"
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

// `on every N` currently generates a plain function: it fires once per call,
// with no phase gate. The specification used to claim otherwise, and this test
// pins the ACTUAL behaviour so the two cannot drift again without a failure.
//
// The phase gate is in roadmap.md with "no real use case yet" as its trigger.
// When it lands, this test is the one that must change.
func TestOnEveryFiresOncePerCall(t *testing.T) {
	const src = `namespace t:e

obj beats
on load { seed.beats = 0 }
on every 1s { seed.beats = seed.beats + 1 }
`
	files := compile(t, src, optimize.O2)
	w, err := sim.NewWorld(files)
	if err != nil {
		t.Skipf("gomc runtime unavailable: %v", err)
	}
	defer w.Close()

	// Find the generated `every` entry rather than assuming its name.
	var every string
	for name := range files {
		if strings.Contains(name, "/every/") {
			every = name
		}
	}
	if every == "" {
		t.Fatalf("no on-every entry point generated; files: %v", keysOf(files))
	}

	run := []string{"execute @e[tag=TICK] ~ ~ ~ function " + every}
	for i := 0; i < 20; i++ {
		run = append(run, "execute @e[tag=TICK] ~ ~ ~ function "+every)
	}
	run = append(run, "scoreboard players operation R beats = seed beats")
	st, err := w.Run([]string{
		"function t:e/init",
		"kill @e[tag=TICK]",
		"summon armor_stand 0 0 0 {Marker:1b}",
		"scoreboard players tag @e[type=armor_stand] add TICK",
		"execute @e[tag=TICK] ~ ~ ~ function t:e/load",
	}, run)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := st.Get("beats", "R"); got != 21 {
		t.Errorf("21 calls fired %d time(s), want 21: on every is an ordinary entry point", got)
	}
}
