package syntax

// EachIntLit visits every integer literal in a file, in source order.
//
// A leading minus is part of the literal rather than an operator: the
// minimum score, -2147483648, is representable while 2147483648 is not.
// Reporting the digits before the sign is applied would therefore reject
// the most common boundary value, so the pair is reported as one signed
// number.
func EachIntLit(f *File, visit func(v int, p Pos)) {
	var w intWalker
	w.visit = visit
	for _, expr := range f.Consts {
		w.expr(expr)
	}
	for _, cb := range f.Comptime {
		w.stmts(cb.Body)
	}
	for _, fn := range f.Funcs {
		w.stmts(fn.Body)
	}
	for _, cl := range f.Classes {
		for _, fld := range cl.Fields {
			if fld.Init != nil {
				w.expr(fld.Init)
			}
		}
		for _, m := range cl.Methods {
			w.stmts(m.Body)
		}
	}
	for _, on := range f.Ons {
		w.stmts(on.Body)
	}
}

type intWalker struct {
	visit func(v int, p Pos)
}

func (w *intWalker) stmts(list []Stmt) {
	for _, s := range list {
		w.stmt(s)
	}
}

func (w *intWalker) stmt(s Stmt) {
	switch x := s.(type) {
	case LetStmt:
		w.expr(x.Value)
	case AssignStmt:
		w.expr(x.Target)
		w.expr(x.Value)
	case IfStmt:
		w.expr(x.Cond)
		w.stmts(x.Then)
		w.stmts(x.Else)
	case WhileStmt:
		w.expr(x.Cond)
		w.stmts(x.Body)
	case LoopStmt:
		w.expr(x.Cond)
		w.stmts(x.Body)
	case ForStmt:
		w.expr(x.Sel)
		w.expr(x.Where)
		w.stmts(x.Body)
	case ExprStmt:
		w.expr(x.X)
	case ReturnStmt:
		if x.Value != nil {
			w.expr(x.Value)
		}
	case BroadcastStmt:
		w.expr(x.Sel)
		w.expr(x.Value)
	}
}

func (w *intWalker) expr(e Expr) {
	switch x := e.(type) {
	case IntLit:
		w.visit(x.Value, x.P)
	case StringLit, BoolLit, Ident, SelfExpr:
		// no literals
	case Unary:
		if x.Op == MINUS {
			if lit, ok := x.X.(IntLit); ok {
				w.visit(-lit.Value, x.P)
				return
			}
		}
		w.expr(x.X)
	case Binary:
		w.expr(x.L)
		w.expr(x.R)
	case Call:
		for _, a := range x.Args {
			w.expr(a)
		}
	case Field:
		w.expr(x.X)
	case Index:
		w.expr(x.X)
		w.expr(x.Idx)
	case ListLit:
		for _, el := range x.Elems {
			w.expr(el)
		}
	}
}
