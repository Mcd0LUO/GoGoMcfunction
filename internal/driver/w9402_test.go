package driver_test

import (
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// An objective name is limited in CHARACTERS, and the message must say so.
//
// 1.12.2 truncates a Java String by code unit. `Count.残照花魄` is 10
// characters and legal, but 18 bytes in UTF-8. This check used len() in three
// places; one of them -- the obj declaration -- was missed when the other two
// were fixed, so a legal name was still rejected and the number printed was
// the byte count, described as characters.
func TestObjectiveNameIsCountedInCharacters(t *testing.T) {
	src := "namespace t:c\n" +
		"obj Count.残照花魄\n" +
		"[export]\nfn f() { Count.残照花魄 = 1 }\n"
	if got := diagCodes(src); len(got) != 0 {
		t.Fatalf("a 10-character name was rejected: %v", got)
	}

	// And a name that really is too long is still rejected, with the
	// character count.
	long := "namespace t:c\nobj 一二三四五六七八九十一二三四五六七\n" +
		"[export]\nfn f() { 一二三四五六七八九十一二三四五六七 = 1 }\n"
	if got := diagCodes(long); !contains(got, "E_OBJECTIVE_NAME") {
		t.Fatalf("a 17-character name was accepted: %v", got)
	}
}

// A decode case value becomes a scoreboard operand, so it must fit a score.
//
// The general literal check walks for IntLit nodes, and a case value is a
// plain int on DecodeCase -- not an IntLit -- so 2147483648 reached the
// command unchanged and 1.12.2 wrapped it silently.
func TestDecodeCaseValueMustFitAScore(t *testing.T) {
	src := "namespace t:c\n" +
		"obj X\n" +
		"nbt f = p\"tag.dat.f\" : bits 2\n" +
		"[export]\nfn g() {\n" +
		"  decode X from @s.SelectedItem[f] {\n" +
		"    n\"{tag:{a:1b}}\" add 2147483648\n" +
		"  }\n" +
		"}\n"
	if got := diagCodes(src); !contains(got, "E_INT_RANGE") {
		t.Fatalf("an out-of-range decode value was accepted: %v", got)
	}
}

// A legal sentinel still compiles.
func TestDecodeSentinelCompiles(t *testing.T) {
	src := "namespace t:c\n" +
		"obj X\n" +
		"nbt f = p\"tag.dat.f\" : bits 2\n" +
		"[export]\nfn g() {\n" +
		"  decode X from @s.SelectedItem[f] {\n" +
		"    n\"{tag:{a:1b}}\" set -1\n" +
		"  }\n" +
		"}\n"
	files := compile(t, src, optimize.O2)
	if _, ok := files["t:c/g"]; !ok {
		t.Fatalf("a legal decode did not compile; files: %v", keysOf(files))
	}
}
