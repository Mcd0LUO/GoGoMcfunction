package target

import "testing"

// The mistake this whole compiler exists to prevent: 1.12.2 selectors
// have no comparison operators. `score_x>=1` is not a narrower test, it
// is a key the command has never heard of, and inside a function file an
// unparseable command can stop the whole file from loading.
func TestSelectorArgWhitelist(t *testing.T) {
	valid := []string{
		"score_x=0", "score_x_min=1",
		"score_C.hp=40", "score_C.hp_min=0",
		"tag=mob", "tag=!mob", "type=zombie", "type=!player",
		"r=5", "rm=1", "c=1", "m=creative", "team=red", "name=Steve",
		"x=1", "y=2", "z=3", "dx=0.5", "dy=1", "dz=0.5",
		"rx=90", "rxm=-90", "ry=0", "rym=0", "l=10", "lm=1",
	}
	for _, a := range valid {
		if !V1_12_2.SelectorArgOK(a) {
			t.Errorf("rejected valid argument %q", a)
		}
	}
	invalid := []string{
		"score_x>=1", "score_x<=1", "score_x==0", "score_x!=0",
		"score_x>1", "score_x<1",
		"nbt={}", "scores={x=1}", "foo=1", "word",
	}
	for _, a := range invalid {
		if V1_12_2.SelectorArgOK(a) {
			t.Errorf("accepted invalid argument %q", a)
		}
	}
}
