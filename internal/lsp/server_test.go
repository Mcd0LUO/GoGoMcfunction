package lsp

// The server is tested over the wire, not by calling its methods.
//
// A language server's failure modes live in the protocol layer: a message
// without Content-Length, a byte count read as characters, a response sent
// where a notification belongs. Calling recompile() directly would test none
// of them. So each test here starts Serve on a pipe and speaks LSP to it.

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// session is a client connected to a server over pipes.
type session struct {
	t *testing.T
	c *conn
}

// newSession starts a server rooted at dir and completes the handshake.
func newSession(t *testing.T, dir string) *session {
	t.Helper()
	cliR, cliW := io.Pipe()   // client writes -> server reads
	servR, servW := io.Pipe() // server writes -> client reads
	s := New(dir)
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(cliR, servW)
		_ = servW.Close()
	}()
	sess := &session{t: t, c: newConn(servR, cliW)}
	t.Cleanup(func() {
		_ = cliW.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("server did not stop after the client closed the pipe")
		}
	})

	sess.request(1, "initialize", InitializeParams{RootURI: uriFromPath(dir)})
	sess.notify("initialized", struct{}{})
	return sess
}

func (s *session) request(id int, method string, params any) *msg {
	s.t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		s.t.Fatalf("marshal %s: %v", method, err)
	}
	frame := msg{JSONRPC: "2.0", ID: json.RawMessage(strconv.Itoa(id)), Method: method, Params: raw}
	if err := s.c.write(frame); err != nil {
		s.t.Fatalf("send %s: %v", method, err)
	}
	m, err := s.c.read()
	if err != nil {
		s.t.Fatalf("read response to %s: %v", method, err)
	}
	return m
}

func (s *session) notify(method string, params any) {
	s.t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		s.t.Fatalf("marshal %s: %v", method, err)
	}
	if err := s.c.write(msg{JSONRPC: "2.0", Method: method, Params: raw}); err != nil {
		s.t.Fatalf("send %s: %v", method, err)
	}
}

// diagnostics reads publishDiagnostics until one arrives for want.
func (s *session) diagnostics(want DocumentURI) []Diagnostic {
	s.t.Helper()
	for i := 0; i < 128; i++ {
		m, err := s.c.read()
		if err != nil {
			s.t.Fatalf("read diagnostics: %v", err)
		}
		if m.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var p PublishDiagnosticsParams
		if err := json.Unmarshal(m.Params, &p); err != nil {
			s.t.Fatalf("decode diagnostics: %v", err)
		}
		if p.URI == want {
			return p.Diagnostics
		}
	}
	s.t.Fatalf("no diagnostics published for %s", want)
	return nil
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A module is one namespace spread over several files. Compiling only the
// edited file would make every cross-file call an unknown callee -- a false
// alarm on correct code, which is worse than no squiggles at all.
func TestCrossFileCallIsNotReportedAsUnknown(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"a.ggm": "namespace t:m\nobj X\n[export]\nfn one() { X = two() }\n",
		"b.ggm": "namespace t:m\n[export]\nfn two() -> int { 2 }\n",
	})
	sess := newSession(t, dir)
	path := filepath.Join(dir, "b.ggm")
	sess.notify("textDocument/didSave", DidSaveTextDocumentParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(path)},
	})
	got := sess.diagnostics(uriFromPath(path))
	for _, d := range got {
		if d.Code == "E_UNKNOWN_CALLEE" {
			t.Fatalf("a call to a function in another file of the same module "+
				"was reported as unknown, so the module was compiled per file: %s", d.Message)
		}
	}
}

// A real mistake must arrive with a usable range and a code.
func TestDiagnosticCarriesAPosition(t *testing.T) {
	dir := t.TempDir()
	src := "namespace t:e\nobj X\n[export]\nfn f() { X = }\n"
	writeFiles(t, dir, map[string]string{"c.ggm": src})
	sess := newSession(t, dir)
	path := filepath.Join(dir, "c.ggm")
	sess.notify("textDocument/didSave", DidSaveTextDocumentParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(path)},
	})
	got := sess.diagnostics(uriFromPath(path))
	if len(got) == 0 {
		t.Fatal("a syntax error produced no diagnostic")
	}
	d := got[0]
	// The bad token is on line 4 (1-based), which is 3 in LSP terms.
	if d.Range.Start.Line != 3 {
		t.Fatalf("diagnostic line = %d, want 3 (0-based): %s", d.Range.Start.Line, d.Message)
	}
	if d.Code == "" {
		t.Fatal("diagnostic has no code; the client cannot filter or link it")
	}
	if d.Source != "ggm" {
		t.Fatalf("diagnostic source = %q, want ggm", d.Source)
	}
	if d.Message == "" {
		t.Fatal("diagnostic has no message")
	}
}

// CJK identifiers must not shift the column.
//
// The compiler counts runes from 1, LSP counts UTF-16 code units from 0. For
// BMP characters -- which Chinese is -- those are equal, so a column after a
// Chinese name stays correct. This pins it, because getting it wrong puts the
// squiggle on the wrong character and looks like a different bug entirely.
func TestColumnAfterChineseIdentifier(t *testing.T) {
	dir := t.TempDir()
	src := "namespace t:c\nobj 残照花魄\n[export]\nfn f() { 残照花魄 = }\n"
	writeFiles(t, dir, map[string]string{"d.ggm": src})
	sess := newSession(t, dir)
	path := filepath.Join(dir, "d.ggm")
	sess.notify("textDocument/didSave", DidSaveTextDocumentParams{
		TextDocument: TextDocumentIdentifier{URI: uriFromPath(path)},
	})
	got := sess.diagnostics(uriFromPath(path))
	if len(got) == 0 {
		t.Fatal("no diagnostic for a syntax error after a Chinese identifier")
	}
	// The } is the 17th rune of the line, so 0-based 16. Counting bytes
	// instead would give 25 and put the squiggle far to the right.
	if c := got[0].Range.Start.Character; c != 16 {
		t.Fatalf("diagnostic column = %d, want 16 (runes, not bytes)", c)
	}
}

// A clean file must publish an EMPTY list: that is how a stale squiggle is
// cleared after the author fixes the mistake.
func TestFixedFileClearsItsDiagnostics(t *testing.T) {
	dir := t.TempDir()
	sess := newSession(t, dir)
	path := filepath.Join(dir, "e.ggm")
	broken := "namespace t:f\nobj X\n[export]\nfn f() { X = }\n"
	writeFiles(t, dir, map[string]string{"e.ggm": broken})

	sess.notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: uriFromPath(path), Text: broken},
	})
	if got := sess.diagnostics(uriFromPath(path)); len(got) == 0 {
		t.Fatal("the broken version produced no diagnostic")
	}

	fixed := "namespace t:f\nobj X\n[export]\nfn f() { X = 1 }\n"
	sess.notify("textDocument/didChange", DidChangeTextDocumentParams{
		TextDocument:   VersionedTextDocumentIdentifier{URI: uriFromPath(path), Version: 2},
		ContentChanges: []TextDocumentContentChangeEvent{{Text: fixed}},
	})
	if got := sess.diagnostics(uriFromPath(path)); len(got) != 0 {
		t.Fatalf("diagnostics were not cleared after the fix: %v", got)
	}
}
