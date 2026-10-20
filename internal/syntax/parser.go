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
func (p *parser) expectName() (Token, error) {
	if p.cur().Kind == IDENT || p.cur().Text != "" {
		return p.next(), nil
	}
	return Token{}, &Error{Pos: p.cur().Pos, Msg: fmt.Sprintf("expected a name, got %s", p.cur().Kind)}
}

// nameable reports whether a keyword may also serve as a name.
//
// A method called tick is the natural spelling next to @tick, and 1.12.2
// itself allows any path segment, so refusing it made the specification's
// own example uncompilable. These five are only keywords inside "on ..."
// and after "@", and none of them can be followed by "(" in valid syntax,
// so treating them as names elsewhere is unambiguous.
func nameable(k Kind) bool {
	switch k {
	case TICK, SPAWN, DEATH, LOAD, EVERY:
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
	// Attributes preceding a fn/class. The position is kept so an
	// unrecognised attribute can be reported where it was written.
	attrs := map[string]Pos{}
	for {
		p.skipNewlines()
		t := p.cur()
		if t.Kind == EOF {
			break
		}
		switch t.Kind {
		case HASH:
			name, pos, err := p.parseAttr()
			if err != nil {
				return nil, err
			}
			attrs[name] = pos
			continue
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
			if err := checkAttrs(attrs, funcAttrs, "fn"); err != nil {
				return nil, err
			}
			fd, err := p.parseFunc(attrs)
			if err != nil {
				return nil, err
			}
			f.Funcs = append(f.Funcs, *fd)
			attrs = map[string]Pos{}
		case LBRACK:
			// [selector(...)] class X
			if err := checkAttrs(attrs, classAttrs, "class"); err != nil {
				return nil, err
			}
			cd, err := p.parseClass(attrs)
			if err != nil {
				return nil, err
			}
			f.Classes = append(f.Classes, *cd)
			attrs = map[string]Pos{}
		case ON:
			od, err := p.parseOn()
			if err != nil {
				return nil, err
			}
			f.Ons = append(f.Ons, *od)
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
	for name, pos := range attrs {
		return nil, &Error{Pos: pos, Msg: fmt.Sprintf("attribute #[%s] does not precede a declaration", name)}
	}
	return f, nil
}

// funcAttrs are the attributes understood on a fn.
//
// wrapping is the only one, and it is accepted rather than acted on:
// 1.12.2 scoreboard arithmetic always wraps at 32 bits, which is exactly
// what the attribute asks for. It becomes load-bearing once a range
// analysis can prove overflow and warn without it.
var funcAttrs = map[string]bool{"wrapping": true, "export": true}

// classAttrs are the attributes understood on a class.
var classAttrs = map[string]bool{}

// checkAttrs rejects an attribute the parser does not implement.
//
// Silently ignoring a directive is worse than rejecting it: the author
// goes on believing it took effect, and a typo becomes a behaviour
// change nobody can see.
func checkAttrs(attrs map[string]Pos, known map[string]bool, what string) error {
	for name, pos := range attrs {
		if !known[name] {
			return &Error{Pos: pos, Msg: fmt.Sprintf("unknown attribute #[%s] on %s", name, what)}
		}
	}
	return nil
}

func (p *parser) parseAttr() (string, Pos, error) {
	start := p.cur().Pos
	if _, err := p.expect(HASH); err != nil {
		return "", start, err
	}
	if _, err := p.expect(LBRACK); err != nil {
		return "", start, err
	}
	name := p.cur().Text
	if p.cur().Kind == IDENT || p.cur().Kind == BUDGET || p.cur().Kind == WRAPPING {
		p.next()
	} else {
		return "", start, &Error{Pos: p.cur().Pos, Msg: "expected attribute name"}
	}
	// optional (args)
	depth := 0
	for p.cur().Kind != RBRACK || depth > 0 {
		if p.cur().Kind == EOF {
			return "", start, &Error{Pos: p.cur().Pos, Msg: "unterminated attribute"}
		}
		if p.cur().Kind == LPAREN {
			depth++
		}
		if p.cur().Kind == RPAREN {
			depth--
		}
		p.next()
	}
	p.next() // ]
	return name, start, nil
}

// parseObj reads `obj <name> [<criteria>] ["<display>"]`.
//
// The three forms map onto the three fields of 1.12.2's
// `scoreboard objectives add <name> <criteria> [<display>]`:
//
//	obj wp.damage1                          -> dummy, no display
//	obj Eq.Defence "防御"                    -> dummy + display
//	obj post_damage stat.damageDealt "造成"  -> criteria + display
//
// A second bare word is the criteria; a string is the display. Distinguishing
// them by token kind rather than by position keeps the two optional arguments
// from needing a placeholder when only the display is given.
func (p *parser) parseObj() (*ObjDecl, error) {
	t, err := p.expect(OBJ)
	if err != nil {
		return nil, err
	}
	name, err := p.parseDottedName()
	if err != nil {
		return nil, err
	}
	od := &ObjDecl{Name: name, P: t.Pos}
	for {
		switch p.cur().Kind {
		case IDENT:
			if od.Criteria != "" {
				return nil, &Error{Pos: p.cur().Pos, Msg: "obj takes at most one criteria"}
			}
			// A criteria is a dotted name: `stat.damageDealt`, `teamkill.red`.
			// expectIdent alone would stop at the dot and leave the rest of the
			// line to be parsed as a top-level declaration.
			c, err := p.parseDottedName()
			if err != nil {
				return nil, err
			}
			od.Criteria = c
		case STRING:
			if od.Display != "" {
				return nil, &Error{Pos: p.cur().Pos, Msg: "obj takes at most one display name"}
			}
			s := p.next()
			od.Display = s.Text
		default:
			return od, nil
		}
	}
}

// parseNBTField reads `nbt <name> = p"<path>" : bits <n>` or
// `nbt <name> = p"<path>" : enum <spec>`.
//
// The encoding after the colon is what the field is: `bits n` is a folded bit
// ladder, `enum` is a value dispatch. One declaration, two shapes, because a
// field is exactly one of them.
func (p *parser) parseNBTField() (*NBTField, error) {
	t, err := p.expect(NBTDECL)
	if err != nil {
		return nil, err
	}
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(ASSIGN); err != nil {
		return nil, err
	}
	if p.cur().Kind != NBTPATH {
		return nil, &Error{Pos: p.cur().Pos, Msg: fmt.Sprintf("expected an NBT path p\"...\", got %s", p.cur().Kind)}
	}
	pathTok := p.next()
	if err := CheckNBTPath(pathTok.Text); err != nil {
		return nil, &Error{Pos: pathTok.Pos, Msg: err.Error()}
	}
	if _, err := p.expect(COLON); err != nil {
		return nil, err
	}
	fd := &NBTField{Name: name.Text, Path: pathTok.Text, P: t.Pos}
	switch p.cur().Kind {
	case BITS:
		p.next()
		n, err := p.expect(INT)
		if err != nil {
			return nil, err
		}
		fd.Bits = atoi(n.Text)
	case ENUM:
		p.next()
		cases, err := p.parseEnumCases()
		if err != nil {
			return nil, err
		}
		fd.Enum = cases
	default:
		return nil, &Error{Pos: p.cur().Pos, Msg: fmt.Sprintf("expected bits or enum after the NBT path, got %s", p.cur().Kind)}
	}
	return fd, nil
}

// parseEnumCases reads either `1..4` (identity) or `{ "残响":1, ... }` (mapped).
func (p *parser) parseEnumCases() ([]EnumCase, error) {
	if p.cur().Kind == LBRACE {
		p.next()
		var out []EnumCase
		for {
			p.skipNewlines()
			if p.cur().Kind == RBRACE {
				p.next()
				return out, nil
			}
			if p.cur().Kind == EOF {
				return nil, &Error{Pos: p.cur().Pos, Msg: "unterminated enum map"}
			}
			var key string
			switch p.cur().Kind {
			case STRING, IDENT, INT:
				key = p.next().Text
			default:
				return nil, &Error{Pos: p.cur().Pos, Msg: fmt.Sprintf("expected an enum key, got %s", p.cur().Kind)}
			}
			if _, err := p.expect(COLON); err != nil {
				return nil, err
			}
			neg := false
			if p.cur().Kind == MINUS {
				p.next()
				neg = true
			}
			v, err := p.expect(INT)
			if err != nil {
				return nil, err
			}
			n := atoi(v.Text)
			if neg {
				n = -n
			}
			out = append(out, EnumCase{Key: key, Value: n})
			p.skipNewlines()
			if p.cur().Kind == COMMA {
				p.next()
			}
		}
	}
	lo, err := p.expect(INT)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(DOTDOT); err != nil {
		return nil, err
	}
	hi, err := p.expect(INT)
	if err != nil {
		return nil, err
	}
	l, h := atoi(lo.Text), atoi(hi.Text)
	if h < l {
		return nil, &Error{Pos: lo.Pos, Msg: fmt.Sprintf("enum range %d..%d is empty", l, h)}
	}
	var out []EnumCase
	for v := l; v <= h; v++ {
		out = append(out, EnumCase{Key: fmt.Sprint(v), Value: v})
	}
	return out, nil
}

// parseContainer reads `@sel.SelectedItem`, `@sel.Inventory(0..3)` or
// `@sel.Item`.
func (p *parser) parseContainer() (Container, error) {
	selExpr, err := p.parseSelectorLiteral()
	if err != nil {
		return Container{}, err
	}
	sel, ok := selExpr.(Ident)
	if !ok {
		return Container{}, &Error{Pos: p.cur().Pos, Msg: "the container must be a selector"}
	}
	if sel.Name != "@s" {
		return Container{}, &Error{Pos: sel.P, Msg: "the container must be @s: these NBT keys describe the executing entity"}
	}
	if _, err := p.expect(DOT); err != nil {
		return Container{}, err
	}
	key, err := p.expectIdent()
	if err != nil {
		return Container{}, err
	}
	c := Container{Key: key.Text, P: sel.P}
	switch key.Text {
	case "SelectedItem", "Item":
		return c, nil
	case "Inventory":
		if _, err := p.expect(LPAREN); err != nil {
			return Container{}, err
		}
		c.IsSlot = true
		lo, err := p.expect(INT)
		if err != nil {
			return Container{}, err
		}
		c.Lo, c.Hi = atoi(lo.Text), atoi(lo.Text)
		if p.cur().Kind == DOTDOT {
			p.next()
			hi, err := p.expect(INT)
			if err != nil {
				return Container{}, err
			}
			c.Hi = atoi(hi.Text)
		}
		if _, err := p.expect(RPAREN); err != nil {
			return Container{}, err
		}
		if c.Hi < c.Lo {
			return Container{}, &Error{Pos: lo.Pos, Msg: fmt.Sprintf("slot range %d..%d is empty", c.Lo, c.Hi)}
		}
		return c, nil
	}
	return Container{}, &Error{Pos: key.Pos, Msg: fmt.Sprintf("unknown container %q; expected SelectedItem, Inventory or Item", key.Text)}
}

// parseDecode reads `decode <objective> from <container>[<field>] [ { cases } ]`.
func (p *parser) parseDecode() (Stmt, error) {
	t := p.next() // decode
	target, err := p.parseDottedName()
	if err != nil {
		return nil, err
	}
	if err := p.expectWord("from"); err != nil {
		return nil, err
	}
	c, err := p.parseContainer()
	if err != nil {
		return nil, err
	}
	s := DecodeStmt{Target: target, Container: c, P: t.Pos}
	if p.cur().Kind == LBRACK {
		p.next()
		f, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		s.Field = f.Text
		if _, err := p.expect(RBRACK); err != nil {
			return nil, err
		}
	} else {
		return nil, &Error{Pos: p.cur().Pos, Msg: "decode needs a field: decode <objective> from <container>[<field>]"}
	}
	// Optional author-written cases, appended after the field's own.
	if p.cur().Kind == LBRACE {
		p.next()
		for {
			p.skipNewlines()
			if p.cur().Kind == RBRACE {
				p.next()
				break
			}
			if p.cur().Kind == EOF {
				return nil, &Error{Pos: p.cur().Pos, Msg: "unterminated decode block"}
			}
			if p.cur().Kind != NBT {
				return nil, &Error{Pos: p.cur().Pos, Msg: fmt.Sprintf("expected an NBT literal in the decode block, got %s", p.cur().Kind)}
			}
			nb := p.next()
			// add/set stay ordinary identifiers rather than keywords: they are
			// plausible variable names, and a keyword would make `let add = 1`
			// a parse error for no gain.
			var op string
			switch p.cur().Text {
			case "add", "set":
				op = p.cur().Text
			default:
				return nil, &Error{Pos: p.cur().Pos, Msg: fmt.Sprintf("expected add or set, got %s", p.cur().Kind)}
			}
			p.next()
			neg := false
			if p.cur().Kind == MINUS {
				p.next()
				neg = true
			}
			v, err := p.expect(INT)
			if err != nil {
				return nil, err
			}
			n := atoi(v.Text)
			if neg {
				n = -n
			}
			s.Cases = append(s.Cases, DecodeCase{NBT: nb.Text, Op: op, Val: n, P: nb.Pos})
		}
	}
	return s, nil
}

// parseEncode reads `encode <container>[<field>] = <objective> over <lo>..<hi>`.
func (p *parser) parseEncode() (Stmt, error) {
	t := p.next() // encode
	c, err := p.parseContainer()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(LBRACK); err != nil {
		return nil, err
	}
	f, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(RBRACK); err != nil {
		return nil, err
	}
	if _, err := p.expect(ASSIGN); err != nil {
		return nil, err
	}
	src, err := p.parseDottedName()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(OVER); err != nil {
		return nil, err
	}
	neg := func() (int, error) {
		sign := 1
		if p.cur().Kind == MINUS {
			p.next()
			sign = -1
		}
		v, err := p.expect(INT)
		if err != nil {
			return 0, err
		}
		return sign * atoi(v.Text), nil
	}
	lo, err := neg()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(DOTDOT); err != nil {
		return nil, err
	}
	hi, err := neg()
	if err != nil {
		return nil, err
	}
	if hi < lo {
		return nil, &Error{Pos: t.Pos, Msg: fmt.Sprintf("over range %d..%d is empty", lo, hi)}
	}
	return EncodeStmt{Container: c, Field: f.Text, Source: src, Lo: lo, Hi: hi, P: t.Pos}, nil
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
			seg, err := p.expectIdent()
			if err != nil {
				return "", err
			}
			parts = append(parts, seg.Text)
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

func (p *parser) parseFunc(attrs map[string]Pos) (*FuncDecl, error) {
	t, err := p.expect(FN)
	if err != nil {
		return nil, err
	}
	name, err := p.expectNameLike()
	if err != nil {
		return nil, err
	}
	params, err := p.parseParams()
	if err != nil {
		return nil, err
	}
	ret := ""
	if p.cur().Kind == ARROW {
		p.next()
		rt, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		ret = rt.Text
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	_, wrapping := attrs["wrapping"]
	_, export := attrs["export"]
	return &FuncDecl{Name: name.Text, Params: params, RetType: ret, Body: body, Wrapping: wrapping, Export: export, P: t.Pos}, nil
}

// parseMethod parses one method, with or without its tick annotation.
//
// The documented spelling is @tick, and it did not parse at all: the class
// body only knew the bare word, which no reader of the specification would
// write. Both are accepted, because the annotation reads as a modifier
// while the bare word reads as a broken sentence.
func (p *parser) parseMethod() (Method, error) {
	start := p.cur().Pos
	tick := false
	if p.cur().Kind == AT {
		p.next()
		if p.cur().Kind != TICK {
			return Method{}, &Error{Pos: start, Msg: "only @tick is allowed on a method"}
		}
		p.next()
		tick = true
	} else if p.cur().Kind == TICK {
		p.next()
		tick = true
	}
	fd, err := p.parseFunc(map[string]Pos{})
	if err != nil {
		return Method{}, err
	}
	return Method{Name: fd.Name, Params: fd.Params, Body: fd.Body, Tick: tick, P: start}, nil
}

func (p *parser) parseParams() ([]Param, error) {
	if _, err := p.expect(LPAREN); err != nil {
		return nil, err
	}
	var out []Param
	for p.cur().Kind != RPAREN {
		n, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(COLON); err != nil {
			return nil, err
		}
		t, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		out = append(out, Param{Name: n.Text, Type: t.Text})
		if p.cur().Kind == COMMA {
			p.next()
		}
	}
	p.next()
	return out, nil
}

func (p *parser) parseClass(attrs map[string]Pos) (*ClassDecl, error) {
	if _, err := p.expect(LBRACK); err != nil {
		return nil, err
	}
	if _, err := p.expect(SELECTOR); err != nil {
		return nil, err
	}
	if _, err := p.expect(LPAREN); err != nil {
		return nil, err
	}
	sel := ""
	depth := 1
	for depth > 0 {
		if p.cur().Kind == EOF {
			return nil, &Error{Pos: p.cur().Pos, Msg: "unterminated selector"}
		}
		if p.cur().Kind == LPAREN {
			depth++
		}
		if p.cur().Kind == RPAREN {
			depth--
			if depth == 0 {
				p.next()
				break
			}
		}
		sel += p.cur().Text
		p.next()
	}
	if _, err := p.expect(RBRACK); err != nil {
		return nil, err
	}
	p.skipNewlines()
	ct, err := p.expect(CLASS)
	if err != nil {
		return nil, err
	}
	name, err := p.expectNameLike()
	if err != nil {
		return nil, err
	}
	cd := &ClassDecl{Name: name.Text, Selector: sel, P: ct.Pos}
	if _, err := p.expect(LBRACE); err != nil {
		return nil, err
	}
	for {
		p.skipNewlines()
		if p.cur().Kind == RBRACE {
			p.next()
			break
		}
		if p.cur().Kind == EOF {
			return nil, &Error{Pos: p.cur().Pos, Msg: "unterminated class body"}
		}
		switch p.cur().Kind {
		case VAR:
			p.next()
			n, err := p.expectNameLike()
			if err != nil {
				return nil, err
			}
			typ := "int"
			if p.cur().Kind == COLON {
				p.next()
				tt, err := p.expectIdent()
				if err != nil {
					return nil, err
				}
				typ = tt.Text
			}
			var init Expr
			if p.cur().Kind == ASSIGN {
				p.next()
				init, err = p.parseExpr()
				if err != nil {
					return nil, err
				}
			}
			cd.Fields = append(cd.Fields, FieldDecl{Name: n.Text, Type: typ, Init: init, P: n.Pos})
		case FN, AT, TICK:
			m, err := p.parseMethod()
			if err != nil {
				return nil, err
			}
			cd.Methods = append(cd.Methods, m)
		case ON:
			p.next()
			ev := p.cur()
			if ev.Kind != SPAWN && ev.Kind != DEATH {
				return nil, &Error{Pos: ev.Pos, Msg: "a class hook is on spawn or on death"}
			}
			p.next()
			body, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			cd.Events = append(cd.Events, ClassEvent{Event: ev.Text, Body: body, P: ev.Pos})
		default:
			return nil, &Error{Pos: p.cur().Pos, Msg: fmt.Sprintf("unexpected %s in class body", p.cur().Kind)}
		}
	}
	return cd, nil
}

func (p *parser) parseExtern() (ExternDecl, error) {
	t, err := p.expect(EXTERN)
	if err != nil {
		return ExternDecl{}, err
	}
	d := ExternDecl{P: t.Pos}
	if p.cur().Kind == FN {
		p.next()
		d.Kind = "fn"
	} else {
		// extern <name>: <type> = <holder>.<objective>
		//
		// The holder is written first because 1.12.2 writes it first
		// (`scoreboard players set <holder> <objective>`), and it is spelled
		// as a dotted name so that `@s.arg1` and `seed.math` are the same
		// shape as the codec's `@s.Item`. An earlier spelling stacked two
		// `@` clauses and a `global` keyword whose only content was the
		// holder it then repeated.
		d.Kind = "var"
	}
	if d.Kind == "fn" {
		name, err := p.parsePath()
		if err != nil {
			return d, err
		}
		d.Name = name
		params, err := p.parseParams()
		if err != nil {
			return d, err
		}
		d.Params = params
		if p.cur().Kind == ARROW {
			p.next()
			t, err := p.expectIdent()
			if err != nil {
				return d, err
			}
			d.Type = t.Text
		}
		if _, err := p.expect(LBRACE); err != nil {
			return d, err
		}
		for p.cur().Kind != RBRACE {
			p.skipNewlines()
			if p.cur().Kind == RBRACE {
				break
			}
			if p.cur().Kind == IN || (p.cur().Kind == IDENT && p.cur().Text == "in") {
				p.next()
				for {
					if p.cur().Kind == IDENT && p.cur().Text == "out" {
						break
					}
					a, err := p.expectIdent()
					if err != nil {
						return d, err
					}
					if _, err := p.expect(ASSIGN); err != nil {
						return d, err
					}
					if p.cur().Kind == GT {
						p.next()
					}
					b, err := p.expectName()
					if err != nil {
						return d, err
					}
					d.In = append(d.In, struct{ Param, Reg string }{a.Text, b.Text})
					if p.cur().Kind == COMMA {
						p.next()
						continue
					}
					break
				}
				continue
			}
			if p.cur().Kind == IDENT && p.cur().Text == "out" {
				p.next()
				r, err := p.expectName()
				if err != nil {
					return d, err
				}
				d.Out = r.Text
				continue
			}
			return d, &Error{Pos: p.cur().Pos, Msg: "expected `in` or `out` in extern fn body"}
		}
		p.next()
		return d, nil
	}
	name, err := p.expectName()
	if err != nil {
		return d, err
	}
	d.Name = name.Text
	if p.cur().Kind == COLON {
		p.next()
		t, err := p.expectIdent()
		if err != nil {
			return d, err
		}
		d.Type = t.Text
	}
	if _, err := p.expect(ASSIGN); err != nil {
		return d, err
	}
	// <holder>.<objective>. The holder is either a selector (@s) or the name
	// of a fake player, and everything after the first dot is the objective,
	// because objective names contain dots themselves (wp.damage1).
	if p.cur().Kind == AT {
		sel, err := p.parseSelectorLiteral()
		if err != nil {
			return d, err
		}
		id, ok := sel.(Ident)
		if !ok {
			return d, &Error{Pos: p.cur().Pos, Msg: "expected a selector as the score holder"}
		}
		d.Holder = id.Name
	} else {
		h, err := p.expectName()
		if err != nil {
			return d, err
		}
		d.Holder = h.Text
	}
	if _, err := p.expect(DOT); err != nil {
		return d, err
	}
	obj, err := p.parseDottedName()
	if err != nil {
		return d, err
	}
	d.Objective = obj
	return d, nil
}

func (p *parser) parseOn() (*OnDecl, error) {
	t, err := p.expect(ON)
	if err != nil {
		return nil, err
	}
	d := &OnDecl{P: t.Pos}
	switch p.cur().Kind {
	case LOAD:
		p.next()
		d.Event = "load"
	case TICK:
		p.next()
		d.Event = "tick"
	case EVERY:
		p.next()
		n, err := p.expect(INT)
		if err != nil {
			return nil, err
		}
		unit, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		d.Event = "every"
		d.Every = n.Text + unit.Text
	default:
		return nil, &Error{Pos: p.cur().Pos, Msg: "expected load, tick or every after on"}
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	d.Body = body
	return d, nil
}

func (p *parser) parseBlock() ([]Stmt, error) {
	if _, err := p.expect(LBRACE); err != nil {
		return nil, err
	}
	var out []Stmt
	for {
		p.skipNewlines()
		if p.cur().Kind == RBRACE {
			p.next()
			break
		}
		if p.cur().Kind == EOF {
			return nil, &Error{Pos: p.cur().Pos, Msg: "unterminated block"}
		}
		s, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func (p *parser) parseStmt() (Stmt, error) {
	t := p.cur()
	switch t.Kind {
	case LET:
		p.next()
		n, err := p.expectIdent()
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
		return LetStmt{Name: n.Text, Value: v, P: t.Pos}, nil
	case IF:
		p.next()
		cond, err := p.parseCond()
		if err != nil {
			return nil, err
		}
		then, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		var els []Stmt
		p.skipNewlines()
		if p.cur().Kind == ELSE {
			p.next()
			if p.cur().Kind == IF {
				s, err := p.parseStmt()
				if err != nil {
					return nil, err
				}
				els = []Stmt{s}
			} else {
				els, err = p.parseBlock()
				if err != nil {
					return nil, err
				}
			}
		}
		return IfStmt{Cond: cond, Then: then, Else: els, P: t.Pos}, nil
	case WHILE:
		p.next()
		cond, err := p.parseCond()
		if err != nil {
			return nil, err
		}
		budget := 0
		// #[budget(N)] may appear before or after the condition
		if p.cur().Kind == HASH {
			p.next()
			if _, err := p.expect(LBRACK); err != nil {
				return nil, err
			}
			if _, err := p.expect(BUDGET); err != nil {
				return nil, err
			}
			if _, err := p.expect(LPAREN); err != nil {
				return nil, err
			}
			n, err := p.expect(INT)
			if err != nil {
				return nil, err
			}
			budget = atoi(n.Text)
			if _, err := p.expect(RPAREN); err != nil {
				return nil, err
			}
			if _, err := p.expect(RBRACK); err != nil {
				return nil, err
			}
		}
		body, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		return WhileStmt{Cond: cond, Budget: budget, Body: body, P: t.Pos}, nil
	case FOR:
		p.next()
		n, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(IN); err != nil {
			return nil, err
		}
		sel, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		var where Expr
		if p.cur().Kind == WHERE {
			p.next()
			where, err = p.parseExpr()
			if err != nil {
				return nil, err
			}
		}
		body, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		return ForStmt{Name: n.Text, Sel: sel, Where: where, Body: body, P: t.Pos}, nil
	case LOOP:
		p.next()
		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		budget := 0
		if p.cur().Kind == HASH {
			p.next()
			if _, err := p.expect(LBRACK); err != nil {
				return nil, err
			}
			if _, err := p.expect(BUDGET); err != nil {
				return nil, err
			}
			if _, err := p.expect(LPAREN); err != nil {
				return nil, err
			}
			n, err := p.expect(INT)
			if err != nil {
				return nil, err
			}
			budget = atoi(n.Text)
			if _, err := p.expect(RPAREN); err != nil {
				return nil, err
			}
			if _, err := p.expect(RBRACK); err != nil {
				return nil, err
			}
		}
		body, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		return LoopStmt{Cond: cond, Budget: budget, Body: body, P: t.Pos}, nil
	case RETURN:
		p.next()
		if p.cur().Kind == NEWLINE || p.cur().Kind == SEMI || p.cur().Kind == RBRACE {
			return ReturnStmt{P: t.Pos}, nil
		}
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return ReturnStmt{Value: v, P: t.Pos}, nil
	case DECODE:
		return p.parseDecode()
	case ENCODE:
		return p.parseEncode()
	case AT:
		// broadcast write: @sel field = value
		sel, err := p.parseSelectorLiteral()
		if err != nil {
			return nil, err
		}
		p.skipNewlines()
		f, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(ASSIGN); err != nil {
			return nil, err
		}
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return BroadcastStmt{Sel: sel, Field: f.Text, Value: v, P: t.Pos}, nil
	default:
		x, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if isAssignOp(p.cur().Kind) {
			op := p.next().Kind
			v, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			return AssignStmt{Target: x, Op: op, Value: v, P: t.Pos}, nil
		}
		return ExprStmt{X: x, P: t.Pos}, nil
	}
}

func isAssignOp(k Kind) bool {
	switch k {
	case ASSIGN, PLUSEQ, MINUSEQ, STAREQ, SLASHEQ, PERCENTEQ:
		return true
	}
	return false
}

// parseSelectorLiteral consumes @base[args] into an Ident whose name is the
// rendered selector text. Selectors are opaque strings to the parser.
func (p *parser) parseSelectorLiteral() (Expr, error) {
	t := p.cur()
	if _, err := p.expect(AT); err != nil {
		return nil, err
	}
	base, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	s := "@" + base.Text
	if p.cur().Kind == LBRACK {
		depth := 0
		for {
			if p.cur().Kind == EOF {
				return nil, &Error{Pos: p.cur().Pos, Msg: "unterminated selector"}
			}
			if p.cur().Kind == LBRACK {
				depth++
			}
			if p.cur().Kind == RBRACK {
				depth--
			}
			s += p.cur().Text
			p.next()
			if depth == 0 {
				break
			}
		}
	}
	return Ident{Name: s, P: t.Pos}, nil
}

func (p *parser) parseExpr() (Expr, error) { return p.parseBinary(0) }

// parseCond reads a condition: an expression optionally followed by
// `matches n"..."`.
//
// `matches` stays an ordinary identifier rather than a keyword, the same way
// add and set do, so a program may still name a variable after it.
func (p *parser) parseCond() (Expr, error) {
	// `@s.Item matches n"..."` starts with a selector, which is not an
	// expression form anywhere else in the grammar. Trying the container first
	// and rewinding keeps `@s` usable as a plain selector elsewhere.
	if p.cur().Kind == AT {
		save := p.i
		if c, sel, err := p.tryMatchContainer(); err == nil {
			pos := p.cur().Pos
			p.next() // matches
			if p.cur().Kind != NBT {
				return nil, &Error{Pos: p.cur().Pos, Msg: fmt.Sprintf("expected an NBT literal after matches, got %s", p.cur().Kind)}
			}
			nb := p.next()
			if _, err := CheckNBT(nb.Text); err != nil {
				return nil, &Error{Pos: nb.Pos, Msg: err.Error()}
			}
			return MatchCond{Sel: sel, Container: c, NBT: nb.Text, P: pos}, nil
		}
		p.i = save
	}
	return p.parseExpr()
}

// tryMatchContainer parses `@sel.Key` and requires the word `matches` after it.
// Any other shape rewinds, so an ordinary selector expression still parses.
func (p *parser) tryMatchContainer() (Container, string, error) {
	selExpr, err := p.parseSelectorLiteral()
	if err != nil {
		return Container{}, "", err
	}
	sel, ok := selExpr.(Ident)
	if !ok || sel.Name != "@s" {
		return Container{}, "", fmt.Errorf("not a container")
	}
	if p.cur().Kind != DOT {
		return Container{}, "", fmt.Errorf("not a container")
	}
	p.next()
	key, err := p.expectIdent()
	if err != nil {
		return Container{}, "", err
	}
	switch key.Text {
	case "SelectedItem", "Item", "Inventory":
	default:
		return Container{}, "", fmt.Errorf("not a container")
	}
	if p.cur().Kind != IDENT || p.cur().Text != "matches" {
		return Container{}, "", fmt.Errorf("not a match")
	}
	return Container{Key: key.Text, P: sel.P}, sel.Name, nil
}

var prec = map[Kind]int{
	DOTDOT: 0,
	OR:     1, AND: 2,
	EQ: 3, NEQ: 3,
	LT: 4, LE: 4, GT: 4, GE: 4,
	PLUS: 5, MINUS: 5,
	STAR: 6, SLASH: 6, PERCENT: 6,
}

func (p *parser) parseBinary(minPrec int) (Expr, error) {
	lhs, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		k := p.cur().Kind
		prec1, ok := prec[k]
		if !ok || prec1 < minPrec {
			return lhs, nil
		}
		opTok := p.next()
		rhs, err := p.parseBinary(prec1 + 1)
		if err != nil {
			return nil, err
		}
		lhs = Binary{Op: opTok.Kind, L: lhs, R: rhs, P: opTok.Pos}
	}
}

func (p *parser) parseUnary() (Expr, error) {
	t := p.cur()
	if t.Kind == MINUS || t.Kind == NOT {
		p.next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return Unary{Op: t.Kind, X: x, P: t.Pos}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.cur()
	switch t.Kind {
	case INT:
		p.next()
		return IntLit{Value: atoi(t.Text), P: t.Pos}, nil
	case STRING:
		p.next()
		return StringLit{Value: t.Text, P: t.Pos}, nil
	case TRUE:
		p.next()
		return BoolLit{Value: true, P: t.Pos}, nil
	case FALSE:
		p.next()
		return BoolLit{Value: false, P: t.Pos}, nil
	case SELF:
		p.next()
		return SelfExpr{P: t.Pos}, nil
	case AT:
		return p.parseSelectorLiteral()
	case LBRACK:
		// list literal, used by comptime for-loops: [1, 2, 4]
		p.next()
		var elems []Expr
		for p.cur().Kind != RBRACK {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			elems = append(elems, e)
			if p.cur().Kind == COMMA {
				p.next()
			}
		}
		p.next()
		return ListLit{Elems: elems, P: t.Pos}, nil
	case LPAREN:
		p.next()
		x, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(RPAREN); err != nil {
			return nil, err
		}
		return x, nil
	case TICK, SPAWN, DEATH, LOAD, EVERY:
		// A nameable keyword can only start an expression as a call.
		p.next()
		if p.cur().Kind != LPAREN {
			return nil, &Error{Pos: t.Pos, Msg: fmt.Sprintf("%s cannot start an expression", t.Text)}
		}
		return p.finishCall(t.Text, t.Pos)
	case EMIT:
		p.next()
		if p.cur().Kind != LPAREN {
			return nil, &Error{Pos: t.Pos, Msg: "expected ( after emit"}
		}
		call, err := p.finishCall("emit", t.Pos)
		if err != nil {
			return nil, err
		}
		return call, nil
	case IDENT:
		p.next()
		name := t.Text
		// namespaced path: luo:math/sqrt
		if p.cur().Kind == COLON {
			p.next()
			var parts []string
			for {
				seg, err := p.expectIdent()
				if err != nil {
					return nil, err
				}
				parts = append(parts, seg.Text)
				if p.cur().Kind == SLASH {
					p.next()
					continue
				}
				break
			}
			name += ":" + join(parts, "/")
		}
		// dotted identifiers
		for p.cur().Kind == DOT {
			p.next()
			seg, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			name += "." + seg.Text
		}
		// quantified builtins: any<@sel>, count<@sel>, reduce<min>(@sel, obj)
		if p.cur().Kind == LT && (name == "any" || name == "count" || name == "reduce") {
			p.next() // <
			op := ""
			for p.cur().Kind != GT {
				if p.cur().Kind == EOF {
					return nil, &Error{Pos: p.cur().Pos, Msg: "unterminated <...> in " + name}
				}
				op += p.cur().Text
				p.next()
			}
			p.next() // >
			callName := name
			var args []Expr
			if name == "reduce" {
				callName = "reduce<" + op + ">"
			} else {
				// any<@sel> / count<@sel>: the selector is the argument
				if op != "" {
					args = append(args, Ident{Name: op, P: t.Pos})
				}
			}
			if p.cur().Kind == LPAREN {
				p.next()
				for p.cur().Kind != RPAREN {
					a, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.cur().Kind == COMMA {
						p.next()
					}
				}
				p.next()
			}
			return Call{Name: callName, Args: args, P: t.Pos}, nil
		}
		if p.cur().Kind == LPAREN {
			return p.finishCall(name, t.Pos)
		}
		return Ident{Name: name, P: t.Pos}, nil
	case LT:
		// reduce<min>(...)
		p.next()
		op, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(GT); err != nil {
			return nil, err
		}
		if _, err := p.expect(LPAREN); err != nil {
			return nil, err
		}
		var args []Expr
		for p.cur().Kind != RPAREN {
			a, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, a)
			if p.cur().Kind == COMMA {
				p.next()
			}
		}
		p.next()
		return Call{Name: "reduce<" + op.Text + ">", Args: args, P: t.Pos}, nil
	}
	return nil, &Error{Pos: t.Pos, Msg: fmt.Sprintf("unexpected %s in expression", t.Kind)}
}

// finishCall parses "(" args ")" given the current token is "(".
func (p *parser) finishCall(name string, pos Pos) (Expr, error) {
	if _, err := p.expect(LPAREN); err != nil {
		return nil, err
	}
	var args []Expr
	for p.cur().Kind != RPAREN {
		a, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, a)
		if p.cur().Kind == COMMA {
			p.next()
		}
	}
	p.next()
	return Call{Name: name, Args: args, P: pos}, nil
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			continue
		}
		n = n*10 + int(c-'0')
	}
	return n
}
