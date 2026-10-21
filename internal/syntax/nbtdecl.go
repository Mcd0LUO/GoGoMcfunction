// The container path a decode/encode statement reads from.
//
// This is NOT the NBT codec -- that lives in nbt.go and knows what an SNBT
// literal means. This file only knows where to look for one.
package syntax

import "fmt"

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
		// A slot may be negative: 1.12.2 puts the offhand at -106 and the
		// armour slots at 100..103, and the reference data pack reads all of
		// them. Parsing only unsigned integers made the whole offhand
		// mechanism -- a shield in the second hand -- inexpressible.
		lo, loPos, err := p.parseSignedInt()
		if err != nil {
			return Container{}, err
		}
		c.Lo, c.Hi = lo, lo
		if p.cur().Kind == DOTDOT {
			p.next()
			hi, _, err := p.parseSignedInt()
			if err != nil {
				return Container{}, err
			}
			c.Hi = hi
		}
		if _, err := p.expect(RPAREN); err != nil {
			return Container{}, err
		}
		if c.Hi < c.Lo {
			return Container{}, &Error{Pos: loPos, Msg: fmt.Sprintf("slot range %d..%d is empty", c.Lo, c.Hi)}
		}
		return c, nil
	}
	return Container{}, &Error{Pos: key.Pos, Msg: fmt.Sprintf("unknown container %q; expected SelectedItem, Inventory or Item", key.Text)}
}

// parseSignedInt reads an integer literal with an optional leading minus.
//
// `expect(INT)` cannot: the minus is its own token, so a negative literal
// arrives as MINUS followed by INT. Only a handful of positions need a
// negative constant -- an inventory slot is one -- so this stays a small
// helper rather than a change to every integer site.
func (p *parser) parseSignedInt() (int, Pos, error) {
	pos := p.cur().Pos
	neg := false
	if p.cur().Kind == MINUS {
		p.next()
		neg = true
	}
	t, err := p.expect(INT)
	if err != nil {
		return 0, pos, err
	}
	v := atoi(t.Text)
	if neg {
		v = -v
	}
	return v, pos, nil
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
	// The accumulate mode is the `[add]` attribute on the statement, not a
	// trailing `into`.
	//
	// `into` trailed here and took no argument, which reads as a preposition
	// with its object missing: "decode atk from the inventory into" -- into
	// what? The answer was "into itself", and the reader had to know that.
	// What the word actually announces is a COST: no reset command is emitted,
	// so the base value is the author's business. This language writes
	// modifiers in brackets, so it is [add].
	//
	// The old spelling is still recognised, to be told what replaced it rather
	// than left with a bare parse error.
	if p.cur().Text == "into" {
		return nil, &Error{Pos: p.cur().Pos, Msg: "the accumulate mode is an attribute now: write `[add] decode ...`, because `into` with no object after it did not say what it modified"}
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
