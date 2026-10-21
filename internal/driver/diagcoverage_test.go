package driver_test

import (
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/driver"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/target"
)

// A diagnostic with no test is a claim nobody checks.
//
// This compiler exists to refuse to be silent about the mistakes 1.12.2
// swallows. A code that stops firing -- because the check was removed, a
// condition inverted, or two arguments swapped -- silently gives back the
// protection it was supposed to provide. Each case below names a code and a
// source that must produce it, so the failure lands here and not in someones
// data pack.
func TestEveryDiagnosticFires(t *testing.T) {
	cases := []struct {
		code string
		src  string
	}{
		{
			"E_ARITY",
			"namespace t:d\nobj X\nfn g(a: int) -> int { a }\n[export]\nfn f() { X = g(1, 2) }\n",
		},
		{
			"E_ASSIGN_TARGET",
			"namespace t:d\nobj X\n[export]\nfn f() { 1 = 2 }\n",
		},
		{
			"E_BROADCAST_VALUE",
			"namespace t:d\nobj X\n[export]\nfn f() { @a[tag=q] X = @a[tag=w] }\n",
		},
		{
			"E_COMPTIME_LIST",
			"namespace t:d\nobj X\ncomptime { for n in [X] { score(\"#a\", \"o\", n) } }\n[export]\nfn f() { X = 1 }\n",
		},
		{
			"E_COMPTIME_RANGE",
			"namespace t:d\nobj X\ncomptime { for n in X..10 { score(\"#a\", \"o\", n) } }\n[export]\nfn f() { X = 1 }\n",
		},
		{
			"E_FIELD_DUP",
			"namespace t:d\nobj X\nnbt f = p\"tag.a\" : bits 4\nnbt f = p\"tag.b\" : bits 4\n[export]\nfn g() { X = 1 }\n",
		},
		{
			"E_FOR_SELECTOR",
			"namespace t:d\nobj X\nobj i\n[export]\nfn f() { for e in X { i = 1 } }\n",
		},
		{
			"E_LOAD_PARAMS",
			"namespace t:d\nobj X\n[load]\nfn boot(a: int) { X = a }\n[export]\nfn f() { X = 1 }\n",
		},
		{
			"E_STMT",
			"namespace t:d\nobj X\nobj Y\n[export]\nfn f() { X Y = 1 }\n",
		},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			_, diags := driver.Compile(c.src, "t.ggm", driver.Options{Target: target.V1_12_2, Level: optimize.O2})
			for _, d := range diags {
				if d.Code == c.code {
					return
				}
			}
			var got []string
			for _, d := range diags {
				got = append(got, d.Code)
			}
			t.Fatalf("%s did not fire; got %v", c.code, got)
		})
	}
}

// Two codes were REMOVED rather than tested, because no source could reach
// them and a diagnostic that can never fire suggests protection the author
// never receives:
//
//	E_BROADCAST_SELECTOR  the parser builds BroadcastStmt only in its AT
//	                      branch, so the left side is always a selector
//	                      literal.
//	E_NO_BUDGET          the parser rejects `budget 0` with E_PARSE before the
//	                      lowerer sees it.
//
// Both lowerer sites panic instead of reporting now. A silent skip or drop
// there would be the quiet wrong answer this compiler exists to prevent, so if
// the parser ever stops enforcing its end, that has to be a crash.
//
// Five codes still have no test. Each is listed with the reason it could not
// be triggered, so the gap is documented rather than forgotten.
//
//	E_BAD_COMMAND         needs Render to emit a line ValidateFor rejects; no
//	                      source path to it was found.
//	E_FIELD_COLLISION     lives in class-field lowering; the class field
//	                      keyword was not identified while writing this.
//	E_FOR_RANGE           `for k in 0..X` lowers to a runtime loop, so this
//	                      check fires under a condition not found here.
//	E_STR_RUNTIME         an extern str assigned to an objective compiles; the
//	                      runtime-use path was not found.
//	E_UNINIT_FIELD        lives in class-field lowering, same blocker as above.
var untestedDiagnostics = []string{
	"E_BAD_COMMAND",
	"E_FIELD_COLLISION",
	"E_FOR_RANGE",
	"E_STR_RUNTIME",
	"E_UNINIT_FIELD",
}
