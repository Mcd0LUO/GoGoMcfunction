package lower

import "github.com/Mcd0LUO/GoGoMcfunction/internal/ir"

// negate returns the logical complement of a predicate.
//
// The implementation lives in ir, because the back end and the optimiser
// need the same answer and three copies of it had already drifted apart:
// two of them emitted a bound of 2147483648, which no score can hold.
func negate(p ir.Predicate) ir.Predicate { return ir.Negate(p) }

// withBudget folds `budget >= 1` into every alternative.
//
// The budget is a score on @s, so it can only be folded into conditions that
// are also about @s. An existence test selects OTHER entities: appending
// `score_budget_min=1` to `@e[tag=x]` asks whether any of those entities has
// the budget, and they do not -- the budget lives on the caller. The loop then
// stops after one iteration, which is exactly what a `while any<@e[...]>`
// around a fan-out body did.
func withBudget(p ir.Predicate, budget string) ir.Predicate {
	bc := ir.Cond{Obj: budget, HasMin: true, Min: 1}
	if p.Exist != nil {
		return ir.Predicate{
			Exist: p.Exist,
			Alts:  [][]ir.Cond{{bc}},
		}
	}
	var out ir.Predicate
	for _, alt := range p.Alts {
		out.Alts = append(out.Alts, append(append([]ir.Cond(nil), alt...), bc))
	}
	return ir.Simplify(out)
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
