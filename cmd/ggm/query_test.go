package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A position in a file that was not given must be an error, not an empty
// answer.
//
// The two are indistinguishable on the terminal: both print nothing. That
// makes a mistyped path look exactly like a position with nothing at it, which
// is how this was found -- a relative -at against an absolute argument list
// answered nothing at all.
func TestQueryRejectsAFileThatWasNotGiven(t *testing.T) {
	dir := t.TempDir()
	src := "namespace t:q\nobj X\n[export]\nfn f() { X = 1 }\n"
	path := filepath.Join(dir, "a.ggm")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runQuery("hover", filepath.Join(dir, "nowhere.ggm")+":4:5", []string{path})
	if err == nil {
		t.Fatal("a position in a file that was not given was accepted")
	}
	if !strings.Contains(err.Error(), "not among") {
		t.Fatalf("the error does not explain the problem: %v", err)
	}
}

// The same file named by a relative path gets a suggestion rather than a
// bare refusal.
func TestQuerySuggestsTheMatchingPath(t *testing.T) {
	dir := t.TempDir()
	src := "namespace t:q2\nobj X\n[export]\nfn f() { X = 1 }\n"
	path := filepath.Join(dir, "a.ggm")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runQuery("hover", filepath.Base(path)+":4:5", []string{path})
	if err == nil {
		t.Fatal("a file named only by its base was accepted")
	}
	if !strings.Contains(err.Error(), "did you mean") {
		t.Fatalf("the error offers no suggestion: %v", err)
	}
}

// A position that IS in a given file answers normally.
func TestQueryAnswersForAGivenFile(t *testing.T) {
	dir := t.TempDir()
	src := "namespace t:q3\nobj X\n[export]\nfn f() { X = 1 }\n"
	path := filepath.Join(dir, "a.ggm")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runQuery("hover", path+":2:5", []string{path})
	if err != nil {
		t.Fatalf("hover failed: %v", err)
	}
	if !strings.Contains(out, "obj X") {
		t.Fatalf("hover said %q", out)
	}
}
