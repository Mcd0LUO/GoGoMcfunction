package lsp

// Hover, go-to-definition and completion.
//
// All three ask the same question three ways -- what name is here, and what
// does it mean -- so they share one index and one position conversion. The
// answers come from internal/symbols, which is plain data. Nothing here
// decides anything about the language.

import (
	"os"
	"sort"
	"strings"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/symbols"
)

// fence wraps text in a ggm code block. Written with byte escapes so this
// file contains no literal backtick, which keeps the source easy to quote.
const fence = "\x60\x60\x60"

func codeBlock(s string) string { return fence + "ggm\n" + s + "\n" + fence }

// indexFor returns the symbol index for a file, or nil while none exists.
//
// A file with a namespace uses its module's index. A file without one -- a
// buffer someone just created -- uses an index of itself, because that is the
// moment completion matters most and the module it will belong to is not yet
// knowable.
func (s *Server) indexFor(ns, path string) *symbols.Index {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ns != "" {
		return s.idx[ns]
	}
	return s.solo[path]
}

// handleHover answers textDocument/hover.
func (s *Server) handleHover(p PositionParams) (any, error) {
	path, _, ns, line, col, ok := s.locate(p)
	if !ok {
		return nil, nil
	}
	ix := s.indexFor(ns, path)
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
	ix := s.indexFor(ns, path)
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
	path, text, ns, line, col, ok := s.locate(p)
	if !ok {
		return nil, nil
	}
	ix := s.indexFor(ns, path)
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
	ix := s.indexFor(s.namespaceOf(path), path)
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
	ix := s.indexFor(ns, path)
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
	path, text, ns, line, col, ok := s.locate(p)
	if !ok {
		return nil, nil
	}
	ix := s.indexFor(ns, path)
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

// handleWatchedFiles refreshes the disk cache after a file appears, changes or
// vanishes outside the editor.
//
// Without this the server only ever knew about the files present at startup:
// creating a module file and calling into it from another one reported the call
// as an unknown callee, which is a false alarm on correct code -- the failure
// mode this whole server exists to avoid.
func (s *Server) handleWatchedFiles(p DidChangeWatchedFilesParams) {
	affected := map[string]bool{}
	for _, ev := range p.Changes {
		path, ok := pathFromURI(string(ev.URI))
		if !ok {
			continue
		}
		// Whatever namespace this file used to belong to must be recompiled,
		// not only the one it belongs to now.
		if ns := s.namespaceOf(path); ns != "" {
			affected[ns] = true
		}
		s.mu.Lock()
		s.nsOf[path] = ""
		s.compiled[path] = "\x00stale"
		s.mu.Unlock()
		if ev.Type == 3 {
			// Deleted: drop it so the index stops offering it.
			s.mu.Lock()
			delete(s.disk, path)
			delete(s.open, path)
			delete(s.ast, path)
			delete(s.astText, path)
			s.mu.Unlock()
		} else if b, err := os.ReadFile(path); err == nil {
			s.mu.Lock()
			s.disk[path] = string(b)
			s.mu.Unlock()
		}
		s.recompile(path)
	}
}

// handlePrepareRename reports whether a symbol can be renamed at a position.
//
// Answering null disables the rename UI for that spot, which is right for a
// holder.objective shorthand: it names a fake player, not a declaration, so a
// rename would either do nothing or silently retarget a different score.
func (s *Server) handlePrepareRename(p PositionParams) (any, error) {
	path, _, ns, line, col, ok := s.locate(p)
	if !ok {
		return nil, nil
	}
	ix := s.indexFor(ns, path)
	if ix == nil {
		return nil, nil
	}
	sym, ok := ix.At(path, line, col)
	if !ok || sym.Kind == symbols.KShorthand {
		return nil, nil
	}
	return tokenRange(line, col, runeCount(lastSegmentOf(sym.Name))), nil
}

// handleRename returns the edits that rename a symbol everywhere it is used.
//
// The edits come from Occurrences, which matches by the RESOLVED declaration
// rather than by text. A rename therefore cannot touch a same-named symbol in
// another function, which is the failure a text search produces.
//
// Renaming the declaration of a function also has to move its call sites, and
// those are recorded as resolutions of the same symbol, so one pass covers
// both.
func (s *Server) handleRename(p RenameParams) (any, error) {
	path, _, ns, line, col, ok := s.locate(PositionParams{
		TextDocument: p.TextDocument,
		Position:     p.Position,
	})
	if !ok || p.NewName == "" {
		return nil, nil
	}
	ix := s.indexFor(ns, path)
	if ix == nil {
		return nil, nil
	}
	sym, ok := ix.At(path, line, col)
	if !ok || sym.Kind == symbols.KShorthand {
		return nil, nil
	}

	changes := map[DocumentURI][]TextEdit{}
	add := func(file string, atLine, atCol int, name string) {
		if atLine <= 0 {
			return
		}
		u := uriFromPath(file)
		changes[u] = append(changes[u], TextEdit{
			Range:   tokenRange(atLine, atCol, runeCount(name)),
			NewText: p.NewName,
		})
	}
	for _, occ := range ix.Occurrences(sym) {
		add(occ.File, occ.Pos.Line, occ.Pos.Col, occ.Name)
	}
	// The declaration itself, when the symbol has one in the index.
	if sym.Pos.Line > 0 && len(ix.Lookup(sym.Name)) > 0 {
		add(sym.File, sym.Pos.Line, sym.Pos.Col, lastSegmentOf(sym.Name))
	}
	if len(changes) == 0 {
		return nil, nil
	}
	return &WorkspaceEdit{Changes: changes}, nil
}

// handleCodeActions offers fixes for the diagnostics the client reports.
//
// One exists: declaring an objective the program uses but never declares.
// 1.12.2 discards writes to an undeclared objective without an error, so this
// is the mistake the compiler exists to catch -- and once it is caught, writing
// the declaration is mechanical, which is exactly the work a quick fix should
// take.
func (s *Server) handleCodeActions(p CodeActionParams) (any, error) {
	path, text, _, _, _, ok := s.locate(PositionParams{TextDocument: p.TextDocument})
	if !ok {
		return nil, nil
	}
	var actions []CodeAction
	seen := map[string]bool{}
	for _, d := range p.Context.Diagnostics {
		if d.Code != "E_OBJECTIVE_UNDECLARED" && d.Code != "W_DOTTED_UNDECLARED" {
			continue
		}
		name := quotedName(d.Message)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		edit := insertLine(path, text, "obj "+name)
		actions = append(actions, CodeAction{
			Title:       "Declare `obj " + name + "`",
			Kind:        CodeActionQuickFix,
			Diagnostics: []Diagnostic{d},
			Edit:        &WorkspaceEdit{Changes: map[DocumentURI][]TextEdit{uriFromPath(path): {edit}}},
		})
	}
	if actions == nil {
		return nil, nil
	}
	return actions, nil
}

// insertLine builds the edit that puts a line after the namespace declaration.
//
// After, not at the top: a file that starts with `obj X` and has its namespace
// on line 2 would otherwise lose the namespace from the first line, and a
// declaration above the namespace reads as a mistake even where it parses.
func insertLine(path, text, line string) TextEdit {
	lines := strings.Split(text, "\n")
	at := 0
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "namespace ") {
			at = i + 1
			break
		}
	}
	return TextEdit{
		Range: Range{
			Start: Position{Line: uint32(at), Character: 0},
			End:   Position{Line: uint32(at), Character: 0},
		},
		NewText: line + "\n",
	}
}

// quotedName returns the first double-quoted name in a diagnostic message.
//
// The name is taken from the message rather than from the source under the
// caret because a decode or encode statement reports the whole statement: the
// caret sits on `decode`, not on the objective it names. The message format is
// the compiler's own and is asserted by the diagnostics tests, so reading it
// here is reading a contract rather than guessing.
func quotedName(msg string) string {
	open := strings.IndexByte(msg, '"')
	if open < 0 {
		return ""
	}
	rest := msg[open+1:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// handleWorkspaceSymbols answers workspace/symbol, the search box.
//
// Every index the server holds is consulted, not only the current file's: a
// workspace search that silently covered one namespace would be worse than no
// search, because the answer looks complete.
func (s *Server) handleWorkspaceSymbols(p WorkspaceSymbolParams) (any, error) {
	needle := strings.ToLower(p.Query)
	s.mu.Lock()
	indexes := make([]*symbols.Index, 0, len(s.idx))
	for _, ix := range s.idx {
		indexes = append(indexes, ix)
	}
	s.mu.Unlock()

	var out []SymbolInformation
	seen := map[string]bool{}
	for _, ix := range indexes {
		for _, sym := range ix.All() {
			if sym.Pos.Line == 0 {
				continue
			}
			if needle != "" && !strings.Contains(strings.ToLower(sym.Name), needle) {
				continue
			}
			key := sym.File + sym.Name + itoa(sym.Pos.Line)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, SymbolInformation{
				Name: sym.Name,
				Kind: symbolKind(sym.Kind),
				Location: Location{
					URI:   uriFromPath(sym.File),
					Range: tokenRange(sym.Pos.Line, sym.Pos.Col, runeCount(lastSegmentOf(sym.Name))),
				},
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Location.URI < out[j].Location.URI
	})
	if out == nil {
		return nil, nil
	}
	return out, nil
}
