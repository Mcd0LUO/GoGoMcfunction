package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// stats and encode name objectives in their commands, so those must be declared.
//
// Both emit Raw commands. Without Writes, usedObjectives could not see the
// objective, so it was never declared -- and 1.12.2 discards a command that
// references an undeclared objective. For encode that meant every generated
// entitydata line was silently dropped.
func TestStatsAndEncodeDeclareTheirObjectives(t *testing.T) {
	stats := "namespace t:s\nobj temp4\n" +
		"[export]\nfn f() { stats(@s, \"set\", \"SuccessCount\", \"temp4\") }\n"
	if init := compile(t, stats, optimize.O2)["t:s/init"]; !strings.Contains(init, "objectives add temp4") {
		t.Fatalf("stats wrote an objective it never declared:\n%s", init)
	}

	enc := "namespace t:e\nobj src\n" +
		"nbt f = p\"tag.dat.f\" : bits 2\n" +
		"[export]\nfn g() { encode @s.SelectedItem[f] = src over 1..3 }\n"
	files := compile(t, enc, optimize.O2)
	if init := files["t:e/init"]; !strings.Contains(init, "objectives add src") {
		t.Fatalf("encode guarded on an objective it never declared:\n%s", init)
	}
	// The guard must name the objective, not "holder objective".
	for _, line := range strings.Split(files["t:e/g"], "\n") {
		if strings.Contains(line, "entitydata") && !strings.Contains(line, "score_src_min=") {
			t.Fatalf("the guard is malformed:\n  %s", line)
		}
	}
}

// comptime cmd() with no argument is a diagnostic, not a crash.
func TestComptimeCmdNeedsAnArgument(t *testing.T) {
	src := "namespace t:c\ncomptime { cmd() }\n"
	if got := diagCodes(src); !contains(got, "E_EMIT_CONST") {
		t.Fatalf("cmd() with no argument was accepted: %v", got)
	}
}

// A Chinese class name must survive field-objective truncation.
//
// fieldObj cut the class name with `short[:6]`, which splits a multi-byte
// character and produces invalid UTF-8 (E5 80 was observed).
func TestChineseClassNameKeepsItsBytes(t *testing.T) {
	src := "namespace t:c\n" +
		"class 残照花魄 {\n" +
		"  var hp: int = 1\n" +
		"  [every(\"1tick\")]\n" +
		"  fn tick() { hp = hp + 1 }\n" +
		"}\n"
	files := compile(t, src, optimize.O2)
	joined := ""
	for _, b := range files {
		joined += b + "\n"
	}
	if !strings.Contains(joined, "残照花魄.hp") {
		t.Fatalf("the class name was cut:\n%s", joined)
	}
	// And nothing may contain the replacement byte or an invalid sequence.
	if strings.Contains(joined, "\uFFFD") {
		t.Fatalf("invalid UTF-8 leaked into the output:\n%s", joined)
	}
}
