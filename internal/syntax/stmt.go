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
	// A statement may carry attributes. `[` never starts a statement
	// otherwise -- a bare list literal is not a statement -- so the bracket is
	// unambiguous here.
	//
	// Today only `[add]` exists, and only on `decode`. It is still parsed as a
	// general attribute list so that a wrong one gets the message "[add] is the
	// only attribute a statement takes" instead of a parse error about `[`.
	if t.Kind == LBRACK {
		attrs, err := p.parseAttrs()
		if err != nil {
			return nil, err
		}
		p.skipNewlines()
		st, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		return applyStmtAttrs(st, attrs)
	}
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
		// `@p[tag=t]."Ap.Amount"` is the READ spelling, so reaching it on the
		// left of an `=` means the author expected to write that entity's
		// score. 1.12.2 cannot: a selector is allowed on the right of a
		// scoreboard operation and not on the left. Saying so beats letting
		// expectName report a stray dot.
		if p.cur().Kind == DOT {
			return nil, &Error{Pos: p.cur().Pos, Msg: "a selector's score can be read but not assigned to: 1.12.2 allows a selector on the right of a scoreboard operation, not the left. To write it, use for e in entities<...> and assign inside"}
		}
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
	// Tried before any other selector form: `@s.ActiveEffects matches n"..."`
	// is a condition, while `@s."objective"` is a score read. The `matches`
	// keyword is what distinguishes them, and tryMatchContainer rewinds when it
	// is absent.
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

// applyStmtAttrs applies a statement's attribute list, returning the statement.
//
// It returns a Stmt rather than mutating one because the AST nodes are value
// types: `st.(DecodeStmt)` hands back a copy, so setting a field on it would
// be discarded and the attribute would be silently dropped -- exactly the
// failure mode this whole file is written to avoid.
//
// Only one attribute exists: `[add]` on `decode`, which skips the reset so
// several decodes can contribute to one objective. Anything else is an error
// rather than being ignored, because an attribute nobody applies is a modifier
// the author believes they wrote.
func applyStmtAttrs(st Stmt, attrs []Attr) (Stmt, error) {
	for _, a := range attrs {
		if a.Name != "add" {
			return nil, &Error{Pos: a.P, Msg: fmt.Sprintf("[%s] is not an attribute a statement takes; [add] is the only one, and it applies to decode", a.Name)}
		}
		if len(a.Args) != 0 {
			return nil, &Error{Pos: a.P, Msg: "[add] takes no arguments"}
		}
		d, ok := st.(DecodeStmt)
		if !ok {
			return nil, &Error{Pos: a.P, Msg: "[add] applies to decode, because it means 'do not reset the objective first'"}
		}
		d.Add = true
		st = d
	}
	return st, nil
}
