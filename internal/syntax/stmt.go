// Statements.
package syntax

import "fmt"

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
		budget, err := p.parseBudget()
		if err != nil {
			return nil, err
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
		budget, err := p.parseBudget()
		if err != nil {
			return nil, err
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
		// The field is an objective name, and objective names are often words
		// the language reserves: `return` is the ABI return register in every
		// 1.12.2 library, so `@a[tag=x] return = 1` has to parse.
		f, err := p.expectName()
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

// parseBudget reads the mandatory `<cond>, budget <n>` bound of a loop.
//
// The comma is what separates the two things a loop header says: what to test,
// and how long it may run. Without it the bound reads as a continuation of the
// condition, which is exactly the ambiguity `while i < n budget 64` would have.
//
// It is a clause rather than `#[budget(N)]` because it is an operand, not a
// decoration: a loop without it is a parse error. The attribute spelling
// implied it was optional, and a mandatory thing dressed as an optional one is
// a trap for the reader.
func (p *parser) parseBudget() (int, error) {
	if p.cur().Kind == HASH {
		// The one thing an author migrating from the old spelling will have
		// written here. Naming the replacement beats repeating the general
		// message at a position that looks like an attribute.
		return 0, &Error{Pos: p.cur().Pos, Msg: "the loop bound is now written \", budget <n>\"; #[budget(N)] was removed"}
	}
	if err := p.expectComma(); err != nil {
		return 0, &Error{Pos: p.cur().Pos, Msg: "a loop needs an iteration bound: while <cond>, budget <n> { ... }"}
	}
	if _, err := p.expect(BUDGET); err != nil {
		return 0, &Error{Pos: p.cur().Pos, Msg: "expected \"budget <n>\" after the comma"}
	}
	n, err := p.expect(INT)
	if err != nil {
		return 0, err
	}
	if v := atoi(n.Text); v > 0 {
		return v, nil
	}
	return 0, &Error{Pos: n.Pos, Msg: "the iteration bound must be at least 1"}
}

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
