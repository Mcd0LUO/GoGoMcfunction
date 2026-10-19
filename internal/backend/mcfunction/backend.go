// Package mcfunction renders IR to Minecraft 1.12.2 mcfunction text.
//
// The back end knows commands and the target's condition semantics; it knows
// nothing about CMCF syntax. Each IR block becomes one .mcfunction file, which
// is forced by 1.12.2 having no intra-file labels.
package mcfunction

import (
	"fmt"
	"sort"
	"strings"

	"cmcf/internal/ir"
	"cmcf/internal/mc"
	"cmcf/internal/target"
)

// Options configures rendering.
type Options struct {
	Target target.Target
	// BlockPath maps (function, block) to the generated function name.
	BlockPath func(fn string, id ir.BlockID) string
	// Comments puts a header naming the declaration each file came from.
	Comments bool
}

// Render converts a program into files keyed by function path.
//
// Ordering matters: every block is rendered first, THEN edges into blocks that
// produced no commands are removed. Checking emptiness before rendering made
// every target look empty, which deleted the loop back edge.
func Render(prog *ir.Program, opt Options) (map[string]string, []string) {
	if opt.Target.Name == "" {
		opt.Target = target.V1_12_2
	}
	if opt.BlockPath == nil {
		opt.BlockPath = func(fn string, id ir.BlockID) string {
			return fmt.Sprintf("%s$b%d", fn, id)
		}
	}

	files := map[string][]mc.Command{}
	var order []string
	seen := map[string]bool{}
	add := func(name string, cmds ...mc.Command) {
		if !seen[name] {
			seen[name] = true
			order = append(order, name)
		}
		files[name] = append(files[name], cmds...)
	}

	// Objective declarations.
	//
	// 1.12.2 requires an objective to exist before any score is written to it.
	// `scoreboard players operation @s t0 = @s a0` on a nonexistent objective
	// fails at runtime with "unknown objective t0" — the command is valid, the
	// program just does nothing. Emitting the declarations is therefore a
	// correctness requirement, not a nicety.
	if objs := usedObjectives(prog); len(objs) > 0 {
		for _, o := range objs {
			add(prog.Namespace+"/init", mc.ObjectiveAdd{Name: o, Display: "cmcf"})
		}
	}

	// Constant pool.
	//
	// These are fake players on objective cmcf.c. They persist in the save, so
	// they only need initialising once — but they DO need initialising, or every
	// literal reads as 0. An earlier revision emitted the pool as a normal
	// function with no caller, so `t0 -= #100` silently subtracted zero.
	//
	// 1.12.2 has no data-pack load tag, so the pool is emitted into BOTH:
	//   - <ns>/consts, for explicit wiring, and
	//   - the entry function's prologue is NOT used (it would run per call).
	// The CLI prints a note telling the user to call <ns>/consts on world load.
	if len(prog.Consts) > 0 {
		// The constant objective must exist before any literal is written.
		add(prog.Namespace+"/init", mc.ObjectiveAdd{Name: ir.ConstObjective, Display: "cmcf"})
		for _, v := range dedupe(prog.Consts) {
			add(prog.Namespace+"/init", mc.ScoreboardSet{
				Target:    ir.ConstName(v),
				Objective: ir.ConstObjective,
				Value:     v,
			})
		}
	}

	// blockName maps an IR block to its generated function name.
	blockName := func(fn *ir.Function, id ir.BlockID) string {
		if id == 0 {
			return fn.Path
		}
		return opt.BlockPath(fn.Path, id)
	}

	for _, fn := range prog.Funcs {
		for _, blk := range fn.Blocks {
			name := blockName(fn, blk.ID)
			var cmds []mc.Command
			for _, op := range blk.Ops {
				cmds = append(cmds, renderOp(opt.Target, op)...)
			}
			cmds = append(cmds, renderTerm(opt.Target, fn.Path, blk.Term, func(f string, id ir.BlockID) string {
				if id == 0 {
					return f
				}
				return opt.BlockPath(f, id)
			})...)
			add(name, cmds...)
		}
	}

	// A call to a block that emits nothing costs a call and does nothing.
	silent := map[string]bool{}
	for name, cmds := range files {
		if len(cmds) == 0 {
			silent[name] = true
		}
	}
	for name, cmds := range files {
		var kept []mc.Command
		for _, c := range cmds {
			if isCallTo(c, silent) {
				continue
			}
			kept = append(kept, c)
		}
		files[name] = kept
	}

	origin := map[string]string{}
	if opt.Comments {
		for _, fn := range prog.Funcs {
			if fn.Origin == "" {
				continue
			}
			origin[fn.Path] = fn.Origin
			for _, b := range fn.Blocks {
				origin[opt.BlockPath(fn.Path, b.ID)] = fn.Origin
			}
		}
	}

	out := map[string]string{}
	var keptOrder []string
	for _, p := range order {
		cmds := files[p]
		if len(cmds) == 0 {
			continue // nothing to emit and nothing can reach it
		}
		var sb strings.Builder
		if o := origin[p]; o != "" {
			// 1.12.2 skips '#' lines, so this costs a line in the file
			// and nothing at run time.
			fmt.Fprintf(&sb, "# %s — declared at %s\n", p, o)
		}
		for _, c := range cmds {
			sb.WriteString(c.Line())
			sb.WriteByte('\n')
		}
		out[p] = sb.String()
		keptOrder = append(keptOrder, p)
	}
	return out, keptOrder
}

// isCallTo reports whether c invokes a function marked silent.
func isCallTo(c mc.Command, silent map[string]bool) bool {
	switch x := c.(type) {
	case mc.Function:
		return silent[x.Name]
	case mc.Execute:
		return isCallTo(x.Cmd, silent)
	}
	return false
}

func dedupe(xs []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, v := range xs {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}

// renderTerm lowers a terminator.
//
// A Branch becomes one conditional call per alternative. 1.12.2 selectors can
// express a conjunction but not a disjunction, so a DNF predicate with N
// alternatives becomes N parallel `execute` lines.
func renderTerm(t target.Target, fn string, term ir.Terminator, bp func(string, ir.BlockID) string) []mc.Command {
	switch x := term.(type) {
	case ir.Jump:
		return []mc.Command{mc.Function{Name: bp(fn, x.Target)}}
	case ir.TailCall:
		return []mc.Command{mc.Function{Name: x.Path}}
	case ir.Branch:
		return renderBranch(t, fn, x, bp)
	case ir.Return:
		return nil
	}
	return nil
}

// silent reports whether a block produces no commands. Calling such a block is
// a no-op that still pays call overhead, so the edge is dropped entirely.
func silent(blocks map[string][]mc.Command, name string) bool {
	return len(blocks[name]) == 0
}

func renderBranch(t target.Target, fn string, br ir.Branch, bp func(string, ir.BlockID) string) []mc.Command {
	thenName := bp(fn, br.Then)
	elseName := bp(fn, br.Else)

	if br.Cond.Exist != nil {
		// An existence test is a single selector lookup. The else edge is the
		// same selector with its tag conditions flipped, which is the only
		// negation 1.12.2 selectors support directly.
		if br.Cond.Exist.Negate {
			return []mc.Command{
				mc.Execute{Selector: parseSelector(br.Cond.Exist.Selector), Cmd: mc.Function{Name: elseName}},
				mc.Execute{Selector: negateSelector(br.Cond.Exist.Selector), Cmd: mc.Function{Name: thenName}},
			}
		}
		return []mc.Command{
			mc.Execute{Selector: parseSelector(br.Cond.Exist.Selector), Cmd: mc.Function{Name: thenName}},
			mc.Execute{Selector: negateSelector(br.Cond.Exist.Selector), Cmd: mc.Function{Name: elseName}},
		}
	}

	// The predicate is a disjunction. 1.12.2 has no boolean OR, so each
	// alternative becomes its own guarded call.
	var out []mc.Command
	for _, alt := range br.Cond.Alts {
		out = append(out, mc.Execute{Selector: selectorFor(t, alt), Cmd: mc.Function{Name: thenName}})
	}
	if len(br.Cond.Alts) == 0 {
		// Always true: the else edge is unreachable.
		return out
	}

	// The else edge needs the complement. Its DNF is the cross product of the
	// per-alternative complements; when that is a single conjunction it becomes
	// one selector, otherwise it becomes one selector per alternative.
	//
	// Rendering only the FIRST complement alternative would drop the rest and
	// make the exit edge fire on a subset of the intended inputs — a silent
	// wrong-answer bug, so the whole complement is always emitted.
	for _, alt := range ir.Negate(br.Cond).Alts {
		out = append(out, mc.Execute{Selector: selectorFor(t, alt), Cmd: mc.Function{Name: elseName}})
	}
	return out
}

// selectorFor builds @s with the given score conditions.
func selectorFor(t target.Target, conds []ir.Cond) *mc.Selector {
	sel := mc.SelfSelector()
	for _, c := range conds {
		sel.Arg(t.Condition(c.Obj, c))
	}
	return sel
}

func renderOp(t target.Target, op ir.Op) []mc.Command {
	switch o := op.(type) {
	case ir.Set:
		return []mc.Command{mc.ScoreboardSet{Target: o.Dst.Holder, Objective: o.Dst.Obj, Value: o.Value}}
	case ir.Add:
		return []mc.Command{mc.ScoreboardAdd{Target: o.Dst.Holder, Objective: o.Dst.Obj, Value: o.Value}}
	case ir.Remove:
		return []mc.Command{mc.ScoreboardRemove{Target: o.Dst.Holder, Objective: o.Dst.Obj, Value: o.Value}}
	case ir.Copy:
		return []mc.Command{mc.ScoreboardOp{Target: o.Dst.Holder, Objective: o.Dst.Obj, Op: "=", Source: o.Src.Holder, SrcObj: o.Src.Obj}}
	case ir.Arith:
		return []mc.Command{mc.ScoreboardOp{Target: o.Dst.Holder, Objective: o.Dst.Obj, Op: o.Op, Source: o.Src.Holder, SrcObj: o.Src.Obj}}
	case ir.Guarded:
		inner := renderOp(t, o.Inner)
		var out []mc.Command
		for _, alt := range o.Cond.Alts {
			sel := selectorFor(t, alt)
			for _, c := range inner {
				out = append(out, mc.Execute{Selector: sel, Cmd: c})
			}
		}
		return out
	case ir.Call:
		return []mc.Command{mc.Function{Name: o.Path}}
	case ir.Raw:
		return []mc.Command{mc.Raw{Text: o.Text}}
	}
	return nil
}

// parseSelector rebuilds a Selector from rendered selector text.
func parseSelector(text string) *mc.Selector {
	if len(text) < 2 || text[0] != '@' {
		return mc.SelfSelector()
	}
	sel := mc.NewSelector(mc.Base(text[:2]))
	for _, arg := range splitArgs(text) {
		sel.Arg(arg)
	}
	return sel
}

// negateSelector flips tag= conditions, which is the only negation 1.12.2
// selectors support directly.
func negateSelector(text string) *mc.Selector {
	if len(text) < 2 || text[0] != '@' {
		return mc.SelfSelector()
	}
	sel := mc.NewSelector(mc.Base(text[:2]))
	for _, arg := range splitArgs(text) {
		if strings.HasPrefix(arg, "tag=!") {
			sel.Arg("tag=" + strings.TrimPrefix(arg, "tag=!"))
			continue
		}
		if strings.HasPrefix(arg, "tag=") {
			sel.Arg("tag=!" + strings.TrimPrefix(arg, "tag="))
			continue
		}
		sel.Arg(arg)
	}
	return sel
}

func splitArgs(sel string) []string {
	i := strings.Index(sel, "[")
	if i < 0 || !strings.HasSuffix(sel, "]") {
		return nil
	}
	body := sel[i+1 : len(sel)-1]
	var out []string
	for _, p := range strings.Split(body, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Validate checks a rendered line against a crude 1.12.2 grammar. A malformed
// command makes the WHOLE function file fail to load, silently.
func Validate(line string) error {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return nil
	}
	if strings.HasPrefix(trimmed, "/") {
		return fmt.Errorf("function commands must not start with '/': %s", trimmed)
	}
	return nil
}

// collectCalls records every function name a command may invoke.
//
// It must recurse into Execute: a guarded call is `execute <sel> ~ ~ ~ function F`,
// and only looking at top-level Function commands made every guarded block look
// unreferenced, so empty-but-reachable blocks were pruned and the program called
// a function that did not exist.
func collectCalls(c mc.Command, out map[string]bool) {
	switch x := c.(type) {
	case mc.Function:
		out[x.Name] = true
	case mc.Execute:
		collectCalls(x.Cmd, out)
	}
}

// usedObjectives returns every objective the program touches, sorted.
//
// The constant objective is excluded: it is declared alongside the pool.
func usedObjectives(prog *ir.Program) []string {
	seen := map[string]bool{}
	note := func(l ir.Loc) {
		if l.Obj == "" || l.Obj == ir.ConstObjective {
			return
		}
		seen[l.Obj] = true
	}
	for _, fn := range prog.Funcs {
		for _, blk := range fn.Blocks {
			for _, op := range blk.Ops {
				switch o := op.(type) {
				case ir.Set:
					note(o.Dst)
				case ir.Copy:
					note(o.Dst)
					note(o.Src)
				case ir.Arith:
					note(o.Dst)
					note(o.Src)
				case ir.Add:
					note(o.Dst)
				case ir.Remove:
					note(o.Dst)
				case ir.Guarded:
					// handled below by recursion
				}
			}
			for _, c := range termConds(blk.Term) {
				seen[c] = true
			}
		}
	}
	var out []string
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// termConds lists objectives appearing in a terminator's conditions.
func termConds(t ir.Terminator) []string {
	br, ok := t.(ir.Branch)
	if !ok {
		return nil
	}
	var out []string
	for _, alt := range br.Cond.Alts {
		for _, c := range alt {
			if c.Obj != "" && c.Obj != ir.ConstObjective {
				out = append(out, c.Obj)
			}
		}
	}
	return out
}
