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
type Source struct {
	Name string
	Text string
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
		file, err := syntax.Parse(s.Text)
		if err != nil {
			// A syntax error already carries its position; repeating the
			// filename around it would print the location twice.
			var se *syntax.Error
			if errors.As(err, &se) {
				bag.Error(diag.Pos{Line: se.Pos.Line, Col: se.Pos.Col}, "E_PARSE", "%s: %s", s.Name, se.Msg)
			} else {
				bag.Error(diag.Pos{}, "E_PARSE", "%s: %v", s.Name, err)
			}
			return nil, bag.Sorted()
		}
		srcFiles = append(srcFiles, file)
		own := map[string]int{}
		collectSigs(file, own)
		perFile = append(perFile, own)
	}

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
		res, lowerDiags := lower.LowerWith(file, opt.Target, srcs[i].Name, elsewhere)
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
				pos := diag.Pos{}
				if len(file.Comptime) > 0 {
					pos = diag.Pos{Line: file.Comptime[0].P.Line, Col: file.Comptime[0].P.Col}
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
			continue
		}
		// Append rather than re-lower: each file has already been lowered in
		// its own namespace, and lowering them into one namespace would merge
		// objectives that must stay apart.
		prog.Funcs = append(prog.Funcs, res.Program.Funcs...)
		prog.Consts = append(prog.Consts, res.Program.Consts...)
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

	files, order := mcfunction.Render(prog, mcfunction.Options{Target: opt.Target, Comments: opt.Comments})
	for p, body := range files {
		for _, line := range splitLines(body) {
			if err := mcfunction.Validate(line); err != nil {
				bag.Error(diag.Pos{}, "E_BAD_COMMAND", "%s: %s", p, err)
			}
		}
	}
	out := &Output{Files: files, Order: order, Skipped: optimize.Skipped(opt.Level)}
	out.NeedsConstInit = bootPath(srcFiles[0].Namespace)
	out.NeedsCycle = cyclePath(srcFiles[0].Namespace)
	out.HasCycle = prog.Lookup(out.NeedsCycle) != nil
	return out, bag.Sorted()
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
