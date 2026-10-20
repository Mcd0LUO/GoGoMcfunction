package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/CelesteaMcfunction/internal/driver"
	"github.com/Mcd0LUO/CelesteaMcfunction/internal/optimize"
	"github.com/Mcd0LUO/CelesteaMcfunction/internal/target"
)

type diag struct {
	code    string
	warning bool
}

func diagsOf(src string, opt driver.Options) []diag {
	if opt.Target.Name == "" {
		opt.Target = target.V1_12_2
	}
	if opt.Level == 0 {
		opt.Level = optimize.O1
	}
	_, ds := driver.Compile(src, "test.cmcf", opt)
	var out []diag
	for _, d := range ds {
		out = append(out, diag{code: d.Code, warning: d.Severity != 0})
	}
	return out
}

func hasDiag(ds []diag, code string) bool {
	for _, d := range ds {
		if d.code == code {
			return true
		}
	}
	return false
}

// A call to a name that is not declared anywhere is not an error in
// 1.12.2: the function command is valid, the file loads, and the call
// does nothing at run time. So a mistyped name buys a silently dead code
// path, which is precisely the kind of defect only a whole-program view
// can see.
func TestCallToUndeclaredFunctionIsAnError(t *testing.T) {
	src := "namespace t:c\nfn entry() {\n  missing()\n  also_missing(1)\n}\n"
	ds := diagsOf(src, driver.Options{})
	if n := countCode(ds, "E_UNKNOWN_CALLEE"); n != 2 {
		t.Fatalf("got %d E_UNKNOWN_CALLEE, want 2: %v", n, ds)
	}
}

func countCode(ds []diag, code string) int {
	n := 0
	for _, d := range ds {
		if d.code == code {
			n++
		}
	}
	return n
}

// The builtins the grammar accepts but this build does not implement
// deserve their own message: "unknown function abs" would send the
// reader looking for a declaration that is not supposed to exist.
func TestUnimplementedBuiltinSaysSo(t *testing.T) {
	for _, call := range []string{"abs(x)", "clamp(x, 1, 2)", "count<@e>"} {
		src := "namespace t:c\nextern var a0: int @ \"a0\"\nfn f(x: int) -> int {\n  " + call + "\n}\n"
		ds := diagsOf(src, driver.Options{})
		if !hasDiag(ds, "E_BUILTIN_UNIMPLEMENTED") {
			t.Errorf("%s: got %v, want E_BUILTIN_UNIMPLEMENTED", call, ds)
		}
	}
}

// abs IS implemented as the left side of a comparison, where it is
// rewritten algebraically and never becomes a call. Reporting it there
// would be a false positive on the most common use.
func TestAbsInComparisonIsNotReported(t *testing.T) {
	src := "namespace t:c\nextern var a0: int @ \"a0\"\nfn f(n: int) -> int {\n  if abs(n - a0) >= 1 { return 1 }\n  0\n}\n"
	ds := diagsOf(src, driver.Options{})
	if hasDiag(ds, "E_BUILTIN_UNIMPLEMENTED") || hasDiag(ds, "E_UNKNOWN_CALLEE") {
		t.Fatalf("false positive on abs in a comparison: %v", ds)
	}
	// Without this the test would also pass on a source that failed to
	// parse. The only diagnostic this input may produce is the
	// uncalled-function warning.
	for _, d := range ds {
		if !d.warning {
			t.Fatalf("source did not compile, so the absence proves nothing: %v", ds)
		}
	}
}

// A function nothing calls never runs, and nothing says so. A warning
// rather than an error: a command block or gameLoopFunction can reach it,
// and the compiler cannot see either.
func TestUncalledFunctionWarns(t *testing.T) {
	src := "namespace t:c\nfn used() -> int { 1 }\nfn unused() -> int { 2 }\nfn caller() -> int { used() }\n"
	ds := diagsOf(src, driver.Options{})
	if countCode(ds, "W_UNREACHABLE_FN") != 2 {
		t.Fatalf("got %v, want exactly unused and caller reported", ds)
	}
	for _, d := range ds {
		if d.code == "W_UNREACHABLE_FN" && !d.warning {
			t.Fatal("W_UNREACHABLE_FN must be a warning, not an error")
		}
	}
}

// Entry points are never called from the module and must not be reported.
func TestEntryPointsAreNotReportedAsUncalled(t *testing.T) {
	src := "namespace t:c\non load { }\non tick { }\non every 5s { }\n"
	ds := diagsOf(src, driver.Options{})
	if hasDiag(ds, "W_UNREACHABLE_FN") {
		t.Fatalf("entry point reported as uncalled: %v", ds)
	}
	if len(ds) != 0 {
		t.Fatalf("expected a clean compile, got %v", ds)
	}
}

// Comments are off by default: the output is meant to be read as
// commands, and a header per file is noise until you are looking for one.
func TestCommentsAreOffByDefaultAndOptIn(t *testing.T) {
	src := "namespace t:c\nfn f() -> int { 1 }\n"
	plain, _ := driver.Compile(src, "test.cmcf", driver.Options{Target: target.V1_12_2, Level: optimize.O1})
	if strings.Contains(plain.Files["t:c/f"], "#") {
		t.Fatalf("a header appeared without -comments:\n%s", plain.Files["t:c/f"])
	}
	annotated, _ := driver.Compile(src, "test.cmcf", driver.Options{Target: target.V1_12_2, Level: optimize.O1, Comments: true})
	body := annotated.Files["t:c/f"]
	if !strings.Contains(body, "# t:c/f") || !strings.Contains(body, "test.cmcf:2") {
		t.Fatalf("header missing or wrong:\n%s", body)
	}
	// The commands themselves must be untouched.
	if strings.TrimSpace(stripComments(body)) != strings.TrimSpace(plain.Files["t:c/f"]) {
		t.Fatalf("comments changed the commands:\n%s", body)
	}
}

func stripComments(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
