// Package syntax turns GGM source into an AST.
//
// The parser is split across files by what is being parsed, because one
// 1600-line file made every change a scroll hunt and hid which functions
// actually belonged together:
//
//	parser.go   the token cursor, and the top level of a file
//	decl.go     declarations: fn, class, method, extern, obj, nbt
//	attr.go     the [a, b(...)] attribute list
//	stmt.go     statements
//	expr.go     expressions
//	nbtdecl.go  the container-of-an-entity path used by decode/encode
//	nbt.go      the NBT value and path codec (not parsing)
//
// The split is by DECLARATION, never by line count: a function and the
// comments explaining why it exists stay together, because they are one
// thought.
package syntax

import "fmt"

// Error is a positioned syntax error.
type Error struct {
	Pos Pos
	Msg string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Pos, e.Msg) }

type parser struct {
	toks []Token
	i    int
}

// Parse parses a GGM source file.
func Parse(src string) (*File, error) {
	toks, err := NewLexer(src).Lex()
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	return p.parseFile()
}

func (p *parser) cur() Token {
	if p.i >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[p.i]
}

func (p *parser) peek() Token {
	if p.i+1 >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[p.i+1]
}

func (p *parser) next() Token {
	t := p.cur()
	if p.i < len(p.toks)-1 {
		p.i++
	}
	return t
}

func (p *parser) skipNewlines() {
	for p.cur().Kind == NEWLINE || p.cur().Kind == SEMI {
		p.next()
	}
}

func (p *parser) expect(k Kind) (Token, error) {
	if p.cur().Kind != k {
		return Token{}, &Error{Pos: p.cur().Pos, Msg: fmt.Sprintf("expected %s, got %s", k, p.cur().Kind)}
	}
	return p.next(), nil
}

// expectName accepts an identifier or a reserved word used as a name.
// Legacy 1.12.2 ABIs call their registers `arg1`, `return`, `in` ... which collide
// with GGM keywords, so extern bindings must be able to spell them.
//
// The test used to be `Text != ""`, which is nearly every token: it accepted
// `@s`, `into`, `from` and any operator that carries text. `decode x into …`
// then swallowed `into` as part of the objective name and reported a missing
// `from` three tokens later, which is a confusing way to say "that word is
// reserved here".
//
// A name is a word: letters, digits and underscore, lexed as IDENT or as a
// keyword. Anything else is not a name no matter what text it carries.
func (p *parser) expectName() (Token, error) {
	if p.cur().Kind == IDENT || isKeyword(p.cur().Kind) {
		return p.next(), nil
	}
	return Token{}, &Error{Pos: p.cur().Pos, Msg: fmt.Sprintf("expected a name, got %s", p.cur().Kind)}
}

// isKeyword reports whether a kind is one of the language's reserved words.
//
// It is derived from the keyword table rather than a second list, so a new
// keyword cannot be usable as a name until somebody decides it should be.
func isKeyword(k Kind) bool {
	for _, v := range keywords {
		if v == k {
			return true
		}
	}
	return false
}

// nameable reports whether a keyword may also serve as a name.
//
// `selector` and `wrapping` are attributes, not reserved words, so a function
// may be called either of them. 1.12.2 allows any path segment, and refusing
// them would make the attribute and the name fight over one spelling.
func nameable(k Kind) bool {
	switch k {
	case SELECTOR, WRAPPING:
		return true
	}
	return false
}

// expectNameLike accepts an identifier or a keyword that may double as one.
func (p *parser) expectNameLike() (Token, error) {
	if p.cur().Kind == IDENT || nameable(p.cur().Kind) {
		return p.next(), nil
	}
	return Token{}, &Error{Pos: p.cur().Pos, Msg: fmt.Sprintf("expected identifier, got %s", p.cur().Kind)}
}

// expectComma consumes a comma, which is also what ends a loop's condition.
func (p *parser) expectComma() error {
	if p.cur().Kind != COMMA {
		return &Error{Pos: p.cur().Pos, Msg: fmt.Sprintf("expected \",\", got %s", p.cur().Kind)}
	}
	p.next()
	return nil
}

// expectWord consumes a token by text. Used for words that read better as
// punctuation than as keywords, so they stay usable as identifiers.
func (p *parser) expectWord(w string) error {
	if p.cur().Text != w {
		return &Error{Pos: p.cur().Pos, Msg: fmt.Sprintf("expected %q, got %s", w, p.cur().Kind)}
	}
	p.next()
	return nil
}

func (p *parser) expectIdent() (Token, error) {
	if p.cur().Kind != IDENT {
		return Token{}, &Error{Pos: p.cur().Pos, Msg: fmt.Sprintf("expected identifier, got %s", p.cur().Kind)}
	}
	return p.next(), nil
}

func (p *parser) parseFile() (*File, error) {
	f := &File{P: p.cur().Pos}
	// Attributes are a prefix on the declaration they apply to, not a
	// separate statement: `[load, every("5s")] fn f() { }`. They used to be
	// two spellings (`[wrapping]` and `[selector(...)]`) and one `on ...`
	// form, which meant three syntaxes for one idea.
	var attrs []Attr
	for {
		p.skipNewlines()
		t := p.cur()
		if t.Kind == EOF {
			break
		}
		if t.Kind == LBRACK {
			a, err := p.parseAttrs()
			if err != nil {
				return nil, err
			}
			attrs = append(attrs, a...)
			continue
		}
		switch t.Kind {
		case NAMESPACE:
			p.next()
			n, err := p.parsePath()
			if err != nil {
				return nil, err
			}
			f.Namespace = n
		case COMPTIME:
			p.next()
			body, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			f.Comptime = append(f.Comptime, ComptimeBlock{Body: body, P: t.Pos})
		case CONST:
			p.next()
			n, err := p.expectName()
			if err != nil {
				return nil, err
			}
			if p.cur().Kind == COLON {
				p.next()
				if _, err := p.expectIdent(); err != nil {
					return nil, err
				}
			}
			if _, err := p.expect(ASSIGN); err != nil {
				return nil, err
			}
			v, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if f.Consts == nil {
				f.Consts = map[string]Expr{}
			}
			f.Consts[n.Text] = v
		case EXTERN:
			d, err := p.parseExtern()
			if err != nil {
				return nil, err
			}
			f.Externs = append(f.Externs, d)
		case FN:
			if err := checkAttrs(attrs, "fn"); err != nil {
				return nil, err
			}
			fd, err := p.parseFunc(attrs)
			if err != nil {
				return nil, err
			}
			f.Funcs = append(f.Funcs, *fd)
			attrs = nil
		case CLASS:
			if err := checkAttrs(attrs, "class"); err != nil {
				return nil, err
			}
			cd, err := p.parseClass(attrs)
			if err != nil {
				return nil, err
			}
			f.Classes = append(f.Classes, *cd)
			attrs = nil
		case OBJ:
			od, err := p.parseObj()
			if err != nil {
				return nil, err
			}
			f.Objs = append(f.Objs, *od)
		case NBTDECL:
			fd, err := p.parseNBTField()
			if err != nil {
				return nil, err
			}
			f.NBTFields = append(f.NBTFields, *fd)
		default:
			return nil, &Error{Pos: t.Pos, Msg: fmt.Sprintf("unexpected %s at top level", t.Kind)}
		}
	}
	// An attribute that never reached a declaration would otherwise be
	// dropped without a word.
	if len(attrs) > 0 {
		return nil, &Error{Pos: attrs[0].P, Msg: fmt.Sprintf("attribute [%s] does not precede a declaration", attrs[0].Name)}
	}
	return f, nil
}

// parseDottedName reads a name of the form NAME ("." NAME)*.
//
// Objective and criteria names both use dots: `wp.damage1`, `stat.damageDealt`,
// `teamkill.red`. The first segment goes through expectName so legacy ABI
// registers such as `return` and `in` remain usable as objective names.
func (p *parser) parseDottedName() (string, error) {
	t, err := p.expectName()
	if err != nil {
		return "", err
	}
	s := t.Text
	// A namespace-style colon is part of many existing objective names:
	// `EasyCoord:x`, `sk.hpRestore`, `wp.passiveId1:1`. 1.12.2 allows it,
	// and a library that could not spell such a name could not replace one.
	if p.cur().Kind == COLON {
		p.next()
		seg, err := p.expectName()
		if err != nil {
			return "", err
		}
		s += ":" + seg.Text
	}
	for p.cur().Kind == DOT {
		p.next()
		seg, err := p.expectIdent()
		if err != nil {
			return "", err
		}
		s += "." + seg.Text
	}
	return s, nil
}

func (p *parser) parsePath() (string, error) {
	t, err := p.expectIdent()
	if err != nil {
		return "", err
	}
	s := t.Text
	if p.cur().Kind == COLON {
		p.next()
		var parts []string
		for {
			// A namespace segment may start with a digit: the reference data
			// pack has `probe:ph/1`. parsePathSegment assembles such a name
			// from adjacent tokens, because the lexer splits `1a` into an
			// integer and an identifier.
			seg, err := p.parsePathSegment()
			if err != nil {
				return "", err
			}
			parts = append(parts, seg)
			if p.cur().Kind == SLASH {
				p.next()
				continue
			}
			break
		}
		s += ":" + join(parts, "/")
	}
	return s, nil
}

func join(parts []string, sep string) string {
	out := ""
	for i, s := range parts {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}
