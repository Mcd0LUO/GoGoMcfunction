package lsp

import (
	"testing"
	"time"
)

// A timing report, not an assertion.
//
// The point is to know what a keystroke costs so the incremental work can be
// judged instead of assumed. Run with:
//
//	go test ./internal/lsp -run KeystrokeCost -v
func TestKeystrokeCostReport(t *testing.T) {
	sess, first := openModule(t, 50)
	base := parsesOf(sess)

	start := time.Now()
	for i := 0; i < 20; i++ {
		text := "namespace t:i\nobj X\n[export]\nfn one() { X = " + itoa(i) + " }\n"
		sess.notify("textDocument/didChange", DidChangeTextDocumentParams{
			TextDocument:   VersionedTextDocumentIdentifier{URI: uriFromPath(first), Version: int32(i + 2)},
			ContentChanges: []TextDocumentContentChangeEvent{{Text: text}},
		})
		sess.diagnostics(uriFromPath(first))
	}
	elapsed := time.Since(start)
	parses := parsesOf(sess) - base
	t.Logf("50-file module: 20 edits in %v, %d parses (one per edit is the target)",
		elapsed, parses)
	if parses > 20 {
		t.Fatalf("%d parses for 20 edits; the AST cache stopped hitting", parses)
	}
}
