// Package lower turns the typed AST into IR.
//
// This is the middle end. It decides control-flow shape, name binding and how
// a language construct maps onto the IR's primitives — but it never writes
// command text and never consults a version table beyond target.CondFor.
package lower

import (
	"fmt"
	"strings"
	"unicode/utf8"

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
	// Periods are the bodies that run on a timer, collected per file and
	// merged by the driver.
	//
	// They are collected rather than lowered because a period is a
	// MODULE-wide fact: `[every("5s")]` in two files, or as `5s` in one and
	// `100ticks` in the other, is one period with one counter. Generating a
	// dispatcher per file would give the same period two counters that drift
	// apart, and the drift is invisible -- both would look correct alone.
	Periods []PeriodEntry
	// Loads are the function bodies to call once on world load.
	Loads []string
	// Spawns are the class hooks to run for entities that have not been
	// initialised yet.
	Spawns []SpawnEntry
}

// PeriodEntry is one body to run every Ticks game ticks.
type PeriodEntry struct {
	Ticks int
	Path  string
	// Selector is empty when the body runs once, on the clock entity, and
	// names a set of entities when it runs once per match. The difference is
	// one `execute` in the dispatcher -- which is exactly what makes a
	// class's period and a free function's period the same feature.
	Selector string
	P        syntax.Pos
}

// SpawnEntry is a class's on-spawn hook.
type SpawnEntry struct {
	Selector string
	Path     string
	P        syntax.Pos
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
	// holders names the score holders the module has introduced, by the
	// externs that bind them. A dotted name starting with one of these is
	// unambiguously the holder.objective shorthand, so the compiler has no
	// business warning that the author might have meant a declared
	// objective instead.
	holders map[string]bool
	// scope is the module-wide view of the declarations. It is kept whole
	// because the report of an ambiguous dotted name has to be ONCE for the
	// module, not once per use.
	scope Scope
	// bodyN numbers the functions that `for e in entities<...>` bodies become.
	// It must be unique across the module: a repeated name silently replaces
	// an earlier body, and the loops then share one.
	bodyN int
	// probeN numbers the marker tags `matches` uses, so two tests in one
	// function cannot read each other's result.
	probeN int
	// periods and loads and spawns are collected, not lowered: they become
	// module-wide entries that the driver merges across files.
	periods []PeriodEntry
	loads   []string
	spawns  []SpawnEntry
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
//
// The file is its own module: it sees its own declarations and nothing else.
func Lower(f *syntax.File, tgt target.Target, source string) (*Result, []diag.Diagnostic) {
	return LowerWith(f, tgt, source, nil, NewScope())
}

// LowerWith compiles one file in a module that also contains others.
//
// extra maps the fully qualified function ids declared elsewhere in the module
// to their arity. Without it a call to a library function is indistinguishable
// from a call to a name that does not exist, and 1.12.2 cannot tell the
// difference either: a call to a missing function is valid syntax that simply
// never runs.
//
// scope carries the module's obj, extern and nbt declarations, so a name
// declared in one file means the same thing in every other. It is
// MODULE-wide on purpose: those three tables say what the module uses, and
// leaving them per-file made the meaning of a dotted name depend on which
// file the author happened to be in.
func LowerWith(f *syntax.File, tgt target.Target, source string, extra map[string]int, scope Scope) (*Result, []diag.Diagnostic) {
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
		holders: scope.holders(),
		scope:   scope,
	}
	l.nsRoot, l.pathPre = f.Namespace, ""
	if i := strings.Index(f.Namespace, ":"); i >= 0 {
		l.nsRoot, l.pathPre = f.Namespace[:i], f.Namespace[i+1:]
	} else if f.Namespace != "" {
		// A namespace must be `domain:path`. Without the colon every generated
		// id loses its domain -- `big/boot` instead of `big:boot` -- and
		// 1.12.2 requires one.
		//
		// The old path accepted it and then failed in the CLI's own writer,
		// which is the worst of both: `-check` reported success, and `-out`
		// exited with "internal: malformed function path", an error that
		// blames the compiler for the author's mistake. It also meant `boot`
		// could never resolve `init`, so no objective was declared and every
		// scoreboard write was silently discarded.
		// The file name goes IN the message, as E_PARSE does it.
		//
		// diag.Pos carries only line and column, so a diagnostic raised with
		// the zero Pos prints as `-:` -- and this one is about the whole
		// file, so it has no line to name. Without the name the author cannot
		// tell which of several files is missing its namespace.
		bag.Error(diag.Pos{}, "E_NO_NAMESPACE",
			"%s: namespace %q has no domain: write `namespace <domain>:%s`, because 1.12.2 function ids are always domain-qualified",
			source, f.Namespace, f.Namespace)
	}
	if f.Namespace == "" {
		bag.Error(diag.Pos{}, "E_NO_NAMESPACE", "%s: file has no `namespace` declaration", source)
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
	// The three declaration tables are MODULE-wide.
	//
	// They were per-file, and that made the meaning of a name depend on
	// which file the author happened to be in: an objective declared in one
	// file was a plain local in another, and a dotted name written in the
	// second file silently became a fake player's score instead. The
	// specification says the module is one namespace -- boot collects every
	// file's load bodies and constant pool -- and these three tables say
	// what the module uses, so they follow the same rule.
	//
	// The file's OWN declarations are validated separately, by lowerObjs and
	// lowerFields, which still see only this file: "declared twice" is a
	// fact about one file's text.
	for name := range scope.Objs {
		l.objs[name] = true
	}
	for name, x := range scope.Externs {
		l.externs[name] = x
	}
	for name, fd := range scope.Fields {
		l.fields[name] = fd
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
	for _, cd := range f.Classes {
		l.lowerClass(cd)
	}
	ids := make(map[string]string, len(l.funcIDs))
	for k, v := range l.funcIDs {
		ids[k] = v.what
	}
	return &Result{
		Program: l.prog,
		FuncIDs: ids,
		Periods: l.periods,
		Loads:   l.loads,
		Spawns:  l.spawns,
	}, bag.Sorted()
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
		if fn.Has("export") {
			continue
		}
		// A triggered function is reached by the generated entry point, which
		// this traversal also cannot see -- and which the compiler itself
		// emits, so claiming it has no caller would be exactly wrong.
		if fn.Has("load") || fn.Has("every") || fn.Has("spawn") {
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
			if m.Has("every") || m.Has("spawn") {
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
	// The clock is generated whenever the module needs one. `boot` is always
	// generated by the driver when there is anything to load.
	hasClock := false
	for _, fn := range f.Funcs {
		if fn.Has("every") {
			hasClock = true
		}
	}
	for _, cd := range f.Classes {
		if cd.Has("every") {
			hasClock = true
		}
		for _, m := range cd.Methods {
			if m.Has("every") || m.Has("spawn") {
				hasClock = true
			}
		}
	}
	if !hasClock {
		return nil
	}
	return []string{"cycle", "make_clock", "on_cycle"}
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

// lowerClassEvent builds one lifecycle hook body.
//
// The marker tag is set BEFORE the user body runs. An early return in the
// body would otherwise skip it, and the entity would be spawned again on
// the next tick, forever.
// lowerSpawnHook emits the tag write that makes a spawn hook run once.
//
// The method's own body was already lowered as its function. What is missing
// is the marker: 1.12.2 has no entity-created event, so "run this once" is a
// tag recording what has been handled, and the dispatcher selects on its
// absence. The tag is written BEFORE the body runs -- an early return would
// otherwise skip it and the entity would be spawned again every tick.
func (l *Lowerer) lowerSpawnHook(cd syntax.ClassDecl, m syntax.Method, fields map[string]string) {
	name := l.path(cd.Name + "/" + m.Name)
	ctx := &fnCtx{path: name, scope: map[string]string{}, localN: &l.localN, class: cd.Name, fields: fields}
	fb := l.prog.NewFunc(name)
	fb.Func().Exported = true
	fb.Func().Origin = l.origin(m.P)
	if len(cd.Fields) > 0 {
		fb.Emit(0, ir.Call{Path: l.path(cd.Name + "/init")})
	}
	fb.Emit(0, ir.Raw{Text: "scoreboard players tag @s add " + spawnTag(cd.Name)})
	end := l.lowerSeq(ctx, fb, 0, m.Body)
	if end != noFallThrough {
		fb.SetTerm(end, ir.Return{})
	}
	l.spawns = append(l.spawns, SpawnEntry{
		Selector: withSelectorArg(cd.Selector, "tag=!"+spawnTag(cd.Name)),
		Path:     name,
		P:        m.P,
	})
}

// clockTag marks the entity that carries the module's clock.
//
// 1.12.2's gameLoopFunction runs a function with NO executor: @s is
// undefined there, and every command that names it is silently dropped.
// The hand-written data packs work around that the same way this does --
// summon one marker entity, then run the tick body inside an execute that
// binds @s to it.
const clockTag = "ggm.clock"

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
	for _, cd := range f.Classes {
		for _, m := range cd.Methods {
			l.claim(l.path(cd.Name+"/"+m.Name), "method "+cd.Name+"."+m.Name, m.P)
		}
		if len(cd.Fields) > 0 {
			l.claim(l.path(cd.Name+"/init"), "the field initialiser for class "+cd.Name, cd.P)
		}
	}
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
	// loopVar names the entity @s is bound to inside a
	// `for e in entities<...>` body. `execute <sel> ~ ~ ~ function <body>`
	// rebinds @s to each matched entity, so `e.field` and `@s.field` are the
	// same score. Without this, `e.field` fell through to the
	// holder.objective shorthand and wrote a fake player named `e`.
	loopVar string
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
	// fanout marks a body that became its OWN FUNCTION: the body of a
	// `for e in entities<...>` loop.
	//
	// The body is entered by `execute <sel> ~ ~ ~ function <body>`, so it is a
	// separate function with its own return. A `return` inside it therefore
	// leaves the BODY and not the function that wrote the loop -- which is
	// not what the author wrote and not what they meant.
	//
	// See fanoutReturn for the whole story; the flag is what lets the
	// return path say so.
	fanout bool
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
		// Characters, not bytes. 1.12.2 truncates a Java String by code unit, so
		// `Count.残照花魄` is legal at 10 characters even though it is 18 bytes in
		// UTF-8. Measuring bytes rejected every Chinese objective name -- and the
		// message said "is 18 characters", so the number it printed was wrong too.
		if n := l.tgt.ObjectiveNameLimit; n > 0 && utf8.RuneCountInString(od.Name) > n {
			l.diags.Error(pos, "E_OBJECTIVE_NAME",
				"objective %q is %d characters; %s truncates at %d, so it collides with any name sharing that prefix",
				od.Name, utf8.RuneCountInString(od.Name), l.tgt.Name, n)
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
	// The duplicate check is against THIS FILE's declarations, not against
	// the module's. `l.fields` already holds every field the module
	// declares, so checking it here would report the file's own
	// declaration as a duplicate of itself.
	//
	// The distinction is the one the language needs: a field declared twice
	// in one file is a mistake in that file's text, while a field declared
	// in two files is the same field said twice, which the first
	// declaration wins.
	seen := map[string]diag.Pos{}
	for _, fd := range f.NBTFields {
		pos := posOf(fd.P)
		if prev, dup := seen[fd.Name]; dup {
			l.diags.Error(pos, "E_FIELD_DUP",
				"nbt field %q is declared twice (first at %s)", fd.Name, prev)
			continue
		}
		seen[fd.Name] = pos
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

// lowerFunc lowers one `fn` declaration and records what triggers it.
//
// The body itself is an ordinary function: nothing about its code depends on
// whether it is scheduled, because a period dispatcher calls it and a load
// entry calls it, and both are just `function <path>`. What the attributes
// change is WHERE the path is called from, and whether it has an executor.
func (l *Lowerer) lowerFunc(fn syntax.FuncDecl) {
	path := l.path(fn.Name)
	// [load] runs from boot, with NO executor: 1.12.2 has no load event, so
	// the user wires a command block, and a command block has no @s.
	//
	// When a function is triggered BOTH ways the contexts intersect, and the
	// intersection of "has @s" and "has none" is "has none". That is why
	// the rule is per-declaration rather than per-call-site: a reader sees
	// [load, every(...)] and knows the body may not touch an entity score.
	noSelf := fn.Has("load")
	ctx := &fnCtx{path: path, scope: map[string]string{}, localN: &l.localN, noSelf: noSelf}
	fb := l.prog.NewFunc(path)
	fb.Func().Exported = true
	fb.Func().NoSelf = noSelf
	if noSelf {
		fb.Func().RegHolder = ir.NoSelfHolder
	}
	fb.Func().Origin = l.origin(fn.P)
	l.bindParams(ctx, fb, fn.Params, fn.Body)
	end := l.lowerSeq(ctx, fb, 0, fn.Body)
	if end != noFallThrough {
		fb.SetTerm(end, ir.Return{})
	}
	if fn.Has("load") {
		if len(fn.Params) > 0 {
			l.diags.Error(posOf(fn.P), "E_LOAD_PARAMS",
				"%s is [load], but nothing can supply its %d parameter(s): the world-load entry is called with no arguments",
				fn.Name, len(fn.Params))
		} else {
			l.loads = append(l.loads, path)
		}
	}
	if fn.EveryTicks > 0 {
		l.periods = append(l.periods, PeriodEntry{Ticks: fn.EveryTicks, Path: path, P: fn.P})
	}
}

// lowerClass lowers every method and records the ones that are scheduled.
//
// A class method with [every(...)] is a period whose body runs once per
// instance: the period dispatcher does `execute <class selector> ~ ~ ~
// function <path>`, so @s inside is the instance. That is the SAME mechanism
// as a free function's period, differing only in the selector -- the clock
// entity is a one-entity fan-out.
func (l *Lowerer) lowerClass(cd syntax.ClassDecl) {
	fields := map[string]string{}
	for _, f := range cd.Fields {
		fields[f.Name] = l.fieldObj(cd.Name, f.Name)
	}
	for _, m := range cd.Methods {
		name := l.path(cd.Name + "/" + m.Name)
		// A [spawn] method is lowered by lowerSpawnHook alone: it has to emit
		// the marker tag BEFORE the body, and lowering the body twice put a
		// second copy after the tag.
		if m.Has("spawn") {
			l.lowerSpawnHook(cd, m, fields)
			continue
		}
		ctx := &fnCtx{path: name, scope: map[string]string{}, localN: &l.localN, class: cd.Name, fields: fields}
		fb := l.prog.NewFunc(name)
		fb.Func().Origin = l.origin(m.P)
		l.bindParams(ctx, fb, m.Params, m.Body)
		end := l.lowerSeq(ctx, fb, 0, m.Body)
		if end != noFallThrough {
			fb.SetTerm(end, ir.Return{})
		}
		if m.EveryTicks > 0 {
			fb.Func().Exported = true
			l.periods = append(l.periods, PeriodEntry{
				Ticks:    m.EveryTicks,
				Path:     name,
				Selector: cd.Selector,
				P:        m.P,
			})
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
	// Truncate by RUNE, not byte.
	//
	// `short[:6]` on a Chinese class name cuts a multi-byte character in half
	// and produces an invalid UTF-8 byte (E5 80 was observed). The objective
	// name then contains bytes 1.12.2 cannot store, and nothing reported it.
	short := class
	if r := []rune(short); len(r) > 6 {
		short = string(r[:6])
	}
	f := field
	if r := []rune(f); len(r) > 8 {
		f = string(r[:8])
	}
	return l.tgt.FitObjective(short + "." + f)
}
