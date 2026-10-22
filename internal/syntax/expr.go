// Expressions.
package syntax

import "fmt"

// parseSelectorLiteral consumes @base[args] into an Ident whose name is the
// rendered selector text. Selectors are opaque strings to the parser.
func (p *parser) parseSelectorLiteral() (Expr, error) {
	t := p.cur()
	// A bare name is a selector SET: `sel Monsters = @e[tag=mob]` and then
	// `entities<Monsters>`. It is resolved when the program is lowered, because
	// the table is module-wide and the parser sees one file at a time.
	if t.Kind == IDENT {
		p.next()
		return Ident{Name: t.Text, P: t.Pos}, nil
	}
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
	// Any identifier is a container. The old whitelist held exactly the three
	// the codec's `decode`/`encode` know about, which is a different question:
	// `matches` tests an arbitrary NBT pattern on the entity, and the data pack
	// uses it on ActiveEffects, Inventory, SelectedItem and others.
	//
	// The three named containers still matter for `decode`, where the slot
	// range has to be meaningful; `matches` only needs to know where to point
	// the pattern, and the pattern itself says the rest.
	_ = key
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

// parsePathSegments reads `a/b/c` and returns it joined by slashes.
//
// A segment may be a NUMBER: the reference data pack names functions
// `rpg:skill/1/main`, where the directory is a slot or a tier.
func (p *parser) parsePathSegments() string {
	var parts []string
	for {
		var seg string
		if p.cur().Kind == INT {
			seg = p.next().Text
		} else {
			tok, err := p.expectIdent()
			if err != nil {
				return join(parts, "/")
			}
			seg = tok.Text
		}
		parts = append(parts, seg)
		if p.cur().Kind == SLASH {
			p.next()
			continue
		}
		break
	}
	return join(parts, "/")
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.cur()
	switch t.Kind {
	case INT:
		p.next()
		// Report the digits the author wrote, not a wrapped value.
		//
		// The range check downstream can only complain about the number it
		// receives, and a silent wrap made that number unrelated to the
		// source: 99999999999999999999999 arrived as 200376420520689663 and
		// the error named that.
		v, ok := atoiChecked(t.Text)
		if !ok {
			return nil, &Error{Pos: t.Pos, Msg: fmt.Sprintf(
				"integer literal %s is too large to represent; 1.12.2 scores are 32-bit signed integers",
				t.Text)}
		}
		return IntLit{Value: v, P: t.Pos}, nil
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
		if p.cur().Kind != DOT {
			return SelfExpr{P: t.Pos}, nil
		}
		// `self.<objective>` -- a score on the executing entity.
		//
		// The lowerer has accepted Field{X: SelfExpr} since it was written,
		// but nothing ever built one: the parser returned a bare SelfExpr and
		// the following dot was a syntax error. So `self.sort.key` -- the
		// spelling the specification used -- did not parse, while three
		// sites in internal/lower were written to receive it.
		//
		// The whole dotted name belongs to the objective, exactly as it does
		// for `seed.math`. There is no competing reading here: unlike a
		// selector, `self` can never itself be an objective name, so the dots
		// cannot be split two ways and no quoting is needed -- which is why
		// `@p[tag=t]."Ap.Amount"` has to quote and this does not.
		p.next()
		name, err := p.parseDottedName()
		if err != nil {
			return nil, err
		}
		return Field{X: SelfExpr{P: t.Pos}, Name: name, P: t.Pos}, nil
	case AT:
		// A selector may be followed by `.field`, which reads THAT entity's
		// score: `@p[tag=target].Ap.Amount`.
		//
		// The field is written as a string because an objective name may
		// contain dots, and `@p.Ap.Amount` cannot be split unambiguously --
		// exactly the ambiguity that made `Ap.Amount` a broken objective name
		// until declaration was allowed to win. A selector is never a declared
		// objective, so here the quoted form is the only honest spelling:
		//
		//	@e[tag=mob]."M.UID"
		//
		// The dot before it keeps the shape of `self.x` and `seed.x`, which is
		// the whole reason to spell it this way rather than with a helper.
		sel, err := p.parseSelectorLiteral()
		if err != nil {
			return nil, err
		}
		if p.cur().Kind != DOT {
			return sel, nil
		}
		p.next()
		if p.cur().Kind != STRING {
			return nil, &Error{Pos: p.cur().Pos, Msg: "expected a quoted objective name after the selector's dot, e.g. @p[tag=t].\"Ap.Amount\""}
		}
		obj := p.next()
		return Field{X: sel, Name: obj.Text, P: t.Pos}, nil
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
	case NBT:
		// `n"..."` used to be legal only where the grammar spelled it out:
		// a `matches` condition and an `nbt` declaration. That made the NBT
		// codec unreachable from the one place it is most useful -- the
		// argument of a command, `summon("armor_stand", n"{Tags:[\"x\"]}")`.
		//
		// It is a literal, not a value: no score can hold it, so it only ever
		// reaches a builtin that wants raw command text.
		p.next()
		if _, err := CheckNBT(t.Text); err != nil {
			return nil, &Error{Pos: t.Pos, Msg: err.Error()}
		}
		return NBTLit{Text: t.Text, P: t.Pos}, nil
	case IDENT:
		p.next()
		name := t.Text
		// A namespaced path: `luo:math/sqrt`.
		//
		// Only WITH the colon. A bare `math/sqrt(4)` is indistinguishable from
		// `math / sqrt(4)` -- both are a valid division whose right operand is
		// a call -- so treating `/` as a path separator here silently turned
		// every `a / b` into a call to a function `a/b`. That broke division
		// across the whole language to make one spelling work.
		//
		// The cost is that a bare path call must be spelled with its domain:
		// `luo:math/sqrt(4)`. That is the same rule a selector and an objective
		// follow, and it is unambiguous.
		if p.cur().Kind == COLON {
			p.next()
			name += ":" + p.parsePathSegments()
		}
		// Dotted identifiers.
		//
		// The segment after the dot goes through expectName, not expectIdent:
		// objective names are often words the language reserves. `return` is
		// the ABI return register in every 1.12.2 library, so `e.return` has to
		// parse -- the same reason parseDottedName uses expectName for the
		// first segment.
		for p.cur().Kind == DOT {
			p.next()
			seg, err := p.expectName()
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

// atoi parses a run of decimal digits.
//
// It wraps silently, which is wrong for anything that ends up in a score:
// 99999999999999999999999 became 200376420520689663, and the range check then
// reported that invented number instead of the one the author wrote. Callers
// that care use atoiChecked.
func atoi(s string) int {
	n, _ := atoiChecked(s)
	return n
}

// atoiChecked parses a run of decimal digits, reporting whether the value fit.
//
// ok is false when the digits overflow int64. The returned value is then
// meaningless, and a caller that puts it in a score would emit a number the
// author never wrote -- so every such caller must check ok.
func atoiChecked(s string) (int, bool) {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			continue
		}
		d := int64(c - '0')
		if n > (1<<63-1-d)/10 {
			return 0, false
		}
		n = n*10 + d
	}
	return int(n), true
}
