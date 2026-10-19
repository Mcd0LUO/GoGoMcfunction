package mc

import "testing"

// These tests exist to lock down the one invariant that silently breaks
// everything if it is wrong: 1.12.2 score_x conditions are intervals.
func TestScoreRangeEmission(t *testing.T) {
	cases := []struct {
		name string
		rng  func(int) (ScoreRange, error)
		n    int
		want []string
	}{
		{"exact", Exact, 5, []string{"score_hp_min=5", "score_hp=5"}},
		{"exact zero", Exact, 0, []string{"score_hp_min=0", "score_hp=0"}},
		{"less than", LessThan, 5, []string{"score_hp=4"}},
		{"at most", AtMost, 5, []string{"score_hp=5"}},
		{"greater than", GreaterThan, 5, []string{"score_hp_min=6"}},
		{"at least", AtLeast, 5, []string{"score_hp_min=5"}},
		{"negative ub", AtMost, -1, []string{"score_hp=-1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := c.rng(c.n)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := r.Parts("hp")
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}

func TestLessThanInt32MinIsRejected(t *testing.T) {
	if _, err := LessThan(-2147483648); err == nil {
		t.Fatal("expected error for `< INT32_MIN`")
	}
	if _, err := GreaterThan(2147483647); err == nil {
		t.Fatal("expected error for `> INT32_MAX`")
	}
}

func TestSelectorString(t *testing.T) {
	s := NewSelector(Entity).Type("zombie").Tag("mob.monster")
	if got, want := s.String(), "@e[type=zombie,tag=mob.monster]"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	// score conditions sort after raw args, and min comes before max
	r, _ := Exact(0)
	s2 := SelfSelector().Score("hp", r)
	if got, want := s2.String(), "@s[score_hp_min=0,score_hp=0]"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	if got := SelfSelector().String(); got != "@s" {
		t.Fatalf("bare selector got %q", got)
	}
}

func TestSelectorDeterministic(t *testing.T) {
	// Multiple objectives must serialize in a stable order for golden tests.
	a, _ := Exact(1)
	b, _ := AtLeast(2)
	s1 := SelfSelector().Score("zzz", a).Score("aaa", b)
	s2 := SelfSelector().Score("aaa", b).Score("zzz", a)
	if s1.String() != s2.String() {
		t.Fatalf("nondeterministic: %q vs %q", s1.String(), s2.String())
	}
}