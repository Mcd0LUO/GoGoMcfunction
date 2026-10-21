package optimize

import "github.com/Mcd0LUO/GoGoMcfunction/internal/ir"

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
		preds = predCount(fn)
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
		preds = predCount(fn)
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
			cond := br.Cond
			for _, op := range target.Ops {
				b.Ops = append(b.Ops, ir.Guarded{Inner: op, Cond: cond})
			}
			target.Ops = nil
			changed = true
		}
	}
	return changed
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

// rotateLoops turns a pre-tested loop into a post-tested one, so the body and
// the condition live in the same block.
//
//	header: cond; Branch(c, body, exit)      body: ...body...
//	body:   ...body...; Jump(header)   ==>   cond; Branch(c, body, exit)
//
// This is what lets a loop compile to ONE file instead of two, matching the
// shape a hand-written library uses (`body; function self unless cond`). The
// header's condition ops move to the end of the body because that is where
// they are evaluated after rotation.
func rotateLoops(fn *ir.Function) bool {
	changed := false
	preds := predCount(fn)
	for _, header := range fn.Blocks {
		br, ok := header.Term.(ir.Branch)
		if !ok {
			continue
		}
		body := blockByID(fn, br.Then)
		if body == nil {
			continue
		}
		j, ok := body.Term.(ir.Jump)
		if !ok || j.Target != header.ID {
			continue
		}
		if body.ID == header.ID {
			continue // already a self-loop; nothing to rotate
		}
		// Every entry into the header except the back edge must be redirectable
		// to the body, so the header must not carry a live value into the body
		// that the body overwrites. Conservatively require the header to hold
		// only the condition computation.
		if len(header.Ops) > 4 {
			continue
		}
		body.Ops = append(body.Ops, header.Ops...)
		body.Term = ir.Branch{Cond: br.Cond, Then: body.ID, Else: br.Else}
		header.Ops = nil
		header.Term = ir.Jump{Target: body.ID}
		// redirect every predecessor of the header to the body
		for _, b := range fn.Blocks {
			if b.ID == body.ID || b.ID == header.ID {
				continue
			}
			b.Term = redirect(b.Term, header.ID, body.ID)
		}
		changed = true
		preds = predCount(fn)
		_ = preds
	}
	return changed
}

// retarget rewrites every edge from -> to in a terminator.
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

func redirect(t ir.Terminator, from, to ir.BlockID) ir.Terminator {
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

// conjoin returns the DNF predicate for `a AND b`.
func conjoin(a, b ir.Predicate) ir.Predicate {
	if len(a.Alts) == 0 {
		return b
	}
	if len(b.Alts) == 0 {
		return a
	}
	var out [][]ir.Cond
	for _, x := range a.Alts {
		for _, y := range b.Alts {
			out = append(out, append(append([]ir.Cond(nil), x...), y...))
		}
	}
	return ir.Predicate{Alts: out}
}

// enteredFrom reports whether any block other than the excluded ones targets id.
func enteredFrom(fn *ir.Function, id ir.BlockID, exclude ...ir.BlockID) bool {
	for _, b := range fn.Blocks {
		skip := false
		for _, e := range exclude {
			if b.ID == e {
				skip = true
			}
		}
		if skip {
			continue
		}
		for _, s := range ir.Succs(b.Term) {
			if s == id {
				return true
			}
		}
	}
	return false
}

// rotatePostTest merges a loop header into its body when the body already
// branches back to the header and both branches share the same exit block.
//
//	H: ops_h; Branch(c_h, B, X)        B: ops_b; ops_h
//	B: ops_b; Branch(c_b, H, X)   ==>  Branch(c_b AND c_h, B, X)
//
// This is the shape a hand-written library writes as `body; function self
// unless cond`, and it removes one function call per iteration. The header's
// condition ops move to the END of the body because that is where they are
// evaluated after rotation — they read values the body just updated.
func rotatePostTest(fn *ir.Function) bool {
	changed := false
	for _, h := range fn.Blocks {
		hb, ok := h.Term.(ir.Branch)
		if !ok {
			continue
		}
		b := blockByID(fn, hb.Then)
		if b == nil || b.ID == h.ID {
			continue
		}
		bb, ok := b.Term.(ir.Branch)
		if !ok || bb.Then != h.ID {
			continue
		}
		if bb.Else != hb.Else {
			continue // exits differ: rotation would need a third block
		}
		if enteredFrom(fn, h.ID, h.ID, b.ID) {
			continue // the header has other entries; they must be retargeted
		}
		b.Ops = append(b.Ops, h.Ops...)
		b.Term = ir.Branch{Cond: conjoin(bb.Cond, hb.Cond), Then: b.ID, Else: hb.Else}
		h.Ops = nil
		h.Term = ir.Jump{Target: b.ID}
		changed = true
	}
	return changed
}

// threadEmptyJumps removes jumps whose target is empty, by adopting the
// target's terminator. This is what makes chained linear merges converge.

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

func blockByID(fn *ir.Function, id ir.BlockID) *ir.Block {
	for _, b := range fn.Blocks {
		if b.ID == id {
			return b
		}
	}
	return nil
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
