package symbols

// The index is tested as data, not through an editor or a pipe.
//
// Every case states a source, a position and the answer the editor will
// show. When the wording or the precedence changes, this table is where the
// change is visible -- which is the whole reason the language server does not
// own this logic.

import (
	"testing"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
)

func buildOne(t *testing.T, src string) *Index {
	t.Helper()
	f, err := syntax.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return Build(map[string]*syntax.File{"a.ggm": f})
}

const src = "namespace t:s\n" +
	"obj Hp\n" +
	"extern ATK: int = @s.atk\n" +
	"nbt flags = p\"tag.f\" : bits 4\n" +
	"const CAP = 100\n" +
	"fn helper(x: int) -> int { x * 2 }\n" +
	"[export]\n" +
	"fn run() {\n" +
	"    let a = helper(ATK)\n" +
	"    Hp = a + CAP + flags\n" +
	"}\n"

func TestResolvesEachKind(t *testing.T) {
	ix := buildOne(t, src)
	cases := []struct {
		name string
		line int
		col  int
		kind Kind
	}{
		{"objective use", 10, 5, KObj},
		{"extern use", 9, 20, KExtern},
		{"constant use", 10, 14, KConst},
		{"nbt field use", 10, 20, KNBT},
		{"function call", 9, 13, KFunc},
		{"local declaration", 9, 9, KLocal},
		{"local use", 10, 10, KLocal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, ok := ix.At("a.ggm", c.line, c.col)
			if !ok {
				t.Fatalf("nothing at %d:%d", c.line, c.col)
			}
			if s.Kind != c.kind {
				t.Fatalf("kind = %s, want %s (%s)", s.Kind, c.kind, s.Type)
			}
		})
	}
}

// A cursor anywhere inside a token resolves, not only at its first column.
func TestCursorAnywhereInToken(t *testing.T) {
	ix := buildOne(t, src)
	for _, col := range []int{13, 14, 15, 18} {
		s, ok := ix.At("a.ggm", 9, col)
		if !ok || s.Name != "helper" {
			t.Fatalf("column %d did not resolve to helper: %+v ok=%v", col, s, ok)
		}
	}
}

// An undeclared dotted name is the holder.objective shorthand, and hover has
// to say so rather than pretend it is a declaration.
func TestUndeclaredDottedNameIsShorthand(t *testing.T) {
	ix := buildOne(t, "namespace t:z\nobj X\n[export]\nfn f() { X = ghost.score }\n")
	s, ok := ix.At("a.ggm", 4, 14)
	if !ok {
		t.Fatal("an undeclared dotted name resolved to nothing")
	}
	if s.Kind != KShorthand {
		t.Fatalf("kind = %s, want %s", s.Kind, KShorthand)
	}
}

// Completion filters by prefix and never repeats a name.
func TestCompletionsAreUniqueAndFiltered(t *testing.T) {
	ix := buildOne(t, src)
	for _, s := range ix.Completions("h") {
		if s.Name != "helper" && s.Name != "Hp" {
			t.Fatalf("prefix h offered %q", s.Name)
		}
	}
	seen := map[string]bool{}
	for _, s := range ix.Completions("") {
		if seen[s.Name] {
			t.Fatalf("%q offered twice", s.Name)
		}
		seen[s.Name] = true
	}
}
