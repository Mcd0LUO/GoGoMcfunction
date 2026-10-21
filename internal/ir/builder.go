package ir

import "fmt"

// NewProgram starts an empty module.
func NewProgram(namespace string) *Program {
	return &Program{Namespace: namespace}
}

// FuncBuilder builds one function's CFG.
//
// Blocks are created explicitly; the builder never guesses where control flows.
// That is what makes the resulting graph analysable.
type FuncBuilder struct {
	prog *Program
	fn   *Function
}

// NewFunc creates a function and returns a builder for it.
func (p *Program) NewFunc(path string) *FuncBuilder {
	fn := &Function{Path: path}
	p.Funcs = append(p.Funcs, fn)
	b := &FuncBuilder{prog: p, fn: fn}
	b.NewBlock() // entry is always block 0
	return b
}

// Lookup returns an existing function by path.
func (p *Program) Lookup(path string) *Function {
	for _, f := range p.Funcs {
		if f.Path == path {
			return f
		}
	}
	return nil
}

// Func returns the function under construction.
func (b *FuncBuilder) Func() *Function { return b.fn }

// Path returns the function path.
func (b *FuncBuilder) Path() string { return b.fn.Path }

// NewBlock appends an empty block and returns its ID.
func (b *FuncBuilder) NewBlock() BlockID {
	id := BlockID(len(b.fn.Blocks))
	b.fn.Blocks = append(b.fn.Blocks, &Block{ID: id, Term: Return{}})
	return id
}

// Block returns a block by ID.
func (b *FuncBuilder) Block(id BlockID) *Block {
	if int(id) < len(b.fn.Blocks) {
		return b.fn.Blocks[id]
	}
	return nil
}

// Emit appends ops to a block.
func (b *FuncBuilder) Emit(id BlockID, ops ...Op) {
	blk := b.Block(id)
	if blk == nil {
		return
	}
	blk.Ops = append(blk.Ops, ops...)
}

// SetTerm installs the terminator of a block.
func (b *FuncBuilder) SetTerm(id BlockID, t Terminator) {
	if blk := b.Block(id); blk != nil {
		blk.Term = t
	}
}

// Tmp allocates a scratch objective name, unique program-wide.
//
// The name is prefixed so that an author's own objective cannot collide with
// it. It used to be plain `t0`, `t1`, ... and `isTempName` recognised exactly
// that shape -- so `obj t1` was treated as a compiler scratch slot. The
// coalescer then renamed it, and since the author's `t1` and a real temporary
// shared one name, the write disappeared: the function that assigned it was
// dropped entirely at -O2, with no diagnostic.
//
// A leading `#` cannot appear in an author's objective: `#` starts the
// constant pool's fake-player names, and the lexer does not allow it in an
// identifier.
func (b *FuncBuilder) Tmp() string {
	n := b.prog.tmpN
	b.prog.tmpN++
	return fmt.Sprintf("%s%d", TempPrefix, n)
}

// Const returns the fake-player holder for a literal.
func (b *FuncBuilder) Const(v int) Loc {
	return Loc{Holder: ConstName(v), Obj: ConstObjective}
}

// TmpLoc allocates a scratch location in this function's register file.
//
// The holder is the function's, not always @s: a function with no executor
// (on load) keeps its registers on a fake player, because a command naming
// an undefined @s is dropped without an error.
func (b *FuncBuilder) TmpLoc() Loc {
	return Loc{Holder: b.fn.RegisterHolder(), Obj: b.Tmp()}
}

// RegisterHolder is the holder this function's registers live on.
func (f *Function) RegisterHolder() string {
	if f.RegHolder == "" {
		return SelfHolder
	}
	return f.RegHolder
}

// AllBlocks returns the CFG in index order.
func (f *Function) AllBlocks() []*Block { return f.Blocks }

// Ops returns every op in the function, including those in unreachable blocks.
func (f *Function) Ops() []Op {
	var out []Op
	for _, b := range f.Blocks {
		out = append(out, b.Ops...)
	}
	return out
}

// Preds computes predecessor blocks for each block.
func (f *Function) Preds() map[BlockID][]BlockID {
	out := map[BlockID][]BlockID{}
	for _, b := range f.Blocks {
		for _, s := range Succs(b.Term) {
			out[s] = append(out[s], b.ID)
		}
	}
	return out
}
