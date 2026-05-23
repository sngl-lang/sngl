package fyne

import (
	_ "embed"
	"fmt"
	"go/format"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
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
func (g *Generator) IsLanguageSupported(l ir.Language) bool { return l.LanguageIdentifier() == "go" }
func (g *Generator) Capabilities() lower.Caps {
	// NoReactivity: lowering injects explicit `nX.<prop> = <expr>`
	// Assigns after every mutation of a tracked Var.
	// NoDeclarative: lowering flattens the entire visual tree into
	// create/append/attachHandler intrinsic-call sequences. fyne
	// consumes the flat output via WalkLowered + fyneTranslator.
	// NoStdlibWrappers: inline fyne.sngl wrapper components at lowering
	// time. fyne wrappers are already pure blueprint-bearing NodeInsts,
	// so behavior is unchanged — the translator still reads the same
	// Constructor/bindings props after inlining.
	return lower.Caps{NoContext: true, NoReactivity: true, NoDeclarative: true, NoStdlibWrappers: true, NoInlineComponents: true}
}

// Generate writes fyne source files directly into sink. This is the
// sink-based path platforms migrate to during the codegen unification.
func (g *Generator) Generate(req *codegen.Request, sink codegen.Sink) error {
	c := &compilation{}
	m, err := c.BuildMutationModel(req, codegen.AnalyzeCommon(req.Pkg))
	if err != nil {
		return err
	}
	return c.EmitFromMutation(m, req, sink)
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
	src, err := emitIR(c.info, c.ctx, c.cfg, c.lang)
	if err != nil {
		return err
	}
	formatted, err := format.Source(src)
	if err != nil {
		return fmt.Errorf("generated code formatting error: %w\n%s", err, src)
	}
	if h := codegen.Header("fyne", req.Source, "// ", ""); h != "" {
		formatted = append([]byte(h), formatted...)
	}
	w, err := sink.Create("model.go")
	if err != nil {
		return err
	}
	if _, err := w.Write(formatted); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}
