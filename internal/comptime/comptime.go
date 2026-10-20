// Package comptime evaluates compile-time blocks.
//
// Comptime runs in the compiler's own arithmetic (Go int64), so a constant
// pool that would be 128 hand-written lines costs nothing at runtime.
package comptime

import (
	"fmt"

	"github.com/Mcd0LUO/CelesteaMcfunction/internal/diag"
	"github.com/Mcd0LUO/CelesteaMcfunction/internal/ir"
	"github.com/Mcd0LUO/CelesteaMcfunction/internal/syntax"
)

// Emitted is one constant command produced at compile time.
type Emitted struct {
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
	return nil
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

// Str folds an expression to a constant string, including the `str(int)`
// builtin and `+` concatenation.
func Str(e syntax.Expr) (string, bool) {
	switch x := e.(type) {
	case syntax.StringLit:
		return x.Value, true
	case syntax.Call:
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
		out = append(out, ir.Set{Dst: ir.Loc{Holder: e.Holder, Obj: e.Objective}, Value: e.Value})
	}
	return out
}
