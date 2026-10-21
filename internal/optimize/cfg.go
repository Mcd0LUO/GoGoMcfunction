package optimize

import (
	"strings"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/analysis"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/ir"
)

// ── CFG shape optimisation ──────────────────────────────────────────────
//
// A function call in 1.12.2 is a real cost: the command dispatcher looks up
// the function, pushes a new execution context, and every command inside runs
// under that context. So the number of generated files is not cosmetic — it is
// the number of calls the program makes per execution.
//
// Two transformations collapse the CFG without changing behaviour:
//
//  1. Linear merging: a block that only jumps to a single-predecessor block
//     is the same as the concatenation of the two.
//  2. Leaf-arm inlining: a branch arm that is straight-line code ending in
//     Return can be executed inline under the branch condition, because
//     falling off the end of a function is the only way to return.
//
// Together they turn the 6-file sqrt into the 2-3 file shape a hand-written
// library has, while keeping the early-return correctness the hand-written
// version lacked.

// SimplifyCFG applies the shape transformations to a fixed point.
type SimplifyCFG struct{}

func (SimplifyCFG) Name() string { return "simplify-cfg" }

func (SimplifyCFG) Run(p *ir.Program) *ir.Program {
	for _, fn := range p.Funcs {
		for i := 0; i < 16; i++ {
			// NOTE: loop rotation is deliberately NOT applied.
			//
			// Rotating `header: cond; Branch(c, body, exit)` to the tail moves
			// the condition computation after the body, which changes what it
			// reads. In sqrt the header computes `d = x - prev` from values the
			// body overwrites, so rotation produced a condition testing a stale
			// slot and expanded to four overlapping exit guards.
			//
			// The hand-written library gets away with a tail condition because
			// it writes the condition in terms of the FINAL values by hand. A
			// compiler cannot infer that without a value-numbering analysis, so
			// it must not perform this rewrite.
			changed := mergeLinear(fn)
			changed = sinkJoinIntoEmptyElse(fn) || changed
			changed = inlineLeafArms(fn) || changed
			changed = foldSameTargetBranch(fn) || changed
			changed = threadEmptyJumps(fn) || changed
			if !changed {
				break
			}
		}
		reindex(fn)
	}
	return p
}

// predCount counts predecessors for each block.
func predCount(fn *ir.Function) map[ir.BlockID]int {
	count := map[ir.BlockID]int{}
	for _, b := range fn.Blocks {
		for _, s := range ir.Succs(b.Term) {
			count[s]++
		}
	}
	return count
}

// sinkJoinIntoEmptyElse moves the join block's code into an empty else arm.
//
//	A: Branch(c, T, E)      A: Branch(c, T, E)
//	T: ops; Jump(J)   ==>   T: ops; Jump(J)
//	E: Jump(J)              E: join-ops; ...
//	J: join-ops; ...        J: (now single-pred, merged by mergeLinear)
//
// This is what turns a run of independent `if`s into flat code. Without it
// every if ends in a join with two predecessors, mergeLinear cannot absorb the
// join, and the next statement becomes another block -- so `if a {}; if b {}`
// costs a file per if, and the reference data pack's flat `execute … if …`
// style turns into a chain.
//
// It is only sound when the else arm is EMPTY: sinking code into an arm that
// does work would run that work in a different order. An empty else arm is
// exactly "fall through", so moving the join there changes nothing.
func sinkJoinIntoEmptyElse(fn *ir.Function) bool {
	changed := false
	preds := predCount(fn)
	for _, b := range fn.Blocks {
		br, ok := b.Term.(ir.Branch)
		if !ok {
			continue
		}
		elseBlk := blockByID(fn, br.Else)
		if elseBlk == nil || len(elseBlk.Ops) != 0 || preds[elseBlk.ID] != 1 {
			continue
		}
		ej, ok := elseBlk.Term.(ir.Jump)
		if !ok {
			continue
		}
		join := blockByID(fn, ej.Target)
		if join == nil || join.ID == elseBlk.ID || join.ID == b.ID {
			continue
		}
		// The join must be reachable only from the two arms; sinking it into
		// the else arm would otherwise duplicate code that a third edge needs.
		if preds[join.ID] != 2 {
			continue
		}
		// The then arm must jump straight to the same join, or the join is not
		// shared and this is a different shape.
		thenBlk := blockByID(fn, br.Then)
		if thenBlk == nil {
			continue
		}
		tj, ok := thenBlk.Term.(ir.Jump)
		if !ok || tj.Target != join.ID {
			continue
		}
		elseBlk.Ops = append(elseBlk.Ops, join.Ops...)
		elseBlk.Term = retarget(join.Term, join.ID, elseBlk.ID)
		join.Ops = nil
		join.Term = ir.Return{}
		for _, other := range fn.Blocks {
			if other.ID == join.ID {
				continue
			}
			other.Term = retarget(other.Term, join.ID, elseBlk.ID)
		}
		changed = true
		// Update the counts this sink changes, instead of rescanning.
		//
		// Every edge that pointed at `join` now points at `elseBlk`, so
		// elseBlk's count grows by join's and join's drops to zero. This was
		// the single hottest line in the compiler: an O(N) rescan per sink
		// made a 3200-if function quadratic, and predCount was 36% of its
		// compile time.
		preds[elseBlk.ID] += preds[join.ID]
		preds[join.ID] = 0
	}
	return changed
}

// mergeLinear concatenates A -> B when B has exactly one predecessor.
func mergeLinear(fn *ir.Function) bool {
	changed := false
	preds := predCount(fn)
	for _, b := range fn.Blocks {
		j, ok := b.Term.(ir.Jump)
		if !ok || j.Target == b.ID {
			continue
		}
		target := blockByID(fn, j.Target)
		if target == nil {
			continue
		}
		// The ENTRY block must never absorb another block. After merging, every
		// edge that pointed at the absorbed block is retargeted to the entry —
		// and for a loop back edge that means re-running the prologue, which
		// resets the counter and the budget every iteration. The result is a
		// bounded loop that never terminates.
		if b.ID == 0 {
			continue
		}
		// B may be merged into when the only predecessors are A and B itself.
		// The self-edge is the loop back edge, which survives the merge because
		// the merged block keeps B's identity.
		if preds[target.ID] != 1 && !(preds[target.ID] == 2 && isSelfLoop(fn, target.ID)) {
			continue
		}
		// Merge B into A. The merged block keeps A's ID, so every edge that
		// pointed at B must be retargeted to A — including B's own terminator
		// when it branches back to itself (a loop back edge).
		//
		// Forgetting this retargeting silently turned a loop into a fall-through:
		// the back edge kept the old numeric ID, which after renumbering named a
		// completely different block.
		b.Ops = append(b.Ops, target.Ops...)
		b.Term = retarget(target.Term, target.ID, b.ID)
		target.Ops = nil
		target.Term = ir.Return{}
		for _, other := range fn.Blocks {
			if other.ID == target.ID {
				continue
			}
			other.Term = retarget(other.Term, target.ID, b.ID)
		}
		changed = true
		// Update the two counts this merge changes, instead of rescanning.
		//
		// Every edge that pointed at `target` now points at `b`, so b's count
		// grows by target's and target's drops to zero -- exact, because the
		// retarget loop above redirected all of them.
		//
		// The rescan was O(N) per merge, so a function of N blocks cost O(N^2)
		// for bookkeeping alone: 39% of the compile time on 3200 independent
		// ifs.
		preds[b.ID] += preds[target.ID]
		preds[target.ID] = 0
	}
	return changed
}

// inlineLeafArms folds a straight-line Return arm into its predecessor.
//
//	A: Branch(cond, T, E)        A: Guarded(T.Ops, cond)
//	T: ops; Return        ==>    A: Branch(cond, T_empty, E)
//	E: ...                       T: Return          (no commands emitted)
//
// The edge to the now-empty T is dropped by the back end, so the branch costs
// one conditional call instead of two.
func inlineLeafArms(fn *ir.Function) bool {
	changed := false
	preds := predCount(fn)
	for _, b := range fn.Blocks {
		br, ok := b.Term.(ir.Branch)
		if !ok {
			continue
		}
		// Only the THEN arm may be inlined.
		//
		// The else arm would need the negated predicate, but a Conditional
		// carries a single DNF and the negation of a multi-alternative DNF is a
		// cross product that cannot be represented as one predicate without
		// silently dropping alternatives. Keeping the else edge as a call is
		// correct; folding it in is not worth the risk.
		for _, side := range []ir.BlockID{br.Then} {
			target := blockByID(fn, side)
			if target == nil || preds[target.ID] != 1 {
				continue
			}
			// A Return arm is the simple case. A JUMP arm is the common one:
			// `if c { x = 1 }; y = 2` makes the then-arm a jump to the join,
			// because the code after the if has to run either way.
			//
			// Without this every `if` in the middle of a function cost two
			// files, and a run of independent one-line ifs -- which is exactly
			// how the reference data pack writes `execute … if …` -- turned
			// into a chain of blocks. 21 lines of data pack became 46 lines
			// across 16 files.
			//
			// A jump arm is folded the same way, and the jump itself is kept:
			// the guard runs the ops only when the condition holds, and then
			// the arm still has to reach the join.
			if _, isRet := target.Term.(ir.Return); !isRet {
				if _, isJump := target.Term.(ir.Jump); !isJump {
					continue
				}
			}
			if len(target.Ops) == 0 || len(target.Ops) > maxInlineOps {
				continue
			}
			// An existence or block probe cannot become a Guarded op.
			//
			// `guarded [exists …] cmd` would have to render as
			// `execute <sel> ~ ~ ~ <cmd>`, which is a FAN-OUT: it runs the
			// command once per matching entity. An existence test must run it
			// at most once, and only `function F if <sel>` has that meaning --
			// an inlined op is not a function.
			//
			// The renderer used to look only at Cond.Alts and emit NOTHING for
			// such a guard, so inlining silently deleted the body:
			// `if any<@e[tag=x]> { y = 1 }` produced no command at all and no
			// diagnostic. Keeping the arm as a Branch is what makes it
			// renderable.
			if br.Cond.Exist != nil || br.Cond.Block != nil {
				continue
			}
			if !canInline(target.Ops) {
				continue
			}
			// The arm must not write anything the guard reads.
			//
			// A guard is re-evaluated for EVERY op it protects, so an op that
			// writes one of the condition's objectives changes the condition
			// for the ops after it. The reference case:
			//
			//     if a >= 1 { if a == 1 { b = c } }
			//
			// both conditions compile to t0, and inlining produced
			//
			//     guarded [t0 >= 0] copy t0 = a
			//     guarded [t0 >= 0] arith t0 -= 1
			//     guarded [t0 >= 0] guarded [t0 == 0] copy b = c
			//
			// The first guard runs with the outer value, then overwrites t0;
			// the rest run with the inner one. At runtime that gave b = 7 when
			// a was 0 (the outer condition was false) and b unchanged when a
			// was 1 (the outer was true) -- the answer was inverted.
			//
			// Keeping the arm as a block is always correct; a rename would
			// also work but needs a live-range analysis this pass does not do.
			if writesAnyOf(target.Ops, condObjectives(br.Cond)) {
				continue
			}
			// A DISJUNCTION duplicates the body, so the body must be
			// REPEATABLE: running it N times must leave the same state as
			// running it once.
			//
			// This is the one guard whose absence changed the ANSWER, not
			// just the output size.
			//
			// A guard is rendered as ONE `execute` PER ALTERNATIVE:
			//
			//     for alt in Cond.Alts: execute <sel(alt)> ~ ~ ~ <body>
			//
			// That is forced by 1.12.2. A selector expresses a conjunction
			// (comma-separated score conditions) and has no boolean OR, so a
			// disjunction is N parallel guarded calls. The consequence is
			// that the body runs ONCE PER MATCHING ALTERNATIVE.
			//
			// An arm kept as a BLOCK runs exactly once: the branch calls it.
			// So inlining an arm under a multi-alternative predicate changes
			// HOW MANY TIMES the body runs. Measured on the engine:
			//
			//     if a >= 10 || b >= 10 { r = r + 1 }
			//
			// gave r = 1 at -O0 (the block form) and r = 2 at -O1 (the
			// inlined form, with both alternatives true). The body executed
			// twice. Every accumulating statement doubles: counters, hp
			// subtraction, score increments.
			//
			// It is worse than a doubling, because it is INVISIBLE for the
			// bodies that happen to be repeatable. `X = 1` written twice
			// gives the same answer as written once, so the bug only shows
			// on the programs that accumulate -- exactly the ones a reader
			// would not suspect.
			//
			// The SECOND consequence is pollution. Each alternative's
			// `execute` runs the WHOLE body, and the alternatives run in
			// sequence. The first copy's body can overwrite a slot the
			// SECOND copy's guard reads, so the second copy tests a
			// condition the author never wrote:
			//
			//     if a >= 10 || b >= 10 { let q = c >= 5; r = q }
			//
			// gave r = 1 at -O0 and r = 0 at -O1, with both alternatives
			// true: the second copy's guard read the difference slot the
			// first copy had already consumed.
			//
			// `isRepeatable` is the criterion, and it is stated as a proof
			// rather than a guess. See its comment for the argument; in one
			// line: every op must be an absolute assignment, and the slots
			// the body writes must be disjoint from the slots it reads.
			//
			// A single alternative is unaffected: one `execute`, one run,
			// and that is the overwhelmingly common shape (every `if` with
			// a simple condition or an `&&`), which is what the
			// optimisation exists for.
			if len(br.Cond.Alts) > 1 && !isRepeatable(target.Ops) {
				continue
			}
			// A COPY of the predicate, not the branch's own.
			//
			// Sharing the Alts slice between the Branch terminator and the
			// Guarded ops made one array reachable from two places, and any
			// pass that rewrote a condition in place then rewrote it twice --
			// see renamePred. The allocator is cheap here; a silently
			// different condition is not.
			cond := ir.ClonePredicate(br.Cond)
			for _, op := range target.Ops {
				// A constant's materialisation is NOT conditional.
				//
				// `x *= -1` lowers to `t = -1; x *= t`, and inlining wrapped BOTH
				// ops in the guard. When the guard failed, `t` kept whatever it
				// held from an earlier iteration and the multiply used a stale
				// value -- measured on the engine, `rpg/monster/hurt/operation/
				// light` gave Ap.Amount = 7 where the data pack gives 3.
				//
				// A write to a compiler temporary that no guard reads is hoisted
				// out: the temporary is dead outside this arm, so filling it
				// unconditionally is always safe and one command cheaper than
				// guarding it.
				if isHoistableConstant(op, cond) {
					b.Ops = append(b.Ops, op)
					continue
				}
				b.Ops = append(b.Ops, ir.Guarded{Inner: op, Cond: cond})
			}
			target.Ops = nil
			changed = true
		}
	}
	return changed
}

// isRepeatable reports whether running an op sequence N times leaves the same
// state as running it once.
//
// It is the condition for inlining an arm under a DISJUNCTION. A guard is
// rendered as one `execute` per alternative, so a body under an N-alternative
// predicate runs N times -- and it must therefore not care how many times it
// runs.
//
// The criterion has two halves, and together they are a proof rather than a
// guess.
//
//  1. Every op is an ABSOLUTE assignment.
//
//     `ir.Set` renders `scoreboard players set`, which writes a literal.
//     `ir.Copy` renders `scoreboard players operation ... =`, which writes a
//     slot. Both write the same value however many times they run.
//
//     Everything else is refused. `ir.Arith`, `ir.Add` and `ir.Remove`
//     ACCUMULATE, so a second run changes the value. `ir.Raw` is a command
//     the IR does not model, so this pass cannot know what it does.
//     `ir.Guarded` is conditional: its condition is re-evaluated for every
//     alternative, and a body that contains one is exactly the shape that
//     pollutes the other copy's guard -- see the audit's `let q = c >= 5`
//     case. Refusing it is what closes that hole.
//
//  2. The slots the body WRITES and the slots it READS are DISJOINT.
//
//     This is the half that is easy to leave out, and leaving it out is a
//     silent wrong answer. A body that writes a slot and then reads it back
//     is NOT repeatable:
//
//     copy t5 = t6     // reads t6
//     set  t6 = 9      // writes t6
//
//     Run once, t6 ends as 9. Run twice, the second run copies the 9 that
//     the first run wrote, not the original value. The state after two runs
//     is not the state after one.
//
//     With (1) and (2), the argument is complete. Between two runs the only
//     state that changed is what the body wrote. By (2) the body reads none
//     of those, so every read sees the same value in the second run as in
//     the first. By (1) every write is absolute, so it writes the same
//     value. Therefore the state after N runs equals the state after one.
//
// WHY NOT JUST "IS EVERY OP ABSOLUTE"?
//
// Because the body may not read what it writes, and then a second run sees
// a different value. The two halves are independent requirements, and the
// audit's own note that "`ir.Copy` also has a condition: the source must
// not be written by a later op in the same sequence" is exactly the
// second half.
//
// WHY NOT REFUSE EVERY DISJUNCTION?
//
// Because that throws away a real optimisation for the bodies that are
// genuinely repeatable, and it makes the optimised output differ from -O0
// for a shape where -O0 is already correct. `if menu == 1 || menu == 2
// { X = 2 }` writes one slot with one value; inlining it is safe and
// cheaper, and the driver test `TestOrBecomesTwoAlternatives` asserts
// exactly that shape.
//
// The cost of refusing everything is not only performance: it is that the
// compiler would stop being able to tell the reader WHY the output changed.
// This criterion says exactly what it requires, and the comment above it
// says what breaks without it.
func isRepeatable(ops []ir.Op) bool {
	writes := map[string]bool{}
	reads := map[string]bool{}
	for _, op := range ops {
		switch o := op.(type) {
		case ir.Set:
			// A literal write. It reads nothing.
			writes[o.Dst.Obj] = true
		case ir.Copy:
			// A slot-to-slot copy: writes the destination, reads the source.
			reads[o.Src.Obj] = true
			writes[o.Dst.Obj] = true
		default:
			// An accumulating op, a raw command, or a nested guard. None of
			// them can be shown to be repeatable here, so none of them is.
			return false
		}
	}
	// (2): no slot is both written and read.
	for w := range writes {
		if reads[w] {
			return false
		}
	}
	return true
}

// maxInlineOps bounds how much code may be duplicated into a guard. Inlining is
// only a win while the arm is small; beyond this the call is cheaper.
const maxInlineOps = 12

// canInline reports whether a straight-line op sequence is safe to fold into a
// guard.
//
// `Raw` is allowed: the ops are not duplicated into a place where they might
// run twice, they are wrapped in a `Guarded`, which runs them only when the
// condition holds. That is the same execution the separate block had.
//
// It used to be excluded on the grounds that a raw command may have side
// effects the IR cannot reason about -- which is true, and irrelevant: the
// side effect happens exactly as often as before. The exclusion made every
// `if` whose body contains a tellraw, a tag change or a kill cost an extra
// file, and those are the bodies authors actually write.
func canInline(ops []ir.Op) bool {
	for _, op := range ops {
		switch op.(type) {
		case ir.Set, ir.Copy, ir.Arith, ir.Add, ir.Remove, ir.Raw:
		case ir.Guarded:
		default:
			return false
		}
	}
	return true
}

// foldSameTargetBranch turns `branch c then=b else=b` into `jump b`.
//
// Inlining a then-arm leaves exactly this shape: the arm's ops are guarded
// and folded into the branch block, and both edges still name the join. The
// condition no longer selects between two destinations, so the branch is
// only a conditional call in the rendering -- the backend emits
// `function b if c` and `function b unless c`, which calls the join TWICE.
//
// It also costs a second call per `if`: a run of independent one-line ifs
// became one extra `function` line each, which is most of the gap between the
// data pack's flat `execute … if …` and GGM's output.
func foldSameTargetBranch(fn *ir.Function) bool {
	changed := false
	for _, b := range fn.Blocks {
		br, ok := b.Term.(ir.Branch)
		if !ok || br.Then != br.Else {
			continue
		}
		b.Term = ir.Jump{Target: br.Then}
		changed = true
	}
	return changed
}
func threadEmptyJumps(fn *ir.Function) bool {
	changed := false
	for _, b := range fn.Blocks {
		j, ok := b.Term.(ir.Jump)
		if !ok {
			continue
		}
		target := blockByID(fn, j.Target)
		if target == nil || target.ID == b.ID || len(target.Ops) != 0 {
			continue
		}
		if _, isRet := target.Term.(ir.Return); isRet {
			continue // already the canonical empty-return block
		}
		b.Term = target.Term
		changed = true
	}
	return changed
}

// condObjectives lists the objectives a predicate reads.
func condObjectives(p ir.Predicate) map[string]bool {
	out := map[string]bool{}
	for _, alt := range p.Alts {
		for _, c := range alt {
			out[c.Obj] = true
		}
	}
	return out
}

// writesAnyOf reports whether any op writes one of the named objectives.
func writesAnyOf(ops []ir.Op, objs map[string]bool) bool {
	if len(objs) == 0 {
		return false
	}
	for _, op := range ops {
		for _, l := range analysis.Defs(op) {
			if objs[l.Obj] {
				return true
			}
		}
	}
	return false
}

// isHoistableConstant reports whether an op is a constant being placed in a
// compiler temporary that the guard does not read.
//
// Such an op does not depend on the branch at all, so guarding it only means
// the temporary holds a stale value when the branch is not taken -- and any
// later op in the same arm that reads it then uses that stale value.
func isHoistableConstant(op ir.Op, cond ir.Predicate) bool {
	set, ok := op.(ir.Set)
	if !ok {
		return false
	}
	if !strings.HasPrefix(set.Dst.Obj, ir.TempPrefix) {
		return false
	}
	for _, alt := range cond.Alts {
		for _, c := range alt {
			if c.Obj == set.Dst.Obj {
				return false
			}
		}
	}
	return true
}

// blockByID finds a block by id.
//
// A linear scan, because the passes run while the block list is mid-edit and
// `Blocks[i].ID == i` is only restored by reindex. The scan was 3.5% of a
// 3200-if compile; the real cost was elsewhere (predCount rebuilt per pass),
// so an index would buy little and would have to be invalidated by every
// pass that adds or removes a block.
func blockByID(fn *ir.Function, id ir.BlockID) *ir.Block {
	for _, b := range fn.Blocks {
		if b.ID == id {
			return b
		}
	}
	return nil
}

// retarget rewrites a terminator's edges from one block to another.
//
// This is the only edge-rewriting primitive in the package. There used to be
// a second copy named redirect with an identical body -- exactly the kind of
// duplication that lets one copy be fixed and the other not.
func retarget(t ir.Terminator, from, to ir.BlockID) ir.Terminator {
	switch x := t.(type) {
	case ir.Jump:
		if x.Target == from {
			x.Target = to
		}
		return x
	case ir.Branch:
		if x.Then == from {
			x.Then = to
		}
		if x.Else == from {
			x.Else = to
		}
		return x
	}
	return t
}

// isSelfLoop reports whether a block has an edge to itself.
func isSelfLoop(fn *ir.Function, id ir.BlockID) bool {
	b := blockByID(fn, id)
	if b == nil {
		return false
	}
	for _, s := range ir.Succs(b.Term) {
		if s == id {
			return true
		}
	}
	return false
}

// reindex renumbers blocks 0..n-1, fixes every edge, and removes blocks that
// emit no commands.
//
// An empty block still costs a function call, so it is always worth removing.
// Removal is driven by incoming edges: a block with no predecessors is dropped,
// and an empty block with a Jump terminator is bypassed by redirecting its
// predecessors to its target.
func reindex(fn *ir.Function) {
	for {
		// 1. bypass empty pass-through blocks
		redirect := map[ir.BlockID]ir.BlockID{}
		for _, b := range fn.Blocks {
			if b.ID == 0 || len(b.Ops) != 0 {
				continue
			}
			j, ok := b.Term.(ir.Jump)
			if !ok {
				continue
			}
			// Bypassing an empty block that leads to the entry would create a
			// back edge into the prologue.
			if j.Target == 0 {
				continue
			}
			redirect[b.ID] = j.Target
		}
		changed := false
		for _, b := range fn.Blocks {
			nt := resolveTerm(b.Term, redirect)
			// Terminator is an interface and Branch holds slices, so it is not
			// comparable with ==. Compare the resolved targets instead.
			if termTargets(nt) != termTargets(b.Term) {
				b.Term = nt
				changed = true
			}
		}
		// a self-redirect would loop forever
		for id, to := range redirect {
			if to == id {
				delete(redirect, id)
			}
		}
		if !changed {
			break
		}
	}

	// 2. drop blocks that nothing can reach
	reach := map[ir.BlockID]bool{}
	work := []ir.BlockID{0}
	for len(work) > 0 {
		id := work[len(work)-1]
		work = work[:len(work)-1]
		if reach[id] {
			continue
		}
		reach[id] = true
		b := blockByID(fn, id)
		if b == nil {
			continue
		}
		for _, s := range ir.Succs(b.Term) {
			if !reach[s] {
				work = append(work, s)
			}
		}
	}

	// 3. renumber
	remap := map[ir.BlockID]ir.BlockID{}
	var kept []*ir.Block
	for _, b := range fn.Blocks {
		if !reach[b.ID] {
			continue
		}
		remap[b.ID] = ir.BlockID(len(kept))
		kept = append(kept, b)
	}
	for _, b := range kept {
		switch t := b.Term.(type) {
		case ir.Jump:
			if n, ok := remap[t.Target]; ok {
				t.Target = n
			}
			b.Term = t
		case ir.Branch:
			if n, ok := remap[t.Then]; ok {
				t.Then = n
			}
			if n, ok := remap[t.Else]; ok {
				t.Else = n
			}
			b.Term = t
		}
	}
	for i, b := range kept {
		b.ID = ir.BlockID(i)
	}
	fn.Blocks = kept
}

// termTargets renders a terminator's successors as a comparable string.
func termTargets(t ir.Terminator) string {
	switch x := t.(type) {
	case ir.Jump:
		return "j" + itoa(int(x.Target))
	case ir.Branch:
		return "b" + itoa(int(x.Then)) + "," + itoa(int(x.Else))
	case ir.TailCall:
		return "t" + x.Path
	case ir.Return:
		return "r"
	}
	return "?"
}

// resolveTerm follows a chain of empty-block redirects.
func resolveTerm(t ir.Terminator, redirect map[ir.BlockID]ir.BlockID) ir.Terminator {
	switch x := t.(type) {
	case ir.Jump:
		seen := map[ir.BlockID]bool{x.Target: true}
		for {
			n, ok := redirect[x.Target]
			if !ok || seen[n] {
				break
			}
			seen[n] = true
			x.Target = n
		}
		return x
	case ir.Branch:
		seenT := map[ir.BlockID]bool{x.Then: true}
		for {
			n, ok := redirect[x.Then]
			if !ok || seenT[n] {
				break
			}
			seenT[n] = true
			x.Then = n
		}
		seenE := map[ir.BlockID]bool{x.Else: true}
		for {
			n, ok := redirect[x.Else]
			if !ok || seenE[n] {
				break
			}
			seenE[n] = true
			x.Else = n
		}
		return x
	}
	return t
}

// negatePred returns the complement of a predicate in DNF.
//
// The implementation lives in ir so that the lowering, the optimiser and
// the back end cannot disagree about it.
func negatePred(p ir.Predicate) ir.Predicate { return ir.Negate(p) }
