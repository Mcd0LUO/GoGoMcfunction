package ir

import "testing"

// The interval convention is the one thing in the compiler that is easy to
// get wrong and produces no error when it is: a condition matched too
// widely silently takes a branch it should not.
//
//	score_x=N       means  x <= N
//	score_x_min=N   means  x >= N
//	both together   means  those two bounds at once
//
// These tests lock the complement, which is where the convention bites.
func TestComplementOfPointIntervalIsTwoRanges(t *testing.T) {
	got := (Cond{Obj: "d", HasMin: true, Min: 0, HasMax: true, Max: 0}).complement()
	want := []Cond{
		{Obj: "d", HasMax: true, Max: -1},
		{Obj: "d", HasMin: true, Min: 1},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("complement of d == 0 = %v, want %v", got, want)
	}
}

func TestComplementOfUpperBound(t *testing.T) {
	got := (Cond{Obj: "d", HasMax: true, Max: -1}).complement()
	if len(got) != 1 || got[0] != (Cond{Obj: "d", HasMin: true, Min: 0}) {
		t.Fatalf("complement of d <= -1 = %v, want d >= 0", got)
	}
}

// The complement of d == max is d < max alone. Emitting the second bound
// would produce score_d_min=2147483648, which no score can hold; two of
// the three former copies of this function did exactly that.
func TestComplementDropsOutOfRangeBound(t *testing.T) {
	got := (Cond{Obj: "d", HasMin: true, Min: ConstMax, HasMax: true, Max: ConstMax}).complement()
	if len(got) != 1 {
		t.Fatalf("complement of d == max = %v, want a single range", got)
	}
	if got[0].HasMin {
		t.Fatalf("complement of d == max emitted a lower bound: %v", got[0])
	}
	if got[0].Max != ConstMax-1 {
		t.Fatalf("complement of d == max = %v, want d <= %d", got[0], ConstMax-1)
	}
}

// The shape that produced four guards where two suffice: the exit of a
// loop whose condition is |d| >= 1 and whose budget must also hold.
//
//	NOT((d <= -1 OR d >= 1) AND budget >= 1)
//	  = d == 0 OR budget <= 0
func TestNegateLoopExitCollapsesToTwoAlternatives(t *testing.T) {
	cond := Predicate{Alts: [][]Cond{
		{{Obj: "t", HasMax: true, Max: -1}, {Obj: "budget", HasMin: true, Min: 1}},
		{{Obj: "t", HasMin: true, Min: 1}, {Obj: "budget", HasMin: true, Min: 1}},
	}}
	got := Negate(cond)
	if len(got.Alts) != 2 {
		t.Fatalf("negation produced %d alternatives, want 2: %v", len(got.Alts), got.Alts)
	}
	want := map[string]bool{"budget <= 0": false, "t == 0": false}
	for _, alt := range got.Alts {
		switch {
		case len(alt) == 1 && alt[0] == (Cond{Obj: "budget", HasMax: true, Max: 0}):
			want["budget <= 0"] = true
		case len(alt) == 1 && alt[0] == (Cond{Obj: "t", HasMin: true, Min: 0, HasMax: true, Max: 0}):
			want["t == 0"] = true
		default:
			t.Errorf("unexpected alternative %v", alt)
		}
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("missing alternative %q in %v", k, got.Alts)
		}
	}
}

func TestSimplifyMergesRepeatedConditions(t *testing.T) {
	got := Simplify(Predicate{Alts: [][]Cond{{
		{Obj: "b", HasMax: true, Max: 0},
		{Obj: "b", HasMax: true, Max: 0},
	}}})
	if len(got.Alts) != 1 || len(got.Alts[0]) != 1 {
		t.Fatalf("repeated condition not merged: %v", got.Alts)
	}
}

func TestSimplifyIntersectsSameObjective(t *testing.T) {
	got := Simplify(Predicate{Alts: [][]Cond{{
		{Obj: "d", HasMin: true, Min: 0},
		{Obj: "d", HasMax: true, Max: 0},
	}}})
	want := Cond{Obj: "d", HasMin: true, Min: 0, HasMax: true, Max: 0}
	if len(got.Alts) != 1 || len(got.Alts[0]) != 1 || got.Alts[0][0] != want {
		t.Fatalf("d >= 0 AND d <= 0 = %v, want %v", got.Alts, want)
	}
}

func TestSimplifyDropsSubsumedAlternative(t *testing.T) {
	got := Simplify(Predicate{Alts: [][]Cond{
		{{Obj: "b", HasMax: true, Max: 0}},
		{{Obj: "b", HasMax: true, Max: 0}, {Obj: "d", HasMin: true, Min: 0}},
	}})
	if len(got.Alts) != 1 {
		t.Fatalf("subsumed alternative kept: %v", got.Alts)
	}
	if len(got.Alts[0]) != 1 || got.Alts[0][0].Obj != "b" {
		t.Fatalf("kept the stronger alternative instead of the weaker one: %v", got.Alts)
	}
}

// Negating an always-true predicate gives one alternative constraining
// nothing, which is how the renderer spells an unconditional call.
func TestNegateOfAlwaysTrue(t *testing.T) {
	got := Negate(Predicate{})
	if len(got.Alts) != 1 || len(got.Alts[0]) != 0 {
		t.Fatalf("negation of true = %v, want one empty alternative", got.Alts)
	}
}

func TestNegateTwiceReturnsTheOriginalSet(t *testing.T) {
	p := Predicate{Alts: [][]Cond{{
		{Obj: "d", HasMin: true, Min: -3, HasMax: true, Max: 5},
	}}}
	back := Negate(Negate(p))
	// NOT(NOT(d in [-3,5])) = d in [-3,5], possibly as several ranges that
	// together cover exactly that interval.
	merged, ok := mergeConds(back.Alts[0])
	if !ok || len(back.Alts) != 1 || len(merged) != 1 {
		t.Fatalf("double negation = %v, want a single interval", back.Alts)
	}
	if merged[0] != p.Alts[0][0] {
		t.Fatalf("double negation = %v, want %v", merged[0], p.Alts[0][0])
	}
}
