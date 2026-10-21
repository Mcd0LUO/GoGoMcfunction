package lower

import (
	"fmt"
	"strings"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/ir"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
)

// commandSpec describes one builtin that emits a whole command.
//
// The commands in this table are not special: each is a verb followed by
// arguments, and every argument is command text. What the compiler has to know
// is only two things -- the verb, and WHICH argument is a selector.
//
// Writing twenty `case` arms that differ only in a verb was the alternative,
// and it is the shape this file already had for kill/tp/say. Each new command
// would have been a branch to add, test and get subtly wrong. A table makes a
// new command one line, and makes it obvious that none of them does anything
// clever.
//
// targetAt is the argument index that must be a selector (or self), or -1 when
// the command takes no target. A selector is the only argument with structure:
// it is validated against the version's argument whitelist, and it is the only
// thing an author writes that the compiler can check.
type commandSpec struct {
	verb     string
	targetAt int
	// minArgs is the number of arguments the command needs, so that a call
	// missing its verb's operands is reported here rather than becoming a
	// command 1.12.2 will reject at run time.
	minArgs int
}

// commandBuiltins is the whole table.
//
// It covers the commands the reference data pack actually uses, in the
// proportions it uses them: `particle` (839) and `tellraw` (333) dwarf
// `advancement` (26). The ones left out are the world-editing commands
// (setblock, fill, clone, blockdata) -- a data pack drives entities and
// scores, and nothing in 1964 functions of real code writes a block.
// The argument positions below were checked against the reference data pack's
// own usage rather than against the command reference, because that is where
// they differ: `playsound` puts the selector LAST, `replaceitem` puts it
// after the word `entity`, and `advancement` puts an action word in front of
// it. Guessing from the command name gave three wrong tables in a row.
var commandBuiltins = map[string]commandSpec{
	// entity and item
	"summon":        {verb: "summon", targetAt: -1, minArgs: 1},
	"entitydata":    {verb: "entitydata", targetAt: 0, minArgs: 2},
	"effect":        {verb: "effect", targetAt: 0, minArgs: 3},
	"tp":            {verb: "tp", targetAt: 0, minArgs: 4},
	"kill":          {verb: "kill", targetAt: 0, minArgs: 1},
	"give":          {verb: "give", targetAt: 0, minArgs: 2},
	"clear":         {verb: "clear", targetAt: 0, minArgs: 1},
	"replaceitem":   {verb: "replaceitem", targetAt: 1, minArgs: 3},
	"spreadplayers": {verb: "spreadplayers", targetAt: 5, minArgs: 6},
	"spawnpoint":    {verb: "spawnpoint", targetAt: 0, minArgs: 2},

	// presentation
	"particle":  {verb: "particle", targetAt: -1, minArgs: 1},
	"tellraw":   {verb: "tellraw", targetAt: 0, minArgs: 2},
	"title":     {verb: "title", targetAt: 0, minArgs: 2},
	"playsound": {verb: "playsound", targetAt: 2, minArgs: 3},
	"say":       {verb: "say", targetAt: -1, minArgs: 1},

	// progress and score-adjacent
	"advancement": {verb: "advancement", targetAt: 1, minArgs: 3},
	"trigger":     {verb: "trigger", targetAt: -1, minArgs: 1},
	// enable is trigger's other half: 1.12.2 refuses a trigger write on an
	// objective that has not been enabled for that player, and says nothing.
	"enable":     {verb: "scoreboard players enable", targetAt: 0, minArgs: 2},
	"tag_add":    {verb: "scoreboard players tag", targetAt: 0, minArgs: 2},
	"tag_remove": {verb: "scoreboard players tag", targetAt: 0, minArgs: 2},
}

func (l *Lowerer) lowerCallStmt(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, s syntax.ExprStmt) {
	call, ok := s.X.(syntax.Call)
	if !ok {
		l.diags.Error(posOf(s.P), "E_STMT", "expression statement must be a call")
		return
	}
	// stats is the one command that does not fit the table: it names the same
	// selector twice, once as the entity whose statistic is bound and once as
	// the score that receives it.
	//
	//	stats entity @s set SuccessCount @s temp4
	//
	// Folding it into the table would need a "repeat this argument" rule,
	// which is a bigger idea than one command deserves.
	if call.Name == "stats" {
		if len(call.Args) != 4 {
			l.diags.Error(posOf(call.P), "E_ARG", "stats needs (target, \"set|clear\", stat, objective)")
			return
		}
		target := l.targetArg(call, 0)
		verb := l.rawString(call, 1)
		stat := l.rawString(call, 2)
		obj := l.rawString(call, 3)
		fb.Emit(block, ir.Raw{Text: fmt.Sprintf("stats entity %s %s %s %s %s",
			target, verb, stat, target, obj)})
		return
	}
	// Every command builtin goes through one table. Only two things differ
	// between them: the verb, and which argument is a selector.
	if spec, ok := commandBuiltins[call.Name]; ok {
		l.emitCommand(ctx, fb, block, call, spec)
		return
	}
	if call.Name == "cmd" {
		// cmd("<verb and args>") -- one command, verbatim.
		//
		// The escape hatch, deliberately spelled the same as the comptime
		// builtin of the same name so that "emit an arbitrary command" has ONE
		// name in the language. At compile time the text is assembled from
		// constants; here it is a literal typed into the source.
		//
		// It bypasses the builtin table but NOT the validator: the rendered
		// line still goes through mcfunction.Validate, so a selector argument
		// that 1.12.2 does not understand is still caught.
		text := l.rawString(call, 0)
		if text == "" {
			l.diags.Error(posOf(call.P), "E_ARG", "cmd needs one non-empty command string")
			return
		}
		fb.Emit(block, ir.Raw{Text: text})
		return
	}
	if xd, ok := l.externs[call.Name]; ok {
		l.marshalExtern(ctx, fb, block, call, xd)
		fb.Emit(block, ir.Call{Path: call.Name})
		return
	}
	path, ok := l.resolveFunction(ctx, call)
	if !ok {
		return
	}
	l.marshalArgs(ctx, fb, block, path, call)
	fb.Emit(block, ir.Call{Path: path})
}

// call lowers a call used as a value.
func (l *Lowerer) call(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, c syntax.Call) (ir.Loc, error) {
	if c.Name == "reduce<min>" || c.Name == "reduce<max>" {
		return l.reduce(ctx, fb, block, c)
	}
	if xd, ok := l.externs[c.Name]; ok {
		return l.callExtern(ctx, fb, block, c, xd)
	}
	if r, ok := l.builtin(ctx, fb, block, c); ok {
		return r, nil
	}
	path, ok := l.resolveFunction(ctx, c)
	if !ok {
		return ir.Loc{}, nil
	}
	n := l.sigs[path]
	if len(c.Args) != n {
		l.diags.Error(posOf(c.P), "E_ARITY", "%s takes %d argument(s), got %d", c.Name, n, len(c.Args))
		return ir.Loc{}, nil
	}
	l.marshalArgs(ctx, fb, block, path, c)
	fb.Emit(block, ir.Call{Path: path})
	t := fb.TmpLoc()
	fb.Emit(block, ir.Copy{Dst: t, Src: ctx.loc("return")})
	return t, nil
}

// resolveFunction reports the path a call targets, and reports the call
// as naming nothing when it does not.
//
// This is a fact only a whole-program view can supply. In 1.12.2 a
// function command naming something that does not exist is not an error
// at load time and does nothing at run time, so a mistyped name buys a
// silently dead code path rather than a message. A reference project's
// own audit found exactly that: a call written without its namespace
// prefix left a random-number routine returning whatever the caller
// happened to have in the return slot.
func (l *Lowerer) resolveFunction(ctx *fnCtx, c syntax.Call) (string, bool) {
	if path, ok := l.callTarget(ctx.class, c); ok {
		return path, true
	}
	path := l.path(c.Name)
	if strings.Contains(c.Name, ":") {
		path = c.Name
	}
	if reservedCalls[c.Name] {
		// These are in the language, and this build does not implement
		// them everywhere the grammar allows. Saying "unknown function"
		// would send the reader looking for a declaration that is not
		// supposed to exist.
		l.diags.Error(posOf(c.P), "E_BUILTIN_UNIMPLEMENTED",
			"%s is a builtin that this build does not implement in this position", c.Name)
		return path, false
	}
	l.diags.Error(posOf(c.P), "E_UNKNOWN_CALLEE",
		"no function named %q in this module; 1.12.2 does not treat that as an error, the call simply never runs", c.Name)
	return path, false
}

// callTarget resolves a call to a declared function, without reporting.
//
// An unqualified call inside a class may name a method of that class.
// Without this the call went to <ns>/<name>, which almost never existed,
// so a method calling a sibling method silently called nothing at all --
// and the sibling was then pruned as unreachable, so the two mistakes
// covered for each other.
func (l *Lowerer) callTarget(class string, c syntax.Call) (string, bool) {
	if _, ok := l.externs[c.Name]; ok {
		return "", false
	}
	// A name that already carries its namespace is absolute: it names a
	// function in another file of the module, which is how a library is
	// called. Prefixing it with this file's namespace would look for
	// `t:app/std:math/double`, which can never exist.
	//
	// The leading colon is what distinguishes it from an ordinary name: no
	// local declaration may contain one, so this cannot shadow anything.
	if strings.Contains(c.Name, ":") {
		if l.hasSig(c.Name) {
			return c.Name, true
		}
		return c.Name, false
	}
	if path := l.path(c.Name); l.hasSig(path) {
		return path, true
	}
	if class != "" && !strings.Contains(c.Name, ".") {
		if inClass := l.path(class + "/" + c.Name); l.hasSig(inClass) {
			return inClass, true
		}
	}
	return l.path(c.Name), false
}

// hasSig reports whether a function path is declared.
func (l *Lowerer) hasSig(path string) bool {
	_, ok := l.sigs[path]
	return ok
}

// reservedCalls are the builtins whose names the language owns but this
// build does not implement in every position the grammar allows.
//
// abs is implemented only as the left operand of a comparison, where it
// is rewritten algebraically and never becomes a call. count has no
// implementation at all.
var reservedCalls = map[string]bool{"abs": true, "clamp": true, "count": true}

// marshalArgs moves arguments into the callee's ABI registers.
// 1.12.2 has no call stack, so parameters are entity-scoped objectives.
func (l *Lowerer) marshalArgs(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, target string, c syntax.Call) {
	n, known := l.sigs[target]
	if !known {
		return
	}
	if len(c.Args) != n {
		l.diags.Error(posOf(c.P), "E_ARITY", "%s takes %d argument(s), got %d", target, n, len(c.Args))
		return
	}
	// Parameters live in the same register file as everything else, so writing
	// a0 while a later argument still needs to read a0 would corrupt it. This is
	// the classic parallel-move problem: evaluate every argument into a scratch
	// slot first, then commit the registers.
	//
	// Example that motivated this: `rand(1, scale)` inside a function whose
	// first parameter is scale (also a0) emitted `a0 = 1; a1 = a0`, silently
	// passing 1 as the range instead of scale.
	operands := make([]ir.Loc, len(c.Args))
	spilled := make([]bool, len(c.Args))
	for i, a := range c.Args {
		op, err := l.expr(ctx, fb, block, a)
		if err != nil {
			l.diags.Error(posOf(c.P), "E_ARG", "%s", err)
			return
		}
		operands[i] = op
		if needsSpill(op, n) {
			t := fb.TmpLoc()
			fb.Emit(block, ir.Copy{Dst: t, Src: op})
			operands[i] = t
			spilled[i] = true
		}
	}
	for i := range c.Args {
		dst := ctx.loc(fmt.Sprintf("a%d", i))
		if spilled[i] || operands[i] != dst {
			fb.Emit(block, ir.Copy{Dst: dst, Src: operands[i]})
		}
	}
}

// needsSpill reports whether an operand must be copied aside before registers
// are committed. Anything already living in an ABI register is at risk, because
// committing an earlier argument overwrites it.
func needsSpill(op ir.Loc, paramCount int) bool {
	if op.Holder != "@s" {
		return false
	}
	if len(op.Obj) < 2 || op.Obj[0] != 'a' {
		return false
	}
	for _, ch := range op.Obj[1:] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

// clobberSummary computes, for every function in the module, how many
// ABI registers a call to it disturbs.
//
// A call writes a0..a(n-1) to pass n arguments, and the callee's own body
// may write a0..a(m-1) when it calls something in turn. A caller holding
// a value in one of those registers across the call loses it, so this has
// to be the transitive maximum rather than the argument count.
//
// The case that exposed it: lcg_next takes no arguments, so its call
// sites looked harmless, but its body passes one argument to lcg_step and
// so writes a0. luo:math/rand kept lo in a0 across that call and read the
// step's argument back instead of lo.
//
// The assignment is monotone over a finite lattice, so iterating until
// nothing changes terminates and handles recursion without a special case.
func (l *Lowerer) clobberSummary(bodies map[string][]syntax.Stmt) map[string]int {
	clobber := map[string]int{}
	for path := range bodies {
		clobber[path] = 0
	}
	for changed := true; changed; {
		changed = false
		for path, body := range bodies {
			want := 0
			for _, c := range syntax.Calls(body) {
				if n := l.regsTouched(c, clobber); n > want {
					want = n
				}
			}
			if want > clobber[path] {
				clobber[path] = want
				changed = true
			}
		}
	}
	return clobber
}

// regsTouched reports how many ABI registers a single call disturbs.
func (l *Lowerer) regsTouched(c syntax.Call, clobber map[string]int) int {
	if xd, ok := l.externs[c.Name]; ok {
		worst := len(c.Args)
		for _, in := range xd.In {
			if n, ok := abiIndex(in.Reg); ok && n+1 > worst {
				worst = n + 1
			}
		}
		return worst
	}
	path := l.path(c.Name)
	worst := l.sigs[path]
	if n, ok := clobber[path]; ok && n > worst {
		worst = n
	}
	return worst
}

// abiIndex reads the register number out of an ABI register name.
func abiIndex(reg string) (int, bool) {
	if len(reg) < 2 || reg[0] != 'a' {
		return 0, false
	}
	n := 0
	for _, ch := range reg[1:] {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		n = n*10 + int(ch-'0')
	}
	return n, true
}

// callInto stores a call's result directly into dst, and reports
// whether it handled the call.
//
// It declines builtins and reduce, which produce their value in a slot of
// their own; those keep the general path.
func (l *Lowerer) callInto(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, dst ir.Loc, c syntax.Call) bool {
	if xd, ok := l.externs[c.Name]; ok {
		l.marshalExtern(ctx, fb, block, c, xd)
		fb.Emit(block, ir.Call{Path: c.Name})
		out := xd.Out
		if out == "" {
			out = "return"
		}
		fb.Emit(block, ir.Copy{Dst: dst, Src: ctx.loc(out)})
		return true
	}
	path := l.path(c.Name)
	if strings.Contains(c.Name, ":") {
		path = c.Name
	}
	if _, ok := l.sigs[path]; !ok {
		return false
	}
	l.marshalArgs(ctx, fb, block, path, c)
	fb.Emit(block, ir.Call{Path: path})
	fb.Emit(block, ir.Copy{Dst: dst, Src: ctx.loc("return")})
	return true
}

func (l *Lowerer) marshalExtern(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, c syntax.Call, x syntax.ExternDecl) {
	for i, a := range c.Args {
		if i >= len(x.In) {
			break
		}
		src, err := l.expr(ctx, fb, block, a)
		if err != nil {
			l.diags.Error(posOf(c.P), "E_ARG", "%s", err)
			return
		}
		fb.Emit(block, ir.Copy{Dst: ctx.loc(x.In[i].Reg), Src: src})
	}
}

func (l *Lowerer) callExtern(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, c syntax.Call, x syntax.ExternDecl) (ir.Loc, error) {
	l.marshalExtern(ctx, fb, block, c, x)
	fb.Emit(block, ir.Call{Path: c.Name})
	out := x.Out
	if out == "" {
		out = "return"
	}
	t := fb.TmpLoc()
	fb.Emit(block, ir.Copy{Dst: t, Src: ctx.loc(out)})
	return t, nil
}

func (l *Lowerer) reduce(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, c syntax.Call) (ir.Loc, error) {
	if len(c.Args) != 2 {
		return ir.Loc{}, fmt.Errorf("reduce needs (selector, objective)")
	}
	sel, ok := c.Args[0].(syntax.Ident)
	if !ok || !strings.HasPrefix(sel.Name, "@") {
		return ir.Loc{}, fmt.Errorf("reduce first argument must be a selector")
	}
	obj, ok := c.Args[1].(syntax.Ident)
	if !ok {
		return ir.Loc{}, fmt.Errorf("reduce second argument must be an objective name")
	}
	// The accumulator is a fake player, not a temp on @s.
	//
	// `scoreboard players operation @s t < @a[tag=x] obj` looks plausible and
	// is silently discarded: 1.12.2's `operation` takes ONE score holder on
	// the right, never a selector. Measured on the engine: the command reports
	// success, applies nothing, and the accumulator keeps its seed, so a
	// reduce returns 2147483647 for every input.
	//
	// The working shape is the one the hand-written library uses: seed a fake
	// player, then fan out with `execute` so each matching entity compares its
	// own score against it. The holder is a constant name so the fan-out and
	// the read-back refer to the same score.
	acc := ir.Loc{Holder: "#ggm.reduce", Obj: obj.Name}
	t := fb.TmpLoc()
	seed := 2147483647
	op := "<"
	if strings.Contains(c.Name, "max") {
		op = ">"
		seed = -2147483647
	}
	fb.Emit(block, ir.Set{Dst: acc, Value: seed})
	fb.Emit(block, ir.Raw{Text: fmt.Sprintf("execute %s ~ ~ ~ scoreboard players operation %s %s %s @s %s",
		sel.Name, acc.Holder, acc.Obj, op, obj.Name)})
	fb.Emit(block, ir.Copy{Dst: t, Src: acc})
	return t, nil
}

// builtin lowers the fixed standard library. Each entry must map onto a bounded
// number of scoreboard operations, and must not require control flow that the
// current block cannot express.
func (l *Lowerer) builtin(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, c syntax.Call) (ir.Loc, bool) {
	arg := func(i int) (ir.Loc, bool) {
		if i >= len(c.Args) {
			return ir.Loc{}, false
		}
		o, err := l.expr(ctx, fb, block, c.Args[i])
		if err != nil {
			l.diags.Error(posOf(c.P), "E_EXPR", "%s", err)
			return ir.Loc{}, false
		}
		return o, true
	}
	switch c.Name {
	case "min", "max":
		a, ok1 := arg(0)
		b, ok2 := arg(1)
		if !ok1 || !ok2 {
			return ir.Loc{}, true
		}
		t := fb.TmpLoc()
		op := "<"
		if c.Name == "max" {
			op = ">"
		}
		fb.Emit(block, ir.Copy{Dst: t, Src: a})
		fb.Emit(block, ir.Arith{Dst: t, Op: op, Src: b})
		return t, true
	case "rem_euclid":
		a, ok1 := arg(0)
		m, ok2 := arg(1)
		if !ok1 || !ok2 {
			return ir.Loc{}, true
		}
		t, u := fb.TmpLoc(), fb.TmpLoc()
		fb.Emit(block, ir.Copy{Dst: t, Src: a})
		fb.Emit(block, ir.Arith{Dst: t, Op: "%=", Src: m})
		fb.Emit(block, ir.Copy{Dst: u, Src: t})
		fb.Emit(block, ir.Arith{Dst: u, Op: "+=", Src: m})
		fb.Emit(block, ir.Arith{Dst: u, Op: "%=", Src: m})
		return u, true
	case "str":
		// compile-time only; reaching here means a runtime use
		l.diags.Error(posOf(c.P), "E_STR_RUNTIME", "str() is compile-time only")
		return ir.Loc{}, true
	}
	return ir.Loc{}, false
}

// emitCommand renders one table-driven command.
//
// The whole body is: put the verb first, then walk the arguments, reading the
// one at targetAt as a selector and every other one as literal text.
//
// Selectors are read separately because they are the only argument with
// structure -- and the only one the compiler can check. Everything else is
// command text, and text has to be a literal: 1.12.2 builds a command from a
// string and nothing can splice a runtime value into one.
func (l *Lowerer) emitCommand(ctx *fnCtx, fb *ir.FuncBuilder, block ir.BlockID, c syntax.Call, spec commandSpec) {
	if len(c.Args) < spec.minArgs {
		l.diags.Error(posOf(c.P), "E_ARG", "%s needs at least %d argument(s), got %d",
			c.Name, spec.minArgs, len(c.Args))
		return
	}
	// tag_add / tag_remove share one verb and differ in the word after the
	// selector, so the word is appended right after the target.
	parts := []string{spec.verb}
	for i := range c.Args {
		if i == spec.targetAt {
			parts = append(parts, l.targetArg(c, i))
			if c.Name == "tag_add" {
				parts = append(parts, "add")
			} else if c.Name == "tag_remove" {
				parts = append(parts, "remove")
			}
			continue
		}
		text, ok := l.rawArg(c, i)
		if !ok {
			return
		}
		parts = append(parts, text)
	}
	fb.Emit(block, ir.Raw{Text: strings.Join(parts, " ")})
}

// rawArg reads an argument that becomes literal command text.
//
// Two shapes are accepted, and the distinction matters:
//
//	"stone"      a string literal
//	n"{Tags:[…]}" an NBT literal
//
// Neither can hold a score. That is not a limitation of this function but of
// 1.12.2: a command is text, and there is no way to splice a scoreboard value
// into text. `summon` with an NBT payload that depends on a runtime value is
// not something this compiler can emit, and pretending otherwise by accepting
// an int and writing its INITIAL value would produce a command that looks
// right and silently ignores every change since.
//
// So the error names the boundary rather than the syntax: an author who writes
// summon("pig", hp) should learn why that cannot work, not that argument 2 has
// the wrong type.
func (l *Lowerer) rawArg(c syntax.Call, i int) (string, bool) {
	if i >= len(c.Args) {
		l.diags.Error(posOf(c.P), "E_ARG", "%s needs %d arguments, got %d", c.Name, i+1, len(c.Args))
		return "", false
	}
	switch a := c.Args[i].(type) {
	case syntax.StringLit:
		return a.Value, true
	case syntax.NBTLit:
		return a.Text, true
	case syntax.IntLit:
		// A bare integer is legitimate for counts and durations, which are
		// ordinary numbers in the command grammar rather than text.
		return itoa(a.Value), true
	case syntax.BoolLit:
		// `effect @a speed 10 1 true` -- the flag is a literal word, and
		// `true`/`false` are the two spellings 1.12.2 accepts.
		if a.Value {
			return "true", true
		}
		return "false", true
	}
	l.diags.Error(posOf(c.P), "E_ARG",
		"%s argument %d must be a literal: 1.12.2 builds a command from text, so a value that only exists at runtime cannot be spliced into it",
		c.Name, i+1)
	return "", false
}

// positionalArgs renders arguments 1..n as one space-separated tail.
//
// Used by the builtins whose remaining arguments are plain command text:
// `effect @s speed 10 1 true` has four, and spelling out a signature for
// each command would be a table to maintain rather than a language feature.
func (l *Lowerer) positionalArgs(c syntax.Call, from int) (string, bool) {
	var parts []string
	for i := from; i < len(c.Args); i++ {
		s, ok := l.rawArg(c, i)
		if !ok {
			return "", false
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " "), true
}

// rawString reads a string-literal argument, reporting when it is missing
// or is not a literal.
//
// It used to return "" for anything it could not read, which turned a
// wrong argument into a command with an empty operand: valid syntax, no
// error, and nothing like what was written.
func (l *Lowerer) rawString(c syntax.Call, i int) string {
	if i < len(c.Args) {
		if s, ok := c.Args[i].(syntax.StringLit); ok {
			return s.Value
		}
	}
	l.diags.Error(posOf(c.P), "E_ARG", "%s needs a string literal as argument %d", c.Name, i+1)
	return ""
}

// targetArg reads a selector argument, or self.
func (l *Lowerer) targetArg(c syntax.Call, i int) string {
	if i < len(c.Args) {
		switch a := c.Args[i].(type) {
		case syntax.Ident:
			if strings.HasPrefix(a.Name, "@") {
				return a.Name
			}
		case syntax.SelfExpr:
			return "@s"
		}
	}
	l.diags.Error(posOf(c.P), "E_ARG", "%s needs a selector or self as argument %d", c.Name, i+1)
	return "@s"
}
