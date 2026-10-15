package gtk4

import "testing"

// effectModelSrc puts an effect on the Model, and effectInstanceSrc puts one
// inside a row the build cannot inline -- the two places a settle func is
// emitted from.
const effectModelSrc = `
import . "sngl:ui"

window {
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

window {
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
	// The emission is `package main` without a main(), which links only under
	// `sngl run`'s own scaffold.
	files := map[string]string{"model.go": model, "entry.go": "package main\n\nfunc main() {}\n"}
	if out, ok := runGo(t, prefix, files, false, "build", "./..."); !ok {
		t.Errorf("emitted Go failed to compile:\n--- output ---\n%s\n--- model.go ---\n%s", out, model)
	}
}
