package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// `decode … into` adds to an objective without resetting it first.
//
// The reference data pack needs this shape: it resets five objectives once, then
// eighteen files each add one slot's worth. Plain decodes cannot express it,
// because each one resets -- so only the last statement's contribution survives
// and the answer is quietly the third slot's alone.
func TestDecodeIntoAccumulates(t *testing.T) {
	src := "namespace t:i\n" +
		"obj hp\n" +
		"nbt f = p\"tag.dat.f\" : bits 3\n" +
		"[export]\nfn reset() { hp = 0 }\n" +
		"[export]\nfn slot9() { [add] decode hp from @s.Inventory(9..9)[f] }\n" +
		"[export]\nfn slot10() { [add] decode hp from @s.Inventory(10..10)[f] }\n" +
		"[export]\nfn all() { reset(); slot9(); slot10() }\n"
	files := compile(t, src, optimize.O2)

	// An into-decode must not reset.
	for name, body := range files {
		if strings.HasSuffix(name, "/reset") || strings.HasSuffix(name, "/all") {
			continue
		}
		if strings.Contains(body, "scoreboard players set @s hp 0") {
			t.Fatalf("%s resets although it is an into-decode:\n%s", name, body)
		}
	}

	// Exactly one reset survives, and it is the explicit one.
	resets := 0
	for _, body := range files {
		resets += strings.Count(body, "scoreboard players set @s hp 0")
	}
	if resets != 1 {
		t.Fatalf("want exactly one reset (the explicit one), got %d", resets)
	}
}

// A plain decode still resets, so the accumulator and the resetter stay distinct.
func TestDecodeWithoutIntoResets(t *testing.T) {
	src := "namespace t:i\n" +
		"obj hp\n" +
		"nbt f = p\"tag.dat.f\" : bits 3\n" +
		"[export]\nfn slot9() { decode hp from @s.Inventory(9..9)[f] }\n"
	files := compile(t, src, optimize.O2)
	body := files["t:i/slot9"]
	if !strings.HasPrefix(strings.TrimSpace(body), "scoreboard players set @s hp 0") {
		t.Fatalf("a plain decode must reset first:\n%s", body)
	}
}
