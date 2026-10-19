// Package target is the single place where Minecraft-version facts live.
//
// Everything version-specific — how a comparison becomes a selector condition,
// which operators exist, what the recursion limit is — belongs here. Porting to
// another version means writing another value of this table, not editing the
// back end.
package target

import (
	"fmt"

	"cmcf/internal/ir"
)

// Target describes a Minecraft version's function/command semantics.
type Target struct {
	Name string

	// MaxCommandChainLength is the gamerule default. Commands past it are
	// dropped silently, which is why it is modelled explicitly.
	MaxCommandChainLength int

	// ObjectiveNameLimit bounds objective identifiers.
	ObjectiveNameLimit int

	// HasFunctionTag is true from 1.13 (data pack tags). 1.12.2 instead uses
	// gamerule gameLoopFunction.
	HasFunctionTag bool

	// ScoreMin and ScoreMax bound a scoreboard value, inclusive.
	//
	// Every version so far stores a score as a signed 32-bit integer and
	// wraps silently on overflow. That makes the bound a correctness fact
	// rather than a range hint: a literal outside it has no encoding, and
	// emitting one produces a constant that reads back as a different
	// number with no error anywhere.
	ScoreMin int
	ScoreMax int
}

// FitsScore reports whether v can be stored in a scoreboard objective.
func (t Target) FitsScore(v int) bool {
	return v >= t.ScoreMin && v <= t.ScoreMax
}

// V1_12_2 is the shipped target.
var V1_12_2 = Target{
	Name:                  "1.12.2",
	MaxCommandChainLength: 65536,
	ObjectiveNameLimit:    16,
	HasFunctionTag:        false,
	ScoreMin:              -2147483648,
	ScoreMax:              2147483647,
}

// Condition renders a score condition as a selector fragment.
//
// This is the highest-value function in the compiler:
//
//	score_x=N       means  x <= N      (single upper bound)
//	score_x_min=N   means  x >= N      (single lower bound)
//	both together   means  A <= x <= B (closed interval)
//
// so equality must always emit two bounds, and inequality must always emit
// two alternatives. Getting this wrong never errors; it silently over-matches
// and is the single most common failure in hand-written 1.12.2 code.
func (t Target) Condition(obj string, c ir.Cond) string {
	switch {
	case c.HasMin && c.HasMax:
		return fmt.Sprintf("score_%s_min=%d,score_%s=%d", obj, c.Min, obj, c.Max)
	case c.HasMin:
		return fmt.Sprintf("score_%s_min=%d", obj, c.Min)
	case c.HasMax:
		return fmt.Sprintf("score_%s=%d", obj, c.Max)
	}
	return ""
}

// CondFor builds the canonical condition pair for a comparison operator.
// Conditions are expressed over the DIFFERENCE slot (lhs - rhs).
//
// The second return value is the complement. For `==` the complement is two
// disjoint ranges and cannot be a single selector, so callers must render it
// as two alternatives; the Exhaustive flag says whether that happened.
func CondFor(op string) (cons ir.Cond, complement ir.Cond, exhaustive bool, err error) {
	switch op {
	case "<": // d <= -1
		return ir.Cond{HasMax: true, Max: -1}, ir.Cond{HasMin: true, Min: 0}, true, nil
	case "<=": // d <= 0
		return ir.Cond{HasMax: true, Max: 0}, ir.Cond{HasMin: true, Min: 1}, true, nil
	case ">": // d >= 1
		return ir.Cond{HasMin: true, Min: 1}, ir.Cond{HasMax: true, Max: 0}, true, nil
	case ">=": // d >= 0
		return ir.Cond{HasMin: true, Min: 0}, ir.Cond{HasMax: true, Max: -1}, true, nil
	case "==": // d == 0 requires BOTH bounds
		// complement is `d <= -1 or d >= 1`, not representable as one Cond
		return ir.Cond{HasMin: true, Min: 0, HasMax: true, Max: 0}, ir.Cond{HasMax: true, Max: -1}, false, nil
	}
	return ir.Cond{}, ir.Cond{}, false, fmt.Errorf("unsupported comparison %q", op)
}

// FitObjective truncates a name to the version's objective limit.
func (t Target) FitObjective(name string) string {
	if len(name) <= t.ObjectiveNameLimit {
		return name
	}
	return name[:t.ObjectiveNameLimit]
}
