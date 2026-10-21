// Package lower turns the typed AST into IR.
//
// This is the middle end. It decides control-flow shape, name binding and how
// a language construct maps onto the IR's primitives — but it never writes
// command text and never consults a version table beyond target.CondFor.
package lower

import (
	"fmt"
	"strings"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/diag"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/ir"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/target"
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
	// objs names the objectives declared with `obj`. Assignment to one of
	// these must land on the declared objective rather than a fresh local,
	// or the declaration would create an objective nothing ever writes.
	objs map[string]bool
	// fields are the `nbt` declarations: an NBT path plus its encoding.
	fields map[string]syntax.NBTField
	// probeN numbers the marker tags `matches` uses, so two tests in one
	// function cannot read each other's result.
	probeN int
	// periods are the `on every N` declarations, driven from the tick entry.
	periods []syntax.OnDecl
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
	return LowerWith(f, tgt, source, nil)
}

// LowerWith compiles one file in a module that also contains others.
//
// extra maps the fully qualified function ids declared elsewhere in the module
// to their arity. Without it a call to a library function is indistinguishable
// from a call to a name that does not exist, and 1.12.2 cannot tell the
// difference either: a call to a missing function is valid syntax that simply
// never runs.
func LowerWith(f *syntax.File, tgt target.Target, source string, extra map[string]int) (*Result, []diag.Diagnostic) {
	bag := &diag.Bag{}
	l := &Lowerer{
		tgt:     tgt,
		source:  source,
		diags:   bag,
		sigs:    map[string]int{},
		externs: map[string]syntax.ExternDecl{},
		objs:    map[string]bool{},
		fields:  map[string]syntax.NBTField{},
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
	l.claimExtra(extra)

	// An entry point the module will generate owns its name, so a
	// declaration that normalises onto it is reported rather than quietly
	// sharing a file. Only the ones this file will actually produce are
	// reserved: reserving them all would reject "fn Spawn" in a module
	// that never hooks spawning.
	for _, ev := range l.entryEvents(f) {
		l.claimGenerated(f.Namespace+"/"+ev, "the compiler-generated "+ev+" entry point", f.P)
	}
	l.claimDeclarations(f)
	l.claimClassFields(f)

	for name, expr := range f.Consts {
		if v, ok := constInt(expr); ok {
			l.kt[name] = v
		}
	}
	for _, x := range f.Externs {
		l.externs[x.Name] = x
	}
	// After externs, so the obj/extern conflict check can see both sides.
	l.lowerObjs(f)
	l.lowerFields(f)
	// Signatures from every file in the module. A library is a separate
	// source file whose functions live in another namespace, so its
	// signatures have to be visible before this file's calls are resolved --
	// otherwise a call across the boundary looks like a typo and the error
	// that exists to catch typos fires on correct code.
	for p, n := range extra {
		l.sigs[p] = n
	}
	for _, fn := range f.Funcs {
		l.sigs[l.path(fn.Name)] = len(fn.Params)
	}
	// Class methods are functions too. Leaving them out of the signature
	// table meant a call to one was not arity-checked, was not counted as
	// a caller, and was not seen by the ABI analysis -- so a method could
	// be called with the wrong number of arguments and the call resolved
	// to nothing.
	for _, cd := range f.Classes {
		for _, m := range cd.Methods {
			l.sigs[l.path(cd.Name+"/"+m.Name)] = len(m.Params)
		}
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
	// Collect every period BEFORE lowering any of them. A period's body has to
	// name the periods it drives, and with collection interleaved the first
	// period lowered saw only itself in the list -- so it drove nothing, and
	// the whole chain silently became seven independent counters.
	for _, od := range f.Ons {
		if od.Event == "every" {
			l.periods = append(l.periods, od)
		}
	}
	for _, od := range f.Ons {
		l.lowerOn(od)
	}
	for _, cd := range f.Classes {
		l.lowerClass(cd)
	}
	l.lowerCycleEntry(f.Classes)
	l.lowerSpawnEntry(f.Classes)
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
	for owner, body := range bodies {
		class := l.classOf(owner)
		for _, c := range syntax.Calls(body) {
			if path, ok := l.callTarget(class, c); ok {
				called[path] = true
			}
		}
	}
	for _, fn := range f.Funcs {
		// An exported function is called from another file, which this
		// traversal cannot see; warning about it would make every library
		// noisy enough that the warning stops being read.
		if fn.Export {
			continue
		}
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

// claimClassFields reserves the objective each class field lives in.
//
// A field objective is "<class>.<field>" truncated to the version's
// 16-character limit, so a class name past six characters is cut and two
// long names sharing a prefix land on the same objective: a field of
// Monster and a field of Monstrosity both become "Monste.hp". 1.12.2
// keeps one score for both, and each class silently corrupts the other's
// state.
func (l *Lowerer) claimClassFields(f *syntax.File) {
	owner := map[string]string{}
	for _, cd := range f.Classes {
		for _, fld := range cd.Fields {
			obj := l.fieldObj(cd.Name, fld.Name)
			what := cd.Name + "." + fld.Name
			if prev, ok := owner[obj]; ok {
				l.diags.Error(posOf(fld.P), "E_FIELD_COLLISION",
					"field %s lives in objective %q, which %s already uses: objective names are truncated to %d characters",
					what, obj, prev, l.tgt.ObjectiveNameLimit)
				continue
			}
			owner[obj] = what
		}
	}
}

// entryEvents lists the generated entry points this file needs.
func (l *Lowerer) entryEvents(f *syntax.File) []string {
	// Only the entries that are generated WITHOUT a matching declaration
	// are reserved here. An "on tick" block claims t:k/tick itself, and
	// reserving it twice reported the declaration as colliding with the
	// thing it was declaring.
	declared := map[string]bool{}
	for _, od := range f.Ons {
		declared[od.Event] = true
	}
	need := map[string]bool{}
	// The clock is generated when anything needs to run once a tick:
	// a period, or a class's @tick method. `on load` does not need one --
	// it runs exactly once, from boot, and never from the clock.
	hasClock := false
	for _, od := range f.Ons {
		if od.Event == "every" {
			hasClock = true
		}
	}
	for _, cd := range f.Classes {
		for _, m := range cd.Methods {
			if m.Tick {
				hasClock = true
			}
		}
		for _, ev := range cd.Events {
			need[ev.Event] = true
		}
	}
	if hasClock {
		need["cycle"] = true
		need["on_cycle"] = true
		need["make_clock"] = true
	}
	var out []string
	// Sorted for a stable collision message.
	for _, ev := range []string{"cycle", "make_clock", "on_cycle", "spawn"} {
		if need[ev] && !declared[ev] {
			out = append(out, ev)
		}
	}
	return out
}

// spawnTag is the marker a class puts on an entity whose spawn hook has
// already run.
//
// 1.12.2 has no entity-created event, so "run this once when the entity
// appears" is built the way the hand-written libraries build it: a tag
// recording what has already been handled, and a per-tick pass over the
// entities that do not carry it yet.
func spawnTag(class string) string { return "ggm.spawn." + class }

// withSelectorArg appends a raw argument to a selector's condition list.
func withSelectorArg(sel, arg string) string {
	if i := strings.LastIndex(sel, "]"); i >= 0 {
		return sel[:i] + "," + arg + sel[i:]
	}
	return sel + "[" + arg + "]"
}

// lowerSpawnEntry builds the namespace spawn entry, if there is one.
func (l *Lowerer) lowerSpawnEntry(classes []syntax.ClassDecl) {
	type hook struct{ selector, path string }
	var hooks []hook
	for _, cd := range classes {
		for _, ev := range cd.Events {
			if ev.Event != "spawn" {
				continue
			}
			hooks = append(hooks, hook{
				selector: withSelectorArg(cd.Selector, "tag=!"+spawnTag(cd.Name)),
				path:     l.path(cd.Name + "/spawn"),
			})
		}
	}
	if len(hooks) == 0 {
		return
	}
	fb := l.prog.NewFunc(l.path("spawn"))
	fb.Func().Exported = true
	for _, h := range hooks {
		fb.Emit(0, ir.FanOut{Selector: h.selector, Path: h.path})
	}
}

// lowerClassEvent builds one lifecycle hook body.
//
// The marker tag is set BEFORE the user body runs. An early return in the
// body would otherwise skip it, and the entity would be spawned again on
// the next tick, forever.
func (l *Lowerer) lowerClassEvent(cd syntax.ClassDecl, ev syntax.ClassEvent, fields map[string]string) {
	name := l.path(cd.Name + "/" + ev.Event)
	ctx := &fnCtx{path: name, scope: map[string]string{}, localN: &l.localN, class: cd.Name, fields: fields}
	fb := l.prog.NewFunc(name)
	fb.Func().Origin = l.origin(ev.P)
	// Fields first: the body may read them, and an entity that reaches the
	// body with uninitialised fields is worse than one that never spawned.
	if len(cd.Fields) > 0 {
		fb.Emit(0, ir.Call{Path: l.path(cd.Name + "/init")})
	}
	fb.Emit(0, ir.Raw{Text: "scoreboard players tag @s add " + spawnTag(cd.Name)})
	end := l.lowerSeq(ctx, fb, 0, ev.Body)
	if end != noFallThrough {
		fb.SetTerm(end, ir.Return{})
	}
}

// clockTag marks the entity that carries the module's clock.
//
// 1.12.2's gameLoopFunction runs a function with NO executor: @s is
// undefined there, and every command that names it is silently dropped.
// The hand-written data packs work around that the same way this does --
// summon one marker entity, then run the tick body inside an execute that
// binds @s to it.
const clockTag = "ggm.clock"

// lowerCycleEntry builds the three functions that make up the module's
// clock: cycle, make_clock and on_cycle.
//
//   - cycle is the only thing gameLoopFunction should name. It has no
//     executor, so it does nothing but ensure the clock exists and hand
//     off.
//   - make_clock summons the marker entity unless one already exists.
//   - on_cycle runs INSIDE `execute @e[tag=ggm.clock]`, so @s there is the
//     clock entity and every @s write lands somewhere.
//
// Splitting them is not cosmetic: putting the body directly in cycle would
// put it back in the no-executor context that made `on tick` broken.
func (l *Lowerer) lowerCycleEntry(classes []syntax.ClassDecl) {
	type ticker struct{ path, selector string }
	var tickers []ticker
	for _, cd := range classes {
		for _, m := range cd.Methods {
			if m.Tick {
				tickers = append(tickers, ticker{l.path(cd.Name + "/" + m.Name), cd.Selector})
			}
		}
	}
	periods := l.periods
	if len(tickers) == 0 && len(periods) == 0 {
		return
	}

	// make_clock
	mk := l.prog.NewFunc(l.path("make_clock"))
	mk.Func().Exported = true
	mk.Emit(0, ir.Raw{Text: "summon armor_stand ~ ~ ~ {Tags:[\"" + clockTag +
		"\"],Marker:1b,Invisible:1b,NoGravity:1b,Invulnerable:1b,PersistenceRequired:1b}"})

	// on_cycle: the body that actually has @s.
	name := l.path("on_cycle")
	ctx := &fnCtx{path: name, scope: map[string]string{}, localN: &l.localN}
	fb := l.prog.NewFunc(name)
	fb.Func().Exported = true
	end := ir.BlockID(0)
	for _, t := range tickers {
		fb.Emit(end, ir.FanOut{Selector: t.selector, Path: t.path})
	}
	_ = ctx
	if len(periods) > 0 {
		fastest := periods[0]
		for _, od := range periods {
			if od.EveryTicks < fastest.EveryTicks {
				fastest = od
			}
		}
		fb.Emit(end, ir.Add{Dst: ir.Loc{Holder: "@s", Obj: periodCounter(fastest)}, Value: 1})
		fb.Emit(end, ir.Raw{Text: fmt.Sprintf(
			"function %s if @s[score_%s_min=%d,score_%s=%d]",
			l.path("every/"+fastest.Every), periodCounter(fastest),
			fastest.EveryTicks, periodCounter(fastest), fastest.EveryTicks)})
	}
	fb.SetTerm(end, ir.Return{})

	// cycle: the entry gameLoopFunction names. No executor here.
	cyc := l.prog.NewFunc(l.path("cycle"))
	cyc.Func().Exported = true
	cyc.Emit(0, ir.Raw{Text: fmt.Sprintf("function %s unless @e[tag=%s]",
		l.path("make_clock"), clockTag)})
	cyc.Emit(0, ir.Raw{Text: fmt.Sprintf("execute @e[tag=%s,c=1] ~ ~ ~ function %s",
		clockTag, l.path("on_cycle"))})
}

// lowerTickEntry is gone. It built the entry gameLoopFunction named, which
// had no executor -- see lowerCycleEntry.

// downstream lists the periods this one drives.
//
// A period drives another when it divides it evenly and no CLOSER period also
// divides it -- otherwise 0.1s would drive 1s directly and the 0.5s step in
// between would advance its counter twice per second.
//
// The result is a chain, not a fan-out: 0.1s drives 0.2/0.3/0.5s, 0.5s drives
// 1s, 1s drives 3s and 5s. Eight edges instead of twenty-one, and each period
// counts only to the ratio it needs.
func (l *Lowerer) downstream(od syntax.OnDecl) []syntax.OnDecl {
	var out []syntax.OnDecl
	// Only periods have a duration. `on load` and `on tick` reach this with
	// EveryTicks == 0, and dividing by it panics at compile time.
	if od.Event != "every" || od.EveryTicks <= 0 {
		return nil
	}
	for _, cand := range l.periods {
		if cand.EveryTicks <= od.EveryTicks || cand.EveryTicks%od.EveryTicks != 0 {
			continue
		}
		closer := false
		for _, mid := range l.periods {
			if mid.EveryTicks > od.EveryTicks && mid.EveryTicks < cand.EveryTicks &&
				cand.EveryTicks%mid.EveryTicks == 0 {
				closer = true
				break
			}
		}
		if !closer {
			out = append(out, cand)
		}
	}
	return out
}

// periodCounter names the objective a period's tick counter lives on.
//
// Short and fixed: `p.<ticks>` stays inside the 16-character objective limit
// for any duration a program would write (`p.1200` is 6 characters), whereas
// `p.<duration text>` could outgrow it -- and an over-long objective name is
// truncated by 1.12.2, so two periods would silently share one counter.
func periodCounter(od syntax.OnDecl) string {
	// The tick count is already on the declaration; parsing the duration text
	// again here was a second implementation of the same rule, and the two
	// diverged the moment one of them learned a new unit: `5s` became p.100
	// while `100ms` also became p.100, so two periods shared one counter.
	return fmt.Sprintf("p.%d", od.EveryTicks)
}

// classOf returns the class a method belongs to, or "" for a plain
// function. Method paths are "<ns>/<Class>/<Name>", so the class is the
// second-to-last segment.
func (l *Lowerer) classOf(path string) string {
	body := path
	if i := strings.Index(body, ":"); i >= 0 {
		body = body[i+1:]
	}
	parts := strings.Split(body, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2]
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
//
// Two `on load` blocks in one file would both claim <ns>/load and the second
// would replace the first on disk, so the collision check still runs -- but
// two `on every 5s` blocks are the same function for the same reason, and
// the diagnostic names whichever was written first.
func (l *Lowerer) onName(od syntax.OnDecl) (string, bool) {
	switch od.Event {
	case "load":
		return l.path("load"), true
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

// claimExtra registers function ids declared by other files in the module.
//
// They are claimed as generated=false and owned by another file, so a
// collision between this file's declarations and a library's is reported here
// rather than discovered as one file overwriting the other on disk.
func (l *Lowerer) claimExtra(extra map[string]int) {
	for p := range extra {
		id := strings.ToLower(p)
		if _, taken := l.funcIDs[id]; taken {
			continue
		}
		l.funcIDs[id] = claimed{what: "a function declared in another file of this module", generated: true}
	}
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
			fb.Emit(0, ir.Copy{Dst: ctx.loc(local), Src: ctx.loc(reg)})
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
	// class is the class whose method body is being lowered, and fields
	// are its declared field names.
	//
	// A field is an objective on @s, and a bare name inside a method has
	// to mean the field rather than a fresh local. Without this, the
	// specification's own example
	//
	//	var hp: int = 40
	//	fn deal(dmg: int) { hp -= dmg }
	//
	// read and wrote an uninitialised local: the objective was set once by
	// the field initialiser and never touched again.
	class string
	// fields maps a field's name to the objective it lives in. The two
	// differ because objective names are truncated to the version's limit.
	fields map[string]string
	// noSelf marks a body entered without an executor: `on load`, and the
	// boot function that calls it.
	//
	// Registers still work here -- they move to a fake player -- but a class
	// field, a selector fan-out or a decode from @s do not, because there is
	// no entity for them to name. Reporting that is the whole point: 1.12.2
	// drops such a command silently.
	noSelf bool
}

// selfHolder is the holder this body's registers live on.
func (c *fnCtx) selfHolder() string {
	if c.noSelf {
		return ir.NoSelfHolder
	}
	return ir.SelfHolder
}

// isField reports whether a bare name refers to a field of the enclosing
// class.
func (c *fnCtx) isField(name string) bool {
	_, ok := c.fields[name]
	return c.class != "" && ok
}

// fieldLoc returns the objective a field lives in.
func (c *fnCtx) fieldLoc(name string) string { return c.fields[name] }

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

// lowerObjs validates `obj` declarations and hands them to the IR.
//
// Three failures are worth catching here rather than at runtime, because all
// three are silent in 1.12.2:
//
//   - a name over the objective limit is truncated, so two names differing
//     only in their tail collide and share one objective;
//   - declaring the same objective twice reads as "updating" it, but the
//     second `objectives add` fails and changes nothing;
//   - an objective both declared and bound with `extern var` means the author
//     believes two different things about who creates it.
func (l *Lowerer) lowerObjs(f *syntax.File) {
	seen := map[string]diag.Pos{}
	for _, od := range f.Objs {
		pos := posOf(od.P)
		if prev, dup := seen[od.Name]; dup {
			l.diags.Error(pos, "E_OBJECTIVE_DUP",
				"objective %q is declared twice (first at %s)", od.Name, prev)
			continue
		}
		seen[od.Name] = pos
		if n := l.tgt.ObjectiveNameLimit; n > 0 && len(od.Name) > n {
			l.diags.Error(pos, "E_OBJECTIVE_NAME",
				"objective %q is %d characters; %s truncates at %d, so it collides with any name sharing that prefix",
				od.Name, len(od.Name), l.tgt.Name, n)
		}
		if xd, ok := l.externs[od.Name]; ok {
			l.diags.Error(pos, "E_OBJECTIVE_CONFLICT",
				"objective %q is both declared with obj and bound with extern %s; only one of them can create it",
				od.Name, xd.Kind)
		}
		l.objs[od.Name] = true
		l.prog.Objs = append(l.prog.Objs, ir.ObjDecl{Name: od.Name, Criteria: od.Criteria, Display: od.Display})
	}
}

// lowerFields registers `nbt` declarations.
//
// A bit width above 30 would make the top rung's value 2^31, which is outside
// int32: the emitted `add` would carry a constant that wraps to a negative
// number, so the field would read back wrong with no error anywhere.
func (l *Lowerer) lowerFields(f *syntax.File) {
	for _, fd := range f.NBTFields {
		pos := posOf(fd.P)
		if _, dup := l.fields[fd.Name]; dup {
			l.diags.Error(pos, "E_FIELD_DUP", "nbt field %q is declared twice", fd.Name)
			continue
		}
		switch {
		case fd.Bits > 0 && len(fd.Enum) > 0:
			l.diags.Error(pos, "E_FIELD_BITS", "field %q is both bits and enum", fd.Name)
			continue
		case fd.Bits <= 0 && len(fd.Enum) == 0:
			l.diags.Error(pos, "E_FIELD_BITS", "field %q has no encoding", fd.Name)
			continue
		case fd.Bits > 30:
			l.diags.Error(pos, "E_FIELD_BITS",
				"field %q is %d bits; the top rung would be 2^%d, which is outside the int32 a score holds",
				fd.Name, fd.Bits, fd.Bits-1)
			continue
		}
		for _, ec := range fd.Enum {
			if !l.tgt.FitsScore(ec.Value) {
				l.diags.Error(pos, "E_INT_RANGE",
					"enum value %d for %q is outside the %s score range", ec.Value, fd.Name, l.tgt.Name)
			}
		}
		l.fields[fd.Name] = fd
	}
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
	// `on load` runs from boot, with NO executor: 1.12.2 has no load event,
	// so the user wires a command block, and a command block has no @s.
	// Every `on every` period instead runs inside `execute @e[tag=ggm.clock]`,
	// so @s there is the clock entity and ordinary code works.
	//
	// This is the difference that matters, and it is why `on tick` is gone:
	// it looked like the second kind and behaved like the first.
	noSelf := od.Event == "load"
	ctx := &fnCtx{path: name, scope: map[string]string{}, localN: &l.localN, noSelf: noSelf}
	fb := l.prog.NewFunc(name)
	fb.Func().Exported = true
	fb.Func().NoSelf = noSelf
	if noSelf {
		fb.Func().RegHolder = ir.NoSelfHolder
	}
	fb.Func().Origin = l.origin(od.P)
	// Reset FIRST, unconditionally.
	//
	// The period's entry runs exactly when its counter reaches the bound, so
	// zeroing here is the reset. Putting it at the end instead meant an early
	// `return` in the body would skip it -- and `lowerSeq` reports an early
	// return as noFallThrough, so the reset would silently vanish for exactly
	// those periods whose body returns.
	if od.Event == "every" {
		fb.Emit(0, ir.Set{Dst: ir.Loc{Holder: ir.SelfHolder, Obj: periodCounter(od)}, Value: 0})
	}
	end := l.lowerSeq(ctx, fb, 0, od.Body)
	if end == noFallThrough {
		// The body already terminated (an early return), so there is no block
		// to append the downstream drive to. The chain edge is lost for that
		// period -- reported rather than silently dropped.
		if len(l.downstream(od)) > 0 {
			l.diags.Warn(posOf(od.P), "W_UNREACHABLE_FN",
				"the body of on every %s always returns, so the periods it should drive will never advance",
				od.Every)
		}
		return
	}
	// Drive this period's downstream: the periods it divides and no nearer
	// period already divides. The counter advances once per firing of THIS
	// period, so the bound is the RATIO, not the absolute period -- 0.5s
	// driving 1s counts to 2, which is what the reference pack writes.
	for _, down := range l.downstream(od) {
		obj := periodCounter(down)
		ratio := down.EveryTicks / od.EveryTicks
		fb.Emit(end, ir.Add{Dst: ir.Loc{Holder: ir.SelfHolder, Obj: obj}, Value: 1})
		fb.Emit(end, ir.Raw{Text: fmt.Sprintf(
			"function %s if @s[score_%s_min=%d,score_%s=%d]",
			l.path("every/"+down.Every), obj, ratio, obj, ratio)})
	}
	fb.SetTerm(end, ir.Return{})
}

func (l *Lowerer) lowerClass(cd syntax.ClassDecl) {
	fields := map[string]string{}
	for _, f := range cd.Fields {
		fields[f.Name] = l.fieldObj(cd.Name, f.Name)
	}
	for _, m := range cd.Methods {
		name := l.path(cd.Name + "/" + m.Name)
		ctx := &fnCtx{path: name, scope: map[string]string{}, localN: &l.localN, class: cd.Name, fields: fields}
		fb := l.prog.NewFunc(name)
		fb.Func().Exported = m.Tick
		fb.Func().Origin = l.origin(m.P)
		l.bindParams(ctx, fb, m.Params, m.Body)
		end := l.lowerSeq(ctx, fb, 0, m.Body)
		if end != noFallThrough {
			fb.SetTerm(end, ir.Return{})
		}
	}
	for _, ev := range cd.Events {
		if ev.Event != "spawn" {
			// An empty body is a placeholder and there is nothing to
			// run, so it is accepted. A non-empty one cannot be honoured
			// correctly: 1.12.2 cannot observe an entity that is already
			// gone, so the hook would need a shadow entity to hang the
			// state on, and pretending otherwise would produce a handler
			// that never fires.
			if len(ev.Body) > 0 {
				l.diags.Error(posOf(ev.P), "E_HOOK_UNSUPPORTED",
					"on %s needs a shadow entity: 1.12.2 cannot observe an entity that is already gone, so the hook has no correct implementation yet", ev.Event)
			}
			continue
		}
		l.lowerClassEvent(cd, ev, fields)
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
