package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// Every table-driven command, checked against the shape the reference data
// pack actually writes.
//
// The table has one column that can be wrong: targetAt, the argument index
// that is a selector. It was wrong three times while this was being written --
// playsound puts the selector last, replaceitem puts it after the word
// "entity", advancement puts an action word in front of it -- and each wrong
// guess produced a command that looked plausible and that 1.12.2 would have
// rejected at run time.
//
// So each case below asserts the whole rendered line, not a prefix.
func TestCommandTableRendersRealSyntax(t *testing.T) {
	cases := []struct{ name, body, want string }{
		// entity and item
		{"summon", `summon("armor_stand", n"{Tags:[\"x\"]}")`, `summon armor_stand {Tags:["x"]}`},
		{"entitydata", `entitydata(self, n"{Health:1.0f}")`, "entitydata @s {Health:1.0f}"},
		{"effect", `effect(@a, "resistance", 10, 10, true)`, "effect @a resistance 10 10 true"},
		{"tp", `tp(@s, "100", "64", "-20")`, "tp @s 100 64 -20"},
		{"kill", `kill(@s)`, "kill @s"},
		{"give", `give(@s, "diamond", 1)`, "give @s diamond 1"},
		{"clear", `clear(@s, "dirt")`, "clear @s dirt"},
		{"replaceitem", `replaceitem("entity", @s, "slot.weapon.mainhand", "diamond_sword")`,
			"replaceitem entity @s slot.weapon.mainhand diamond_sword"},
		{"spreadplayers", `spreadplayers("~", "~", "2", "8", "false", @a)`,
			"spreadplayers ~ ~ 2 8 false @a"},
		{"spawnpoint", `spawnpoint(@s, "0", "64", "0")`, "spawnpoint @s 0 64 0"},

		// presentation
		{"particle", `particle("smoke", "~", "~1", "~", "0.1", "0.1", "0.1", "0", "5")`,
			"particle smoke ~ ~1 ~ 0.1 0.1 0.1 0 5"},
		{"tellraw", `tellraw(@a, n"{text:\"hello\"}")`, `tellraw @a {text:"hello"}`},
		{"title", `title(@a, "title", n"{text:\"Hi\"}")`, `title @a title {text:"Hi"}`},
		{"playsound", `playsound("minecraft:entity.player.levelup", "master", @a)`,
			"playsound minecraft:entity.player.levelup master @a"},
		{"say", `say("hi")`, "say hi"},

		// progress and score-adjacent
		{"advancement", `advancement("grant", @a, "only", "minecraft:story/root")`,
			"advancement grant @a only minecraft:story/root"},
		{"trigger", `trigger("myobj", "set", "1")`, "trigger myobj set 1"},
		{"tag_add", `tag_add(@s, "x")`, "scoreboard players tag @s add x"},
		{"tag_remove", `tag_remove(@s, "x")`, "scoreboard players tag @s remove x"},

		// the one command that names its selector twice
		{"stats", `stats(@s, "set", "SuccessCount", "tmp")`, "stats entity @s set SuccessCount @s tmp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "namespace t:c\n[load]\nfn on_load() { " + tc.body + " }\n"
			files := compile(t, src, optimize.O2)
			got := strings.TrimSpace(files["t:c/on_load"])
			if got != tc.want {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

// A command called with too few arguments is caught at compile time.
//
// 1.12.2 rejects the whole function file when one command in it is malformed,
// so a missing operand does not lose one command -- it loses every command
// after it in the same file.
func TestCommandTableChecksArity(t *testing.T) {
	cases := []struct{ name, body string }{
		{"give without item", `give(@s)`},
		{"tp without coordinates", `tp(@s, "1")`},
		{"effect without duration", `effect(@s, "speed")`},
		// `set` needs a receiving objective; `clear` does not, and takes
		// three arguments. Both were four-argument-only, so `clear` forced a
		// dummy objective into the generated line.
		{"stats set without objective", `stats(@s, "set", "SuccessCount")`},
		{"stats with no stat", `stats(@s, "clear")`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "namespace t:c\n[load]\nfn on_load() { " + tc.body + " }\n"
			if got := diagCodes(src); !contains(got, "E_ARG") {
				t.Fatalf("a short argument list was accepted: %v", got)
			}
		})
	}
}
