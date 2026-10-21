package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// An exported function must produce a file even when it emits nothing.
//
// `[export] fn main() { helper() }` where helper is empty produced NO
// main.mcfunction: pruning empty bodies is right for a private helper, but an
// entry point is wired by name, so the user was told to connect a function
// that does not exist -- and 1.12.2 treats that as a silent no-op.
func TestExportedFunctionIsAlwaysEmitted(t *testing.T) {
	src := "namespace t:e\n" +
		"[export]\nfn main() { }\n"
	files := compile(t, src, optimize.O2)
	if _, ok := files["t:e/main"]; !ok {
		t.Fatalf("an exported entry point must have a file; files: %v", keysOf(files))
	}
}

// A function name may be a path, and the directory stays in the id.
//
// The reference data pack organises functions by directory and the directory
// is part of the id, so flattening it makes a rewritten file unreachable from
// the rest of the pack.
func TestFunctionNameMayBeAPath(t *testing.T) {
	src := "namespace rpg:player\n" +
		"obj X\n" +
		"[export]\nfn atk/main_weapon() { X = 1 }\n"
	files := compile(t, src, optimize.O2)
	if _, ok := files["rpg:player/atk/main_weapon"]; !ok {
		t.Fatalf("the path was flattened; files: %v", keysOf(files))
	}
}

// An inventory slot may be negative: the offhand is -106.
func TestNegativeInventorySlot(t *testing.T) {
	src := "namespace t:d\n" +
		"obj X\n" +
		"nbt f = p\"tag.dat.f\" : bits 2\n" +
		"[export]\nfn g() { decode X from @s.Inventory(-106..-106)[f] }\n"
	files := compile(t, src, optimize.O2)
	body := files["t:d/g"]
	if !strings.Contains(body, "Slot:-106b") {
		t.Fatalf("the offhand slot was not emitted:\n%s", body)
	}
}

// `!cond` compiles, and a bare existence test uses the exact `unless` form.
func TestNegationOfACondition(t *testing.T) {
	src := "namespace t:n\n" +
		"obj a\nobj X\n" +
		"[export]\nfn f() { if !(a >= 1) { X = 1 } }\n" +
		"[export]\nfn g() { if !any<@e[tag=foo]> { X = 2 } }\n"
	files := compile(t, src, optimize.O2)
	if body := files["t:n/f"]; !strings.Contains(body, "X 1") {
		t.Fatalf("!(a >= 1) lost its body:\n%s", body)
	}
	// The existence case needs no flag: `unless` is the exact complement.
	joined := ""
	for _, b := range files {
		joined += b + "\n"
	}
	if !strings.Contains(joined, "unless @e[tag=foo]") {
		t.Fatalf("!any<...> should render as an unless test:\n%s", joined)
	}
}
