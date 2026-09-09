package gtk4

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// effectModelSrc puts an effect on the Model, and effectInstanceSrc puts one
// inside a row the build cannot inline -- the two places a settle func is
// emitted from.
const effectModelSrc = `
import . "sngl:ui"
import app "sngl:app"

app.window {
    var (
        n = 0
        log list<string> = []
    )

    effect(
        on=n,
        @mount { log.push("mount") },
        @unmount { log.push("unmount") },
    )

    button #bump(text="+", @click { n += 1 })
    text #out(value=log.join(","))
}
`

const effectInstanceSrc = `
import . "sngl:ui"
import app "sngl:app"

component row(name = "") node {
    var (
        n = 0
        log list<string> = []
    )

    effect(
        on=n,
        @mount { log.push("mount " + name) },
        @unmount { log.push("unmount " + name) },
    )

    button(text=name, @click { n += 1 })
}

app.window {
    var names list<string> = ["a", "b"]
    for var nm = names {
        row(name=nm)
    }
}
`

// TestAnEffectCompiles is gtk4's first effect harness. It builds rather than
// runs, because constructing a widget needs a GTK display -- the same split as
// TestInstanceRecordIsPerRow, and enough to catch what was wrong: every
// synthesized func was handed a slot render's `parent` parameter, so the
// emitted `__effects0_settle()` call did not match its own definition.
func TestAnEffectCompiles(t *testing.T) {
	skipWithoutGIR(t)
	for name, src := range map[string]string{
		"onTheModel":   effectModelSrc,
		"inAnInstance": effectInstanceSrc,
	} {
		t.Run(name, func(t *testing.T) {
			buildGTK4Model(t, "gtk4-effect-", generateGTK4Model(t, src))
		})
	}
}

// buildGTK4Model compiles model as a package under the main module, so gtk4rt
// resolves through the project's go.mod.
func buildGTK4Model(t *testing.T, prefix, model string) {
	t.Helper()
	tmp, err := os.MkdirTemp(".", prefix)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "model.go"), []byte(model), 0o644); err != nil {
		t.Fatal(err)
	}
	// The emission is `package main` without a main(), which links only under
	// `sngl run`'s own scaffold.
	if err := os.WriteFile(filepath.Join(tmp, "entry.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = tmp
	combined, buildErr := cmd.CombinedOutput()
	if buildErr != nil {
		t.Errorf("emitted Go failed to compile: %v\n--- output ---\n%s\n--- model.go ---\n%s", buildErr, combined, model)
	}
}
