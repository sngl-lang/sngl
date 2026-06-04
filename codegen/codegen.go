package codegen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"text/template"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

// ErrSkip is returned by an OutputFile's WriteTo to indicate the file should
// be skipped entirely (no output written, no error reported).
var ErrSkip = errors.New("skip")

// ExprScope provides context for expression translation (which names are model
// fields vs local vars vs computeds).
type ExprScope struct {
	ModelFields    map[string]bool            // data fields → prefix with model accessor
	ComputedFields map[string]bool            // computed names → call as methods
	FuncNames      map[string]bool            // user-defined function names
	ExternFuncs    map[string]bool            // extern func names from native imports
	ExternVars     map[string]bool            // extern var names from native imports
	LocalVars      map[string]bool            // for-loop vars, params → no prefix
	Renames        map[string]string          // local var renames (original → unique name)
	EventVar       string                     // what "event" maps to in this context
	ContextVar     string                     // expression to supply for native context args (e.g., "r.Context()")
	NeededHelpers  map[string]bool            // helper functions needed (e.g., "String")
	NativeImports  map[string]map[string]bool // module path → set of imported names; populated as native calls are emitted
	// RawFieldAccess names identifiers whose Select-field accesses bypass
	// the usual Export-name capitalization — used by test runners that
	// emit `_test.go` into the same Go package as the generated Model, so
	// they can read and write unexported fields directly (`c.count` for
	// reads, `c.count = …` for writes) instead of going through getters.
	RawFieldAccess map[string]bool
	// MethodFields names identifiers whose bare `recv.<field>` Select
	// access should lower to a method call `recv.<field>()` instead
	// of a raw field read. Used by test runners for #id refs that
	// platform codegen surfaces as methods (e.g. gtk4's nilable
	// conditional/loop refs).
	MethodFields map[string]bool
	// IdentRewrites remaps bare identifiers regardless of scope
	// (LocalVars / ModelFields). Applied first in identifier
	// translation. Used by the Android test path to route every
	// component-level var through a hoisted state object
	// (`count` → `state.count`).
	IdentRewrites map[string]string
	// Pkg is the IR package being translated. Used by translators that need
	// package-level analysis results (e.g. points-to / slot-color for funcvar
	// await inference). May be nil when the scope is constructed without a
	// package (tests, incomplete compilation paths).
	Pkg *ir.Package
}

// NativeAlias produces a deterministic JS identifier for a native module
// import path. Used at both the call site (codegen/lang/javascript) and the
// import-prelude emission (codegen/platform/html) so they agree on the name.
//
//	"./lib"     → "__sngl_n_lib"
//	"../foo/x"  → "__sngl_n_foo_x"
//	"lodash"    → "__sngl_n_lodash"
//	"@scope/x"  → "__sngl_n_scope_x"
func NativeAlias(importPath string) string {
	var b strings.Builder
	b.WriteString("__sngl_n")
	prevSep := true
	for _, r := range importPath {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			if prevSep {
				b.WriteByte('_')
			}
			b.WriteRune(r)
			prevSep = false
		default:
			prevSep = true
		}
	}
	if prevSep && b.Len() == len("__sngl_n") {
		b.WriteString("_anon")
	}
	return b.String()
}

// LangTranslator translates SNGL expressions into a target language's syntax.
type LangTranslator interface {
	ir.Language

	// Capabilities declares which high-level SNGL constructs this language's
	// translator cannot consume. Drives lowering passes; return lower.Caps{}
	// when no lowering is needed.
	Capabilities() lower.Caps

	GenerateIdentifier(name *ir.Ident) string

	// IR-typed public API. TranslateIRExpr/TranslateIRMutation were removed
	// from the interface once the html platform moved fully onto each
	// language's *IRContext path; languages may still implement them as
	// concrete (non-interface) methods for internal use (e.g. golang/http.go
	// calls golang.Translator.TranslateIRMutation directly).
	TranslateIRLiteral(e ir.Expr) string

	TypeToNative(hint string) string
	ExportName(name string) string

	// NewFileEmitter returns a per-file emitter that owns header,
	// imports, body buffer, and source-map sidecar internally. The
	// platform writes IR-translated content through the emitter; Close
	// flushes the assembled file to sink under opts.Name. See
	// FileEmitter and FileOptions.
	NewFileEmitter(sink Sink, opts FileOptions) FileEmitter
}

// UnimplementedFileEmitter is the placeholder returned by languages that
// haven't migrated to the FileEmitter surface yet. Every method that
// requires actual emission returns an error indicating the language is
// not migrated. Used by codegen.RequireFileEmitter to error early when
// a platform attempts file-emitter usage against a non-migrated lang.
type UnimplementedFileEmitter struct {
	Lang string
}

func (u *UnimplementedFileEmitter) Write(p []byte) (int, error) {
	return 0, fmt.Errorf("%s: FileEmitter not yet implemented", u.Lang)
}
func (u *UnimplementedFileEmitter) EvalExpr(ir.Expr) string          { return "" }
func (u *UnimplementedFileEmitter) EvalStmt(ir.Stmt) []string        { return nil }
func (u *UnimplementedFileEmitter) RequireImport(path string) string { return path }
func (u *UnimplementedFileEmitter) Close() error {
	return fmt.Errorf("%s: FileEmitter not yet implemented", u.Lang)
}

// FileOptions configures a per-file emitter.
type FileOptions struct {
	// Name is the output filename under the sink, e.g. "model.go".
	Name string
	// Source is the SNGL source filename for the generated-by header.
	// Empty suppresses the header.
	Source string
	// Platform identifies the platform in the generated-by header
	// (e.g. "gtk4"). Empty suppresses the header.
	Platform string
	// PackageName, when non-empty, is rendered as the file's package
	// declaration where the language requires one (Go, Kotlin). Empty
	// means the platform writes the package decl itself or the language
	// has no notion of one.
	PackageName string
	// Maps enables source-map emission: Go inline //line directives,
	// JS data-URL sourceMappingURL.
	Maps bool
	// CgoPreamble, when non-empty, is emitted immediately after the package
	// clause and before the regular import block (Go only). It holds the cgo
	// comment block plus its `import "C"` line, which must sit on their own
	// and precede other imports.
	CgoPreamble string
}

// FileEmitter is the per-file rendering surface. Languages own their
// concrete implementation; the platform writes through this generic
// interface so its code stays lang-agnostic.
//
// Lifecycle:
//  1. Platform asks the language: `e := lang.NewFileEmitter(sink, opts)`.
//  2. Platform writes via io.Writer (raw bytes for things like function
//     signatures) and/or calls EvalExpr/EvalStmt to translate IR. Each
//     translation call may auto-register imports as a side-effect.
//  3. Platform calls Close. The emitter flushes header + package decl
//     + import block + body to the sink as a single file, plus any
//     source-map sidecar.
//
// Imports are tracked internally. The platform never sees raw paths;
// when it needs a specific alias for an import it explicitly registers,
// it gets the alias back from RequireImport.
type FileEmitter interface {
	io.Writer

	// EvalExpr translates an IR expression to its language form,
	// auto-registering any imports the expression references.
	EvalExpr(e ir.Expr) string

	// EvalStmt translates an IR statement to one or more lines. The
	// caller decides indentation/joining.
	EvalStmt(s ir.Stmt) []string

	// RequireImport registers a native package the emitted file needs,
	// returning the local alias the caller should use at the call site.
	// Repeated calls for the same path return the same alias.
	RequireImport(path string) string

	// Close flushes assembled file content to the sink and closes the
	// underlying writer. Idempotent; second call returns nil.
	Close() error
}

// PlatformGenerator produces output files from a checked SNGL document.
type PlatformGenerator interface {
	ir.Platform
	SupportedLangs() []string

	// Capabilities declares which high-level SNGL constructs this platform
	// cannot consume. Drives lowering passes; return lower.Caps{} when no
	// lowering is needed.
	Capabilities() lower.Caps

	Generate(req *Request, sink Sink) error
}

// OptionConfigurable is optionally implemented by PlatformGenerators that
// accept CLI options affecting the type-check phase (not just code generation).
// The CLI calls Configure with the parsed --opt key=value map before checkDoc
// so platform Resolve() can honor the supplied options.
type OptionConfigurable interface {
	Configure(opts map[string]string) error
}

// TestRunner is optionally implemented by PlatformGenerators that provide
// their own test execution (e.g., browser-based testing for HTML).
//
// opts carries the same merged stdlib+lang+platform options struct that
// Generate receives via Request.Options. Platform implementations
// typically forward it to codegen.ApplyOptions to populate their Config.
type TestRunner interface {
	RunTests(pkg *ir.Package, lang LangTranslator, opts *ir.StructLit) ([]*TestResult, error)
}

// TestProber is optionally implemented by TestRunners that can detect
// whether their headless harness can run on the current host. When absent,
// callers assume the runner is always available. The Probe call must be
// cheap (no spawning processes, no network); reuse cached results across a
// single CLI invocation.
type TestProber interface {
	ProbeTest() (ok bool, reason string)
}

// TestResult holds the outcome of a single test.
type TestResult struct {
	Component string
	Desc      string
	Passed    bool
	// Error is the human-readable failure text. When multiple soft
	// (`t.assert`) failures occurred their messages are joined with
	// newlines; a fatal failure (`t.must` or runtime error) appears as
	// the last entry.
	Error string
	// ErrorLine is the 1-based source line of the first failure. Zero
	// when not available.
	ErrorLine int
	// Failures is the structured list of every recorded failure, in the
	// order they happened. Empty on success.
	Failures []TestFailure
	Log      []string
	Children []*TestResult
	Duration time.Duration
}

// TestFailure describes a single recorded failure on a test result.
type TestFailure struct {
	Line    int    // 1-based source line of the failing expression
	Message string // structured message, same shape as TestResult.Error
	Fatal   bool   // true for must() / runtime errors that halted the test
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
	Snapshot(pkg *ir.Package, lang LangTranslator, width, height int) ([]byte, error)
}

// TextSnapshotter is optionally implemented by TUI platforms that can render
// their output as ANSI text. This provides a .txt alternative to PNG
// screenshots, useful for terminal-native documentation and diffing.
type TextSnapshotter interface {
	SnapshotText(pkg *ir.Package, lang LangTranslator, width, height int) ([]byte, error)
}

// BatchDoc pairs an identifier with a type-checked package and its
// language translator for batch snapshotting.
type BatchDoc struct {
	ID   string // unique identifier, used as result map key
	Pkg  *ir.Package
	Lang LangTranslator
}

// BatchSnapshotter is optionally implemented by platforms that can amortize
// build costs across multiple snapshots. The orchestrator groups documents
// by platform and calls BatchSnapshot instead of individual Snapshot calls.
type BatchSnapshotter interface {
	BatchSnapshot(docs []BatchDoc, width, height int) (map[string][]byte, error)
}

// BatchTextSnapshotter is optionally implemented by TUI platforms that can
// render multiple documents as ANSI text in a single build cycle.
type BatchTextSnapshotter interface {
	BatchSnapshotText(docs []BatchDoc, width, height int) (map[string][]byte, error)
}

// Runner is optionally implemented by PlatformGenerators that can execute
// their generated output directly (e.g., "go run" for bubbletea, open
// browser for HTML, adb install for Android).
type Runner interface {
	Run(dir string, opts *ir.StructLit, args []string) error
}

// LangRunner is optionally implemented by LangTranslators that know how to
// execute generated code in a temp directory. Platforms delegate to this
// interface so execution logic lives in the language, not each platform.
type LangRunner interface {
	// RunDir bootstraps a module in dir and runs the generated code.
	// goVersion sets the toolchain version emitted in go.mod (Go lang).
	// goModExtra is appended verbatim — typically a `replace …` directive
	// pointing SNGL runtime imports at a local checkout when developing.
	RunDir(dir, goVersion, goModExtra string, args []string) error
}

// Builder is optionally implemented by PlatformGenerators that have a build
// step between code generation and execution (e.g., compiling an APK).
type Builder interface {
	Build(dir string, opts *ir.StructLit) (artifact string, err error)
}

// TestLauncher launches a compiled SNGL program in agent mode and
// returns an RPC channel for the sngl test driver to use. Implementable
// by either a PlatformGenerator (custom build/launch lifecycle — e.g.
// android APK + adb forward) or a LangTranslator (default lifecycle —
// compile a binary and spawn it with stdin/stdout RPC). The driver
// tries the platform first and falls back to the language; mirrors
// codegen.Builder's resolution.
type TestLauncher interface {
	// LaunchTest compiles the package, spawns the agent-mode binary, and
	// returns a connected RPCChannel plus a Cleanup func the caller must
	// invoke when done. dir is the codegen output directory (already
	// populated with sources + the linked testagent runtime).
	LaunchTest(ctx context.Context, dir string, lang LangTranslator, opts *ir.StructLit) (RPCChannel, Cleanup, error)
}

// SkipError signals that a target's prerequisites are missing on the
// host (e.g. pkg-config can't find gtk4). sngl test surfaces this as
// a SKIP line rather than a FAIL. Match via errors.As.
type SkipError struct {
	Reason string
}

func (e *SkipError) Error() string { return "skip: " + e.Reason }

// RPCChannel is a full-duplex byte stream over which the driver and a
// running testagent exchange JSON-RPC messages.
type RPCChannel interface {
	io.ReadWriteCloser
}

// Cleanup runs after the driver finishes with the channel. Idempotent.
type Cleanup func()

// HTTPCompiler is optionally implemented by LangTranslators that can generate
// HTTP server code. The html platform's route mode delegates all
// language-specific code generation to this interface: handler scaffolding,
// framework-specific mux syntax for dynamic route paths, server start
// (main/ListenAndServe equivalents), and the chosen filenames for the emitted
// output package. The platform itself stays language- and framework-agnostic.
type HTTPCompiler interface {
	CompileHTTP(req *HTTPRequest) ([]*OutputFile, error)
}

// HTTPRequest describes what the platform needs the language to generate.
// Route paths are abstract templates (e.g. "/users/{name}"); the language
// translates {param} placeholders into its framework's routing syntax.
type HTTPRequest struct {
	Pkg        *ir.Package               // v2 checked IR
	Package    string                    // target package name (e.g., "main", "ui")
	Main       bool                      // generate standalone server with main()
	Framework  string                    // HTTP framework: "net/http", "gin", "echo"
	Routes     []HTTPRoute               // window → route mapping
	RenderHTML func(routeIdx int) string // callback: returns HTML body for a route
}

// HTTPRoute maps a window to an HTTP route.
type HTTPRoute struct {
	Name      string       // handler function name (e.g., "handleHome")
	Path      string       // URL path template: "/", "/about", "/users/{name}"
	Title     string       // page title
	Params    []string     // route parameter names extracted from Path (e.g., ["name"])
	WindowIdx int          // index into CodegenCtx.Windows()
	Actions   []HTTPAction // server-state form actions (POST handlers)
}

// HTTPAction describes a form-based server action triggered by an event handler
// whose mutation crosses the target-language boundary (e.g. invokes a go://
// function when compiling with --lang go).
type HTTPAction struct {
	Name      string    // action identifier (e.g., "action0")
	Mutations []ir.Stmt // type-checked handler body to execute server-side
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
	// HasErrorReturn reports whether the underlying Go function has a
	// trailing error result (already stripped from ReturnType). The bridge
	// must consume it from the call and surface failures to JS.
	HasErrorReturn bool
}

// CCompiler is optionally implemented by LangTranslators that can emit C FFI
// call sites via cgo. Checked via type assertion at codegen time.
type CCompiler interface {
	// EmitCHeader returns the cgo preamble comment block and `import "C"` line
	// for the given C native imports. Called once per output file.
	// Returns empty string when imports is empty.
	EmitCHeader(imports []*ir.NativeImport) string
}

// MutationModelEmitter is optionally implemented by platforms that use the
// Document+Mutations model: emit a static tree once, then generate targeted
// updater functions to patch specific parts when state changes.
//
// Platforms: HTML, Fyne.
type MutationModelEmitter interface {
	BuildMutationModel(req *Request, analysis *CommonAnalysis) (*MutationModel, error)
	EmitFromMutation(m *MutationModel, req *Request, sink Sink) error
}

// RenderModelEmitter is optionally implemented by platforms that use the
// Render Loop model: re-render the full view from state on every change,
// letting the framework handle diffing.
//
// Platforms: BubbleTea, Android/Compose.
type RenderModelEmitter interface {
	BuildRenderModel(req *Request, analysis *CommonAnalysis) (*RenderModel, error)
	EmitFromRender(m *RenderModel, req *Request, sink Sink) error
}

// MutationCompilerFactory is optionally implemented by PlatformGenerators that
// expose a per-request MutationModelEmitter. The singleton Generator stays
// stateless; each call returns a fresh compilation object that owns per-request
// build state across BuildMutationModel and EmitFromMutation.
type MutationCompilerFactory interface {
	NewMutationCompiler() MutationModelEmitter
}

// RenderCompilerFactory is the render-loop counterpart to
// MutationCompilerFactory.
type RenderCompilerFactory interface {
	NewRenderCompiler() RenderModelEmitter
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

// FileAsset records a file that must be copied to the output directory.
type FileAsset struct {
	SrcPath string // absolute path on disk
	OutPath string // relative path in output (e.g. "assets/sngl.svg")
	Data    []byte // optional: contents already read by the optimizer
}

// Request is the input to a platform generator.
type Request struct {
	Doc        *ast.Document // Deprecated: use Pkg. Will be removed when all platforms are ported.
	Pkg        *ir.Package   // v2 checked IR
	Lang       LangTranslator
	Options    *ir.StructLit // merged stdlib+lang+platform options for this output target
	Source     string        // source .sngl filename (base name only)
	FileAssets []FileAsset   // file:// assets to copy to output
	// ProjectFS is the filesystem the project sources were read from. Used
	// by platforms that resolve native imports (`js://`, `go://`) at codegen
	// time so the same code path serves CLI (os.DirFS) and the in-memory
	// playground. May be nil; callers that need it must fall back to
	// os.DirFS(projectDir) from the options struct.
	ProjectFS fs.FS
	// Maps enables source-map generation: position tracking on, sourcemap
	// sidecars emitted via CodeWriter. Sourced from the top-level "maps"
	// field of the output() options block (lib/options.sngl).
	Maps bool
}

// SplitScheme separates a scheme prefix (e.g. "go") from the rest of an import
// path. Returns ("", path) when no scheme is present.
func SplitScheme(path string) (scheme, uri string) {
	if before, after, ok := strings.Cut(path, "://"); ok {
		return before, after
	}
	return "", path
}

// Header returns a generated-file comment for the given platform and comment
// style.  When source is empty (playground, tests, etc.) it returns "".
func Header(platform, source, commentStart, commentEnd string) string {
	if source == "" {
		return ""
	}
	return commentStart + "Code generated by sngl (" + platform + ") from " + source + "; DO NOT EDIT." + commentEnd + "\n\n"
}
