package syntax

// Visitor receives the nodes of a file in source order.
//
// A nil callback is skipped; its children are still visited. One traversal
// serves every caller that needs to ask a question about the tree, so a
// new question does not mean a new walk that can disagree with the others.
type Visitor struct {
	// Ident is called for every identifier used as a name.
	Ident func(name string, p Pos)
	// Call is called for every call expression, before its arguments.
	Call func(c Call)
	// Field is called for every member access, before its base.
	//
	// The field's name is not an identifier node, so without this no traversal
	// could see it: `self.hp` reported only the `self`, and an editor asking
	// what `hp` means had nothing to answer with.
	Field func(name string, p Pos, self bool)
	// NBTField is called for the field named in a decode or encode statement.
	//
	// A statement holds names, not nodes, so without this the field in
	// `decode X from @s.Inventory(0)[flags]` was invisible to every traversal.
	NBTField func(name string, p Pos)
	// IntLit is called for every integer literal.
	//
	// A leading minus is part of the literal rather than an operator: the
	// minimum score, -2147483648, is representable while 2147483648 is
	// not. The pair is therefore reported as one signed value, and this
	// callback is not called for the digits alone.
	IntLit func(v int, p Pos)
}

// WalkFile visits every node of a parsed file.
func WalkFile(f *File, v Visitor) {
	for _, expr := range f.Consts {
		Walk(expr, v)
	}
	for _, cb := range f.Comptime {
		WalkStmts(cb.Body, v)
	}
	for _, fn := range f.Funcs {
		WalkStmts(fn.Body, v)
	}
	for _, cl := range f.Classes {
		for _, fld := range cl.Fields {
			if fld.Init != nil {
				Walk(fld.Init, v)
			}
		}
		for _, m := range cl.Methods {
			WalkStmts(m.Body, v)
		}
	}
}

// WalkStmts visits every node of a statement list.
func WalkStmts(list []Stmt, v Visitor) {
	for _, s := range list {
		walkStmt(s, v)
	}
}

func walkStmt(s Stmt, v Visitor) {
	switch x := s.(type) {
	case LetStmt:
		Walk(x.Value, v)
	case AssignStmt:
		Walk(x.Target, v)
		Walk(x.Value, v)
	case IfStmt:
		Walk(x.Cond, v)
		WalkStmts(x.Then, v)
		WalkStmts(x.Else, v)
	case WhileStmt:
		Walk(x.Cond, v)
		WalkStmts(x.Body, v)
	case LoopStmt:
		Walk(x.Cond, v)
		WalkStmts(x.Body, v)
	case ForStmt:
		Walk(x.Sel, v)
		Walk(x.Where, v)
		WalkStmts(x.Body, v)
	case ExprStmt:
		Walk(x.X, v)
	case ReturnStmt:
		if x.Value != nil {
			Walk(x.Value, v)
		}
	case BroadcastStmt:
		Walk(x.Sel, v)
		Walk(x.Value, v)
	case DecodeStmt:
		if v.NBTField != nil && x.Field != "" {
			v.NBTField(x.Field, x.FieldPos)
		}
		// A decode carries no expression, but leaving the case out made the
		// walker incomplete: any future check that walks the tree -- literal
		// range today, something else tomorrow -- silently skips decode
		// statements, and a silently skipped check is worse than no check.
	case EncodeStmt:
		if v.NBTField != nil && x.Field != "" {
			v.NBTField(x.Field, x.FieldPos)
		}
	}
}

// Walk visits an expression and everything under it.
func Walk(e Expr, v Visitor) {
	switch x := e.(type) {
	case IntLit:
		if v.IntLit != nil {
			v.IntLit(x.Value, x.P)
		}
	case StringLit, BoolLit, SelfExpr:
		// no children
	case Ident:
		if v.Ident != nil {
			v.Ident(x.Name, x.P)
		}
	case Unary:
		if x.Op == MINUS {
			if lit, ok := x.X.(IntLit); ok {
				// Report the folded value only. `-2147483648` is a legal int32 and
				// 1.12.2 accepts `scoreboard players set @s X -2147483648` -- the
				// constant pool's `#2147483648` entry is skipped by the runtime,
				// not fatal, and the value still lands.
				//
				// Reporting the MAGNITUDE too looks safer and is not: it rejects a
				// value the target accepts, which is the one thing a range check
				// must never do. The magnitude only reaches a command when the
				// expression is NOT a folded literal -- `y * -2147483648` -- and
				// that path reports through the ordinary IntLit case below.
				if v.IntLit != nil {
					v.IntLit(-lit.Value, x.P)
				}
				return
			}
		}
		Walk(x.X, v)
	case Binary:
		Walk(x.L, v)
		Walk(x.R, v)
	case Call:
		if v.Call != nil {
			v.Call(x)
		}
		for _, a := range x.Args {
			Walk(a, v)
		}
	case Field:
		if v.Field != nil && x.Name != "" {
			_, isSelf := x.X.(SelfExpr)
			v.Field(x.Name, x.P, isSelf)
		}
		Walk(x.X, v)
	case Index:
		Walk(x.X, v)
		Walk(x.Idx, v)
	case ListLit:
		for _, el := range x.Elems {
			Walk(el, v)
		}
	}
}

// EachIntLit visits every integer literal in a file, in source order.
func EachIntLit(f *File, visit func(v int, p Pos)) {
	WalkFile(f, Visitor{IntLit: visit})
}

// Mentions reports whether a statement list names an identifier.
//
// It is deliberately name-based rather than scope-aware: the only caller
// uses it to decide whether a value is worth preserving, and preserving
// one that turns out to be shadowed costs a single command.
func Mentions(list []Stmt, name string) bool {
	found := false
	WalkStmts(list, Visitor{Ident: func(n string, _ Pos) {
		if n == name {
			found = true
		}
	}})
	return found
}

// Calls lists every call expression in a statement list, in source order.
func Calls(list []Stmt) []Call {
	var out []Call
	WalkStmts(list, Visitor{Call: func(c Call) { out = append(out, c) }})
	return out
}
