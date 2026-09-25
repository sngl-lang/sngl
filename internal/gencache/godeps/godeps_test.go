//go:build !js

package godeps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.duckfam.us/jonathan/sngl/internal/gencache"
)

// module writes a two-package module: a imports b, and b embeds a directory.
// Every file is backdated past recentEdit, as a checked-out tree would be.
func module(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":            "module example.com/m\n\ngo 1.22\n",
		"a/a.go":            "package a\n\nimport \"example.com/m/b\"\n\nvar X = b.Y\n",
		"b/b.go":            "package b\n\nimport \"embed\"\n\n//go:embed data\nvar FS embed.FS\n\nvar Y = 1\n",
		"b/data/one.txt":    "one",
		"b/data/sub/two.md": "two",
	}
	old := time.Now().Add(-time.Hour)
	for name, src := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(path, old, old)
	}
	return dir
}

func produceFor(t *testing.T, store *gencache.Store, dir string) []byte {
	t.Helper()
	req, err := Request(dir, []string{"example.com/m/a"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := store.Get(req)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The closure records the files of every package it holds, the listings that
// decide which files those are, and the module files -- a go.sum that is not
// there included, since it would change the build by appearing.
func TestClosureRecordsWhatTheBuildReads(t *testing.T) {
	dir := module(t)
	data := string(produceFor(t, gencache.Open(""), dir))
	for _, want := range []string{
		`cache.file(path="` + filepath.Join(dir, "a", "a.go"),
		`cache.file(path="` + filepath.Join(dir, "b", "b.go"),
		`cache.file(path="` + filepath.Join(dir, "b", "data", "sub", "two.md"),
		`cache.godir(path="` + filepath.Join(dir, "b", "data", "sub") + `"`,
		`cache.file(path="` + filepath.Join(dir, "go.mod"),
		`cache.absent(path="` + filepath.Join(dir, "go.sum") + `"`,
		`cache.goenv(dir="` + dir + `", name="GOVERSION"`,
		`"example.com/m/b"`,
	} {
		if !strings.Contains(data, want) {
			t.Errorf("closure is missing %s\n%s", want, data)
		}
	}
	if strings.Contains(data, `name="embed"`) || strings.Contains(data, "/src/embed/") {
		t.Errorf("a GOROOT package was recorded file by file:\n%s", data)
	}
}

// Each way the build's input set can change makes the recorded closure stale.
func TestClosureGoesStale(t *testing.T) {
	for name, change := range map[string]func(dir string){
		"edit an imported package": func(dir string) {
			os.WriteFile(filepath.Join(dir, "b", "b.go"), []byte("package b\n\nvar Y = 2\n"), 0o644)
		},
		"add a file to a package": func(dir string) { os.WriteFile(filepath.Join(dir, "a", "more.go"), []byte("package a\n"), 0o644) },
		"add an embedded file":    func(dir string) { os.WriteFile(filepath.Join(dir, "b", "data", "sub", "three"), nil, 0o644) },
		"edit go.mod": func(dir string) {
			os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n\ngo 1.23\n"), 0o644)
		},
		"create go.sum": func(dir string) { os.WriteFile(filepath.Join(dir, "go.sum"), nil, 0o644) },
	} {
		t.Run(name, func(t *testing.T) {
			dir := module(t)
			data := produceFor(t, gencache.Open(""), dir)
			if why, err := gencache.Open(t.TempDir()).Stale(data); why != "" || err != nil {
				t.Fatalf("stale before any change: %q %v", why, err)
			}
			change(dir)
			if why, err := gencache.Open(t.TempDir()).Stale(data); why == "" && err == nil {
				t.Error("still current after the change")
			}
		})
	}
}

// What the go command ignores beside a package -- a test's `_scratch`
// package, an editor's dotfile -- leaves the closure current. The platform
// tests build their generated code in such a directory, and while any of them
// ran, every consteval closure over the codegen packages was stale.
func TestClosureIgnoresWhatGoIgnores(t *testing.T) {
	dir := module(t)
	data := produceFor(t, gencache.Open(""), dir)
	os.Mkdir(filepath.Join(dir, "a", "_scratch"), 0o755)
	os.WriteFile(filepath.Join(dir, "b", "data", ".swp"), nil, 0o644)
	if why, err := gencache.Open(t.TempDir()).Stale(data); why != "" || err != nil {
		t.Errorf("stale after adding names the go command ignores: %q %v", why, err)
	}
}

// An `all:` embed pattern embeds the names the go command otherwise ignores,
// so under one a new `_` file changes the build.
func TestAllEmbedCountsIgnoredNames(t *testing.T) {
	dir := module(t)
	src := filepath.Join(dir, "b", "b.go")
	os.WriteFile(src, []byte("package b\n\nimport \"embed\"\n\n//go:embed all:data\nvar FS embed.FS\n\nvar Y = 1\n"), 0o644)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(src, old, old)
	data := produceFor(t, gencache.Open(""), dir)
	os.WriteFile(filepath.Join(dir, "b", "data", "sub", "_hidden"), nil, 0o644)
	if why, err := gencache.Open(t.TempDir()).Stale(data); why == "" && err == nil {
		t.Error("still current after a file an all: pattern embeds appeared")
	}
}

// A file edited moments ago may have changed between `go list` reading it and
// the producer hashing it, so the answer is used and not kept.
func TestRecentEditIsNotStored(t *testing.T) {
	dir := module(t)
	os.WriteFile(filepath.Join(dir, "a", "a.go"), []byte("package a\n\nimport \"example.com/m/b\"\n\nvar X = b.Y + 1\n"), 0o644)
	store := t.TempDir()
	produceFor(t, gencache.Open(store), dir)
	var stored []string
	filepath.WalkDir(store, func(path string, d os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(path, ".sngl") {
			stored = append(stored, path)
		}
		return nil
	})
	if len(stored) != 0 {
		t.Errorf("a closure read moments after an edit was stored: %v", stored)
	}
}
