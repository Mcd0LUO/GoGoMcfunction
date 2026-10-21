package driver_test

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
)

// initFile returns the generated init function, which is where objective
// declarations are emitted.
func initFile(t *testing.T, files map[string]string) string {
	t.Helper()
	for name, body := range files {
		if strings.HasSuffix(name, "/init") {
			return body
		}
	}
	t.Fatalf("no init function in %v", keysOf(files))
	return ""
}

// An `obj` declaration supplies the criteria and display name of the
// generated `scoreboard objectives add`. Without it the backend invents a
// label, which is fine for compiler-generated objectives but wrong for one
// the author named.
func TestObjDeclarationControlsTheGeneratedCommand(t *testing.T) {
	files := compile(t, `namespace t:o

obj counter "计数"
obj post_damage stat.damageDealt "造成伤害"

on every 1tick {
  counter = 1
  post_damage = 2
}
`, optimize.O2)

	init := initFile(t, files)
	for _, want := range []string{
		"scoreboard objectives add counter dummy 计数",
		"scoreboard objectives add post_damage stat.damageDealt 造成伤害",
	} {
		if !strings.Contains(init, want) {
			t.Errorf("init is missing %q\ngot:\n%s", want, init)
		}
	}
}

// Assigning to a declared objective must write that objective. Falling
// through to local allocation would emit l0 instead, and the declaration
// would create an objective no command ever touches -- a silent no-op, which
// is exactly the failure `obj` exists to make impossible.
func TestAssignmentToADeclaredObjectiveUsesItsName(t *testing.T) {
	files := compile(t, `namespace t:o
obj counter "计数"
on every 1tick { counter = 7 }
`, optimize.O2)

	for name, body := range files {
		if !strings.Contains(body, "scoreboard players set @s counter 7") {
			continue
		}
		if strings.Contains(body, "set @s l0 7") {
			t.Errorf("%s wrote a local instead of the declared objective:\n%s", name, body)
		}
		return
	}
	t.Errorf("no function set counter to 7; files: %v", keysOf(files))
}

// A name longer than the objective limit is truncated by 1.12.2, so two names
// sharing that prefix collapse into one objective. Rejecting it is the only
// way the author finds out.
func TestObjNameOverTheLimitIsAnError(t *testing.T) {
	msgs := compileDiags(`namespace t:o
obj this_name_is_way_too_long
on every 1tick { this_name_is_way_too_long = 1 }
`)
	if !hasCode(msgs, "E_OBJECTIVE_NAME") {
		t.Fatalf("want E_OBJECTIVE_NAME, got %v", msgs)
	}
}

func TestObjDeclaredTwiceIsAnError(t *testing.T) {
	msgs := compileDiags(`namespace t:o
obj counter
obj counter
on every 1tick { counter = 1 }
`)
	if !hasCode(msgs, "E_OBJECTIVE_DUP") {
		t.Fatalf("want E_OBJECTIVE_DUP, got %v", msgs)
	}
}

// `obj` says the compiler creates it; `extern` says it already exists.
// Declaring both means the author holds two contradictory beliefs about the
// same objective, and only one of them can be true.
func TestObjAndExternConflict(t *testing.T) {
	msgs := compileDiags(`namespace t:o
obj arg1
extern arg1: int = @s.arg1
on every 1tick { arg1 = 1 }
`)
	if !hasCode(msgs, "E_OBJECTIVE_CONFLICT") {
		t.Fatalf("want E_OBJECTIVE_CONFLICT, got %v", msgs)
	}
}
