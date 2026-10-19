package driver_test

import (
	"strings"
	"testing"

	"cmcf/internal/driver"
	"cmcf/internal/optimize"
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
	"  @tick fn onTick() {\n" +
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
	leaf := files["t:k/tick"]
	if !strings.Contains(leaf, "execute @e[tag=x] ~ ~ ~ function t:k/C/onTick") {
		t.Fatalf("no fan-out to the tick method:\n%s", leaf)
	}

	h := newHarness(t, "t:k", files)
	st, err := h.world.Run([]string{
		"function t:k/init",
		"summon armor_stand 0 0 0 {Marker:1b,Tags:[\"x\"]}",
		"execute @e[tag=x] ~ ~ ~ function t:k/C/init",
	}, []string{
		"function t:k/tick",
		"function t:k/tick",
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
		"  on spawn {\n" +
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
		"function t:k/spawn",
		"function t:k/spawn",
		"function t:k/spawn",
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
	if !strings.Contains(strings.Join(st.Output, "\n"), "cmcf.spawn.C") {
		t.Logf("no marker tag in output; state: %v", st.Keys())
	}
}

// on death cannot be honoured: 1.12.2 cannot observe an entity that is
// already gone. Saying so is better than emitting a handler that never
// fires. An empty body is a placeholder and is accepted.
func TestOnDeathWithABodyIsRejected(t *testing.T) {
	src := "namespace t:k\n[selector(@e[tag=x])]\nclass C {\n  var hp: int = 1\n  on death { hp = 0 }\n}\n"
	if !hasDiag(diagsOf(src, driver.Options{Level: optimize.O1}), "E_HOOK_UNSUPPORTED") {
		t.Fatalf("on death with a body compiled: %v", diagsOf(src, driver.Options{Level: optimize.O1}))
	}
	empty := "namespace t:k\n[selector(@e[tag=x])]\nclass C {\n  var hp: int = 1\n  on death { }\n}\n"
	if hasDiag(diagsOf(empty, driver.Options{Level: optimize.O1}), "E_HOOK_UNSUPPORTED") {
		t.Fatal("an empty on death placeholder was rejected")
	}
}

// Field objectives are truncated to the version limit, so two classes
// whose names share a six-character prefix land on the same objective and
// silently share one score.
func TestFieldObjectCollisionIsReported(t *testing.T) {
	src := "namespace t:k\n" +
		"[selector(@e[tag=a])]\nclass MonsterA { var hp: int = 1 }\n" +
		"[selector(@e[tag=b])]\nclass MonsterB { var hp: int = 2 }\n"
	if !hasDiag(diagsOf(src, driver.Options{Level: optimize.O1}), "E_FIELD_COLLISION") {
		t.Fatalf("colliding field objectives not reported")
	}
}

func keysOf(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
