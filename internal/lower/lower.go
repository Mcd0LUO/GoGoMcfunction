// Package lower turns the typed AST into IR.
//
// This is the middle end. It decides control-flow shape, name binding and how
// a language construct maps onto the IR's primitives — but it never writes
// command text and never consults a version table beyond target.CondFor.
package lower

import (
	"fmt"
	"strings"

	"cmcf/internal/diag"
	"cmcf/internal/ir"
	"cmcf/internal/syntax"
	"cmcf/internal/target"
)

// Result is everything the back end needs.
type Result struct {
	Program *ir.Program
}

// Lowerer holds the state of one module's lowering.
type Lowerer struct {
	tgt     target.Target
	prog    *ir.Program
	diags   *diag.Bag
	sigs    map[string]int
	externs map[string]syntax.ExternDecl
	kt      map[string]int
	clobber map[string]int
	localN  int
	nsRoot  string
	pathPre string
}

// Lower compiles one parsed file.
func Lower(f *syntax.File, tgt target.Target) (*Result, []diag.Diagnostic) {
	bag := &diag.Bag{}
	l := &Lowerer{
		tgt:     tgt,
		diags:   bag,
		sigs:    map[string]int{},
		externs: map[string]syntax.ExternDecl{},
		kt:      map[string]int{},
	}
	l.nsRoot, l.pathPre = f.Namespace, ""
	if i := strings.Index(f.Namespace, ":"); i >= 0 {
		l.nsRoot, l.pathPre = f.Namespace[:i], f.Namespace[i+1:]
	}
	if f.Namespace == "" {
		bag.Error(diag.Pos{}, "E_NO_NAMESPACE", "file has no `namespace` declaration")
	}
	l.prog = ir.NewProgram(f.Namespace)

	for name, expr := range f.Consts {
		if v, ok := constInt(expr); ok {
			l.kt[name] = v
		}
	}
	for _, x := range f.Externs {
		l.externs[x.Name] = x
	}
	for _, fn := range f.Funcs {
		l.sigs[l.path(fn.Name)] = len(fn.Params)
	}
	bodies := map[string][]syntax.Stmt{}
	for _, fn := range f.Funcs {
		bodies[l.path(fn.Name)] = fn.Body
	}
	for _, cd := range f.Classes {
		for _, m := range cd.Methods {
			bodies[l.path(cd.Name+"/"+m.Name)] = m.Body
		}
	}
	l.clobber = l.clobberSummary(bodies)
	for _, fn := range f.Funcs {
		l.lowerFunc(fn)
	}
	for _, od := range f.Ons {
		l.lowerOn(od)
	}
	for _, cd := range f.Classes {
		l.lowerClass(cd)
	}
	return &Result{Program: l.prog}, bag.Sorted()
}

// path renders a function identifier under the current namespace.
func (l *Lowerer) path(name string) string {
	if l.pathPre == "" {
		return l.nsRoot + ":" + name
	}
	return l.nsRoot + ":" + l.pathPre + "/" + name
}

// blockPath renders the name of a generated block function.
//
// Blocks become separate .mcfunction files because 1.12.2 has no intra-file
// labels; a branch is expressed by calling another function.
func (l *Lowerer) blockPath(fn string, id ir.BlockID) string {
	return fmt.Sprintf("%s$b%d", fn, id)
}

// bindParams binds a declaration's parameters to their ABI registers,
// moving any that a nested call would clobber into a local first.
//
// A parameter arrives in an ABI register, and those registers are the
// same objective space as everything else. Any nested call writes
// a0..a(n-1) while marshalling its own arguments, so a parameter that is
// still needed afterwards has to be moved out of the ABI file.
//
// The move happens at entry rather than at the call site because entry is
// the one point where the register is provably still intact and nothing
// has read it yet. After the copy the name is bound to a local for the
// whole body, so no later analysis has to reason about a value's
// lifetime across a call.
//
// This was a silent miscompile. With
//
//	fn chance(scale: int, p: int) -> bool { rand(1, scale) <= p }
//
// marshalling wrote a1 = scale for the call and the comparison then read
// a1 back as p, answering rand <= scale instead of rand <= p.
func (l *Lowerer) bindParams(ctx *fnCtx, fb *ir.FuncBuilder, params []syntax.Param, body []syntax.Stmt) {
	clobbered := l.clobber[ctx.path]
	for i, p := range params {
		reg := fmt.Sprintf("a%d", i)
		if i < clobbered && syntax.Mentions(body, p.Name) {
			local := ctx.newSlot()
			ctx.scope[p.Name] = local
			fb.Emit(0, ir.Copy{Dst: loc(local), Src: loc(reg)})
			continue
		}
		ctx.params = append(ctx.params, reg)
		ctx.scope[p.Name] = reg
	}
	fb.Func().Params = ctx.params
}

// fnCtx carries name binding for one function body.
type fnCtx struct {
	path   string
	params []string
	scope  map[string]string
	// localN points at the module's local counter, not a per-function one.
	//
	// A local is an objective on @s, and @s is a single register file
	// shared by the whole call stack: a callee's l0 IS the caller's l0.
	// Numbering locals per function therefore let any nested call silently
	// overwrite the values its caller was still holding. Numbering them
	// per module makes the names collide only when the allocator proves
	// the live ranges cannot overlap -- the same rule the temporaries
	// already follow.
	localN *int
}

func (c *fnCtx) slot(name string) string {
	if s, ok := c.scope[name]; ok {
		return s
	}
	s := c.newSlot()
	c.scope[name] = s
	return s
}

// newSlot allocates a local objective that no name is bound to yet.
func (c *fnCtx) newSlot() string {
	s := fmt.Sprintf("l%d", *c.localN)
	*c.localN++
	return s
}

func (l *Lowerer) lowerFunc(fn syntax.FuncDecl) {
	ctx := &fnCtx{path: l.path(fn.Name), scope: map[string]string{}, localN: &l.localN}
	fb := l.prog.NewFunc(ctx.path)
	fb.Func().Exported = true
	l.bindParams(ctx, fb, fn.Params, fn.Body)
	end := l.lowerSeq(ctx, fb, 0, fn.Body)
	if end != noFallThrough {
		fb.SetTerm(end, ir.Return{})
	}
}

func (l *Lowerer) lowerOn(od syntax.OnDecl) {
	var name string
	switch od.Event {
	case "load":
		name = l.path("load")
	case "tick":
		name = l.path("tick")
	case "every":
		name = l.path("every/" + od.Every)
	default:
		return
	}
	ctx := &fnCtx{path: name, scope: map[string]string{}, localN: &l.localN}
	fb := l.prog.NewFunc(name)
	fb.Func().Exported = true
	end := l.lowerSeq(ctx, fb, 0, od.Body)
	if end != noFallThrough {
		fb.SetTerm(end, ir.Return{})
	}
}

func (l *Lowerer) lowerClass(cd syntax.ClassDecl) {
	for _, m := range cd.Methods {
		name := l.path(cd.Name + "/" + m.Name)
		ctx := &fnCtx{path: name, scope: map[string]string{}, localN: &l.localN}
		fb := l.prog.NewFunc(name)
		fb.Func().Exported = m.Tick
		l.bindParams(ctx, fb, m.Params, m.Body)
		end := l.lowerSeq(ctx, fb, 0, m.Body)
		if end != noFallThrough {
			fb.SetTerm(end, ir.Return{})
		}
	}
	if len(cd.Fields) > 0 {
		init := l.path(cd.Name + "/init")
		fb := l.prog.NewFunc(init)
		fb.Func().Exported = true
		for _, f := range cd.Fields {
			v, ok := constInt(f.Init)
			if !ok {
				l.diags.Error(posOf(f.P), "E_UNINIT_FIELD", "field %s needs a constant initialiser", f.Name)
				continue
			}
			fb.Emit(0, ir.Set{Dst: ir.Loc{Holder: "@s", Obj: l.fieldObj(cd.Name, f.Name)}, Value: v})
		}
	}
}

func (l *Lowerer) fieldObj(class, field string) string {
	short := class
	if len(short) > 6 {
		short = short[:6]
	}
	f := field
	if len(f) > 8 {
		f = f[:8]
	}
	return l.tgt.FitObjective(short + "." + f)
}
