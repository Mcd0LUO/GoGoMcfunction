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
	return Build(map[string]*syntax.File{"a.ggm": f}, map[string]string{"a.ggm": src})
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
	// A fresh index is needed for this source, so buildOne is bypassed.
	src := "namespace t:z\nobj X\n[export]\nfn f() { X = ghost.score }\n"
	f, err := syntax.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	ix := Build(map[string]*syntax.File{"a.ggm": f}, map[string]string{"a.ggm": src})
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

// A declaration is found at its NAME, not at its keyword.
//
// The parser records where a declaration starts, which is `obj`, `fn` or
// `extern`. Hovering the name found nothing and go-to-definition jumped to the
// keyword -- both wrong the same way, and both invisible while the name happened
// to sit within a few columns of it.
func TestDeclarationsResolveAtTheirName(t *testing.T) {
	ix := buildOne(t, src)
	cases := []struct {
		name string
		line int
		col  int
	}{
		{"objective declaration", 2, 5},
		{"extern declaration", 3, 8},
		{"nbt declaration", 4, 5},
		{"constant declaration", 5, 7},
		{"function declaration", 6, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, ok := ix.At("a.ggm", c.line, c.col)
			if !ok {
				t.Fatalf("nothing at %d:%d", c.line, c.col)
			}
			if s.Pos.Line != c.line || s.Pos.Col != c.col {
				t.Fatalf("declaration reports %d:%d, want %d:%d", s.Pos.Line, s.Pos.Col, c.line, c.col)
			}
		})
	}
}

// A class method called from outside the class resolves.
//
// Two things were wrong and both were invisible while the call compiled: the
// method is indexed as Class/method but called as ns:Class/method, and bodies
// were walked while declarations were still being added, so a function could
// not see a class declared later in the same file. Declaration order must not
// decide whether the index can see a name.
const classSrc = "namespace t:c\n" +
	"obj X\n" +
	"class Hero {\n" +
	"    var hp: int = 20\n" +
	"    fn heal(n: int) -> int { hp + n }\n" +
	"}\n" +
	"[export]\n" +
	"fn run() {\n" +
	"    X = t:c/Hero/heal(3)\n" +
	"}\n"

func buildClass(t *testing.T) *Index {
	t.Helper()
	f, err := syntax.Parse(classSrc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return Build(map[string]*syntax.File{"a.ggm": f}, map[string]string{"a.ggm": classSrc})
}

func TestQualifiedMethodCallResolves(t *testing.T) {
	ix := buildClass(t)
	s, ok := ix.At("a.ggm", 9, 20)
	if !ok {
		t.Fatal("the call did not resolve")
	}
	if s.Kind != KMethod || s.Name != "Hero/heal" {
		t.Fatalf("resolved to %s %q", s.Kind, s.Name)
	}
	if defs := ix.Lookup("Hero/heal"); len(defs) != 1 {
		t.Fatalf("%d declarations for Hero/heal", len(defs))
	}
	if occ := ix.Occurrences(s); len(occ) != 1 {
		t.Fatalf("%d occurrences of the method call", len(occ))
	}
}

// A field is reachable by its bare name inside a class and by self. too.
//
// The parser builds a Field node for self.hp, whose BASE carries the position;
// the field's name is not an identifier node at all, so no traversal reported
// it and an editor had nothing to answer with.
const selfSrc = "namespace t:sf\n" +
	"obj X\n" +
	"class Hero {\n" +
	"    var hp: int = 20\n" +
	"    fn inner() -> int { hp }\n" +
	"    fn outer() -> int { inner() + self.hp }\n" +
	"}\n" +
	"[export]\n" +
	"fn run() { X = t:sf/Hero/outer() }\n"

func TestSelfFieldResolvesToTheClassField(t *testing.T) {
	f, err := syntax.Parse(selfSrc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ix := Build(map[string]*syntax.File{"a.ggm": f}, map[string]string{"a.ggm": selfSrc})

	for _, pos := range []struct{ line, col int }{{5, 25}, {6, 40}} {
		s, ok := ix.At("a.ggm", pos.line, pos.col)
		if !ok {
			t.Fatalf("nothing at %d:%d", pos.line, pos.col)
		}
		if s.Name != "Hero.hp" || s.Kind != KField {
			t.Fatalf("%d:%d resolved to %s %q", pos.line, pos.col, s.Kind, s.Name)
		}
	}

	sym, _ := ix.At("a.ggm", 6, 40)
	if occ := ix.Occurrences(sym); len(occ) != 2 {
		t.Fatalf("%d occurrences, want the bare use and the self use", len(occ))
	}
}
