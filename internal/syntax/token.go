package syntax

import "fmt"

// Pos is a source position. Every diagnostic carries one; the quality of these
// is what separates a usable language from a toy.
type Pos struct {
	Line int // 1-based
	Col  int // 1-based, in runes
}

func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

type Kind int

const (
	EOF Kind = iota
	NEWLINE
	IDENT
	INT
	STRING
	// NBT is `n"..."`: an NBT value in SNBT form.
	NBT
	// NBTPATH is `p"..."`: a key path from the entity root.
	NBTPATH
	// keywords
	NAMESPACE
	COMPTIME
	CONST
	EXTERN
	FN
	CLASS
	SELECTOR
	LET
	IF
	ELSE
	WHILE
	FOR
	IN
	STEP
	RETURN
	SELF
	TRUE
	FALSE
	VAR
	ENTITIES
	WHERE
	BUDGET
	WRAPPING
	LOOP
	OBJ
	BITS
	ENUM
	DECODE
	ENCODE
	OVER
	NBTDECL
	// SEL declares a named set of selector filters.
	SEL
	// punctuation
	LPAREN
	RPAREN
	LBRACE
	RBRACE
	LBRACK
	RBRACK
	COLON
	SEMI
	COMMA
	DOT
	DOTDOT
	AT
	HASH
	ASSIGN
	PLUS
	MINUS
	STAR
	SLASH
	PERCENT
	EQ
	NEQ
	LT
	LE
	GT
	GE
	ARROW
	PLUSEQ
	MINUSEQ
	STAREQ
	SLASHEQ
	PERCENTEQ
	LTLT
	GTGT
	AND
	OR
	NOT
)

var keywords = map[string]Kind{
	"namespace": NAMESPACE, "comptime": COMPTIME, "const": CONST,
	"extern": EXTERN, "fn": FN, "class": CLASS, "selector": SELECTOR,
	"let": LET, "if": IF, "else": ELSE, "while": WHILE,
	"for": FOR, "in": IN, "step": STEP, "return": RETURN,
	"self": SELF, "true": TRUE, "false": FALSE, "none": FALSE,
	"var": VAR, "entities": ENTITIES, "where": WHERE,
	"budget": BUDGET, "wrapping": WRAPPING,
	"loop": LOOP, "obj": OBJ,
	"bits": BITS, "enum": ENUM, "decode": DECODE, "encode": ENCODE, "over": OVER,
	"nbt": NBTDECL, "sel": SEL,
}

func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

var kindNames = map[Kind]string{
	EOF: "end of file", NEWLINE: "newline", IDENT: "identifier",
	INT: "integer", STRING: "string literal",
	NBT: "NBT value", NBTPATH: "NBT path",
	NAMESPACE: "namespace", COMPTIME: "comptime", CONST: "const",
	EXTERN: "extern", FN: "fn", CLASS: "class", SELECTOR: "selector",
	LET: "let", IF: "if", ELSE: "else", WHILE: "while",
	FOR: "for", IN: "in", STEP: "step", RETURN: "return",
	SELF: "self", TRUE: "true", FALSE: "false", VAR: "var",
	ENTITIES: "entities", WHERE: "where",
	BUDGET: "budget", WRAPPING: "wrapping", LOOP: "loop", OBJ: "obj",
	BITS: "bits", ENUM: "enum", DECODE: "decode", ENCODE: "encode", OVER: "over",
	NBTDECL: "nbt",
	SEL:     "sel",
	LPAREN:  "(", RPAREN: ")", LBRACE: "{", RBRACE: "}",
	LBRACK: "[", RBRACK: "]", COLON: ":", SEMI: ";", COMMA: ",",
	DOT: ".", DOTDOT: "..", AT: "@", HASH: "#", ASSIGN: "=",
	PLUS: "+", MINUS: "-", STAR: "*", SLASH: "/", PERCENT: "%",
	EQ: "==", NEQ: "!=", LT: "<", LE: "<=", GT: ">", GE: ">=",
	ARROW: "->", PLUSEQ: "+=", MINUSEQ: "-=", STAREQ: "*=",
	SLASHEQ: "/=", PERCENTEQ: "%=", LTLT: "<<", GTGT: ">>",
	AND: "&&", OR: "||", NOT: "!",
}

// Token is a lexed token.
type Token struct {
	Kind Kind
	Text string
	Pos  Pos
}

func (t Token) String() string {
	if t.Text != "" {
		return fmt.Sprintf("%s(%q)", t.Kind, t.Text)
	}
	return t.Kind.String()
}
