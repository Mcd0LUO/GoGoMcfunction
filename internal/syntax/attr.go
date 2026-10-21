// The attribute list.
//
// One spelling for every modifier and trigger: [load], [every("5s")],
// [spawn], [selector(...)], [wrapping], [export].
package syntax

import "fmt"

// Attr is one attribute in a declaration's attribute list.
//
// Attributes used to be two different spellings -- `[wrapping]` and
// `[selector(...)]` -- plus a separate `on ...` statement, so the same idea
// had three syntaxes and a reader had to know which one went with which
// declaration. There is now one: a comma-separated list in square brackets
// before fn or class.
type Attr struct {
	Name string
	Args []Expr
	P    Pos
}

// parseAttrs reads `[a, b(...), c]`, or returns nil when the next token is
// not `[`, leaving the cursor alone.
func (p *parser) parseAttrs() ([]Attr, error) {
	if p.cur().Kind != LBRACK {
		return nil, nil
	}
	open := p.next()
	var out []Attr
	for {
		p.skipNewlines()
		if p.cur().Kind == RBRACK {
			p.next()
			return out, nil
		}
		if p.cur().Kind == EOF {
			return nil, &Error{Pos: open.Pos, Msg: "unterminated attribute list"}
		}
		n, err := p.expectNameLike()
		if err != nil {
			return nil, err
		}
		a := Attr{Name: n.Text, P: n.Pos}
		if p.cur().Kind == LPAREN {
			p.next()
			for p.cur().Kind != RPAREN {
				e, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				a.Args = append(a.Args, e)
				if p.cur().Kind == COMMA {
					p.next()
				}
			}
			p.next()
		}
		out = append(out, a)
		if p.cur().Kind == COMMA {
			p.next()
		}
	}
}

// DeclAttrs are the attributes this build understands on a fn or a class.
//
// `spawn` is only meaningful on a class and `every` on both, but the split is
// checked where it matters rather than here: a single table keeps the error
// message one sentence instead of two.
var DeclAttrs = map[string]bool{
	"load": true, "every": true, "spawn": true,
	"selector": true, "wrapping": true, "export": true,
}

// checkAttrs rejects an attribute the compiler does not implement.
//
// Silently ignoring a directive is worse than rejecting it: the author goes on
// believing it took effect, and a typo becomes a behaviour change nobody can
// see.
func checkAttrs(attrs []Attr, what string) error {
	seen := map[string]Pos{}
	for _, a := range attrs {
		if !DeclAttrs[a.Name] {
			return &Error{Pos: a.P, Msg: fmt.Sprintf("unknown attribute [%s] on %s", a.Name, what)}
		}
		// A repeated attribute is almost always a typo, and keeping the last
		// one would make [every("1s"), every("5s")] look like two schedules
		// while only one of them ever ran.
		if prev, dup := seen[a.Name]; dup {
			return &Error{Pos: a.P, Msg: fmt.Sprintf("attribute [%s] appears twice (first at %s)", a.Name, prev)}
		}
		seen[a.Name] = a.P
	}
	return nil
}

// everyTicks resolves [every("...")] into a tick count.
//
// The duration is a STRING literal rather than a unit-suffixed number. That
// is what lets the lexer forget durations entirely: `1tick` inside a string
// is just text, so `tick` never has to be a keyword in that position, and
// there is exactly one implementation of the unit table.
//
// Only literals are accepted. A period decides the name of the dispatcher
// function and of its counter objective, so it has to be known at compile
// time -- a const would be known too, but accepting it invites a value that
// is only constant in some build.
func everyTicks(attrs []Attr) (int, error) {
	for _, a := range attrs {
		if a.Name != "every" {
			continue
		}
		if len(a.Args) != 1 {
			return 0, &Error{Pos: a.P, Msg: "every takes one argument: a duration string like \"5s\" or \"1tick\""}
		}
		lit, ok := a.Args[0].(StringLit)
		if !ok {
			return 0, &Error{Pos: a.P, Msg: "every takes a string literal, because a period has to be known before the program runs"}
		}
		n, ok := ParseDuration(lit.Value)
		if !ok {
			return 0, &Error{Pos: a.P, Msg: fmt.Sprintf("%q is not a duration I can schedule; the smallest unit is one game tick, so use tick(s), s or m", lit.Value)}
		}
		return n, nil
	}
	return 0, nil
}

// selectorArg reads [selector(@e[...])].
//
// The selector arrives as an expression: `@e[tag=x]` lexes as AT, IDENT,
// LBRACK, ... and parseSelectorLiteral already knows how to reassemble that
// shape, so the class declaration reuses it rather than scanning text.
func selectorArg(attrs []Attr) (string, error) {
	for _, a := range attrs {
		if a.Name != "selector" {
			continue
		}
		if len(a.Args) != 1 {
			return "", &Error{Pos: a.P, Msg: "selector takes one argument: a selector like @e[tag=mob]"}
		}
		id, ok := a.Args[0].(Ident)
		if !ok {
			return "", &Error{Pos: a.P, Msg: "selector takes a selector literal"}
		}
		return id.Name, nil
	}
	return "", nil
}
