package lsp

// Hover, definition and completion, exercised over the wire.
//
// The point of pushing them through the protocol rather than calling the
// handlers is that the handlers' bugs are mostly about POSITIONS: an
// off-by-one in the line, or a column counted in the wrong unit. Those only
// appear when a real request is answered for a real file.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// ask sends a request and decodes its result into out.
func (s *session) ask(id int, method string, params, out any) {
	s.t.Helper()
	m := s.request(id, method, params)
	if m.Error != nil {
		s.t.Fatalf("%s failed: %s", method, m.Error.Message)
	}
	if out == nil {
		return
	}
	if err := json.Unmarshal(m.Result, out); err != nil {
		s.t.Fatalf("decode %s result: %v (raw %s)", method, err, string(m.Result))
	}
}

// fixture is one small module covering the declaration kinds the three
// features have to describe.
const fixture = "namespace t:h\n" +
	"obj Hp\n" +
	"extern ATK: int = @s.atk\n" +
	"const CAP = 100\n" +
	"fn helper(x: int) -> int { x * 2 }\n" +
	"[export]\n" +
	"fn run() {\n" +
	"    let a = helper(ATK)\n" +
	"    Hp = a + CAP\n" +
	"}\n"

// openFixture writes the module, starts a server and opens the file.
func openFixture(t *testing.T) (*session, string) {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"h.ggm": fixture})
	sess := newSession(t, dir)
	path := filepath.Join(dir, "h.ggm")
	sess.notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: uriFromPath(path), Text: fixture},
	})
	return sess, path
}

func pos(line, char int) Position { return Position{Line: uint32(line), Character: uint32(char)} }

// Hover on a call reports the callee, even when the cursor is mid-token.
func TestHoverOnACall(t *testing.T) {
	sess, path := openFixture(t)
	var h Hover
	sess.ask(10, "textDocument/hover", PositionParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(path)},
		Position:     pos(7, 15), // inside helper, not its first column
	}, &h)
	if !strings.Contains(h.Contents.Value, "fn helper") {
		t.Fatalf("hover did not describe the function: %q", h.Contents.Value)
	}
	if h.Range == nil {
		t.Fatal("hover returned no range, so the client cannot highlight the token")
	}
}

// Hover distinguishes an objective from an extern, which are different
// things an author has to keep straight.
func TestHoverSeparatesObjectiveFromExtern(t *testing.T) {
	sess, path := openFixture(t)
	var onObj, onExt Hover
	sess.ask(11, "textDocument/hover", PositionParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(path)},
		Position:     pos(8, 4), // Hp
	}, &onObj)
	sess.ask(12, "textDocument/hover", PositionParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(path)},
		Position:     pos(7, 21), // ATK
	}, &onExt)
	if !strings.Contains(onObj.Contents.Value, "obj Hp") {
		t.Fatalf("hover on an objective said: %q", onObj.Contents.Value)
	}
	if !strings.Contains(onExt.Contents.Value, "extern ATK") {
		t.Fatalf("hover on an extern said: %q", onExt.Contents.Value)
	}
}

// Definition jumps from a use to the declaration.
func TestDefinitionOnACall(t *testing.T) {
	sess, path := openFixture(t)
	var locs []Location
	sess.ask(13, "textDocument/definition", PositionParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(path)},
		Position:     pos(7, 15),
	}, &locs)
	if len(locs) == 0 {
		t.Fatal("definition returned nothing for a call to a declared function")
	}
	if locs[0].Range.Start.Line != 4 {
		t.Fatalf("definition line = %d, want 4 (0-based, the fn helper line)", locs[0].Range.Start.Line)
	}
}

// Completion filters by the identifier fragment under the cursor.
func TestCompletionFiltersByPrefix(t *testing.T) {
	sess, path := openFixture(t)
	var list CompletionList
	sess.ask(14, "textDocument/completion", PositionParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(path)},
		Position:     pos(7, 16), // inside "helper"
	}, &list)
	var labels []string
	for _, it := range list.Items {
		labels = append(labels, it.Label)
	}
	if len(labels) == 0 {
		t.Fatal("completion returned nothing inside an identifier")
	}
	for _, l := range labels {
		if !strings.HasPrefix(l, "helper") {
			t.Fatalf("completion offered %q for prefix \"helper\": %v", l, labels)
		}
	}
}

// An unknown position must answer null rather than fail, or the client shows
// an error every time the cursor sits on whitespace.
func TestQueriesOnWhitespaceReturnNull(t *testing.T) {
	sess, path := openFixture(t)
	var h any
	sess.ask(15, "textDocument/hover", PositionParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(path)},
		Position:     pos(8, 0), // leading indentation
	}, &h)
	if h != nil {
		t.Fatalf("hover on whitespace returned %v, want null", h)
	}
}

// The outline lists only the declarations in the requested file.
//
// The index is module-wide, which is what hover needs, so a naive outline
// would list another file's functions in this file's sidebar.
func TestDocumentSymbolsStayInTheFile(t *testing.T) {
	dir := t.TempDir()
	aSrc := "namespace t:o\nobj X\nfn alpha() { X = 1 }\n"
	writeFiles(t, dir, map[string]string{
		"a.ggm": aSrc,
		"b.ggm": "namespace t:o\nfn beta() { X = 2 }\n",
	})
	sess := newSession(t, dir)
	path := filepath.Join(dir, "a.ggm")
	sess.notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: uriFromPath(path), Text: aSrc},
	})
	var syms []SymbolInformation
	sess.ask(40, "textDocument/documentSymbol", DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(path)},
	}, &syms)
	if len(syms) == 0 {
		t.Fatal("the outline is empty")
	}
	var sawAlpha bool
	for _, s := range syms {
		if s.Name == "beta" {
			t.Fatalf("the outline of a.ggm listed b.ggm symbol %q", s.Name)
		}
		if s.Name == "alpha" {
			sawAlpha = true
		}
		if s.Kind == 0 {
			t.Fatalf("symbol %q has no kind", s.Name)
		}
	}
	if !sawAlpha {
		t.Fatalf("the outline did not list alpha: %+v", syms)
	}
}

// References finds every use of a symbol, matched by what it RESOLVES to
// rather than by text: two files in one module may share a short name.
func TestReferencesFindEveryUse(t *testing.T) {
	sess, path := openFixture(t)
	var locs []Location
	sess.ask(50, "textDocument/references", ReferenceParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(path)},
		Position:     pos(7, 15), // the call to helper
	}, &locs)
	if len(locs) == 0 {
		t.Fatal("references returned nothing for a called function")
	}
	for _, l := range locs {
		if l.Range.Start.Character > 200 {
			t.Fatalf("a location has an implausible character offset: %+v", l)
		}
	}
}

// Signature help names the call and the argument the cursor is on.
//
// The call is found by scanning the line, because the AST does not record
// where an argument list ends. The rule is strict on purpose: no unclosed
// paren means no answer, and a client showing nothing beats a client
// pointing at the wrong signature.
func TestSignatureHelpTracksTheActiveArgument(t *testing.T) {
	dir := t.TempDir()
	src := "namespace t:sig\nobj X\nfn pair(a: int, b: int) -> int { a + b }\n[export]\nfn run() { X = pair(1, 2) }\n"
	writeFiles(t, dir, map[string]string{"s.ggm": src})
	sess := newSession(t, dir)
	path := filepath.Join(dir, "s.ggm")
	sess.notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: uriFromPath(path), Text: src},
	})

	cases := []struct {
		char int
		arg  uint32
	}{
		{20, 0}, // the `1`, before the comma
		{23, 1}, // the `2`, after it
	}
	for _, c := range cases {
		var h SignatureHelp
		sess.ask(60, "textDocument/signatureHelp", PositionParams{
			TextDocument: TextDocumentIdentifier{URI: uriFromPath(path)},
			Position:     Position{Line: 4, Character: uint32(c.char)},
		}, &h)
		if len(h.Signatures) == 0 {
			t.Fatalf("no signature at char %d", c.char)
		}
		if !strings.Contains(h.Signatures[0].Label, "pair") {
			t.Fatalf("signature at char %d was %q", c.char, h.Signatures[0].Label)
		}
		if h.ActiveParameter != c.arg {
			t.Fatalf("at char %d active parameter = %d, want %d", c.char, h.ActiveParameter, c.arg)
		}
		if len(h.Signatures[0].Parameters) != 2 {
			t.Fatalf("signature listed %d parameters, want 2", len(h.Signatures[0].Parameters))
		}
	}
}

// Outside a call the answer is null, not a guess.
func TestSignatureHelpIsSilentOutsideACall(t *testing.T) {
	dir := t.TempDir()
	src := "namespace t:sig2\nobj X\n[export]\nfn run() { X = 1 }\n"
	writeFiles(t, dir, map[string]string{"s.ggm": src})
	sess := newSession(t, dir)
	path := filepath.Join(dir, "s.ggm")
	sess.notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: uriFromPath(path), Text: src},
	})
	var h any
	sess.ask(61, "textDocument/signatureHelp", PositionParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(path)},
		Position:     Position{Line: 3, Character: 10},
	}, &h)
	if h != nil {
		t.Fatalf("signature help answered outside a call: %v", h)
	}
}

// A SEMANTIC error must reach the editor, not only a syntax error.
//
// The lowerer builds positions from the AST, which carries a line and column
// but no filename. Every lowering diagnostic therefore arrived with an empty
// File, and the server -- which groups by file -- discarded all of them. The
// syntax-error test above did not catch it because E_PARSE is raised in the
// driver, which DID set the name.
func TestSemanticErrorReachesTheEditor(t *testing.T) {
	dir := t.TempDir()
	src := "namespace t:sem\nobj X\n[export]\nfn one() { X = missing() }\n"
	writeFiles(t, dir, map[string]string{"a.ggm": src})
	sess := newSession(t, dir)
	path := filepath.Join(dir, "a.ggm")
	sess.notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: uriFromPath(path), Text: src},
	})
	got := sess.diagnostics(uriFromPath(path))
	if len(got) == 0 {
		t.Fatal("a semantic error produced no diagnostic; lowering diagnostics " +
			"were probably discarded for having no filename")
	}
	var found bool
	for _, d := range got {
		if d.Code == "E_UNKNOWN_CALLEE" {
			found = true
			if d.Range.Start.Line != 3 {
				t.Fatalf("diagnostic line = %d, want 3 (0-based)", d.Range.Start.Line)
			}
		}
	}
	if !found {
		t.Fatalf("E_UNKNOWN_CALLEE was not among the published diagnostics: %+v", got)
	}
}

// A rename must reach every use, and must not reach a same-named stranger.
//
// The edits come from the recorded RESOLUTIONS, not from a text search. A
// text search would rename a local called `score` in every function of the
// module; this only renames the one the cursor resolved to.
func TestRenameIsScopedToTheSymbol(t *testing.T) {
	dir := t.TempDir()
	aSrc := "namespace t:r\nobj X\nfn score(v: int) -> int { v }\n[export]\nfn one() { X = score(1) }\n"
	bSrc := "namespace t:r\n[export]\nfn two() -> int { let score = 2  score }\n"
	writeFiles(t, dir, map[string]string{"a.ggm": aSrc, "b.ggm": bSrc})
	sess := newSession(t, dir)
	aPath := filepath.Join(dir, "a.ggm")
	sess.notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: uriFromPath(aPath), Text: aSrc},
	})

	var edit WorkspaceEdit
	sess.ask(70, "textDocument/rename", RenameParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(aPath)},
		Position:     Position{Line: 2, Character: 4}, // the declaration of score
		NewName:      "points",
	}, &edit)

	var total int
	for _, edits := range edit.Changes {
		total += len(edits)
	}
	if total < 2 {
		t.Fatalf("rename produced %d edits; the declaration and its call both need one", total)
	}
	bURI := uriFromPath(filepath.Join(dir, "b.ggm"))
	if edits, ok := edit.Changes[bURI]; ok {
		t.Fatalf("rename touched b.ggm, where `score` is an unrelated local: %+v", edits)
	}
	for _, edits := range edit.Changes {
		for _, e := range edits {
			if e.NewText != "points" {
				t.Fatalf("an edit carries %q", e.NewText)
			}
		}
	}
}

// A shorthand names a fake player rather than a declaration, so there is
// nothing to rename and the client must be told so.
func TestRenameRefusesAShorthand(t *testing.T) {
	dir := t.TempDir()
	src := "namespace t:r2\nobj X\n[export]\nfn f() { X = ghost.score }\n"
	writeFiles(t, dir, map[string]string{"c.ggm": src})
	sess := newSession(t, dir)
	path := filepath.Join(dir, "c.ggm")
	sess.notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: uriFromPath(path), Text: src},
	})
	var got any
	sess.ask(71, "textDocument/rename", RenameParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(path)},
		Position:     Position{Line: 3, Character: 14},
		NewName:      "other",
	}, &got)
	if got != nil {
		t.Fatalf("rename answered for a holder.objective shorthand: %v", got)
	}
}

// A quick fix for the mistake the compiler exists to catch.
//
// Writing to an objective nobody declared is discarded by 1.12.2 without an
// error, so the compiler reports it -- and once reported, adding the
// declaration is mechanical, which is the work a quick fix should take.
func TestQuickFixDeclaresTheObjective(t *testing.T) {
	dir := t.TempDir()
	src := "namespace t:fix\nobj X\nnbt f = p\"tag.a\" : bits 4\n[export]\nfn g() { decode atk from @s.Inventory(0)[f] }\n"
	writeFiles(t, dir, map[string]string{"c.ggm": src})
	sess := newSession(t, dir)
	path := filepath.Join(dir, "c.ggm")
	sess.notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: uriFromPath(path), Text: src},
	})
	diags := sess.diagnostics(uriFromPath(path))
	var target *Diagnostic
	for i := range diags {
		if diags[i].Code == "E_OBJECTIVE_UNDECLARED" {
			target = &diags[i]
		}
	}
	if target == nil {
		t.Fatalf("no undeclared-objective diagnostic was published: %+v", diags)
	}

	var actions []CodeAction
	sess.ask(80, "textDocument/codeAction", CodeActionParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(path)},
		Range:        target.Range,
		Context:      CodeActionContext{Diagnostics: []Diagnostic{*target}},
	}, &actions)
	if len(actions) == 0 {
		t.Fatal("no quick fix offered for an undeclared objective")
	}
	if !strings.Contains(actions[0].Title, "obj atk") {
		t.Fatalf("the fix is titled %q, want it to name obj atk", actions[0].Title)
	}
	if actions[0].Edit == nil {
		t.Fatal("the fix carries no edit")
	}
	for _, edits := range actions[0].Edit.Changes {
		if len(edits) != 1 {
			t.Fatalf("the fix carries %d edits, want 1", len(edits))
		}
		if !strings.Contains(edits[0].NewText, "obj atk") {
			t.Fatalf("the inserted text is %q", edits[0].NewText)
		}
		// After the namespace line, so the file still opens with its namespace.
		if edits[0].Range.Start.Line != 1 {
			t.Fatalf("the insertion is at line %d, want 1 (after namespace)", edits[0].Range.Start.Line)
		}
	}
}
