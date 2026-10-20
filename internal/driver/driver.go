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
	// NeedsConstInit is the function that must run once on world load to
	// initialise the constant pool. 1.12.2 has no automatic load hook.
	NeedsConstInit string
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
			}
		}

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
	out.NeedsConstInit = srcFiles[0].Namespace + "/init"
	return out, bag.Sorted()
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
