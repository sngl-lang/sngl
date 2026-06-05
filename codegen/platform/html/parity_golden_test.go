package html

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/none"
)

// TestParityGolden compiles every codegen/platform/html/testdata/parity/*.sngl
// to html and compares against the committed .golden file. This is the
// byte-identical parity bar for the JS translator unification: the output must
// not change as emission moves from the legacy path to JsIRContext.
//
// Regenerate goldens with: SNGL_UPDATE_GOLDEN=1 go test ./codegen/platform/html/ -run TestParityGolden
func TestParityGolden(t *testing.T) {
	update := os.Getenv("SNGL_UPDATE_GOLDEN") == "1"
	srcs, err := filepath.Glob("testdata/parity/*.sngl")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(srcs)
	if len(srcs) == 0 {
		t.Fatal("no parity fixtures found")
	}
	for _, src := range srcs {
		t.Run(filepath.Base(src), func(t *testing.T) {
			got := generateHTML(t, src)
			golden := strings.TrimSuffix(src, ".sngl") + ".golden"
			if update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run with SNGL_UPDATE_GOLDEN=1 to create): %v", err)
			}
			if got != string(want) {
				t.Errorf("output differs from golden %s\n--- got ---\n%s", golden, got)
			}
		})
	}
}
