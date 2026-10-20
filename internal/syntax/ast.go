package syntax

// Expr is a GGM expression.
type Expr interface{ exprNode() }

type (
	IntLit struct {
		Value int
		P     Pos
	}
	StringLit struct {
		Value string
		P     Pos
	}
	BoolLit struct {
		Value bool
		P     Pos
	}
	Ident struct {
		Name string
		P    Pos
	}
	SelfExpr struct{ P Pos }
	Binary   struct {
		Op   Kind
		L, R Expr
		P    Pos
	}
	Unary struct {
		Op Kind
		X  Expr
		P  Pos
	}
	Call struct {
		Name string
		Args []Expr
		P    Pos
	}
	Field struct {
		X    Expr
		Name string
		P    Pos
	}
	Index struct {
		X   Expr
		Idx Expr
		P   Pos
	}
	ListLit struct {
		Elems []Expr
		P     Pos
	}
)

func (IntLit) exprNode()    {}
func (StringLit) exprNode() {}
func (BoolLit) exprNode()   {}
func (Ident) exprNode()     {}
func (SelfExpr) exprNode()  {}
func (Binary) exprNode()    {}
func (Unary) exprNode()     {}
func (Call) exprNode()      {}
func (Field) exprNode()     {}
func (Index) exprNode()     {}
func (ListLit) exprNode()   {}

// Stmt is a GGM statement.
type Stmt interface{ stmtNode() }

type (
	LetStmt struct {
		Name  string
		Value Expr
		P     Pos
	}
	AssignStmt struct {
		Target Expr
		Op     Kind
		Value  Expr
		P      Pos
	}
	IfStmt struct {
		Cond Expr
		Then []Stmt
		Else []Stmt
		P    Pos
	}
	WhileStmt struct {
		Cond   Expr
		Budget int
		Body   []Stmt
		P      Pos
	}
	ForStmt struct {
		Name  string
		Sel   Expr
		Where Expr
		Body  []Stmt
		P     Pos
	}
	ExprStmt struct {
		X Expr
		P Pos
	}
	ReturnStmt struct {
		Value Expr
		P     Pos
	}
	BroadcastStmt struct {
		Sel   Expr
		Field string
		Value Expr
		P     Pos
	}
	// LoopStmt is a tail-tested loop: the body runs first, then the condition
	// is evaluated. It corresponds exactly to the hand-written idiom
	// `body; function self unless cond`, so it needs no loop rotation.
	LoopStmt struct {
		Cond   Expr
		Budget int
		Body   []Stmt
		P      Pos
	}
)

func (LetStmt) stmtNode()       {}
func (AssignStmt) stmtNode()    {}
func (IfStmt) stmtNode()        {}
func (WhileStmt) stmtNode()     {}
func (ForStmt) stmtNode()       {}
func (ExprStmt) stmtNode()      {}
func (ReturnStmt) stmtNode()    {}
func (BroadcastStmt) stmtNode() {}
func (LoopStmt) stmtNode()      {}

// Param is a function parameter.
type Param struct {
	Name string
	Type string
}

// FuncDecl is a `fn` declaration.
type FuncDecl struct {
	Name     string
	Params   []Param
	RetType  string
	Body     []Stmt
	Wrapping bool
	P        Pos
}

// FieldDecl is a class field: an objective on @s.
type FieldDecl struct {
	Name string
	Type string
	Init Expr
	P    Pos
}

// Method is a class method. Tick marks @tick fan-out.
type Method struct {
	Name   string
	Params []Param
	Body   []Stmt
	Tick   bool
	P      Pos
}

// ClassEvent is an entity lifecycle hook on a class.
type ClassEvent struct {
	// Event is "spawn" or "death".
	Event string
	Body  []Stmt
	P     Pos
}

// ClassDecl is a class: selector predicate + objective namespace + methods.
type ClassDecl struct {
	Name     string
	Selector string
	Fields   []FieldDecl
	Methods  []Method
	Events   []ClassEvent
	P        Pos
}

// EmitStep is one command produced inside a comptime block.
type EmitStep struct {
	Cmd string // rendered command text
	P   Pos
}

// ComptimeBlock holds statements evaluated at compile time.
type ComptimeBlock struct {
	Body []Stmt
	P    Pos
}

// ExternDecl binds an existing objective or function.
type ExternDecl struct {
	Kind      string // "var" | "global" | "fn"
	Name      string
	Type      string
	Objective string
	Holder    string
	Params    []Param
	In        []struct{ Param, Reg string }
	Out       string
	P         Pos
}

// OnDecl is an entry point.
type OnDecl struct {
	Event string // load | tick | every
	Every string // duration text, when Event == "every"
	Body  []Stmt
	P     Pos
}

// File is a parsed source file.
type File struct {
	Namespace string
	Consts    map[string]Expr
	Comptime  []ComptimeBlock
	Externs   []ExternDecl
	Funcs     []FuncDecl
	Classes   []ClassDecl
	Ons       []OnDecl
	P         Pos
}
