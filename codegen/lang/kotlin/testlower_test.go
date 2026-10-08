package kotlin

import (
	"strings"
	"testing"

	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

func TestKotlinLowerTestFile_agentModeEmitsRegisterAll(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:test"
component box node {
    var count = 0
    text(value="x")
}

func testFoo(t Test, c box) {
    t.assert(c.count == 0)
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Msg)
		}
	}
	if pkg == nil {
		t.Fatal("nil pkg")
	}
	var fn *ir.Func
	for _, f := range pkg.Funcs {
		if f.IsTest {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatal("no test func in package")
	}
	out := LowerTestFile("us.duckfam.sngl.app", []*ir.Func{fn}, []string{"Foo"}, TestSurface{}, TestEmitAgent, "")
	if !strings.Contains(out, "import us.duckfam.git.jonathan.sngl.testagent.T") {
		t.Errorf("agent mode missing T import:\n%s", out)
	}
	if !strings.Contains(out, "import us.duckfam.git.jonathan.sngl.testagent.Registry") {
		t.Errorf("agent mode missing Registry import:\n%s", out)
	}
	if !strings.Contains(out, "fun testFoo(t: T)") {
		t.Errorf("agent func signature missing:\n%s", out)
	}
	if !strings.Contains(out, "Registry.register(\"Foo\", ::testFoo)") {
		t.Errorf("Registry.register call missing:\n%s", out)
	}
}

func TestKotlinLowerTestFile_nativeModeEmitsJUnitClass(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:test"
component box node {
    var count = 0
    text(value="x")
}

func testFoo(t Test, c box) {
    t.assert(c.count == 0)
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	var fn *ir.Func
	for _, f := range pkg.Funcs {
		if f.IsTest {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatal("no test func in package")
	}
	out := LowerTestFile("us.duckfam.sngl.app", []*ir.Func{fn}, []string{"Foo"}, TestSurface{}, TestEmitNative, "robolectric")
	if !strings.Contains(out, "import org.junit.Test") {
		t.Errorf("native mode missing JUnit import:\n%s", out)
	}
	if !strings.Contains(out, "@Test fun testFoo()") {
		t.Errorf("native @Test missing:\n%s", out)
	}
	if strings.Contains(out, "Registry.register") {
		t.Errorf("native mode should not call Registry.register:\n%s", out)
	}
	if !strings.Contains(out, "@RunWith(RobolectricTestRunner::class)") {
		t.Errorf("native robolectric mode missing RunWith annotation:\n%s", out)
	}
	if !strings.Contains(out, "createAndroidComposeRule<ComponentActivity>()") {
		t.Errorf("native mode missing compose rule:\n%s", out)
	}
	if !strings.Contains(out, "composeRule.setContent { MainScreen(c) }") {
		t.Errorf("native mode missing setContent wrap:\n%s", out)
	}
}

func TestKotlinLowerTestFile_nativeDeviceUsesAndroidJUnit4(t *testing.T) {
	src := `
import . "sngl:ui"
import . "sngl:test"
component box node {
    var count = 0
    text(value="x")
}

func testFoo(t Test, c box) {
    t.assert(c.count == 0)
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	var fn *ir.Func
	for _, f := range pkg.Funcs {
		if f.IsTest {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatal("no test func in package")
	}
	out := LowerTestFile("us.duckfam.sngl.app", []*ir.Func{fn}, []string{"Foo"}, TestSurface{}, TestEmitNative, "device")
	if !strings.Contains(out, "@RunWith(AndroidJUnit4::class)") {
		t.Errorf("device mode should use AndroidJUnit4:\n%s", out)
	}
	if strings.Contains(out, "RobolectricTestRunner") {
		t.Errorf("device mode should not reference RobolectricTestRunner:\n%s", out)
	}
	if !strings.Contains(out, "createAndroidComposeRule<ComponentActivity>()") {
		t.Errorf("device mode missing compose rule:\n%s", out)
	}
}
