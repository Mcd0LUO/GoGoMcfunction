// Package optimize rewrites IR without knowing the target version.
//
// Passes are functions over a Program. They are conservative: every rewrite
// must preserve observable scoreboard state, because functions communicate
// through global registers and proving a write dead requires liveness.
package optimize

import (
	"sort"
	"strings"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/analysis"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/ir"
)

// Pass is one IR-to-IR transformation.
type Pass interface {
	Name() string
	Run(p *ir.Program) *ir.Program
}

// DefaultLevel is the shipped optimisation level. It matches C++ convention:",
// the default is not the maximum, because maximum optimisation should be an",
// explicit choice.
const DefaultLevel = O2

// Run applies the pipeline for a level. Each pass runs in catalog order, and",
// passes requiring a stronger proof than the level provides are skipped.
func Run(p *ir.Program, level Level) *ir.Program {
	for _, pass := range Pipeline(level) {
		p = pass.Run(p)
	}
	return p
}

// RunPasses applies an explicit pass list, for tests.
func RunPasses(p *ir.Program, passes []Pass) *ir.Program {
	for _, pass := range passes {
		p = pass.Run(p)
	}
	return p
}

// ── PruneUnreachable ────────────────────────────────────────────────────

// PruneUnreachable drops blocks and functions nothing can reach.
type PruneUnreachable struct{}

func (PruneUnreachable) Name() string { return "prune-unreachable" }

func (PruneUnreachable) Run(p *ir.Program) *ir.Program {
	for _, fn := range p.Funcs {
		reach := analysis.Reachable(fn)
		var kept []*ir.Block
		remap := map[ir.BlockID]ir.BlockID{}
		for _, b := range fn.Blocks {
			if reach[b.ID] {
				remap[b.ID] = ir.BlockID(len(kept))
				kept = append(kept, b)
			}
		}
		for i, b := range kept {
			b.ID = ir.BlockID(i)
			b.Term = remapTerm(b.Term, remap)
		}
		fn.Blocks = kept
	}
	pruneFuncs(p)
	return p
}

func remapTerm(t ir.Terminator, m map[ir.BlockID]ir.BlockID) ir.Terminator {
	switch x := t.(type) {
	case ir.Jump:
		if n, ok := m[x.Target]; ok {
			return ir.Jump{Target: n}
		}
	case ir.Branch:
		n := x
		if v, ok := m[x.Then]; ok {
			n.Then = v
		}
		if v, ok := m[x.Else]; ok {
			n.Else = v
		}
		return n
	}
	return t
}

// pruneFuncs removes functions that no exported function can call.
func pruneFuncs(p *ir.Program) {
	byPath := map[string]*ir.Function{}
	for _, f := range p.Funcs {
		byPath[f.Path] = f
	}
	seen := map[string]bool{}
	var visit func(string)
	visit = func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		fn, ok := byPath[path]
		if !ok {
			return // extern: not ours
		}
		for _, b := range fn.Blocks {
			for _, op := range b.Ops {
				switch o := op.(type) {
				case ir.Call:
					visit(o.Path)
				case ir.FanOut:
					// A fan-out is a call graph edge like any other.
					// Missing it pruned every class lifecycle handler:
					// the entry point existed and called a function the
					// pruner had already thrown away.
					visit(o.Path)
				}
			}
			if c, ok := b.Term.(ir.TailCall); ok {
				visit(c.Path)
			}
		}
	}
	for _, f := range p.Funcs {
		if f.Exported {
			visit(f.Path)
		}
	}
	var kept []*ir.Function
	for _, f := range p.Funcs {
		if seen[f.Path] {
			kept = append(kept, f)
		}
	}
	// a function reachable only via a call op inside a dead block is gone too
	p.Funcs = kept
}

// ── SelfAssign ──────────────────────────────────────────────────────────

// SelfAssign removes `x = x` copies, which appear when a call argument is
// already in the register the callee expects.
type SelfAssign struct{}

func (SelfAssign) Name() string { return "self-assign" }

func (SelfAssign) Run(p *ir.Program) *ir.Program {
	for _, fn := range p.Funcs {
		for _, b := range fn.Blocks {
			var kept []ir.Op
			for _, op := range b.Ops {
				if selfAssign(op) {
					continue
				}
				kept = append(kept, op)
			}
			b.Ops = kept
		}
	}
	return p
}

// selfAssign reports whether an op writes a location to itself, recursing
// through Guarded.
//
// The guarded case is not cosmetic: inlining wraps an arm's ops in
// `Guarded{...}`, so `for e in entities<...> where cond { x = x + 1 }` produced
//
//	guarded [t == 0] copy x = x
//	guarded [t == 0] arith x += 1
//
// -- a command per iteration that reads a score and writes it back unchanged.
// The top-level-only check never saw it.
func selfAssign(op ir.Op) bool {
	switch o := op.(type) {
	case ir.Copy:
		return o.Dst == o.Src
	case ir.Guarded:
		return selfAssign(o.Inner)
	}
	return false
}

// ── DeadStore ───────────────────────────────────────────────────────────

// DeadStore removes writes to locations that are never read afterwards.
//
// Only temporaries are eligible. Locals, parameters and class fields are
// observable by other functions through the shared @s register file, so
// removing their writes would change behaviour across calls.
type DeadStore struct{}

func (DeadStore) Name() string { return "dead-store" }

func (DeadStore) Run(p *ir.Program) *ir.Program {
	for _, fn := range p.Funcs {
		liveOut := analysis.LiveOut(fn)
		for _, b := range fn.Blocks {
			live := map[ir.Loc]bool{}
			for l := range liveOut[b.ID] {
				live[l] = true
			}
			// walk backwards, deleting stores whose target is not live
			var kept []ir.Op
			for j := len(b.Ops) - 1; j >= 0; j-- {
				op := b.Ops[j]
				removed := false
				if isTempWrite(op) {
					d := analysis.Defs(op)[0]
					if !live[d] {
						removed = true
					}
				}
				if !removed {
					for _, d := range analysis.Defs(op) {
						delete(live, d)
					}
					for _, u := range analysis.Uses(op) {
						live[u] = true
					}
					kept = append(kept, op)
				}
			}
			// reverse back into program order
			for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
				kept[i], kept[j] = kept[j], kept[i]
			}
			b.Ops = kept
		}
	}
	return p
}

// isTempWrite reports whether an op is a pure write of a compiler temporary.
//
// A raw command is excluded even when it declares a temporary as its only
// write: the command does something observable besides storing the value. The
// block probe is exactly this -- `execute … detect … scoreboard players set @s
// t0 1` writes a temp AND performs the probe, and deleting it because nothing
// read t0 afterwards removed the test itself.
func isTempWrite(op ir.Op) bool {
	if _, ok := op.(ir.Raw); ok {
		return false
	}
	// A guarded write is conditional too: where the guard fails the old value
	// survives, so the write cannot be deleted on liveness alone.
	if _, ok := op.(ir.Guarded); ok {
		return false
	}
	d := analysis.Defs(op)
	if len(d) != 1 {
		return false
	}
	return isTempName(d[0].Obj)
}

// isTempName reports whether an objective is one the compiler minted.
//
// The test is the prefix `Tmp` actually emits, not a guess at its shape. It
// used to accept any `t` followed by digits -- which is also a perfectly
// ordinary author name, so `obj t1` was renamed and merged with a real
// temporary, and the function that assigned it disappeared at -O2 with no
// diagnostic.
// IsTempName is isTempName, exported so tests assert against the real
// predicate instead of restating it. A restated predicate drifts: when the
// naming scheme changed, the copy in lower_test.go kept matching the OLD
// shape, found nothing, and its assertion silently stopped running.
func IsTempName(obj string) bool { return isTempName(obj) }

func isTempName(obj string) bool {
	if !strings.HasPrefix(obj, ir.TempPrefix) {
		return false
	}
	if !strings.HasSuffix(obj, ir.TempSuffix) {
		return false
	}
	rest := obj[len(ir.TempPrefix) : len(obj)-len(ir.TempSuffix)]
	if rest == "" {
		return false
	}
	for _, c := range rest {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ── CoalesceTemps ───────────────────────────────────────────────────────

// CoalesceTemps renames temporaries so that slots with disjoint live ranges
// share a name.
//
// This is the payoff of having liveness: the current lowering gives every
// temporary a program-wide unique name, which is safe but inflates the
// objective count. Interference-free renaming recovers the compact form a
// hand-written library uses (temp1..temp4).
type CoalesceTemps struct{}

func (CoalesceTemps) Name() string { return "coalesce-temps" }

func (CoalesceTemps) Run(p *ir.Program) *ir.Program {
	base := 0
	for _, fn := range p.Funcs {
		rename, used := interferenceRename(fn, base)
		applyRename(fn, rename)
		base += used
	}
	return p
}

// addEdge records that two temporaries cannot share an objective.
//
// The relation is symmetric and MUST be stored that way. Recording only
// the direction the scan happens to observe left one side blind: with
// t10 -> t8 stored but not t8 -> t10, colouring t8 could not see that t10
// already claimed the slot, and three temporaries that needed three
// objectives collapsed into one. In luo:math/chance that turned
// "rand <= p" into "always true".
func addEdge(g map[string]map[string]bool, a, b string) {
	if g[a] == nil {
		g[a] = map[string]bool{}
	}
	if g[b] == nil {
		g[b] = map[string]bool{}
	}
	g[a][b] = true
	g[b][a] = true
}

// interferenceRename maps each temporary to a slot index such that no two
// temporaries live simultaneously share an index.
//
// A simple greedy colouring over the interference graph is sufficient: the
// graphs produced by structured control flow are small and sparse.
//
// Names are drawn from a module-wide pool: a function's colour k becomes
// t(base+k), and base advances past every colour the function used.
//
// Reusing slot numbers from zero in each function would look tidier and
// be wrong, for the same reason the locals were wrong. @s is one register
// file across the whole call stack, so a callee's t0 IS the caller's t0;
// two functions can only share a name if the allocator proves they are
// never live at the same time, and being in different functions proves
// nothing of the sort. It is exactly the case that made luo:math/chance
// read a callee's scratch value as one of its own arguments.
func interferenceRename(fn *ir.Function, base int) (map[string]string, int) {
	interfere := map[string]map[string]bool{}
	all := map[string]bool{}

	out := analysis.LiveOut(fn)
	for _, b := range fn.Blocks {
		// The exact fixed point, not a peek at the immediate successors.
		// A local approximation misses conflicts that only appear
		// through a back edge or a chain of blocks, and a missed
		// conflict is two live values sharing one objective.
		live := map[ir.Loc]bool{}
		for l := range out[b.ID] {
			live[l] = true
		}
		for j := len(b.Ops) - 1; j >= 0; j-- {
			op := b.Ops[j]
			defs := analysis.Defs(op)
			uses := analysis.Uses(op)
			for _, u := range uses {
				if isTempName(u.Obj) {
					all[u.Obj] = true
				}
			}

			// Everything read AND written by this op is simultaneously live
			// across it. Computing interference from `live` alone missed the
			// case where a def and a use belong to the same instruction, which
			// let a conditional write share a slot with the value it tests.
			liveNow := map[ir.Loc]bool{}
			for l := range live {
				liveNow[l] = true
			}
			for _, u := range uses {
				liveNow[u] = true
			}

			for _, d := range defs {
				if !isTempName(d.Obj) {
					continue
				}
				all[d.Obj] = true
				for l := range liveNow {
					if isTempName(l.Obj) && l.Obj != d.Obj {
						addEdge(interfere, d.Obj, l.Obj)
					}
				}
			}
			// ...and the same for the values this op READS.
			//
			// Interference was built from defs alone, so two temporaries that are
			// simultaneously live only as USES of one instruction never got an
			// edge. `guarded [t1 <= 0 and t3 >= 0]` reads both and defines
			// neither, so the allocator was free to give them one slot -- and
			// did. The guard then tested the same register twice, which is a
			// different condition from the one the author wrote:
			//
			//     if W <= -9 && W >= -100      (a range)
			//
			// collapsed to `t0 <= 0 and t0 >= 0`, that is `W == -9` exactly.
			// The reference pack's random-number wraparound guard is written in
			// this shape, so the port produced different numbers from the pack.
			for _, u := range uses {
				if !isTempName(u.Obj) {
					continue
				}
				for l := range liveNow {
					if isTempName(l.Obj) && l.Obj != u.Obj {
						addEdge(interfere, u.Obj, l.Obj)
					}
				}
			}
			for _, d := range defs {
				delete(live, d)
			}
			for _, u := range uses {
				live[u] = true
			}
		}
	}

	// greedy colouring in a deterministic order
	var names []string
	for n := range all {
		names = append(names, n)
	}
	sort.Strings(names)

	colour := map[string]int{}
	for _, n := range names {
		used := map[int]bool{}
		for peer := range interfere[n] {
			if c, ok := colour[peer]; ok {
				used[c] = true
			}
		}
		c := 0
		for used[c] {
			c++
		}
		colour[n] = c
	}

	maxColour := -1
	for _, c := range colour {
		if c > maxColour {
			maxColour = c
		}
	}
	rename := map[string]string{}
	for n, c := range colour {
		// Same prefix Tmp emits. A bare `t0` here was an ordinary name an
		// author could declare, and isTempName then treated it as scratch.
		rename[n] = ir.TempPrefix + itoa(base+c) + ir.TempSuffix
	}
	return rename, maxColour + 1
}

func applyRename(fn *ir.Function, rename map[string]string) {
	rn := func(l ir.Loc) ir.Loc {
		if l.Holder != "@s" {
			return l
		}
		if n, ok := rename[l.Obj]; ok {
			l.Obj = n
		}
		return l
	}
	for _, b := range fn.Blocks {
		for i, op := range b.Ops {
			switch o := op.(type) {
			case ir.Set:
				o.Dst = rn(o.Dst)
				b.Ops[i] = o
			case ir.Copy:
				o.Dst = rn(o.Dst)
				o.Src = rn(o.Src)
				b.Ops[i] = o
			case ir.Arith:
				o.Dst = rn(o.Dst)
				o.Src = rn(o.Src)
				b.Ops[i] = o
			case ir.Add:
				o.Dst = rn(o.Dst)
				b.Ops[i] = o
			case ir.Remove:
				o.Dst = rn(o.Dst)
				b.Ops[i] = o
			case ir.Guarded:
				// The predicate names objectives too. Renaming the ops but not
				// the condition produced guards reading `t8` while the
				// arithmetic wrote `t0`.
				o.Inner = renameOp(o.Inner, rename)
				o.Cond = renamePred(o.Cond, rename)
				b.Ops[i] = o
			case ir.Raw:
				// A raw command names locations in two places: the Args that get
				// spliced into its text at render time, and the declared effects
				// that liveness reads. Renaming neither left the op claiming to
				// write t0 while the rendered command still said t1.
				for k := range o.Args {
					o.Args[k] = rn(o.Args[k])
				}
				for k := range o.Writes {
					o.Writes[k] = rn(o.Writes[k])
				}
				for k := range o.Reads {
					o.Reads[k] = rn(o.Reads[k])
				}
				b.Ops[i] = o
			}
		}
		if br, ok := b.Term.(ir.Branch); ok {
			br.Cond = renamePred(br.Cond, rename)
			b.Term = br
		}
	}
}

// renameOp applies a temporary renaming to an op's locations.
func renameOp(op ir.Op, rename map[string]string) ir.Op {
	rn := func(l ir.Loc) ir.Loc {
		if l.Holder != "@s" {
			return l
		}
		if n, ok := rename[l.Obj]; ok {
			l.Obj = n
		}
		return l
	}
	switch o := op.(type) {
	case ir.Set:
		o.Dst = rn(o.Dst)
		return o
	case ir.Copy:
		o.Dst, o.Src = rn(o.Dst), rn(o.Src)
		return o
	case ir.Arith:
		o.Dst, o.Src = rn(o.Dst), rn(o.Src)
		return o
	case ir.Add:
		o.Dst = rn(o.Dst)
		return o
	case ir.Remove:
		o.Dst = rn(o.Dst)
		return o
	}
	return op
}

// renamePred applies a temporary renaming to every condition in a predicate.
//
// It COPIES the alternatives rather than editing them in place, because a
// predicate's Alts slice can be shared by more than one op. `inlineLeafArms`
// hands the branch's own Cond to the Guarded it creates, so the Guarded and
// the Branch terminator point at one array -- and this function used to run
// for each of them. The second run then saw names the first had already
// rewritten and rewrote them AGAIN:
//
//	[t1 <= 0 and t3 >= 0]
//	-> [t0 <= 0 and t1 >= 0]      first run, correct
//	-> [t0 <= 0 and t0 >= 0]      second run, t1 renamed as if it were t3
//
// That collapsed a two-sided range into `t0 == 0` -- a different condition,
// and one that fires on different inputs. The reference data pack's
// random-number wraparound guard is written exactly this way, so -O2 produced
// different numbers from the pack while compiling cleanly.
func renamePred(p ir.Predicate, rename map[string]string) ir.Predicate {
	if len(p.Alts) == 0 {
		return p
	}
	alts := make([][]ir.Cond, len(p.Alts))
	for ai := range p.Alts {
		alts[ai] = make([]ir.Cond, len(p.Alts[ai]))
		copy(alts[ai], p.Alts[ai])
		for ci := range alts[ai] {
			if n, ok := rename[alts[ai][ci].Obj]; ok {
				alts[ai][ci].Obj = n
			}
		}
	}
	p.Alts = alts
	return p
}

// ── CollectConsts ───────────────────────────────────────────────────────

// CollectConsts rebuilds the program's constant list from the FINAL IR.
//
// Lowering records every literal it materialises, including ones that later
// passes delete. Collecting here instead means the emitted pool contains only
// constants that survived optimisation.
type CollectConsts struct{}

func (CollectConsts) Name() string { return "collect-consts" }

func (CollectConsts) Run(p *ir.Program) *ir.Program {
	seen := map[string]int{}
	for _, fn := range p.Funcs {
		for _, b := range fn.Blocks {
			for _, op := range b.Ops {
				for _, l := range append(analysis.Defs(op), analysis.Uses(op)...) {
					if l.Obj == ir.ConstObjective && l.Holder != "" {
						if v, ok := parseConstName(l.Holder); ok {
							seen[l.Holder] = v
						}
					}
				}
			}
		}
	}
	var names []string
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	p.Consts = p.Consts[:0]
	for _, n := range names {
		p.Consts = append(p.Consts, seen[n])
	}
	return p
}

func parseConstName(holder string) (int, bool) {
	if len(holder) < 2 || holder[0] != '#' {
		return 0, false
	}
	body := holder[1:]
	neg := false
	if len(body) > 0 && body[0] == 'n' {
		neg = true
		body = body[1:]
	}
	if body == "" {
		return 0, false
	}
	v := 0
	for _, c := range body {
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + int(c-'0')
	}
	if neg {
		v = -v
	}
	return v, true
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
