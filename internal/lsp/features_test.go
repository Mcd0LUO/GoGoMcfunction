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
