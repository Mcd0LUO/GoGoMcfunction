package lower

import (
	"fmt"
	"strings"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/ir"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/target"
)

// assignInto stores the value of e into dst.
//
// When dst is a plain @s objective and e is a binary arithmetic op, the left
// operand is written STRAIGHT INTO dst rather than into a scratch slot. That
// removes two commands per binary op. The optimisation is only safe when the
// right operand does not mention dst, because @s is a single shared register
// file and writing dst early would clobber it.
func (l *Lowerer) assignInto(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, dst ir.Loc, e syntax.Expr) {
	if lit, ok := constInt(e); ok {
		fb.Emit(block, ir.Set{Dst: dst, Value: lit})
		return
	}
	// A call in initialiser position needs no scratch slot. call() hands
	// back a temporary because the value has to survive whatever the
	// enclosing expression does next; here there is no next, so dst can
	// read the ABI return register directly.
	if c, ok := e.(syntax.Call); ok && l.callInto(ctx, fb, block, dst, c) {
		return
	}
	if dst.Holder == "@s" {
		if bin, ok := e.(syntax.Binary); ok && !isComparison(bin.Op) {
			if l.assignBinaryInto(ctx, fb, block, dst.Obj, bin) {
				return
			}
		}
	}
	src, err := l.expr(ctx, fb, block, e)
	if err != nil {
		l.diags.Error(posOf(exprPos(e)), "E_EXPR", "%s", err)
		return
	}
	fb.Emit(block, ir.Copy{Dst: dst, Src: src})
}

func (l *Lowerer) assignBinaryInto(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, dst string, bin syntax.Binary) bool {
	op, ok := arithOp(bin.Op)
	if !ok {
		return false
	}
	if exprMentions(bin.R, ctx.loc(dst), ctx) {
		return false
	}
	// When the left operand is itself an arithmetic op, compute it into dst
	// as well. Writing (a + b) * c then costs three commands rather than
	// four, because the intermediate result never needs a slot:
	//
	//	dst = a; dst += b; dst *= c
	//
	// This is safe for the same reason the copy below is: dst is written
	// only after its old value has been read, and the right operand is
	// known not to mention dst. An ABI register as dst is safe too --
	// bindParams moves any parameter a nested call could reach into a
	// local, so a dst still in a0.. is one no call in this function
	// touches.
	if inner, ok := bin.L.(syntax.Binary); ok && !isComparison(inner.Op) {
		if l.assignBinaryInto(ctx, fb, block, dst, inner) {
			rhs, err := l.expr(ctx, fb, block, bin.R)
			if err != nil {
				l.diags.Error(posOf(exprPos(bin.R)), "E_EXPR", "%s", err)
				return true
			}
			fb.Emit(block, ir.Arith{Dst: ctx.loc(dst), Op: op, Src: rhs})
			return true
		}
	}
	lhs, err := l.expr(ctx, fb, block, bin.L)
	if err != nil {
		l.diags.Error(posOf(exprPos(bin.L)), "E_EXPR", "%s", err)
		return true
	}
	fb.Emit(block, ir.Copy{Dst: ctx.loc(dst), Src: lhs})
	rhs, err := l.expr(ctx, fb, block, bin.R)
	if err != nil {
		l.diags.Error(posOf(exprPos(bin.R)), "E_EXPR", "%s", err)
		return true
	}
	fb.Emit(block, ir.Arith{Dst: ctx.loc(dst), Op: op, Src: rhs})
	return true
}

// exprMentions reports whether an expression reads the given location.
//
// It takes the location rather than an objective name because a class
// field lives on a truncated objective ("C.hp") while the source spells it
// "hp", and comparing the wrong pair would either miss the read or invent
// one.
func exprMentions(e syntax.Expr, at ir.Loc, ctx *fnCtx) bool {
	switch x := e.(type) {
	case syntax.Ident:
		if strings.Contains(x.Name, ".") {
			return false
		}
		if ctx.isField(x.Name) {
			return at.Obj == ctx.fieldLoc(x.Name)
		}
		return at.Holder == "@s" && ctx.slot(x.Name) == at.Obj
	case syntax.Binary:
		return exprMentions(x.L, at, ctx) || exprMentions(x.R, at, ctx)
	case syntax.Unary:
		return exprMentions(x.X, at, ctx)
	case syntax.Call:
		for _, a := range x.Args {
			if exprMentions(a, at, ctx) {
				return true
			}
		}
	}
	return false
}

// expr lowers an expression, returning the location holding its value.
func (l *Lowerer) expr(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, e syntax.Expr) (ir.Loc, error) {
	switch x := e.(type) {
	case syntax.IntLit:
		return fb.Const(x.Value), nil
	case syntax.Ident:
		if strings.HasPrefix(x.Name, "@") {
			return ir.Loc{}, fmt.Errorf("selector %s cannot be used as a value", x.Name)
		}
		if xd, ok := l.externs[x.Name]; ok && (xd.Kind == "var" || xd.Kind == "global") {
			return l.externTarget(ctx, xd, x.P), nil
		}
		if v, ok := l.kt[x.Name]; ok {
			return fb.Const(v), nil
		}
		// A declared objective reads by its own name. Missing this while the
		// WRITE path had it made `ticks = ticks + 1` compile to
		// `ticks = l0 + 1` -- the target landed on the right objective but
		// the value came from a fresh local that nothing ever wrote, so the
		// counter was stuck at 1 forever.
		//
		// It is checked BEFORE the field, and it replaced a `holder.objective`
		// fallback for any dotted name. That fallback was wrong: an objective
		// name may itself contain dots -- `Ap.Amount`, `wp.damage1` are real
		// names in the reference data pack -- so `Ap.Amount` was split into
		// holder `Ap` and objective `Amount`, and the command named a score
		// nothing writes. A wrong answer with no diagnostic.
		if l.objs[x.Name] {
			return l.entityScore(ctx, x.Name, x.P), nil
		}
		// `e.field` inside `for e in entities<...>`: e IS @s there.
		if ctx.loopVar != "" && strings.HasPrefix(x.Name, ctx.loopVar+".") {
			return l.entityScore(ctx, x.Name[len(ctx.loopVar)+1:], x.P), nil
		}
		// A field of the enclosing class beats a fresh local. Fields are
		// checked after externs and constants so an explicit binding
		// still wins.
		if ctx.isField(x.Name) {
			return l.entityScore(ctx, ctx.fieldLoc(x.Name), x.P), nil
		}
		// `holder.objective` is shorthand for a score on a fake player.
		//
		// It is checked LAST, after declared objectives and fields, because it
		// is ambiguous with an objective whose name contains a dot -- and that
		// shape is far more common. The reference data pack declares
		// `Ap.Amount`, `Count.残照花魄`, `Time.day`, `timer.tick` and dozens
		// more; only two names in this repository's own examples use the
		// shorthand. Checking the shorthand first silently split every one of
		// those objectives into a holder and a shorter objective name.
		//
		// Declaration wins, and an undeclared dotted name is still the
		// shorthand -- so both spellings keep working, and the one that is
		// written down explicitly is the one that is unambiguous.
		if i := strings.Index(x.Name, "."); i > 0 {
			return ir.Loc{Holder: x.Name[:i], Obj: x.Name[i+1:]}, nil
		}
		return ctx.loc(ctx.slot(x.Name)), nil
	case syntax.Call:
		return l.call(ctx, fb, block, x)
	case syntax.Binary:
		if isComparison(x.Op) {
			return l.comparison(ctx, fb, block, x)
		}
		return l.binary(ctx, fb, block, x)
	case syntax.Unary:
		return l.unary(ctx, fb, block, x)
	case syntax.Field:
		// `<selector>."objective"` -- another entity's score, read in place.
		//
		// 1.12.2 allows a selector on the RIGHT of a scoreboard operation but
		// not on the left, so this is a read-only shape: it can be copied out
		// of, never assigned to. That asymmetry is the engine's, and hiding it
		// would mean copying through a temporary and writing back, which is a
		// different operation with different failure modes.
		if base, ok := x.X.(syntax.Ident); ok && strings.HasPrefix(base.Name, "@") {
			return ir.Loc{Holder: base.Name, Obj: x.Name}, nil
		}
		if _, ok := x.X.(syntax.SelfExpr); ok {
			return l.entityScore(ctx, x.Name, x.P), nil
		}
		return ir.Loc{}, fmt.Errorf("only a selector or self can qualify an objective with a dot")
	}
	return ir.Loc{}, fmt.Errorf("unsupported expression")
}

func (l *Lowerer) binary(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, x syntax.Binary) (ir.Loc, error) {
	lhs, err := l.expr(ctx, fb, block, x.L)
	if err != nil {
		return ir.Loc{}, err
	}
	rhs, err := l.expr(ctx, fb, block, x.R)
	if err != nil {
		return ir.Loc{}, err
	}
	op, ok := arithOp(x.Op)
	if !ok {
		return ir.Loc{}, fmt.Errorf("%s is not an arithmetic operator", x.Op)
	}
	t := fb.TmpLoc()
	fb.Emit(block, ir.Copy{Dst: t, Src: lhs})
	fb.Emit(block, ir.Arith{Dst: t, Op: op, Src: rhs})
	return t, nil
}

func (l *Lowerer) unary(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, x syntax.Unary) (ir.Loc, error) {
	src, err := l.expr(ctx, fb, block, x.X)
	if err != nil {
		return ir.Loc{}, err
	}
	t := fb.TmpLoc()
	switch x.Op {
	case syntax.MINUS:
		fb.Emit(block, ir.Copy{Dst: t, Src: src})
		fb.Emit(block, ir.Arith{Dst: t, Op: "*=", Src: fb.Const(-1)})
	case syntax.NOT:
		fb.Emit(block, ir.Set{Dst: t, Value: 0})
		// NOTE: this is a conditional write inside a straight-line block. It is
		// expressed as a branch in expression position, which the CFG cannot
		// represent without splitting the block, so negation is materialised
		// arithmetically instead: !x = (x == 0).
		fb.Emit(block, ir.Raw{Text: "# not: see lower.builtin not"})
		return ir.Loc{}, fmt.Errorf("unary ! needs lowering support")
	default:
		return ir.Loc{}, fmt.Errorf("unsupported unary operator %s", x.Op)
	}
	return t, nil
}

// comparison materialises a boolean into a 0/1 slot using a two-block shape.
func (l *Lowerer) comparison(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, x syntax.Binary) (ir.Loc, error) {
	d := fb.Tmp()
	p, err := l.lowerCondInto(ctx, fb, block, d, x)
	if err != nil {
		return ir.Loc{}, err
	}
	r := fb.TmpLoc()
	// Conditionally write 1 WITHOUT splitting the block. Splitting forced the
	// caller to stop emitting into `block`, which silently dropped the rest of
	// the enclosing expression.
	seen := map[string]bool{}
	for _, alt := range p.Alts {
		for _, c := range alt {
			seen[c.Obj] = true
		}
	}
	if len(p.Alts) == 0 {
		fb.Emit(block, ir.Set{Dst: r, Value: 1})
		return r, nil
	}
	fb.Emit(block, ir.Set{Dst: r, Value: 0})
	fb.Emit(block, ir.Guarded{Inner: ir.Set{Dst: r, Value: 1}, Cond: p})
	return r, nil
}

// lowerCond lowers a condition to a predicate, appending any ops it needs to
// the given block.
func (l *Lowerer) lowerCond(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, e syntax.Expr) (ir.Predicate, error) {
	if call, ok := e.(syntax.Call); ok && call.Name == "any" && len(call.Args) == 1 {
		if sel, ok := call.Args[0].(syntax.Ident); ok && strings.HasPrefix(sel.Name, "@") {
			return ir.Predicate{Exist: &ir.ExistTest{Selector: sel.Name}}, nil
		}
	}
	if mc, ok := e.(syntax.MatchCond); ok {
		return l.lowerMatch(ctx, fb, block, mc)
	}
	if call, ok := e.(syntax.Call); ok && call.Name == "detect" {
		return l.lowerBlockProbe(ctx, fb, block, call)
	}
	// `a && b` is a conjunction, which a selector expresses directly: one
	// alternative carrying both conditions. `a || b` is a disjunction, which
	// is two alternatives -- and the Predicate is already in DNF, so neither
	// needs a temporary or a nested block.
	//
	// Both were missing while the specification documented them, and the
	// reference data pack is written almost entirely in this shape:
	// `@s[score_menu_min=1,score_menu=99]` IS `menu >= 1 && menu <= 99`.
	// Without it every such condition had to be nested ifs, which cost a
	// block each and -- before the guard fix -- could silently invert.
	if bin, ok := e.(syntax.Binary); ok {
		switch bin.Op {
		case syntax.AND:
			left, err := l.lowerCond(ctx, fb, block, bin.L)
			if err != nil {
				return ir.Predicate{}, err
			}
			right, err := l.lowerCond(ctx, fb, block, bin.R)
			if err != nil {
				return ir.Predicate{}, err
			}
			return andPredicates(left, right), nil
		case syntax.OR:
			left, err := l.lowerCond(ctx, fb, block, bin.L)
			if err != nil {
				return ir.Predicate{}, err
			}
			right, err := l.lowerCond(ctx, fb, block, bin.R)
			if err != nil {
				return ir.Predicate{}, err
			}
			return orPredicates(left, right), nil
		}
	}
	// `!c` negates the predicate. There is no `unless` form for an arbitrary
	// condition in 1.12.2, so a negation is materialised into a flag: set 1,
	// clear it when the inner condition holds, then test the flag. That costs
	// two commands and is correct; refusing to compile it was worse, because
	// `if !any<...>` is how an author writes "nothing matched".
	//
	// The comparison and existence cases are handled directly by the caller
	// where a negated selector is available; this is the general fallback.
	if un, ok := e.(syntax.Unary); ok && un.Op == syntax.NOT {
		inner, err := l.lowerCond(ctx, fb, block, un.X)
		if err != nil {
			return ir.Predicate{}, err
		}
		// A bare existence test has an exact `unless` form, so it needs no flag.
		if inner.Exist != nil && len(inner.Alts) == 0 && inner.Block == nil {
			return ir.Predicate{Exist: &ir.ExistTest{
				Selector: inner.Exist.Selector,
				Negate:   !inner.Exist.Negate,
			}}, nil
		}
		d := fb.Tmp()
		fb.Emit(block, ir.Set{Dst: ctx.loc(d), Value: 1})
		fb.Emit(block, ir.Guarded{Inner: ir.Set{Dst: ctx.loc(d), Value: 0}, Cond: inner})
		return ir.Predicate{Alts: [][]ir.Cond{{{Obj: d, HasMin: true, Min: 1}}}}, nil
	}
	if bin, ok := e.(syntax.Binary); ok && isComparison(bin.Op) {
		d := fb.Tmp()
		return l.lowerCondInto(ctx, fb, block, d, bin)
	}
	// boolean-valued expression: compare against 1 so the slot becomes 0/1
	src, err := l.expr(ctx, fb, block, e)
	if err != nil {
		return ir.Predicate{}, err
	}
	d := fb.Tmp()
	fb.Emit(block, ir.Copy{Dst: ctx.loc(d), Src: src})
	fb.Emit(block, ir.Arith{Dst: ctx.loc(d), Op: "-=", Src: fb.Const(1)})
	return ir.Predicate{Alts: [][]ir.Cond{{{Obj: d, HasMin: true, Min: 0, HasMax: true, Max: 0}}}}, nil
}

// andPredicates combines two conditions with AND.
//
// A selector expresses a conjunction as comma-separated arguments, so the
// common case is a cross product of the alternatives: (a|b) and (c|d) is
// ac|ad|bc|bd. An existence test is not an alternative -- it names OTHER
// entities -- so it cannot join a conjunction that also constrains @s.
//
// When that happens the two halves are materialised into a flag instead of
// being expressed in a selector, which is correct at the cost of two
// commands. Reporting an error would be worse: the condition is meaningful,
// just not expressible in one selector.
func andPredicates(a, b ir.Predicate) ir.Predicate {
	if a.IsAlways() {
		return b
	}
	if b.IsAlways() {
		return a
	}
	// An existence or block probe on either side: keep it and fold the other
	// side into the Alts that guard it. The backend already knows how to
	// combine `Exist` with `Alts` (it emits `execute @s[<alts>] ~ ~ ~ function
	// F if <sel>`), so the conjunction stays representable as long as only ONE
	// side carries a probe.
	if a.Exist != nil && b.Exist != nil {
		return ir.Predicate{Alts: crossAlts(a.Alts, b.Alts)}
	}
	if a.Exist != nil || a.Block != nil {
		out := a
		out.Alts = crossAlts(a.Alts, b.Alts)
		return out
	}
	if b.Exist != nil || b.Block != nil {
		out := b
		out.Alts = crossAlts(b.Alts, a.Alts)
		return out
	}
	return ir.Predicate{Alts: crossAlts(a.Alts, b.Alts)}
}

// orPredicates combines two conditions with OR by concatenating alternatives.
//
// A selector cannot express a disjunction, so each alternative becomes its own
// guarded call. That is what the backend already does for multi-alternative
// predicates.
//
// An existence test on either side is not an alternative and has no OR form:
// `exists @e[tag=x] or y == 1` would need `unless`-of-both. It is rejected
// rather than silently dropped, because dropping it would make the branch fire
// on a subset of the inputs the author wrote.
func orPredicates(a, b ir.Predicate) ir.Predicate {
	if a.IsAlways() || b.IsAlways() {
		return ir.Predicate{}
	}
	out := ir.Predicate{Alts: append(append([][]ir.Cond{}, a.Alts...), b.Alts...)}
	if a.Exist != nil {
		out.Exist = a.Exist
	}
	if b.Exist != nil {
		out.Exist = b.Exist
	}
	if a.Block != nil {
		out.Block = a.Block
	}
	if b.Block != nil {
		out.Block = b.Block
	}
	return out
}

// crossAlts is the DNF cross product of two alternative lists.
func crossAlts(a, b [][]ir.Cond) [][]ir.Cond {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	out := make([][]ir.Cond, 0, len(a)*len(b))
	for _, x := range a {
		for _, y := range b {
			merged := make([]ir.Cond, 0, len(x)+len(y))
			merged = append(merged, x...)
			merged = append(merged, y...)
			out = append(out, merged)
		}
	}
	return out
}

// lowerBlockProbe turns `detect(...)` into a flag the branch can read.
//
// 1.12.2 can only test a block by running a command conditionally: there is no
// boolean expression, and `unless detect` does not exist. So the probe is:
//
//	set @s flag 0
//	execute @s ~ ~ ~ detect <x> <y> <z> <block> <data> scoreboard players set @s flag 1
//
// and then the branch tests `flag == 1`, which the existing predicate
// machinery already knows how to render -- including its complement, which is
// the thing `detect` alone cannot express.
//
// This costs two commands and a slot. The alternative was a second kind of
// condition in the IR that every pass would have to learn about, and one that
// `while` could not use.
func (l *Lowerer) lowerBlockProbe(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, c syntax.Call) (ir.Predicate, error) {
	if len(c.Args) != 6 {
		return ir.Predicate{}, fmt.Errorf("detect needs (x, y, z, block, data, target): the position to probe, the block, and who probes it")
	}
	// Arguments 0..4 are command text; argument 5 is the probe's executor.
	coords := make([]string, 5)
	for i := 0; i < 5; i++ {
		v, ok := l.rawArg(c, i)
		if !ok {
			return ir.Predicate{}, fmt.Errorf("detect argument %d must be a literal", i+1)
		}
		coords[i] = v
	}
	sel := l.targetArg(c, 5)

	// TmpLoc, not Tmp: Tmp only mints a NAME, and the holder has to come
	// from the function -- a [load] function keeps its registers on a fake
	// player, so writing @s there would land on nothing.
	f := fb.TmpLoc()
	fb.Emit(block, ir.Set{Dst: f, Value: 0})
	fb.Emit(block, ir.Raw{
		// The flag is %s, not fmt.Sprintf("%s %s", ...): a location baked
		// into the text cannot be renamed with the op that writes it, and the
		// two then disagree -- the op said t0, the command said t1, and the
		// branch read the wrong one at -O2.
		Text: fmt.Sprintf("execute %s ~ ~ ~ detect %s %s %s %s %s scoreboard players set %%s 1",
			sel, coords[0], coords[1], coords[2], coords[3], coords[4]),
		Args: []ir.Loc{f},
		// Declared, not inferred: without this the flag looks dead to liveness
		// and the next probe is coloured onto the same objective.
		Writes: []ir.Loc{f},
	})
	return ir.Predicate{Alts: [][]ir.Cond{{{Obj: f.Obj, HasMin: true, Min: 1}}}}, nil
}

// lowerCondInto computes lhs - rhs into slot d and returns the true predicate.
//
// Two algebraic identities are applied first, because they turn a comparison
// that would need a scratch slot and an extra selector scan into a plain
// two-alternative predicate:
//
//	abs(x) >= n  <=>  x <= -n  OR  x >= n
//	abs(x) <  n  <=>  -n < x < n
func (l *Lowerer) lowerCondInto(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, d string, x syntax.Binary) (ir.Predicate, error) {
	if p, ok, err := l.lowerAbsComparison(ctx, fb, block, d, x); ok {
		return p, err
	}
	// Compute the left side straight into d.
	//
	// d is always a fresh temporary here, so nothing in x.R can read it,
	// and a nested call cannot collide with it either: names are unique
	// across the module, so no callee writes this slot. That removes the
	// scratch slot a comparison used to need for its left operand --
	// rand(1, scale) <= p becomes "d = return; d -= p" instead of
	// "t = return; d = t; d -= p".
	if l.assignOperandInto(ctx, fb, block, d, x.L) {
		rhs, err := l.expr(ctx, fb, block, x.R)
		if err != nil {
			return ir.Predicate{}, err
		}
		fb.Emit(block, ir.Arith{Dst: ctx.loc(d), Op: "-=", Src: rhs})
		return l.predicateFor(d, x.Op)
	}
	lhs, err := l.expr(ctx, fb, block, x.L)
	if err != nil {
		return ir.Predicate{}, err
	}
	rhs, err := l.expr(ctx, fb, block, x.R)
	if err != nil {
		return ir.Predicate{}, err
	}
	fb.Emit(block, ir.Copy{Dst: ctx.loc(d), Src: lhs})
	fb.Emit(block, ir.Arith{Dst: ctx.loc(d), Op: "-=", Src: rhs})
	return l.predicateFor(d, x.Op)
}

// assignOperandInto computes e into the plain slot d, and reports whether
// it could.
//
// It covers the two shapes worth redirecting -- a call and an arithmetic
// chain -- and declines everything else so the caller keeps its general
// path.
func (l *Lowerer) assignOperandInto(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, d string, e syntax.Expr) bool {
	if c, ok := e.(syntax.Call); ok {
		return l.callInto(ctx, fb, block, ctx.loc(d), c)
	}
	if bin, ok := e.(syntax.Binary); ok && !isComparison(bin.Op) {
		return l.assignBinaryInto(ctx, fb, block, d, bin)
	}
	return false
}

func (l *Lowerer) lowerAbsComparison(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, d string, x syntax.Binary) (ir.Predicate, bool, error) {
	call, ok := x.L.(syntax.Call)
	if !ok || call.Name != "abs" || len(call.Args) != 1 {
		return ir.Predicate{}, false, nil
	}
	k, ok := constInt(x.R)
	if !ok {
		return ir.Predicate{}, false, nil
	}
	inner, err := l.expr(ctx, fb, block, call.Args[0])
	if err != nil {
		return ir.Predicate{}, true, err
	}
	// reuse the operand directly when it is already a plain @s slot
	slot := d
	if inner.Holder == "@s" {
		slot = inner.Obj
	} else {
		fb.Emit(block, ir.Copy{Dst: ctx.loc(d), Src: inner})
	}
	switch x.Op {
	case syntax.GE, syntax.GT:
		if k <= 0 {
			return ir.Predicate{}, true, nil // always true
		}
		lo := ir.Cond{Obj: slot, HasMax: true, Max: -k}
		hi := ir.Cond{Obj: slot, HasMin: true, Min: k}
		if x.Op == syntax.GT {
			lo = ir.Cond{Obj: slot, HasMax: true, Max: -k - 1}
			hi = ir.Cond{Obj: slot, HasMin: true, Min: k + 1}
		}
		return ir.Predicate{Alts: [][]ir.Cond{{lo}, {hi}}}, true, nil
	case syntax.LT, syntax.LE:
		if k <= 0 {
			return ir.Predicate{Alts: [][]ir.Cond{{}}}, true, nil // never true
		}
		lo, hi := -k, k
		if x.Op == syntax.LT {
			lo, hi = -k+1, k-1
		}
		condLo := ir.Cond{Obj: slot, HasMin: true, Min: lo}
		condHi := ir.Cond{Obj: slot, HasMax: true, Max: hi}
		return ir.Predicate{Alts: [][]ir.Cond{{condLo, condHi}}}, true, nil
	}
	return ir.Predicate{}, false, nil
}

// predicateFor delegates all version knowledge to target.CondAlts.
func (l *Lowerer) predicateFor(d string, op syntax.Kind) (ir.Predicate, error) {
	alts, err := target.CondAlts(comparisonText(op))
	if err != nil {
		return ir.Predicate{}, err
	}
	var p ir.Predicate
	for _, c := range alts {
		p.Alts = append(p.Alts, []ir.Cond{{Obj: d, HasMin: c.HasMin, Min: c.Min, HasMax: c.HasMax, Max: c.Max}})
	}
	return p, nil
}

func comparisonText(k syntax.Kind) string {
	switch k {
	case syntax.LT:
		return "<"
	case syntax.LE:
		return "<="
	case syntax.GT:
		return ">"
	case syntax.GE:
		return ">="
	case syntax.EQ:
		return "=="
	case syntax.NEQ:
		return "!="
	}
	return ""
}

// externLoc resolves a bound score. The holder is the body's register file
// unless the declaration named a fake player, which is how a global is
// spelled.
//
// A binding that names @s explicitly stays on @s: the author asked for the
// executor, and in a body that has none the access is reported by the
// caller rather than silently retargeted to a fake player.
func externLoc(c *fnCtx, d syntax.ExternDecl) ir.Loc {
	if d.Holder != "" && d.Holder != "@s" {
		return ir.Loc{Holder: d.Holder, Obj: d.Objective}
	}
	if d.Objective != "" {
		return c.loc(d.Objective)
	}
	return c.loc(d.Name)
}

func isComparison(k syntax.Kind) bool {
	switch k {
	case syntax.EQ, syntax.NEQ, syntax.LT, syntax.LE, syntax.GT, syntax.GE:
		return true
	}
	return false
}

func arithOp(k syntax.Kind) (string, bool) {
	switch k {
	case syntax.PLUS:
		return "+=", true
	case syntax.MINUS:
		return "-=", true
	case syntax.STAR:
		return "*=", true
	case syntax.SLASH:
		return "/=", true
	case syntax.PERCENT:
		return "%=", true
	}
	return "", false
}

func exprPos(e syntax.Expr) syntax.Pos {
	switch x := e.(type) {
	case syntax.IntLit:
		return x.P
	case syntax.Ident:
		return x.P
	case syntax.Binary:
		return x.P
	case syntax.Call:
		return x.P
	case syntax.StringLit:
		return x.P
	case syntax.BoolLit:
		return x.P
	case syntax.Unary:
		return x.P
	}
	return syntax.Pos{}
}

func constInt(e syntax.Expr) (int, bool) {
	switch x := e.(type) {
	case syntax.IntLit:
		return x.Value, true
	case syntax.Unary:
		if x.Op == syntax.MINUS {
			if v, ok := constInt(x.X); ok {
				return -v, true
			}
		}
	}
	return 0, false
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [12]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func itoaJoin(obj string, lo, hi int) string {
	return "score_" + obj + "_min=" + itoa(lo) + ",score_" + obj + "=" + itoa(hi)
}
