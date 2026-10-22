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
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/diag"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/symbols"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
)

// queryKinds are the questions this mode can answer.
var queryKinds = map[string]bool{"hover": true, "def": true, "complete": true, "refs": true, "sig": true}

// runQuery answers one question about a position and prints the answer.
//
// It returns (handled, error): handled is false when -query was not given, so
// the caller falls through to compiling.
func runQuery(kind, at string, files []string) (string, error) {
	if kind == "" {
		return "", errNotAQuery
	}
	if !queryKinds[kind] {
		return "", fmt.Errorf("-query %q is not one of hover, def, complete, refs, sig", kind)
	}
	if at == "" {
		return "", fmt.Errorf("-query needs -at <file>:<line>:<col>")
	}
	file, line, col, err := splitAt(at)
	if err != nil {
		return "", err
	}
	ix, known, err := buildIndex(files)
	if err != nil {
		return "", err
	}
	// A position in a file that is not among the inputs looks exactly like a
	// position with nothing at it: both print an empty answer. Naming the
	// problem is the difference between a wrong path and a wrong line.
	if !known[file] {
		if alt := matchByBase(known, file); alt != "" {
			return "", fmt.Errorf("-at names %s, which is not among the inputs; did you mean %s", file, alt)
		}
		return "", fmt.Errorf("-at names %s, which is not among the %d input files", file, len(known))
	}
	switch kind {
	case "hover":
		return hover(ix, file, line, col), nil
	case "def":
		return definition(ix, file, line, col), nil
	case "complete":
		return completion(ix, file, line, col), nil
	case "refs":
		return references(ix, file, line, col), nil
	case "sig":
		return signature(ix, file, line, col), nil
	}
	return "", nil
}

var errNotAQuery = fmt.Errorf("not a query")

// buildIndex parses every file and indexes the module.
//
// It also returns the set of files it managed to read, so a caller can tell a
// position in a file that was not given from a position with nothing at it.
func buildIndex(files []string) (*symbols.Index, map[string]bool, error) {
	parsed := map[string]*syntax.File{}
	known := map[string]bool{}
	textsOf := map[string]string{}
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			return nil, nil, err
		}
		known[name] = true
		textsOf[name] = string(b)
		f, err := syntax.Parse(string(b))
		if err != nil {
			// A file that does not parse has no symbols. Reporting the parse
			// error here would hide the symbols of every other file, so the
			// file is skipped and the index covers what is readable.
			continue
		}
		parsed[name] = f
	}
	return symbols.Build(parsed, textsOf), known, nil
}

// matchByBase finds the input whose file name alone matches a path.
//
// This is the mistake worth catching: the same file written relative in -at
// and absolute in the argument list, or the other way round.
func matchByBase(known map[string]bool, want string) string {
	base := filepath.Base(want)
	var hits []string
	for name := range known {
		if filepath.Base(name) == base {
			hits = append(hits, name)
		}
	}
	if len(hits) == 1 {
		return hits[0]
	}
	return ""
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

// signature prints the call the cursor is inside, and which argument it is on.
func signature(ix *symbols.Index, file string, line, col int) string {
	name, active, ok := callAt(file, line, col)
	if !ok {
		return ""
	}
	for _, s := range ix.Lookup(name) {
		if s.Kind != symbols.KFunc && s.Kind != symbols.KMethod {
			continue
		}
		want := fmt.Sprintf("argument %d of %d", active+1, len(s.Params))
		if len(s.Params) == 0 {
			want = "takes no arguments"
		}
		return s.Type + "\n" + want
	}
	return ""
}

// callAt finds the nearest unclosed paren before the cursor and the name in
// front of it, plus which comma-separated argument the cursor is on.
func callAt(file string, line, col int) (string, int, bool) {
	b, err := os.ReadFile(file)
	if err != nil {
		return "", 0, false
	}
	lines := strings.Split(string(b), "\n")
	if line < 1 || line > len(lines) {
		return "", 0, false
	}
	src := []rune(strings.TrimSuffix(lines[line-1], "\r"))
	cur := col - 1
	if cur < 0 || cur > len(src) {
		return "", 0, false
	}
	depth, open := 0, -1
	for i := cur - 1; i >= 0; i-- {
		switch src[i] {
		case ')':
			depth++
		case '(':
			if depth == 0 {
				open = i
			} else {
				depth--
			}
		}
		if open >= 0 {
			break
		}
	}
	if open < 0 {
		return "", 0, false
	}
	start := open
	for start > 0 && isIdentRune(src[start-1]) {
		start--
	}
	if start == open {
		return "", 0, false
	}
	active, depth := 0, 0
	for i := open + 1; i < cur && i < len(src); i++ {
		switch src[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				active++
			}
		}
	}
	return string(src[start:open]), active, true
}

// references prints every place a symbol is used, one file:line:col per line.
func references(ix *symbols.Index, file string, line, col int) string {
	sym, ok := ix.At(file, line, col)
	if !ok || sym.Kind == symbols.KShorthand {
		return ""
	}
	var out []string
	for _, o := range ix.Occurrences(sym) {
		out = append(out, fmt.Sprintf("%s:%d:%d", o.File, o.Pos.Line, o.Pos.Col))
	}
	return strings.Join(out, "\n")
}

// completion prints the names that start with the identifier under the cursor.
func completion(ix *symbols.Index, file string, line, col int) string {
	prefix := prefixAt(file, line, col)
	var out []string
	// Keywords first, exactly as the editor shows them: this command exists
	// to answer "what will the editor say", so it must not answer differently.
	if b, err := os.ReadFile(file); err == nil {
		for _, kw := range syntax.KeywordsAt(string(b), line, col) {
			if !strings.HasPrefix(kw.Word, prefix) {
				continue
			}
			out = append(out, kw.Word+"\tkeyword\t"+kw.Detail)
		}
	}
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
