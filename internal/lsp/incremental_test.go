package lsp

// Incrementality, proved rather than assumed.
//
// The server caches one parse per file and skips a recompile when nothing
// changed. Both are easy to break silently -- a cache that never hits still
// produces correct answers, just slowly -- so the tests count real parses
// instead of trusting the design.

import (
	"path/filepath"
	"testing"
)

// openModule writes n files in one namespace and opens the first.
func openModule(t *testing.T, n int) (*session, string) {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{}
	files["a.ggm"] = "namespace t:i\nobj X\n[export]\nfn one() { X = 1 }\n"
	for i := 1; i < n; i++ {
		name := "f" + itoa(i) + ".ggm"
		files[name] = "namespace t:i\n[export]\nfn g" + itoa(i) + "() { X = " + itoa(i) + " }\n"
	}
	writeFiles(t, dir, files)
	sess := newSession(t, dir)
	first := filepath.Join(dir, "a.ggm")
	sess.notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: uriFromPath(first), Text: files["a.ggm"]},
	})
	sess.diagnostics(uriFromPath(first))
	return sess, first
}

func parsesOf(s *session) int {
	s.srv.mu.Lock()
	defer s.srv.mu.Unlock()
	return s.srv.parses
}

// Editing one file must re-parse that file only.
func TestOnlyTheEditedFileIsReparsed(t *testing.T) {
	sess, first := openModule(t, 5)
	before := parsesOf(sess)

	text := "namespace t:i\nobj X\n[export]\nfn one() { X = 2 }\n"
	sess.notify("textDocument/didChange", DidChangeTextDocumentParams{
		TextDocument:   VersionedTextDocumentIdentifier{URI: uriFromPath(first), Version: 2},
		ContentChanges: []TextDocumentContentChangeEvent{{Text: text}},
	})
	sess.diagnostics(uriFromPath(first))

	if got := parsesOf(sess) - before; got > 1 {
		t.Fatalf("one edit re-parsed %d files out of 5; the AST cache is not hitting", got)
	}
}

// A save that changed nothing must not recompile.
func TestUnchangedSaveDoesNoWork(t *testing.T) {
	sess, first := openModule(t, 3)
	before := parsesOf(sess)

	sess.notify("textDocument/didSave", DidSaveTextDocumentParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(first)},
	})
	// An unchanged module publishes nothing, so wait for the server to fall
	// idle by asking something that does answer.
	var h any
	sess.ask(30, "textDocument/hover", PositionParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(first)},
		Position:     Position{Line: 3, Character: 4},
	}, &h)

	if got := parsesOf(sess) - before; got != 0 {
		t.Fatalf("a save with no edits re-parsed %d files", got)
	}
}
