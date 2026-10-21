package lsp

// The GGM language server.
//
// It does one thing: compile the module a document belongs to and publish the
// diagnostics as squiggles. That is the compiler's whole reason for existing,
// so it is the first thing to surface in an editor.
//
// What it deliberately does NOT do yet: hover, go-to-definition, completion.
// All three need a symbol table (name -> declaration position), and the
// lowerer produces IR rather than symbols. Adding them here without that table
// would mean re-deriving declarations from the AST in the server, which is a
// second implementation of the language to keep in sync.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/Mcd0LUO/GoGoMcfunction/internal/diag"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/driver"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/optimize"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/symbols"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/syntax"
	"github.com/Mcd0LUO/GoGoMcfunction/internal/target"
)

// Server holds the workspace and the open documents.
type Server struct {
	root string
	conn *conn

	mu sync.Mutex
	// open is the client's copy of documents it has opened. An open document
	// is authoritative over what is on disk: the editor may hold unsaved text.
	open map[string]string
	// disk is the text last read from disk.
	disk map[string]string
	// nsOf caches each file's namespace so a keystroke does not re-parse the
	// whole workspace. An entry is cleared when its file changes.
	nsOf map[string]string
	// idx holds one symbol index per namespace, rebuilt whenever that module
	// is recompiled. Hover, definition and completion read it and never touch
	// the AST themselves.
	idx map[string]*symbols.Index
	// ast caches the parse of each file, and astText remembers which text it
	// came from. A keystroke changes one file; without this, every other file
	// in the module is parsed again for the symbol index and a third time
	// inside the compiler.
	ast     map[string]*syntax.File
	astText map[string]string
	// compiled remembers the text of each file at the last successful
	// compilation, so a save that changed nothing does no work.
	compiled map[string]string
	// parses counts real parse calls, so a test can prove the cache works
	// instead of assuming it does.
	parses int
}

// New returns a server rooted at dir.
func New(dir string) *Server {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	return &Server{
		root:     abs,
		open:     map[string]string{},
		disk:     map[string]string{},
		nsOf:     map[string]string{},
		idx:      map[string]*symbols.Index{},
		ast:      map[string]*syntax.File{},
		astText:  map[string]string{},
		compiled: map[string]string{},
	}
}

// Serve reads messages until the client closes the stream.
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	s.conn = newConn(r, w)
	for {
		m, err := s.conn.read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := s.dispatch(m); err != nil {
			return err
		}
	}
}

// dispatch handles one message. Requests get a response; notifications do not,
// because JSON-RPC forbids answering one.
func (s *Server) dispatch(m *msg) error {
	switch m.Method {
	case "initialize":
		var p InitializeParams
		if err := decode(m.Params, &p); err != nil {
			return s.fail(m, codeInvalidRequest, err)
		}
		s.adoptRoot(p)
		return s.reply(m, InitializeResult{
			Capabilities: ServerCapabilities{
				PositionEncoding: "utf-16",
				TextDocumentSync: TextDocumentSyncOptions{
					OpenClose: true,
					Change:    TextDocumentSyncKindFull,
					Save:      &SaveOptions{},
				},
				HoverProvider:          true,
				DefinitionProvider:     true,
				CompletionProvider:     &CompletionOptions{TriggerCharacters: []string{"."}},
				DocumentSymbolProvider: true,
				ReferencesProvider:     true,
			},
			ServerInfo: &ServerInfo{Name: "ggmls", Version: serverVersion},
		})

	case "initialized":
		// The handshake is done. Read the workspace now rather than during
		// initialize, so a slow disk cannot time out the client's handshake.
		s.scan()
		return nil

	case "shutdown":
		return s.reply(m, nil)

	case "exit":
		return nil

	case "textDocument/didOpen":
		var p DidOpenTextDocumentParams
		if err := decode(m.Params, &p); err != nil {
			return nil
		}
		path, ok := pathFromURI(string(p.TextDocument.URI))
		if !ok {
			return nil
		}
		s.mu.Lock()
		s.open[path] = p.TextDocument.Text
		s.nsOf[path] = ""
		s.mu.Unlock()
		s.recompile(path)
		return nil

	case "textDocument/didChange":
		var p DidChangeTextDocumentParams
		if err := decode(m.Params, &p); err != nil {
			return nil
		}
		path, ok := pathFromURI(string(p.TextDocument.URI))
		if !ok || len(p.ContentChanges) == 0 {
			return nil
		}
		// Full sync: the last change carries the whole document.
		s.mu.Lock()
		s.open[path] = p.ContentChanges[len(p.ContentChanges)-1].Text
		s.nsOf[path] = ""
		s.mu.Unlock()
		s.recompile(path)
		return nil

	case "textDocument/didSave":
		var p DidSaveTextDocumentParams
		if err := decode(m.Params, &p); err != nil {
			return nil
		}
		if path, ok := pathFromURI(string(p.TextDocument.URI)); ok {
			s.recompile(path)
		}
		return nil

	case "textDocument/didClose":
		var p DidCloseTextDocumentParams
		if err := decode(m.Params, &p); err != nil {
			return nil
		}
		path, ok := pathFromURI(string(p.TextDocument.URI))
		if !ok {
			return nil
		}
		s.mu.Lock()
		delete(s.open, path)
		s.nsOf[path] = ""
		s.mu.Unlock()
		// Re-read from disk: the buffer is gone, so disk is authoritative and
		// may have been saved with different content.
		if b, err := os.ReadFile(path); err == nil {
			s.mu.Lock()
			s.disk[path] = string(b)
			s.mu.Unlock()
		}
		s.recompile(path)
		return nil

	case "textDocument/hover":
		var p PositionParams
		if err := decode(m.Params, &p); err != nil {
			return s.fail(m, codeInvalidRequest, err)
		}
		r, err := s.handleHover(p)
		if err != nil {
			return s.fail(m, codeInternalError, err)
		}
		return s.reply(m, r)

	case "textDocument/definition":
		var p PositionParams
		if err := decode(m.Params, &p); err != nil {
			return s.fail(m, codeInvalidRequest, err)
		}
		r, err := s.handleDefinition(p)
		if err != nil {
			return s.fail(m, codeInternalError, err)
		}
		return s.reply(m, r)

	case "textDocument/references":
		var p ReferenceParams
		if err := decode(m.Params, &p); err != nil {
			return s.fail(m, codeInvalidRequest, err)
		}
		r, err := s.handleReferences(p)
		if err != nil {
			return s.fail(m, codeInternalError, err)
		}
		return s.reply(m, r)

	case "textDocument/documentSymbol":
		var p DocumentSymbolParams
		if err := decode(m.Params, &p); err != nil {
			return s.fail(m, codeInvalidRequest, err)
		}
		r, err := s.handleDocumentSymbols(p)
		if err != nil {
			return s.fail(m, codeInternalError, err)
		}
		return s.reply(m, r)

	case "textDocument/completion":
		var p PositionParams
		if err := decode(m.Params, &p); err != nil {
			return s.fail(m, codeInvalidRequest, err)
		}
		r, err := s.handleCompletion(p)
		if err != nil {
			return s.fail(m, codeInternalError, err)
		}
		return s.reply(m, r)
	}

	if m.isCall() {
		return s.conn.replyErr(m.ID, codeMethodNotFound, "method not implemented: "+m.Method)
	}
	return nil
}

// serverVersion is reported in the handshake.
const serverVersion = "0.1.0"

func (s *Server) reply(m *msg, result any) error {
	if !m.isCall() {
		return nil
	}
	return s.conn.reply(m.ID, result)
}

func (s *Server) fail(m *msg, code int, err error) error {
	if !m.isCall() {
		return nil
	}
	return s.conn.replyErr(m.ID, code, err.Error())
}

// adoptRoot records the workspace root the client opened.
func (s *Server) adoptRoot(p InitializeParams) {
	var raw string
	if len(p.WorkspaceFolders) > 0 {
		raw = string(p.WorkspaceFolders[0].URI)
	} else if p.RootURI != "" {
		raw = string(p.RootURI)
	}
	if raw == "" {
		return
	}
	path, ok := pathFromURI(raw)
	if !ok || path == "" {
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	s.root = abs
}

// scan reads every .ggm file under the root into the disk cache.
func (s *Server) scan() {
	found := map[string]string{}
	_ = filepath.WalkDir(s.root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".ggm") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		found[filepath.Clean(abs)] = string(b)
		return nil
	})
	s.mu.Lock()
	for k, v := range found {
		s.disk[k] = v
	}
	s.mu.Unlock()
}

// textOf returns a document's current text.
func (s *Server) textOf(path string) (string, bool) {
	if t, ok := s.open[path]; ok {
		return t, true
	}
	t, ok := s.disk[path]
	return t, ok
}

// astOf returns a file's parse, re-parsing only when its text changed.
func (s *Server) astOf(path string) *syntax.File {
	s.mu.Lock()
	text, ok := s.textOf(path)
	cached, hasCache := s.ast[path]
	sameText := s.astText[path] == text
	s.mu.Unlock()
	if !ok {
		return nil
	}
	if hasCache && sameText {
		return cached
	}
	s.mu.Lock()
	s.parses++
	s.mu.Unlock()
	f, err := syntax.Parse(text)
	if err != nil {
		f = nil
	}
	s.mu.Lock()
	if f != nil {
		s.ast[path] = f
	}
	s.astText[path] = text
	s.mu.Unlock()
	return f
}

// namespaceOf returns a file's namespace, through the AST cache.
func (s *Server) namespaceOf(path string) string {
	if f := s.astOf(path); f != nil {
		return f.Namespace
	}
	return ""
}

// recompile compiles the module the changed file belongs to and publishes
// diagnostics for every file in it.
//
// A GGM module is one namespace spread over several files. Compiling only the
// file that was touched would report every cross-file call as an unknown
// callee: a false alarm on correct code, which is worse than no squiggles.
func (s *Server) recompile(changed string) {
	ns := s.namespaceOf(changed)

	s.mu.Lock()
	seen := make([]string, 0, len(s.disk)+len(s.open))
	known := map[string]bool{}
	for path := range s.disk {
		if !known[path] {
			known[path] = true
			seen = append(seen, path)
		}
	}
	for path := range s.open {
		if !known[path] {
			known[path] = true
			seen = append(seen, path)
		}
	}
	s.mu.Unlock()

	var members []string
	if ns == "" {
		// The file has no namespace yet -- it is being typed, and a namespace
		// is the first thing written. Compile it alone so its own syntax errors
		// still show while the module it belongs to is not yet known.
		members = []string{changed}
	} else {
		// The module is every file declaring this namespace. namespaceOf caches,
		// so a repeated keystroke only re-parses files that actually changed.
		for _, path := range seen {
			if s.namespaceOf(path) == ns {
				members = append(members, path)
			}
		}
	}
	sort.Strings(members)

	// Collect the module's texts and parses. astOf re-parses only the file
	// whose text actually changed, so a keystroke costs one parse instead of
	// one per file in the module.
	srcs := make([]driver.Source, 0, len(members))
	texts := make(map[string]string, len(members))
	parsed := make(map[string]*syntax.File, len(members))
	for _, path := range members {
		s.mu.Lock()
		t, ok := s.textOf(path)
		s.mu.Unlock()
		if !ok {
			continue
		}
		f := s.astOf(path)
		texts[path] = t
		srcs = append(srcs, driver.Source{Name: path, Text: t, Parsed: f})
		if f != nil {
			parsed[path] = f
		}
	}
	if len(srcs) == 0 {
		return
	}

	// A save with no edits should not recompile at all.
	s.mu.Lock()
	unchanged := len(s.compiled) == len(texts)
	for path, t := range texts {
		if s.compiled[path] != t {
			unchanged = false
			break
		}
	}
	if unchanged {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	if ns != "" {
		s.mu.Lock()
		s.idx[ns] = symbols.Build(parsed)
		s.mu.Unlock()
	}

	_, diags := driver.CompileFiles(srcs, driver.Options{
		Target: target.V1_12_2,
		Level:  optimize.O2,
	})
	s.mu.Lock()
	for path, t := range texts {
		s.compiled[path] = t
	}
	s.mu.Unlock()

	byFile := map[string][]Diagnostic{}
	for _, d := range diags {
		file := d.Pos.File
		if file == "" {
			// No file means the complaint is about the module or the compiler,
			// not a location. Attaching it to the edited file would underline
			// text that has nothing to do with it.
			continue
		}
		file = filepath.Clean(file)
		byFile[file] = append(byFile[file], toDiagnostic(texts[file], d))
	}

	// Publish for every file in the module, including ones with nothing wrong:
	// an empty list is how a stale squiggle is cleared.
	for _, path := range members {
		out := byFile[path]
		if out == nil {
			out = []Diagnostic{}
		}
		_ = s.conn.notify("textDocument/publishDiagnostics", PublishDiagnosticsParams{
			URI:         uriFromPath(path),
			Diagnostics: out,
		})
	}
}

// toDiagnostic converts one compiler diagnostic into an LSP range.
//
// The compiler counts lines and columns from 1 over RUNES; LSP counts from 0
// over UTF-16 code units. For ASCII and CJK -- which is nearly all of what this
// language is written in -- the two agree after subtracting one. They diverge
// only outside the BMP, so the column is converted rather than assumed equal.
func toDiagnostic(text string, d diag.Diagnostic) Diagnostic {
	sev := DiagnosticSeverityError
	if d.Severity == diag.Warning {
		sev = DiagnosticSeverityWarning
	}
	start := positionOf(text, d.Pos.Line, d.Pos.Col)
	end := positionOf(text, d.Pos.Line, d.Pos.Col+1)
	if end.Line != start.Line {
		// The diagnostic sits at end of line and there is no character to
		// underline; a zero-width range is better than wrapping to the next.
		end = start
	}
	return Diagnostic{
		Range:    Range{Start: start, End: end},
		Severity: sev,
		Code:     d.Code,
		Source:   "ggm",
		Message:  d.Msg,
	}
}

// positionOf converts a 1-based rune position into an LSP position.
func positionOf(text string, line, col int) Position {
	if line < 1 {
		return Position{}
	}
	lines := strings.Split(text, "\n")
	if line > len(lines) {
		return Position{Line: uint32(line - 1)}
	}
	src := strings.TrimSuffix(lines[line-1], "\r")
	runes := []rune(src)
	i := col - 1
	if i < 0 {
		i = 0
	}
	if i > len(runes) {
		i = len(runes)
	}
	units := 0
	for _, r := range runes[:i] {
		units += len(utf16.Encode([]rune{r}))
	}
	return Position{Line: uint32(line - 1), Character: uint32(units)}
}

// decode unmarshals a message's params.
//
// This used to read `return fmt.Errorf("decode: %w", json.Unmarshal(...))`,
// which is ALWAYS a non-nil error: fmt.Errorf builds an error even when the
// verb it wraps is nil. Every message was therefore treated as undecodable --
// initialize never recorded the workspace root, and every didOpen/didChange/
// didSave returned early -- so the server published nothing, and the test
// hung waiting for a diagnostic that was never sent.
func decode(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("decode params: %w", err)
	}
	return nil
}
