package fyne

import (
	_ "embed"
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/ir"
)

//go:embed preview.css
var previewCSS string

func init() {
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for Fyne.
type Generator struct{}

func (g *Generator) PlatformIdentifier() string { return "fyne" }
func (g *Generator) Description() string {
	return "Cross-platform desktop GUI, written in Go using Fyne."
}

// SupportedLangs includes "none", which does not mean "generate Go without a
// language". It means the program is not translated at all: the interpreter
// runs the IR and drives a worker that owns the toolkit. Generate rejects it,
// because there is nothing to generate -- see runInterpreted in cmd/sngl.
func (g *Generator) SupportedLangs() []string { return []string{"go", "none"} }
func (g *Generator) PreviewCSS() string       { return previewCSS }
func (g *Generator) Capabilities(lang codegen.LangTranslator) lower.Features {
	f := lang.Capabilities()
	// NoReactivity: lowering injects explicit `nX.<prop> = <expr>` Assigns
	// after every mutation of a tracked Var.
	f.Reactivity = false
	// NoDeclarative: lowering flattens the entire visual tree into
	// create/append/attachHandler intrinsic-call sequences. fyne consumes
	// the flat output via WalkLowered + fyneTranslator.
	f.Declarative = false
	f.InlineComponents = false
	f.StructSpread = false
	f.StructComponents = true
	f.StdlibContextParam = true
	// Canvas2D: fyne renders shape subtrees to a software raster (gg) drawn
	// into a *canvas.Image. ReactiveCanvas re-rasterises + Refreshes when a
	// state var read by a draw func mutates.
	f.Canvas = true
	f.ReactiveCanvas = true
	// fyne.Do queues onto the goroutine running the driver, which is the one
	// that owns every widget -- so a blocking call can be moved off it and its
	// answer written back. See async.go for the emitter.
	f.AsyncPost = true
	return f
}

// Generate writes fyne source files directly into sink. This is the
// sink-based path platforms migrate to during the codegen unification.
func (g *Generator) Generate(req *codegen.Request, sink codegen.Sink) error {
	// "none" is not a language this platform emits in -- it says the program is
	// not translated at all, and the interpreter runs it. There is nothing to
	// generate, and quietly emitting Go under that flag would produce a build
	// nobody asked for.
	if req.Lang != nil && req.Lang.LanguageIdentifier() == "none" {
		return fmt.Errorf("fyne: --lang none is interpreted, not generated; use `sngl run --platform fyne --lang none`")
	}
	// Agent-mode test build: suppress the user's main() loop and force
	// the package name to "main" so the agent's func main() compiles
	// alongside it. The launcher's `go build .` step expects a main
	// package on disk.
	agentMode := codegen.OptionString(req.Options, "testMode") == "agent"
	if agentMode {
		if req.Options == nil {
			req.Options = &ir.StructLit{}
		}
		codegen.SetOptionField(req.Options, "main", false)
		codegen.SetOptionField(req.Options, "package", "main")
	}

	// fyne has no errorBoundary emitter. Left to travel on, the boundary
	// reaches the Go renderer, which panics on a statement kind no platform
	// was meant to hand it.
	if err := codegen.FirstUnimplementedNode(req.Pkg, "fyne", "errorBoundary"); err != nil {
		return err
	}

	c := &compilation{}
	m, err := c.BuildMutationModel(req, codegen.AnalyzeCommon(req.Pkg))
	if err != nil {
		return err
	}
	if err := c.EmitFromMutation(m, req, sink); err != nil {
		return err
	}
	if golang.PackageUsesI18n(req.Pkg) {
		if err := golang.EmitI18nManifestEmbed(sink, c.cfg.Package, req.ProjectFS, ""); err != nil {
			return fmt.Errorf("fyne: i18n manifest embed: %w", err)
		}
	}

	if codegen.OptionBool(req.Options, "test") {
		testFns, suffixes, methodFields := codegen.CollectTestFuncs(req.Pkg)
		if len(testFns) > 0 {
			if agentMode {
				src := golang.LowerTestFile(c.cfg.Package, req.Pkg, testFns, suffixes, methodFields, golang.TestEmitAgent)
				if err := writeRawFile(sink, "testagent_main.go", []byte(src)); err != nil {
					return err
				}
				mainSrc := []byte("package " + c.cfg.Package + "\n\nimport \"git.duckfam.us/jonathan/sngl/pkg/go/testagent\"\n\nfunc main() { testagent.Main() }\n")
				if err := writeRawFile(sink, "agent_main.go", mainSrc); err != nil {
					return err
				}
				snapshotSrc := []byte(`package ` + c.cfg.Package + `

import (
	"bytes"
	"image/png"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"git.duckfam.us/jonathan/sngl/pkg/go/testagent"
)

var currentModel *Model

func setCurrentTestModel(m *Model) { currentModel = m }
func currentTestModel() *Model     { return currentModel }

// New() only zeroes the state; the widget fields an Updater writes through are
// assigned by BuildUI, so a test that never renders would nil-deref on the
// first state change. The test app has to exist before any widget does.
func newTestComponent() *Model {
	test.NewApp()
	m := New()
	m.BuildUI()
	return m
}

func snapshotBytes(m *Model) (string, []byte, error) {
	win := test.NewWindow(m.BuildUI())
	defer win.Close()
	win.Resize(fyne.NewSize(800, 600))
	img := win.Canvas().Capture()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", nil, err
	}
	return "image/png", buf.Bytes(), nil
}

func init() {
	testagent.RegisterSnapshot(func() (string, []byte, error) {
		return snapshotBytes(currentTestModel())
	})
}
`)
				if err := writeRawFile(sink, "snapshot.go", snapshotSrc); err != nil {
					return err
				}
			} else {
				src := golang.LowerTestFile(c.cfg.Package, req.Pkg, testFns, suffixes, methodFields, golang.TestEmitNative)
				if err := writeRawFile(sink, "model_test.go", []byte(src)); err != nil {
					return err
				}
				// Its own file rather than an appendix to model_test.go: the
				// helper needs an import, and that file's import block is
				// written by LowerTestFile.
				helper := []byte("package " + c.cfg.Package + "\n\nimport \"fyne.io/fyne/v2/test\"\n\nfunc newTestComponent() *Model {\n\ttest.NewApp()\n\tm := New()\n\tm.BuildUI()\n\treturn m\n}\n")
				if err := writeRawFile(sink, "testcomponent_test.go", helper); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// writeRawFile writes pre-formatted content directly to sink without
// running it through the language file emitter (which would re-attach
// source maps and headers we don't want on synthetic test/agent files).
func writeRawFile(sink codegen.Sink, name string, content []byte) error {
	wc, err := sink.Create(name)
	if err != nil {
		return err
	}
	if _, err := wc.Write(content); err != nil {
		wc.Close()
		return err
	}
	return wc.Close()
}

// compilation holds per-request build state flowing between
// BuildMutationModel and EmitFromMutation.
type compilation struct {
	ctx  *codegen.CodegenCtx
	info *irAnalysis
	cfg  Config
	lang codegen.LangTranslator
}

func (c *compilation) BuildMutationModel(req *codegen.Request, analysis *codegen.CommonAnalysis) (*codegen.MutationModel, error) {
	if req.Lang.LanguageIdentifier() != "go" {
		return nil, fmt.Errorf("fyne: unsupported lang %q", req.Lang.LanguageIdentifier())
	}
	if err := codegen.ApplyOptions(&c.cfg, req.Options); err != nil {
		return nil, fmt.Errorf("fyne: %w", err)
	}
	c.cfg = c.cfg.withDefaults()
	c.ctx = codegen.NewCodegenCtx(req, "fyne")
	c.lang = req.Lang
	c.info = analyzeIR(c.ctx)

	var stmts []ir.Stmt
	if main := c.ctx.RootDecl(); main != nil {
		stmts = main.Body
	}
	return c.ctx.BuildMutation(stmts), nil
}

func (c *compilation) EmitFromMutation(_ *codegen.MutationModel, req *codegen.Request, sink codegen.Sink) error {
	body, imports, aliases, cgoPreamble, err := emitIR(c.info, c.ctx, c.cfg, c.lang)
	if err != nil {
		return err
	}
	e := req.Lang.NewFileEmitter(sink, codegen.FileOptions{
		Name:        "model.go",
		Source:      req.Source,
		Platform:    "fyne",
		PackageName: c.cfg.Package,
		Maps:        req.Maps,
		CgoPreamble: cgoPreamble,
	})
	// The SNGL canvas runtime (pkg/go/canvas) and fyne's own canvas package
	// both default-alias to "canvas"; force the runtime under snglcanvas so the
	// draw-func selectors (snglcanvas.New / *snglcanvas.Context) resolve.
	type aliasImporter interface {
		RequireImportAs(path, alias string) string
	}
	// The emitter is its own context, so an alias forced during translation
	// does not reach it on its own. A widget package has to arrive under the
	// selector its Spec's Go spellings are written with -- both the
	// constructor call and the Model field's type string are emitted verbatim
	// -- and the alias the emitter would derive is the package name guessed
	// from the path, which for a /vN module is not even close.
	for _, p := range imports {
		if p == snglCanvasImportPath {
			if ai, ok := e.(aliasImporter); ok {
				ai.RequireImportAs(p, snglCanvasAlias)
				continue
			}
		}
		if alias := aliases[p]; alias != "" {
			if ai, ok := e.(aliasImporter); ok {
				ai.RequireImportAs(p, alias)
				continue
			}
		}
		e.RequireImport(p)
	}
	if _, err := e.Write([]byte(body)); err != nil {
		e.Close()
		return err
	}
	return e.Close()
}
