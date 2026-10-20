// Package comptime evaluates compile-time blocks.
//
// Comptime runs in the compiler's own arithmetic (Go int64), so a constant
// pool that would be 128 hand-written lines costs nothing at runtime.
package comptime

import (
	"fmt"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/diag"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/ir"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
)

// Emitted is one command produced at compile time.
//
// Two shapes, because a compile-time generator has two jobs:
//
//   - a constant score (`emit("#seed", "math", 12345)`), which is what the
//     constant pool is built from;
//   - an arbitrary command (`emit_line("tp @s ~" + d + " ~ ~")`), which is what
//     makes a loop over bit widths possible. Without it a library like the
//     coordinate probe has to spell out every rung by hand, and a generator
//     that can only emit one command kind is not a generator.
type Emitted struct {
	// Raw is the whole command line when non-empty; the other fields are then
	// unused.
	Raw string
	// Holder, Objective and Value describe a constant score.
	Holder    string
	Objective string
	Value     int
	// P is the position of the emit call, so an out-of-range value can be
	// reported where the author wrote it rather than at the command.
	P diag.Pos
}

// Eval runs all comptime blocks and returns the commands they produced.
func Eval(blocks []syntax.ComptimeBlock, bag *diag.Bag) []Emitted {
	var out []Emitted
	for _, cb := range blocks {
		for _, st := range cb.Body {
			out = append(out, evalStmt(st, bag)...)
		}
	}
	return out
}

func evalStmt(st syntax.Stmt, bag *diag.Bag) []Emitted {
	switch s := st.(type) {
	case syntax.ExprStmt:
		// emit_line takes one string and emits it verbatim.
		//
		// This is what makes a table generatable: 88 `tp` rungs whose offsets
		// are decimal text cannot come out of `emit`, which only writes a
		// constant score. Hand-expanding them is not a language feature, it is
		// the absence of one.
		if call, ok := s.X.(syntax.Call); ok && call.Name == "emit_line" {
			text, ok := Str(call.Args[0])
			if !ok || len(call.Args) != 1 {
				bag.Error(diag.Pos{Line: call.P.Line, Col: call.P.Col}, "E_EMIT_CONST",
					"emit_line takes one compile-time string")
				return nil
			}
			return []Emitted{{Raw: text, P: diag.Pos{Line: call.P.Line, Col: call.P.Col}}}
		}
		if call, ok := s.X.(syntax.Call); ok && call.Name == "emit" {
			if e, ok := evalEmit(call); ok {
				e.P = diag.Pos{Line: call.P.Line, Col: call.P.Col}
				return []Emitted{e}
			}
			bag.Error(diag.Pos{Line: call.P.Line, Col: call.P.Col}, "E_EMIT_CONST",
				"emit arguments must be compile-time constants")
		}
	case syntax.ForStmt:
		// Two forms: a constant range (`0..10`) or an explicit list (`[1, 2, 4]`).
		// The list form is what keeps the emitted constant pool to exactly the
		// literals the program uses.
		if list, ok := s.Sel.(syntax.ListLit); ok {
			var out []Emitted
			for _, item := range list.Elems {
				v, ok := Int(item)
				if !ok {
					bag.Error(diag.Pos{Line: s.P.Line, Col: s.P.Col}, "E_COMPTIME_LIST",
						"comptime list elements must be constants")
					return nil
				}
				for _, inner := range s.Body {
					out = append(out, evalStmt(Subst(inner, s.Name, v), bag)...)
				}
			}
			return out
		}
		lo, hi, ok := Range(s.Sel)
		if !ok {
			bag.Error(diag.Pos{Line: s.P.Line, Col: s.P.Col}, "E_COMPTIME_RANGE",
				"comptime for needs a constant range like `0..10` or a list")
			return nil
		}
		var out []Emitted
		for i := lo; i <= hi; i++ {
			for _, inner := range s.Body {
				out = append(out, evalStmt(Subst(inner, s.Name, i), bag)...)
			}
		}
		return out
	}
	// Anything else is rejected rather than ignored.
	//
	// `if` inside comptime used to fall through this switch and vanish: the
	// body simply did not run, no diagnostic was produced, and the generated
	// table came out short by however many rows the branch guarded. A silent
	// no-op in a generator is worse than a missing feature -- the output looks
	// complete.
	bag.Error(diag.Pos{Line: stmtPos(st).Line, Col: stmtPos(st).Col}, "E_COMPTIME_STMT",
		"comptime supports only `for`, `emit` and `emit_line`; this statement would be ignored")
	return nil
}

// stmtPos digs the source position out of a statement for a diagnostic.
func stmtPos(st syntax.Stmt) syntax.Pos {
	switch s := st.(type) {
	case syntax.IfStmt:
		return s.P
	case syntax.LetStmt:
		return s.P
	case syntax.ExprStmt:
		return s.P
	case syntax.ForStmt:
		return s.P
	case syntax.AssignStmt:
		return s.P
	}
	return syntax.Pos{}
}

func evalEmit(call syntax.Call) (Emitted, bool) {
	if len(call.Args) < 3 {
		return Emitted{}, false
	}
	holder, ok1 := Str(call.Args[0])
	obj, ok2 := Str(call.Args[1])
	val, ok3 := Int(call.Args[2])
	if !ok1 || !ok2 || !ok3 {
		return Emitted{}, false
	}
	return Emitted{Holder: holder, Objective: obj, Value: val}, true
}

// Range extracts constant bounds from a `lo..hi` expression.
func Range(e syntax.Expr) (int, int, bool) {
	b, ok := e.(syntax.Binary)
	if !ok || b.Op != syntax.DOTDOT {
		return 0, 0, false
	}
	lo, ok1 := Int(b.L)
	hi, ok2 := Int(b.R)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return lo, hi, true
}

// Int folds an expression to a constant integer.
func Int(e syntax.Expr) (int, bool) {
	switch x := e.(type) {
	case syntax.IntLit:
		return x.Value, true
	case syntax.Unary:
		if x.Op == syntax.MINUS {
			if v, ok := Int(x.X); ok {
				return -v, true
			}
		}
	case syntax.Binary:
		l, ok1 := Int(x.L)
		r, ok2 := Int(x.R)
		if !ok1 || !ok2 {
			return 0, false
		}
		switch x.Op {
		case syntax.PLUS:
			return l + r, true
		case syntax.MINUS:
			return l - r, true
		case syntax.STAR:
			return l * r, true
		case syntax.SLASH:
			if r == 0 {
				return 0, false
			}
			return l / r, true
		case syntax.PERCENT:
			if r == 0 {
				return 0, false
			}
			return l % r, true
		}
	}
	return 0, false
}

// fmtDec renders n/scale as a decimal literal with exactly enough fractional
// digits to name the scale: fmtDec(2147483648, 1000) is "2147483.648".
//
// Hand-rolled rather than fmt.Sprintf("%f") because the width is a property of
// the scale, not of the value: 1000 always means three digits, including for
// 0.001 and 0.010.
func fmtDec(n, scale int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	whole, frac := n/scale, n%scale
	width := 0
	for s := scale; s > 1; s /= 10 {
		width++
	}
	s := fmt.Sprintf("%d.%0*d", whole, width, frac)
	if neg {
		return "-" + s
	}
	return s
}

// Str folds an expression to a constant string, including the `str(int)`
// builtin and `+` concatenation.
func Str(e syntax.Expr) (string, bool) {
	switch x := e.(type) {
	case syntax.StringLit:
		return x.Value, true
	case syntax.Call:
		// strdec(n, scale) renders n thousandths as a decimal literal:
		// strdec(2147483648, 1000) is "2147483.648".
		//
		// strdec2(bit, scale) is the same for 2^bit, which is what a bit-width
		// table needs. A generator that cannot raise two to a loop variable
		// forces the author to write the table out by hand, and a hand-written
		// table of 88 rungs is not a generated one.
		// strpow2(bit) renders 2^bit as an integer literal. A bit-weight
		// table needs both forms: the offset is 2^bit / 1000 (strdec2) and the
		// weight added back is 2^bit itself (strpow2).
		if x.Name == "strpow2" && len(x.Args) == 1 {
			bit, ok := Int(x.Args[0])
			if !ok || bit < 0 || bit > 30 {
				return "", false
			}
			return fmt.Sprint(int64(1) << uint(bit)), true
		}
		if x.Name == "strdec2" && len(x.Args) == 2 {
			bit, ok1 := Int(x.Args[0])
			scale, ok2 := Int(x.Args[1])
			if !ok1 || !ok2 || scale == 0 || bit < 0 || bit > 62 {
				return "", false
			}
			return fmtDec(int64(1)<<uint(bit), int64(scale)), true
		}
		if x.Name == "strdec" && len(x.Args) == 2 {
			n, ok1 := Int(x.Args[0])
			scale, ok2 := Int(x.Args[1])
			if !ok1 || !ok2 || scale == 0 {
				return "", false
			}
			return fmtDec(int64(n), int64(scale)), true
		}
		if x.Name == "str" && len(x.Args) == 1 {
			if n, ok := Int(x.Args[0]); ok {
				return fmt.Sprint(n), true
			}
		}
	case syntax.Binary:
		if x.Op != syntax.PLUS {
			return "", false
		}
		l, ok1 := Str(x.L)
		r, ok2 := Str(x.R)
		if ok1 && ok2 {
			return l + r, true
		}
		if ok1 {
			if n, ok := Int(x.R); ok {
				return l + fmt.Sprint(n), true
			}
		}
	}
	return "", false
}

// Subst replaces a loop variable with a constant inside a statement.
func Subst(st syntax.Stmt, name string, v int) syntax.Stmt {
	switch s := st.(type) {
	case syntax.ExprStmt:
		return syntax.ExprStmt{X: SubstExpr(s.X, name, v), P: s.P}
	case syntax.LetStmt:
		return syntax.LetStmt{Name: s.Name, Value: SubstExpr(s.Value, name, v), P: s.P}
	}
	return st
}

// SubstExpr replaces a free identifier with a constant.
func SubstExpr(e syntax.Expr, name string, v int) syntax.Expr {
	switch x := e.(type) {
	case syntax.Ident:
		if x.Name == name {
			return syntax.IntLit{Value: v, P: x.P}
		}
		return x
	case syntax.Binary:
		return syntax.Binary{Op: x.Op, L: SubstExpr(x.L, name, v), R: SubstExpr(x.R, name, v), P: x.P}
	case syntax.Call:
		args := make([]syntax.Expr, len(x.Args))
		for i, a := range x.Args {
			args[i] = SubstExpr(a, name, v)
		}
		return syntax.Call{Name: x.Name, Args: args, P: x.P}
	case syntax.Unary:
		return syntax.Unary{Op: x.Op, X: SubstExpr(x.X, name, v), P: x.P}
	case syntax.ListLit:
		elems := make([]syntax.Expr, len(x.Elems))
		for i, e := range x.Elems {
			elems[i] = SubstExpr(e, name, v)
		}
		return syntax.ListLit{Elems: elems, P: x.P}
	}
	return e
}

// ToIR converts comptime output into IR ops appended to a pool function.
func ToIR(items []Emitted) []ir.Op {
	var out []ir.Op
	for _, e := range items {
		if e.Raw != "" {
			out = append(out, ir.Raw{Text: e.Raw})
			continue
		}
		out = append(out, ir.Set{Dst: ir.Loc{Holder: e.Holder, Obj: e.Objective}, Value: e.Value})
	}
	return out
}
