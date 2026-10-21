package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/driver"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/target"
)

// compileDiags compiles and returns the diagnostics without failing.
func compileDiags(src string) []string {
	_, diags := driver.Compile(src, "test.ggm", driver.Options{Target: target.V1_12_2, Level: optimize.O1})
	var out []string
	for _, d := range diags {
		out = append(out, d.Code+": "+d.Msg)
	}
	return out
}

func hasCode(diags []string, code string) bool {
	for _, d := range diags {
		if strings.HasPrefix(d, code+": ") {
			return true
		}
	}
	return false
}

// A score in 1.12.2 is a signed 32-bit integer that wraps silently.
// An out-of-range literal has no encoding at all, so accepting one emits
// a constant that reads back as a different number.
func TestLiteralRangeIsEnforced(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool // true = must be rejected
	}{
		{"max", "namespace t:e\nfn f() -> int { 2147483647 }\n", false},
		{"min", "namespace t:e\nfn f() -> int { -2147483648 }\n", false},
		{"zero", "namespace t:e\nfn f() -> int { 0 }\n", false},
		{"over max", "namespace t:e\nfn f() -> int { 2147483648 }\n", true},
		{"under min", "namespace t:e\nfn f() -> int { -2147483649 }\n", true},
		{"nested", "namespace t:e\nfn f(n: int) -> int { n + (2147483648) }\n", true},
		{"in call arg", "namespace t:e\nfn f() -> int { max(1, 2147483648) }\n", true},
		{"in comptime list", "namespace t:e\ncomptime { for n in [2147483648] { score(\"#x\", \"math\", n) } }\n", true},
		{"comptime folded", "namespace t:e\ncomptime { score(\"#x\", \"math\", 1073741824 * 4) }\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hasCode(compileDiags(tc.src), "E_INT_RANGE")
			if got != tc.want {
				t.Fatalf("E_INT_RANGE reported = %v, want %v\ndiags: %v", got, tc.want, compileDiags(tc.src))
			}
		})
	}
}

// The minimum score is written as a minus applied to out-of-range digits.
// Folding the sign is what keeps the boundary usable; checking the digits
// alone would reject it.
func TestMinIntIsAccepted(t *testing.T) {
	diags := compileDiags("namespace t:e\nfn f() -> int { -2147483648 }\n")
	if hasCode(diags, "E_INT_RANGE") {
		t.Fatalf("min int rejected: %v", diags)
	}
}
