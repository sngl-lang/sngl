package codegen

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

// writeHostModule lays down a fake host module and points
// SNGL_HOST_GO_MOD at it.
func writeHostModule(t *testing.T, gomod, gosum string) string {
	t.Helper()
	root := t.TempDir()
	modPath := filepath.Join(root, "go.mod")
	if err := os.WriteFile(modPath, []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	if gosum != "" {
		if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte(gosum), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SNGL_HOST_GO_MOD", modPath)
	return root
}

func TestWriteGoMod_CopiesHostRequiresAndSum(t *testing.T) {
	writeHostModule(t, `module example.com/host

go 1.26.1

require (
	fyne.io/fyne/v2 v2.7.4
	golang.org/x/text v0.36.0 // indirect
)
`, "fyne.io/fyne/v2 v2.7.4 h1:deadbeef=\n")

	dir := t.TempDir()
	writeGoFile(t, dir, "model.go", `package main

import _ "fyne.io/fyne/v2"
`)

	needTidy, err := WriteGoMod(dir, "", "")
	if err != nil {
		t.Fatalf("WriteGoMod: %v", err)
	}
	if needTidy {
		t.Error("needTidy = true, want false: a host go.mod was found to align with")
	}

	got := readFile(t, filepath.Join(dir, "go.mod"))
	// Pinning to the host's versions is the point: without it `go mod
	// tidy` resolves latest and the generated program compiles against a
	// different fyne than the compiler was built with.
	if !strings.Contains(got, "fyne.io/fyne/v2 v2.7.4") {
		t.Errorf("go.mod lost the host's pinned fyne version:\n%s", got)
	}
	if !strings.Contains(got, "golang.org/x/text v0.36.0") {
		t.Errorf("go.mod dropped an indirect require:\n%s", got)
	}
	if sum := readFile(t, filepath.Join(dir, "go.sum")); !strings.Contains(sum, "fyne.io/fyne/v2 v2.7.4") {
		t.Errorf("go.sum was not copied:\n%s", sum)
	}
}

// A replace only takes effect for a module the main module requires, so
// the synthesized go.mod must carry a require alongside every replace.
func TestWriteGoMod_ReplaceGetsMatchingRequire(t *testing.T) {
	root := writeHostModule(t, "module "+snglModulePath+"\n\ngo 1.26.1\n", "")

	dir := t.TempDir()
	writeGoFile(t, dir, "agent.go", `package main

import _ "`+snglModulePath+`/pkg/go/testagent"
`)

	if _, err := WriteGoMod(dir, "", ""); err != nil {
		t.Fatalf("WriteGoMod: %v", err)
	}

	got := readFile(t, filepath.Join(dir, "go.mod"))
	if !strings.Contains(got, "replace "+snglModulePath+" => "+root) {
		t.Errorf("missing replace to host root:\n%s", got)
	}
	if !strings.Contains(got, snglModulePath+" v0.0.0") {
		t.Errorf("replace has no matching require:\n%s", got)
	}
}

func TestWriteGoMod_ExtraDirectivesOverrideHostReplace(t *testing.T) {
	writeHostModule(t, "module "+snglModulePath+"\n\ngo 1.26.1\n", "")

	dir := t.TempDir()
	writeGoFile(t, dir, "model.go", "package main\n")

	extra := "replace " + snglModulePath + " => /somewhere/else"
	if _, err := WriteGoMod(dir, "", extra); err != nil {
		t.Fatalf("WriteGoMod: %v", err)
	}

	got := readFile(t, filepath.Join(dir, "go.mod"))
	if strings.Count(got, "replace "+snglModulePath) != 1 {
		t.Errorf("want exactly one replace for %s, got:\n%s", snglModulePath, got)
	}
	if !strings.Contains(got, "=> /somewhere/else") {
		t.Errorf("caller directive did not win:\n%s", got)
	}
}

// noHostModule leaves WriteGoMod no host go.mod to find and hands it bi as
// the running binary's build info.
func noHostModule(t *testing.T, bi *debug.BuildInfo) {
	t.Helper()
	t.Setenv("SNGL_HOST_GO_MOD", "")
	t.Chdir(t.TempDir())
	prev := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) { return bi, bi != nil }
	t.Cleanup(func() { readBuildInfo = prev })
}

func TestWriteGoMod_NoHostModule(t *testing.T) {
	noHostModule(t, nil)
	dir := t.TempDir()

	needTidy, err := WriteGoMod(dir, "1.25", "")
	if err != nil {
		t.Fatalf("WriteGoMod: %v", err)
	}
	if !needTidy {
		t.Error("needTidy = false, want true when there is no host go.mod")
	}
	if got := readFile(t, filepath.Join(dir, "go.mod")); !strings.Contains(got, "go 1.25") {
		t.Errorf("caller go version ignored:\n%s", got)
	}
}

// With no host go.mod, an installed binary is the only record of which sngl
// and which dependencies the generated code was written against; left to
// tidy, every one of them resolved at its newest release.
func TestWriteGoMod_SeedsFromBuildInfo(t *testing.T) {
	noHostModule(t, &debug.BuildInfo{
		Main: debug.Module{Path: snglModulePath, Version: "v0.3.0", Sum: "h1:sngl="},
		Deps: []*debug.Module{
			{Path: "charm.land/bubbletea/v2", Version: "v2.0.2", Sum: "h1:tea="},
			{Path: "example.com/forked", Version: "v1.0.0", Replace: &debug.Module{Path: "example.com/fork", Version: "v1.0.1", Sum: "h1:fork="}},
			{Path: "example.com/local", Version: "v0.0.0", Replace: &debug.Module{Path: "../local"}},
		},
	})
	dir := t.TempDir()

	needTidy, err := WriteGoMod(dir, "", "")
	if err != nil {
		t.Fatalf("WriteGoMod: %v", err)
	}
	// Build info carries no go.mod hashes, so the graph is still incomplete.
	if !needTidy {
		t.Error("needTidy = false, want true")
	}

	mod := readFile(t, filepath.Join(dir, "go.mod"))
	for _, want := range []string{
		snglModulePath + " v0.3.0",
		"charm.land/bubbletea/v2 v2.0.2",
		"example.com/forked v1.0.0",
		"replace example.com/forked v1.0.0 => example.com/fork v1.0.1",
	} {
		if !strings.Contains(mod, want) {
			t.Errorf("go.mod missing %q:\n%s", want, mod)
		}
	}
	if strings.Contains(mod, "example.com/local") {
		t.Errorf("go.mod carries a filesystem replace relative to a go.mod it cannot see:\n%s", mod)
	}

	sum := readFile(t, filepath.Join(dir, "go.sum"))
	for _, want := range []string{
		snglModulePath + " v0.3.0 h1:sngl=",
		"charm.land/bubbletea/v2 v2.0.2 h1:tea=",
		"example.com/fork v1.0.1 h1:fork=",
	} {
		if !strings.Contains(sum, want) {
			t.Errorf("go.sum missing %q:\n%s", want, sum)
		}
	}
}

// A binary built with no VCS information, or from a modified tree, names no
// sngl the proxy can serve; its dependencies are still pinned.
func TestWriteGoMod_BuildInfoUnfetchableMain(t *testing.T) {
	for _, v := range []string{"(devel)", "v0.0.0-20261008012256-3dc47b4b0a96+dirty"} {
		t.Run(v, func(t *testing.T) {
			noHostModule(t, &debug.BuildInfo{
				Main: debug.Module{Path: snglModulePath, Version: v},
				Deps: []*debug.Module{{Path: "charm.land/bubbletea/v2", Version: "v2.0.2"}},
			})
			dir := t.TempDir()
			if _, err := WriteGoMod(dir, "", ""); err != nil {
				t.Fatalf("WriteGoMod: %v", err)
			}
			mod := readFile(t, filepath.Join(dir, "go.mod"))
			if strings.Contains(mod, snglModulePath) {
				t.Errorf("go.mod requires an unfetchable sngl:\n%s", mod)
			}
			if !strings.Contains(mod, "charm.land/bubbletea/v2 v2.0.2") {
				t.Errorf("go.mod lost the pinned dependency:\n%s", mod)
			}
		})
	}
}

func TestWriteGoMod_ExtraDirectivesOverrideBuildInfo(t *testing.T) {
	noHostModule(t, &debug.BuildInfo{Main: debug.Module{Path: snglModulePath, Version: "v0.3.0"}})
	dir := t.TempDir()

	extra := "replace " + snglModulePath + " => /somewhere/else"
	if _, err := WriteGoMod(dir, "", extra); err != nil {
		t.Fatalf("WriteGoMod: %v", err)
	}
	mod := readFile(t, filepath.Join(dir, "go.mod"))
	if strings.Contains(mod, "v0.3.0") || !strings.Contains(mod, "=> /somewhere/else") {
		t.Errorf("caller directive did not win:\n%s", mod)
	}
}

func writeGoFile(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A host go.mod covers the generated code's imports in the ordinary case, but
// it cannot cover a package whose own dependencies the host module never
// pulls in: the repo requires charm.land/bubbles/v2 and imports nothing that
// reaches harmonica, so a generated program importing bubbles/progress builds
// against a go.sum with no entry for it. The build says so, and that is the
// signal callers retry on.
func TestNeedsModuleTidy(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{
			name: "missing go.sum entry",
			output: "progress.go:14:2: missing go.sum entry for module providing " +
				"package github.com/charmbracelet/harmonica (imported by " +
				"charm.land/bubbles/v2/progress); to add:\n\tgo get charm.land/bubbles/v2/progress@v2.1.0",
			want: true,
		},
		{
			name:   "module provides no package",
			output: `model.go:7:2: no required module provides package example.com/x; to add it:`,
			want:   true,
		},
		{
			name:   "go.mod needs updating",
			output: "updates to go.mod needed; to update it:\n\tgo mod tidy",
			want:   true,
		},
		{
			name:   "a compile error is not a module problem",
			output: "./model.go:12:5: undefined: notAThing",
			want:   false,
		},
		{
			name:   "a missing native library is not a module problem",
			output: "# pkg-config --cflags gtk4\nPackage gtk4 was not found in the pkg-config search path.",
			want:   false,
		},
		{
			name:   "success",
			output: "",
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NeedsModuleTidy(tt.output); got != tt.want {
				t.Errorf("NeedsModuleTidy(%q) = %v, want %v", tt.output, got, tt.want)
			}
		})
	}
}
