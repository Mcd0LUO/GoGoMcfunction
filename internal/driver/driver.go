// Package driver wires the compiler phases together.
//
// Pipeline:
//
//	source --syntax--> AST
//	AST --comptime--> constants
//	AST --lower--> IR(CFG) --optimize--> IR --backend--> text
//
// Each arrow is a package boundary. No phase reaches across more than one.
package driver

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/analysis"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/backend/mcfunction"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/comptime"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/diag"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/ir"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/lower"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/target"
)

// Output is a compiled module.
type Output struct {
	Files map[string]string
	Order []string
	// Skipped names the optimisations the requested level left out.
	Skipped []string
	// NeedsConstInit is the function that must run once on world load.
	// 1.12.2 has no automatic load hook, so the user wires this to a command
	// block or to their own first-tick guard. It runs every file's on-load
	// body and every file's constant pool, in command-line order.
	NeedsConstInit string
	// NeedsCycle is the function gameLoopFunction should name. Empty when
	// the module has no per-tick work at all.
	NeedsCycle string
	// HasCycle reports whether NeedsCycle was actually generated.
	HasCycle bool
	// IR is the optimised program, kept so `-ir` can show the shape the back
	// end was given. Reading a wrong answer out of generated .mcfunction is
	// guesswork: a merged block and an inlined one produce similar commands
	// from very different CFGs, and which one happened decides whether an
	// optimisation is a bug.
	IR *ir.Program
}

// Options configures a compilation.
type Options struct {
	Target target.Target
	// Level selects the optimisation pipeline.
	Level optimize.Level
	// Comments adds a header naming the declaration each generated file
	// came from. Off by default: the output is meant to be read as
	// commands, and a comment per file is noise until you need it.
	Comments bool
}

// Source is one file of a module.
// objOriginEntry is where an objective name was first declared, and what that
// declaration said.
//
// Two files may name the same objective -- a module stating one fact in each
// file that uses it, which the reference data pack does. That is only legal
// while they AGREE: the criteria is what the objective counts, so two files
// disagreeing about it is a contradiction, not a merge.
type objOriginEntry struct {
	Criteria string
	Display  string
	File     string
}

type Source struct {
	Name string
	Text string
	// Parsed, when set, is used instead of parsing Text again.
	//
	// The language server keeps an AST per file so its symbol index does not
	// re-parse the module on every keystroke; handing that AST back here
	// removes the second parse of the same bytes. It must have been produced
	// from Text: a stale AST would make the diagnostics describe a different
	// program than the one on screen.
	Parsed *syntax.File
}

// Compile runs the full pipeline on one source file.
func Compile(src, filename string, opt Options) (*Output, []diag.Diagnostic) {
	return CompileFiles([]Source{{Name: filename, Text: src}}, opt)
}

// CompileFiles compiles several files into ONE module.
//
// A module is more than one file when it uses a library: the library lives in
// its own namespace, so an unqualified name cannot reach it. Linking here --
// rather than concatenating the texts, which would give every file the same
// namespace and let their objectives overwrite each other -- keeps each file's
// namespace intact while making every declaration visible to every other file.
//
// The merge happens at the IR, after each file is lowered with the whole
// module's signatures already in view. That order matters: resolving a call
// needs the library's signatures, or a correct cross-namespace call is
// reported as the typo E_UNKNOWN_CALLEE exists to catch.
func CompileFiles(srcs []Source, opt Options) (*Output, []diag.Diagnostic) {
	if opt.Target.Name == "" {
		opt.Target = target.V1_12_2
	}
	bag := &diag.Bag{}
	if len(srcs) == 0 {
		bag.Error(diag.Pos{}, "E_PARSE", "no input files")
		return nil, bag.Sorted()
	}

	srcFiles := make([]*syntax.File, 0, len(srcs))
	// Signatures are kept per file so that, when one file is lowered, the
	// others can be offered as "declared elsewhere" without the file's own
	// declarations being mistaken for a collision with themselves.
	perFile := make([]map[string]int, 0, len(srcs))
	for _, s := range srcs {
		file := s.Parsed
		if file == nil {
			parsed, err := syntax.Parse(s.Text)
			if err != nil {
				// The file name belongs in Pos.File, not in the message.
				//
				// It used to be prefixed into the text, because Pos had no file
				// field and the CLI printed "4:17: error: x.ggm: ...". That made
				// the message carry a location the renderer did not know about,
				// so a consumer that had its own idea of the file -- an editor
				// showing a squiggle -- saw the name twice or not at all.
				var se *syntax.Error
				if errors.As(err, &se) {
					bag.Error(diag.Pos{File: s.Name, Line: se.Pos.Line, Col: se.Pos.Col}, "E_PARSE", "%s", se.Msg)
				} else {
					bag.Error(diag.Pos{File: s.Name}, "E_PARSE", "%v", err)
				}
				return nil, bag.Sorted()
			}
			file = parsed
		}
		srcFiles = append(srcFiles, file)
		own := map[string]int{}
		collectSigs(file, own)
		perFile = append(perFile, own)
	}

	// The module's obj, extern and nbt declarations, collected ONCE.
	//
	// These three tables say what the MODULE uses, so they are collected
	// here and handed to every file's lowering. They used to be per-file,
	// which made the meaning of a name depend on which file the author
	// happened to be in: an objective declared in file A was a fresh local
	// in file B, and a dotted name in file B became a fake player's score
	// instead of the objective -- a write to a score nobody reads, which
	// 1.12.2 discards without an error. The specification already says the
	// module is one namespace (boot collects every file's load bodies and
	// constant pool); these tables now follow the same rule.
	scope := lower.CollectScope(srcFiles)

	// Every file contributes its on-load bodies and its constant pool to ONE
	// module-level boot function. Per-file entries cannot work: the user
	// wires a single function to the world, and nothing would call a
	// library's <ns>/load. The library's initialisation would simply never
	// run, silently -- which is the same failure as an undeclared objective.
	type bootPart struct{ path, what string }
	var bootParts []bootPart
	// Periods are collected from every file and merged by TICK COUNT.
	//
	// `[every("5s")]` and `[every("100ticks")]` are the same period, so
	// they must share one counter and one dispatcher. Keying on the written
	// text instead would produce two counters that drift apart, and each
	// would look correct in isolation -- the kind of failure this compiler
	// exists to make impossible.
	periods := map[int][]lower.PeriodEntry{}
	var periodOrder []int
	var spawns []lower.SpawnEntry
	var prog *ir.Program
	// `init` first. It is where the back end puts every `objectives add` and
	// the constant pool, and 1.12.2 discards a command that writes an
	// objective that does not exist -- silently, with no error. So the order
	// is not cosmetic: declarations must run before anything that uses them.
	//
	// It used to be missing entirely. `boot` called only the comptime and
	// [load] bodies, so <ns>/init was generated and never called by anything:
	// every scoreboard write in the program was discarded at runtime and
	// nothing said so. The back end's own comment calls the declarations
	// "a correctness requirement, not a nicety" and then put them in a
	// function nothing reached.
	bootParts = append(bootParts, bootPart{srcFiles[0].Namespace + "/init", "objective declarations"})
	// objOrigin remembers which file declared each objective, so a second
	// declaration of the same name can be reported instead of silently
	// replacing the first.
	objOrigin := map[string]objOriginEntry{}
	for i, file := range srcFiles {
		checkLiterals(file, opt.Target, bag)
		elsewhere := map[string]int{}
		for j, m := range perFile {
			if j == i {
				continue
			}
			for k, v := range m {
				elsewhere[k] = v
			}
		}
		res, lowerDiags := lower.LowerWith(file, opt.Target, srcs[i].Name, elsewhere, scope)
		for _, d := range lowerDiags {
			bag.Add(d)
		}

		// Each file gets its own constant pool. Sharing one would put a
		// library's literals on the caller's namespace, and a library is
		// exactly what this function exists to support.
		consts := comptime.Eval(file.Comptime, bag)
		for _, c := range consts {
			if !opt.Target.FitsScore(c.Value) {
				bag.Error(c.P, "E_INT_RANGE",
					"emit value %d is outside the %s score range [%d, %d]",
					c.Value, opt.Target.Name, opt.Target.ScoreMin, opt.Target.ScoreMax)
			}
		}
		if len(consts) > 0 {
			// The constant pool is emitted as a function of its own, so a
			// declaration that already took that name would replace it and
			// silently drop every constant in the program.
			id := strings.ToLower(file.Namespace + "/comptime")
			if what, taken := res.FuncIDs[id]; taken {
				// Name the file either way. The position is the first comptime
				// block when there is one, and the file alone otherwise -- a
				// bare `-:` would not say which file took the name.
				pos := diag.Pos{File: srcs[i].Name}
				if len(file.Comptime) > 0 {
					pos = diag.Pos{File: srcs[i].Name, Line: file.Comptime[0].P.Line, Col: file.Comptime[0].P.Col}
				}
				bag.Error(pos, "E_FN_RESERVED",
					"the compiler-generated constant pool needs the name %q, which %s already uses", id, what)
			} else {
				fb := res.Program.NewFunc(file.Namespace + "/comptime")
				fb.Func().Exported = true
				fb.Emit(0, comptime.ToIR(consts)...)
				bootParts = append(bootParts, bootPart{file.Namespace + "/comptime", "the constant pool"})
			}
		}
		// A [load] function is an ordinary function, so boot calls it by
		// name. Its own name is the author's, which is what makes two [load]
		// functions in one module possible at all: they no longer compete for
		// one generated id.
		for _, p := range res.Loads {
			bootParts = append(bootParts, bootPart{p, "[load]"})
		}
		for _, pe := range res.Periods {
			if _, seen := periods[pe.Ticks]; !seen {
				periodOrder = append(periodOrder, pe.Ticks)
			}
			periods[pe.Ticks] = append(periods[pe.Ticks], pe)
		}
		spawns = append(spawns, res.Spawns...)

		if prog == nil {
			prog = res.Program
			// The first file's declarations still have to be REGISTERED, even
			// though they need no appending: a later file declaring the same
			// name must be reported, and it can only be reported if the first
			// one was recorded.
			for _, od := range res.Program.Objs {
				objOrigin[od.Name] = objOriginEntry{Criteria: od.Criteria, Display: od.Display, File: srcs[i].Name}
			}
			continue
		}
		// Append rather than re-lower: each file has already been lowered in
		// its own namespace, and lowering them into one namespace would merge
		// objectives that must stay apart.
		prog.Funcs = append(prog.Funcs, res.Program.Funcs...)
		prog.Consts = append(prog.Consts, res.Program.Consts...)
		// The objective DECLARATIONS merge too, and they have to: the
		// declaration is what carries the criteria and the display name, and
		// the back end reads them from here.
		//
		// They did not, and a second file's declaration lost both:
		//
		//	obj Beta stat.killCount "beta"
		//
		// became `scoreboard objectives add Beta dummy ggm` -- a different
		// objective, with a different meaning, because the criteria the author
		// wrote was thrown away. The name was still declared, so nothing said
		// so. The first file's declarations were kept only because the first
		// file's program became the module's program.
		// A name declared in two files is a contradiction, not a merge: the
		// criteria decides what the objective COUNTS, and two files cannot
		// disagree about that. Appending both let the last one silently win,
		// so `obj Score stat.kills` in one file and `obj Score stat.deaths` in
		// another produced one declaration with the second's criteria and no
		// message -- the same class of quiet substitution this whole change is
		// meant to remove.
		//
		// One file declaring a name twice is already E_OBJECTIVE_DUP; this is
		// the same mistake spread across two files.
		for _, od := range res.Program.Objs {
			if prev, seen := objOrigin[od.Name]; seen {
				// Declaring the same name in two files is fine when they AGREE:
				// a module that names its objectives in each file that uses
				// them is stating one fact several times, and the reference
				// data pack does exactly that.
				//
				// It is a contradiction when the CRITERIA differ, because that
				// is what the objective counts. Appending both let the last one
				// silently win: `obj Score stat.kills` in one file and
				// `obj Score stat.deaths` in another produced one declaration
				// with the second's criteria and no message.
				if prev.Criteria != od.Criteria || prev.Display != od.Display {
					// "%s", not the message itself: bag.Error treats its third
					// argument as a format string, and the message is already
					// formatted. Passing it directly lets a % in an objective
					// name be re-read as a verb.
					bag.Error(diag.Pos{}, "E_OBJECTIVE_DUP", "%s",
						objConflictMessage(od, prev, srcs[i].Name))
				}
				continue
			}
			objOrigin[od.Name] = objOriginEntry{Criteria: od.Criteria, Display: od.Display, File: srcs[i].Name}
			prog.Objs = append(prog.Objs, od)
		}
	}

	// The module-wide clock, if anything is scheduled.
	buildClock(prog, srcFiles[0].Namespace, periods, periodOrder, spawns)

	// The module-wide boot: the ONE function to run when the world loads.
	//
	// 1.12.2 has no load event, so somebody has to call something. Making
	// that one function the compiler's job rather than the author's is what
	// makes a library usable: the caller wires <ns>/boot and gets every
	// file's initialisation, including files it did not write.
	if len(bootParts) > 0 {
		bootID := bootPath(srcFiles[0].Namespace)
		if prog.Lookup(bootID) != nil {
			bag.Error(diag.Pos{}, "E_FN_RESERVED",
				"the compiler-generated boot entry needs the name %q, which a declaration already uses", bootID)
		} else {
			fb := prog.NewFunc(bootID)
			fb.Func().Exported = true
			for _, p := range bootParts {
				fb.Emit(0, ir.Call{Path: p.path})
			}
		}
	}

	prog = optimize.Run(prog, opt.Level)

	// Structural invariants are checked before rendering, because several of
	// these failures are silent miscompiles rather than crashes.
	for _, v := range analysis.Verify(prog, opt.Target.ObjectiveNameLimit) {
		bag.Error(diag.Pos{}, "E_IR_INVARIANT", "%s", v.String())
	}
	for _, m := range checkNoSelfReachability(prog) {
		bag.Error(diag.Pos{}, "E_NO_SELF_CONTEXT", "%s", m)
	}

	files, order := mcfunction.Render(prog, mcfunction.Options{Target: opt.Target, Comments: opt.Comments})
	for p, body := range files {
		for _, line := range splitLines(body) {
			if err := mcfunction.ValidateFor(opt.Target, line); err != nil {
				bag.Error(diag.Pos{}, "E_BAD_COMMAND", "%s: %s", p, err)
			}
		}
	}
	out := &Output{Files: files, Order: order, Skipped: optimize.Skipped(opt.Level), IR: prog}
	out.NeedsConstInit = bootPath(srcFiles[0].Namespace)
	out.NeedsCycle = cyclePath(srcFiles[0].Namespace)
	out.HasCycle = prog.Lookup(out.NeedsCycle) != nil
	return out, bag.Sorted()
}

// checkNoSelfReachability reports a function that runs without an executor but
// calls one that needs an entity.
//
// [load] runs from a command block, so @s names nothing there. The lowerer
// already refuses `x = 1` written directly in such a body, but the rule did
// not cross a call:
//
//	[load]
//	fn on_load() { worker() }
//	[export]
//	fn worker() { N = 1 }        // -> scoreboard players set @s N 1
//
// compiled cleanly, and 1.12.2 discarded the set because no entity is bound.
// The program did nothing at world load and said nothing about it.
//
// Reachability is transitive: a helper called only from [load] is part of that
// body for this purpose.
func checkNoSelfReachability(prog *ir.Program) []string {
	byPath := map[string]*ir.Function{}
	for _, fn := range prog.Funcs {
		byPath[fn.Path] = fn
	}
	var roots []*ir.Function
	for _, fn := range prog.Funcs {
		if fn.NoSelf {
			roots = append(roots, fn)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	var walk func(fn *ir.Function, from string)
	walk = func(fn *ir.Function, from string) {
		if seen[fn.Path] {
			return
		}
		seen[fn.Path] = true
		for _, blk := range fn.Blocks {
			for _, op := range blk.Ops {
				for _, callee := range calleesOf(op) {
					next, ok := byPath[callee]
					if !ok {
						continue
					}
					if !next.NoSelf && usesSelf(next) {
						out = append(out, fmt.Sprintf(`%s runs without an executor (reached from %s) but calls %s, which reads or writes @s; 1.12.2 discards those commands because no entity is bound`, next.Path, from, next.Path))
						continue
					}
					walk(next, from)
				}
			}
		}
	}
	for _, r := range roots {
		walk(r, r.Path)
	}
	return out
}

// calleesOf lists the functions an op calls by name.
func calleesOf(op ir.Op) []string {
	switch o := op.(type) {
	case ir.Call:
		return []string{o.Path}
	case ir.FanOut:
		return []string{o.Path}
	case ir.Guarded:
		return calleesOf(o.Inner)
	default:
		// Every remaining op is a score operation that names no function, so
		// it has no callees. Listing them explicitly keeps the omission
		// visible: if a future ir.Op carries a path, this is where it must be
		// added, and `default` returning nil would hide it.
		if _, isSet := op.(ir.Set); isSet {
			return nil
		}
		if _, isCopy := op.(ir.Copy); isCopy {
			return nil
		}
		if _, isArith := op.(ir.Arith); isArith {
			return nil
		}
		if _, isAdd := op.(ir.Add); isAdd {
			return nil
		}
		if _, isRemove := op.(ir.Remove); isRemove {
			return nil
		}
		if _, isRaw := op.(ir.Raw); isRaw {
			return nil
		}
		panic(fmt.Sprintf("calleesOf: unhandled ir.Op %T", op))
	}
}

// usesSelf reports whether a function touches the executing entity.
func usesSelf(fn *ir.Function) bool {
	for _, blk := range fn.Blocks {
		for _, op := range blk.Ops {
			for _, l := range append(analysis.Uses(op), analysis.Defs(op)...) {
				if l.Holder == ir.SelfHolder {
					return true
				}
			}
			if r, ok := op.(ir.Raw); ok && strings.Contains(r.Text, "@s") {
				return true
			}
		}
	}
	return false
}

// buildClock generates the module's timer: cycle, make_clock, on_cycle, and
// one dispatcher per period.
//
// 1.12.2 offers exactly one automatic hook -- `gamerule gameLoopFunction` --
// and it calls a function with NO executor, so @s there names nothing and
// every command mentioning it is discarded without an error. The hand-written
// data packs work around that by summoning one marker entity and running the
// tick body inside `execute @e[tag=...]`; this is the same shape, generated.
//
//	cycle       what gameLoopFunction names; no executor, does no arithmetic
//	make_clock  summons the marker unless it already exists
//	on_cycle    runs with @s = the clock entity
//	every/<n>t  one dispatcher per period, entered from on_cycle or another
//	            period
//
// Periods form a CHAIN rather than a fan-out: only the periods no smaller
// period divides are driven from on_cycle, and each drives the next by
// counting the RATIO. Driving all of them from on_cycle would cost one
// counter and one guarded call per period per tick.
func buildClock(prog *ir.Program, ns string, periods map[int][]lower.PeriodEntry, order []int, spawns []lower.SpawnEntry) {
	if len(periods) == 0 && len(spawns) == 0 {
		return
	}
	path := func(name string) string { return ns + "/" + name }

	mk := prog.NewFunc(path("make_clock"))
	mk.Func().Exported = true
	mk.Emit(0, ir.Raw{Text: "summon armor_stand ~ ~ ~ {Tags:[\"" + clockTag +
		"\"],Marker:1b,Invisible:1b,NoGravity:1b,Invulnerable:1b,PersistenceRequired:1b}"})

	// on_cycle: @s is the clock entity here, so this is where ordinary code
	// can live. Spawn hooks run FIRST, so an entity that appeared this tick
	// is initialised before its tick body sees it.
	onCycle := prog.NewFunc(path("on_cycle"))
	onCycle.Func().Exported = true
	for _, sp := range spawns {
		onCycle.Emit(0, ir.FanOut{Selector: sp.Selector, Path: sp.Path})
	}
	// The one-tick period IS the cycle, so it is inlined rather than given a
	// counter: a counter that reaches 1 every tick is two wasted commands and
	// a guard that is always true.
	for _, pe := range periods[1] {
		if pe.Selector == "" {
			onCycle.Emit(0, ir.Call{Path: pe.Path})
		} else {
			onCycle.Emit(0, ir.FanOut{Selector: pe.Selector, Path: pe.Path})
		}
	}
	for _, ticks := range roots(order, periods) {
		emitPeriodDrive(onCycle, 0, path, ticks, ticks, periods)
	}
	onCycle.SetTerm(0, ir.Return{})

	for _, ticks := range order {
		if ticks == 1 {
			continue
		}
		buildDispatcher(prog, path, ticks, periods)
	}

	cyc := prog.NewFunc(path("cycle"))
	cyc.Func().Exported = true
	cyc.Emit(0, ir.Raw{Text: fmt.Sprintf("function %s unless @e[tag=%s]", path("make_clock"), clockTag)})
	cyc.Emit(0, ir.Raw{Text: fmt.Sprintf("execute @e[tag=%s,c=1] ~ ~ ~ function %s", clockTag, path("on_cycle"))})
}

// roots lists the periods on_cycle drives directly: those no smaller period
// divides. 1 is never a root because on_cycle IS the one-tick period.
func roots(order []int, periods map[int][]lower.PeriodEntry) []int {
	var out []int
	for _, t := range order {
		if t == 1 {
			continue
		}
		divided := false
		for _, other := range order {
			if other < t && t%other == 0 {
				divided = true
				break
			}
		}
		if !divided {
			out = append(out, t)
		}
	}
	return out
}

// downstream lists the periods a given one drives.
//
// A period drives another when it divides it and no CLOSER period also
// divides it -- otherwise the intermediate would advance twice.
func downstream(order []int, from int) []int {
	var out []int
	for _, t := range order {
		if t <= from || t%from != 0 {
			continue
		}
		closer := false
		for _, mid := range order {
			if mid > from && mid < t && t%mid == 0 {
				closer = true
				break
			}
		}
		if !closer {
			out = append(out, t)
		}
	}
	return out
}

// periodCounter names a period's counter objective.
//
// Keyed on the TICK COUNT, never on the written duration: `5s` and
// `100ticks` are one period, so they must be one counter. `p.<ticks>` also
// stays inside the 16-character objective limit for any period a program
// would write.
func periodCounter(ticks int) string { return fmt.Sprintf("p.%d", ticks) }

// emitPeriodDrive appends the counter increment and guarded call for a period.
func emitPeriodDrive(fb *ir.FuncBuilder, block ir.BlockID, path func(string) string, ticks, ratio int, periods map[int][]lower.PeriodEntry) {
	obj := periodCounter(ticks)
	fb.Emit(block, ir.Add{Dst: ir.Loc{Holder: ir.SelfHolder, Obj: obj}, Value: 1})
	fb.Emit(block, ir.Raw{Text: fmt.Sprintf(
		"function %s if @s[score_%s_min=%d,score_%s=%d]",
		path(fmt.Sprintf("every/%dt", ticks)), obj, ratio, obj, ratio)})
}

// buildDispatcher emits one period's entry: reset the counter, run every body
// registered for this period, then drive the periods it divides.
//
// The reset comes FIRST and unconditionally. Putting it after the bodies
// meant an early `return` in one of them skipped it, and the period then
// fired every tick forever.
func buildDispatcher(prog *ir.Program, path func(string) string, ticks int, periods map[int][]lower.PeriodEntry) {
	fb := prog.NewFunc(path(fmt.Sprintf("every/%dt", ticks)))
	fb.Func().Exported = true
	fb.Emit(0, ir.Set{Dst: ir.Loc{Holder: ir.SelfHolder, Obj: periodCounter(ticks)}, Value: 0})
	for _, pe := range periods[ticks] {
		if pe.Selector == "" {
			fb.Emit(0, ir.Call{Path: pe.Path})
		} else {
			fb.Emit(0, ir.FanOut{Selector: pe.Selector, Path: pe.Path})
		}
	}
	order := make([]int, 0, len(periods))
	for t := range periods {
		order = append(order, t)
	}
	for _, down := range downstream(order, ticks) {
		emitPeriodDrive(fb, 0, path, down, down/ticks, periods)
	}
	fb.SetTerm(0, ir.Return{})
}

// clockTag marks the entity that carries the module's clock.
const clockTag = "ggm.clock"

// bootPath is the module-wide load entry.
//
// Named after the first file's namespace because a module has one entry
// point: the user wires ONE function, not one per file.
func bootPath(ns string) string { return ns + "/boot" }

// cyclePath is the module-wide tick entry, the one gameLoopFunction names.
func cyclePath(ns string) string { return ns + "/cycle" }

// objConflictMessage explains why two files disagree about one objective.
//
// The two fields are NOT equally important, and a message that always talks
// about the criteria prints the same criteria twice when the criteria agree
// and only the DISPLAY name differs:
//
//	obj Score stat.kills "kills"    file A
//	obj Score stat.kills "deaths"   file B
//
// would be reported as "different criteria: stat.kills in A, stat.kills in B".
// That reads as a mistake in the message rather than in the source, and the
// author has to work out which field actually moved.
//
// So the message names the field that differs, and says what it decides.
// The CRITERIA decides what the objective counts, so a disagreement there is
// a contradiction about the program. The DISPLAY NAME is only the label
// shown in the scoreboard sidebar, so a disagreement there is a presentation
// disagreement -- still worth saying, but a different kind of thing, and the
// message says so.
func objConflictMessage(od ir.ObjDecl, prev objOriginEntry, file string) string {
	if prev.Criteria != od.Criteria {
		return fmt.Sprintf(
			"objective %q is declared twice with different criteria: %s in %s, %s in %s; "+
				"the criteria decides what the objective counts, so only one can be right",
			od.Name, prev.Criteria, prev.File, od.Criteria, file)
	}
	return fmt.Sprintf(
		"objective %q is declared twice with different display names: %q in %s, %q in %s; "+
			"the display name is only the label shown in the scoreboard sidebar, "+
			"so this is a presentation disagreement rather than a different objective",
		od.Name, prev.Display, prev.File, od.Display, file)
}

// collectSigs records every function a file declares, qualified the way the
// lowerer names them.
func collectSigs(f *syntax.File, out map[string]int) {
	root, pre := f.Namespace, ""
	if i := strings.Index(root, ":"); i >= 0 {
		root, pre = root[:i], root[i+1:]
	}
	q := func(name string) string {
		if pre == "" {
			return root + ":" + name
		}
		return root + ":" + pre + "/" + name
	}
	for _, fn := range f.Funcs {
		out[q(fn.Name)] = len(fn.Params)
	}
	for _, cd := range f.Classes {
		for _, m := range cd.Methods {
			out[q(cd.Name+"/"+m.Name)] = len(m.Params)
		}
	}
}

// checkLiterals rejects integer literals the target cannot represent.
//
// This is a hard error rather than a warning because there is no correct
// code to emit. The runtime wraps silently, so a rejected program would
// otherwise compile, run, and produce a wrong value with no error
// anywhere -- the same failure mode as a mis-derived selector bound.
func checkLiterals(file *syntax.File, tgt target.Target, bag *diag.Bag) {
	syntax.EachIntLit(file, func(v int, pos syntax.Pos) {
		if !tgt.FitsScore(v) {
			bag.Error(diag.Pos{Line: pos.Line, Col: pos.Col}, "E_INT_RANGE",
				"%d is outside the %s score range [%d, %d]",
				v, tgt.Name, tgt.ScoreMin, tgt.ScoreMax)
		}
	})
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// CountLines reports total generated lines, for reporting.
func (o *Output) CountLines() int {
	n := 0
	for _, b := range o.Files {
		n += len(splitLines(b))
	}
	return n
}

var _ = ir.Program{}
var _ = fmt.Sprint
