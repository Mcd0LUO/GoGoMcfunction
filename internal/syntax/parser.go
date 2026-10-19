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

// Parse parses a CMCF source file.
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
// with CMCF keywords, so extern bindings must be able to spell them.
func (p *parser) expectName() (Token, error) {
	if p.cur().Kind == IDENT || p.cur().Text != "" {
		return p.next(), nil
	}
	return Token{}, &Error{Pos: p.cur().Pos, Msg: fmt.Sprintf("expected a name, got %s", p.cur().Kind)}
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
var funcAttrs = map[string]bool{"wrapping": true}

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
	name, err := p.expectIdent()
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
	return &FuncDecl{Name: name.Text, Params: params, RetType: ret, Body: body, Wrapping: wrapping, P: t.Pos}, nil
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
	name, err := p.expectIdent()
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
			n, err := p.expectIdent()
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
	switch p.cur().Kind {
	case VAR:
		p.next()
		d.Kind = "var"
	case GLOBAL:
		p.next()
		if p.cur().Kind == VAR {
			p.next()
		}
		d.Kind = "global"
	case FN:
		p.next()
		d.Kind = "fn"
	default:
		return d, &Error{Pos: p.cur().Pos, Msg: "expected var, global or fn after extern"}
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
	if p.cur().Kind == AT {
		p.next()
		s, err := p.expect(STRING)
		if err != nil {
			return d, err
		}
		d.Objective = s.Text
	}
	if p.cur().Kind == AT {
		p.next()
		h, err := p.expectName()
		if err != nil {
			return d, err
		}
		// @holder "name" binds a specific score holder on an external objective
		if h.Text == "holder" && p.cur().Kind == STRING {
			s := p.next()
			d.Holder = s.Text
		} else {
			d.Holder = h.Text
		}
	}
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
		cond, err := p.parseExpr()
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
		cond, err := p.parseExpr()
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
