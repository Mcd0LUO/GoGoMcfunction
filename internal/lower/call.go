package lower

import (
	"fmt"
	"strings"

	"cmcf/internal/ir"
	"cmcf/internal/syntax"
)

func (l *Lowerer) lowerCallStmt(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, s syntax.ExprStmt) {
	call, ok := s.X.(syntax.Call)
	if !ok {
		l.diags.Error(posOf(s.P), "E_STMT", "expression statement must be a call")
		return
	}
	switch call.Name {
	case "tag_add":
		fb.Emit(block, ir.Raw{Text: "tag @s add " + strArg(call.Args, 1)})
		return
	case "tag_remove":
		fb.Emit(block, ir.Raw{Text: "tag @s remove " + strArg(call.Args, 1)})
		return
	case "kill":
		fb.Emit(block, ir.Raw{Text: "kill @s"})
		return
	case "say":
		fb.Emit(block, ir.Raw{Text: "say " + strArg(call.Args, 0)})
		return
	}
	if xd, ok := l.externs[call.Name]; ok {
		l.marshalExtern(ctx, fb, block, call, xd)
		fb.Emit(block, ir.Call{Path: call.Name})
		return
	}
	path := l.path(call.Name)
	l.marshalArgs(ctx, fb, block, path, call)
	fb.Emit(block, ir.Call{Path: path})
}

// call lowers a call used as a value.
func (l *Lowerer) call(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, c syntax.Call) (ir.Loc, error) {
	if c.Name == "reduce<min>" || c.Name == "reduce<max>" {
		return l.reduce(ctx, fb, block, c)
	}
	if xd, ok := l.externs[c.Name]; ok {
		return l.callExtern(ctx, fb, block, c, xd)
	}
	if r, ok := l.builtin(ctx, fb, block, c); ok {
		return r, nil
	}
	path := l.path(c.Name)
	if n, ok := l.sigs[path]; ok {
		if len(c.Args) != n {
			return ir.Loc{}, fmt.Errorf("%s takes %d argument(s), got %d", c.Name, n, len(c.Args))
		}
		l.marshalArgs(ctx, fb, block, path, c)
		fb.Emit(block, ir.Call{Path: path})
		t := fb.TmpLoc()
		fb.Emit(block, ir.Copy{Dst: t, Src: loc("return")})
		return t, nil
	}
	return ir.Loc{}, fmt.Errorf("call to %s is not usable as a value", c.Name)
}

// marshalArgs moves arguments into the callee's ABI registers.
// 1.12.2 has no call stack, so parameters are entity-scoped objectives.
func (l *Lowerer) marshalArgs(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, target string, c syntax.Call) {
	n, known := l.sigs[target]
	if !known {
		return
	}
	if len(c.Args) != n {
		l.diags.Error(posOf(c.P), "E_ARITY", "%s takes %d argument(s), got %d", target, n, len(c.Args))
		return
	}
	// Parameters live in the same register file as everything else, so writing
	// a0 while a later argument still needs to read a0 would corrupt it. This is
	// the classic parallel-move problem: evaluate every argument into a scratch
	// slot first, then commit the registers.
	//
	// Example that motivated this: `rand(1, scale)` inside a function whose
	// first parameter is scale (also a0) emitted `a0 = 1; a1 = a0`, silently
	// passing 1 as the range instead of scale.
	operands := make([]ir.Loc, len(c.Args))
	spilled := make([]bool, len(c.Args))
	for i, a := range c.Args {
		op, err := l.expr(ctx, fb, block, a)
		if err != nil {
			l.diags.Error(posOf(c.P), "E_ARG", "%s", err)
			return
		}
		operands[i] = op
		if needsSpill(op, n) {
			t := fb.TmpLoc()
			fb.Emit(block, ir.Copy{Dst: t, Src: op})
			operands[i] = t
			spilled[i] = true
		}
	}
	for i := range c.Args {
		dst := loc(fmt.Sprintf("a%d", i))
		if spilled[i] || operands[i] != dst {
			fb.Emit(block, ir.Copy{Dst: dst, Src: operands[i]})
		}
	}
}

// needsSpill reports whether an operand must be copied aside before registers
// are committed. Anything already living in an ABI register is at risk, because
// committing an earlier argument overwrites it.
func needsSpill(op ir.Loc, paramCount int) bool {
	if op.Holder != "@s" {
		return false
	}
	if len(op.Obj) < 2 || op.Obj[0] != 'a' {
		return false
	}
	for _, ch := range op.Obj[1:] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func (l *Lowerer) marshalExtern(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, c syntax.Call, x syntax.ExternDecl) {
	for i, a := range c.Args {
		if i >= len(x.In) {
			break
		}
		src, err := l.expr(ctx, fb, block, a)
		if err != nil {
			l.diags.Error(posOf(c.P), "E_ARG", "%s", err)
			return
		}
		fb.Emit(block, ir.Copy{Dst: loc(x.In[i].Reg), Src: src})
	}
}

func (l *Lowerer) callExtern(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, c syntax.Call, x syntax.ExternDecl) (ir.Loc, error) {
	l.marshalExtern(ctx, fb, block, c, x)
	fb.Emit(block, ir.Call{Path: c.Name})
	out := x.Out
	if out == "" {
		out = "return"
	}
	t := fb.TmpLoc()
	fb.Emit(block, ir.Copy{Dst: t, Src: loc(out)})
	return t, nil
}

func (l *Lowerer) reduce(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, c syntax.Call) (ir.Loc, error) {
	if len(c.Args) != 2 {
		return ir.Loc{}, fmt.Errorf("reduce needs (selector, objective)")
	}
	sel, ok := c.Args[0].(syntax.Ident)
	if !ok || !strings.HasPrefix(sel.Name, "@") {
		return ir.Loc{}, fmt.Errorf("reduce first argument must be a selector")
	}
	obj, ok := c.Args[1].(syntax.Ident)
	if !ok {
		return ir.Loc{}, fmt.Errorf("reduce second argument must be an objective name")
	}
	t := fb.TmpLoc()
	seed := 2147483647
	op := "<"
	if strings.Contains(c.Name, "max") {
		op = ">"
		seed = -2147483647
	}
	fb.Emit(block, ir.Copy{Dst: t, Src: fb.Const(seed)})
	fb.Emit(block, ir.Arith{Dst: t, Op: op, Src: ir.Loc{Holder: sel.Name, Obj: obj.Name}})
	return t, nil
}

// builtin lowers the fixed standard library. Each entry must map onto a bounded
// number of scoreboard operations, and must not require control flow that the
// current block cannot express.
func (l *Lowerer) builtin(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, c syntax.Call) (ir.Loc, bool) {
	arg := func(i int) (ir.Loc, bool) {
		if i >= len(c.Args) {
			return ir.Loc{}, false
		}
		o, err := l.expr(ctx, fb, block, c.Args[i])
		if err != nil {
			l.diags.Error(posOf(c.P), "E_EXPR", "%s", err)
			return ir.Loc{}, false
		}
		return o, true
	}
	switch c.Name {
	case "min", "max":
		a, ok1 := arg(0)
		b, ok2 := arg(1)
		if !ok1 || !ok2 {
			return ir.Loc{}, true
		}
		t := fb.TmpLoc()
		op := "<"
		if c.Name == "max" {
			op = ">"
		}
		fb.Emit(block, ir.Copy{Dst: t, Src: a})
		fb.Emit(block, ir.Arith{Dst: t, Op: op, Src: b})
		return t, true
	case "rem_euclid":
		a, ok1 := arg(0)
		m, ok2 := arg(1)
		if !ok1 || !ok2 {
			return ir.Loc{}, true
		}
		t, u := fb.TmpLoc(), fb.TmpLoc()
		fb.Emit(block, ir.Copy{Dst: t, Src: a})
		fb.Emit(block, ir.Arith{Dst: t, Op: "%=", Src: m})
		fb.Emit(block, ir.Copy{Dst: u, Src: t})
		fb.Emit(block, ir.Arith{Dst: u, Op: "+=", Src: m})
		fb.Emit(block, ir.Arith{Dst: u, Op: "%=", Src: m})
		return u, true
	case "str":
		// compile-time only; reaching here means a runtime use
		l.diags.Error(posOf(c.P), "E_STR_RUNTIME", "str() is compile-time only")
		return ir.Loc{}, true
	}
	return ir.Loc{}, false
}

func strArg(args []syntax.Expr, i int) string {
	if i < len(args) {
		if s, ok := args[i].(syntax.StringLit); ok {
			return s.Value
		}
	}
	return ""
}
