// Package ir is the middle end: a machine-independent control-flow graph.
//
// Invariants that the rest of the compiler relies on:
//
//  1. A Block contains only straight-line ops; every branch is a Terminator.
//  2. Every Loc has a non-empty Holder and Obj.
//  3. Every Predicate alternative has at least one Cond, and every Cond names
//     its objective.
//  4. Blocks[0] is the entry block; no other block may be unreachable after
//     the reachability pass.
//
// See docs/ARCHITECTURE.md for why the IR is a CFG rather than a nested tree.
package ir

// Program is a compiled module.
type Program struct {
	Namespace string
	Funcs     []*Function
	// Consts are the integer literals the program needs, collected from the
	// final IR so that optimised-away constants are not emitted.
	Consts []int
	// Objs are objectives the author declared with `obj`, in source order.
	//
	// Declaring one is not what makes it exist: the backend already emits a
	// declaration for every objective it sees written. What this adds is the
	// criteria and display name, plus the guarantee that the objective was
	// named on purpose rather than by a typo.
	Objs []ObjDecl
	// tmpN backs program-wide temporary numbering. It is a deliberate
	// over-approximation: @s is shared by every function, so a callee reusing
	// the caller's slot would corrupt live state. Register allocation replaces
	// this later.
	tmpN int
}

// ObjDecl is one `obj` declaration.
type ObjDecl struct {
	Name     string
	Criteria string
	Display  string
}

// Function is a control-flow graph plus its calling convention.
type Function struct {
	// Path is the fully qualified identifier, e.g. "luo:math/sqrt".
	Path string
	// Params are the incoming ABI register names, in order.
	Params []string
	// Blocks is the CFG. Blocks[0] is the entry.
	Blocks []*Block
	// Exported marks an entry point that must survive pruning.
	Exported bool
	// RegHolder is the score holder this function's registers live on.
	//
	// It is "@s" for anything reached from an entity -- a normal function, a
	// class method, a fan-out body, an `on every` period. It is a fake player
	// for an entry with no executor (`on load`, boot), because @s is undefined
	// there and 1.12.2 DISCARDS a command that names it: the arithmetic
	// computes nothing and nothing reports it.
	RegHolder string
	// NoSelf marks a function entered WITHOUT an executor. Its own registers
	// are fine (they are on a fake player), but anything that genuinely needs
	// an entity -- a class field, a selector fan-out, a decode from @s -- has
	// no meaning here and must be reported rather than emitted.
	NoSelf bool
	// Origin is where the declaration was written, as "file:line", or ""
	// for a function the compiler generated.
	//
	// It exists so the output can name the line to go and read. The text
	// is carried as a string rather than a position type because the IR
	// has no business knowing about source files, and three position
	// types in one program is two too many.
	Origin string
}

// BlockID identifies a basic block within its function.
type BlockID int

// Block is a straight-line op sequence with exactly one terminator.
type Block struct {
	ID   BlockID
	Ops  []Op
	Term Terminator
}

// Terminator ends a block. Exactly one is executed.
type Terminator interface{ termNode() }

// Jump transfers control unconditionally.
type Jump struct{ Target BlockID }

// Branch transfers control on a predicate.
type Branch struct {
	Cond Predicate
	Then BlockID
	Else BlockID
}

// TailCall calls another function and then returns from this one.
//
// 1.12.2 has no call/return pair, so "call and return" is the only sound
// composition: the callee finishes and control comes back here to fall off the
// end of this function.
type TailCall struct{ Path string }

// Return ends the function.
type Return struct{}

func (Jump) termNode()     {}
func (Branch) termNode()   {}
func (TailCall) termNode() {}
func (Return) termNode()   {}

// Succs lists the successors of a terminator.
func Succs(t Terminator) []BlockID {
	switch x := t.(type) {
	case Jump:
		return []BlockID{x.Target}
	case Branch:
		return []BlockID{x.Then, x.Else}
	}
	return nil
}

// Loc is a scoreboard location: a holder plus an objective.
type Loc struct {
	Holder string
	Obj    string
}

// Valid reports whether the location is well formed. The compiler asserts this
// in tests because an empty objective renders as `score_=0`, which is valid
// syntax testing a DIFFERENT objective — a silent miscompile.
func (l Loc) Valid() bool { return l.Holder != "" && l.Obj != "" }

// Op is one primitive operation. Every op maps to a bounded number of commands.
type Op interface{ opNode() }

// Set assigns an integer literal.
type Set struct {
	Dst   Loc
	Value int
}

// Copy assigns one score to another (`operation ... =`).
type Copy struct {
	Dst Loc
	Src Loc
}

// Arith applies a compound operator to Dst using Src.
// Op is one of += -= *= /= %= and the min/max operators < > <= >=.
type Arith struct {
	Dst Loc
	Op  string
	Src Loc
}

// Add adds a literal to a location.
type Add struct {
	Dst   Loc
	Value int
}

// Remove subtracts a literal from a location.
type Remove struct {
	Dst   Loc
	Value int
}

// Guarded executes an op only when a predicate holds.
//
// It is how a straight-line block expresses a conditional, and it is what makes
// block inlining possible: a leaf block reached by a branch can be folded into
// its predecessor instead of costing a function call. A function call in 1.12.2
// is a real cost, so this matters.
//
// Semantics note: the inner op's destination is BOTH read and written, because
// on the path where the guard fails the previous value survives.
type Guarded struct {
	Inner Op
	Cond  Predicate
}

// Call invokes another function without returning to this block.
//
// Ret is the slot the callee leaves its result in, or the zero Loc when the
// caller ignores the result. It is recorded HERE rather than inferred by the
// analysis because only the lowering knows the holder: a function with no
// executor keeps its registers on a fake player, and guessing `@s` there names
// a score that does not exist.
//
// Liveness needs it. Without it the `copy dst = ret` that follows a call looked
// like it read a slot nothing defines, and the register allocator was free to
// give the return slot to a temporary still live across the call.
type Call struct {
	Path string
	Ret  Loc
}

// FanOut runs a function once for every entity a selector matches.
//
// It is how a class's tick entry reaches its instances: @s inside the
// callee is each matched entity in turn, at that entity's position. It is
// modelled separately from Raw so that the analysed call graph includes
// the edge -- a Raw line would make the callee look unreachable.
type FanOut struct {
	// Selector is the raw selector text, e.g. "@e[tag=mob]".
	Selector string
	// Path is the function to run per matched entity.
	Path string
}

// Raw is an escape hatch for commands the IR does not model.
//
// It carries its effects explicitly, because analysis cannot read them out of
// the text: a `Raw` that writes an objective is invisible to liveness, and an
// invisible write lets the allocator reuse that slot for something else.
//
// That is not hypothetical. A block probe writes its flag with
//
//	execute @s ~ ~ ~ detect … scoreboard players set @s t0 1
//
// and with no declared write the temp looked dead, so the next probe was
// coloured onto the SAME objective -- the second probe then overwrote the
// first one's answer and the first branch read the wrong flag. At -O0 the
// output was correct, which is the worst shape for a bug to have.
//
// Declaring the effects rather than parsing the text keeps the dependency the
// right way round: the code that builds the command already knows what it
// touches, and a string parser would have to understand every command.
type Raw struct {
	Text string
	// Writes and Reads are the locations the command touches, if any.
	Writes []Loc
	Reads  []Loc
	// Args are locations spliced into Text at each %s, in order.
	//
	// A location must never be written into Text directly. Optimization
	// renames temporaries, and a name baked into a string cannot be renamed
	// with it: the op's Writes would say t0 while the command still said t1,
	// and the two would drift apart silently. Splicing at render time is the
	// only way a Raw can name a slot and survive a rename.
	Args []Loc
}

func (Set) opNode()     {}
func (Copy) opNode()    {}
func (Arith) opNode()   {}
func (Add) opNode()     {}
func (Remove) opNode()  {}
func (Guarded) opNode() {}
func (Call) opNode()    {}
func (FanOut) opNode()  {}
func (Raw) opNode()     {}

// Cond is a scoreboard interval test.
//
// 1.12.2 semantics: `score_x=N` means x <= N and `score_x_min=N` means x >= N,
// so equality needs BOTH bounds and a two-sided interval is a closed range.
type Cond struct {
	Obj    string
	HasMin bool
	Min    int
	HasMax bool
	Max    int
}

// Predicate is a condition in disjunctive normal form.
//
// 1.12.2 selectors express conjunction (comma-separated score conditions) but
// have no boolean OR, so a disjunction is rendered as several parallel guarded
// calls. That is why this type exists instead of a single Cond.
type Predicate struct {
	Alts  [][]Cond
	Exist *ExistTest
	Block *BlockTest
}

// ExistTest is `function F if <selector>` — an O(1) existence test rather than
// a fan-out count. It is how `any<sel>` lowers.
type ExistTest struct {
	Selector string
	Negate   bool
}

// BlockTest is `execute <sel> ~ ~ ~ detect <x> <y> <z> <block> <data>`.
//
// 1.12.2 has no boolean expression in a command, so a block probe can only
// exist as a guarded command. Lowering it as a Predicate keeps it usable in
// `if` and in `while` without giving the language a second kind of condition.
//
// Block and Data are raw command text: a block id, and a data value or `*`.
// Both are literals because a command is text.
type BlockTest struct {
	Selector string
	X, Y, Z  string
	Block    string
	Data     string
}

// ClonePredicate returns a predicate that shares no memory with p.
//
// The Alts slice is the only part that is not a value. A predicate is copied
// by assignment everywhere else, so two ops can end up pointing at one array
// without anything looking wrong -- and then a pass that rewrites a condition
// in place rewrites it for both. That produced a guard testing one register
// twice where the author wrote a range.
func ClonePredicate(p Predicate) Predicate {
	if len(p.Alts) == 0 {
		return p
	}
	alts := make([][]Cond, len(p.Alts))
	for i := range p.Alts {
		alts[i] = append([]Cond(nil), p.Alts[i]...)
	}
	p.Alts = alts
	return p
}

// IsAlways reports whether the predicate imposes no condition.
func (p Predicate) IsAlways() bool {
	return p.Exist == nil && p.Block == nil && len(p.Alts) == 0
}

// SelfHolder is the executor selector, the default register file.
const SelfHolder = "@s"

// NoSelfHolder is the fake player a function without an executor uses for
// its registers. A fake player is a legal score holder in 1.12.2, so the
// same scoreboard arithmetic works -- it just does not belong to an entity.
const NoSelfHolder = "#ggm.reg"

// ConstObjective is the objective every literal lives on.
const ConstObjective = "ggm.c"

// TempPrefix starts every compiler-allocated scratch objective: `t0.ggm`,
// `t1.ggm`.
//
// An author's identifier cannot contain a dot at the end like this, so no
// declaration can collide with a temporary. Plain `t0` could: an author's
// `obj t1` was taken for a scratch slot, renamed by the coalescer, and the
// write vanished with no diagnostic.
//
// The shape is `t<n>.ggm` rather than `#t<n>` because an objective name also
// appears inside a SELECTOR: `execute @s[score_t0.ggm_min=1]`. A leading `#`
// is legal in 1.12.2 and is what the constant pool's fake players use, but
// GoMcRuntime's selector parser rejects it -- `Entity
// '@s[score_#t0_min=1]' cannot be found` -- so every guarded command silently
// failed to run. Since a guarded command is how `if` is emitted, that broke
// `if` entirely on the runtime this project verifies against.
const TempPrefix = "t"

// TempSuffix ends every compiler-allocated scratch objective.
const TempSuffix = ".ggm"

// ConstName is the canonical fake-player name for a literal.
// Negative values use an 'n' instead of '-' for portability across versions.
func ConstName(v int) string {
	if v < 0 {
		return "#n" + itoa(-v)
	}
	return "#" + itoa(v)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
