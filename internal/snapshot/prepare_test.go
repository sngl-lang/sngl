package snapshot

import (
	"testing"

	"duckfam.us/sngl/codegen"
	_ "duckfam.us/sngl/codegen/lang"
	_ "duckfam.us/sngl/codegen/platform"
	"duckfam.us/sngl/internal/checker"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/ir"
)

// A snapshot draws the program and runs none of its tests, so the program it
// lowers holds none, as a build's does: a test reaching into a component's
// state was lowered for win32 as a test the build emits, and placement found
// no var holding what it read.
func TestPrepareDropsTests(t *testing.T) {
	const src = `import ui "sngl:ui"
import test "sngl:test"

output {
    c {
        win32
    }
}

component counter ui.node {
    var n = 0
    ui.button(text="Press", @click { n += 1 })
}

ui.window(title="Snapshot") {
    counter()
}

func testCounts(t test.Test, c counter) {
    t.assert(c.n == 0)
}
`
	doc, err := parser.Parse("app.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms(), Languages: codegen.CollectLangs(), Targets: []ir.StaticTarget{{Platform: "win32", Language: "c"}}})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Msg)
		}
	}
	pkg = prepare(pkg, "win32", "c")
	for _, fn := range pkg.Funcs {
		if fn.IsTest {
			t.Errorf("the snapshot's program holds the test %s", fn.Name)
		}
	}
	feats, err := codegen.CapsFor("c", "win32")
	if err != nil {
		t.Fatal(err)
	}
	if err := lower.Lower(pkg, feats, lowerOptions("win32", "c")); err != nil {
		t.Fatalf("lower: %v", err)
	}
}
