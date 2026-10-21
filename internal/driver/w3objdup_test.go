package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/driver"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/target"
)

// moduleDiags compiles several files as ONE module and returns every
// diagnostic, so a test can look for a code without failing on the rest.
func moduleDiags(t *testing.T, srcs ...string) []diag {
	t.Helper()
	files := make([]driver.Source, 0, len(srcs))
	for i, s := range srcs {
		files = append(files, driver.Source{Name: string(rune('A'+i)) + ".ggm", Text: s})
	}
	_, ds := driver.CompileFiles(files, driver.Options{Target: target.V1_12_2, Level: optimize.O2})
	var out []diag
	for _, d := range ds {
		out = append(out, diag{code: d.Code, warning: d.Severity != 0})
	}
	return out
}

// moduleMsg returns the text of the first diagnostic with the given code.
func moduleMsg(t *testing.T, code string, srcs ...string) string {
	t.Helper()
	files := make([]driver.Source, 0, len(srcs))
	for i, s := range srcs {
		files = append(files, driver.Source{Name: string(rune('A'+i)) + ".ggm", Text: s})
	}
	_, ds := driver.CompileFiles(files, driver.Options{Target: target.V1_12_2, Level: optimize.O2})
	for _, d := range ds {
		if d.Code == code {
			return d.Msg
		}
	}
	return ""
}

// Two files naming the same objective is LEGAL while they agree.
//
// A module that names its objectives in each file that uses them is
// stating one fact several times, and the reference data pack does exactly
// that -- the port corpus declares arg1 in 43 files and Ap.Amount in 6.
//
// It is a contradiction when the criteria differ, because the criteria is
// what the objective COUNTS. Reporting the repetition would break every
// port in the corpus, so the condition is the DISAGREEMENT, not the
// repetition.
func TestSameObjectiveInTwoFilesIsLegalWhenTheyAgree(t *testing.T) {
	ds := moduleDiags(t,
		"namespace t:x\nobj Score stat.kills\n[export]\nfn a() { Score = 1 }\n",
		"namespace t:x\nobj Score stat.kills\n[export]\nfn b() { Score = 2 }\n",
	)
	if hasDiag(ds, "E_OBJECTIVE_DUP") {
		t.Fatalf("the same objective declared in two files was reported: %v", ds)
	}
}

// Two files naming the same objective with DIFFERENT criteria is a
// contradiction: the criteria decides what the objective counts, so only
// one of the two can be right.
func TestSameObjectiveWithDifferentCriteriaIsAnError(t *testing.T) {
	ds := moduleDiags(t,
		"namespace t:x\nobj Score stat.kills\n[export]\nfn a() { Score = 1 }\n",
		"namespace t:x\nobj Score stat.deaths\n[export]\nfn b() { Score = 2 }\n",
	)
	if !hasDiag(ds, "E_OBJECTIVE_DUP") {
		t.Fatalf("want E_OBJECTIVE_DUP, got %v", ds)
	}
}

// The message has to name the field that actually differs.
//
// A fixed wording that always talked about the criteria printed the same
// criteria twice when only the DISPLAY name was different, which reads as
// a mistake in the message rather than in the source -- and the author has
// to work out which field actually moved.
func TestConflictMessageNamesTheFieldThatDiffers(t *testing.T) {
	msg := moduleMsg(t, "E_OBJECTIVE_DUP",
		"namespace t:x\nobj Score stat.kills \"kills\"\n[export]\nfn a() { Score = 1 }\n",
		"namespace t:x\nobj Score stat.kills \"deaths\"\n[export]\nfn b() { Score = 2 }\n",
	)
	if msg == "" {
		t.Fatal("no E_OBJECTIVE_DUP was reported")
	}
	if !strings.Contains(msg, "display") {
		t.Fatalf("the message does not name the display name, which is what differs: %s", msg)
	}
	if strings.Contains(msg, "different criteria") {
		t.Fatalf("the message blames the criteria, which agrees here: %s", msg)
	}
}
