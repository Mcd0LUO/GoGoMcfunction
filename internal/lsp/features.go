package lsp

// Hover, go-to-definition and completion.
//
// All three ask the same question three ways -- what name is here, and what
// does it mean -- so they share one index and one position conversion. The
// answers come from internal/symbols, which is plain data. Nothing here
// decides anything about the language.

import (
	"sort"
	"strings"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/symbols"
)

// fence wraps text in a ggm code block. Written with byte escapes so this
// file contains no literal backtick, which keeps the source easy to quote.
const fence = "\x60\x60\x60"

func codeBlock(s string) string { return fence + "ggm\n" + s + "\n" + fence }

// indexFor returns the symbol index of a module, or nil while none exists.
func (s *Server) indexFor(ns string) *symbols.Index {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idx[ns]
}

// handleHover answers textDocument/hover.
func (s *Server) handleHover(p PositionParams) (any, error) {
	path, _, ns, line, col, ok := s.locate(p)
	if !ok {
		return nil, nil
	}
	ix := s.indexFor(ns)
	if ix == nil {
		return nil, nil
	}
	sym, ok := ix.At(path, line, col)
	if !ok {
		return nil, nil
	}

	var b strings.Builder
	b.WriteString(codeBlock(sym.Type))
	if sym.Detail != "" {
		b.WriteString("\n\n" + sym.Detail)
	}
	if sym.Pos.Line > 0 {
		where := shortPath(sym.File) + ":" + itoa(sym.Pos.Line) + ":" + itoa(sym.Pos.Col)
		b.WriteString("\n\nDeclared at " + codeSpan(where))
	}

	rng := tokenRange(line, col, runeCount(lastSegmentOf(sym.Name)))
	return &Hover{
		Contents: MarkupContent{Kind: MarkupMarkdown, Value: b.String()},
		Range:    &rng,
	}, nil
}

// handleDefinition answers textDocument/definition.
func (s *Server) handleDefinition(p PositionParams) (any, error) {
	path, _, ns, line, col, ok := s.locate(p)
	if !ok {
		return nil, nil
	}
	ix := s.indexFor(ns)
	if ix == nil {
		return nil, nil
	}
	sym, ok := ix.At(path, line, col)
	if !ok || sym.Kind == symbols.KShorthand {
		// A shorthand names a fake player rather than a declaration.
		// Jumping somewhere unrelated would be worse than not jumping.
		return nil, nil
	}

	var out []Location
	seen := map[string]bool{}
	add := func(file string, atLine, atCol int, name string) {
		if atLine <= 0 {
			return
		}
		key := file + itoa(atLine) + itoa(atCol)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, Location{
			URI:   uriFromPath(file),
			Range: tokenRange(atLine, atCol, runeCount(name)),
		})
	}

	if decls := ix.Lookup(sym.Name); len(decls) > 0 {
		for _, d := range decls {
			add(d.File, d.Pos.Line, d.Pos.Col, lastSegmentOf(d.Name))
		}
	} else {
		// A local has exactly one declaration, and At already found it.
		add(sym.File, sym.Pos.Line, sym.Pos.Col, lastSegmentOf(sym.Name))
	}
	if out == nil {
		return nil, nil
	}
	return out, nil
}

// handleCompletion answers textDocument/completion.
func (s *Server) handleCompletion(p PositionParams) (any, error) {
	_, text, ns, line, col, ok := s.locate(p)
	if !ok {
		return nil, nil
	}
	ix := s.indexFor(ns)
	if ix == nil {
		return nil, nil
	}
	prefix := identPrefix(text, line, col)
	var items []CompletionItem
	seen := map[string]bool{}
	for _, sym := range ix.Completions(prefix) {
		if seen[sym.Name] {
			continue
		}
		seen[sym.Name] = true
		items = append(items, CompletionItem{
			Label:         sym.Name,
			Kind:          completionKind(sym.Kind),
			Detail:        sym.Type,
			Documentation: sym.Detail,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Label < items[j].Label })
	return &CompletionList{IsIncomplete: false, Items: items}, nil
}

// locate turns an LSP position into everything the queries need.
func (s *Server) locate(p PositionParams) (path, text, ns string, line, col int, ok bool) {
	abs, ok := pathFromURI(string(p.TextDocument.URI))
	if !ok {
		return "", "", "", 0, 0, false
	}
	s.mu.Lock()
	t, has := s.textOf(abs)
	s.mu.Unlock()
	if !has {
		return "", "", "", 0, 0, false
	}
	line = int(p.Position.Line) + 1
	col = runeColOf(t, int(p.Position.Line), int(p.Position.Character))
	return abs, t, s.namespaceOf(abs), line, col, true
}

// runeColOf converts an LSP character offset back to a 1-based rune column.
//
// The inverse of positionOf, and for the same reason: the two agree on BMP
// characters and diverge outside it, so the conversion is done rather than
// assumed.
func runeColOf(text string, lspLine, lspChar int) int {
	lines := strings.Split(text, "\n")
	if lspLine < 0 || lspLine >= len(lines) {
		return 1
	}
	src := strings.TrimSuffix(lines[lspLine], "\r")
	units := 0
	for i, r := range src {
		if units >= lspChar {
			return i + 1
		}
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return len([]rune(src)) + 1
}

// identPrefix returns the identifier fragment the cursor sits in, which is
// what filters a completion list.
func identPrefix(text string, line, col int) string {
	lines := strings.Split(text, "\n")
	if line < 1 || line > len(lines) {
		return ""
	}
	src := []rune(strings.TrimSuffix(lines[line-1], "\r"))
	i := col - 1
	if i > len(src) {
		i = len(src)
	}
	start := i
	for start > 0 && identRune(src[start-1]) {
		start--
	}
	end := i
	for end < len(src) && identRune(src[end]) {
		end++
	}
	return string(src[start:end])
}

func identRune(r rune) bool {
	return r == '_' || r == '.' ||
		(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r > 127
}

// tokenRange is the span of a name starting at a 1-based position.
func tokenRange(line, col, length int) Range {
	return Range{
		Start: Position{Line: uint32(line - 1), Character: uint32(col - 1)},
		End:   Position{Line: uint32(line - 1), Character: uint32(col - 1 + length)},
	}
}

func codeSpan(s string) string { return "\x60" + s + "\x60" }

func lastSegmentOf(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

func runeCount(s string) int { return len([]rune(s)) }

func completionKind(k symbols.Kind) uint32 {
	switch k {
	case symbols.KFunc, symbols.KMethod:
		return CompletionItemKindFunction
	case symbols.KField, symbols.KObj, symbols.KNBT:
		return CompletionItemKindField
	case symbols.KClass:
		return CompletionItemKindClass
	case symbols.KConst:
		return CompletionItemKindConstant
	}
	return CompletionItemKindVariable
}

// shortPath keeps hover text readable when a module spans directories.
func shortPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	parts := strings.Split(p, "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return p
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

// handleDocumentSymbols answers textDocument/documentSymbol, which is the
// outline a client shows beside the editor.
//
// Only the declarations in THIS file are returned. The index is module-wide,
// which is what hover and jump need, but an outline listing another file's
// functions would be wrong.
func (s *Server) handleDocumentSymbols(p DocumentSymbolParams) (any, error) {
	path, ok := pathFromURI(string(p.TextDocument.URI))
	if !ok {
		return nil, nil
	}
	ix := s.indexFor(s.namespaceOf(path))
	if ix == nil {
		return nil, nil
	}
	var out []SymbolInformation
	for _, sym := range ix.All() {
		if sym.File != path || sym.Pos.Line == 0 {
			continue
		}
		out = append(out, SymbolInformation{
			Name: sym.Name,
			Kind: symbolKind(sym.Kind),
			Location: Location{
				URI:   uriFromPath(sym.File),
				Range: tokenRange(sym.Pos.Line, sym.Pos.Col, runeCount(lastSegmentOf(sym.Name))),
			},
		})
	}
	if out == nil {
		return nil, nil
	}
	return out, nil
}

func symbolKind(k symbols.Kind) uint32 {
	switch k {
	case symbols.KFunc, symbols.KMethod:
		return SymbolKindFunction
	case symbols.KClass:
		return SymbolKindClass
	case symbols.KField, symbols.KNBT:
		return SymbolKindField
	case symbols.KObj:
		return SymbolKindObject
	case symbols.KConst:
		return SymbolKindConstant
	}
	return SymbolKindVariable
}

// handleReferences answers textDocument/references.
func (s *Server) handleReferences(p ReferenceParams) (any, error) {
	path, _, ns, line, col, ok := s.locate(PositionParams{
		TextDocument: p.TextDocument,
		Position:     p.Position,
	})
	if !ok {
		return nil, nil
	}
	ix := s.indexFor(ns)
	if ix == nil {
		return nil, nil
	}
	sym, ok := ix.At(path, line, col)
	if !ok || sym.Kind == symbols.KShorthand {
		return nil, nil
	}
	var out []Location
	for _, occ := range ix.Occurrences(sym) {
		out = append(out, Location{
			URI:   uriFromPath(occ.File),
			Range: tokenRange(occ.Pos.Line, occ.Pos.Col, runeCount(occ.Name)),
		})
	}
	if p.Context.IncludeDeclaration && sym.Pos.Line > 0 {
		out = append(out, Location{
			URI:   uriFromPath(sym.File),
			Range: tokenRange(sym.Pos.Line, sym.Pos.Col, runeCount(lastSegmentOf(sym.Name))),
		})
	}
	if out == nil {
		return nil, nil
	}
	return out, nil
}

// handleSignatureHelp answers textDocument/signatureHelp.
//
// Finding the call is a scan of the cursor's line rather than a walk of the
// AST, because the AST does not record where a call's argument list ends. The
// rule is deliberately strict: the nearest unclosed `(` before the cursor,
// immediately preceded by a name that resolves to a function. Anything else
// answers null, and a client shows nothing -- which is better than pointing at
// the wrong signature.
func (s *Server) handleSignatureHelp(p PositionParams) (any, error) {
	_, text, ns, line, col, ok := s.locate(p)
	if !ok {
		return nil, nil
	}
	ix := s.indexFor(ns)
	if ix == nil {
		return nil, nil
	}
	lines := strings.Split(text, "\n")
	if line < 1 || line > len(lines) {
		return nil, nil
	}
	src := []rune(strings.TrimSuffix(lines[line-1], "\r"))
	cur := col - 1
	if cur < 0 || cur > len(src) {
		return nil, nil
	}

	// Walk back to the nearest unclosed paren.
	depth := 0
	open := -1
	for i := cur - 1; i >= 0; i-- {
		switch src[i] {
		case ')':
			depth++
		case '(':
			if depth == 0 {
				open = i
			} else {
				depth--
			}
		}
		if open >= 0 {
			break
		}
	}
	if open < 0 {
		return nil, nil
	}
	start := open
	for start > 0 && identRune(src[start-1]) {
		start--
	}
	if start == open {
		return nil, nil
	}
	name := string(src[start:open])

	var found *symbols.Symbol
	for _, cand := range ix.Lookup(name) {
		if cand.Kind == symbols.KFunc || cand.Kind == symbols.KMethod {
			c := cand
			found = &c
			break
		}
	}
	if found == nil {
		return nil, nil
	}

	// The active parameter is the number of commas between the paren and the
	// cursor, counting only those at depth zero.
	active := 0
	depth = 0
	for i := open + 1; i < cur && i < len(src); i++ {
		switch src[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				active++
			}
		}
	}

	params := make([]ParameterInformation, 0, len(found.Params))
	for _, t := range found.Params {
		params = append(params, ParameterInformation{Label: t})
	}
	return &SignatureHelp{
		Signatures: []SignatureInformation{{
			Label:         found.Type,
			Documentation: found.Detail,
			Parameters:    params,
		}},
		ActiveParameter: uint32(active),
	}, nil
}
