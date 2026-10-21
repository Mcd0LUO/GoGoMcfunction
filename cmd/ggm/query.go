package main

// The -query mode exists for one reason: iterating on hover, definition and
// completion should not require an editor.

// A language feature that can only be checked by clicking is a feature whose
// wording and ranges get verified once and then rot. With a query mode each
// check is one command:

//	ggm -query hover -at a.ggm:4:14 a.ggm b.ggm

// and the same code path serves the editor, so what is checked is what ships.

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/diag"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/symbols"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
)

// queryKinds are the questions this mode can answer.
var queryKinds = map[string]bool{"hover": true, "def": true, "complete": true}

// runQuery answers one question about a position and prints the answer.
//
// It returns (handled, error): handled is false when -query was not given, so
// the caller falls through to compiling.
func runQuery(kind, at string, files []string) (string, error) {
	if kind == "" {
		return "", errNotAQuery
	}
	if !queryKinds[kind] {
		return "", fmt.Errorf("-query %q is not one of hover, def, complete", kind)
	}
	if at == "" {
		return "", fmt.Errorf("-query needs -at <file>:<line>:<col>")
	}
	file, line, col, err := splitAt(at)
	if err != nil {
		return "", err
	}
	ix, err := buildIndex(files)
	if err != nil {
		return "", err
	}
	switch kind {
	case "hover":
		return hover(ix, file, line, col), nil
	case "def":
		return definition(ix, file, line, col), nil
	case "complete":
		return completion(ix, file, line, col), nil
	}
	return "", nil
}

var errNotAQuery = fmt.Errorf("not a query")

// buildIndex parses every file and indexes the module.
func buildIndex(files []string) (*symbols.Index, error) {
	parsed := map[string]*syntax.File{}
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		f, err := syntax.Parse(string(b))
		if err != nil {
			// A file that does not parse has no symbols. Reporting the parse
			// error here would hide the symbols of every other file, so the
			// file is skipped and the index covers what is readable.
			continue
		}
		parsed[name] = f
	}
	return symbols.Build(parsed), nil
}

// hover prints what the name at a position is.
func hover(ix *symbols.Index, file string, line, col int) string {
	s, ok := ix.At(file, line, col)
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString(s.Type)
	if s.Detail != "" {
		b.WriteString("\n\n" + s.Detail)
	}
	if s.Pos.Line > 0 {
		b.WriteString(fmt.Sprintf("\n\ndeclared at %s:%d:%d", s.File, s.Pos.Line, s.Pos.Col))
	} else if s.Kind != symbols.KShorthand {
		b.WriteString("\n\n" + s.File)
	}
	return b.String()
}

// definition prints where the name at a position is declared.
func definition(ix *symbols.Index, file string, line, col int) string {
	s, ok := ix.At(file, line, col)
	if !ok {
		return ""
	}
	if s.Kind == symbols.KShorthand {
		// There is nothing to jump to: the name is a fake player, not a
		// declaration. Saying so is better than jumping somewhere unrelated.
		return ""
	}
	decls := ix.Lookup(s.Name)
	if len(decls) == 0 && s.Pos.Line > 0 {
		return fmt.Sprintf("%s:%d:%d", s.File, s.Pos.Line, s.Pos.Col)
	}
	var out []string
	seen := map[string]bool{}
	for _, d := range decls {
		if d.Pos.Line == 0 {
			continue
		}
		loc := fmt.Sprintf("%s:%d:%d", d.File, d.Pos.Line, d.Pos.Col)
		if seen[loc] {
			continue
		}
		seen[loc] = true
		out = append(out, loc)
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

// completion prints the names that start with the identifier under the cursor.
func completion(ix *symbols.Index, file string, line, col int) string {
	prefix := prefixAt(file, line, col)
	var out []string
	for _, s := range ix.Completions(prefix) {
		out = append(out, s.Name+"\t"+string(s.Kind)+"\t"+s.Type)
	}
	return strings.Join(out, "\n")
}

// prefixAt reads the identifier fragment the cursor sits in or just after.
func prefixAt(file string, line, col int) string {
	b, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	if line < 1 || line > len(lines) {
		return ""
	}
	src := []rune(strings.TrimSuffix(lines[line-1], "\r"))
	i := col - 1
	if i > len(src) {
		i = len(src)
	}
	start := i
	for start > 0 && isIdentRune(src[start-1]) {
		start--
	}
	end := i
	for end < len(src) && isIdentRune(src[end]) {
		end++
	}
	return string(src[start:end])
}

func isIdentRune(r rune) bool {
	return r == '_' || r == '.' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r > 127
}

// splitAt parses <file>:<line>:<col>.
//
// The file may contain a colon -- on Windows every path does -- so the split
// is taken from the right.
func splitAt(at string) (string, int, int, error) {
	i := strings.LastIndex(at, ":")
	if i < 0 {
		return "", 0, 0, fmt.Errorf("-at must be <file>:<line>:<col>")
	}
	col, err := strconv.Atoi(at[i+1:])
	if err != nil {
		return "", 0, 0, fmt.Errorf("-at column %q is not a number", at[i+1:])
	}
	rest := at[:i]
	j := strings.LastIndex(rest, ":")
	if j < 0 {
		return "", 0, 0, fmt.Errorf("-at must be <file>:<line>:<col>")
	}
	line, err := strconv.Atoi(rest[j+1:])
	if err != nil {
		return "", 0, 0, fmt.Errorf("-at line %q is not a number", rest[j+1:])
	}
	return rest[:j], line, col, nil
}

var _ = diag.Pos{}
