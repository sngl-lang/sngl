package lsp

// JSON-RPC 2.0 and LSP protocol types.
// Only the subset needed for diagnostics, hover, and completion.

// --- JSON-RPC 2.0 ---

type Request struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RespError  `json:"error,omitempty"`
}

type RespError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type Notification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// --- LSP Base ---

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type TextDocumentContentChangeEvent struct {
	Text string `json:"text"`
}

// --- Initialize ---

type InitializeParams struct {
	ProcessID int         `json:"processId"`
	RootURI   string      `json:"rootUri"`
	RootPath  string      `json:"rootPath"`
	Trace     string      `json:"trace"`
	Params    interface{} `json:"initializationOptions"`
}

type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   *ServerInfo        `json:"serverInfo,omitempty"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type ServerCapabilities struct {
	TextDocumentSync   int                `json:"textDocumentSync"` // 1=Full
	HoverProvider      bool               `json:"hoverProvider,omitempty"`
	CompletionProvider *CompletionOptions `json:"completionProvider,omitempty"`
	DiagnosticProvider *DiagnosticOptions `json:"diagnosticProvider,omitempty"`
}

type CompletionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
}

type DiagnosticOptions struct {
	InterFileDependencies bool `json:"interFileDependencies"`
	WorkspaceDiagnostics  bool `json:"workspaceDiagnostics"`
}

// --- Document Sync ---

type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type DidSaveTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// --- Diagnostics ---

type Diagnostic struct {
	Range    Range        `json:"range"`
	Severity DiagSeverity `json:"severity"`
	Source   string       `json:"source,omitempty"`
	Message  string       `json:"message"`
}

type DiagSeverity int

const (
	SeverityError       DiagSeverity = 1
	SeverityWarning     DiagSeverity = 2
	SeverityInformation DiagSeverity = 3
	SeverityHint        DiagSeverity = 4
)

type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// --- Hover ---

type HoverParams = TextDocumentPositionParams

type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// --- Completion ---

type CompletionParams struct {
	TextDocumentPositionParams
	Context *CompletionContext `json:"context,omitempty"`
}

type CompletionContext struct {
	TriggerKind      int    `json:"triggerKind"`
	TriggerCharacter string `json:"triggerCharacter,omitempty"`
}

type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

type CompletionItem struct {
	Label         string `json:"label"`
	Kind          int    `json:"kind,omitempty"`
	Detail        string `json:"detail,omitempty"`
	Documentation string `json:"documentation,omitempty"`
	InsertText    string `json:"insertText,omitempty"`
}

// CompletionItemKind constants
const (
	CIKText       = 1
	CIKMethod     = 2
	CIKFunction   = 3
	CIKField      = 5
	CIKVariable   = 6
	CIKClass      = 7
	CIKInterface  = 8
	CIKModule     = 9
	CIKProperty   = 10
	CIKKeyword    = 14
	CIKSnippet    = 15
	CIKValue      = 12
	CIKEnum       = 13
	CIKEnumMember = 20
	CIKStruct     = 22
	CIKEvent      = 23
	CIKColor      = 16
	CIKConstant   = 21
)
