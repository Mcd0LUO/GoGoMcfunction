package driver_test

import (
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/driver"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/target"
)

func diagCodes(src string) []string {
	_, diags := driver.Compile(src, "test.ggm", driver.Options{Target: target.V1_12_2, Level: optimize.O1})
	var out []string
	for _, d := range diags {
		out = append(out, d.Code)
	}
	return out
}

// 1.12.2 identifies a function with a MinecraftKey, whose constructor
// lower-cases the namespace and the path. Two names differing only in case
// are therefore one function -- one file, on a case-insensitive
// filesystem, and one call target everywhere.
//
// Nothing used to notice. The second declaration replaced the first and
// every call to either went to it, so
//
//	fn Foo() -> int { 1 }
//	fn foo() -> int { 2 }
//	fn call() -> int { Foo() + foo() }
//
// returned 4. It should have been an error: there is no program to emit.
func TestFunctionsThatDifferOnlyInCaseCollide(t *testing.T) {
	src := "namespace t:c\nfn Foo() -> int { 1 }\nfn foo() -> int { 2 }\n"
	if got := diagCodes(src); !contains(got, "E_FN_COLLISION") {
		t.Fatalf("no collision reported for Foo/foo, got %v", got)
	}
}

// A declaration landing on a name the compiler emits is a different
// mistake with a different fix, so it gets its own code.
//
// fn init() used to overwrite the function that declares the module's
// objectives and constant pool, leaving a program with no objectives at
// all -- every scoreboard command in it then reads 0.
func TestDeclarationCannotTakeAGeneratedName(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"init", "namespace t:i\nfn init() -> int { 1 }\n"},
		{"INIT", "namespace t:i\nfn INIT() -> int { 1 }\n"},
		// comptime is a keyword, so only the mixed-case spelling can
		// reach the generated name.
		{"comptime via case", "namespace t:i\ncomptime { score(\"#1\", \"math\", 1) }\nfn Comptime() -> int { 1 }\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := diagCodes(tc.src)
			if !contains(got, "E_FN_RESERVED") {
				t.Fatalf("no reserved-name error, got %v", got)
			}
		})
	}
}

func TestOtherCollisionsAreCaught(t *testing.T) {
	cases := []struct{ name, src string }{
		{"duplicate", "namespace t:a\nfn foo() -> int { 1 }\nfn foo() -> int { 2 }\n"},
		{"method vs field init", "namespace t:a\n[selector(@e)]\nclass C {\n  var hp: int = 1\n  fn init() { }\n}\n"},
		{"class name case", "namespace t:a\n[selector(@e)]\nclass C { var hp: int = 1 }\n[selector(@e)]\nclass c { var hp: int = 1 }\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := diagCodes(tc.src)
			if !contains(got, "E_FN_COLLISION") {
				t.Fatalf("no collision reported, got %v", got)
			}
		})
	}
}

// Two [load] functions no longer collide, and that is the point of the
// attribute: they are ordinary functions with the author's names, so a
// module can have as many as it likes and boot calls every one.
func TestTwoLoadFunctionsCoexist(t *testing.T) {
	src := "namespace t:a\n[load]\nfn alpha() { seed.a = 1 }\n[load]\nfn beta() { seed.b = 2 }\n"
	if got := diagCodes(src); contains(got, "E_FN_COLLISION") {
		t.Fatalf("two [load] functions collided: %v", got)
	}
}

// The check must not fire on names that are genuinely different.
func TestDistinctNamesAreAccepted(t *testing.T) {
	src := "namespace t:a\nfn foo() -> int { 1 }\nfn bar() -> int { 2 }\nfn FooBar() -> int { 3 }\n"
	for _, code := range diagCodes(src) {
		if code == "E_FN_COLLISION" || code == "E_FN_RESERVED" {
			t.Fatalf("false collision: %v", diagCodes(src))
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
