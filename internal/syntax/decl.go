// Declarations: the things a file is made of.
//
// fn, class, method, extern, obj, nbt -- and the duration that a period
// attribute resolves to.
package syntax

import "fmt"

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

func (p *parser) parseFunc(attrs []Attr) (*FuncDecl, error) {
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
	every, err := everyTicks(attrs)
	if err != nil {
		return nil, err
	}
	return &FuncDecl{Name: name.Text, Params: params, RetType: ret, Body: body, Attrs: attrs, EveryTicks: every, P: t.Pos}, nil
}

// parseMethod parses one method with its attribute list.
func (p *parser) parseMethod() (Method, error) {
	attrs, err := p.parseAttrs()
	if err != nil {
		return Method{}, err
	}
	p.skipNewlines()
	if err := checkAttrs(attrs, "method"); err != nil {
		return Method{}, err
	}
	every, err := everyTicks(attrs)
	if err != nil {
		return Method{}, err
	}
	fd, err := p.parseFunc(attrs)
	if err != nil {
		return Method{}, err
	}
	return Method{Name: fd.Name, Params: fd.Params, Body: fd.Body, Attrs: attrs, EveryTicks: every, P: fd.P}, nil
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

func (p *parser) parseClass(attrs []Attr) (*ClassDecl, error) {
	ct, err := p.expect(CLASS)
	if err != nil {
		return nil, err
	}
	name, err := p.expectNameLike()
	if err != nil {
		return nil, err
	}
	sel, err := selectorArg(attrs)
	if err != nil {
		return nil, err
	}
	every, err := everyTicks(attrs)
	if err != nil {
		return nil, err
	}
	cd := &ClassDecl{Name: name.Text, Selector: sel, Attrs: attrs, EveryTicks: every, P: ct.Pos}
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
		case FN, LBRACK:
			m, err := p.parseMethod()
			if err != nil {
				return nil, err
			}
			cd.Methods = append(cd.Methods, m)
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

// ParseDuration converts a duration literal into game ticks.
//
// The game tick is the smallest unit 1.12.2 has, so this is the ONE place that
// knows the conversion. A separate parser elsewhere made `5s` and `100ms`
// both become p.100, and two periods shared a counter.
func ParseDuration(text string) (int, bool) {
	i := 0
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		i++
	}
	if i == 0 || i == len(text) {
		return 0, false
	}
	count := 0
	for _, c := range text[:i] {
		count = count*10 + int(c-'0')
	}
	switch text[i:] {
	case "tick", "ticks":
		return count, count > 0
	case "s", "sec", "secs", "second", "seconds":
		return count * 20, count > 0
	case "m", "min", "mins", "minute", "minutes":
		return count * 20 * 60, count > 0
	}
	return 0, false
}
