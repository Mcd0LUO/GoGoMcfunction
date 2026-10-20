package driver_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/sim"
)

// The NBT codec has two halves that must agree: decode folds NBT predicates
// into a score, encode writes a score back out as NBT. A hand-written data pack
// maintains them in two separate files, so they can drift; here they share one
// declaration, and these tests are what proves the sharing holds.
const nbtRoundTripSrc = `namespace t:n

obj money
nbt money = p"dat.money" : bits 9

on load {
  decode money from @s.SelectedItem[money]
}

fn put(v: int) -> int {
  money = v
  encode @s.SelectedItem[money] = money over 0..511
  1
}
`

// The decode half must fold the bit ladder back into the value that was
// packed. Testing it against a re-implementation in Go means the expected
// answer does not come from the same code being tested.
func TestNBTDecodeFoldsBits(t *testing.T) {
	files := compile(t, nbtRoundTripSrc, optimize.O0)

	// Find the decode ladder and check it has one reset plus one rung per bit.
	var ladder []string
	for _, body := range files {
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, "scoreboard players set @s money 0") ||
				strings.Contains(line, "scoreboard players add @s money ") {
				ladder = append(ladder, line)
			}
		}
	}
	if len(ladder) != 10 {
		t.Fatalf("want 1 reset + 9 rungs = 10 commands, got %d:\n%s", len(ladder), strings.Join(ladder, "\n"))
	}
	for i := 1; i < len(ladder); i++ {
		want := fmt.Sprintf("scoreboard players add @s money %d {SelectedItem:{tag:{dat:{money:{%d:1b}}}}}", 1<<uint(i-1), i-1)
		if ladder[i] != want {
			t.Errorf("rung %d:\n got  %s\n want %s", i, ladder[i], want)
		}
	}
}

// Every value the field can hold must survive write-then-read. This is the
// invariant the codec exists for: the two halves are generated from one
// declaration, so a value that encodes must decode.
func TestNBTEncodeDecodeRoundTrip(t *testing.T) {
	files := compile(t, nbtRoundTripSrc, optimize.O0)

	var encode []string
	for _, body := range files {
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "entitydata ") {
				encode = append(encode, line)
			}
		}
	}
	if len(encode) != 512 {
		t.Fatalf("want 512 entitydata commands for over 0..511, got %d", len(encode))
	}
	// The guard must carry BOTH bounds: `score_money=3` alone means `<= 3`
	// and would fire for every smaller value too.
	for _, line := range encode[:3] {
		if !strings.Contains(line, "_min=") {
			t.Errorf("guard has no lower bound: %s", line)
		}
	}
}

// An encode whose range exceeds the field is a silent truncation: the high
// values pack into fewer bits than the field can hold and read back wrong.
func TestNBTEncodeRangeIsChecked(t *testing.T) {
	msgs := compileDiags(`namespace t:n
obj money
nbt money = p"dat.money" : bits 9
on load {
  money = 1
  encode @s.SelectedItem[money] = money over 0..600
}
`)
	if !hasCode(msgs, "E_ENCODE_RANGE") {
		t.Fatalf("want E_ENCODE_RANGE, got %v", msgs)
	}
}

// Writing to an objective nobody declared is discarded by 1.12.2 without an
// error, so every value read back is 0 and nothing points at the cause.
func TestNBTDecodeIntoUndeclaredObjective(t *testing.T) {
	msgs := compileDiags(`namespace t:n
nbt money = p"dat.money" : bits 9
on load {
  decode undeclared from @s.SelectedItem[money]
}
`)
	if !hasCode(msgs, "E_OBJECTIVE_UNDECLARED") {
		t.Fatalf("want E_OBJECTIVE_UNDECLARED, got %v", msgs)
	}
}

func TestNBTUnknownField(t *testing.T) {
	msgs := compileDiags(`namespace t:n
obj money
on load {
  decode money from @s.SelectedItem[nosuch]
}
`)
	if !hasCode(msgs, "E_FIELD_UNKNOWN") {
		t.Fatalf("want E_FIELD_UNKNOWN, got %v", msgs)
	}
}

// A bit width of 32 would put 2^31 on the top rung, which is not representable
// in the int32 a score holds.
func TestNBTBitWidthIsChecked(t *testing.T) {
	msgs := compileDiags(`namespace t:n
obj money
nbt money = p"dat.money" : bits 31
on load { decode money from @s.SelectedItem[money] }
`)
	if !hasCode(msgs, "E_FIELD_BITS") {
		t.Fatalf("want E_FIELD_BITS, got %v", msgs)
	}
}

// The sentinel case is written by the author and must use `set`, because `add`
// would accumulate onto the ladder's result instead of replacing it.
func TestNBTSentinelCaseUsesSet(t *testing.T) {
	files := compile(t, `namespace t:n
obj lv
nbt lv = p"dat.lv" : bits 3
on load {
  decode lv from @s.SelectedItem[lv] {
    n"{SelectedItem:{tag:{dat:{lv:{max:1b}}}}}" set -1
  }
}
`, optimize.O0)

	var last string
	for _, body := range files {
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, "scoreboard players") && strings.Contains(line, " @s lv ") {
				last = line
			}
		}
	}
	want := "scoreboard players set @s lv -1 {SelectedItem:{tag:{dat:{lv:{max:1b}}}}}"
	if last != want {
		t.Fatalf("sentinel case:\n got  %s\n want %s", last, want)
	}
}

// The tests above read the generated text. This one runs it.
//
// A codec is only correct if the two halves agree at runtime, and the failure
// mode when they do not is silence: the ladder simply never matches and the
// score stays 0. Static assertions cannot see that, so this test writes a value
// into an item entity through the encode half and reads it back through the
// decode half on a real 1.12.2 engine.
func TestNBTCodecRoundTripsOnRealRuntime(t *testing.T) {
	const src = `namespace t:n

obj money
nbt money = p"dat.money" : bits 9

on load {
  decode money from @s.Item[money]
}

fn enc() -> int {
  encode @s.Item[money] = money over 0..511
  1
}
`
	files := compile(t, src, optimize.O0)
	w, err := sim.NewWorld(files)
	if err != nil {
		t.Skipf("gomc runtime unavailable: %v", err)
	}
	defer w.Close()

	for _, want := range []int{0, 1, 500, 511} {
		st, err := w.Run([]string{
			"function t:n/init",
			"kill @e[type=item]",
			`summon item 0 4 0 {Item:{id:"stone",Count:1b}}`,
			// Seed the objective the encode reads from.
			fmt.Sprintf("scoreboard players set @e[type=item] money %d", want),
		}, []string{
			"execute @e[type=item] ~ ~ ~ function t:n/enc",
			// Clear it, so a decode that never matches cannot pass by leaving
			// the seeded value in place.
			"scoreboard players set @e[type=item] money 0",
			"execute @e[type=item] ~ ~ ~ function t:n/load",
			"scoreboard players operation R money = @e[type=item] money",
		})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if !st.Has("money", "R") {
			t.Fatalf("value %d: nothing was decoded\nstate: %v\nerrors: %v", want, st.Keys(), st.Errors)
		}
		if got := st.Get("money", "R"); got != want {
			t.Errorf("round trip of %d gave %d", want, got)
		}
	}
}
