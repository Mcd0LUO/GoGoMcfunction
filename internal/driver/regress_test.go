package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/sim"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
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
on every 1tick { ticks = 7 }
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
on every 1tick { seed.t = 9 }
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
on every 1tick {
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

// A fan-out body runs with @s bound to the selected entity, so an assignment
// there writes THAT entity's score -- not the caller's.
//
// This is the shape sort_asc needs and could not express: measure something
// per entity, then select the entity that matched. It also exposed a real bug:
// the body used to be lowered as a block of the calling function, entered only
// by a text `execute ... function` call, so the CFG gave it no predecessor and
// the reachability pass deleted it.
func TestFanOutBodyRunsPerEntity(t *testing.T) {
	const src = `namespace t:f

obj m
obj d
on every 1tick { m = 42 }

fn run() -> int {
  d = 0
  for e in entities<@e[tag=x]> {
    d = m - 5
  }
  d
}
`
	files := compile(t, src, optimize.O2)
	w, err := sim.NewWorld(files)
	if err != nil {
		t.Skipf("gomc runtime unavailable: %v", err)
	}
	defer w.Close()

	st, err := w.Run([]string{
		"function t:f/init",
		"kill @e[type=armor_stand]",
		"summon armor_stand 0 0 0 {Tags:[\"x\"],Marker:1b}",
		"summon armor_stand 1 0 0 {Tags:[\"x\"],Marker:1b}",
		"scoreboard players set @e[tag=x,x=0,dx=0] m 42",
		"scoreboard players set @e[tag=x,x=1,dx=0] m 100",
	}, []string{
		"execute @e[tag=x] ~ ~ ~ function t:f/run",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Each entity computed its own value; a body that ran once, or on the
	// caller, would leave both at the same number.
	got := map[string]int{}
	for _, k := range st.Keys() {
		// Keys look like "objective/holder".
		if obj, holder, ok := strings.Cut(k, "/"); ok && obj == "d" {
			got[holder] = st.Get("d", holder)
		}
	}
	seen := map[int]bool{}
	for _, v := range got {
		seen[v] = true
	}
	if !seen[37] || !seen[95] {
		t.Errorf("want d = 37 and 95 (42-5, 100-5), got %v", got)
	}
}

// The body must be reachable. It is entered by a text call from another
// function, which the CFG cannot see, so lowering it as a block of the caller
// left it with no predecessor.
func TestFanOutBodyIsNotPruned(t *testing.T) {
	files := compile(t, `namespace t:f
obj m
obj d
on every 1tick { m = 1 }
fn run() -> int {
  d = 0
  for e in entities<@e[tag=x]> {
    d = m
  }
  d
}
`, optimize.O2)

	var bodies int
	for _, b := range files {
		if strings.Contains(b, "players operation @s d = @s m") {
			bodies++
		}
	}
	if bodies == 0 {
		t.Errorf("the fan-out body was pruned; files: %v", keysOf(files))
	}
}

// A compile-time generator has to be able to emit arbitrary commands.
//
// `emit` writes a constant score, which is all the constant pool needs. A bit
// table needs more: 88 `tp` rungs whose offsets are decimal text. Without
// emit_line those rungs have to be written out by hand, and a hand-written
// table is not a generated one.
func TestComptimeEmitLine(t *testing.T) {
	files := compile(t, `namespace t:g
comptime {
  for bit in 0..2 {
    emit_line("say bit " + str(bit))
  }
}
`, optimize.O0)

	var body string
	for name, b := range files {
		if strings.HasSuffix(name, "/comptime") {
			body = b
		}
	}
	for _, want := range []string{"say bit 0", "say bit 1", "say bit 2"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

// strdec2 renders 2^bit/1000 as the decimal literal a coordinate offset needs,
// and strpow2 renders 2^bit for the weight added back.
func TestComptimeDecimals(t *testing.T) {
	files := compile(t, `namespace t:g
comptime {
  emit_line("a " + strdec2(31, 1000))
  emit_line("b " + strdec2(0, 1000))
  emit_line("c " + strpow2(10))
}
`, optimize.O0)

	var body string
	for name, b := range files {
		if strings.HasSuffix(name, "/comptime") {
			body = b
		}
	}
	for _, want := range []string{"a 2147483.648", "b 0.001", "c 1024"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

// A statement comptime does not understand must be REJECTED, not skipped.
//
// `if` inside a comptime block used to fall through and vanish: the guarded
// rows were never generated, nothing was reported, and the output looked
// complete while being short by however many rows the branch covered.
func TestComptimeRejectsUnsupportedStatements(t *testing.T) {
	msgs := compileDiags(`namespace t:g
comptime {
  for bit in 0..2 {
    if bit > 0 {
      emit_line("say hi")
    }
  }
}
`)
	if !hasCode(msgs, "E_COMPTIME_STMT") {
		t.Fatalf("want E_COMPTIME_STMT, got %v", msgs)
	}
}

// `on every N` must actually fire once per period.
//
// The specification claimed a phase gate from the start; the implementation
// generated a plain function that ran once per call, so `on every 1s` fired
// every tick. Ten ticks produced ten beats instead of zero.
//
// The driver is the tick entry: one counter per period, incremented each tick,
// with a guarded call when it reaches the bound. The period's own entry resets
// its counter first, so the next call is exactly one period later.
func TestOnEveryFiresOncePerPeriod(t *testing.T) {
	const src = `namespace t:p

obj beats
on every 1tick { }
on every 1s { beats = beats + 1 }
`
	files := compile(t, src, optimize.O2)
	w, err := sim.NewWorld(files)
	if err != nil {
		t.Skipf("gomc runtime unavailable: %v", err)
	}
	defer w.Close()

	// gameLoopFunction names <ns>/cycle, so that is what the test calls.
	// Calling it bare is exactly what the game does: no executor, which is
	// why cycle only summons the clock and hands off.
	run := []string{"function t:p/boot"}
	for i := 0; i < 45; i++ {
		run = append(run, "function t:p/cycle")
	}
	run = append(run, "scoreboard players operation R beats = @e[tag=ggm.clock] beats")

	st, err := w.Run([]string{"function t:p/init"}, run)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// 45 ticks at 20 ticks per second is two whole periods, with 5 ticks left
	// over. One period would mean the counter never resets; 45 would mean
	// there is no gate at all.
	if got := st.Get("beats", "R"); got != 2 {
		t.Errorf("45 ticks fired %d times, want 2", got)
	}
}

// A period whose body returns early must still reset its counter. The reset
// is emitted before the body for exactly this reason: `lowerSeq` reports an
// early return as terminating, and a reset placed after the body would vanish.
func TestOnEveryResetsBeforeAnEarlyReturn(t *testing.T) {
	files := compile(t, `namespace t:p

obj beats
fn helper() -> int { 1 }

on every 2s { return }
`, optimize.O2)

	var entry string
	for name, b := range files {
		if strings.HasSuffix(name, "/every/2s") {
			entry = b
		}
	}
	if !strings.Contains(entry, "scoreboard players set @s p.40 0") {
		t.Errorf("the period does not reset its counter:\n%s", entry)
	}
	first := strings.Split(strings.TrimSpace(entry), "\n")[0]
	if !strings.Contains(first, "scoreboard players set @s p.40 0") {
		t.Errorf("the reset must come first, got:\n%s", entry)
	}
}

// A duration the compiler cannot convert to ticks is rejected rather than
// becoming an entry that never fires.
func TestOnEveryRejectsUnknownUnits(t *testing.T) {
	msgs := compileDiags(`namespace t:p
on every 3fortnight { }
`)
	if !hasCode(msgs, "E_PARSE") {
		t.Fatalf("want a parse error for an unschedulable duration, got %v", msgs)
	}
}

// Periods form a CHAIN, not a fan-out.
//
// The tick entry drives only the fastest period. Every other period is driven
// by the closest shorter period that divides it, so each counter counts to a
// ratio rather than to an absolute duration:
//
//	tick  -> 1ticks
//	1ticks -> 2ticks(2), 3ticks(3), 5ticks(5)
//	5ticks -> 1s(20/5=4)
//	1s     -> 3s(3), 5s(5)
//
// Driving every period from the tick entry instead would cost one counter and
// one guarded call per period per tick, which is what the chain avoids.
func TestPeriodChainShape(t *testing.T) {
	files := compile(t, `namespace t:c
obj n
on every 1ticks { }
on every 2ticks { }
on every 5ticks { }
on every 1s { }
on every 3s { }
`, optimize.O2)

	read := func(suffix string) string {
		for name, b := range files {
			if strings.HasSuffix(name, suffix) {
				return b
			}
		}
		return ""
	}

	// The clock entry drives exactly one period.
	onCycle := read("/on_cycle")
	if got := strings.Count(onCycle, "function t:c/every/"); got != 1 {
		t.Errorf("the clock entry drives %d periods, want 1:\n%s", got, onCycle)
	}

	// 5ticks drives 1s with ratio 4, not 20: the counter advances once per
	// firing of 5ticks, so it only needs to reach 20/5.
	five := read("/every/5ticks")
	if !strings.Contains(five, "score_p.20_min=4,score_p.20=4") {
		t.Errorf("5ticks should drive 1s at ratio 4:\n%s", five)
	}
	if strings.Contains(five, "score_p.20_min=20") {
		t.Errorf("5ticks uses the absolute period instead of the ratio:\n%s", five)
	}

	// 1ticks must not drive 1s directly: 5ticks sits between them.
	one := read("/every/1ticks")
	if strings.Contains(one, "every/1s") {
		t.Errorf("1ticks drives 1s directly, skipping 5ticks:\n%s", one)
	}
}

// Counter objectives are named from the tick count, so two periods that differ
// only in how they are spelled share ONE counter. 5s and 5ticks both once read
// p.100, and 1s read p.20 while 100ms also read p.100.
func TestPeriodCountersDoNotCollide(t *testing.T) {
	files := compile(t, `namespace t:c
obj n
on every 5ticks { }
on every 5s { }
`, optimize.O2)

	seen := map[string]string{}
	for name, b := range files {
		for _, line := range strings.Split(b, "\n") {
			if !strings.Contains(line, "scoreboard players set @s p.") {
				continue
			}
			f := strings.Fields(line)
			obj := f[4]
			if prev, dup := seen[obj]; dup {
				t.Errorf("%s and %s share the counter %s", prev, name, obj)
			}
			seen[obj] = name
		}
	}
	if len(seen) != 2 {
		t.Errorf("want two distinct counters, got %v", seen)
	}
}

// A duration below one tick has no schedule. Rounding it would fire on a
// cadence nobody asked for, so it is rejected instead.
func TestSubTickDurationIsRejected(t *testing.T) {
	msgs := compileDiags(`namespace t:c
on every 100ms { }
`)
	if !hasCode(msgs, "E_PARSE") {
		t.Fatalf("want a parse error for a sub-tick duration, got %v", msgs)
	}
}

// A duration is ONE token, so its unit may be a keyword.
//
// `tick` starts `on tick`, and with durations lexed as INT + identifier the
// parser could not glue them: `on every 1tick` was unwritable, and the
// workaround was to make the plural carry the meaning (`1ticks`). A duration
// is a literal, like a hex integer, so the lexer owns it.
func TestDurationIsOneToken(t *testing.T) {
	for _, tc := range []struct {
		text  string
		ticks int
	}{
		{"1tick", 1},
		{"1ticks", 1},
		{"5s", 100},
		{"5sec", 100},
		{"5seconds", 100},
		{"2m", 2400},
		{"2min", 2400},
	} {
		got, ok := syntax.ParseDuration(tc.text)
		if !ok || got != tc.ticks {
			t.Errorf("ParseDuration(%q) = %d, %v; want %d", tc.text, got, ok, tc.ticks)
		}
	}
	// A unit the target cannot schedule is rejected rather than rounded.
	for _, bad := range []string{"100ms", "0s", "5", "s", "5x"} {
		if _, ok := syntax.ParseDuration(bad); ok {
			t.Errorf("ParseDuration(%q) was accepted", bad)
		}
	}
}

// The singular `tick` must work in source, which is what the single token buys.
func TestOnEveryAcceptsSingularTick(t *testing.T) {
	files := compile(t, `namespace t:d
obj n
on every 1tick { n = 0 }
`, optimize.O2)

	var entry string
	for name, b := range files {
		if strings.HasSuffix(name, "/every/1tick") {
			entry = b
		}
	}
	if !strings.Contains(entry, "scoreboard players set @s p.1 0") {
		t.Errorf("on every 1tick did not produce a one-tick period:\n%s", entry)
	}
}
