package syntax

import (
	"fmt"
	"strings"
)

// NBTKind is the tag type of an NBT literal, as 1.12.2 names it.
type NBTKind int

const (
	NBTInvalid NBTKind = iota
	NBTCompound
	NBTList
	NBTString
	NBTNumber
)

func (k NBTKind) String() string {
	switch k {
	case NBTCompound:
		return "compound"
	case NBTList:
		return "list"
	case NBTString:
		return "string"
	case NBTNumber:
		return "number"
	}
	return "invalid"
}

// CheckNBT validates the shape of an SNBT literal and reports its outer type.
//
// It is a SHAPE check, not a parser: it does not build a value tree and it does
// not decide whether `[0b,1L,"x",{}]` is a legal list, because 1.12.2's own
// answer to that is subtle and the runtime (GoMcRuntime) is the authority on
// it. What it does decide is everything the lexer cannot: unbalanced brackets,
// a missing colon, an empty value, and an unterminated string.
//
// Reimplementing full SNBT here would create a second definition of NBT that
// could drift from the runtime's -- the same failure the project already fixed
// once by making ir.Negate the only complement implementation.
func CheckNBT(s string) (NBTKind, error) {
	c := &nbtCheck{src: s}
	c.space()
	if c.eof() {
		return NBTInvalid, fmt.Errorf("empty NBT literal")
	}
	k, err := c.value()
	if err != nil {
		return NBTInvalid, err
	}
	c.space()
	if !c.eof() {
		return NBTInvalid, fmt.Errorf("trailing %q after the value at offset %d", c.src[c.i:], c.i)
	}
	return k, nil
}

type nbtCheck struct {
	src string
	i   int
}

func (c *nbtCheck) eof() bool { return c.i >= len(c.src) }

func (c *nbtCheck) space() {
	for !c.eof() {
		switch c.src[c.i] {
		case ' ', '\t', '\n', '\r':
			c.i++
		default:
			return
		}
	}
}

func (c *nbtCheck) value() (NBTKind, error) {
	if c.eof() {
		return NBTInvalid, fmt.Errorf("unexpected end of NBT literal")
	}
	switch c.src[c.i] {
	case '{':
		return NBTCompound, c.compound()
	case '[':
		return NBTList, c.list()
	case '"', '\'':
		return NBTString, c.quoted()
	}
	return NBTNumber, c.bare()
}

func (c *nbtCheck) compound() error {
	c.i++ // '{'
	for {
		c.space()
		if c.eof() {
			return fmt.Errorf("unterminated compound")
		}
		if c.src[c.i] == '}' {
			c.i++
			return nil
		}
		if err := c.key(); err != nil {
			return err
		}
		c.space()
		if c.eof() || c.src[c.i] != ':' {
			return fmt.Errorf("expected ':' after a key at offset %d", c.i)
		}
		c.i++
		c.space()
		if c.eof() || c.src[c.i] == ',' || c.src[c.i] == '}' {
			return fmt.Errorf("expected a value after ':' at offset %d", c.i)
		}
		if _, err := c.value(); err != nil {
			return err
		}
		c.space()
		if !c.eof() && c.src[c.i] == ',' {
			c.i++
			continue
		}
	}
}

func (c *nbtCheck) key() error {
	if c.src[c.i] == '"' || c.src[c.i] == '\'' {
		return c.quoted()
	}
	start := c.i
	for !c.eof() {
		switch c.src[c.i] {
		case ':', ' ', '\t', ',', '}':
			if c.i == start {
				return fmt.Errorf("expected a key at offset %d", c.i)
			}
			return nil
		}
		c.i++
	}
	if c.i == start {
		return fmt.Errorf("expected a key at offset %d", c.i)
	}
	return nil
}

func (c *nbtCheck) list() error {
	c.i++ // '['
	// [B;..] and [I;..] are typed arrays, not lists of values.
	if !c.eof() && (c.src[c.i] == 'B' || c.src[c.i] == 'I' || c.src[c.i] == 'L') &&
		c.i+1 < len(c.src) && c.src[c.i+1] == ';' {
		c.i += 2
	}
	for {
		c.space()
		if c.eof() {
			return fmt.Errorf("unterminated list")
		}
		if c.src[c.i] == ']' {
			c.i++
			return nil
		}
		if _, err := c.value(); err != nil {
			return err
		}
		c.space()
		if !c.eof() && c.src[c.i] == ',' {
			c.i++
		}
	}
}

func (c *nbtCheck) quoted() error {
	quote := c.src[c.i]
	c.i++
	for !c.eof() {
		if c.src[c.i] == '\\' {
			c.i += 2
			continue
		}
		if c.src[c.i] == quote {
			c.i++
			return nil
		}
		c.i++
	}
	return fmt.Errorf("unterminated string")
}

// bare reads an unquoted token: a number with an optional type suffix, or a
// bare word, which 1.12.2 treats as a string.
func (c *nbtCheck) bare() error {
	start := c.i
	for !c.eof() {
		switch c.src[c.i] {
		case ',', '}', ']', ' ', '\t', '\n', '\r':
			if c.i == start {
				return fmt.Errorf("expected a value at offset %d", c.i)
			}
			return nil
		}
		c.i++
	}
	if c.i == start {
		return fmt.Errorf("expected a value at offset %d", c.i)
	}
	return nil
}

// CheckNBTPath validates a p"..." path: dot-separated segments, each a
// non-empty run of characters that can appear in a compound key.
func CheckNBTPath(s string) error {
	if s == "" {
		return fmt.Errorf("empty NBT path")
	}
	for _, seg := range strings.Split(s, ".") {
		if seg == "" {
			return fmt.Errorf("NBT path %q has an empty segment", s)
		}
		if strings.ContainsAny(seg, "{}[]:, \"\\") {
			return fmt.Errorf("NBT path segment %q contains a character that cannot appear in a key", seg)
		}
	}
	return nil
}
