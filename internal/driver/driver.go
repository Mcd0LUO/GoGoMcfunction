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
	"fmt"

	"cmcf/internal/analysis"
	"cmcf/internal/backend/mcfunction"
	"cmcf/internal/comptime"
	"cmcf/internal/diag"
	"cmcf/internal/ir"
	"cmcf/internal/lower"
	"cmcf/internal/optimize"
	"cmcf/internal/syntax"
	"cmcf/internal/target"
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
}

// Compile runs the full pipeline on one source file.
func Compile(src, filename string, opt Options) (*Output, []diag.Diagnostic) {
	if opt.Target.Name == "" {
		opt.Target = target.V1_12_2
	}
	bag := &diag.Bag{}

	file, err := syntax.Parse(src)
	if err != nil {
		bag.Error(diag.Pos{}, "E_PARSE", "%s: %v", filename, err)
		return nil, bag.Sorted()
	}

	res, lowerDiags := lower.Lower(file, opt.Target)
	for _, d := range lowerDiags {
		bag.Add(d)
	}
	prog := res.Program

	// comptime output becomes its own function in the pool
	consts := comptime.Eval(file.Comptime, bag)
	if len(consts) > 0 {
		fb := prog.NewFunc(file.Namespace + "/comptime")
		fb.Func().Exported = true
		fb.Emit(0, comptime.ToIR(consts)...)
	}

	prog = optimize.Run(prog, opt.Level)

	// Structural invariants are checked before rendering, because several of
	// these failures are silent miscompiles rather than crashes.
	for _, v := range analysis.Verify(prog, opt.Target.ObjectiveNameLimit) {
		bag.Error(diag.Pos{}, "E_IR_INVARIANT", "%s", v.String())
	}

	files, order := mcfunction.Render(prog, mcfunction.Options{Target: opt.Target})
	for p, body := range files {
		for _, line := range splitLines(body) {
			if err := mcfunction.Validate(line); err != nil {
				bag.Error(diag.Pos{}, "E_BAD_COMMAND", "%s: %s", p, err)
			}
		}
	}
	out := &Output{Files: files, Order: order, Skipped: optimize.Skipped(opt.Level)}
	out.NeedsConstInit = file.Namespace + "/init"
	return out, bag.Sorted()
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