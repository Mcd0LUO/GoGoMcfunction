package optimize

import (
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/analysis"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/ir"
)

// A DISJUNCTION must not duplicate a body that accumulates.
//
// A guard is rendered as one `execute` PER ALTERNATIVE, because 1.12.2 has
// no boolean OR in a selector. So a body inlined under a two-alternative
// predicate runs TWICE when both alternatives hold, and every accumulating
// statement doubles.
//
// The shape below is `if a >= 10 || b >= 10 { r = r + 1 }`. The arm is an
// accumulating `r += 1`, so it is not repeatable and must stay a block.
//
// Measured on the engine before the fix: r = 1 at -O0 and r = 2 at -O1.
func TestDisjunctionDoesNotDuplicateAnAccumulatingBody(t *testing.T) {
	p := ir.NewProgram("t")
	fb := p.NewFunc("t/f")
	arm := fb.NewBlock()
	join := fb.NewBlock()

	cond := ir.Predicate{Alts: [][]ir.Cond{
		{{Obj: "t0.ggm", HasMin: true, Min: 0}},
		{{Obj: "t1.ggm", HasMin: true, Min: 0}},
	}}
	fb.Emit(0,
		ir.Copy{Dst: ir.Loc{Holder: "@s", Obj: "t0.ggm"}, Src: ir.Loc{Holder: "@s", Obj: "a"}},
		ir.Copy{Dst: ir.Loc{Holder: "@s", Obj: "t1.ggm"}, Src: ir.Loc{Holder: "@s", Obj: "b"}},
	)
	fb.SetTerm(0, ir.Branch{Cond: cond, Then: arm, Else: join})
	fb.Emit(arm, ir.Arith{
		Dst: ir.Loc{Holder: "@s", Obj: "r"},
		Op:  "+=",
		Src: ir.Loc{Holder: "#1", Obj: ir.ConstObjective},
	})
	fb.SetTerm(arm, ir.Jump{Target: join})
	fb.SetTerm(join, ir.Return{})

	SimplifyCFG{}.Run(p)

	// The arm must still be a block, so the branch calls it once.
	if len(fb.Block(arm).Ops) == 0 {
		t.Fatalf("the arm was emptied: the body was inlined under a disjunction")
	}
	// And no op in the entry block may be guarded by a two-alternative
	// predicate: that is what renders the body once per alternative.
	for i, op := range fb.Block(0).Ops {
		g, ok := op.(ir.Guarded)
		if !ok {
			continue
		}
		if len(g.Cond.Alts) > 1 {
			t.Fatalf("op %d is guarded by a %d-alternative predicate; the body would run once per alternative:\n%s",
				i, len(g.Cond.Alts), p.DumpString())
		}
	}
}

// A body that IS repeatable may still be inlined under a disjunction.
//
// `if menu == 1 || menu == 2 { X = 2 }` writes one slot with one value, so
// running it twice is indistinguishable from running it once. Refusing every
// disjunction would throw away a real optimisation AND would make the
// optimised output differ from -O0 for a shape where -O0 is already right.
//
// The driver test TestOrBecomesTwoAlternatives asserts this shape.
func TestDisjunctionStillInlinesARepeatableBody(t *testing.T) {
	p := ir.NewProgram("t")
	fb := p.NewFunc("t/g")
	arm := fb.NewBlock()
	join := fb.NewBlock()

	cond := ir.Predicate{Alts: [][]ir.Cond{
		{{Obj: "t0.ggm", HasMin: true, Min: 0}},
		{{Obj: "t1.ggm", HasMin: true, Min: 0}},
	}}
	fb.SetTerm(0, ir.Branch{Cond: cond, Then: arm, Else: join})
	fb.Emit(arm, ir.Set{Dst: ir.Loc{Holder: "@s", Obj: "X"}, Value: 2})
	fb.SetTerm(arm, ir.Jump{Target: join})
	fb.SetTerm(join, ir.Return{})

	SimplifyCFG{}.Run(p)

	if len(fb.Block(arm).Ops) != 0 {
		t.Fatalf("the repeatable body was not inlined:\n%s", p.DumpString())
	}
	guarded := 0
	for _, op := range fb.Block(0).Ops {
		if g, ok := op.(ir.Guarded); ok && len(g.Cond.Alts) == 2 {
			guarded++
		}
	}
	if guarded != 1 {
		t.Fatalf("expected one guarded op over both alternatives, got %d:\n%s",
			guarded, p.DumpString())
	}
}

// A body that writes a slot and then reads it back is NOT repeatable.
//
// This is the half of the criterion that is easy to leave out, and leaving
// it out is a silent wrong answer. Run once, `t6` ends as 9. Run twice, the
// second run copies the 9 the first run wrote, not the original value.
//
// The audit left this open: "`ir.Copy` also has a condition: the source must
// not be written by a later op in the same sequence".
func TestRepeatableRejectsAWriteThenRead(t *testing.T) {
	dst := ir.Loc{Holder: "@s", Obj: "t5.ggm"}
	src := ir.Loc{Holder: "@s", Obj: "t6.ggm"}
	dst2 := ir.Loc{Holder: "@s", Obj: "t7.ggm"}

	if isRepeatable([]ir.Op{ir.Copy{Dst: dst, Src: src}, ir.Set{Dst: src, Value: 9}}) {
		t.Fatal("a body that writes t6 and then reads it must not be repeatable")
	}
	// The ORDER does not matter: a body that reads t6 and then writes t6 is
	// just as unrepeatable, because the second run reads the value the
	// first run wrote.
	if isRepeatable([]ir.Op{ir.Set{Dst: src, Value: 9}, ir.Copy{Dst: dst, Src: src}}) {
		t.Fatal("a body that writes t6 and then reads it must not be repeatable, in either order")
	}
	// A body that writes t7 and reads t6 IS repeatable -- the slots are
	// disjoint. That is the positive case, and it has its own test below.
	if !isRepeatable([]ir.Op{ir.Copy{Dst: dst, Src: src}, ir.Set{Dst: dst2, Value: 9}}) {
		t.Fatal("a body that writes t7 and reads t6 is repeatable: the slots are disjoint")
	}
}

// The complement of the test above: a body that reads one slot and writes
// another IS repeatable.
func TestRepeatableAcceptsDisjointReadAndWrite(t *testing.T) {
	dst := ir.Loc{Holder: "@s", Obj: "t5.ggm"}
	src := ir.Loc{Holder: "@s", Obj: "t6.ggm"}
	other := ir.Loc{Holder: "@s", Obj: "t7.ggm"}

	if !isRepeatable([]ir.Op{ir.Copy{Dst: dst, Src: src}, ir.Set{Dst: other, Value: 9}}) {
		t.Fatal("a body that writes t7 and reads t6 is repeatable: the slots are disjoint")
	}
}

// An accumulating op is not repeatable, whatever else is true.
func TestRepeatableRejectsAccumulatingOps(t *testing.T) {
	dst := ir.Loc{Holder: "@s", Obj: "r"}
	if isRepeatable([]ir.Op{ir.Arith{Dst: dst, Op: "+=", Src: ir.Loc{Holder: "#1", Obj: ir.ConstObjective}}}) {
		t.Fatal("an accumulating op is not repeatable")
	}
	if isRepeatable([]ir.Op{ir.Add{Dst: dst, Value: 1}}) {
		t.Fatal("an add is not repeatable")
	}
	if isRepeatable([]ir.Op{ir.Remove{Dst: dst, Value: 1}}) {
		t.Fatal("a remove is not repeatable")
	}
	// A raw command is one the IR does not model, so this pass cannot know
	// what it does.
	if isRepeatable([]ir.Op{ir.Raw{Text: "say hi"}}) {
		t.Fatal("a raw command is not repeatable")
	}
	// A nested guard is the shape that pollutes the other copy.
	inner := ir.Guarded{
		Inner: ir.Set{Dst: ir.Loc{Holder: "@s", Obj: "t3.ggm"}, Value: 1},
		Cond:  ir.Predicate{Alts: [][]ir.Cond{{{Obj: "t2.ggm", HasMin: true, Min: 0}}}},
	}
	if isRepeatable([]ir.Op{inner}) {
		t.Fatal("a nested guard is not repeatable")
	}
}

// A guard inside a guard must be renamed, condition and all.
//
// `renameOp` had no case for `ir.Guarded`, so a guard inside a guard kept
// its pre-rename slot names while everything around it moved. The boolean
// the inner guard wrote went to a slot nobody read, and the sum was short
// by one. Measured on the engine: r = 2 at -O0 and r = 1 at -O2.
func TestRenameOpRenamesANestedGuard(t *testing.T) {
	rename := map[string]string{"t4.ggm": "t1.ggm"}
	inner := ir.Guarded{
		Inner: ir.Set{Dst: ir.Loc{Holder: "@s", Obj: "t4.ggm"}, Value: 1},
		Cond:  ir.Predicate{Alts: [][]ir.Cond{{{Obj: "t3.ggm", HasMin: true, Min: 0}}}},
	}
	out, ok := renameOp(inner, rename).(ir.Guarded)
	if !ok {
		t.Fatal("renameOp did not return a Guarded")
	}
	dst, ok := out.Inner.(ir.Set)
	if !ok {
		t.Fatal("the inner op was not a Set")
	}
	if dst.Dst.Obj != "t1.ggm" {
		t.Fatalf("the inner guard wrote %s, want the renamed t1.ggm", dst.Dst.Obj)
	}
	// The condition must move with it.
	if got := out.Cond.Alts[0][0].Obj; got != "t3.ggm" {
		t.Fatalf("the inner guard condition is %s, want t3.ggm (unchanged: it was not in the rename map)", got)
	}
}

// A raw command names its locations in three places, and all three must
// move together: the arguments spliced into the text, and the declared
// reads and writes that liveness reads.
func TestRenameOpRenamesARawCommand(t *testing.T) {
	rename := map[string]string{"t4.ggm": "t1.ggm"}
	raw := ir.Raw{
		Text:   "entitydata @s[score_%%o_min=0,score_%%o=0] x",
		Args:   []ir.Loc{{Holder: "@s", Obj: "t4.ggm"}, {Holder: "@s", Obj: "t4.ggm"}},
		Reads:  []ir.Loc{{Holder: "@s", Obj: "t4.ggm"}},
		Writes: []ir.Loc{{Holder: "@s", Obj: "t4.ggm"}},
	}
	out, ok := renameOp(raw, rename).(ir.Raw)
	if !ok {
		t.Fatal("renameOp did not return a Raw")
	}
	for _, l := range out.Args {
		if l.Obj != "t1.ggm" {
			t.Fatalf("an argument kept the name %s, want t1.ggm", l.Obj)
		}
	}
	for _, l := range out.Reads {
		if l.Obj != "t1.ggm" {
			t.Fatalf("a read kept the name %s, want t1.ggm", l.Obj)
		}
	}
	for _, l := range out.Writes {
		if l.Obj != "t1.ggm" {
			t.Fatalf("a write kept the name %s, want t1.ggm", l.Obj)
		}
	}
}

// A call names the slot the callee leaves its result in, and that slot has
// to be renamed with everything else.
//
// `ir.Call.Ret` was added after the rename switches were written, and the
// comment there used to say "a call names a function, not a slot". That was
// true when `ir.Call` carried only a path, and it stopped being true the
// moment the field appeared.
//
// The rename is a no-op today -- the lowering only ever puts the `return`
// objective or an extern's out-objective there, and neither is a compiler
// temporary -- so nothing was broken. But the guarantee these switches exist
// to provide is "every location an op names is renamed", and leaving one
// field out breaks that guarantee by a hair, to be paid the day someone
// lowers a call into a temporary.
func TestRenameOpRenamesACallReturnSlot(t *testing.T) {
	rename := map[string]string{"t4.ggm": "t1.ggm"}
	call := ir.Call{Path: "t/g", Ret: ir.Loc{Holder: "@s", Obj: "t4.ggm"}}
	out, ok := renameOp(call, rename).(ir.Call)
	if !ok {
		t.Fatal("renameOp did not return a Call")
	}
	if out.Ret.Obj != "t1.ggm" {
		t.Fatalf("the return slot kept the name %s, want the renamed t1.ggm", out.Ret.Obj)
	}
	// A call with no result slot is fine: rn ignores a zero Loc.
	empty := renameOp(ir.Call{Path: "t/g"}, rename).(ir.Call)
	if empty.Ret.Obj != "" || empty.Ret.Holder != "" {
		t.Fatalf("a call with no result slot gained one: %+v", empty.Ret)
	}
}

// Every op type the IR knows must be handled by the two rename switches.
//
// A rename has no conservative answer: an op either names a slot or it
// does not, and leaving an unrecognised one alone names the WRONG slot. So
// the switches are exhaustive over the op vocabulary, and the default
// branch panics for anything else.
//
// The list is written out here rather than derived, so that adding an op to
// the IR without teaching the switches makes THIS test fail, naming the
// op -- instead of the default branch firing at compile time on some
// unrelated program.
func TestRenameSwitchesCoverEveryOpType(t *testing.T) {
	// One op of each type, with the fields a rename could touch.
	ops := map[string]ir.Op{
		"Set":     ir.Set{Dst: ir.Loc{Holder: "@s", Obj: "t0.ggm"}, Value: 1},
		"Copy":    ir.Copy{Dst: ir.Loc{Holder: "@s", Obj: "t0.ggm"}, Src: ir.Loc{Holder: "@s", Obj: "t1.ggm"}},
		"Arith":   ir.Arith{Dst: ir.Loc{Holder: "@s", Obj: "t0.ggm"}, Op: "+=", Src: ir.Loc{Holder: "@s", Obj: "t1.ggm"}},
		"Add":     ir.Add{Dst: ir.Loc{Holder: "@s", Obj: "t0.ggm"}, Value: 1},
		"Remove":  ir.Remove{Dst: ir.Loc{Holder: "@s", Obj: "t0.ggm"}, Value: 1},
		"Guarded": ir.Guarded{Inner: ir.Set{Dst: ir.Loc{Holder: "@s", Obj: "t0.ggm"}, Value: 1}, Cond: ir.Predicate{Alts: [][]ir.Cond{{{Obj: "t1.ggm", HasMin: true, Min: 0}}}}},
		"Call":    ir.Call{Path: "t/g"},
		"FanOut":  ir.FanOut{Selector: "@e", Path: "t/g"},
		"Raw":     ir.Raw{Text: "say hi"},
	}

	rename := map[string]string{"t0.ggm": "t9.ggm", "t1.ggm": "t8.ggm"}
	for name, op := range ops {
		// renameOp must accept it.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("renameOp panicked on ir.%s: %v", name, r)
				}
			}()
			renameOp(op, rename)
		}()
		// applyRename must accept it too.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("applyRename panicked on ir.%s: %v", name, r)
				}
			}()
			p := ir.NewProgram("t")
			fb := p.NewFunc("t/f")
			fb.Emit(0, op)
			applyRename(fb.Func(), rename)
		}()
	}
}

// An op the switches do not know about must fail loudly, not silently.
//
// `ir.Op` is an open interface. The audit found that a new op type would
// pass `go build` and `go vet`, and would then be invisible to the
// register allocator: the op would keep a pre-rename slot name while the
// allocator believed that name was free, and two values would share one
// objective.
//
// The default branch is what stops that. It cannot be a conservative
// "assume it touches everything" -- that is a liveness answer, and this is
// a rename -- so it panics, naming the type to register.
func TestUnknownOpPanicsInTheRenameSwitches(t *testing.T) {
	// A stand-in for a hypothetical new op: it satisfies ir.Op but is not
	// one of the types the switches know.
	type alienOp struct{ ir.Op }

	// renameOp first.
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("renameOp accepted an unregistered op; it must panic")
			}
		}()
		renameOp(alienOp{}, map[string]string{})
	}()
	// then applyRename.
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("applyRename accepted an unregistered op; it must panic")
			}
		}()
		p := ir.NewProgram("t")
		fb := p.NewFunc("t/f")
		fb.Emit(0, alienOp{})
		applyRename(fb.Func(), map[string]string{})
	}()
}

// The coalescer must not merge a temporary with another one that is live
// at the same time. This is the property the whole pass exists for, and
// the guard the audit mentions: "two simultaneous uses never interfered".
//
// The shape is a guard that READS two temporaries and defines neither,
// which is what `guarded [t1 <= 0 and t3 >= 0]` lowers to.
func TestCoalesceKeepsTwoSimultaneousUsesApart(t *testing.T) {
	p := ir.NewProgram("t")
	fb := p.NewFunc("t/f")
	a := ir.Loc{Holder: "@s", Obj: "t1.ggm"}
	b := ir.Loc{Holder: "@s", Obj: "t3.ggm"}
	fb.Emit(0,
		ir.Copy{Dst: a, Src: ir.Loc{Holder: "@s", Obj: "x"}},
		ir.Copy{Dst: b, Src: ir.Loc{Holder: "@s", Obj: "y"}},
		ir.Guarded{
			Inner: ir.Set{Dst: ir.Loc{Holder: "@s", Obj: "t5.ggm"}, Value: 1},
			Cond:  ir.Predicate{Alts: [][]ir.Cond{{{Obj: a.Obj, HasMax: true, Max: 0}, {Obj: b.Obj, HasMin: true, Min: 0}}}},
		},
	)
	p = CoalesceTemps{}.Run(p)

	// The two slots the guard reads must still be different afterwards.
	got := map[string]bool{}
	for _, op := range p.Funcs[0].Ops() {
		for _, l := range analysis.Uses(op) {
			if isTempName(l.Obj) {
				got[l.Obj] = true
			}
		}
	}
	if len(got) < 2 {
		t.Fatalf("the two simultaneous uses collapsed onto %d slot(s): %v", len(got), got)
	}
}
