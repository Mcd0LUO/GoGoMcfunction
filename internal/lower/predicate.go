package lower

import "cmcf/internal/ir"

// negate returns the logical complement of a predicate.
//
// De Morgan plus distribution: NOT(∨ᵢ ∧ⱼ cᵢⱼ) = ∧ᵢ (∨ⱼ NOT cᵢⱼ), and the
// conjunction of disjunctions expands to the cross product of the per-clause
// choices. Predicates are a handful of alternatives at most, so this stays
// tiny.
//
// The complement of a point interval is TWO ranges (x < n and x > n), and
// 1.12.2 has no boolean OR, so it must become two alternatives. Returning a
// single range here produced `score_=0` guards in an earlier revision.
func negate(p ir.Predicate) ir.Predicate {
	if p.Exist != nil {
		ex := *p.Exist
		ex.Negate = !ex.Negate
		return ir.Predicate{Exist: &ex}
	}
	if len(p.Alts) == 0 {
		// NOT(true) is unsatisfiable: an empty conjunction never matches.
		return ir.Predicate{Alts: [][]ir.Cond{{}}}
	}
	var choices [][]ir.Cond
	for _, alt := range p.Alts {
		var opts []ir.Cond
		for _, c := range alt {
			opts = append(opts, complements(c)...)
		}
		if len(opts) == 0 {
			return ir.Predicate{Alts: [][]ir.Cond{{}}}
		}
		choices = append(choices, opts)
	}
	result := [][]ir.Cond{{}}
	for _, opts := range choices {
		var next [][]ir.Cond
		for _, acc := range result {
			for _, o := range opts {
				merged := append(append([]ir.Cond(nil), acc...), o)
				next = append(next, merged)
			}
		}
		result = next
	}
	return ir.Predicate{Alts: result}
}

const (
	int32Min = -2147483648
	int32Max = 2147483647
)

// complements returns the disjoint intervals whose union is exactly the
// complement of c.
func complements(c ir.Cond) []ir.Cond {
	switch {
	case c.HasMin && c.HasMax:
		var out []ir.Cond
		if c.Min > int32Min {
			out = append(out, ir.Cond{Obj: c.Obj, HasMax: true, Max: c.Min - 1})
		}
		if c.Max < int32Max {
			out = append(out, ir.Cond{Obj: c.Obj, HasMin: true, Min: c.Max + 1})
		}
		return out
	case c.HasMax:
		return []ir.Cond{{Obj: c.Obj, HasMin: true, Min: c.Max + 1}}
	case c.HasMin:
		return []ir.Cond{{Obj: c.Obj, HasMax: true, Max: c.Min - 1}}
	}
	return []ir.Cond{{Obj: c.Obj, HasMax: true, Max: -1}}
}

// withBudget folds `budget >= 1` into every alternative.
func withBudget(p ir.Predicate, budget string) ir.Predicate {
	bc := ir.Cond{Obj: budget, HasMin: true, Min: 1}
	if p.Exist != nil {
		ex := *p.Exist
		ex.Selector = appendSelectorCond(ex.Selector, bc)
		return ir.Predicate{Exist: &ex}
	}
	var out ir.Predicate
	for _, alt := range p.Alts {
		out.Alts = append(out.Alts, append(append([]ir.Cond(nil), alt...), bc))
	}
	return out
}

// appendSelectorCond inserts a raw condition into a rendered selector.
func appendSelectorCond(sel string, c ir.Cond) string {
	cond := scoreCond(c)
	if cond == "" {
		return sel
	}
	if len(sel) > 0 && sel[len(sel)-1] == ']' {
		return sel[:len(sel)-1] + "," + cond + "]"
	}
	return sel + "[" + cond + "]"
}

// scoreCond renders a single condition; the objective must be present, because
// `score_=0` is valid syntax that tests a different objective.
func scoreCond(c ir.Cond) string {
	switch {
	case c.HasMin && c.HasMax:
		return itoaJoin(c.Obj, c.Min, c.Max)
	case c.HasMin:
		return "score_" + c.Obj + "_min=" + itoa(c.Min)
	case c.HasMax:
		return "score_" + c.Obj + "=" + itoa(c.Max)
	}
	return ""
}
