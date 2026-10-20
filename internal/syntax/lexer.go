package syntax

import (
	"fmt"
	"strings"
	"unicode"
)

// Lexer turns source bytes into tokens. It keeps newlines as tokens because
// GGM uses newline as the statement separator (semicolons are optional).
type Lexer struct {
	src  []rune
	i    int
	line int
	col  int
}

func NewLexer(src string) *Lexer {
	return &Lexer{src: []rune(src), line: 1, col: 1}
}

func (l *Lexer) peek() rune {
	if l.i >= len(l.src) {
		return 0
	}
	return l.src[l.i]
}

func (l *Lexer) peekAt(n int) rune {
	if l.i+n >= len(l.src) {
		return 0
	}
	return l.src[l.i+n]
}

func (l *Lexer) advance() rune {
	if l.i >= len(l.src) {
		return 0
	}
	c := l.src[l.i]
	l.i++
	if c == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return c
}

func (l *Lexer) pos() Pos { return Pos{Line: l.line, Col: l.col} }

// Lex returns all tokens including NEWLINE and a trailing EOF.
func (l *Lexer) Lex() ([]Token, error) {
	var toks []Token
	for {
		// skip horizontal whitespace and comments
		for {
			c := l.peek()
			if c == ' ' || c == '\t' || c == '\r' {
				l.advance()
				continue
			}
			if c == '/' && l.peekAt(1) == '/' {
				for l.peek() != 0 && l.peek() != '\n' {
					l.advance()
				}
				continue
			}
			if c == '#' && l.peekAt(1) == '[' {
				break
			}
			break
		}

		p := l.pos()
		c := l.peek()

		switch {
		case c == 0:
			toks = append(toks, Token{Kind: EOF, Pos: p})
			return toks, nil
		case c == '\n':
			l.advance()
			toks = append(toks, Token{Kind: NEWLINE, Text: "\\n", Pos: p})
		case unicode.IsLetter(c) || c == '_':
			start := l.i
			for unicode.IsLetter(l.peek()) || unicode.IsDigit(l.peek()) || l.peek() == '_' {
				l.advance()
			}
			word := string(l.src[start:l.i])
			if k, ok := keywords[word]; ok {
				toks = append(toks, Token{Kind: k, Text: word, Pos: p})
			} else {
				toks = append(toks, Token{Kind: IDENT, Text: word, Pos: p})
			}
		case unicode.IsDigit(c):
			start := l.i
			for unicode.IsDigit(l.peek()) {
				l.advance()
			}
			toks = append(toks, Token{Kind: INT, Text: string(l.src[start:l.i]), Pos: p})
		case c == '"':
			l.advance()
			var sb strings.Builder
			for l.peek() != 0 && l.peek() != '"' {
				if l.peek() == '\\' {
					l.advance()
					esc := l.advance()
					switch esc {
					case 'n':
						sb.WriteRune('\n')
					case 't':
						sb.WriteRune('\t')
					default:
						sb.WriteRune(esc)
					}
					continue
				}
				sb.WriteRune(l.advance())
			}
			if l.peek() != '"' {
				return nil, fmt.Errorf("%s: unterminated string literal", p)
			}
			l.advance()
			toks = append(toks, Token{Kind: STRING, Text: sb.String(), Pos: p})
		default:
			t, err := l.lexOperator(p)
			if err != nil {
				return nil, err
			}
			toks = append(toks, t)
		}
	}
}

func (l *Lexer) lexOperator(p Pos) (Token, error) {
	c := l.advance()
	two := string(c) + string(l.peek())
	switch two {
	case "->":
		l.advance()
		return Token{Kind: ARROW, Text: two, Pos: p}, nil
	case "==":
		l.advance()
		return Token{Kind: EQ, Text: two, Pos: p}, nil
	case "!=":
		l.advance()
		return Token{Kind: NEQ, Text: two, Pos: p}, nil
	case "<=":
		l.advance()
		return Token{Kind: LE, Text: two, Pos: p}, nil
	case ">=":
		l.advance()
		return Token{Kind: GE, Text: two, Pos: p}, nil
	case "+=":
		l.advance()
		return Token{Kind: PLUSEQ, Text: two, Pos: p}, nil
	case "-=":
		l.advance()
		return Token{Kind: MINUSEQ, Text: two, Pos: p}, nil
	case "*=":
		l.advance()
		return Token{Kind: STAREQ, Text: two, Pos: p}, nil
	case "/=":
		l.advance()
		return Token{Kind: SLASHEQ, Text: two, Pos: p}, nil
	case "%=":
		l.advance()
		return Token{Kind: PERCENTEQ, Text: two, Pos: p}, nil
	case "..":
		l.advance()
		return Token{Kind: DOTDOT, Text: two, Pos: p}, nil
	case "&&":
		l.advance()
		return Token{Kind: AND, Text: two, Pos: p}, nil
	case "||":
		l.advance()
		return Token{Kind: OR, Text: two, Pos: p}, nil
	}
	single := map[rune]Kind{
		'(': LPAREN, ')': RPAREN, '{': LBRACE, '}': RBRACE,
		'[': LBRACK, ']': RBRACK, ':': COLON, ';': SEMI, ',': COMMA,
		'.': DOT, '@': AT, '#': HASH, '=': ASSIGN, '+': PLUS,
		'-': MINUS, '*': STAR, '/': SLASH, '%': PERCENT,
		'<': LT, '>': GT, '!': NOT,
	}
	if k, ok := single[c]; ok {
		return Token{Kind: k, Text: string(c), Pos: p}, nil
	}
	return Token{}, fmt.Errorf("%s: unexpected character %q", p, c)
}
