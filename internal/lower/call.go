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

// clobberSummary computes, for every function in the module, how many
// ABI registers a call to it disturbs.
//
// A call writes a0..a(n-1) to pass n arguments, and the callee's own body
// may write a0..a(m-1) when it calls something in turn. A caller holding
// a value in one of those registers across the call loses it, so this has
// to be the transitive maximum rather than the argument count.
//
// The case that exposed it: lcg_next takes no arguments, so its call
// sites looked harmless, but its body passes one argument to lcg_step and
// so writes a0. luo:math/rand kept lo in a0 across that call and read the
// step's argument back instead of lo.
//
// The assignment is monotone over a finite lattice, so iterating until
// nothing changes terminates and handles recursion without a special case.
func (l *Lowerer) clobberSummary(bodies map[string][]syntax.Stmt) map[string]int {
	clobber := map[string]int{}
	for path := range bodies {
		clobber[path] = 0
	}
	for changed := true; changed; {
		changed = false
		for path, body := range bodies {
			want := 0
			for _, c := range syntax.Calls(body) {
				if n := l.regsTouched(c, clobber); n > want {
					want = n
				}
			}
			if want > clobber[path] {
				clobber[path] = want
				changed = true
			}
		}
	}
	return clobber
}

// regsTouched reports how many ABI registers a single call disturbs.
func (l *Lowerer) regsTouched(c syntax.Call, clobber map[string]int) int {
	if xd, ok := l.externs[c.Name]; ok {
		worst := len(c.Args)
		for _, in := range xd.In {
			if n, ok := abiIndex(in.Reg); ok && n+1 > worst {
				worst = n + 1
			}
		}
		return worst
	}
	path := l.path(c.Name)
	worst := l.sigs[path]
	if n, ok := clobber[path]; ok && n > worst {
		worst = n
	}
	return worst
}

// abiIndex reads the register number out of an ABI register name.
func abiIndex(reg string) (int, bool) {
	if len(reg) < 2 || reg[0] != 'a' {
		return 0, false
	}
	n := 0
	for _, ch := range reg[1:] {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		n = n*10 + int(ch-'0')
	}
	return n, true
}

// callInto stores a call's result directly into dst, and reports
// whether it handled the call.
//
// It declines builtins and reduce, which produce their value in a slot of
// their own; those keep the general path.
func (l *Lowerer) callInto(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, dst ir.Loc, c syntax.Call) bool {
	if xd, ok := l.externs[c.Name]; ok {
		l.marshalExtern(ctx, fb, block, c, xd)
		fb.Emit(block, ir.Call{Path: c.Name})
		out := xd.Out
		if out == "" {
			out = "return"
		}
		fb.Emit(block, ir.Copy{Dst: dst, Src: loc(out)})
		return true
	}
	path := l.path(c.Name)
	if _, ok := l.sigs[path]; !ok {
		return false
	}
	l.marshalArgs(ctx, fb, block, path, c)
	fb.Emit(block, ir.Call{Path: path})
	fb.Emit(block, ir.Copy{Dst: dst, Src: loc("return")})
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
