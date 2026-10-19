package driver_test

import (
	"fmt"
	"sort"
	"testing"

	"cmcf/internal/driver"
	"cmcf/internal/optimize"
	"cmcf/internal/sim"
	"cmcf/internal/target"
)

// compile lowers a source string and fails the test on any error.
func compile(t *testing.T, src string, level optimize.Level) map[string]string {
	t.Helper()
	out, diags := driver.Compile(src, "test.cmcf", driver.Options{Target: target.V1_12_2, Level: level})
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

// harness runs generated functions on a real 1.12.2 engine.
//
// Every function cmcf emits reads and writes through @s, and 1.12.2
// gives @s no value unless the caller supplies an executor. A test that
// calls a function straight from the console therefore observes nothing
// at all, so the harness summons a marker armor stand and enters the
// function through it, the way a command block would.
type harness struct {
	t     *testing.T
	world *sim.World
	ns    string
	// tag marks the entity that carries @s.
	tag string
}

const markerTag = "CMCF"

func newHarness(t *testing.T, ns string, files map[string]string) *harness {
	t.Helper()
	w, err := sim.NewWorld(files)
	if err != nil {
		t.Skipf("gomc runtime unavailable: %v", err)
	}
	t.Cleanup(w.Close)
	return &harness{t: t, world: w, ns: ns, tag: markerTag}
}

// Call runs one function with @s bound to a fresh entity and reads back
// what it left in the `return` objective.
//
// The result is copied onto the fake player R. Reading it off the entity
// instead would mean depending on the runtime's entity naming, which is
// an implementation detail rather than part of the language contract.
func (h *harness) Call(fn string, args map[string]int) (int, *sim.State) {
	h.t.Helper()
	setup := []string{
		"function " + h.ns + "/init",
		// One entity per call, so a leftover score cannot be mistaken
		// for a result this call produced.
		"kill @e[tag=" + h.tag + "]",
		"summon armor_stand 0 0 0 {Marker:1b}",
		"scoreboard players tag @e[type=armor_stand] add " + h.tag,
	}
	for _, k := range sortedKeys(args) {
		setup = append(setup, fmt.Sprintf("scoreboard players set @e[tag=%s] %s %d", h.tag, k, args[k]))
	}
	st, err := h.world.Run(setup, []string{
		fmt.Sprintf("execute @e[tag=%s] ~ ~ ~ function %s", h.tag, fn),
		fmt.Sprintf("scoreboard players operation R return = @e[tag=%s] return", h.tag),
	})
	if err != nil {
		h.t.Fatalf("run %s: %v", fn, err)
	}
	if !st.Has("return", "R") {
		h.t.Fatalf("run %s: no result; the function never wrote @s return\nstate: %v\nerrors: %v",
			fn, st.Keys(), st.Errors)
	}
	return st.Get("return", "R"), st
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Deterministic setup order keeps a failure reproducible.
	sort.Strings(out)
	return out
}
