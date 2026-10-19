package optimize

import "cmcf/internal/ir"

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
			changed = inlineLeafArms(fn) || changed
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
			if _, isRet := target.Term.(ir.Return); !isRet {
				continue
			}
			if len(target.Ops) == 0 || len(target.Ops) > maxInlineOps {
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

// canInline reports whether a straight-line op sequence is safe to duplicate.
// Raw commands are excluded because they may have side effects the IR cannot
// reason about (tag changes, kills, entity spawning).
func canInline(ops []ir.Op) bool {
	for _, op := range ops {
		switch op.(type) {
		case ir.Set, ir.Copy, ir.Arith, ir.Add, ir.Remove:
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
func negatePred(p ir.Predicate) ir.Predicate {
	if p.Exist != nil {
		ex := *p.Exist
		ex.Negate = !ex.Negate
		return ir.Predicate{Exist: &ex}
	}
	if len(p.Alts) == 0 {
		return ir.Predicate{Alts: [][]ir.Cond{{}}}
	}
	var choices [][]ir.Cond
	for _, alt := range p.Alts {
		var opts []ir.Cond
		for _, c := range alt {
			opts = append(opts, complementCond(c)...)
		}
		if len(opts) == 0 {
			return ir.Predicate{Alts: [][]ir.Cond{{}}}
		}
		choices = append(choices, opts)
	}
	result := [][]ir.Cond{{}}
	for _, opts := range choices {
		var next [][]ir.Cond
		for _, acc := range result {
			for _, o := range opts {
				next = append(next, append(append([]ir.Cond(nil), acc...), o))
			}
		}
		result = next
	}
	return ir.Predicate{Alts: result}
}

func complementCond(c ir.Cond) []ir.Cond {
	switch {
	case c.HasMin && c.HasMax:
		var out []ir.Cond
		out = append(out, ir.Cond{Obj: c.Obj, HasMax: true, Max: c.Min - 1})
		out = append(out, ir.Cond{Obj: c.Obj, HasMin: true, Min: c.Max + 1})
		return out
	case c.HasMax:
		return []ir.Cond{{Obj: c.Obj, HasMin: true, Min: c.Max + 1}}
	case c.HasMin:
		return []ir.Cond{{Obj: c.Obj, HasMax: true, Max: c.Min - 1}}
	}
	return []ir.Cond{{Obj: c.Obj, HasMax: true, Max: -1}}
}