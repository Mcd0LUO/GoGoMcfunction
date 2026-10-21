// Expressions.
package syntax

import "fmt"

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
	case ENTITIES:
		// `entities<@sel>` is the fan-out source in `for e in entities<...>`.
		// It is a keyword rather than an identifier so the `for` header reads
		// as one phrase, which means the expression parser has to accept it
		// where an identifier would otherwise be expected.
		p.next()
		if p.cur().Kind != LT {
			return nil, &Error{Pos: p.cur().Pos, Msg: "expected '<' after entities"}
		}
		p.next()
		sel, err := p.parseSelectorLiteral()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(GT); err != nil {
			return nil, err
		}
		id, ok := sel.(Ident)
		if !ok {
			return nil, &Error{Pos: t.Pos, Msg: "entities<...> needs a selector"}
		}
		return id, nil
	case SELECTOR, WRAPPING:
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
