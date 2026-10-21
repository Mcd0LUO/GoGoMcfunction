package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// The NBT codec writes objectives, so those writes must be declared.
//
// The bit ladder and the enum cases are emitted as Raw commands, and
// usedObjectives only saw objectives that appeared in a typed op. `decode X
// from ... into` emits no typed op at all -- only the ladder -- so the
// objective was never declared, 1.12.2 discarded every add, and the value
// stayed 0 with no diagnostic. `into` is the spelling the reference data
// pack uses.
func TestDecodeIntoDeclaresItsObjective(t *testing.T) {
	src := "namespace t:d\n" +
		"obj legacy.AtkBase\n" +
		"nbt f = p\"tag.dat.AtkBase\" : bits 3\n" +
		"[export]\nfn g() { [add] decode legacy.AtkBase from @s.Inventory(9..9)[f] }\n"
	files := compile(t, src, optimize.O2)
	init, ok := files["t:d/init"]
	if !ok {
		t.Fatalf("no init; files: %v", keysOf(files))
	}
	if !strings.Contains(init, "objectives add legacy.AtkBase") {
		t.Fatalf("[add] decode wrote an objective it never declared:\n%s", init)
	}
}

// A [load] body cannot test a score, and must say so.
//
// Every guard is a selector, and a [load] body runs from a command block
// where @s names nothing. The old code emitted `execute
// @s[score_t0.ggm_min=1] ~ ~ ~ ...` anyway; the engine reported "Entity
// '@s[score_t0.ggm_min=1]' cannot be found" and skipped it, so every
// comparison inside [load] was silently false.
func TestLoadBodyRejectsConditions(t *testing.T) {
	src := "namespace t:l\n" +
		"extern G: int = seed.G\n" +
		"[load]\nfn on_load() {\n" +
		"  let x = 5\n" +
		"  if x > 3 { G = 1 }\n" +
		"}\n"
	if got := diagCodes(src); !contains(got, "E_COND") {
		t.Fatalf("a condition in [load] was accepted: %v", got)
	}
}

// A namespace without a domain is rejected at the source, not in the writer.
//
// `namespace big` produced ids like `big/boot`, which 1.12.2 cannot resolve
// -- so `boot` never reached `init`, no objective was declared, and every
// scoreboard write was discarded. Worse, `-check` reported success and only
// `-out` failed, with a message blaming the compiler.
func TestNamespaceNeedsADomain(t *testing.T) {
	src := "namespace big\nobj X\n[export]\nfn f() { X = 1 }\n"
	if got := diagCodes(src); !contains(got, "E_NO_NAMESPACE") {
		t.Fatalf("a namespace with no domain was accepted: %v", got)
	}
}
