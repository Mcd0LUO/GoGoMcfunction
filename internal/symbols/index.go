// Package symbols answers the three questions an editor asks: what is this
// name, where is it declared, and what could I type here.
//
// It is a pass over the AST that produces plain data. No I/O, no IR, no
// compiler state beyond the parsed files -- so it can be tested in a table and
// driven from a one-line command instead of an editor.
//
// The resolution order here MIRRORS internal/lower, which is the compiler's
// own answer to the same question. That duplication is deliberate and
// bounded: symbols never decides what a program means, the compiler does. If
// the two ever disagree the compiler is right, so this package is written to
// be obviously wrong rather than clever, and a test pins the precedence.
package symbols

import (
	"sort"
	"strings"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/diag"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
)

// Kind is what a name is.
type Kind string

const (
	KFunc      Kind = "fn"
	KClass     Kind = "class"
	KMethod    Kind = "method"
	KField     Kind = "field"
	KObj       Kind = "obj"
	KExtern    Kind = "extern"
	KNBT       Kind = "nbt"
	KConst     Kind = "const"
	KLocal     Kind = "let"
	KParam     Kind = "param"
	KShorthand Kind = "holder.objective"
)

// Symbol is one declaration.
type Symbol struct {
	Name string
	Kind Kind
	// File is where the declaration was written; Pos is the line and column.
	File string
	Pos  diag.Pos
	// Type is the signature, criteria or binding, shown on hover.
	Type string
	// Detail is a second line of context: the period of a function, the
	// holder of an extern, the objective a field packs into.
	Detail string
	// Params are the declared parameters, in order, as "name: type". Kept
	// structured as well as rendered so signature help can point at the
	// active argument without re-parsing the rendered text.
	Params []string
}

// Index is the symbol table for one module.
type Index struct {
	byName map[string][]Symbol
	occ    map[occ]Symbol
	all    []Symbol
	// texts is the source of each file, used only to find where a declaration's
	// NAME sits on its line.
	texts map[string]string
}

type occ struct {
	file string
	line int
	// col and end bracket the identifier, so a cursor anywhere inside it
	// resolves. Matching only the first column made hover work from exactly
	// one pixel, which is not how anyone uses a mouse.
	col int
	end int
}

// Build indexes every file given. Files are processed in name order so the
// output does not depend on map iteration.
func Build(files map[string]*syntax.File, texts map[string]string) *Index {
	ix := &Index{byName: map[string][]Symbol{}, occ: map[occ]Symbol{}, texts: texts}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		ix.addFile(name, files[name])
	}
	return ix
}

func (ix *Index) declare(s Symbol) Symbol {
	s.Pos = ix.namePos(s)
	ix.byName[s.Name] = append(ix.byName[s.Name], s)
	ix.all = append(ix.all, s)
	return s
}

// namePos narrows a declaration's position to its NAME.
//
// The parser records where a declaration starts, which is its keyword: `obj X`
// carries the position of `obj`. Hovering the name then found nothing, and
// go-to-definition jumped to the keyword -- both wrong in the same way, and both
// invisible until someone typed a name further along the line than the keyword.
//
// The fix is here rather than in the parser because every declaration kind has
// the same problem and the same cure: search the declaration's own line forward
// from the keyword for the name. A declaration's name always follows its
// keyword, and the search starts at the keyword, so an earlier identical word
// (the namespace, a comment) cannot be picked up.
func (ix *Index) namePos(s Symbol) diag.Pos {
	if s.Pos.Line <= 0 {
		// A constant has no node at all: File.Consts maps a name to its value,
		// so the name carries no position and go-to-definition had nowhere to
		// go. Its declaration is still in the text, so look for it.
		if s.Kind == KConst {
			return ix.constPos(s)
		}
		return s.Pos
	}
	name := lastSegment(s.Name)
	if name == "" {
		return s.Pos
	}
	text, ok := ix.texts[s.File]
	if !ok {
		return s.Pos
	}
	lines := strings.Split(text, "\n")
	if s.Pos.Line > len(lines) {
		return s.Pos
	}
	src := []rune(strings.TrimSuffix(lines[s.Pos.Line-1], "\r"))
	start := s.Pos.Col - 1
	if start < 0 || start > len(src) {
		return s.Pos
	}
	hay := string(src[start:])
	i := strings.Index(hay, name)
	if i < 0 {
		return s.Pos
	}
	return diag.Pos{File: s.File, Line: s.Pos.Line, Col: start + i + 1}
}

func (ix *Index) addFile(file string, f *syntax.File) {
	if f == nil {
		return
	}
	// Constants carry no name position of their own: File.Consts is a map of
	// name to value expression, so the value is the only position available.
	for name := range f.Consts {
		// A constant has no position of its own: File.Consts is a map of name
		// to value expression, and the name is not a node. Hover still works;
		// go-to-definition lands on the file, which is the honest answer.
		ix.declare(Symbol{Name: name, Kind: KConst, File: file, Type: "const " + name})
	}
	for _, od := range f.Objs {
		ix.declare(Symbol{Name: od.Name, Kind: KObj, File: file,
			Pos:  diag.Pos{File: file, Line: od.P.Line, Col: od.P.Col},
			Type: objType(od)})
	}
	for _, x := range f.Externs {
		ix.declare(Symbol{Name: x.Name, Kind: KExtern, File: file,
			Pos:    diag.Pos{File: file, Line: x.P.Line, Col: x.P.Col},
			Type:   "extern " + x.Name + ": " + x.Type,
			Detail: bindingOf(x)})
	}
	for _, fd := range f.NBTFields {
		ix.declare(Symbol{Name: fd.Name, Kind: KNBT, File: file,
			Pos:    diag.Pos{File: file, Line: fd.P.Line, Col: fd.P.Col},
			Type:   "nbt " + fd.Name + " = " + fd.Path,
			Detail: fieldEncoding(fd)})
	}
	for _, fn := range f.Funcs {
		ix.declare(Symbol{Name: fn.Name, Kind: KFunc, File: file,
			Pos:    diag.Pos{File: file, Line: fn.P.Line, Col: fn.P.Col},
			Type:   funcType(fn.Name, fn.Params, fn.RetType),
			Detail: funcDetail(fn),
			Params: paramTexts(fn.Params)})
		ix.addBody(file, fn.Name, fn.Params, fn.Body, "")
	}
	for _, cl := range f.Classes {
		ix.declare(Symbol{Name: cl.Name, Kind: KClass, File: file,
			Pos:  diag.Pos{File: file, Line: cl.P.Line, Col: cl.P.Col},
			Type: "class " + cl.Name})
		fields := map[string]bool{}
		for _, fld := range cl.Fields {
			fields[fld.Name] = true
			ix.declare(Symbol{Name: cl.Name + "." + fld.Name, Kind: KField, File: file,
				Pos:  diag.Pos{File: file, Line: fld.P.Line, Col: fld.P.Col},
				Type: "field " + cl.Name + "." + fld.Name + ": " + fld.Type})
		}
		for _, m := range cl.Methods {
			full := cl.Name + "/" + m.Name
			ix.declare(Symbol{Name: full, Kind: KMethod, File: file,
				Pos:    diag.Pos{File: file, Line: m.P.Line, Col: m.P.Col},
				Type:   funcType(full, m.Params, ""),
				Detail: methodDetail(m),
				Params: paramTexts(m.Params)})
			ix.addBody(file, full, m.Params, m.Body, cl.Name)
		}
	}
}

// addBody records the locals of one function and the meaning of every
// identifier inside it.
func (ix *Index) addBody(file, owner string, params []syntax.Param, body []syntax.Stmt, class string) {
	scope := map[string]Symbol{}
	for _, p := range params {
		// A parameter has no position of its own: Param carries a name and a
		// type only, so hover points at the function it belongs to.
		s := Symbol{Name: p.Name, Kind: KParam, File: file,
			Type: "param " + p.Name + ": " + p.Type, Detail: "of " + owner}
		scope[p.Name] = s
		ix.all = append(ix.all, s)
	}
	collectLocals(body, file, scope)
	// Locals go into all as well, so hovering the declaration answers. They
	// stay out of byName: a bare name resolves module-wide, and one function's
	// local must not shadow another's.
	for _, s := range scope {
		if s.Kind == KLocal {
			ix.all = append(ix.all, s)
		}
	}

	syntax.WalkStmts(body, syntax.Visitor{Call: func(call syntax.Call) {
		// A call's callee is NOT reported as an Ident: it is the Call node's
		// name. Without this, hover and go-to-definition did nothing on the
		// most common thing anyone points at.
		if call.Name == "" {
			return
		}
		at := occ{file: file, line: call.P.Line, col: call.P.Col,
			end: call.P.Col + runeLen(lastSegment(call.Name))}
		for _, candidate := range calleeCandidates(call.Name, class) {
			if s, ok := ix.resolveDecl(candidate, ""); ok {
				ix.occ[at] = s
				return
			}
		}
	}, Ident: func(name string, p syntax.Pos) {
		if name == "" {
			return
		}
		at := occ{file: file, line: p.Line, col: p.Col, end: p.Col + runeLen(name)}
		if s, ok := scope[name]; ok {
			ix.occ[at] = s
			return
		}
		if s, ok := ix.resolveDecl(name, class); ok {
			ix.occ[at] = s
			return
		}
		// An unresolvable dotted name is the holder.objective shorthand: a
		// score on a fake player. The compiler accepts it, so hover explains
		// it rather than saying nothing.
		if i := strings.Index(name, "."); i > 0 {
			ix.occ[at] = Symbol{Name: name, Kind: KShorthand, File: file,
				Pos:    diag.Pos{File: file, Line: p.Line, Col: p.Col},
				Type:   "score on fake player " + name[:i] + ": objective " + name[i+1:],
				Detail: "not declared as an objective, so this is the holder.objective shorthand"}
		}
	}})
}

// collectLocals records every let in a body, including nested blocks.
func collectLocals(body []syntax.Stmt, file string, scope map[string]Symbol) {
	for _, st := range body {
		switch x := st.(type) {
		case syntax.LetStmt:
			p := x.NamePos
			if p.Line == 0 {
				p = x.P
			}
			scope[x.Name] = Symbol{Name: x.Name, Kind: KLocal, File: file,
				Pos: diag.Pos{File: file, Line: p.Line, Col: p.Col}, Type: "let " + x.Name}
		case syntax.IfStmt:
			collectLocals(x.Then, file, scope)
			collectLocals(x.Else, file, scope)
		case syntax.WhileStmt:
			collectLocals(x.Body, file, scope)
		case syntax.LoopStmt:
			collectLocals(x.Body, file, scope)
		case syntax.ForStmt:
			scope[x.Name] = Symbol{Name: x.Name, Kind: KLocal, File: file,
				Pos: diag.Pos{File: file, Line: x.P.Line, Col: x.P.Col}, Type: "for variable " + x.Name}
			collectLocals(x.Body, file, scope)
		}
	}
}

// resolveDecl looks a name up among the module-level declarations.
func (ix *Index) resolveDecl(name, class string) (Symbol, bool) {
	// A bare name inside a class may be one of its fields.
	if class != "" {
		if ss := ix.byName[class+"."+name]; len(ss) > 0 {
			return ss[0], true
		}
	}
	if ss := ix.byName[name]; len(ss) > 0 {
		return ss[0], true
	}
	return Symbol{}, false
}

// calleeCandidates lists the spellings a call may use for one function.
//
// Inside a class a bare name may mean a sibling method; otherwise it is a
// function in this module; otherwise it is fully qualified.
func calleeCandidates(name, class string) []string {
	var out []string
	if class != "" && !strings.Contains(name, "/") && !strings.Contains(name, ":") {
		out = append(out, class+"/"+name)
	}
	out = append(out, name)
	return out
}

// At returns what the identifier under a position refers to.
//
// The position may sit anywhere inside the name: an editor reports the cursor,
// not the token start, and a hover that only fires on the first character is
// indistinguishable from a broken one.
func (ix *Index) At(file string, line, col int) (Symbol, bool) {
	for k, s := range ix.occ {
		if k.file == file && k.line == line && col >= k.col && col < k.end {
			return s, true
		}
	}
	// The cursor may also sit on a declaration.
	for _, s := range ix.all {
		if s.File != file || s.Pos.Line != line {
			continue
		}
		end := s.Pos.Col + runeLen(lastSegment(s.Name))
		if col >= s.Pos.Col && col < end {
			return s, true
		}
	}
	return Symbol{}, false
}

// constPos finds a constant's declaration in the file text.
//
// Anchored on the keyword at the start of a line, so a mention of the name
// inside a comment or an expression cannot be mistaken for the declaration.
func (ix *Index) constPos(s Symbol) diag.Pos {
	text, ok := ix.texts[s.File]
	if !ok {
		return s.Pos
	}
	lines := strings.Split(text, "\n")
	for i, raw := range lines {
		trimmed := strings.TrimLeft(raw, " \t")
		rest, ok := strings.CutPrefix(trimmed, "const ")
		if !ok {
			continue
		}
		rest = strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(rest, s.Name) {
			continue
		}
		after := rest[len(s.Name):]
		if after != "" && (isNameRune(rune(after[0]))) {
			continue
		}
		// Column of the name: the text before it, counted in runes.
		before := raw[:len(raw)-len(rest)]
		return diag.Pos{File: s.File, Line: i + 1, Col: len([]rune(before)) + 1}
	}
	return s.Pos
}

func isNameRune(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r > 127
}

// lastSegment is the name without its qualifying path, which is the token a
// declaration's position points at.
func lastSegment(name string) string {
	if i := strings.LastIndexAny(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

func runeLen(s string) int { return len([]rune(s)) }

// Occurrence is one place a symbol is used.
type Occurrence struct {
	File string
	Pos  diag.Pos
	// Name is the text at that place, which differs from the declaration for
	// a shorthand or a qualified spelling.
	Name string
}

// Occurrences returns every place that refers to the same declaration.
//
// Matching is by the resolved symbol's identity, not by text: two functions
// in one module may share a short name, and a field of one class must not
// answer for a field of another.
func (ix *Index) Occurrences(s Symbol) []Occurrence {
	var out []Occurrence
	for at, got := range ix.occ {
		if got.Kind != s.Kind || got.Pos != s.Pos || got.File != s.File || got.Name != s.Name {
			continue
		}
		out = append(out, Occurrence{File: at.file,
			Pos: diag.Pos{File: at.file, Line: at.line, Col: at.col}, Name: got.Name})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Pos.Line < out[j].Pos.Line
	})
	return out
}

// Lookup returns every declaration of a name, for go-to-definition.
func (ix *Index) Lookup(name string) []Symbol { return ix.byName[name] }

// All returns every declaration, in a stable order.
func (ix *Index) All() []Symbol {
	out := make([]Symbol, len(ix.all))
	copy(out, ix.all)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Pos.Line < out[j].Pos.Line
	})
	return out
}

// Completions offers the names visible at a position, filtered by prefix.
func (ix *Index) Completions(prefix string) []Symbol {
	var out []Symbol
	seen := map[string]bool{}
	for _, s := range ix.All() {
		if !strings.HasPrefix(s.Name, prefix) || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		out = append(out, s)
	}
	return out
}

func objType(od syntax.ObjDecl) string {
	s := "obj " + od.Name
	if od.Criteria != "" {
		s += " " + od.Criteria
	}
	if od.Display != "" {
		s += " " + od.Display
	}
	return s
}

func bindingOf(x syntax.ExternDecl) string {
	switch {
	case x.Kind == "fn":
		return "calls an existing function"
	case x.Holder != "" && x.Holder != "@s":
		return "score " + x.Objective + " on fake player " + x.Holder
	case x.Holder == "@s":
		return "score " + objectiveOf(x) + " on the executing entity"
	}
	return "score " + objectiveOf(x)
}

func objectiveOf(x syntax.ExternDecl) string {
	if x.Objective != "" {
		return x.Objective
	}
	return x.Name
}

func fieldEncoding(fd syntax.NBTField) string {
	if fd.Bits > 0 {
		return "packs " + itoa(fd.Bits) + " bits"
	}
	if len(fd.Enum) > 0 {
		return "enum with " + itoa(len(fd.Enum)) + " cases"
	}
	return ""
}

// paramTexts renders each parameter as "name: type".
func paramTexts(params []syntax.Param) []string {
	if len(params) == 0 {
		return nil
	}
	out := make([]string, 0, len(params))
	for _, p := range params {
		out = append(out, p.Name+": "+p.Type)
	}
	return out
}

func funcType(name string, params []syntax.Param, ret string) string {
	var b strings.Builder
	b.WriteString("fn " + name + "(")
	for i, p := range params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Name + ": " + p.Type)
	}
	b.WriteString(")")
	if ret != "" {
		b.WriteString(" -> " + ret)
	}
	return b.String()
}

func methodDetail(m syntax.Method) string {
	var parts []string
	for _, a := range m.Attrs {
		parts = append(parts, "["+a.Name+"]")
	}
	if m.EveryTicks > 0 {
		parts = append(parts, "runs every "+itoa(m.EveryTicks)+" ticks")
	}
	return strings.Join(parts, " ")
}

func funcDetail(fn syntax.FuncDecl) string {
	var parts []string
	for _, a := range fn.Attrs {
		parts = append(parts, "["+a.Name+"]")
	}
	if fn.EveryTicks > 0 {
		parts = append(parts, "runs every "+itoa(fn.EveryTicks)+" ticks")
	}
	return strings.Join(parts, " ")
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
