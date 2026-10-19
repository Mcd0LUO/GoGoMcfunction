// Package mc models Minecraft 1.12.2 commands.
//
// The single most important thing in this package is Selector: the 1.12.2
// scoreboard selector condition is an INTERVAL, not an equality.
//
//	score_X=N        matches  X <= N
//	score_X_min=N    matches  X >= N
//	score_X_min=A,score_X=B   matches  A <= X <= B
//
// Getting this wrong does not error. It silently matches more entities than
// intended. Every comparison in the language must go through this file.
package mc

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Base is a selector variable. All five exist in 1.12.2 (@r included).
type Base string

const (
	Self    Base = "@s"
	All     Base = "@a"
	Entity  Base = "@e"
	Nearest Base = "@p"
	Random  Base = "@r"
)

func (b Base) Valid() bool {
	switch b {
	case Self, All, Entity, Nearest, Random:
		return true
	}
	return false
}

// ScoreRange is one objective condition on a selector, stored as a closed
// interval over int32. HasMin/HasMax say which bounds are present; an absent
// bound is unbounded on that side.
type ScoreRange struct {
	HasMin bool
	Min    int
	HasMax bool
	Max    int
}

const (
	int32Min = math.MinInt32
	int32Max = math.MaxInt32
)

func checkInt32(name string, v int) error {
	if v < int32Min || v > int32Max {
		return fmt.Errorf("%s: %d is outside int32 range", name, v)
	}
	return nil
}

// Exact is `X == n`. It MUST emit both bounds.
// Emitting only score_X=n would mean X <= n, silently matching negatives too.
func Exact(n int) (ScoreRange, error) {
	if err := checkInt32("==", n); err != nil {
		return ScoreRange{}, err
	}
	return ScoreRange{HasMin: true, Min: n, HasMax: true, Max: n}, nil
}

// LessThan is `X < n`, which in range form is `X <= n-1` (single upper bound).
func LessThan(n int) (ScoreRange, error) {
	if err := checkInt32("<", n); err != nil {
		return ScoreRange{}, err
	}
	if n <= int32Min {
		return ScoreRange{}, fmt.Errorf("< %d is never true", n)
	}
	return ScoreRange{HasMax: true, Max: n - 1}, nil
}

// AtMost is `X <= n` (single upper bound).
func AtMost(n int) (ScoreRange, error) {
	if err := checkInt32("<=", n); err != nil {
		return ScoreRange{}, err
	}
	return ScoreRange{HasMax: true, Max: n}, nil
}

// GreaterThan is `X > n`, which is `X >= n+1` (single lower bound).
func GreaterThan(n int) (ScoreRange, error) {
	if err := checkInt32(">", n); err != nil {
		return ScoreRange{}, err
	}
	if n >= int32Max {
		return ScoreRange{}, fmt.Errorf("> %d is never true", n)
	}
	return ScoreRange{HasMin: true, Min: n + 1}, nil
}

// AtLeast is `X >= n` (single lower bound).
func AtLeast(n int) (ScoreRange, error) {
	if err := checkInt32(">=", n); err != nil {
		return ScoreRange{}, err
	}
	return ScoreRange{HasMin: true, Min: n}, nil
}

// Parts renders the condition fragments for this range, e.g.
// ["score_hp_min=0" "score_hp=0"] for exact equality with 0.
func (r ScoreRange) Parts(objective string) []string {
	var out []string
	if r.HasMin {
		out = append(out, fmt.Sprintf("score_%s_min=%d", objective, r.Min))
	}
	if r.HasMax {
		out = append(out, fmt.Sprintf("score_%s=%d", objective, r.Max))
	}
	return out
}

// Selector is a buildable entity selector.
type Selector struct {
	Base   Base
	Args   []string // raw conditions, e.g. "type=zombie", "tag=x", "c=1"
	Scores map[string]ScoreRange
}

func NewSelector(b Base) *Selector {
	return &Selector{Base: b, Scores: map[string]ScoreRange{}}
}

// SelfSelector is the common @s case.
func SelfSelector() *Selector { return NewSelector(Self) }

func (s *Selector) Arg(a string) *Selector {
	s.Args = append(s.Args, a)
	return s
}

func (s *Selector) Tag(tag string) *Selector { return s.Arg("tag=" + tag) }

// NotTag excludes entities carrying tag.
func (s *Selector) NotTag(tag string) *Selector { return s.Arg("tag=!" + tag) }

func (s *Selector) Type(t string) *Selector { return s.Arg("type=" + t) }

// One caps the match to a single entity. Required for /function ... if <sel>,
// which resolves the selector as getOneEntity and errors when ambiguous.
func (s *Selector) One() *Selector { return s.Arg("c=1") }

func (s *Selector) Score(objective string, r ScoreRange) *Selector {
	s.Scores[objective] = r
	return s
}

func (s *Selector) String() string {
	parts := append([]string(nil), s.Args...)
	keys := make([]string, 0, len(s.Scores))
	for k := range s.Scores {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, s.Scores[k].Parts(k)...)
	}
	if len(parts) == 0 {
		return string(s.Base)
	}
	return string(s.Base) + "[" + strings.Join(parts, ",") + "]"
}

// Clone returns a copy safe to modify.
func (s *Selector) Clone() *Selector {
	n := &Selector{Base: s.Base, Args: append([]string(nil), s.Args...), Scores: map[string]ScoreRange{}}
	for k, v := range s.Scores {
		n.Scores[k] = v
	}
	return n
}
