package codegen

import (
	"errors"
	"io"
	"io/fs"
	"strings"
	"text/template"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
)

// ErrSkip is returned by an OutputFile's WriteTo to indicate the file should
// be skipped entirely (no output written, no error reported).
var ErrSkip = errors.New("skip")

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
	NeededHelpers  map[string]bool   // helper functions needed (e.g., "String")
}

// LangTranslator translates SNGL expressions into a target language's syntax.
type LangTranslator interface {
	Lang() string
	PkgSource() string // .sngl package source for this language
	TranslateExpr(e ast.Node, scope *ExprScope) string
	TranslateMutation(e ast.Node, scope *ExprScope) []string
	TranslateLiteral(expr ast.Expr) string
	TypeToNative(hint string) string // "int" → "int", "float" → "float64", etc.
	ExportName(name string) string   // capitalize for Go, camelCase for TS, etc.
}

// PlatformGenerator produces output files from a checked SNGL document.
type PlatformGenerator interface {
	Platform() string
	PkgSource() string // .sngl package source for this platform
	SupportedLangs() []string
	Generate(req *Request) (*Response, error)
}

// TestRunner is optionally implemented by PlatformGenerators that provide
// their own test execution (e.g., browser-based testing for HTML).
type TestRunner interface {
	RunTests(doc *ast.Document, lang LangTranslator) ([]*TestResult, error)
}

// TestResult holds the outcome of a single test.
type TestResult struct {
	Component string
	Desc      string
	Passed    bool
	Error     string
	Log       []string
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

// TextSnapshotter is optionally implemented by TUI platforms that can render
// their output as ANSI text. This provides a .txt alternative to PNG
// screenshots, useful for terminal-native documentation and diffing.
type TextSnapshotter interface {
	SnapshotText(doc *ast.Document, lang LangTranslator, width, height int) ([]byte, error)
}

// Runner is optionally implemented by PlatformGenerators that can execute
// their generated output directly (e.g., "go run" for bubbletea, open
// browser for HTML, adb install for Android).
type Runner interface {
	Run(dir string, opts map[string]string, args []string) error
}

// Builder is optionally implemented by PlatformGenerators that have a build
// step between code generation and execution (e.g., compiling an APK).
type Builder interface {
	Build(dir string, opts map[string]string) (artifact string, err error)
}

// HTTPCompiler is optionally implemented by LangTranslators that can generate
// HTTP server code. The http platform delegates language-specific code generation
// (handler scaffolding, server main, framework-specific routing) to this interface.
type HTTPCompiler interface {
	CompileHTTP(req *HTTPRequest) ([]byte, error)
}

// HTTPRequest describes what the http platform needs the language to generate.
type HTTPRequest struct {
	Doc         *ast.Document
	Package     string       // target package name (e.g., "main", "ui")
	Main        bool         // generate standalone server with main()
	Framework   string       // HTTP framework: "net/http", "gin", "echo"
	Routes      []HTTPRoute  // window → route mapping
	RenderHTML  func(routeIdx int) string // callback: returns HTML body for a route
}

// HTTPRoute maps a window to an HTTP route.
type HTTPRoute struct {
	Name      string       // handler function name (e.g., "handleHome")
	Path      string       // URL path: "/", "/about", "/{name}"
	Title     string       // page title (may contain {param} placeholders)
	Params    []string     // route parameter names extracted from Path (e.g., ["name"])
	WindowIdx int          // index into doc.App.EffectiveWindows()
	Actions   []HTTPAction // server-state form actions (POST handlers)
}

// HTTPAction describes a form-based server action triggered by a button click.
type HTTPAction struct {
	Name string    // action identifier (e.g., "action0")
	Expr ast.Expr  // the mutation expression to execute
}

// WASMCompiler is optionally implemented by LangTranslators that can compile
// imported packages to WebAssembly with JS bindings. Platforms like HTML check
// for this interface to enable go:// (or other scheme) imports at runtime.
type WASMCompiler interface {
	// BuildWASM compiles a package to WASM and returns the binary.
	// projectDir is the project root (for module resolution).
	// importPath is the Go/native package path.
	// funcs lists which functions need JS bindings.
	BuildWASM(projectDir, importPath string, funcs []WASMFunc) ([]byte, error)

	// WASMExecJS returns the runtime support JS needed to bootstrap WASM
	// (e.g., Go's wasm_exec.js).
	WASMExecJS() ([]byte, error)
}

// WASMFunc describes a function to expose from WASM to JavaScript.
type WASMFunc struct {
	Name       string
	ParamTypes []string
	ReturnType string
}

// APIProvider is optionally implemented by LangTranslator or PlatformGenerator
// to expose a pre-defined SNGL API as a checker namespace. The returned document's
// structs, enums, data, and components become available under the lang/platform name.
//
// Deprecated: Use PkgSource instead for .sngl-based package definitions.
type APIProvider interface {
	API() *ast.Document
}

// APIResolver is optionally implemented alongside or instead of APIProvider
// for dynamic name resolution when a name isn't found in the static API document.
type APIResolver interface {
	ResolveAPI(name string) *ast.NativeDecls
}

// MutationModelEmitter is optionally implemented by platforms that use the
// Document+Mutations model: emit a static tree once, then generate targeted
// updater functions to patch specific parts when state changes.
//
// Platforms: HTML, Fyne.
type MutationModelEmitter interface {
	BuildMutationModel(req *Request, analysis *CommonAnalysis) (*MutationModel, error)
	EmitFromMutation(m *MutationModel, req *Request) (*Response, error)
}

// RenderModelEmitter is optionally implemented by platforms that use the
// Render Loop model: re-render the full view from state on every change,
// letting the framework handle diffing.
//
// Platforms: BubbleTea, Android/Compose.
type RenderModelEmitter interface {
	BuildRenderModel(req *Request, analysis *CommonAnalysis) (*RenderModel, error)
	EmitFromRender(m *RenderModel, req *Request) (*Response, error)
}

// OutputFile represents a single generated file. Its WriteTo function writes
// the file content lazily, allowing template execution to be deferred to
// write time.
type OutputFile struct {
	Name    string // relative path, e.g. "model.go"
	WriteTo func(w io.Writer) (int64, error)
}

// BytesFile creates an OutputFile backed by a byte slice.
func BytesFile(name string, content []byte) *OutputFile {
	return &OutputFile{
		Name: name,
		WriteTo: func(w io.Writer) (int64, error) {
			n, err := w.Write(content)
			return int64(n), err
		},
	}
}

// TemplateFile creates an OutputFile that executes a Go template lazily.
// If the template calls {{skip}}, WriteTo returns ErrSkip.
func TemplateFile(name string, tmpl *template.Template, data any) *OutputFile {
	return &OutputFile{
		Name: name,
		WriteTo: func(w io.Writer) (n int64, err error) {
			defer func() {
				if r := recover(); r != nil {
					if skipErr, ok := r.(skipError); ok {
						err = skipErr
					} else {
						panic(r)
					}
				}
			}()
			cw := &countWriter{w: w}
			err = tmpl.Execute(cw, data)
			return cw.n, err
		},
	}
}

// TemplateFuncs returns the base template FuncMap with the skip function.
func TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"skip": func() string { panic(skipError(ErrSkip)) },
	}
}

// RenderTemplates walks an embedded template FS, executing each .tmpl file
// against data. The output file name is the path with .tmpl stripped.
// Templates that call {{skip}} are silently omitted.
func RenderTemplates(fsys fs.FS, root string, data any) []*OutputFile {
	var files []*OutputFile
	fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".tmpl") {
			return nil
		}
		tmpl, err := template.New(d.Name()).Funcs(TemplateFuncs()).ParseFS(fsys, path)
		if err != nil {
			return nil
		}
		outName := strings.TrimPrefix(path, root+"/")
		outName = strings.TrimSuffix(outName, ".tmpl")
		files = append(files, TemplateFile(outName, tmpl, data))
		return nil
	})
	return files
}

type skipError error

type countWriter struct {
	w io.Writer
	n int64
}

func (cw *countWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.n += int64(n)
	return n, err
}

// Opts are globally-applicable generator options set at the output level.
type Opts struct {
	Name string // display name for the app
	Icon string // path to icon file (SVG or PNG)
}

// Request is the input to a platform generator.
type Request struct {
	Doc     *ast.Document
	Lang    LangTranslator
	Opts    Opts
	Options map[string]string // key=value from --opt flags
	Source  string            // source .sngl filename (base name only)
}

// Header returns a generated-file comment for the given platform and comment
// style.  When source is empty (playground, tests, etc.) it returns "".
func Header(platform, source, commentStart, commentEnd string) string {
	if source == "" {
		return ""
	}
	return commentStart + "Code generated by sngl (" + platform + ") from " + source + "; DO NOT EDIT." + commentEnd + "\n\n"
}

// Response is the output from a platform generator.
type Response struct {
	Files []*OutputFile
	Error string // non-empty on failure
}
