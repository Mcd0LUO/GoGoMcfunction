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
	// FuncIDs maps each reserved function id, lower-cased the way 1.12.2
	// lower-cases it, to a description of what claimed it.
	//
	// The driver generates further functions and checks its names here
	// rather than guessing which of them exist.
	FuncIDs map[string]string
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
	source  string
	nsRoot  string
	pathPre string
	// funcIDs maps the id 1.12.2 will know a function by to what declared it.
	funcIDs map[string]claimed
}

// claimed records where a function id came from, for collision messages.
type claimed struct {
	what      string
	pos       syntax.Pos
	generated bool
}

// Lower compiles one parsed file.
func Lower(f *syntax.File, tgt target.Target, source string) (*Result, []diag.Diagnostic) {
	bag := &diag.Bag{}
	l := &Lowerer{
		tgt:     tgt,
		source:  source,
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
	l.funcIDs = map[string]claimed{}
	l.claimGenerated(f.Namespace+"/init", "the compiler-generated initialiser", f.P)
	l.claimDeclarations(f)

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
	l.reportUncalled(f, bodies)
	for _, fn := range f.Funcs {
		l.lowerFunc(fn)
	}
	for _, od := range f.Ons {
		l.lowerOn(od)
	}
	for _, cd := range f.Classes {
		l.lowerClass(cd)
	}
	ids := make(map[string]string, len(l.funcIDs))
	for k, v := range l.funcIDs {
		ids[k] = v.what
	}
	return &Result{Program: l.prog, FuncIDs: ids}, bag.Sorted()
}

// reportUncalled warns about a declared function that nothing calls.
//
// In 1.12.2 a function with no caller simply never runs, and nothing says
// so: the file loads, the function sits there, and the feature it was
// written for never fires. A reference project's own audit listed eleven
// such functions, two of them byte-identical copies of live ones.
//
// It is a warning rather than an error because a world may legitimately
// reach a function from a command block or from gameLoopFunction, and the
// compiler cannot see either. That is also why the message names the way
// out rather than just asserting the function is dead.
func (l *Lowerer) reportUncalled(f *syntax.File, bodies map[string][]syntax.Stmt) {
	called := map[string]bool{}
	for _, body := range bodies {
		for _, c := range syntax.Calls(body) {
			if _, ok := l.externs[c.Name]; ok {
				continue
			}
			if _, ok := l.sigs[l.path(c.Name)]; ok {
				called[l.path(c.Name)] = true
			}
		}
	}
	for _, fn := range f.Funcs {
		path := l.path(fn.Name)
		if !called[path] {
			l.diags.Warn(posOf(fn.P), "W_UNREACHABLE_FN",
				"nothing in this module calls %s; 1.12.2 will not run it unless a command block or gameLoopFunction does", path)
		}
	}
	for _, cd := range f.Classes {
		for _, m := range cd.Methods {
			if m.Tick {
				continue
			}
			path := l.path(cd.Name + "/" + m.Name)
			if !called[path] {
				l.diags.Warn(posOf(m.P), "W_UNREACHABLE_FN",
					"nothing in this module calls %s; 1.12.2 will not run it unless a command block or gameLoopFunction does", path)
			}
		}
	}
}

// claimDeclarations reserves every function id the file declares.
//
// It runs before anything is lowered so that a collision is reported
// against the declarations rather than discovered as one function quietly
// replacing another.
func (l *Lowerer) claimDeclarations(f *syntax.File) {
	for _, fn := range f.Funcs {
		l.claim(l.path(fn.Name), "function "+fn.Name, fn.P)
	}
	for _, od := range f.Ons {
		if name, ok := l.onName(od); ok {
			l.claim(name, "the "+od.Event+" entry point", od.P)
		}
	}
	for _, cd := range f.Classes {
		for _, m := range cd.Methods {
			l.claim(l.path(cd.Name+"/"+m.Name), "method "+cd.Name+"."+m.Name, m.P)
		}
		if len(cd.Fields) > 0 {
			l.claim(l.path(cd.Name+"/init"), "the field initialiser for class "+cd.Name, cd.P)
		}
	}
}

// onName returns the function an entry point lowers to.
func (l *Lowerer) onName(od syntax.OnDecl) (string, bool) {
	switch od.Event {
	case "load":
		return l.path("load"), true
	case "tick":
		return l.path("tick"), true
	case "every":
		return l.path("every/" + od.Every), true
	}
	return "", false
}

// claim reserves a function id, reporting a collision against whatever
// reserved it first.
//
// 1.12.2 identifies a function with a MinecraftKey, whose constructor
// lower-cases both the namespace and the path. Two names differing only in
// case are therefore ONE function: fn Foo and fn foo compiled to a single
// function whose body was whichever happened to be written last, and every
// call to either went there. On a case-insensitive filesystem they are
// also one file, so the second silently replaced the first on disk too.
//
// A user declaration colliding with a generated one is worse still: fn
// init() overwrote the function that declares the module's objectives and
// constant pool, so the entire program lost its objectives.
func (l *Lowerer) claim(id, what string, pos syntax.Pos) {
	key := strings.ToLower(id)
	if prev, ok := l.funcIDs[key]; ok {
		// A name the author declared landing on a name the compiler
		// emits is a different mistake from two of the author's own
		// names landing on each other, and the fix is different too.
		code := "E_FN_COLLISION"
		if prev.generated {
			code = "E_FN_RESERVED"
		}
		l.diags.Error(posOf(pos), code,
			"%s collides with %s: 1.12.2 lower-cases function ids, so both are %q",
			what, prev.what, key)
		return
	}
	l.funcIDs[key] = claimed{what: what, pos: pos}
}

// claimGenerated reserves an id the compiler emits rather than the author.
func (l *Lowerer) claimGenerated(id, what string, pos syntax.Pos) {
	l.funcIDs[strings.ToLower(id)] = claimed{what: what, pos: pos, generated: true}
}

// origin renders where a declaration was written, for the optional
// comments in the output.
func (l *Lowerer) origin(p syntax.Pos) string {
	if l.source == "" || p.Line == 0 {
		return ""
	}
	// The comment is read, not parsed, so the platform's separator is
	// noise; forward slashes match the function ids on the same line.
	return fmt.Sprintf("%s:%d", strings.ReplaceAll(l.source, "\\", "/"), p.Line)
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
	fb.Func().Origin = l.origin(fn.P)
	l.bindParams(ctx, fb, fn.Params, fn.Body)
	end := l.lowerSeq(ctx, fb, 0, fn.Body)
	if end != noFallThrough {
		fb.SetTerm(end, ir.Return{})
	}
}

func (l *Lowerer) lowerOn(od syntax.OnDecl) {
	name, ok := l.onName(od)
	if !ok {
		return
	}
	ctx := &fnCtx{path: name, scope: map[string]string{}, localN: &l.localN}
	fb := l.prog.NewFunc(name)
	fb.Func().Exported = true
	fb.Func().Origin = l.origin(od.P)
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
		fb.Func().Origin = l.origin(m.P)
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
