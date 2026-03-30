package codegen

import (
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
)

// ExprScope provides context for expression translation (which names are model
// fields vs local vars vs computeds).
type ExprScope struct {
	ModelFields    map[string]bool   // data fields → prefix with model accessor
	ComputedFields map[string]bool   // computed names → call as methods
	FuncNames      map[string]bool   // user-defined function names
	ExternFuncs    map[string]bool   // extern func names from native imports
	ExternVars     map[string]bool   // extern var names from native imports
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
	RunTests(doc *ast.Document, lang LangTranslator, tests []*ast.TestDef) ([]*TestResult, error)
}

// TestResult holds the outcome of a single test.
type TestResult struct {
	Component string
	Desc      string
	Passed    bool
	Error     string
	Children  []*TestResult
	Duration  time.Duration
}

// PreviewStyler is optionally implemented by PlatformGenerators that want
// to provide CSS to style the HTML preview to resemble their target.
type PreviewStyler interface {
	PreviewCSS() string
}

// Snapshotter is optionally implemented by PlatformGenerators that capture
// their own screenshots (e.g., browser screenshots for HTML, terminal
// screenshots for bubbletea).
type Snapshotter interface {
	Snapshot(doc *ast.Document, lang LangTranslator, width, height int) ([]byte, error)
}

// Runner is optionally implemented by PlatformGenerators that can execute
// their generated output directly (e.g., "go run" for bubbletea, open
// browser for HTML, adb install for Android).
type Runner interface {
	Run(dir string, args []string) error
}

// APIProvider is optionally implemented by LangTranslator or PlatformGenerator
// to expose a pre-defined SNGL API as a checker namespace. The returned document's
// structs, enums, data, and components become available under the lang/platform name.
type APIProvider interface {
	API() *ast.Document
}

// APIResolver is optionally implemented alongside or instead of APIProvider
// for dynamic name resolution when a name isn't found in the static API document.
type APIResolver interface {
	ResolveAPI(name string) *ast.NativeDecls
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
