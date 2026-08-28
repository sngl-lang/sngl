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
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

// ErrSkip is returned by an OutputFile's WriteTo to indicate the file should
// be skipped entirely (no output written, no error reported).
var ErrSkip = errors.New("skip")

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

	// Capabilities returns the SNGL constructs this language can natively emit.
	// The platform receives these features and may restrict them further before
	// lowering runs. See lower.Features and lower.AllFeatures.
	Capabilities() lower.Features

	GenerateIdentifier(name *ir.Ident) string

	TranslateIRLiteral(e ir.Expr) string

	TypeToNative(hint string) string
	ExportName(name string) string

	// NewFileEmitter returns a per-file emitter that owns header, imports,
	// body buffer, and source-map sidecar internally.
	NewFileEmitter(sink Sink, opts FileOptions) FileEmitter
}

// UnimplementedFileEmitter is returned by languages that have not migrated to
// the FileEmitter surface; every emitting method errors.
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

// FileEmitter is the per-file rendering surface. Languages own the concrete
// implementation so the platform's code stays lang-agnostic: it writes raw
// bytes and/or IR through the emitter, then Close flushes header, package
// decl, import block and body to the sink as one file.
//
// Imports are tracked internally; the platform never sees raw paths, and gets
// an alias back from RequireImport for one it registers explicitly.
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

	// Capabilities receives the active language and returns the combined set of
	// features the platform+language pair can natively emit. The platform
	// should start from lang.Capabilities(), restrict what it cannot consume,
	// and add StructComponents / StdlibContextParam when required. The lang
	// argument may be type-asserted to check for optional language extensions.
	Capabilities(lang LangTranslator) lower.Features

	Generate(req *Request, sink Sink) error
}

// OptionConfigurable is optionally implemented by PlatformGenerators that
// accept CLI options affecting the type-check phase (not just code generation).
// The CLI calls Configure with the parsed --opt key=value map before checkDoc,
// so a platform's declarations can honor the supplied options.
type OptionConfigurable interface {
	Configure(opts map[string]string) error
}

// ForeignIntrinsics is optionally implemented by a PlatformGenerator that
// renders an #[intrinsic] component from another platform's namespace.
// Naming a foreign primitive is otherwise an error (lower's ForeignPrimitive
// pass); a platform that does implement one says so here rather than having it
// assumed. No platform implements this today.
type ForeignIntrinsics interface {
	// ClaimsIntrinsic reports whether this platform emits the component
	// carrying this #[intrinsic] id, e.g. "android:Column".
	ClaimsIntrinsic(id string) bool
}

// ClaimsIntrinsicFunc returns p's foreign-intrinsic predicate, or nil when it
// claims none. Shaped for lower.Options.ClaimsIntrinsic, which cannot name this
// interface: lower is below codegen.
func ClaimsIntrinsicFunc(p PlatformGenerator) func(string) bool {
	if fi, ok := p.(ForeignIntrinsics); ok {
		return fi.ClaimsIntrinsic
	}
	return nil
}

// PlatformAvailability is optionally implemented by PlatformGenerators whose
// component vocabulary depends on files outside this repository — today only
// gtk4 and its GIR file.
//
// An unavailable platform also stops contributing its sngl://platforms/<id>
// declarations: the checker merges every registered platform's overrides
// regardless of build target, so overrides naming types it cannot resolve
// would fail every compile in the process.
type PlatformAvailability interface {
	// Unavailable returns nil when the platform can be used here, or an error
	// naming what is missing and how to supply it.
	Unavailable() error
}

// PlatformUnavailable reports why the named platform cannot be used in this
// environment, or nil when it can (including for platforms that do not
// implement PlatformAvailability, which are always usable). An unregistered
// name is reported as unavailable.
func PlatformUnavailable(name string) error {
	p := LookupPlatform(name)
	if p == nil {
		return fmt.Errorf("unknown platform %q", name)
	}
	if a, ok := p.(PlatformAvailability); ok {
		return a.Unavailable()
	}
	return nil
}

// PlatformDocs returns the SNGL declarations p contributes — the source of its
// `sngl://platforms/<id>` package, both what lib/ embeds and what p
// synthesizes — or nil when it declares none or cannot be used here. gtk4
// without a GIR file has no widget set to declare and its overrides are
// written against that set, so it contributes nothing rather than declarations
// no one can check.
func PlatformDocs(p PlatformGenerator) []*ast.Document {
	if p == nil {
		return nil
	}
	if a, ok := p.(PlatformAvailability); ok && a.Unavailable() != nil {
		return nil
	}
	return append(checker.PackageDocsFor("platforms/"+p.PlatformIdentifier()),
		checker.ProvidedDocs(p)...)
}

// LangDocs returns the SNGL declarations l contributes -- the source of its
// `sngl://languages/<id>` package, both what lib/ embeds and what l serves
// itself -- or nil when it declares none. The path is keyed by the language's
// own identifier, so Go's package is languages/go.
func LangDocs(l LangTranslator) []*ast.Document {
	if l == nil {
		return nil
	}
	return append(checker.PackageDocsFor("languages/"+l.LanguageIdentifier()),
		checker.ProvidedDocs(l)...)
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
	// Skipped marks a result that never ran — a launcher whose
	// prerequisites are missing, or a test calling t.skip. It is neither
	// a pass nor a failure, so reporters must not fold it into either.
	Skipped    bool
	SkipReason string
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
	RenderHTML func(routeIdx int) string // Deprecated: returns a baked static HTML body for a route. Phase 3 introduced the structured HTTPRoute.Render (RouteRender) model that carries IR-expr holes; the language renders those via its own *IRContext. RenderHTML remains until Phase 4 switches the Go consumer onto Render.
}

// HTTPRoute maps a window to an HTTP route.
type HTTPRoute struct {
	Name      string       // handler function name (e.g., "handleHome")
	Path      string       // URL path template: "/", "/about", "/users/{name}"
	Title     string       // page title
	Params    []string     // route parameter names extracted from Path (e.g., ["name"])
	WindowIdx int          // index into CodegenCtx.Windows()
	Actions   []HTTPAction // server-state form actions (POST handlers)

	// Render is a static HTML skeleton interleaved with IR-expr holes, which
	// the target language fills against the route's State. nil when the route
	// has no server-rendered content.
	Render *RouteRender
	// StateVars are the component state fields surfaced to the server State
	// struct.
	StateVars []StateVar
}

// HTTPAction describes a form-based server action triggered by an event handler
// whose mutation crosses the target-language boundary (e.g. invokes a go://
// function when compiling with --lang go).
type HTTPAction struct {
	Name string // action identifier (e.g., "action0")
	// Mutations is the full type-checked handler body, visual/DOM-patch
	// statements included.
	//
	// Deprecated: use LogicalMutations, which excludes those.
	Mutations []ir.Stmt
	// LogicalMutations is the handler's state-mutation IR with visual/DOM-patch
	// statements removed.
	LogicalMutations []ir.Stmt
}

// StateVar is a component state field surfaced to the server State struct.
type StateVar struct {
	Name string
	Type *ir.Type
}

// RouteHoleKind classifies a dynamic slot in a server-rendered page.
type RouteHoleKind int

const (
	HoleText RouteHoleKind = iota // interpolate Expr (string-coerced)
	HoleAttr                      // attribute value = Expr
	HoleIf                        // reactive if: Cond + Then/Else skeletons
	HoleFor                       // reactive for: Iter + element skeleton
)

// RouteHole is a dynamic insertion point in a route's HTML skeleton.
type RouteHole struct {
	Kind RouteHoleKind
	Expr ir.Expr      // text/attr/if-cond/for-iter expression (language-agnostic)
	Attr string       // attribute name (HoleAttr)
	Key  string       // loop var (HoleFor)
	Then *RouteRender // HoleIf/HoleFor nested skeleton
	Else *RouteRender // HoleIf else skeleton
}

// RouteRender is a static HTML skeleton interleaved with holes. Chunks[i] is
// emitted, then Holes[i] (if present), alternating. Invariant:
// len(Chunks) == len(Holes)+1.
type RouteRender struct {
	Chunks []string
	Holes  []RouteHole
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

// MutationModelEmitter is optionally implemented by platforms that emit a
// static tree once, then generate targeted updater functions to patch it.
type MutationModelEmitter interface {
	BuildMutationModel(req *Request, analysis *CommonAnalysis) (*MutationModel, error)
	EmitFromMutation(m *MutationModel, req *Request, sink Sink) error
}

// RenderModelEmitter is optionally implemented by platforms that re-render the
// full view from state on every change, letting the framework diff.
type RenderModelEmitter interface {
	BuildRenderModel(req *Request, analysis *CommonAnalysis) (*RenderModel, error)
	EmitFromRender(m *RenderModel, req *Request, sink Sink) error
}

// MutationCompilerFactory keeps the singleton Generator stateless: each call
// returns a fresh compilation owning the per-request build state across
// BuildMutationModel and EmitFromMutation.
type MutationCompilerFactory interface {
	NewMutationCompiler() MutationModelEmitter
}

// RenderCompilerFactory is the render-loop counterpart to
// MutationCompilerFactory.
type RenderCompilerFactory interface {
	NewRenderCompiler() RenderModelEmitter
}

// OutputFile represents a single generated file. WriteTo is lazy, so template
// execution is deferred to write time.
type OutputFile struct {
	Name    string // relative path, e.g. "model.go"
	WriteTo func(w io.Writer) (int64, error)
}

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
