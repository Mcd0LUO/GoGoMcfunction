package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// boot must call init, or no objective is ever declared.
//
// The back end puts every `objectives add` in <ns>/init, and 1.12.2 discards
// a command that writes an objective that does not exist -- silently. boot
// called only the comptime and [load] bodies, so init was generated and
// nothing reached it: every scoreboard write in the program did nothing and
// nothing said so.
func TestBootDeclaresObjectives(t *testing.T) {
	src := "namespace t:v\n" +
		"obj money\n" +
		"[export]\nfn f() { money = 1 }\n"
	files := compile(t, src, optimize.O2)
	init, ok := files["t:v/init"]
	if !ok {
		t.Fatalf("no init function; files: %v", keysOf(files))
	}
	if !strings.Contains(init, "objectives add money") {
		t.Fatalf("init does not declare money:\n%s", init)
	}
	boot, ok := files["t:v/boot"]
	if !ok {
		t.Fatalf("no boot function, so nothing can call init; files: %v", keysOf(files))
	}
	if !strings.Contains(boot, "function t:v/init") {
		t.Fatalf("boot does not call init, so the declaration never runs:\n%s", boot)
	}
}

// An if/else must test the else arm BEFORE running the then arm.
//
// Both calls name the same condition objective, and the then arm may
// overwrite it -- a nested if recomputes the same temporary, and @s is one
// register file shared by the whole call stack. Emitting the `if` first made
// the `unless` test a value the then arm had already replaced:
//
//	if arg1 == 1 { if arg1 == 5 { X = 1 } } else { Y = 2 }
//
// set Y = 2 for arg1 = 1, 5 and 7 alike. Checked on the engine.
func TestElseIsTestedBeforeThen(t *testing.T) {
	src := "namespace t:v\n" +
		"obj X\nobj Y\n" +
		"extern A: int = @s.a0\n" +
		"[export]\nfn f() {\n" +
		"  if A == 1 { if A == 5 { X = 1 } }\n" +
		"  else { Y = 2 }\n" +
		"}\n"
	files := compile(t, src, optimize.O2)
	var body string
	for name, b := range files {
		if strings.HasSuffix(name, "/f") {
			body = b
		}
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	ifLine, elseLine := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "unless @s[score_#t") {
			elseLine = i
		}
		if strings.Contains(l, "if @s[score_#t") && !strings.Contains(l, "unless") {
			ifLine = i
		}
	}
	if ifLine < 0 || elseLine < 0 {
		t.Fatalf("expected both arms guarded by t0:\n%s", body)
	}
	if elseLine > ifLine {
		t.Fatalf("the else arm is tested AFTER the then arm, which may have\n"+
			"overwritten the condition:\n%s", body)
	}
}
