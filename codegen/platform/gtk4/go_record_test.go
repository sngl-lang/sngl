//go:build !js

package gtk4

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"hash"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var (
	update = flag.Bool("update", false, "rerun each go build or go test whose files moved, and rewrite its testdata/go_runs record")
	verify = flag.Bool("verify", false, "rerun every go build and go test, whatever its testdata/go_runs record says")
)

const modulePath = "duckfam.us/sngl"

var moduleRoot = filepath.Join("..", "..", "..")

var goRunsPerTest = map[string]int{}

// runGo runs `go args...` over files in a fresh directory under this package,
// so the checkout's runtime packages resolve -- unless a record says these
// files, run this way, already succeeded, in which case nothing is written or
// run and the answer is ("", true).
//
// The digest covers the files, the arguments and the source of every package
// of this module the files import, transitively, so an edit to gtk4rt asks for
// a run as an edit to the generated program does. A go.mod is left out: it
// names the checkout's absolute path. A toolchain or GTK upgrade moves none of
// it, which is what -verify is for.
//
// A record is testdata/go_runs/<test>, with .<n> after the first run a test
// makes. Only a run that succeeded is recorded.
func runGo(t *testing.T, prefix string, files map[string]string, display bool, args ...string) (string, bool) {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "_")
	goRunsPerTest[name]++
	if n := goRunsPerTest[name]; n > 1 {
		name += "." + strconv.Itoa(n)
	}
	record := filepath.Join("testdata", "go_runs", name)
	want := goRunDigest(t, files, args) + "\n"

	have, _ := os.ReadFile(record)
	matched := string(have) == want
	switch {
	case matched && !*verify:
		return "", true
	case !*update && !*verify && have == nil:
		t.Fatalf("%s: no record of this go %s; rerun with -update on a host with GTK 4", record, args[0])
	case !*update && !*verify:
		t.Fatalf("%s: the files changed since go %s ran over them; rerun with -update", record, args[0])
	}
	if display && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Fatalf("%s: cannot record: go %s needs an X11/Wayland display", record, args[0])
	}
	out, ok := execGo(t, prefix, files, args...)
	if ok && *update {
		if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(record, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return out, ok
}

// execGo runs `go args...` over files in a fresh directory under this package,
// recording nothing.
func execGo(t *testing.T, prefix string, files map[string]string, args ...string) (string, bool) {
	t.Helper()
	dir, err := os.MkdirTemp(".", "_"+prefix)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// goFiles is the Go source among generated files.
func goFiles(files map[string]string) map[string]string {
	out := map[string]string{}
	for name, src := range files {
		if filepath.Ext(name) == ".go" {
			out[name] = src
		}
	}
	return out
}

func goRunDigest(t *testing.T, files map[string]string, args []string) string {
	t.Helper()
	h := sha256.New()
	writeField := func(s string) { fmt.Fprintf(h, "%d\x00%s", len(s), s) }
	for _, a := range args {
		writeField(a)
	}
	var names []string
	for name := range files {
		if name != "go.mod" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	imports := map[string]bool{}
	for _, name := range names {
		writeField(name)
		writeField(files[name])
		if filepath.Ext(name) == ".go" {
			addModuleImports(t, name, files[name], imports)
		}
	}
	hashed := map[string]bool{}
	for len(imports) > 0 {
		var next []string
		for p := range imports {
			if !hashed[p] {
				next = append(next, p)
			}
		}
		clear(imports)
		slices.Sort(next)
		for _, p := range next {
			hashed[p] = true
			hashModulePackage(t, h, p, imports)
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)[:16])
}

// hashModulePackage hashes the source a build of the module package p reads,
// and adds the module packages it imports.
func hashModulePackage(t *testing.T, h hash.Hash, p string, imports map[string]bool) {
	t.Helper()
	dir := filepath.Join(moduleRoot, filepath.FromSlash(strings.TrimPrefix(p, modulePath)))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("hashing %s: %v", p, err)
	}
	fmt.Fprintf(h, "%d\x00%s", len(p), p)
	for _, e := range entries {
		name := e.Name()
		switch filepath.Ext(name) {
		case ".go", ".c", ".h":
		default:
			continue
		}
		if e.IsDir() || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(h, "%d\x00%s%d\x00", len(name), name, len(src))
		h.Write(src)
		if filepath.Ext(name) == ".go" {
			addModuleImports(t, name, string(src), imports)
		}
	}
}

func addModuleImports(t *testing.T, name, src string, imports map[string]bool) {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
	if err != nil {
		return
	}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p == modulePath || strings.HasPrefix(p, modulePath+"/") {
			imports[p] = true
		}
	}
}
