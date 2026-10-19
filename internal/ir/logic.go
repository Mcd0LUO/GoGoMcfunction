package ir

import "sort"

// This file holds the one implementation of predicate negation.
//
// It used to exist three times -- in lower, in the back end and in the
// optimiser -- and two of the three copies had already drifted: they
// emitted a complement bound of 2147483648, which no score can hold,
// where the third correctly dropped it. A single copy cannot disagree
// with itself.
//
// The semantics being encoded are the target's: a condition is a closed
// interval over an objective, and a point interval's complement is TWO
// ranges, which is why a negation can turn one guard into several.

// ConstMin and ConstMax bound a score. They live here because the
// complement below has to know when an interval runs off the end.
const (
	ConstMin = -2147483648
	ConstMax = 2147483647
)

// Negate returns the complement of a predicate in disjunctive normal form.
//
// De Morgan plus distribution: NOT(OR_i AND_j c_ij) = AND_i (OR_j NOT c_ij),
// and the conjunction of disjunctions expands to the cross product of the
// per-clause choices. The result is passed through Simplify, because that
// cross product repeats conditions.
func Negate(p Predicate) Predicate {
	if p.Exist != nil {
		ex := *p.Exist
		ex.Negate = !ex.Negate
		return Predicate{Exist: &ex}
	}
	if len(p.Alts) == 0 {
		// NOT(true) is unsatisfiable: an empty conjunction never matches.
		return Predicate{Alts: [][]Cond{{}}}
	}
	var choices [][]Cond
	for _, alt := range p.Alts {
		var opts []Cond
		for _, c := range alt {
			opts = append(opts, c.complement()...)
		}
		if len(opts) == 0 {
			return Predicate{Alts: [][]Cond{{}}}
		}
		choices = append(choices, opts)
	}
	result := [][]Cond{{}}
	for _, opts := range choices {
		var next [][]Cond
		for _, acc := range result {
			for _, o := range opts {
				merged := append(append([]Cond(nil), acc...), o)
				next = append(next, merged)
			}
		}
		result = next
	}
	return Simplify(Predicate{Alts: result})
}

// complement returns the disjoint intervals whose union is exactly the
// complement of c.
//
// The two bounds of a point interval are handled separately so that a
// bound running off the end of the representable range is dropped: the
// complement of "x == 2147483647" is x < 2147483647 alone, not that plus
// a range starting at 2147483648.
//
// A single-sided condition always yields a bound, even one no score can
// hold. That keeps every alternative non-empty, which matters because an
// empty alternative is how the renderer spells "no conditions at all".
func (c Cond) complement() []Cond {
	switch {
	case c.HasMin && c.HasMax:
		var out []Cond
		if c.Min > ConstMin {
			out = append(out, Cond{Obj: c.Obj, HasMax: true, Max: c.Min - 1})
		}
		if c.Max < ConstMax {
			out = append(out, Cond{Obj: c.Obj, HasMin: true, Min: c.Max + 1})
		}
		return out
	case c.HasMax:
		return []Cond{{Obj: c.Obj, HasMin: true, Min: c.Max + 1}}
	case c.HasMin:
		return []Cond{{Obj: c.Obj, HasMax: true, Max: c.Min - 1}}
	}
	return []Cond{{Obj: c.Obj, HasMax: true, Max: -1}}
}

// Simplify removes alternatives and conditions that cannot change what a
// predicate matches.
//
// Negation distributes into a cross product, and a cross product repeats
// conditions. The complement of "|d| >= 1 AND budget >= 1" comes out as
// four alternatives:
//
//	d >= 0 AND d <= 0          (the point d == 0, the real exit)
//	d >= 0 AND budget <= 0
//	budget <= 0 AND d <= 0
//	budget <= 0 AND budget <= 0
//
// The last states one condition twice, and it is weaker than the two in
// the middle, which it therefore covers. Two alternatives describe the
// same set, but four guards run on every iteration instead -- and a guard
// that cannot fire is not free in 1.12.2: it still costs a command and an
// error line.
func Simplify(p Predicate) Predicate {
	if p.Exist != nil || len(p.Alts) == 0 {
		return p
	}
	var alts [][]Cond
	for _, alt := range p.Alts {
		merged, ok := mergeConds(alt)
		if !ok {
			// The conditions contradict each other, so this alternative
			// can never match. Dropping it is sound; the guard below
			// keeps the predicate unchanged if that would drop them all.
			continue
		}
		alts = append(alts, merged)
	}
	if len(alts) == 0 {
		// Every alternative was impossible. Rather than invent a value
		// for "matches nothing" -- the encodings for it and for "matches
		// everything" would be easy to confuse -- leave the predicate
		// alone. Nothing in the compiler produces such a predicate today,
		// and an un-simplified predicate is still a correct one.
		return p
	}
	var kept [][]Cond
	for i, a := range alts {
		redundant := false
		for j, b := range alts {
			if i == j {
				continue
			}
			// b is weaker than a, so a adds nothing. Ties keep the first,
			// which drops exact duplicates.
			if subsumes(b, a) && (!subsumes(a, b) || j < i) {
				redundant = true
				break
			}
		}
		if !redundant {
			kept = append(kept, a)
		}
	}
	return Predicate{Alts: kept}
}

// mergeConds folds repeated conditions on one objective into a single
// interval, and reports false when they contradict each other.
func mergeConds(alt []Cond) ([]Cond, bool) {
	byObj := map[string]Cond{}
	var order []string
	for _, c := range alt {
		// A condition with neither bound constrains nothing, and the
		// renderer already drops it.
		if c.Obj == "" || (!c.HasMin && !c.HasMax) {
			continue
		}
		cur, seen := byObj[c.Obj]
		if !seen {
			byObj[c.Obj] = c
			order = append(order, c.Obj)
			continue
		}
		lo, hasMin := cur.Min, cur.HasMin
		if c.HasMin && (!hasMin || c.Min > lo) {
			lo, hasMin = c.Min, true
		}
		hi, hasMax := cur.Max, cur.HasMax
		if c.HasMax && (!hasMax || c.Max < hi) {
			hi, hasMax = c.Max, true
		}
		if hasMin && hasMax && lo > hi {
			return nil, false
		}
		byObj[c.Obj] = Cond{Obj: c.Obj, HasMin: hasMin, Min: lo, HasMax: hasMax, Max: hi}
	}
	out := make([]Cond, 0, len(order))
	for _, o := range order {
		out = append(out, byObj[o])
	}
	// Canonical order, so subsumption is a plain set comparison.
	sort.Slice(out, func(i, j int) bool { return out[i].Obj < out[j].Obj })
	return out, true
}

// subsumes reports whether a is at least as weak as b, so that b can never
// match where a does not.
//
// Conditions are intervals over one objective, so a is weaker exactly when
// each of a's conditions also appears in b.
func subsumes(a, b []Cond) bool {
	for _, ca := range a {
		found := false
		for _, cb := range b {
			if ca == cb {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
