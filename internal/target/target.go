// Package target is the single place where Minecraft-version facts live.
//
// Everything version-specific — how a comparison becomes a selector condition,
// which operators exist, what the recursion limit is — belongs here. Porting to
// another version means writing another value of this table, not editing the
// back end.
package target

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/ir"
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

// selectorArgs are the raw filter arguments 1.12.2 understands.
//
// The list is a whitelist because an unrecognised key is not ignored:
// the command fails with "Invalid selector argument", and inside a
// function file an unparseable command can stop the whole file from
// loading. That makes a typo here cost an entire function, silently.
var selectorArgs = map[string]bool{
	"r": true, "rm": true, "l": true, "lm": true,
	"x": true, "y": true, "z": true,
	"dx": true, "dy": true, "dz": true,
	"rx": true, "rxm": true, "ry": true, "rym": true,
	"c": true, "m": true, "team": true, "name": true, "type": true, "tag": true,
}

// SelectorArgOK reports whether one raw selector argument is legal.
//
// This exists to catch the mistake the whole compiler is built around:
// 1.12.2 has no comparison operators in a selector. A score is tested
// with `score_x=N` meaning x <= N and `score_x_min=N` meaning x >= N, so
// `score_x>=1` is not a narrower test, it is not a test at all -- the
// key is `score_x>` and no such argument exists.
func (t Target) SelectorArgOK(arg string) bool {
	if arg == "" {
		return true
	}
	eq := strings.Index(arg, "=")
	if eq <= 0 {
		// No '=' at all, or an empty key: a bare word is malformed.
		return false
	}
	key, value := arg[:eq], arg[eq+1:]
	if value == "" {
		return false
	}
	// An operator cannot appear on either side. `score_x==0` splits at
	// the first '=', leaving "=0" as the value, which is not a number;
	// checking only the key accepted it.
	if strings.ContainsAny(value, "=<>") {
		return false
	}
	if selectorArgs[key] {
		switch key {
		case "tag", "team", "name", "type", "m":
			// A leading '!' negates one of these.
			return strings.TrimPrefix(value, "!") != ""
		}
		return isNumber(value)
	}
	name, ok := strings.CutPrefix(key, "score_")
	if !ok {
		return false
	}
	name = strings.TrimSuffix(name, "_min")
	if name == "" || strings.ContainsAny(name, "=<>!") {
		return false
	}
	return isNumber(value)
}

// isNumber reports whether s is an integer or decimal with an optional
// sign. Selector values are never expressions.
func isNumber(s string) bool {
	s = strings.TrimPrefix(s, "-")
	if s == "" {
		return false
	}
	dot := false
	for _, c := range s {
		if c == '.' {
			if dot {
				return false
			}
			dot = true
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// FitObjective truncates a name to the version's objective limit.
//
// The limit counts CHARACTERS, not bytes. 1.12.2 stores the name as a Java
// String and truncates it by code unit, so `Count.残照花魄` is 10 characters
// and legal even though it is 18 bytes in UTF-8. Measuring bytes rejected
// every objective name containing Chinese, which is most of them in the
// reference data pack -- and the rejection message called them characters,
// so the number it printed was wrong too.
func (t Target) FitObjective(name string) string {
	if utf8.RuneCountInString(name) <= t.ObjectiveNameLimit {
		return name
	}
	r := []rune(name)
	return string(r[:t.ObjectiveNameLimit])
}
