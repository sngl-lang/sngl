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
func (g *Generator) Capabilities(lang codegen.LangTranslator) lower.Features {
	f := lang.Capabilities()
	// NoInlineComponents: hoist user-component vars/funcs/timers into main
	// with per-instance renames. Android's RenderModel emits a Composable
	// per surviving component; after inlining only main + recursive
	// components remain, eliminating cross-component state plumbing.
	f.InlineComponents = false
	f.StructSpread = false
	f.StructComponents = true
	f.StdlibContextParam = true
	// Canvas2D: passCanvas extracts the canvas+shapes subtree into a
	// synthesized _canvasDrawN(ctx) func of canvas intrinsics, which we
	// translate inline into a Compose Canvas {} DrawScope lambda.
	f.Canvas = true
	// ReactiveCanvas stays FALSE: the draw lambda reads Compose state vars
	// directly (radius, computed styles), so Compose recomposes and
	// redraws the Canvas automatically when that state changes — no
	// explicit redraw call (CanvasRedrawStmt) is needed. Leaving
	// passCanvasReactivity off avoids injecting redraws that have no
	// Compose-native target.
	f.ReactiveCanvas = false
	return f
}

// Generate writes android platform output directly into sink. This is
// the sink-based path platforms migrate to during the codegen unification.
func (g *Generator) Generate(req *codegen.Request, sink codegen.Sink) error {
	// Agent-mode test build: both robolectric and device paths emit the
	// full Android Gradle Plugin scaffold (MainActivity, manifest,
	// app/build.gradle.kts, gradlew). The difference is the entry point:
	//   device: MainActivity.onCreate calls TestAgentBootstrap.start(this)
	//     when SNGL_AGENT_PORT is present on the launching intent. The
	//     launcher installs the APK and dials in over adb forward.
	//   robolectric: a JUnit @Test class (MainScreenAgentTest.kt) sits
	//     under app/src/test/kotlin and runs under :app:testDebugUnitTest.
	//     The JUnit body dials back to a driver-side listener using
	//     System.getProperty("sngl.agent.port").
	// Both paths reuse the same AGP build (compose deps resolve cleanly).
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
	agentMode := codegen.OptionString(req.Options, "testMode") == "agent"
	deviceAgent := testMode && agentMode && cfg.TestRunner == "device"
	robolectricAgent := testMode && agentMode && cfg.TestRunner == "robolectric"

	// Agent-mode (both robolectric and device) needs the full AGP
	// scaffold so compose dependencies resolve cleanly. Robolectric
	// runs :app:testDebugUnitTest under JVM; device runs
	// :app:assembleDebug + adb. Force gradle-scaffold on regardless
	// of cfg.Main so the launcher has the project layout it expects.
	effectiveMain := cfg.Main
	if deviceAgent || robolectricAgent {
		effectiveMain = true
	}

	if !effectiveMain {
		if err := writeAndroidSourceFile(sink, "MainScreen.kt", req.Lang, ktOpts, src); err != nil {
			return err
		}
	} else if cfg.UseGradle() {
		pkgPath := pkgToPath(cfg.Package)
		if err := writeAndroidSourceFile(sink, "app/src/main/java/"+pkgPath+"/MainScreen.kt", req.Lang, ktOpts, src); err != nil {
			return err
		}
		testAgentInc := ""
		if deviceAgent || robolectricAgent {
			p, err := findTestAgentPath()
			if err != nil {
				return fmt.Errorf("android: locate pkg/kotlin/testagent: %w", err)
			}
			testAgentInc = p
		}
		// templateTestMode flips the gradle template's TestMode flag,
		// which adds junit+robolectric+compose-ui-test deps and a
		// testOptions block. Required for both agent-robolectric and
		// native-robolectric paths so :app:testDebugUnitTest can
		// compile + run the generated JUnit class. Device path skips
		// this since instrumented tests live in androidTest sourceset
		// with a different dep set.
		templateTestMode := robolectricAgent || (testMode && cfg.TestRunner != "device")
		for _, f := range scaffoldFiles(cfg, usesI18n, deviceAgent, testAgentInc, templateTestMode) {
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
		for _, f := range directBuildFiles(cfg, usesI18n, deviceAgent) {
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
		if err := emitKotlinTestSources(req, sink, cfg, ktOpts, effectiveMain); err != nil {
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
//   - RobolectricSnapshot.kt (when testRunner=robolectric). The
//     launcher in Task 5 relocates these into a generated project.
func emitKotlinTestSources(req *codegen.Request, sink codegen.Sink, cfg Config, ktOpts codegen.FileOptions, gradleScaffold bool) error {
	testFns, suffixes, methodFields := codegen.CollectTestFuncs(req.Pkg)
	if len(testFns) == 0 {
		return nil
	}
	agent := codegen.OptionString(req.Options, "testMode") == "agent"
	mode := kotlin.TestEmitNative
	if agent {
		mode = kotlin.TestEmitAgent
	}
	src := kotlin.LowerTestFile(cfg.Package, testFns, suffixes, methodFields, mode, cfg.TestRunner)

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

	// Agent mode: when running under the gradle scaffold (device path),
	// agent files belong inside the app's main sourceset so AGP compiles
	// them. Without the scaffold (robolectric path) the launcher
	// relocates them into a synthetic project.
	prefix := ""
	if gradleScaffold {
		prefix = "app/src/main/java/" + pkgToPath(cfg.Package) + "/"
	}
	// TestAgentRunner.kt references androidx.compose.ui.test types
	// (ComposeContentTestRule + onNodeWithTag/runOnUiThread/etc.),
	// which only live on the testImplementation classpath. Under the
	// robolectric path that means it has to live in src/test/, not
	// src/main/, or :app:compileDebugKotlin can't resolve those refs.
	// Device path keeps it in main because instrumented tests pull
	// the same deps into the runtime classpath via androidTest.
	runnerPath := cfg.TestRunner
	if runnerPath == "" {
		runnerPath = "robolectric"
	}
	runnerPrefix := prefix
	if gradleScaffold && runnerPath == "robolectric" {
		runnerPrefix = "app/src/test/kotlin/" + pkgToPath(cfg.Package) + "/"
	}
	if err := writeAndroidSourceFile(sink, runnerPrefix+"TestAgentRunner.kt", req.Lang, ktOpts, []byte(src)); err != nil {
		return err
	}
	accessor := []byte("package " + cfg.Package + `

private var __snglCurrentModel: MainScreenState? = null

fun setCurrentTestModel(m: MainScreenState) { __snglCurrentModel = m }
fun currentTestModel(): MainScreenState =
    __snglCurrentModel ?: error("currentTestModel: no model set yet")

fun newTestComponent(): MainScreenState = MainScreenState()
`)
	if err := writeAndroidSourceFile(sink, prefix+"TestModelAccessor.kt", req.Lang, ktOpts, accessor); err != nil {
		return err
	}
	runner := cfg.TestRunner
	if runner == "" {
		runner = "robolectric"
	}
	switch runner {
	case "robolectric":
		// Robolectric path runs under AGP's :app:testDebugUnitTest task.
		// Emit a JUnit @Test class that dials back to the driver-side
		// listener and serves the RPC loop. Lands under app/src/test/
		// kotlin/<pkg>/ regardless of where the rest of the sources go
		// (AGP scans only that sourceset for unit tests).
		testPrefix := "app/src/test/kotlin/" + pkgToPath(cfg.Package) + "/"
		if err := writeAndroidSourceFile(sink, testPrefix+"MainScreenAgentTest.kt", req.Lang, ktOpts, robolectricAgentTestKotlin(cfg.Package)); err != nil {
			return err
		}
	case "device":
		// No AgentMain.kt — MainActivity.onCreate is the entry point;
		// scaffold patches it to call TestAgentBootstrap.start(this).
		if err := writeAndroidSourceFile(sink, prefix+"TestAgentBootstrap.kt", req.Lang, ktOpts, deviceAgentBootstrapKotlin(cfg.Package)); err != nil {
			return err
		}
		if err := writeAndroidSourceFile(sink, prefix+"DeviceSnapshot.kt", req.Lang, ktOpts, deviceSnapshotCaptureKotlin(cfg.Package)); err != nil {
			return err
		}
	}
	return nil
}

// robolectricAgentTestKotlin returns the source of MainScreenAgentTest,
// a single @RunWith(RobolectricTestRunner)-annotated JUnit class with
// one @Test method. The method:
//  1. Touches SnglTestRegistration so its init {} block fires, wiring
//     every generated test function into the testagent Registry.
//  2. Registers a Snapshots capture closure that rasterises the
//     ComposeContentTestRule's root into a PNG (used by t.snapshot()).
//  3. Reads -Dsngl.agent.port=<N> and dials back to the driver-side
//     listener, then runs the same RPC loop as the device path.
//
// Runs under AGP's :app:testDebugUnitTest, which Robolectric instruments
// with a fake Android runtime — so android.graphics.Bitmap, compose UI
// test infra, etc. all resolve to real implementations on the JVM.
func robolectricAgentTestKotlin(pkg string) []byte {
	return []byte("package " + pkg + `

import android.graphics.Bitmap
import android.os.Looper
import androidx.activity.ComponentActivity
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import us.duckfam.git.jonathan.sngl.testagent.Snapshots
import us.duckfam.git.jonathan.sngl.testagent.TestAgent
import java.io.ByteArrayOutputStream

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [33])
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class MainScreenAgentTest {
    @get:Rule val composeRule = createAndroidComposeRule<ComponentActivity>()

    private var __sngl_content_set = false

    @Test fun runAgent() {
        // Publish the @Rule to the package-scope composeTestRule
        // lateinit var that TestAgentRunner.kt's package-level test
        // functions reference. Must happen before SnglTestRegistration
        // (or any registered test body) executes.
        composeTestRule = composeRule

        // Force-load SnglTestRegistration so its init {} block wires
        // every test function into the testagent Registry before the
        // driver issues its first "list"/"run".
        SnglTestRegistration.ensure()

        Snapshots.register("robolectric/") {
            // Render MainScreen against the test's current model on
            // demand. The test body calls newTestComponent() +
            // setCurrentTestModel(c) before any snapshot, so the model
            // is guaranteed populated by the time we reach here.
            // setContent throws if called more than once on the same
            // rule, so guard via a flag — multiple t.snapshot() calls
            // in the same @Test reuse the existing composition.
            //
            // Connection between testagent and Compose UI:
            // TestAgent.connectAndDrive (used by the robolectric path)
            // keeps the dispatch loop on the JUnit thread, so this
            // closure runs synchronously on the main thread Robolectric
            // accepts. No marshaling needed.
            if (!__sngl_content_set) {
                composeRule.setContent { MainScreen(currentTestModel()) }
                __sngl_content_set = true
            }
            shadowOf(Looper.getMainLooper()).idle()
            composeRule.waitForIdle()

            // captureToImage uses PixelCopy or forceRedraw — neither works
            // reliably under Robolectric (no real surface). Fall back to
            // drawing the host Activity's decor view tree directly into
            // an offscreen bitmap via View.draw(Canvas). createAndroid-
            // ComposeRule<ComponentActivity> gives us a real Activity
            // backed view hierarchy on the JVM under Robolectric.
            val decor = composeRule.activity.window.decorView
            val w = decor.width.coerceAtLeast(1)
            val h = decor.height.coerceAtLeast(1)
            val bmp = Bitmap.createBitmap(w, h, Bitmap.Config.ARGB_8888)
            decor.draw(android.graphics.Canvas(bmp))
            val out = ByteArrayOutputStream()
            bmp.compress(Bitmap.CompressFormat.PNG, 100, out)
            "image/png" to out.toByteArray()
        }

        val port = System.getProperty("sngl.agent.port")?.toIntOrNull()
            ?: error("sngl.agent.port system property not set")
        TestAgent.connectAndDrive(port)
    }
}
`)
}

// deviceAgentBootstrapKotlin returns the source of the on-device
// testagent bootstrap. MainActivity.onCreate calls
// TestAgentBootstrap.start(this) when the launching intent carries a
// non-zero SNGL_AGENT_PORT extra; the bootstrap binds startTcp on that
// port in a background daemon thread and attaches the activity to
// DeviceSnapshotCapture so the snapshot RPC can rasterise its view.
func deviceAgentBootstrapKotlin(pkg string) []byte {
	return []byte("package " + pkg + `

import android.app.Activity
import kotlin.concurrent.thread
import us.duckfam.git.jonathan.sngl.testagent.TestAgent

object TestAgentBootstrap {
    fun start(activity: Activity) {
        val port = activity.intent?.getIntExtra("SNGL_AGENT_PORT", 0) ?: 0
        if (port <= 0) return
        DeviceSnapshotCapture.attach(activity)
        // Force test registration before we accept the first RPC call.
        // SnglTestRegistration's init {} block does the Registry.register
        // calls; ensure() is just a touch-point.
        SnglTestRegistration.ensure()
        thread(start = true, isDaemon = false, name = "sngl-testagent") {
            TestAgent.startTcp(port)
        }
    }
}
`)
}

// deviceSnapshotCaptureKotlin returns the source of the on-device
// snapshot capture object. The init block registers the capture
// function with the testagent Snapshots singleton under the "device/"
// namePrefix; the testagent runtime invokes it when the agent's
// snapshot RPC fires. The Activity pointer is populated by
// TestAgentBootstrap.start before the agent loop starts serving.
func deviceSnapshotCaptureKotlin(pkg string) []byte {
	return []byte("package " + pkg + `

import android.app.Activity
import android.graphics.Bitmap
import android.graphics.Canvas
import android.view.View
import us.duckfam.git.jonathan.sngl.testagent.Snapshots
import java.io.ByteArrayOutputStream
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

object DeviceSnapshotCapture {
    @Volatile private var activity: Activity? = null

    init {
        Snapshots.register("device/") {
            val a = activity ?: error("DeviceSnapshotCapture: no Activity attached")
            // Wait for first layout pass — without this the content view
            // reports 0x0 on a fresh activity and the snapshot is a 1x1
            // pixel. The agent thread isn't the UI thread, so we post to
            // the activity's main looper, draw, and signal via a latch.
            val ready = CountDownLatch(1)
            var captured: ByteArray = ByteArray(0)
            a.runOnUiThread {
                val v = a.findViewById<View>(android.R.id.content)
                if (v == null) { ready.countDown(); return@runOnUiThread }
                val capture = Runnable {
                    val w = v.width.coerceAtLeast(1)
                    val h = v.height.coerceAtLeast(1)
                    val bmp = Bitmap.createBitmap(w, h, Bitmap.Config.ARGB_8888)
                    v.draw(Canvas(bmp))
                    val out = ByteArrayOutputStream()
                    bmp.compress(Bitmap.CompressFormat.PNG, 100, out)
                    captured = out.toByteArray()
                    ready.countDown()
                }
                if (v.width > 0 && v.height > 0) {
                    capture.run()
                } else {
                    v.post(capture)
                }
            }
            ready.await(5, TimeUnit.SECONDS)
            "image/png" to captured
        }
    }

    fun attach(a: Activity) { activity = a }
}
`)
}
