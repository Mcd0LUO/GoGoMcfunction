package lsp

// Incrementality, proved rather than assumed.
//
// The server caches one parse per file and skips a recompile when nothing
// changed. Both are easy to break silently -- a cache that never hits still
// produces correct answers, just slowly -- so the tests count real parses
// instead of trusting the design.

import (
	"os"
	"path/filepath"
	"strings"
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

// A file created outside the editor must join the module.
//
// Before this was handled, the server only knew the files present at startup,
// so creating a module file and calling into it reported the call as an
// unknown callee -- a false alarm on correct code.
func TestNewlyCreatedFileJoinsTheModule(t *testing.T) {
	dir := t.TempDir()
	aSrc := "namespace t:w\nobj X\n[export]\nfn one() { X = two() }\n"
	writeFiles(t, dir, map[string]string{"a.ggm": aSrc})
	sess := newSession(t, dir)
	aPath := filepath.Join(dir, "a.ggm")
	sess.notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: uriFromPath(aPath), Text: aSrc},
	})
	// At this point `two` does not exist, so the compiler is right to complain.
	first := sess.diagnostics(uriFromPath(aPath))
	var unknown bool
	for _, d := range first {
		if d.Code == "E_UNKNOWN_CALLEE" {
			unknown = true
		}
	}
	if !unknown {
		t.Fatal("a call to a missing function was not reported, so the fixture proves nothing")
	}

	// Now the file appears, and the editor says so.
	bPath := filepath.Join(dir, "b.ggm")
	bSrc := "namespace t:w\n[export]\nfn two() -> int { 2 }\n"
	if err := os.WriteFile(bPath, []byte(bSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	sess.notify("workspace/didChangeWatchedFiles", DidChangeWatchedFilesParams{
		Changes: []FileEvent{{URI: uriFromPath(bPath), Type: 1}},
	})
	after := sess.diagnostics(uriFromPath(aPath))
	for _, d := range after {
		if d.Code == "E_UNKNOWN_CALLEE" {
			t.Fatalf("the call is still reported as unknown after the file appeared: %s", d.Message)
		}
	}
}

// A brand-new buffer has no namespace yet -- it is the first thing anyone
// types -- and help is worth most exactly then.
//
// The server used to build an index only for a file that declared a
// namespace, so completion, hover and jump answered nothing until it existed.
func TestNamespaceLessFileStillGetsHelp(t *testing.T) {
	dir := t.TempDir()
	src := "obj X\n[export]\nfn f() { X = 1 }\n"
	writeFiles(t, dir, map[string]string{"new.ggm": src})
	sess := newSession(t, dir)
	path := filepath.Join(dir, "new.ggm")
	sess.notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: uriFromPath(path), Text: src},
	})
	sess.diagnostics(uriFromPath(path)) // the E_NO_NAMESPACE report

	var h Hover
	sess.ask(95, "textDocument/hover", PositionParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(path)},
		Position:     Position{Line: 0, Character: 4},
	}, &h)
	if !strings.Contains(h.Contents.Value, "obj X") {
		t.Fatalf("hover in a namespace-less file said %q", h.Contents.Value)
	}

	var list CompletionList
	sess.ask(96, "textDocument/completion", PositionParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(path)},
		Position:     Position{Line: 2, Character: 7},
	}, &list)
	if len(list.Items) == 0 {
		t.Fatal("completion in a namespace-less file offered nothing")
	}
}
