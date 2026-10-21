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
nbt money = p"tag.dat.money" : bits 9

fn read1() {
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
nbt money = p"tag.dat.money" : bits 9
fn read2() {
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
nbt money = p"tag.dat.money" : bits 9
fn read3() {
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
fn read4() {
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
nbt money = p"tag.dat.money" : bits 31
fn read5() {
  decode money from @s.SelectedItem[money]
}
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
fn read6() {
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
nbt money = p"tag.dat.money" : bits 9

fn read7() {
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
			"execute @e[type=item] ~ ~ ~ function t:n/read7",
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

// Both enum forms are value dispatches, not bit ladders, and the two differ in
// how the value sits in the NBT: a number is attached straight to the key
// (`spec:1b`), while a name is an element of a list (`Tags:["残响"]`). Getting
// that wrong produces a pattern that never matches, which 1.12.2 reports as
// silence rather than an error.
func TestNBTEnumForms(t *testing.T) {
	files := compile(t, `namespace t:n
obj spec
obj quality
nbt spec    = p"tag.dat.spec" : enum 1..4
nbt quality = p"tag.Tags"     : enum { "残响":1, "蚀刻":2, "回响":3 }
fn read8() {
  decode spec from @s.SelectedItem[spec]
  decode quality from @s.SelectedItem[quality]
}
`, optimize.O0)

	var body []string
	for name, b := range files {
		if strings.HasSuffix(name, "/read8") {
			for _, line := range strings.Split(b, "\n") {
				// A rendered body ends with a newline; the empty tail is not a
				// command and would shift every index if left in.
				if strings.TrimSpace(line) != "" {
					body = append(body, line)
				}
			}
		}
	}
	want := []string{
		"scoreboard players set @s spec 0",
		"scoreboard players set @s spec 1 {SelectedItem:{tag:{dat:{spec:1b}}}}",
		"scoreboard players set @s spec 2 {SelectedItem:{tag:{dat:{spec:2b}}}}",
		"scoreboard players set @s spec 3 {SelectedItem:{tag:{dat:{spec:3b}}}}",
		"scoreboard players set @s spec 4 {SelectedItem:{tag:{dat:{spec:4b}}}}",
		"scoreboard players set @s quality 0",
		"scoreboard players set @s quality 1 {SelectedItem:{tag:{Tags:[\"残响\"]}}}",
		"scoreboard players set @s quality 2 {SelectedItem:{tag:{Tags:[\"蚀刻\"]}}}",
		"scoreboard players set @s quality 3 {SelectedItem:{tag:{Tags:[\"回响\"]}}}",
	}
	if len(body) != len(want) {
		t.Fatalf("want %d commands, got %d:\n%s", len(want), len(body), strings.Join(body, "\n"))
	}
	for i := range want {
		if body[i] != want[i] {
			t.Errorf("line %d:\n got  %s\n want %s", i, body[i], want[i])
		}
	}
}

// The enum shapes are checked against the engine, not just the text: a pattern
// that looks right but never matches is the failure mode here, and only running
// it can tell the two apart.
func TestNBTEnumMatchesOnRealRuntime(t *testing.T) {
	const src = `namespace t:n

obj spec
obj quality
nbt spec    = p"tag.dat.spec" : enum 1..4
nbt quality = p"tag.Tags"     : enum { "残响":1, "蚀刻":2 }

fn read9() {
  decode spec from @s.Item[spec]
  decode quality from @s.Item[quality]
}
`
	files := compile(t, src, optimize.O0)
	w, err := sim.NewWorld(files)
	if err != nil {
		t.Skipf("gomc runtime unavailable: %v", err)
	}
	defer w.Close()

	cases := []struct {
		nbt         string
		wantSpec    int
		wantQuality int
	}{
		{`{Item:{id:"stone",Count:1b,tag:{dat:{spec:3b}}}}`, 3, 0},
		{`{Item:{id:"stone",Count:1b,tag:{Tags:["残响"]}}}`, 0, 1},
		{`{Item:{id:"stone",Count:1b,tag:{Tags:["蚀刻"],dat:{spec:1b}}}}`, 1, 2},
		// Neither marker is present: both stay at the reset value, which is how
		// the data pack detects "this item carries nothing".
		{`{Item:{id:"stone",Count:1b,tag:{}}}`, 0, 0},
	}
	for _, tc := range cases {
		st, err := w.Run([]string{
			"function t:n/init",
			"kill @e[type=item]",
			"summon item 0 4 0 " + tc.nbt,
		}, []string{
			"execute @e[type=item] ~ ~ ~ function t:n/read9",
			"scoreboard players operation R spec = @e[type=item] spec",
			"scoreboard players operation R2 quality = @e[type=item] quality",
		})
		if err != nil {
			t.Fatalf("%s: run: %v", tc.nbt, err)
		}
		if got := st.Get("spec", "R"); got != tc.wantSpec {
			t.Errorf("%s: spec = %d, want %d", tc.nbt, got, tc.wantSpec)
		}
		if got := st.Get("quality", "R2"); got != tc.wantQuality {
			t.Errorf("%s: quality = %d, want %d", tc.nbt, got, tc.wantQuality)
		}
	}
}

// A match test is a pattern comparison, not a key-existence check: the data
// pack writes `{Item:{tag:{dat:{qlt:0b}}}` and expects a specific value, so
// qlt:1b in the item must NOT match it.
//
// 1.12.2 cannot put NBT in a selector, so the test has to go through a marker
// tag. Only running it shows whether the marker is written when it should be,
// and -- just as important -- whether a marker left by an earlier evaluation
// makes a later test pass on stale evidence.
func TestNBTMatchOnRealRuntime(t *testing.T) {
	const src = `namespace t:n

obj out

fn read10() {
  out = 0
  if @s.Item matches n"{Item:{tag:{dat:{qlt:0b}}}}" {
    out = 1
  }
  if @s.Item matches n"{Item:{tag:{Tags:[\"残响\"]}}}" {
    out += 2
  }
}
`
	files := compile(t, src, optimize.O0)
	w, err := sim.NewWorld(files)
	if err != nil {
		t.Skipf("gomc runtime unavailable: %v", err)
	}
	defer w.Close()

	cases := []struct {
		nbt  string
		want int
	}{
		// qlt:0b matches the first pattern.
		{`{Item:{id:"stone",Count:1b,tag:{dat:{qlt:0b}}}}`, 1},
		// qlt:1b is the same key with a different value: a key-existence test
		// would wrongly accept it.
		{`{Item:{id:"stone",Count:1b,tag:{dat:{qlt:1b}}}}`, 0},
		{`{Item:{id:"stone",Count:1b,tag:{Tags:["残响"]}}}`, 2},
		{`{Item:{id:"stone",Count:1b,tag:{Tags:["蚀刻"]}}}`, 0},
		{`{Item:{id:"stone",Count:1b,tag:{Tags:["残响"],dat:{qlt:0b}}}}`, 3},
		{`{Item:{id:"stone",Count:1b,tag:{}}}`, 0},
	}
	for _, tc := range cases {
		st, err := w.Run([]string{
			"function t:n/init",
			"kill @e[type=item]",
			"summon item 0 4 0 " + tc.nbt,
		}, []string{
			"execute @e[type=item] ~ ~ ~ function t:n/read10",
			"scoreboard players operation R out = @e[type=item] out",
		})
		if err != nil {
			t.Fatalf("%s: run: %v", tc.nbt, err)
		}
		if got := st.Get("out", "R"); got != tc.want {
			t.Errorf("%s:\n out = %d, want %d", tc.nbt, got, tc.want)
		}
	}
}

// Two match tests in one function must not read each other's marker. Without
// per-site numbering the second probe would find the first one's tag still set
// and report a match that never happened.
func TestNBTMatchProbesAreDistinct(t *testing.T) {
	files := compile(t, `namespace t:n
obj out
fn read11() {
  out = 0
  if @s.Item matches n"{Item:{tag:{dat:{a:1b}}}}" { out = 1 }
  if @s.Item matches n"{Item:{tag:{dat:{b:1b}}}}" { out = 2 }
}
`, optimize.O0)

	seen := map[string]bool{}
	for _, body := range files {
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, "tag @s add ggm.probe") {
				f := strings.Fields(line)
				seen[f[len(f)-2]] = true
			}
		}
	}
	if len(seen) != 2 {
		t.Fatalf("want two distinct marker tags, got %v", seen)
	}
}
