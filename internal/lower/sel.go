package lower

// sel: a name for a whole selector.
//
//   sel Monsters = @e[tag=mob,r=12]
//   for e in entities<Monsters> { ... }
//
// A selector is text all the way down here -- the parser stores @e[tag=x] as one
// opaque string, the back end renders it verbatim, and the target's validator is
// the only thing that knows what a filter key is. So a set is substituted into
// that text and nothing downstream needs to know it existed.
//
// The name stands for the WHOLE selector, so it is written where a selector is
// written and never inside the brackets: @e[Monsters] would mean the filter list
// of one selector plus the base of another, which is two things at once.

import (
	"strings"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/diag"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
)

// inertSelector replaces a name that could not be resolved.
//
// Leaving the name in place would emit a bare word where a selector belongs,
// which 1.12.2 rejects -- and a malformed command makes the whole function file
// fail to load, silently, taking every other command in it with it. The build
// has already failed by then so nothing is written, but an inert selector keeps
// the rest of the program valid while the error is reported, which is the
// difference between a compile error and a pile of them.
const inertSelector = "@e[tag=ggm.sel.unresolved]"

// selText resolves an identifier used in selector position.
//
// ok is false when the name is neither a selector literal nor a declared set,
// which is how a caller tells `kill(@e[tag=x])` from `kill(someLocal)`.
func (l *Lowerer) selText(x syntax.Ident) (string, bool) {
	if strings.HasPrefix(x.Name, "@") {
		return l.selString(x.Name, x.P), true
	}
	if _, ok := l.sels[x.Name]; ok {
		return l.selString(x.Name, x.P), true
	}
	return "", false
}

// selString resolves a string that should hold a selector.
//
// p is where the text starts, so a name inside it can be reported at its own
// column rather than at the start of the statement.
func (l *Lowerer) selString(text string, p syntax.Pos) string {
	if text == "" {
		return ""
	}
	if !strings.HasPrefix(text, "@") {
		sel, ok := l.sels[text]
		if !ok {
			l.diags.Error(diag.Pos{Line: p.Line, Col: p.Col}, "E_SEL_UNKNOWN",
				"no selector set named %q; write the selector out, or declare it with `sel %s = @e[...]`",
				text, text)
			return inertSelector
		}
		return sel
	}
	return l.checkBracketNames(text, p)
}

// checkBracketNames reports a set name written inside a selector's brackets.
//
// That spelling is now wrong: the name means a whole selector, and putting it
// inside [ ] asks for one selector's base with another's filters.
func (l *Lowerer) checkBracketNames(text string, p syntax.Pos) string {
	open := strings.IndexByte(text, '[')
	if open < 0 {
		return text
	}
	close := matchBracket(text, open)
	if close < 0 {
		return text
	}
	seen := map[string]bool{}
	var out []string
	for _, el := range splitFilters(text[open+1 : close]) {
		arg := strings.TrimSpace(el.text)
		if arg == "" {
			continue
		}
		at := shiftPos(p, open+1+el.offset)
		if !strings.ContainsRune(arg, '=') {
			if _, isSet := l.sels[arg]; isSet {
				l.diags.Error(at, "E_SEL_IN_BRACKETS",
					"%q is a selector set, so it stands for the whole selector; write `%s` instead of `[...]` around it",
					arg, arg)
			} else {
				l.diags.Error(at, "E_SEL_BAD",
					"%q is not a filter; a filter is key=value, for example tag=mob", arg)
			}
			out = append(out, "tag=ggm.sel.unresolved")
			continue
		}
		key := strings.TrimSpace(arg[:strings.IndexByte(arg, '=')])
		if seen[key] {
			l.diags.Warn(at, "W_SEL_DUP_KEY",
				"filter %q appears more than once; 1.12.2 does not specify which one wins", key)
		}
		seen[key] = true
		out = append(out, arg)
	}
	return text[:open+1] + strings.Join(out, ",") + text[close:]
}

// lowerSels checks each `sel` declaration where it is written.
//
// The only thing that knows what a filter key is is the target's validator --
// the same predicate the back end applies to the rendered command. Asking it
// here is not a second implementation; it is the same check, run earlier, where
// there is a line and a column.
func (l *Lowerer) lowerSels(f *syntax.File) {
	for _, sd := range f.Sels {
		pos := sd.P
		if sd.FiltersPos.Line > 0 {
			pos = sd.FiltersPos
		}
		if !strings.HasPrefix(sd.Filters, "@") {
			l.diags.Error(diag.Pos{Line: pos.Line, Col: pos.Col}, "E_SEL_BAD",
				"a selector set is a whole selector, so it must start with @: write `sel %s = @e[%s]`",
				sd.Name, sd.Filters)
			l.poison(sd.Name)
			continue
		}
		open := strings.IndexByte(sd.Filters, '[')
		if open < 0 {
			continue
		}
		close := matchBracket(sd.Filters, open)
		if close < 0 {
			continue
		}
		bad := false
		seen := map[string]bool{}
		for _, el := range splitFilters(sd.Filters[open+1 : close]) {
			arg := strings.TrimSpace(el.text)
			at := shiftPos(pos, open+1+el.offset)
			if arg == "" {
				l.diags.Error(at, "E_SEL_BAD", "empty filter in selector set %q", sd.Name)
				bad = true
				continue
			}
			if !strings.ContainsRune(arg, '=') {
				if _, isSet := l.sels[arg]; isSet {
					l.diags.Error(at, "E_SEL_IN_BRACKETS",
						"%q is a selector set, so it stands for the whole selector; a set cannot be part of another set's filters", arg)
				} else {
					l.diags.Error(at, "E_SEL_BAD",
						"%q is not a filter; a filter is key=value, for example tag=mob", arg)
				}
				bad = true
				continue
			}
			if !l.tgt.SelectorArgOK(arg) {
				l.diags.Error(at, "E_SEL_BAD", "%q is not a filter this target accepts", arg)
				bad = true
				continue
			}
			key := strings.TrimSpace(arg[:strings.IndexByte(arg, '=')])
			if seen[key] {
				l.diags.Warn(at, "W_SEL_DUP_KEY",
					"filter %q appears twice in selector set %q; 1.12.2 does not specify which one wins",
					key, sd.Name)
			}
			seen[key] = true
		}
		if bad {
			l.poison(sd.Name)
		}
	}
}

// poison replaces a set's text with an inert selector for every later file too.
func (l *Lowerer) poison(name string) {
	l.sels[name] = inertSelector
	if l.scope.Sels != nil {
		l.scope.Sels[name] = inertSelector
	}
}

// filterEl is one comma-separated element of a filter list.
type filterEl struct {
	text   string
	offset int // rune offset from the start of the list body
}

// splitFilters splits on commas that are not inside brackets, braces or quotes.
func splitFilters(body string) []filterEl {
	var out []filterEl
	src := []rune(body)
	depth := 0
	start := 0
	inString := false
	for i, r := range src {
		switch {
		case r == '"':
			inString = !inString
		case inString:
		case r == '[' || r == '{':
			depth++
		case r == ']' || r == '}':
			depth--
		case r == ',' && depth == 0:
			out = append(out, filterEl{text: string(src[start:i]), offset: start})
			start = i + 1
		}
	}
	out = append(out, filterEl{text: string(src[start:]), offset: start})
	return out
}

// matchBracket returns the index of the bracket closing the one at open.
func matchBracket(s string, open int) int {
	depth := 0
	inString := false
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '"':
			inString = !inString
		case '[':
			if !inString {
				depth++
			}
		case ']':
			if !inString {
				depth--
				if depth == 0 {
					return i
				}
			}
		}
	}
	return -1
}

// shiftPos moves a position along the line by a rune offset.
func shiftPos(p syntax.Pos, offset int) diag.Pos {
	return diag.Pos{Line: p.Line, Col: p.Col + offset}
}
