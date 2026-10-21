package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// A 65-line lookup table from the reference data pack becomes one declaration.
//
// The pack writes `scoreboard players set @s return N {SelectedItem:{Count:Nb}}`
// sixty-four times, once per stack size. `enum 1..64` says the same thing, and
// the generated ladder is byte-identical to the hand-written one.
func TestEnumTableMatchesTheHandWrittenOne(t *testing.T) {
	src := "namespace rpg:api/get\n" +
		"extern RET: int = @s.return\n" +
		"nbt count = p\"Count\" : enum 1..64\n" +
		"[export]\nfn get_count() { decode RET from @s.SelectedItem[count] }\n"
	files := compile(t, src, optimize.O2)
	body := strings.TrimSpace(files["rpg:api/get/get_count"])
	got := strings.Split(body, "\n")
	if len(got) != 65 {
		t.Fatalf("want 1 reset + 64 cases = 65 lines, got %d", len(got))
	}
	if got[0] != "scoreboard players set @s return 0" {
		t.Fatalf("first line must reset:\n%s", got[0])
	}
	for i := 1; i <= 64; i++ {
		want := "scoreboard players set @s return " + itoaTest(i) + " {SelectedItem:{Count:" + itoaTest(i) + "b}}"
		if got[i] != want {
			t.Errorf("line %d:\n got  %s\n want %s", i, got[i], want)
		}
	}
}

// A bit ladder has one rung per bit and a reset, and the rung values are the
// powers of two -- so a 14-bit field is 15 lines exactly as the pack writes it.
func TestBitLadderMatchesTheHandWrittenOne(t *testing.T) {
	src := "namespace rpg:api/get\n" +
		"extern RET: int = @s.return\n" +
		"nbt money_bits = p\"tag.dat.money\" : bits 14\n" +
		"[export]\nfn get_money() { decode RET from @s.SelectedItem[money_bits] }\n"
	files := compile(t, src, optimize.O2)
	body := strings.TrimSpace(files["rpg:api/get/get_money"])
	got := strings.Split(body, "\n")
	if len(got) != 15 {
		t.Fatalf("want 1 reset + 14 rungs = 15 lines, got %d", len(got))
	}
	for k := 0; k < 14; k++ {
		want := "scoreboard players add @s return " + itoaTest(1<<uint(k)) + " {SelectedItem:{tag:{dat:{money:{" + itoaTest(k) + ":1b}}}}}"
		if got[k+1] != want {
			t.Errorf("rung %d:\n got  %s\n want %s", k, got[k+1], want)
		}
	}
}

// itoaTest renders a small non-negative int the way the emitter does.
func itoaTest(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}
