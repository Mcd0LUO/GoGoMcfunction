package optimize

// Level is an optimisation level, modelled on the C++ compilers.
//
// The difference between levels is NOT "how many passes run". It is the
// strength of the proof each pass requires before it rewrites anything.
// A pass that is safe at O2 may be unsound at O1 because a supporting analysis
// is not available yet — so the same transformation belongs to a higher level
// rather than being toggled independently.
//
// Levels are ordered: every pass enabled at O1 also runs at O2.
type Level int

const (
	// O0 disables the IR pipeline entirely. The output is a direct transcription
	// of the lowering, which is what you debug against when an optimisation
	// looks wrong.
	O0 Level = 0
	// O1 applies only transformations that are unconditionally sound: they
	// cannot change observable behaviour under any input.
	O1 Level = 1
	// O2 adds transformations requiring liveness, which is a whole-function
	// fixed point. These are sound but need more machinery to justify.
	O2 Level = 2
	// O3 adds transformations requiring value analysis across blocks. Reserved
	// for rewrites that are only sound once reaching definitions are known.
	O3 Level = 3
)

func (l Level) String() string {
	switch l {
	case O0:
		return "O0"
	case O1:
		return "O1"
	case O2:
		return "O2"
	case O3:
		return "O3"
	}
	return "?"
}

// ParseLevel accepts the CLI spellings -O0 .. -O3 and plain integers.
func ParseLevel(s string) (Level, bool) {
	switch s {
	case "0", "O0", "o0", "-O0":
		return O0, true
	case "1", "O1", "o1", "-O1":
		return O1, true
	case "2", "O2", "o2", "-O2":
		return O2, true
	case "3", "O3", "o3", "-O3":
		return O3, true
	}
	return O1, false
}

// PassInfo documents a pass and the proof it relies on.
type PassInfo struct {
	Pass Pass
	// MinLevel is the lowest level at which the pass is sound.
	MinLevel Level
	// Requires names the analysis or property that justifies the rewrite.
	Requires string
}

// Catalog is the ordered pass list with its justification. The pipeline for a
// level is the catalog filtered by MinLevel, so a pass can never be enabled
// without its supporting proof.
func Catalog() []PassInfo {
	return []PassInfo{
		{
			Pass:     SimplifyCFG{},
			MinLevel: O1,
			Requires: "structural only: merges blocks and inlines single-command arms, no value reasoning",
		},
		{
			Pass:     PruneUnreachable{},
			MinLevel: O1,
			Requires: "reachability, which is exact",
		},
		{
			Pass:     SelfAssign{},
			MinLevel: O1,
			Requires: "x = x is a no-op by definition",
		},
		{
			Pass:     DeadStore{},
			MinLevel: O2,
			Requires: "whole-function liveness fixed point; deleting a live write changes behaviour",
		},
		{
			Pass:     CoalesceTemps{},
			MinLevel: O2,
			Requires: "interference graph built from liveness; sharing a slot between overlapping live ranges corrupts values",
		},
		{
			// NOTE: this is NOT an optimisation. Emitting the constant pool is
			// required for the program to work at all: a literal such as `#4`
			// is a fake player on objective cmcf.c, and if nothing initialises
			// it the scoreboard reads 0. It runs at every level, including O0.
			Pass:     CollectConsts{},
			MinLevel: O0,
			Requires: "correctness: uninitialised constants read as 0",
		},
	}
}

// Pipeline returns the passes to run at a level, in catalog order.
func Pipeline(l Level) []Pass {
	var out []Pass
	for _, pi := range Catalog() {
		if pi.MinLevel <= l {
			out = append(out, pi.Pass)
		}
	}
	return out
}

// Skipped lists the passes a level deliberately omits, with the reason.
// The CLI prints this so a user who wonders why -O1 output is larger than
// -O2 gets an answer instead of a mystery.
func Skipped(l Level) []string {
	var out []string
	for _, pi := range Catalog() {
		if pi.MinLevel > l {
			out = append(out, pi.Pass.Name()+" (needs "+pi.MinLevel.String()+": "+pi.Requires+")")
		}
	}
	return out
}