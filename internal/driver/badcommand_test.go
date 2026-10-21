package driver

import (
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/target"
)

// E_BAD_COMMAND is the back end's self-check: every rendered line must be a
// command the target accepts.
//
// No source language construct is known to make the renderer emit a malformed
// line, so this could not be reached through a .ggm file, and it sat in the
// untested list for that reason. A check nobody can exercise is a check nobody
// knows works, so the wiring is tested by handing the validator the renderer's
// output directly.
func TestMalformedRenderedLineIsReported(t *testing.T) {
	bad := map[string]string{
		// A selector argument 1.12.2 does not know. A malformed command makes
		// the whole function file fail to load, silently, which is why this
		// check exists at all.
		"data/functions/t/x.mcfunction": "say @s[foo=bar]",
	}
	got := validateRendered(bad, target.V1_12_2)
	if len(got) != 1 {
		t.Fatalf("got %d diagnostics for a malformed line, want 1", len(got))
	}
	if got[0].Code != "E_BAD_COMMAND" {
		t.Fatalf("code = %s, want E_BAD_COMMAND", got[0].Code)
	}
}

// A valid line must produce nothing, or every build fails.
func TestValidRenderedLineIsSilent(t *testing.T) {
	good := map[string]string{
		"data/functions/t/x.mcfunction": "scoreboard players set @s t 1\n",
	}
	if got := validateRendered(good, target.V1_12_2); len(got) != 0 {
		t.Fatalf("a valid line was rejected: %v", got)
	}
}
