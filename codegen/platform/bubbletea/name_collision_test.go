package bubbletea

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	_ "duckfam.us/sngl/codegen/lang"
)

// nameCollisionSrc is testdata/func_nested_name_taken.txtar's program: a func
// nested in a *top-level* func, whose hoisted `step__mark` is the name a struct
// beside it already holds.
const nameCollisionSrc = `
import . "sngl:ui"

struct step__mark {
    a int
}

func step(a int) int {
    func mark(k int) int {
        if k <= 0 {
            return 0
        }
        return mark(k - 1) + k
    }
    return mark(a)
}

component main node {
    var n int = 0
    var keep step__mark = step__mark{a=1}

    text(value="{n}{keep.a}")
    button(text="go", @click {
        n = step(n) + keep.a
    })
}

window {
    main
}
`

// TestGeneratedGoWithACollidingNestedFuncCompiles builds the emitted program.
//
// TestFixtures parses the generated Go and stops there, which is exactly what
// let this through: `type Step__mark` and `func Step__mark` in one file is a
// redeclaration, and a redeclaration parses. The golden shows the two names are
// distinct; only a compiler says they have to be.
func TestGeneratedGoWithACollidingNestedFuncCompiles(t *testing.T) {
	modelSrc := []byte(generateBubbleteaModel(t, nameCollisionSrc))

	// Under the main module, so charm.land/bubbletea resolves through the
	// project's own go.mod.
	tmp, err := os.MkdirTemp(".", "_bt-name-collision-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "model.go"), modelSrc, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "build", ".")
	cmd.Dir = tmp
	out, buildErr := cmd.CombinedOutput()
	if buildErr != nil {
		t.Errorf("the emitted program does not compile: %v\n--- output ---\n%s\n--- model.go ---\n%s",
			buildErr, out, modelSrc)
	}
}
