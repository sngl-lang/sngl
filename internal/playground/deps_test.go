package playground

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// The playground's wasm must not link text/template or html/template. Either
// looks names up with reflect.Value.MethodByName, and a binary that can reach
// that call keeps every exported method of every type it holds, because the
// linker can no longer tell which go unused: with the Android scaffold
// written as a template, that was half the wasm, and the deploy outgrew
// GitHub's 100 MiB file limit.
func TestWasmLinksNoTemplatePackage(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "./cmd")
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	deps := strings.Fields(string(out))
	for _, pkg := range []string{"text/template", "html/template"} {
		if slices.Contains(deps, pkg) {
			t.Errorf("the playground's wasm depends on %s", pkg)
		}
	}
}
