package lower

import (
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/diag"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/ir"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/target"
)

// lowerFiles lowers several files as ONE module, the way the driver does,
// and returns every diagnostic plus the module's objective table.
//
// The declaration tables are module-wide, so a test that only ever lowers
// one file can never see the bug that used to be there: a name declared
// in one file and used in another.
func lowerFiles(t *testing.T, srcs ...string) ([]diag.Diagnostic, map[string]bool) {
	t.Helper()
	files := make([]*syntax.File, 0, len(srcs))
	for _, src := range srcs {
		f, err := syntax.Parse(src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		files = append(files, f)
	}
	scope := CollectScope(files)
	var all []diag.Diagnostic
	for i, f := range files {
		_, diags := LowerWith(f, target.V1_12_2, "file"+string(rune('A'+i))+".ggm", nil, scope)
		all = append(all, diags...)
	}
	return all, scope.Objs
}

// hasCode reports whether any diagnostic carries the given code.
func hasCode(diags []diag.Diagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

// An undeclared dotted name is the holder.objective shorthand, which the
// language keeps on purpose. But the two readings are indistinguishable,
// and one of them is a mistyped declaration -- so the compiler says so
// rather than picking silently.
//
// Before this, the author wrote
//
//	obj X
//	fn f() { Ap.Defence = 1 }
//
// and got `scoreboard players set Ap Defence 1`: a score on a fake player
// named Ap, and an objective declared as `Defence`. The write landed on a
// score nobody reads and 1.12.2 said nothing.
func TestUndeclaredDottedNameIsReported(t *testing.T) {
	diags, _ := lowerFiles(t, "namespace t:x\nobj X\n[export]\nfn f() { Ap.Defence = 1 }\n")
	if !hasCode(diags, "W_DOTTED_UNDECLARED") {
		t.Fatalf("want W_DOTTED_UNDECLARED, got %v", diags)
	}
}

// The shorthand is legitimate, so a name whose HOLDER the module has
// already introduced must stay quiet. An extern bound to a named holder
// says what that holder is, and the dotted name is then unambiguous.
func TestKnownHolderIsNotReported(t *testing.T) {
	diags, _ := lowerFiles(t, "namespace t:x\nextern SEED: int = seed.math\n[export]\nfn f() { seed.t = 9 }\n")
	if hasCode(diags, "W_DOTTED_UNDECLARED") {
		t.Fatalf("the shorthand was reported for a known holder: %v", diags)
	}
}

// A DECLARED dotted name is never reported: there is no ambiguity at all.
func TestDeclaredDottedNameIsNotReported(t *testing.T) {
	diags, _ := lowerFiles(t, "namespace t:x\nobj Ap.Amount\n[export]\nfn f() { Ap.Amount = 1 }\n")
	if hasCode(diags, "W_DOTTED_UNDECLARED") {
		t.Fatalf("a declared objective was reported: %v", diags)
	}
}

// The report is once per NAME for the whole module. A name used three
// times is one decision, not three.
func TestAmbiguousNameIsReportedOnce(t *testing.T) {
	diags, _ := lowerFiles(t, "namespace t:x\n[export]\nfn f() { Ap.Defence = 1 }\n[export]\nfn g() { Ap.Defence = 2 }\n[export]\nfn h() { Ap.Defence = 3 }\n")
	n := 0
	for _, d := range diags {
		if d.Code == "W_DOTTED_UNDECLARED" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want 1 report, got %d: %v", n, diags)
	}
}

// An objective declared in one file is an objective in EVERY file of the
// module.
//
// The tables were per-file, and that made the meaning of a name depend
// on which file the author happened to be in: the same Ap.Amount was an
// objective in the file that declared it and a fake player's score in
// every other file. The second reading compiled cleanly, wrote to a
// score nobody reads, and 1.12.2 discarded it without an error.
func TestObjectiveIsVisibleAcrossFiles(t *testing.T) {
	diags, objs := lowerFiles(t,
		"namespace t:x\nobj Ap.Amount\n",
		"namespace t:x\n[export]\nfn f() { Ap.Amount = 1 }\n",
	)
	for _, d := range diags {
		if d.Severity == diag.Error {
			t.Fatalf("unexpected error: %v", d)
		}
	}
	if !objs["Ap.Amount"] {
		t.Fatal("the module does not know the objective declared in the other file")
	}
}

// The same for an extern: a binding declared in one file binds in every
// file of the module.
func TestExternIsVisibleAcrossFiles(t *testing.T) {
	diags, _ := lowerFiles(t,
		"namespace t:x\nextern SEED: int = seed.math\n",
		"namespace t:x\n[export]\nfn f() { SEED = 1 }\n",
	)
	for _, d := range diags {
		if d.Severity == diag.Error {
			t.Fatalf("unexpected error: %v", d)
		}
	}
}

// A return inside a fan-out body leaves the BODY, not the function that
// wrote the loop. Measured on the 1.12.2 interpreter:
//
//	for e in entities<@e[tag=m1]> { return 5 }   // body ends; caller continues
//	flag = 99                                   // <- still runs
//
//	while 1 == 1, budget 3 { return 7 }          // whole function ends
//	flag = 98                                   // <- does NOT run
//
// The same word, two meanings, decided by the statement the body sits
// in. The author who wrote it to leave a loop early gets a program that
// keeps going, silently.
func TestReturnInFanoutBodyIsReported(t *testing.T) {
	diags, _ := lowerFiles(t, "namespace t:x\n[export]\nfn f() {\n  for e in entities<@e[tag=x]> {\n    return 5\n  }\n}\n")
	if !hasCode(diags, "E_FANOUT_RETURN") {
		t.Fatalf("want E_FANOUT_RETURN, got %v", diags)
	}
}

// A return inside a while body leaves the WHOLE function, and that is
// what the language says it does -- the while body is not a function of
// its own. So it must NOT be reported.
func TestReturnInWhileBodyIsNotReported(t *testing.T) {
	diags, _ := lowerFiles(t, "namespace t:x\n[export]\nfn f() {\n  while 1 == 1, budget 3 {\n    return 7\n  }\n}\n")
	if hasCode(diags, "E_FANOUT_RETURN") {
		t.Fatalf("a return in a while body was reported: %v", diags)
	}
}

// A declared objective whose name contains a dot reads by its own name,
// and the code that computes a destination has to KNOW that.
//
// exprMentions used to answer false for any dotted name, before the
// declared case was checked. So the right operand of
//
//	Ap.Amount = X - Ap.Amount
//
// was believed not to mention the destination, the left operand was
// written into the destination first, and the destination was then
// subtracted from itself:
//
//	scoreboard players operation @s Ap.Amount = @s X
//	scoreboard players operation @s Ap.Amount -= @s Ap.Amount   <- always 0
//
// The result was always zero, with no error anywhere. A test that only
// checks the COMMAND TEXT would not have caught it: the commands are
// valid, they just compute the wrong thing.
func TestDeclaredDottedObjectiveIsSeenAsAReadOfItself(t *testing.T) {
	prog := mustLower(t, "namespace t:x\nobj Ap.Amount\nobj X\n[export]\nfn f() { Ap.Amount = X - Ap.Amount }\n")
	sawSelfSub := false
	for _, fn := range prog.Funcs {
		for _, b := range fn.Blocks {
			for _, op := range b.Ops {
				ar, ok := op.(ir.Arith)
				if !ok || ar.Op != "-=" {
					continue
				}
				if ar.Dst.Obj == "Ap.Amount" && ar.Src.Obj == "Ap.Amount" {
					sawSelfSub = true
				}
			}
		}
	}
	if sawSelfSub {
		t.Fatal("the objective was subtracted from itself: the result is always zero")
	}
}
