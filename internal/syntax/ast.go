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
	// MatchCond is `<container> matches n"..."`: an NBT pattern test.
	//
	// It is a pattern match, not a key-existence test. The data pack's own
	// spelling, repeated 19 times, matches on a value:
	//   scoreboard players tag @s add 残响 {Item:{tag:{dat:{qlt:0b}}}}
	// so qlt:0b must compare equal rather than merely be present.
	MatchCond struct {
		Sel       string
		Container Container
		NBT       string
		P         Pos
	}
	Binary struct {
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
func (MatchCond) exprNode() {}
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
func (DecodeStmt) stmtNode()    {}
func (EncodeStmt) stmtNode()    {}
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

// ObjDecl is an `obj` declaration: an objective the compiler must create.
//
// This is distinct from ExternDecl, which binds an objective that already
// exists. In 1.12.2 writing to an undeclared objective neither errors nor
// takes effect -- the score stays 0 forever -- so "who creates it" has to be
// answerable from the source alone.
type ObjDecl struct {
	Name     string
	Criteria string // empty means dummy
	Display  string // empty means no display name
	P        Pos
}

// FieldDecl2 is an `nbt` field: a bit-packed or enumerated value reachable
// through a p"..." path.
//
// (Named FieldDecl2 because FieldDecl is already the class field.)
type NBTField struct {
	Name string
	Path string // from CheckNBTPath
	Bits int    // 0 when Enum is used
	Enum []EnumCase
	P    Pos
}

// EnumCase is one entry of `enum { "残响":1, ... }` or a range `enum 1..4`.
type EnumCase struct {
	// Key is the NBT text matched for this case. For a numeric range it is the
	// decimal value; for a map it is the literal key text.
	Key string
	// Value is the score assigned when Key matches.
	Value int
}

// DecodeStmt is `decode <objective> from <container>[<field>] [ { cases } ]`.
type DecodeStmt struct {
	Target    string
	Container Container
	Field     string
	Cases     []DecodeCase
	P         Pos
}

// DecodeCase is one author-written `<nbt> add|set <v>` inside a decode block.
type DecodeCase struct {
	NBT string // raw SNBT text
	Op  string // "add" or "set"
	Val int
	P   Pos
}

// EncodeStmt is `encode <container>[<field>] = <objective> over <lo>..<hi>`.
type EncodeStmt struct {
	Container Container
	Field     string
	Source    string
	Lo, Hi    int
	P         Pos
}

// Container names where in the entity's NBT to look.
//
// The spellings are 1.12.2's own key names, not invented ones:
//
//	@s.SelectedItem        {SelectedItem:{tag:{...}}}
//	@s.Inventory(0)        {Inventory:[{Slot:0b,tag:{...}}]}
//	@s.Inventory(100..103) four slots, one reset then four ladders
//	@s.Item                {Item:{tag:{...}}}
type Container struct {
	Key string // SelectedItem | Inventory | Item
	// Lo, Hi are the slot range for Inventory; both 0 for the others.
	Lo, Hi int
	IsSlot bool
	P      Pos
}

// File is a parsed source file.
type File struct {
	Namespace string
	Consts    map[string]Expr
	Comptime  []ComptimeBlock
	Externs   []ExternDecl
	Objs      []ObjDecl
	NBTFields []NBTField
	Funcs     []FuncDecl
	Classes   []ClassDecl
	Ons       []OnDecl
	P         Pos
}
