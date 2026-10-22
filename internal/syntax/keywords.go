// Keywords offered by completion, grouped by where they may appear.
//
// This is NOT the lexer's keyword table. That one answers "is this word
// reserved", so it contains `in`, `else`, `bits` and `over` -- words that are
// never at the start of a line and would be noise in a completion list. This
// table answers "what may I begin here", which is a different question with a
// different answer.
package syntax

import "strings"

// Keyword is one completion candidate that is not a name.
type Keyword struct {
	Word string
	// Detail is shown beside the entry. It says what writing the word does,
	// not what it is called -- `fn` is unhelpful, "declares a function" is not.
	Detail string
}

// TopLevelKeywords begin a declaration or a block at file scope.
func TopLevelKeywords() []Keyword {
	return []Keyword{
		{"namespace", "declares this file's module"},
		{"obj", "declares an objective"},
		{"extern", "binds a name to a score or a command"},
		{"nbt", "declares a bit-packed or enumerated NBT field"},
		{"const", "declares an integer constant"},
		{"class", "declares a class"},
		{"fn", "declares a function"},
		{"comptime", "runs at compile time and emits literal commands"},
	}
}

// StatementKeywords begin a statement inside a function.
func StatementKeywords() []Keyword {
	return []Keyword{
		{"let", "declares a local"},
		{"decode", "reads an NBT container into an objective"},
		{"encode", "writes an objective into an NBT container"},
		{"if", "conditional"},
		{"while", "loop while a condition holds"},
		{"loop", "loop with no condition"},
		{"for", "iterates over entities or constants"},
		{"return", "returns from the function"},
	}
}

// Attributes may precede a declaration or a statement, in square brackets.
func Attributes() []Keyword {
	return []Keyword{
		{"export", "makes the function or class reachable from a command block"},
		{"load", "runs once at world load"},
		{"every", "runs every N ticks: [every(1s)]"},
		{"spawn", "fan-out on spawn, for a class"},
		{"selector", "the class acts on this selector instead of @s"},
		{"wrapping", "arithmetic wraps instead of saturating"},
		{"add", "decode accumulates instead of resetting"},
	}
}

// KeywordsAt returns the keywords worth offering at a position.
//
// A keyword is only offered where one may be written: at the start of a line
// (indentation aside), or right after the `[` of an attribute. Offering `obj`
// in the middle of an expression is worse than offering nothing -- it teaches
// the author that the list is noise.
//
// The line is read from the text rather than from the AST because the file
// being typed usually does not parse, which is exactly when completion matters.
func KeywordsAt(text string, line, col int) []Keyword {
	lines := strings.Split(text, "\n")
	if line < 1 || line > len(lines) {
		return nil
	}
	src := []rune(strings.TrimSuffix(lines[line-1], "\r"))
	i := col - 1
	if i < 0 || i > len(src) {
		return nil
	}
	// Walk back over the identifier the cursor is inside or just after.
	start := i
	for start > 0 && isNameRune(src[start-1]) {
		start--
	}
	before := strings.TrimRight(string(src[:start]), " \t")
	if before == "" {
		return append(TopLevelKeywords(), StatementKeywords()...)
	}
	if strings.HasSuffix(before, "[") {
		return Attributes()
	}
	return nil
}

// isNameRune reports whether r may be part of a name.
func isNameRune(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r > 127
}
