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
	// DURATION is `<digits><unit>`, e.g. 5s or 1tick.
	//
	// One token rather than INT followed by an identifier, because the unit
	// includes the keyword `tick`: gluing two tokens in the parser made
	// `on every 1tick` unwritable, and the workaround was to make the plural
	// carry the meaning. A duration is a literal, so the lexer owns it.
	DURATION
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
	ON
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
	GLOBAL
	ENTITIES
	WHERE
	EMIT
	BUDGET
	WRAPPING
	TICK
	LOOP
	OBJ
	BITS
	ENUM
	DECODE
	ENCODE
	OVER
	NBTDECL
	SPAWN
	DEATH
	LOAD
	EVERY
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
	"on": ON, "let": LET, "if": IF, "else": ELSE, "while": WHILE,
	"for": FOR, "in": IN, "step": STEP, "return": RETURN,
	"self": SELF, "true": TRUE, "false": FALSE, "none": FALSE,
	"var": VAR, "global": GLOBAL, "entities": ENTITIES, "where": WHERE,
	"emit": EMIT, "budget": BUDGET, "wrapping": WRAPPING, "tick": TICK,
	"spawn": SPAWN, "death": DEATH, "load": LOAD, "every": EVERY,
	"loop": LOOP, "obj": OBJ,
	"bits": BITS, "enum": ENUM, "decode": DECODE, "encode": ENCODE, "over": OVER,
	"nbt": NBTDECL,
}

func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

var kindNames = map[Kind]string{
	EOF: "end of file", NEWLINE: "newline", IDENT: "identifier",
	INT: "integer", STRING: "string literal", DURATION: "duration",
	NBT: "NBT value", NBTPATH: "NBT path",
	NAMESPACE: "namespace", COMPTIME: "comptime", CONST: "const",
	EXTERN: "extern", FN: "fn", CLASS: "class", SELECTOR: "selector",
	ON: "on", LET: "let", IF: "if", ELSE: "else", WHILE: "while",
	FOR: "for", IN: "in", STEP: "step", RETURN: "return",
	SELF: "self", TRUE: "true", FALSE: "false", VAR: "var",
	GLOBAL: "global", ENTITIES: "entities", WHERE: "where", EMIT: "emit",
	BUDGET: "budget", WRAPPING: "wrapping", TICK: "tick", SPAWN: "spawn",
	DEATH: "death", LOOP: "loop", LOAD: "load", EVERY: "every", OBJ: "obj",
	BITS: "bits", ENUM: "enum", DECODE: "decode", ENCODE: "encode", OVER: "over",
	NBTDECL: "nbt",
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
