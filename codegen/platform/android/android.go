package android

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/kotlin"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

//go:embed preview.css
var previewCSS string

//go:embed android.sngl
var pkgSource string

var pkgDocs []*ast.Document

func init() {
	doc, err := parser.Parse("android.sngl", []byte(pkgSource))
	if err != nil {
		panic(fmt.Errorf("platform android init: parsing android.sngl: %w", err))
	}
	pkgDocs = []*ast.Document{doc}
	codegen.RegisterPlatform(&Generator{})
}

// writeAndroidFile writes content to a named file in sink. Plain bytes;
// no source-file header. Used for manifests, go.mod, icon resources, etc.
func writeAndroidFile(sink codegen.Sink, name string, content []byte) error {
	w, err := sink.Create(name)
	if err != nil {
		return err
	}
	if _, err := w.Write(content); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

// writeAndroidSourceFile routes through the language's FileEmitter so the
// file gets a generated-by header, gofmt/source-map handling, and (when
// enabled) source-map sidecar emission.
func writeAndroidSourceFile(sink codegen.Sink, name string, lang codegen.LangTranslator, opts codegen.FileOptions, content []byte) error {
	opts.Name = name
	e := lang.NewFileEmitter(sink, opts)
	if _, err := e.Write(content); err != nil {
		e.Close()
		return err
	}
	return e.Close()
}

// writeOutputFile writes an OutputFile's content into sink using its name.
// If WriteTo returns ErrSkip, the file is silently omitted.
func writeOutputFile(sink codegen.Sink, f *codegen.OutputFile) error {
	return writeOutputFileAs(sink, f.Name, f)
}

// writeOutputFileAs writes an OutputFile's content into sink under an overridden name.
// If WriteTo returns ErrSkip, the file is silently omitted.
func writeOutputFileAs(sink codegen.Sink, name string, f *codegen.OutputFile) error {
	w, err := sink.Create(name)
	if err != nil {
		return err
	}
	_, werr := f.WriteTo(w)
	if errors.Is(werr, codegen.ErrSkip) {
		// Don't close — uncommitted writer is abandoned; file is skipped.
		return nil
	}
	if cerr := w.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// Generator implements codegen.PlatformGenerator for Android (Jetpack Compose).
type Generator struct{}

func (g *Generator) PlatformIdentifier() string { return "android" }
func (g *Generator) Description() string {
	return "Android app. Emits Jetpack Compose; can mix in Go via gomobile when --lang go is used."
}
func (g *Generator) SupportedLangs() []string            { return []string{"kotlin", "go"} }
func (g *Generator) PreviewCSS() string                  { return previewCSS }
func (g *Generator) Package() []*ast.Document            { return pkgDocs }
func (g *Generator) Resolve(identifier string) ir.Symbol { return nil }
func (g *Generator) Capabilities() lower.Caps {
	// NoInlineComponents: hoist user-component vars/funcs/timers into main
	// with per-instance renames. Android's RenderModel emits a Composable
	// per surviving component; after inlining only main + recursive
	// components remain, eliminating cross-component state plumbing.
	return lower.Caps{StructComponents: true, StdlibContextParam: true, NoInlineComponents: true}
}

// Generate writes android platform output directly into sink. This is
// the sink-based path platforms migrate to during the codegen unification.
func (g *Generator) Generate(req *codegen.Request, sink codegen.Sink) error {
	// Agent-mode test build: suppress the user-facing MainActivity/project
	// scaffold so the agent driver (AgentMain.kt → testagent.main()) owns
	// the entry point. The launcher (Task 5) relocates the emitted files
	// into a synthetic robolectric project before invoking gradle.
	if codegen.OptionString(req.Options, "testMode") == "agent" {
		if req.Options == nil {
			req.Options = &ir.StructLit{}
		}
		codegen.SetOptionField(req.Options, "main", false)
	}
	c := &compilation{}
	m, err := c.BuildRenderModel(req, codegen.AnalyzeCommon(req.Pkg))
	if err != nil {
		return err
	}
	return c.EmitFromRender(m, req, sink)
}

// Generate is the legacy entry point; delegates to Generate via a
// MemSink and converts captured files back to the Response shape.
// compilation holds per-request build state flowing between
// BuildRenderModel and EmitFromRender.
type compilation struct {
	ctx  *codegen.CodegenCtx
	cfg  Config
	lang string
}

func (c *compilation) BuildRenderModel(req *codegen.Request, analysis *codegen.CommonAnalysis) (*codegen.RenderModel, error) {
	c.lang = req.Lang.LanguageIdentifier()
	if c.lang != "kotlin" && c.lang != "go" {
		return nil, fmt.Errorf("android: unsupported lang %q", c.lang)
	}
	c.ctx = codegen.NewCodegenCtx(req, "android")
	cfg, err := (&Generator{}).configFromRequest(req)
	if err != nil {
		return nil, err
	}
	c.cfg = cfg
	if c.lang == "go" {
		c.cfg.GoLib = true
	}
	var stmts []ir.Stmt
	if main := c.ctx.MainComponent(); main != nil {
		stmts = main.Body
	}
	return c.ctx.BuildRender(stmts), nil
}

func (c *compilation) EmitFromRender(_ *codegen.RenderModel, req *codegen.Request, sink codegen.Sink) error {
	switch c.lang {
	case "kotlin":
		return c.emitKotlin(req, sink)
	case "go":
		return c.emitGo(req, sink)
	default:
		return fmt.Errorf("android: unsupported lang %q", c.lang)
	}
}

func (g *Generator) configFromRequest(req *codegen.Request) (Config, error) {
	var cfg Config
	if err := codegen.ApplyOptions(&cfg, req.Options); err != nil {
		return Config{}, fmt.Errorf("android: configFromRequest: %w", err)
	}
	return cfg.withDefaults(), nil
}

func (c *compilation) emitKotlin(req *codegen.Request, sink codegen.Sink) error {
	cfg := c.cfg
	ctx := c.ctx
	// In --opt test=true builds the Compose source needs a hoisted
	// MainScreenState class so generated tests can construct a state
	// instance, assert against its fields, and mutate from outside the
	// composition. CompileTestIR is the only difference between the
	// two emit paths.
	testMode := codegen.OptionBool(req.Options, "test")
	var (
		src []byte
		err error
	)
	if testMode {
		src, err = CompileTestIR(ctx, cfg)
	} else {
		src, err = CompileIR(ctx, cfg)
	}
	if err != nil {
		return err
	}

	ktOpts := codegen.FileOptions{Source: req.Source, Platform: "android", Maps: req.Maps}
	usesI18n := hasI18nCalls(req.Pkg)

	if !cfg.Main {
		if err := writeAndroidSourceFile(sink, "MainScreen.kt", req.Lang, ktOpts, src); err != nil {
			return err
		}
	} else if cfg.UseGradle() {
		pkgPath := pkgToPath(cfg.Package)
		if err := writeAndroidSourceFile(sink, "app/src/main/java/"+pkgPath+"/MainScreen.kt", req.Lang, ktOpts, src); err != nil {
			return err
		}
		for _, f := range scaffoldFiles(cfg, usesI18n) {
			if err := writeOutputFile(sink, f); err != nil {
				return err
			}
		}
		if iconRes, err := iconFiles(cfg); err == nil {
			for _, f := range iconRes {
				if err := writeOutputFileAs(sink, "app/src/main/"+f.Name, f); err != nil {
					return err
				}
			}
		}
	} else {
		if err := writeAndroidSourceFile(sink, "MainScreen.kt", req.Lang, ktOpts, src); err != nil {
			return err
		}
		for _, f := range directBuildFiles(cfg, usesI18n) {
			if err := writeOutputFile(sink, f); err != nil {
				return err
			}
		}
		if iconRes, err := iconFiles(cfg); err == nil {
			for _, f := range iconRes {
				if err := writeOutputFile(sink, f); err != nil {
					return err
				}
			}
		}
	}

	// When i18n is in use, inject the Kotlin runtime and manifest.
	if usesI18n && cfg.Main {
		if err := emitI18nRuntimeFile(sink); err != nil {
			return err
		}
		if err := emitI18nManifestFile(sink, cfg, req.ProjectFS); err != nil {
			return err
		}
	}

	// Test sources: native mode lands in the gradle test/androidTest
	// sourceset alongside MainScreen.kt; agent mode emits driver,
	// model accessor, and snapshot capture files for the launcher
	// to relocate (Task 5).
	if testMode {
		if err := emitKotlinTestSources(req, sink, cfg, ktOpts); err != nil {
			return err
		}
	}

	return nil
}

// emitKotlinTestSources emits the Kotlin test files for an android
// build. Behaviour splits on testMode:
//
//   - native (default): one JUnit-style MainScreenTest.kt under
//     app/src/test/kotlin (robolectric) or app/src/androidTest/kotlin
//     (device), suitable for `./gradlew test` / `connectedCheck`.
//   - agent: TestAgentRunner.kt + AgentMain.kt + TestModelAccessor.kt
//     + RobolectricSnapshot.kt (when testRunner=robolectric). The
//     launcher in Task 5 relocates these into a generated project.
func emitKotlinTestSources(req *codegen.Request, sink codegen.Sink, cfg Config, ktOpts codegen.FileOptions) error {
	testFns, suffixes, methodFields := codegen.CollectTestFuncs(req.Pkg)
	if len(testFns) == 0 {
		return nil
	}
	agent := codegen.OptionString(req.Options, "testMode") == "agent"
	mode := kotlin.TestEmitNative
	if agent {
		mode = kotlin.TestEmitAgent
	}
	src := kotlin.LowerTestFile(cfg.Package, testFns, suffixes, methodFields, mode)

	if !agent {
		// Native: target gradle test sourceset (robolectric) or
		// androidTest sourceset (device). Matches the layout the
		// android gradle plugin scans by default.
		sub := "test"
		if cfg.TestRunner == "device" {
			sub = "androidTest"
		}
		fname := fmt.Sprintf("app/src/%s/kotlin/%s/MainScreenTest.kt",
			sub, strings.ReplaceAll(cfg.Package, ".", "/"))
		return writeAndroidSourceFile(sink, fname, req.Lang, ktOpts, []byte(src))
	}

	// Agent mode: emit at the sink root for the launcher to
	// relocate into a synthetic robolectric/device project.
	if err := writeAndroidSourceFile(sink, "TestAgentRunner.kt", req.Lang, ktOpts, []byte(src)); err != nil {
		return err
	}
	agentMain := []byte("package " + cfg.Package + "\n\nfun main() { us.duckfam.git.jonathan.sngl.testagent.main() }\n")
	if err := writeAndroidSourceFile(sink, "AgentMain.kt", req.Lang, ktOpts, agentMain); err != nil {
		return err
	}
	accessor := []byte("package " + cfg.Package + `

private var __snglCurrentModel: MainScreenState? = null

fun setCurrentTestModel(m: MainScreenState) { __snglCurrentModel = m }
fun currentTestModel(): MainScreenState =
    __snglCurrentModel ?: error("currentTestModel: no model set yet")

fun newTestComponent(): MainScreenState = MainScreenState()
`)
	if err := writeAndroidSourceFile(sink, "TestModelAccessor.kt", req.Lang, ktOpts, accessor); err != nil {
		return err
	}
	if cfg.TestRunner == "robolectric" || cfg.TestRunner == "" {
		if err := writeAndroidSourceFile(sink, "RobolectricSnapshot.kt", req.Lang, ktOpts, robolectricSnapshotCaptureKotlin(cfg.Package)); err != nil {
			return err
		}
	}
	// Device snapshot capture is emitted in Task 8.
	return nil
}

// robolectricSnapshotCaptureKotlin returns the source of the
// Compose-test-rule snapshot capture object. The init block registers
// the capture function with the testagent Snapshots singleton under
// the "robolectric/" namePrefix; the testagent runtime invokes it
// when the agent's snapshot RPC fires. The ComposeContentTestRule
// pointer is populated by the @Test method before it triggers a
// snapshot — see TestAgentRunner.kt.
func robolectricSnapshotCaptureKotlin(pkg string) []byte {
	return []byte("package " + pkg + `

import android.graphics.Bitmap
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.ComposeContentTestRule
import androidx.compose.ui.test.onRoot
import org.robolectric.annotation.GraphicsMode
import us.duckfam.git.jonathan.sngl.testagent.Snapshots
import java.io.ByteArrayOutputStream

@GraphicsMode(GraphicsMode.Mode.NATIVE)
object RobolectricSnapshotCapture {
    var rule: ComposeContentTestRule? = null

    init {
        Snapshots.register("robolectric/") {
            val r = rule ?: error("snapshot: ComposeTestRule not set")
            val img = r.onRoot().captureToImage()
            val bmp = img.asAndroidBitmap()
            val out = ByteArrayOutputStream()
            bmp.compress(Bitmap.CompressFormat.PNG, 100, out)
            "image/png" to out.toByteArray()
        }
    }
}
`)
}
