package lsp

// The subset of LSP this server speaks.
//
// These are written out rather than taken from a library. The evidence for
// that choice: no Go language server of any size uses one. gopls,
// terraform-ls, hcl-lang, templ and scip-go all carry their own copies, and
// the two libraries that exist are archived (sourcegraph/go-lsp) or marginal
// (go.lsp.dev/protocol, ~138 stars, and it drags in a pseudo-version of an
// experimental JSON package).
//
// The types are mechanical, so a subset is cheap. Everything below is a field
// the server actually reads or writes; names and optionality follow the
// specification, and the JSON tags are what the wire sees.

// DocumentURI is a client-side document identifier, normally file:///....
type DocumentURI string

// Position is a zero-based line and character offset.
//
// The character offset counts UTF-16 code units unless another encoding is
// negotiated. This server announces utf-16 and converts from the compiler's
// rune columns, which differ only outside the BMP.
type Position struct {
	Line      uint32 `json:"line"`
	Character uint32 `json:"character"`
}

// Range is a half-open span between two positions.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// DiagnosticSeverity mirrors the specification's numbering.
type DiagnosticSeverity uint32

const (
	DiagnosticSeverityError   DiagnosticSeverity = 1
	DiagnosticSeverityWarning DiagnosticSeverity = 2
)

// Diagnostic is one problem to underline.
type Diagnostic struct {
	Range    Range              `json:"range"`
	Severity DiagnosticSeverity `json:"severity,omitempty"`
	// Code is the compiler's own code, e.g. "E_PARSE". Clients show it in the
	// problems panel and some let the user filter by it.
	Code    string `json:"code,omitempty"`
	Source  string `json:"source,omitempty"`
	Message string `json:"message"`
}

// PublishDiagnosticsParams replaces the diagnostic list for one document.
//
// It REPLACES rather than appends: sending an empty list is how a stale
// squiggle is cleared.
type PublishDiagnosticsParams struct {
	URI         DocumentURI  `json:"uri"`
	Version     *int32       `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// TextDocumentSyncKind says how the client sends changes.
type TextDocumentSyncKind uint32

const (
	// TextDocumentSyncKindFull sends the whole document each time.
	TextDocumentSyncKindFull TextDocumentSyncKind = 1
)

// SaveOptions controls the textDocument/didSave notification.
type SaveOptions struct {
	IncludeText bool `json:"includeText,omitempty"`
}

// TextDocumentSyncOptions is the detailed form of the sync capability.
type TextDocumentSyncOptions struct {
	OpenClose bool                 `json:"openClose,omitempty"`
	Change    TextDocumentSyncKind `json:"change,omitempty"`
	Save      *SaveOptions         `json:"save,omitempty"`
}

// ServerCapabilities is what this server announces it can do.
type ServerCapabilities struct {
	// PositionEncoding is echoed so the client knows the column unit. Omitting
	// it defaults to utf-16, which is what this server implements, so it is
	// redundant -- and stated anyway, because a negotiation that is implicit
	// becomes a bug the day someone assumes the other default.
	PositionEncoding       string                  `json:"positionEncoding,omitempty"`
	TextDocumentSync       TextDocumentSyncOptions `json:"textDocumentSync"`
	HoverProvider          bool                    `json:"hoverProvider,omitempty"`
	DefinitionProvider     bool                    `json:"definitionProvider,omitempty"`
	CompletionProvider     *CompletionOptions      `json:"completionProvider,omitempty"`
	DocumentSymbolProvider bool                    `json:"documentSymbolProvider,omitempty"`
	ReferencesProvider     bool                    `json:"referencesProvider,omitempty"`
	RenameProvider         bool                    `json:"renameProvider,omitempty"`
	CodeActionProvider     bool                    `json:"codeActionProvider,omitempty"`
	SignatureHelpProvider  *SignatureHelpOptions   `json:"signatureHelpProvider,omitempty"`
}

// CompletionOptions configures completion. The trigger characters are the ones
// that start a name in the grammar.
type CompletionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
}

// ServerInfo names the server in the client's log.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// InitializeResult answers the handshake.
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   *ServerInfo        `json:"serverInfo,omitempty"`
}

// WorkspaceFolder is one root the client opened.
type WorkspaceFolder struct {
	URI  DocumentURI `json:"uri"`
	Name string      `json:"name,omitempty"`
}

// InitializeParams carries the workspace roots.
type InitializeParams struct {
	// RootURI is deprecated in the specification but still sent by clients
	// that predate workspaceFolders, so both are read.
	RootURI          DocumentURI       `json:"rootUri,omitempty"`
	RootPath         string            `json:"rootPath,omitempty"`
	WorkspaceFolders []WorkspaceFolder `json:"workspaceFolders,omitempty"`
}

// TextDocumentItem is a document the client opened.
type TextDocumentItem struct {
	URI        DocumentURI `json:"uri"`
	LanguageID string      `json:"languageId,omitempty"`
	Version    int32       `json:"version,omitempty"`
	Text       string      `json:"text"`
}

// TextDocumentIdentifier names a document.
type TextDocumentIdentifier struct {
	URI DocumentURI `json:"uri"`
}

// VersionedTextDocumentIdentifier names a document at a version.
type VersionedTextDocumentIdentifier struct {
	URI     DocumentURI `json:"uri"`
	Version int32       `json:"version,omitempty"`
}

// TextDocumentContentChangeEvent is one change. Under full sync the range
// fields are absent and Text holds the whole document.
type TextDocumentContentChangeEvent struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

// DidOpenTextDocumentParams is textDocument/didOpen.
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// DidChangeTextDocumentParams is textDocument/didChange.
type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

// DidSaveTextDocumentParams is textDocument/didSave.
type DidSaveTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text,omitempty"`
}

// DidCloseTextDocumentParams is textDocument/didClose.
type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// ---- hover, definition, completion ----

// MarkupKind selects how hover content is rendered.
type MarkupKind string

// MarkupMarkdown and MarkupPlainText are the two kinds the specification
// defines. Markdown is used so a signature and its declaration line can sit on
// separate lines without a client collapsing them.
const (
	MarkupMarkdown  MarkupKind = "markdown"
	MarkupPlainText MarkupKind = "plaintext"
)

// MarkupContent is rendered hover text.
type MarkupContent struct {
	Kind  MarkupKind `json:"kind"`
	Value string     `json:"value"`
}

// Hover is the result of textDocument/hover.
type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

// Location is a file and a span, used by go-to-definition.
type Location struct {
	URI   DocumentURI `json:"uri"`
	Range Range       `json:"range"`
}

// PositionParams is the shared shape of every request that names a document
// and a cursor position.
type PositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// CompletionItem is one suggestion.
type CompletionItem struct {
	Label  string `json:"label"`
	Kind   uint32 `json:"kind,omitempty"`
	Detail string `json:"detail,omitempty"`
	// Documentation is shown beside the list in most clients.
	Documentation string `json:"documentation,omitempty"`
}

// CompletionItemKind values used by this server.
const (
	CompletionItemKindFunction uint32 = 3
	CompletionItemKindField    uint32 = 5
	CompletionItemKindVariable uint32 = 6
	CompletionItemKindClass    uint32 = 7
	CompletionItemKindConstant uint32 = 14
)

// CompletionList is the result of textDocument/completion.
type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

// HoverProvider and the neighbouring capability markers are object-or-bool in
// the specification. This server sends `true`, which every client accepts, so
// they are plain bools here.

// SymbolKind values used by the document outline.
const (
	SymbolKindFunction uint32 = 12
	SymbolKindVariable uint32 = 13
	SymbolKindConstant uint32 = 14
	SymbolKindClass    uint32 = 5
	SymbolKindField    uint32 = 8
	SymbolKindObject   uint32 = 19
)

// SymbolInformation is one entry of a document outline.
type SymbolInformation struct {
	Name          string   `json:"name"`
	Kind          uint32   `json:"kind"`
	Location      Location `json:"location"`
	ContainerName string   `json:"containerName,omitempty"`
}

// DocumentSymbolParams names a document.
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// ReferenceParams asks for the places a symbol is used.
type ReferenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	// Context.IncludeDeclaration asks whether the declaration itself belongs
	// in the answer. Most clients send false and draw the declaration
	// separately.
	Context ReferenceContext `json:"context"`
}

// ReferenceContext is the options of a references request.
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// SignatureHelp is the result of textDocument/signatureHelp: what the call
// being typed expects.
type SignatureHelp struct {
	Signatures      []SignatureInformation `json:"signatures"`
	ActiveSignature uint32                 `json:"activeSignature,omitempty"`
	ActiveParameter uint32                 `json:"activeParameter,omitempty"`
}

// SignatureInformation is one candidate signature.
type SignatureInformation struct {
	Label         string                 `json:"label"`
	Documentation string                 `json:"documentation,omitempty"`
	Parameters    []ParameterInformation `json:"parameters,omitempty"`
}

// ParameterInformation is one parameter of a signature.
type ParameterInformation struct {
	Label string `json:"label"`
}

// SignatureHelpOptions configures the trigger characters for signature help.
type SignatureHelpOptions struct {
	TriggerCharacters   []string `json:"triggerCharacters,omitempty"`
	RetriggerCharacters []string `json:"retriggerCharacters,omitempty"`
}

// FileEvent is one file that appeared, changed or vanished outside the editor.
type FileEvent struct {
	URI  DocumentURI `json:"uri"`
	Type uint32      `json:"type"` // 1 created, 2 changed, 3 deleted
}

// DidChangeWatchedFilesParams reports filesystem changes the editor noticed.
type DidChangeWatchedFilesParams struct {
	Changes []FileEvent `json:"changes"`
}

// TextEdit replaces a range with new text.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// TextDocumentEdit groups edits for one document.
type TextDocumentEdit struct {
	TextDocument VersionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []TextEdit                      `json:"edits"`
}

// WorkspaceEdit is the result of a rename: edits across several files.
type WorkspaceEdit struct {
	Changes map[DocumentURI][]TextEdit `json:"changes,omitempty"`
}

// RenameParams names the symbol to rename and its new name.
type RenameParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	NewName      string                 `json:"newName"`
}

// CodeActionKind tells the client how to group an action.
type CodeActionKind string

// CodeActionQuickFix is the kind used here: a fix for one diagnostic.
const CodeActionQuickFix CodeActionKind = "quickfix"

// CodeActionParams asks what can be done about a range or its diagnostics.
type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Context      CodeActionContext      `json:"context"`
}

// CodeActionContext carries the diagnostics the client already knows about.
type CodeActionContext struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// CodeAction is one offered fix.
type CodeAction struct {
	Title       string         `json:"title"`
	Kind        CodeActionKind `json:"kind,omitempty"`
	Diagnostics []Diagnostic   `json:"diagnostics,omitempty"`
	Edit        *WorkspaceEdit `json:"edit,omitempty"`
}
