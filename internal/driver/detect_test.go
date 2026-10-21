package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// detect probes a block and branches on the answer.
//
// 1.12.2 has no boolean expression in a command and no `unless detect`, so the
// probe has to become a flag that the ordinary predicate machinery can test --
// including its complement, which is the half `detect` alone cannot express.
func TestDetectLowersToAFlag(t *testing.T) {
	src := "namespace t:d\n" +
		"obj hit\n" +
		"[every(\"1tick\")]\n" +
		"fn tick() {\n" +
		"  if detect(\"~\", \"~-1\", \"~\", \"stone\", \"0\", self) { hit = 1 }\n" +
		"}\n"
	files := compile(t, src, optimize.O2)
	body := files["t:d/tick"]
	if !strings.Contains(body, "detect ~ ~-1 ~ stone 0") {
		t.Fatalf("no detect command:\n%s", body)
	}
	// The flag is what makes the complement expressible at all: `detect` can
	// only run a command when the block matches, and 1.12.2 has no
	// `unless detect`. So the else edge is the ABSENCE of the flag being set,
	// tested with an ordinary selector condition rather than a second probe.
	if !strings.Contains(body, "score_#t0") {
		t.Fatalf("the probe's flag is not tested:\n%s", body)
	}
}

// A detect whose flag is renamed must have its command text renamed with it.
//
// This is the bug that made -O2 disagree with -O0: the probe wrote its flag
// through a Raw command whose objective was baked into a string, and the
// allocator renamed the Loc without renaming the string. The op then claimed to
// write t0 while the command still said t1, and the branch read whichever one
// the allocator had NOT written.
//
// The check is that every objective a probe mentions in its command text is
// also the one its branch tests.
func TestDetectFlagSurvivesRenaming(t *testing.T) {
	src := "namespace t:d\n" +
		"obj a\nobj b\n" +
		"[every(\"1tick\")]\n" +
		"fn tick() {\n" +
		"  if detect(\"~\", \"~-1\", \"~\", \"stone\", \"0\", self) { a = 1 }\n" +
		"  if detect(\"~\", \"~\", \"~\", \"air\", \"*\", self) { b = 1 }\n" +
		"}\n"
	for _, lvl := range []optimize.Level{optimize.O0, optimize.O1, optimize.O2, optimize.O3} {
		t.Run(lvl.String(), func(t *testing.T) {
			files := compile(t, src, lvl)
			for name, body := range files {
				for _, line := range strings.Split(body, "\n") {
					if !strings.Contains(line, "detect ") {
						continue
					}
					// "… scoreboard players set @s tN 1"
					i := strings.Index(line, "scoreboard players set ")
					if i < 0 {
						t.Fatalf("%s: probe does not set a flag: %s", name, line)
					}
					rest := strings.Fields(line[i+len("scoreboard players set "):])
					if len(rest) < 2 {
						t.Fatalf("%s: malformed probe: %s", name, line)
					}
					flag := rest[1]
					// The branch that reads this flag must name the same objective.
					if !strings.Contains(body, "score_"+flag) {
						t.Fatalf("%s: probe writes %s but no branch tests it:\n%s", name, flag, body)
					}
				}
			}
		})
	}
}

// enable is trigger's other half. 1.12.2 refuses a trigger write on an
// objective that was never enabled, and reports nothing.
func TestEnableRenders(t *testing.T) {
	src := "namespace t:d\n[load]\nfn tick() { enable(@a, \"menu\") }\n"
	files := compile(t, src, optimize.O2)
	if got := strings.TrimSpace(files["t:d/tick"]); got != "scoreboard players enable @a menu" {
		t.Fatalf("got %q", got)
	}
}
