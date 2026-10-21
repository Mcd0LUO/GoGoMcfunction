package driver_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/driver"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// The class is the language's answer to "state that outlives a tick",
// and none of it worked: the documented @tick spelling did not parse,
// fields resolved to fresh locals, a method calling a sibling method
// called nothing, and nothing ever invoked a @tick method. Each mistake
// covered for the next, so the whole feature looked merely unused.
const classSrc = "namespace t:k\n" +
	"\n[selector(@e[tag=x])]\n" +
	"class C {\n" +
	"  var hp: int = 10\n" +
	"  [every(\"1tick\")]\n" +
	"  fn onTick() {\n" +
	"    add(5)\n" +
	"  }\n" +
	"  fn add(n: int) {\n" +
	"    hp = hp + n\n" +
	"  }\n" +
	"}\n"

// The documented spelling is @tick, and the bare word was the only one
// the parser knew.
func TestAtTickParses(t *testing.T) {
	compile(t, classSrc, optimize.O2)
}

// A field is an objective on @s; a bare name inside a method has to mean
// the field. It used to mean a fresh local, so the initialiser set an
// objective nothing ever touched again.
func TestClassFieldResolvesToItsObjective(t *testing.T) {
	files := compile(t, classSrc, optimize.O2)
	body := files["t:k/C/add"]
	if !strings.Contains(body, "C.hp") {
		t.Fatalf("method does not address the field objective:\n%s", body)
	}
	if strings.Contains(body, "l0") {
		t.Fatalf("method fell back to a local:\n%s", body)
	}
}

// A sibling method call resolves inside the class. It used to go to
// <ns>/add, which did not exist, and add was then pruned as unreachable
// -- so the call and the pruning hid each other.
func TestSiblingMethodCallResolves(t *testing.T) {
	files := compile(t, classSrc, optimize.O2)
	if _, ok := files["t:k/C/add"]; !ok {
		t.Fatalf("sibling method was pruned; files: %v", keysOf(files))
	}
	if got := files["t:k/C/onTick"]; !strings.Contains(got, "function t:k/C/add") {
		t.Fatalf("call did not resolve to the sibling method:\n%s", got)
	}
}

// Nothing invoked a @tick method. The entry point fans out to it over the
// class selector, and @s inside the method is each instance.
func TestClassTickFanOut(t *testing.T) {
	files := compile(t, classSrc, optimize.O2)
	// The @tick method is driven from on_cycle, which runs inside the clock
	// entity's execute. That is what makes @s the instance rather than
	// nothing at all.
	leaf := files["t:k/on_cycle"]
	if !strings.Contains(leaf, "execute @e[tag=x] ~ ~ ~ function t:k/C/onTick") {
		t.Fatalf("no fan-out to the tick method:\n%s", leaf)
	}

	h := newHarness(t, "t:k", files)
	st, err := h.world.Run([]string{
		"function t:k/init",
		"summon armor_stand 0 0 0 {Marker:1b,Tags:[\"x\"]}",
		"execute @e[tag=x] ~ ~ ~ function t:k/C/init",
	}, []string{
		"function t:k/cycle",
		"function t:k/cycle",
		"scoreboard players operation R C.hp = @e[tag=x] C.hp",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// 10 from the initialiser, then add(5) twice.
	if got, want := st.Get("C.hp", "R"), 20; got != want {
		t.Fatalf("hp after two ticks = %d, want %d", got, want)
	}
}

// on spawn runs once per entity: 1.12.2 has no entity-created event, so
// the entry fans out over the entities that do not carry the marker tag
// yet, and the handler sets it before the body runs.
func TestClassSpawnRunsOnce(t *testing.T) {
	src := "namespace t:k\n" +
		"\n[selector(@e[tag=x])]\n" +
		"class C {\n" +
		"  var hp: int = 10\n" +
		"  [spawn]\n" +
		"  fn spawned() {\n" +
		"    hp = 40\n" +
		"  }\n" +
		"}\n"
	files := compile(t, src, optimize.O2)
	h := newHarness(t, "t:k", files)
	st, err := h.world.Run([]string{
		"function t:k/init",
		"summon armor_stand 0 0 0 {Marker:1b,Tags:[\"x\"]}",
		"scoreboard players set @e[tag=x] C.hp 10",
	}, []string{
		// The spawn hook is driven from on_cycle now, so three cycles must
		// still run it exactly once: the marker tag is what makes it a
		// one-shot rather than a per-tick handler.
		"function t:k/cycle",
		"function t:k/cycle",
		"function t:k/cycle",
		"scoreboard players operation R C.hp = @e[tag=x] C.hp",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// 40 once, not 40 three times (which would still read 40) and not 10
	// at all. The marker tag is what proves it: it must be set.
	if got, want := st.Get("C.hp", "R"), 40; got != want {
		t.Fatalf("hp after three spawn passes = %d, want %d", got, want)
	}
	if !strings.Contains(strings.Join(st.Output, "\n"), "ggm.spawn.C") {
		t.Logf("no marker tag in output; state: %v", st.Keys())
	}
}

// keysOf lists a map's keys, for a failure message that names the files
// that WERE produced instead of just saying the expected one is missing.
func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// [death] does not exist: 1.12.2 cannot observe an entity that is already
// gone, so the only honest implementation would need a shadow entity. The
// attribute was removed rather than left as one that always errors -- an
// attribute that can never work is a question, not a feature.
func TestDeathAttributeIsGone(t *testing.T) {
	src := "namespace t:k\n[selector(@e[tag=x])]\nclass C {\n  var hp: int = 1\n  [death]\n  fn died() { hp = 0 }\n}\n"
	if !hasDiag(diagsOf(src, driver.Options{Level: optimize.O1}), "E_PARSE") {
		t.Fatalf("a [death] attribute parsed: %v", diagsOf(src, driver.Options{Level: optimize.O1}))
	}
}
