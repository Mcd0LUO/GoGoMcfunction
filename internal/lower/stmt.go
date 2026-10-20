package lower

import (
	"fmt"
	"strings"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/diag"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/ir"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
)

func posOf(p syntax.Pos) diag.Pos { return diag.Pos{Line: p.Line, Col: p.Col} }

// noFallThrough marks a block from which control cannot continue.
const noFallThrough = ir.BlockID(-1)

// lowerSeq lowers a statement list into block, returning the block that control
// continues in (or noFallThrough).
//
// The CFG makes early return trivial: `return` just terminates the current
// block. The join after an `if` is reachable only from the arms that actually
// fall through, so no statement relocation is needed.
func (l *Lowerer) lowerSeq(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, body []syntax.Stmt) ir.BlockID {
	cur := block
	for i, st := range body {
		if cur == noFallThrough {
			return noFallThrough
		}
		// implicit return: a trailing bare expression is the function's value
		if i == len(body)-1 {
			if es, ok := st.(syntax.ExprStmt); ok {
				if _, isCall := es.X.(syntax.Call); !isCall {
					l.assignInto(ctx, fb, cur, loc("return"), es.X)
					fb.SetTerm(cur, ir.Return{})
					return noFallThrough
				}
			}
		}
		switch s := st.(type) {
		case syntax.LetStmt:
			l.assignInto(ctx, fb, cur, loc(ctx.slot(s.Name)), s.Value)
		case syntax.AssignStmt:
			target, ok := lvalueName(s.Target)
			if !ok {
				l.diags.Error(posOf(s.P), "E_ASSIGN_TARGET", "assignment target must be a variable or self.field")
				continue
			}
			l.assignInto(ctx, fb, cur, l.targetLoc(ctx, target), assignValue(s))
		case syntax.ReturnStmt:
			if s.Value != nil {
				l.assignInto(ctx, fb, cur, loc("return"), s.Value)
			}
			fb.SetTerm(cur, ir.Return{})
			return noFallThrough
		case syntax.IfStmt:
			cur = l.lowerIf(ctx, fb, cur, s, body[i+1:])
			// lowerIf has already lowered the remainder into a continuation
			// block, so this loop is done.
			return cur
		case syntax.WhileStmt:
			cur = l.lowerWhile(ctx, fb, cur, s)
		case syntax.LoopStmt:
			cur = l.lowerLoop(ctx, fb, cur, s)
		case syntax.ForStmt:
			cur = l.lowerFor(ctx, fb, cur, s)
		case syntax.BroadcastStmt:
			l.lowerBroadcast(ctx, fb, cur, s)
		case syntax.DecodeStmt:
			l.lowerDecode(ctx, fb, cur, s)
		case syntax.EncodeStmt:
			l.lowerEncode(ctx, fb, cur, s)
		case syntax.ExprStmt:
			l.lowerCallStmt(ctx, fb, cur, s)
		}
	}
	return cur
}

func loc(obj string) ir.Loc { return ir.Loc{Holder: "@s", Obj: obj} }

// assignValue folds a compound assignment into the binary expression it
// abbreviates, so `x += e` lowers through exactly the same path as `x = x + e`.
//
// The parser records the operator in AssignStmt.Op. Reading it here rather
// than adding a second lowering path is what keeps the two spellings from
// drifting apart: a compound assignment that took its own route could disagree
// with the longhand it stands for, and 1.12.2 would run the wrong arithmetic
// silently. Dropping the operator entirely is what this replaced -- every
// compound assignment compiled to a plain overwrite.
func assignValue(s syntax.AssignStmt) syntax.Expr {
	if s.Op == syntax.ASSIGN {
		return s.Value
	}
	op, ok := compoundOp(s.Op)
	if !ok {
		return s.Value
	}
	return syntax.Binary{Op: op, L: s.Target, R: s.Value, P: s.P}
}

// compoundOp maps `+=` to `+` and friends.
func compoundOp(k syntax.Kind) (syntax.Kind, bool) {
	switch k {
	case syntax.PLUSEQ:
		return syntax.PLUS, true
	case syntax.MINUSEQ:
		return syntax.MINUS, true
	case syntax.STAREQ:
		return syntax.STAR, true
	case syntax.SLASHEQ:
		return syntax.SLASH, true
	case syntax.PERCENTEQ:
		return syntax.PERCENT, true
	}
	return syntax.EOF, false
}

// targetLoc resolves a name that may be bound to an external objective.
func (l *Lowerer) targetLoc(ctx *fnCtx, name string) ir.Loc {
	if xd, ok := l.externs[name]; ok && (xd.Kind == "var" || xd.Kind == "global") {
		return externLoc(xd)
	}
	// A declared objective keeps its own name. Falling through to slot() would
	// allocate l0 for it, and the declaration would then create an objective
	// that nothing writes -- the exact silent-no-op obj exists to prevent.
	if l.objs[name] {
		return loc(name)
	}
	if ctx.isField(name) {
		return loc(ctx.fieldLoc(name))
	}
	return loc(ctx.slot(name))
}

// lowerIf emits a branch, both arms, and a join holding the remainder.
//
// The remainder is lowered EXACTLY ONCE, into the join block. Passing it down
// through nested ifs caused the remainder to be lowered repeatedly (or not at
// all), which silently dropped whole loop bodies.
func (l *Lowerer) lowerIf(ctx *fnCtx, fb *ir.FuncBuilder, cur ir.BlockID, s syntax.IfStmt, rest []syntax.Stmt) ir.BlockID {
	p, err := l.lowerCond(ctx, fb, cur, s.Cond)
	if err != nil {
		l.diags.Error(posOf(s.P), "E_COND", "%s", err)
		return cur
	}

	thenB := fb.NewBlock()
	elseB := fb.NewBlock()
	fb.SetTerm(cur, ir.Branch{Cond: p, Then: thenB, Else: elseB})

	thenEnd := l.lowerSeq(ctx, fb, thenB, s.Then)
	elseEnd := ir.BlockID(noFallThrough)
	if len(s.Else) > 0 {
		elseEnd = l.lowerSeq(ctx, fb, elseB, s.Else)
	} else {
		// No else arm: the else edge falls straight through to the remainder.
		elseEnd = elseB
	}

	if thenEnd == noFallThrough && elseEnd == noFallThrough {
		return noFallThrough
	}

	// A join is needed only when at least two paths reach the remainder.
	var join ir.BlockID = noFallThrough
	needJoin := (thenEnd != noFallThrough) && (elseEnd != noFallThrough)
	if needJoin {
		join = fb.NewBlock()
	}

	target := ir.BlockID(noFallThrough)
	switch {
	case thenEnd != noFallThrough && elseEnd != noFallThrough:
		target = join
		fb.SetTerm(thenEnd, ir.Jump{Target: join})
		fb.SetTerm(elseEnd, ir.Jump{Target: join})
	case thenEnd != noFallThrough:
		target = thenEnd
	default:
		target = elseEnd
	}

	return l.lowerSeq(ctx, fb, target, rest)
}

// lowerWhile emits the canonical loop shape:
//
//	cur:   set budget; jump loop
//	loop:  branch cond -> body, after
//	body:  ...body...; budget -= 1; branch budget >= 1 -> loop, after
//	after: ...rest...
//
// The budget check sits at the TAIL, not folded into the head condition.
// Folding it into the head makes the exit predicate the complement of a
// conjunction, which expands to four overlapping alternatives; keeping it at
// the tail costs nothing and leaves both branch predicates as single ranges.
// lowerWhile lowers a PRE-tested loop by turning it into its tail-tested
// equivalent: if cond { loop cond { body } }.
//
// The two are the same program. The rewrite is worth doing because the
// tail-tested shape keeps the condition in the same block as the body, so
// each iteration costs one self-call instead of a call into a separate
// test block.
func (l *Lowerer) lowerWhile(ctx *fnCtx, fb *ir.FuncBuilder, cur ir.BlockID, s syntax.WhileStmt) ir.BlockID {
	if s.Budget <= 0 {
		l.diags.Error(posOf(s.P), "E_NO_BUDGET", "while needs an iteration bound: while <cond>, budget <n> { ... }")
		return cur
	}
	budget := ctx.slot(fmt.Sprintf("$budget%d", s.P.Line))
	fb.Emit(cur, ir.Set{Dst: loc(budget), Value: s.Budget})

	body := fb.NewBlock()
	after := fb.NewBlock()

	// The entry test, evaluated once before the loop. This is what
	// distinguishes a while loop from a tail-tested one.
	p, err := l.lowerCond(ctx, fb, cur, s.Cond)
	if err != nil {
		l.diags.Error(posOf(s.P), "E_COND", "%s", err)
		return after
	}
	fb.SetTerm(cur, ir.Branch{Cond: p, Then: body, Else: after})

	bodyEnd := l.lowerSeq(ctx, fb, body, s.Body)
	if bodyEnd == noFallThrough {
		return after
	}
	l.closeLoop(ctx, fb, body, bodyEnd, after, budget, s.Cond, s.P)
	return after
}

// lowerLoop lowers a TAIL-tested loop.
//
//	cur:   set budget; jump body
//	body:  ...body...; budget -= 1; Branch(cond AND budget>=1, body, after)
//	after: ...rest...
//
// The body and the test share one block, so the back edge is a self-call
// and an iteration is one file rather than two.
func (l *Lowerer) lowerLoop(ctx *fnCtx, fb *ir.FuncBuilder, cur ir.BlockID, s syntax.LoopStmt) ir.BlockID {
	if s.Budget <= 0 {
		l.diags.Error(posOf(s.P), "E_NO_BUDGET", "loop needs an iteration bound: loop <cond>, budget <n> { ... }")
		return cur
	}
	budget := ctx.slot(fmt.Sprintf("$budget%d", s.P.Line))
	fb.Emit(cur, ir.Set{Dst: loc(budget), Value: s.Budget})

	body := fb.NewBlock()
	after := fb.NewBlock()
	fb.SetTerm(cur, ir.Jump{Target: body})

	bodyEnd := l.lowerSeq(ctx, fb, body, s.Body)
	if bodyEnd == noFallThrough {
		return after
	}
	l.closeLoop(ctx, fb, body, bodyEnd, after, budget, s.Cond, s.P)
	return after
}

// closeLoop emits the back edge shared by both loop forms.
//
// The budget is folded into the continue condition rather than tested in
// a successor block. Keeping it separate costs two guards per iteration,
// not one: the budget test needs a guard for each side, and then the
// condition needs its own. Merging them pays the condition's complement
// once instead of twice.
//
// The complement has the same size either way -- a multi-alternative
// condition already produces a cross product -- so folding is never
// larger, and it always removes one block and one call.
func (l *Lowerer) closeLoop(ctx *fnCtx, fb *ir.FuncBuilder, body, bodyEnd, after ir.BlockID, budget string, cond syntax.Expr, pos syntax.Pos) {
	p, err := l.lowerCond(ctx, fb, bodyEnd, cond)
	if err != nil {
		l.diags.Error(posOf(pos), "E_COND", "%s", err)
		fb.SetTerm(bodyEnd, ir.Jump{Target: after})
		return
	}
	fb.Emit(bodyEnd, ir.Remove{Dst: loc(budget), Value: 1})
	fb.SetTerm(bodyEnd, ir.Branch{Cond: withBudget(p, budget), Then: body, Else: after})
}

func (l *Lowerer) lowerFor(ctx *fnCtx, fb *ir.FuncBuilder, cur ir.BlockID, s syntax.ForStmt) ir.BlockID {
	// A range is a count. It needs no budget clause: the range already says
	// how many iterations there are. The bounds may be variables
	// (`for i in 1..n` is the common case), so they are evaluated, not
	// folded -- but a count is still bounded, because the bound is fixed
	// before the first iteration.
	if b, ok := s.Sel.(syntax.Binary); ok && b.Op == syntax.DOTDOT {
		return l.lowerCountFor(ctx, fb, cur, s, b)
	}
	sel, ok := s.Sel.(syntax.Ident)
	if !ok || !strings.HasPrefix(sel.Name, "@") {
		l.diags.Error(posOf(s.P), "E_FOR_SELECTOR",
			"`for` needs either a range (for i in 0..64) or an entities<@sel> source")
		return cur
	}
	body := fb.NewBlock()
	after := fb.NewBlock()
	fb.Emit(cur, ir.Raw{Text: "execute " + sel.Name + " ~ ~ ~ function " + l.blockPath(fb.Path(), body)})
	fb.SetTerm(cur, ir.Jump{Target: after})
	bodyEnd := l.lowerSeq(ctx, fb, body, s.Body)
	if bodyEnd != noFallThrough {
		fb.SetTerm(bodyEnd, ir.Jump{Target: after})
	}
	return after
}

// lowerCountFor lowers `for i in lo..hi { }`.
//
// The counter is a loop variable in the ordinary sense: it starts at lo, the
// body sees it, and the back edge increments and re-tests. No budget is
// involved because the range already says how many iterations there are --
// which is the whole reason a count and a condition are different statements.
//
// The bound is checked once before the body, so an empty range runs zero times
// rather than once.
func (l *Lowerer) lowerCountFor(ctx *fnCtx, fb *ir.FuncBuilder, cur ir.BlockID, s syntax.ForStmt, r syntax.Binary) ir.BlockID {
	i := ctx.slot(fmt.Sprintf("$for%d", s.P.Line))
	// The bound is evaluated ONCE, before the first iteration. Evaluating it
	// per iteration would let the body change the loop's length, which no
	// other language allows and which makes termination hard to reason about.
	lim := ctx.slot(fmt.Sprintf("$lim%d", s.P.Line))

	// Initial value: the lower bound, which may itself be an expression.
	if lit, ok := constInt(r.L); ok {
		fb.Emit(cur, ir.Set{Dst: loc(i), Value: lit})
	} else {
		src, err := l.expr(ctx, fb, cur, r.L)
		if err != nil {
			l.diags.Error(posOf(s.P), "E_FOR_RANGE", "%s", err)
			return cur
		}
		fb.Emit(cur, ir.Copy{Dst: loc(i), Src: src})
	}
	if lit, ok := constInt(r.R); ok {
		fb.Emit(cur, ir.Set{Dst: loc(lim), Value: lit})
	} else {
		src, err := l.expr(ctx, fb, cur, r.R)
		if err != nil {
			l.diags.Error(posOf(s.P), "E_FOR_RANGE", "%s", err)
			return cur
		}
		fb.Emit(cur, ir.Copy{Dst: loc(lim), Src: src})
	}

	// The variable is an ordinary local, so the body reads it by name.
	ctx.scope[s.Name] = i

	body := fb.NewBlock()
	after := fb.NewBlock()
	// One comparison covers both cases: for an empty range i is already above
	// lim on entry, so the body is skipped rather than run once.
	//
	// The block is a parameter: the head test belongs in the entry block and
	// the back-edge test in the body's exit, and emitting both into one block
	// would put the increment on the wrong side of the test.
	// Both bounds are required. `score_d=0` alone means `d <= 0`, so an
	// empty range -- where d is already negative -- would pass the test and
	// the loop would run until the counter wrapped. That is the mistake this
	// whole project exists to make impossible, so it is spelled out here:
	// `i - lim <= 0` needs min=INT_MIN and max=0.
	test := func(where ir.BlockID) ir.Predicate {
		d := fb.Tmp()
		fb.Emit(where, ir.Copy{Dst: loc(d), Src: loc(i)})
		fb.Emit(where, ir.Arith{Dst: loc(d), Op: "-=", Src: loc(lim)})
		return ir.Predicate{Alts: [][]ir.Cond{{{Obj: d, HasMin: true, Min: -2147483648, HasMax: true, Max: 0}}}}
	}
	fb.SetTerm(cur, ir.Branch{Cond: test(cur), Then: body, Else: after})

	bodyEnd := l.lowerSeq(ctx, fb, body, s.Body)
	if bodyEnd != noFallThrough {
		fb.Emit(bodyEnd, ir.Add{Dst: loc(i), Value: 1})
		fb.SetTerm(bodyEnd, ir.Branch{Cond: test(bodyEnd), Then: body, Else: after})
	}
	return after
}

func (l *Lowerer) lowerBroadcast(ctx *fnCtx, fb *ir.FuncBuilder, cur ir.BlockID, s syntax.BroadcastStmt) {
	sel, ok := s.Sel.(syntax.Ident)
	if !ok || !strings.HasPrefix(sel.Name, "@") {
		l.diags.Error(posOf(s.P), "E_BROADCAST_SELECTOR", "broadcast write needs a selector on the left")
		return
	}
	if lit, ok := constInt(s.Value); ok {
		fb.Emit(cur, ir.Set{Dst: ir.Loc{Holder: sel.Name, Obj: s.Field}, Value: lit})
		return
	}
	op, err := l.expr(ctx, fb, cur, s.Value)
	if err != nil {
		l.diags.Error(posOf(s.P), "E_BROADCAST_VALUE", "%s", err)
		return
	}
	fb.Emit(cur, ir.Copy{Dst: ir.Loc{Holder: sel.Name, Obj: s.Field}, Src: op})
}

func lvalueName(e syntax.Expr) (string, bool) {
	switch x := e.(type) {
	case syntax.Ident:
		return x.Name, true
	case syntax.Field:
		if _, ok := x.X.(syntax.SelfExpr); ok {
			return x.Name, true
		}
	}
	return "", false
}
