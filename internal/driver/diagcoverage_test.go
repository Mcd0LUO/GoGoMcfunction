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
		{
			"E_FOR_RANGE",
			"namespace t:d\nobj X\nobj i\n[export]\nfn f() { for k in 0..\"s\" { i = k } }\n",
		},
		// A class field is `var <name> [: type] [= init]`, not `nbt`. Both
		// cases below are about class field LOWERING, not the nbt codec, and
		// the first attempt at them used the wrong keyword and so tested
		// nothing.
		{
			"E_UNINIT_FIELD",
			"namespace t:d\nobj X\nclass C {\n  var a: int\n}\n[export]\nfn f() { X = 1 }\n",
		},
		{
			"E_FIELD_COLLISION",
			"namespace t:d\nobj X\nclass C {\n  var averylongname1 = 1\n  var averylongname2 = 2\n}\n[export]\nfn f() { X = 1 }\n",
		},
		{
			"E_STR_RUNTIME",
			"namespace t:d\nobj X\n[export]\nfn f() { X = 1 + str(5) }\n",
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
// Two others were nearly left here for a bad reason: E_UNINIT_FIELD and
// E_FIELD_COLLISION were listed as untriggerable because the first attempt at
// them wrote `nbt` where a class field is written `var`. They fired on the
// first try once the keyword was right, which is worth recording: "I could not
// trigger it" is a statement about the person trying, not about the code.
var untestedDiagnostics = []string{}
