package driver_test

// sel: a name for a WHOLE selector.
//
//   sel Monsters = @e[tag=mob]
//   for e in entities<Monsters> { ... }
//
// The name is written where a selector is written, never inside the brackets,
// and the filters stay text all the way to the rendered command -- so the test
// that matters most reads the generated output.

import (
	"strings"
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/driver"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/target"
)

func compileOut(t *testing.T, srcs ...driver.Source) (*driver.Output, []string) {
	t.Helper()
	out, diags := driver.CompileFiles(srcs, driver.Options{Target: target.V1_12_2, Level: optimize.O1})
	var codes []string
	for _, d := range diags {
		codes = append(codes, d.Code)
	}
	return out, codes
}

func allText(out *driver.Output) string {
	if out == nil {
		return ""
	}
	var b strings.Builder
	for _, body := range out.Files {
		b.WriteString(body + "\n")
	}
	return b.String()
}

func hasDiagCode(codes []string, want string) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

// The whole selector is substituted at every place a selector is written.
func TestSelReplacesTheWholeSelector(t *testing.T) {
	src := "namespace t:s\nobj X\nsel Monsters = @e[tag=mob,r=12]\n[export]\n" +
		"fn f() {\n    for e in entities<Monsters> { X = 1 }\n    kill(Monsters)\n    if any<Monsters> { X = 2 }\n}\n"
	out, codes := compileOut(t, driver.Source{Name: "a.ggm", Text: src})
	if len(codes) != 0 {
		t.Fatalf("compilation reported %v", codes)
	}
	text := allText(out)
	if !strings.Contains(text, "execute @e[tag=mob,r=12] ~ ~ ~ function") {
		t.Fatalf("the for loop did not use the set:\n%s", text)
	}
	if !strings.Contains(text, "kill @e[tag=mob,r=12]") {
		t.Fatalf("the call did not use the set:\n%s", text)
	}
	if strings.Contains(text, "Monsters") {
		t.Fatalf("the name survived into the output:\n%s", text)
	}
}

// The table is module-wide, like obj and extern.
func TestSelIsModuleWide(t *testing.T) {
	a := "namespace t:s\nsel Monsters = @e[tag=mob]\nobj X\n"
	b := "namespace t:s\n[export]\nfn f() { kill(Monsters) }\n"
	out, codes := compileOut(t,
		driver.Source{Name: "a.ggm", Text: a},
		driver.Source{Name: "b.ggm", Text: b})
	if len(codes) != 0 {
		t.Fatalf("a set from another file was not seen: %v", codes)
	}
	if !strings.Contains(allText(out), "kill @e[tag=mob]") {
		t.Fatalf("the selector did not reach the command:\n%s", allText(out))
	}
}

// An unknown name is a positioned error, not a bare word in the output.
func TestSelUnknownNameIsReported(t *testing.T) {
	src := "namespace t:s\nobj X\n[export]\nfn f() { kill(Monst) }\n"
	_, diags := driver.Compile(src, "a.ggm", driver.Options{Target: target.V1_12_2, Level: optimize.O1})
	var found bool
	for _, d := range diags {
		if d.Code == "E_SEL_UNKNOWN" {
			found = true
			if d.Pos.Line != 4 {
				t.Fatalf("reported at line %d, want 4", d.Pos.Line)
			}
			if d.Pos.Col != 15 {
				t.Fatalf("reported at column %d, want the name", d.Pos.Col)
			}
		}
	}
	if !found {
		t.Fatalf("no E_SEL_UNKNOWN in %v", diags)
	}
}

// A declaration that is not a whole selector is refused where it is written,
// with the fix spelled out.
func TestSelMustBeAWholeSelector(t *testing.T) {
	src := "namespace t:s\nobj X\nsel B = tag=mob\n[export]\nfn f() { kill(B) }\n"
	_, diags := driver.Compile(src, "a.ggm", driver.Options{Target: target.V1_12_2, Level: optimize.O1})
	var found bool
	for _, d := range diags {
		if d.Code == "E_SEL_BAD" {
			found = true
			if d.Pos.Line != 3 {
				t.Fatalf("reported at line %d, want 3 (the declaration)", d.Pos.Line)
			}
			if !strings.Contains(d.Msg, "must start with @") {
				t.Fatalf("the message does not say what to write: %s", d.Msg)
			}
		}
		if d.Code == "E_BAD_COMMAND" {
			t.Fatalf("the same mistake was also reported by the back end: %s", d.Msg)
		}
	}
	if !found {
		t.Fatalf("no E_SEL_BAD in %v", diags)
	}
}

// A name inside the brackets is the old spelling and is refused with the fix.
func TestSelInsideBracketsIsRefused(t *testing.T) {
	src := "namespace t:s\nobj X\nsel Monsters = @e[tag=mob]\n[export]\nfn f() { kill(@e[Monsters]) }\n"
	_, diags := driver.Compile(src, "a.ggm", driver.Options{Target: target.V1_12_2, Level: optimize.O1})
	var found bool
	for _, d := range diags {
		if d.Code == "E_SEL_IN_BRACKETS" {
			found = true
			if !strings.Contains(d.Msg, "Monsters") {
				t.Fatalf("the message does not name the set: %s", d.Msg)
			}
		}
	}
	if !found {
		t.Fatalf("no E_SEL_IN_BRACKETS in %v", diags)
	}
}

// A repeated filter key is a warning: the compiler itself appends a tag filter
// to a class's selector, so duplicates already occur in working code.
func TestSelDuplicateKeyWarns(t *testing.T) {
	src := "namespace t:s\nobj X\nsel A = @e[tag=mob,tag=boss]\n[export]\nfn f() { kill(A) }\n"
	_, codes := compileOut(t, driver.Source{Name: "a.ggm", Text: src})
	if !hasDiagCode(codes, "W_SEL_DUP_KEY") {
		t.Fatalf("no duplicate warning: %v", codes)
	}
	for _, c := range codes {
		if strings.HasPrefix(c, "E_") {
			t.Fatalf("a duplicate key was treated as an error: %v", codes)
		}
	}
}

// A set is usable wherever a selector literal is, including a class attribute.
func TestSelInClassSelectorAttribute(t *testing.T) {
	src := "namespace t:s\nobj X\nsel Mob = @e[tag=mob]\n" +
		"[selector(Mob)]\nclass Monster {\n    var hp: int = 10\n    [spawn] fn onspawn() { hp = 10 }\n}\n"
	out, codes := compileOut(t, driver.Source{Name: "a.ggm", Text: src})
	for _, c := range codes {
		if strings.HasPrefix(c, "E_") {
			t.Fatalf("a set in a class attribute was refused: %v", codes)
		}
	}
	if !strings.Contains(allText(out), "execute @e[tag=mob") {
		t.Fatalf("the class selector did not use the set:\n%s", allText(out))
	}
}
