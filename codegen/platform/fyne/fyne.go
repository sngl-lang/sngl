package fyne

import (
	_ "embed"
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

//go:embed preview.css
var previewCSS string

//go:embed fyne.sngl
var pkgSource string

var pkgDocs []*ast.Document

func init() {
	doc, err := parser.Parse("fyne.sngl", []byte(pkgSource))
	if err != nil {
		panic(fmt.Errorf("platform fyne init: parsing fyne.sngl: %w", err))
	}
	pkgDocs = []*ast.Document{doc}
	codegen.RegisterPlatform(&Generator{})
}

// Generator implements codegen.PlatformGenerator for Fyne.
type Generator struct{}

func (g *Generator) PlatformIdentifier() string { return "fyne" }
func (g *Generator) Description() string {
	return "Cross-platform desktop GUI, written in Go using Fyne."
}
func (g *Generator) SupportedLangs() []string { return []string{"go"} }
func (g *Generator) PreviewCSS() string       { return previewCSS }
func (g *Generator) Package() []*ast.Document { return pkgDocs }
func (g *Generator) Resolve(identifier string) ir.Symbol {
	// Fyne accepts any tag name; codegen reads metadata from blueprint
	// .sngl bodies (Container/Label/Button/Entry/Check/Select/etc.).
	return &ir.Component{Name: identifier}
}
func (g *Generator) Capabilities(lang codegen.LangTranslator) lower.Features {
	f := lang.Capabilities()
	// NoReactivity: lowering injects explicit `nX.<prop> = <expr>` Assigns
	// after every mutation of a tracked Var.
	f.Reactivity = false
	// NoDeclarative: lowering flattens the entire visual tree into
	// create/append/attachHandler intrinsic-call sequences. fyne consumes
	// the flat output via WalkLowered + fyneTranslator.
	f.Declarative = false
	// NoStdlibWrappers: inline fyne.sngl wrapper components at lowering time.
	// fyne wrappers are pure blueprint-bearing NodeInsts; the translator
	// reads the same Constructor/bindings props after inlining.
	f.StdlibWrappers = false
	f.InlineComponents = false
	f.StructSpread = false
	f.StructComponents = true
	f.StdlibContextParam = true
	// Canvas2D: fyne renders shape subtrees to a software raster (gg) drawn
	// into a *canvas.Image. ReactiveCanvas re-rasterises + Refreshes when a
	// state var read by a draw func mutates.
	f.Canvas = true
	f.ReactiveCanvas = true
	return f
}

// Generate writes fyne source files directly into sink. This is the
// sink-based path platforms migrate to during the codegen unification.
func (g *Generator) Generate(req *codegen.Request, sink codegen.Sink) error {
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
				src := golang.LowerTestFile(c.cfg.Package, testFns, suffixes, methodFields, golang.TestEmitAgent)
				if err := writeRawFile(sink, "testagent_main.go", []byte(src)); err != nil {
					return err
				}
				// fyne's New() returns *Model; LowerTestFile generates code
				// against `newTestComponent()` and dereferences fields via
				// `c.<field>`, which Go handles transparently on a pointer.
				mainSrc := []byte("package " + c.cfg.Package + "\n\nimport \"git.duckfam.us/jonathan/sngl/pkg/go/testagent\"\n\nfunc newTestComponent() *Model { return New() }\n\nfunc main() { testagent.Main() }\n")
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
				src := golang.LowerTestFile(c.cfg.Package, testFns, suffixes, methodFields, golang.TestEmitNative)
				// newTestComponent helper: fyne's New() returns *Model.
				helper := []byte("\nfunc newTestComponent() *Model { return New() }\n")
				if err := writeRawFile(sink, "model_test.go", append([]byte(src), helper...)); err != nil {
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
	if main := c.ctx.MainComponent(); main != nil {
		stmts = main.Body
	}
	return c.ctx.BuildMutation(stmts), nil
}

func (c *compilation) EmitFromMutation(_ *codegen.MutationModel, req *codegen.Request, sink codegen.Sink) error {
	body, imports, cgoPreamble, err := emitIR(c.info, c.ctx, c.cfg, c.lang)
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
	for _, p := range imports {
		e.RequireImport(p)
	}
	if _, err := e.Write([]byte(body)); err != nil {
		e.Close()
		return err
	}
	return e.Close()
}
