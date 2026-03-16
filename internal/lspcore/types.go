package lspcore

// Position represents a 0-based line/character position (LSP convention).
type Position struct {
	Line      int
	Character int
}

// Range is a start-end pair of positions.
type Range struct {
	Start Position
	End   Position
}

// Diagnostic represents a single diagnostic (error, warning, etc.).
type Diagnostic struct {
	Range    Range
	Severity DiagSeverity
	Source   string
	Message  string
}

type DiagSeverity int

const (
	SeverityError       DiagSeverity = 1
	SeverityWarning     DiagSeverity = 2
	SeverityInformation DiagSeverity = 3
	SeverityHint        DiagSeverity = 4
)

// CompletionItem represents a single completion suggestion.
type CompletionItem struct {
	Label         string
	Kind          int
	Detail        string
	Documentation string
	InsertText    string
}

// CompletionItemKind constants.
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
	CIKValue      = 12
	CIKEnum       = 13
	CIKKeyword    = 14
	CIKSnippet    = 15
	CIKColor      = 16
	CIKEnumMember = 20
	CIKConstant   = 21
	CIKStruct     = 22
	CIKEvent      = 23
)
