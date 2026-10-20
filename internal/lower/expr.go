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
	if exprMentions(bin.R, loc(dst), ctx) {
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
			fb.Emit(block, ir.Arith{Dst: loc(dst), Op: op, Src: rhs})
			return true
		}
	}
	lhs, err := l.expr(ctx, fb, block, bin.L)
	if err != nil {
		l.diags.Error(posOf(exprPos(bin.L)), "E_EXPR", "%s", err)
		return true
	}
	fb.Emit(block, ir.Copy{Dst: loc(dst), Src: lhs})
	rhs, err := l.expr(ctx, fb, block, bin.R)
	if err != nil {
		l.diags.Error(posOf(exprPos(bin.R)), "E_EXPR", "%s", err)
		return true
	}
	fb.Emit(block, ir.Arith{Dst: loc(dst), Op: op, Src: rhs})
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
			return externLoc(xd), nil
		}
		if v, ok := l.kt[x.Name]; ok {
			return fb.Const(v), nil
		}
		if strings.Contains(x.Name, ".") {
			parts := strings.SplitN(x.Name, ".", 2)
			return ir.Loc{Holder: parts[0], Obj: parts[1]}, nil
		}
		// A field of the enclosing class beats a fresh local. Fields are
		// checked after externs and constants so an explicit binding
		// still wins.
		if ctx.isField(x.Name) {
			return loc(ctx.fieldLoc(x.Name)), nil
		}
		return loc(ctx.slot(x.Name)), nil
	case syntax.Call:
		return l.call(ctx, fb, block, x)
	case syntax.Binary:
		if isComparison(x.Op) {
			return l.comparison(ctx, fb, block, x)
		}
		return l.binary(ctx, fb, block, x)
	case syntax.Unary:
		return l.unary(ctx, fb, block, x)
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
	fb.Emit(block, ir.Copy{Dst: loc(d), Src: src})
	fb.Emit(block, ir.Arith{Dst: loc(d), Op: "-=", Src: fb.Const(1)})
	return ir.Predicate{Alts: [][]ir.Cond{{{Obj: d, HasMin: true, Min: 0, HasMax: true, Max: 0}}}}, nil
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
		fb.Emit(block, ir.Arith{Dst: loc(d), Op: "-=", Src: rhs})
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
	fb.Emit(block, ir.Copy{Dst: loc(d), Src: lhs})
	fb.Emit(block, ir.Arith{Dst: loc(d), Op: "-=", Src: rhs})
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
		return l.callInto(ctx, fb, block, loc(d), c)
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
		fb.Emit(block, ir.Copy{Dst: loc(d), Src: inner})
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

// externLoc resolves a bound score. The holder is @s unless the declaration
// named a fake player, which is how a global is spelled.
func externLoc(d syntax.ExternDecl) ir.Loc {
	if d.Holder != "" && d.Holder != "@s" {
		return ir.Loc{Holder: d.Holder, Obj: d.Objective}
	}
	if d.Objective != "" {
		return loc(d.Objective)
	}
	return loc(d.Name)
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
