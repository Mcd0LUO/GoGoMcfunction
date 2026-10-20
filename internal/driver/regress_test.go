package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// The five fixes below all come from running generated code on the engine
// rather than from reading it. Each was silent: the compiler reported no
// error, the function loaded, and the only symptom was a wrong number.

// 1. Reading a name declared with `obj` used to allocate a fresh local.
//
// The write path already resolved the declared objective, so `ticks = ticks + 1`
// compiled to `ticks = l0 + 1`: the target landed on the right objective and
// the value came from a local nothing ever wrote.
func TestDeclaredObjectiveReadsItself(t *testing.T) {
	files := compile(t, `namespace t:d

obj ticks
on load { ticks = 7 }
fn bump() -> int {
  ticks = ticks + 1
  ticks
}
`, optimize.O0)

	var body []string
	for name, b := range files {
		if strings.HasSuffix(name, "/bump") {
			body = strings.Split(b, "\n")
		}
	}
	if len(body) == 0 {
		t.Fatal("no bump function generated")
	}
	// The increment must name `ticks` on both sides, or an objective nothing
	// writes is exactly what the program reads.
	joined := strings.Join(body, "\n")
	if strings.Contains(joined, " @s ticks = @s l") || strings.Contains(joined, "= @s l0\n") && !strings.Contains(joined, "ticks = #1") {
		// fall through to the precise check below
	}
	sawIncrement := false
	for _, line := range body {
		if strings.Contains(line, "ticks") && strings.Contains(line, "+=") {
			sawIncrement = true
		}
		if strings.Contains(line, "@s l0") && strings.Contains(line, "ticks") {
			t.Errorf("reading ticks produced a local: %s", line)
		}
	}
	if !sawIncrement {
		t.Errorf("no increment on ticks; got:\n%s", joined)
	}
}

// 2. `holder.objective = value` is the assignment form of `extern SEED = seed.math`.
// It used to parse, then fall through to a fresh local.
func TestHolderObjectiveAssignment(t *testing.T) {
	files := compile(t, `namespace t:h
obj t
on load { seed.t = 9 }
`, optimize.O0)

	var saw bool
	for _, b := range files {
		if strings.Contains(b, "scoreboard players set seed t 9") {
			saw = true
		}
	}
	if !saw {
		t.Errorf("seed.t = 9 did not reach the named holder; files: %v", keysOf(files))
	}
}

// 3. `any<sel>` is an EXISTENCE test, so its arm runs at most once.
//
// It was rendered as `execute <sel> ~ ~ ~ function F`, which runs F once PER
// matching entity -- so a loop body ran N times per iteration.
func TestAnyRunsItsArmOnce(t *testing.T) {
	files := compile(t, `namespace t:a
obj hits
fn run() -> int {
  hits = 0
  if any<@e[tag=probe]> {
    hits = hits + 1
  }
  hits
}
`, optimize.O2)

	for name, b := range files {
		if strings.HasSuffix(name, "/run") && strings.Contains(b, "hits") {
			if strings.Contains(b, "execute @e[tag=probe]") {
				t.Errorf("%s fans out instead of testing existence:\n%s", name, b)
			}
		}
	}
}

// 4. `reduce<min>` seeded an accumulator on @s and compared against a SELECTOR.
//
// `scoreboard players operation @s t < @a[tag=x] obj` is silently discarded by
// 1.12.2 -- the right operand of an operation is one holder, never a selector --
// so every reduce returned its seed.
func TestReduceFansOutThroughAHolder(t *testing.T) {
	files := compile(t, `namespace t:r
obj v
fn lo() -> int {
  let x = reduce<min>(@e[tag=sort], v)
  x
}
`, optimize.O2)

	var joined string
	for _, b := range files {
		joined += b
	}
	if strings.Contains(joined, "< @e[") || strings.Contains(joined, "< @a[") {
		t.Errorf("reduce compares against a selector, which 1.12.2 ignores:\n%s", joined)
	}
	if !strings.Contains(joined, "execute @e[tag=sort]") {
		t.Errorf("reduce does not fan out; got:\n%s", joined)
	}
}

// 5. The loop budget lives on @s, so it must not be folded into an existence
// selector naming OTHER entities.
//
// `if @e[tag=x,score_budget_min=1]` asks whether one of those entities has the
// budget, and they do not -- the loop then stopped after one iteration.
func TestLoopBudgetStaysOnTheExecutor(t *testing.T) {
	files := compile(t, `namespace t:b
obj n
fn spin() -> int {
  let i = 0
  while any<@e[tag=go]>, budget 4 {
    i = i + 1
  }
  i
}
`, optimize.O2)

	for name, b := range files {
		if strings.Contains(b, "@e[tag=go,score_l") {
			t.Errorf("%s puts the budget inside the existence selector:\n%s", name, b)
		}
	}
}

// 6. An objective written only by a broadcast write must still be declared.
//
// Broadcasts lower to raw command text, which the objective collector did not
// scan, so `@e[…] temp1 = …` wrote an objective that was never created --
// silently discarded, the exact failure `obj` exists to prevent.
func TestBroadcastObjectiveIsDeclared(t *testing.T) {
	files := compile(t, `namespace t:w
on load {
  @e[tag=x] fresh = 1
}
`, optimize.O2)

	var init string
	for name, b := range files {
		if strings.HasSuffix(name, "/init") {
			init = b
		}
	}
	if !strings.Contains(init, "objectives add fresh") {
		t.Errorf("broadcast target objective is not declared:\n%s", init)
	}
}
