package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// `[add]` on a decode skips the reset command, so several decodes can
// contribute to one objective.
//
// The mode used to be a trailing `into` with no argument -- `decode a from b
// into` -- which is a preposition whose object is missing. It named a COST
// (one command less), and this language writes modifiers in brackets, so it is
// an attribute now.
func TestAddAttributeSkipsTheDecodeReset(t *testing.T) {
	src := "namespace t:ad\n" +
		"obj atk\n" +
		"nbt f = p\"tag.a\" : bits 4\n" +
		"[export]\nfn plain() { decode atk from @s.Inventory(0)[f] }\n" +
		"[export]\nfn accum() { [add] decode atk from @s.Inventory(0)[f] }\n"
	files := compile(t, src, optimize.O2)
	if got := files["t:ad/plain"]; !strings.Contains(got, "set @s atk 0") {
		t.Fatalf("a plain decode must reset first:\n%s", got)
	}
	if got := files["t:ad/accum"]; strings.Contains(got, "set @s atk 0") {
		t.Fatalf("[add] decode must NOT reset:\n%s", got)
	}
}

// The old spelling must say what replaced it, not leave a bare parse error.
func TestRemovedIntoSpellingExplainsItself(t *testing.T) {
	src := "namespace t:ad2\n" +
		"obj atk\n" +
		"nbt f = p\"tag.a\" : bits 4\n" +
		"[export]\nfn g() { decode atk from @s.Inventory(0)[f] into }\n"
	codes := compileDiags(src)
	for _, c := range codes {
		if strings.Contains(c, "[add]") {
			return
		}
	}
	t.Fatalf("the removed `into` spelling did not point at `[add]`: %v", codes)
}

// An attribute that no statement applies must be an error, never ignored:
// a silently dropped modifier is one the author believes they wrote.
func TestUnknownStatementAttributeIsRejected(t *testing.T) {
	src := "namespace t:ad3\n" +
		"obj atk\n" +
		"[export]\nfn g() { [wat] atk = 1 }\n"
	if codes := compileDiags(src); len(codes) == 0 {
		t.Fatal("an unknown statement attribute was accepted")
	}
}
