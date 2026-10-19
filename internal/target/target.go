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

// CondAlts returns the predicate for a comparison over the DIFFERENCE
// slot (lhs - rhs), as a list of alternatives.
//
// Alternatives rather than one Condition because 1.12.2 selectors express
// conjunction but not disjunction: `d != 0` means `d <= -1 OR d >= 1`,
// which is two guarded calls. A single-Cond return could not express it,
// so != was silently unimplemented -- parsing fine, stopping the build
// with "unsupported comparison" only when someone actually used it.
//
// Only the true side is returned. The complement is derived from the
// Conditions by ir.Negate, which is the single implementation of that
// rule; computing it a second time here is how the two copies drifted
// apart before.
func CondAlts(op string) ([]ir.Cond, error) {
	switch op {
	case "<": // d <= -1
		return []ir.Cond{{HasMax: true, Max: -1}}, nil
	case "<=": // d <= 0
		return []ir.Cond{{HasMax: true, Max: 0}}, nil
	case ">": // d >= 1
		return []ir.Cond{{HasMin: true, Min: 1}}, nil
	case ">=": // d >= 0
		return []ir.Cond{{HasMin: true, Min: 0}}, nil
	case "==": // d == 0 needs BOTH bounds at once
		return []ir.Cond{{HasMin: true, Min: 0, HasMax: true, Max: 0}}, nil
	case "!=": // d <= -1 or d >= 1: two alternatives, no OR available
		return []ir.Cond{{HasMax: true, Max: -1}, {HasMin: true, Min: 1}}, nil
	}
	return nil, fmt.Errorf("unsupported comparison %q", op)
}

// FitObjective truncates a name to the version's objective limit.
func (t Target) FitObjective(name string) string {
	if len(name) <= t.ObjectiveNameLimit {
		return name
	}
	return name[:t.ObjectiveNameLimit]
}
