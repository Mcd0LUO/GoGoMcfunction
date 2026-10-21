package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/driver"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// The builtins that write a whole command.
//
// Each of them takes command TEXT, never a score. That is not an
// implementation shortcut: 1.12.2 builds a command from a string, and there is
// no syntax that splices a scoreboard value into one. Accepting an int and
// writing its initial value would emit a command that looks right and ignores
// every change since -- the exact failure this compiler exists to prevent.
func TestCommandBuiltinsEmitTheirText(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"summon plain", `summon("pig")`, "summon pig"},
		{"summon with nbt", `summon("armor_stand", n"{Tags:[\"mob\"],Marker:1b}")`, `summon armor_stand {Tags:["mob"],Marker:1b}`},
		{"summon with position", `summon("pig", n"{NoAI:1b}", "~", "~1", "~")`, `summon pig {NoAI:1b} ~ ~1 ~`},
		{"entitydata", `entitydata(self, n"{Health:1.0f}")`, "entitydata @s {Health:1.0f}"},
		{"effect", `effect(self, "speed", 10, 1, true)`, "effect @s speed 10 1 true"},
		{"tp relative", `tp(self, "~", "~1", "~")`, "tp @s ~ ~1 ~"},
		{"tp absolute", `tp(self, "100", "64", "-20")`, "tp @s 100 64 -20"},
		{"cmd", `cmd("particle smoke ~ ~1 ~ 0.1 0.1 0.1 0 5")`, "particle smoke ~ ~1 ~ 0.1 0.1 0.1 0 5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "namespace t:b\n[load]\nfn on_load() { " + tc.body + " }\n"
			files := compile(t, src, optimize.O2)
			got := files["t:b/on_load"]
			if strings.TrimSpace(got) != tc.want {
				t.Fatalf("got %q, want %q", strings.TrimSpace(got), tc.want)
			}
		})
	}
}

// A command builtin rejects a runtime value instead of freezing its initial
// value into the command text.
//
// This is the one place where the language's inability to splice a score into
// a command has to be reported rather than worked around.
func TestCommandBuiltinsRejectRuntimeValues(t *testing.T) {
	cases := []struct{ name, body string }{
		{"summon hp", "obj hp\n[load]\nfn on_load() { hp = 5; summon(\"pig\", hp) }"},
		{"effect amplifier", "obj n\n[load]\nfn on_load() { n = 5; effect(self, \"speed\", n) }"},
		{"cmd from a score", "obj n\n[load]\nfn on_load() { n = 5; cmd(n) }"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "namespace t:b\n" + tc.body + "\n"
			if !hasDiag(diagsOf(src, driver.Options{Level: optimize.O2}), "E_ARG") {
				t.Fatalf("a runtime value was accepted as command text: %v", diagsOf(src, driver.Options{Level: optimize.O2}))
			}
		})
	}
}

// n"..." is usable as an ordinary expression, not only where the grammar used
// to spell it out. Without this the NBT codec cannot reach a command.
func TestNBTLiteralIsAnExpression(t *testing.T) {
	src := "namespace t:b\n[load]\nfn on_load() { summon(\"armor_stand\", n\"{Tags:[\\\"x\\\"]}\") }\n"
	if ds := diagsOf(src, driver.Options{Level: optimize.O2}); len(ds) != 0 {
		t.Fatalf("an NBT literal argument was rejected: %v", ds)
	}
}

// A malformed NBT literal is still caught at parse time, wherever it appears.
func TestNBTLiteralIsStillValidated(t *testing.T) {
	src := "namespace t:b\n[load]\nfn on_load() { summon(\"pig\", n\"{Tags:[\")\") }\n"
	if !hasDiag(diagsOf(src, driver.Options{Level: optimize.O2}), "E_PARSE") {
		t.Fatal("a malformed NBT literal was accepted")
	}
}
