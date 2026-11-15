package cgogen

import (
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	_ "duckfam.us/sngl/codegen/platform"
)

var (
	update = flag.Bool("update", false, "record each MinGW build of the wrapper package whose files moved")
	verify = flag.Bool("verify", false, "build the wrapper package with MinGW whatever its records say")
)

// root is this repository's checkout, which the test's package sits two
// directories below.
const root = "../.."

// TestCommitted holds each committed wrapper package, and the marks its
// platform's .sngl files carry, to what the tool writes now.
func TestCommitted(t *testing.T) {
	for _, p := range []struct{ name, goos string }{{"win32", "windows"}, {"gtk4", ""}} {
		t.Run(p.name, func(t *testing.T) { committed(t, p.name, p.goos) })
	}
}

func committed(t *testing.T, platform, goos string) {
	plan, err := Lib(root, "platform/"+platform, "", goos)
	if err != nil {
		t.Fatal(err)
	}
	rerun := "rerun `go generate ./lib`, which runs `sngl tool cgogen sngl:platform/" + platform + "`"
	for _, name := range slices.Sorted(maps.Keys(plan.Files)) {
		have, err := os.ReadFile(filepath.Join(plan.Dir, name))
		if err != nil || !bytes.Equal(have, plan.Files[name]) {
			t.Errorf("%s is not what cgogen writes; %s", filepath.Join(plan.Dir, name), rerun)
		}
	}
	for _, name := range plan.Stale {
		t.Errorf("%s is no file cgogen writes any longer; %s", filepath.Join(plan.Dir, name), rerun)
	}
	for _, disk := range slices.Sorted(maps.Keys(plan.Sources)) {
		have, err := os.ReadFile(disk)
		if err != nil || !bytes.Equal(have, plan.Sources[disk]) {
			t.Errorf("%s does not carry the go.native marks cgogen writes; %s", disk, rerun)
		}
	}
}

// TestBuilds builds the wrapper package with MinGW for each architecture
// win32 builds for, recorded in testdata/builds/<arch>: a digest of the
// package's files, the C they include, and cbind, which the callbacks
// import, so the build runs again only when one of them moved.
func TestBuilds(t *testing.T) {
	dir := filepath.Join(root, "lib", "platform", "win32", "cgo")
	for _, arch := range []struct{ goarch, cc string }{{"amd64", "x86_64-w64-mingw32-gcc"}, {"386", "i686-w64-mingw32-gcc"}} {
		t.Run(arch.goarch, func(t *testing.T) {
			record := filepath.Join("testdata", "builds", arch.goarch)
			want := buildDigest(t, arch.cc, dir, filepath.Join(root, "lib", "platform", "win32", "rt"), filepath.Join(root, "pkg", "go", "cbind")) + "\n"
			have, _ := os.ReadFile(record)
			switch {
			case string(have) == want && !*verify:
				return
			case !*update && !*verify && have == nil:
				t.Fatalf("%s: no record of this build; rerun with -update on a host with MinGW", record)
			case !*update && !*verify:
				t.Fatalf("%s: the files changed since the package was built; rerun with -update -- `go tool verify -update` rerecords every package's", record)
			}
			cmd := exec.Command("go", "build", ".")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+arch.goarch, "CGO_ENABLED=1", "CC="+arch.cc)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("go build for windows/%s: %v\n%s", arch.goarch, err, out)
			}
			if *update {
				if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(record, []byte(want), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// buildDigest is the digest of the compiler's name and every file in dirs.
func buildDigest(t *testing.T, cc string, dirs ...string) string {
	t.Helper()
	h := sha256.New()
	fmt.Fprintf(h, "cc %s\n", cc)
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			fmt.Fprintf(h, "%s %d\n", filepath.ToSlash(rel), len(data))
			h.Write(data)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// A struct a package names as its own, a leading underscore, is a Go type
// the program can name: each word capitalized, as a C function's wrapper is.
func TestTypeName(t *testing.T) {
	for in, want := range map[string]string{"_p_GtkWidget": "PGtkWidget", "_c_gboolean": "CGboolean", "_gpointer": "Gpointer", "HWND": "HWND", "look": "Look"} {
		if got := typeName(in); got != want {
			t.Errorf("typeName(%q) = %q, want %q", in, got, want)
		}
	}
}
