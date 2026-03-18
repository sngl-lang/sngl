package codegen

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/testrunner"
)

// ExprScope provides context for expression translation (which names are model
// fields vs local vars vs computeds).
type ExprScope struct {
	ModelFields    map[string]bool   // data fields → prefix with model accessor
	ComputedFields map[string]bool   // computed names → call as methods
	LocalVars      map[string]bool   // for-loop vars, params → no prefix
	Renames        map[string]string // local var renames (original → unique name)
	EventVar       string            // what "event" maps to in this context
}

// LangTranslator translates SNGL expressions into a target language's syntax.
type LangTranslator interface {
	Lang() string
	TranslateExpr(e ast.Node, scope *ExprScope) string
	TranslateMutation(e ast.Node, scope *ExprScope) []string
	TranslateLiteral(expr ast.Expr) string
	TypeToNative(hint string) string // "int" → "int", "float" → "float64", etc.
	ExportName(name string) string   // capitalize for Go, camelCase for TS, etc.
}

// PlatformGenerator produces output files from a checked SNGL document.
type PlatformGenerator interface {
	Platform() string
	SupportedLangs() []string
	Generate(req *Request) (*Response, error)
}

// TestRunner is optionally implemented by PlatformGenerators that provide
// their own test execution (e.g., browser-based testing for HTML).
type TestRunner interface {
	RunTests(doc *ast.Document, lang LangTranslator, tests []*ast.TestDef) ([]*testrunner.Result, error)
}

// PreviewStyler is optionally implemented by PlatformGenerators that want
// to provide CSS to style the HTML preview to resemble their target.
type PreviewStyler interface {
	PreviewCSS() string
}

// OutputFile represents a single generated file.
type OutputFile struct {
	Name    string // relative path, e.g. "model.go"
	Content []byte
}

// Request is the input to a platform generator.
type Request struct {
	Doc     *ast.Document
	Lang    LangTranslator
	Options map[string]string // key=value from --opt flags
}

// Response is the output from a platform generator.
type Response struct {
	Files []*OutputFile
	Error string // non-empty on failure
}
