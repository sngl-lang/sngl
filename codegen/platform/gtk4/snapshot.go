//go:build !js

package gtk4

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Snapshot generates gtk4 Go code, builds it with a cgo harness that
// renders the top-level window into a GdkTexture via
// gtk_widget_paintable_new + GskCairoRenderer, and returns the
// captured PNG. Requires $DISPLAY or $WAYLAND_DISPLAY to be set —
// the same headless-display requirement as RunTests.
func (g *Generator) Snapshot(pkg *ir.Package, lang codegen.LangTranslator, width, height int) ([]byte, error) {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return nil, fmt.Errorf("go not found in PATH")
	}

	sink := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{
		Pkg:  pkg,
		Lang: lang,
		Options: codegen.OptionsFromMap(map[string]any{
			"package": "main",
		}),
	}, sink); err != nil {
		return nil, fmt.Errorf("generating gtk4 code: %w", err)
	}

	// Keyed by the generated content, which the harness and go.mod written
	// below are a function of. See codegen.BuildDirForContent.
	tmpDir, release, err := codegen.BuildDirForContent("snapshot-gtk4", sink.Files())
	if err != nil {
		return nil, fmt.Errorf("creating build dir: %w", err)
	}
	defer release()

	if err := writeGtk4SnapshotHarness(tmpDir, dirIsWrapped(tmpDir)); err != nil {
		return nil, err
	}
	if err := writeGtk4GoMod(tmpDir, ""); err != nil {
		return nil, err
	}

	tidy := exec.Command(goPath, "mod", "tidy")
	tidy.Dir = tmpDir
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		return nil, fmt.Errorf("go mod tidy: %w", err)
	}

	outPath := filepath.Join(tmpDir, "out.png")
	run := exec.Command(goPath, "run", ".", outPath,
		fmt.Sprintf("%d", width), fmt.Sprintf("%d", height))
	run.Dir = tmpDir
	run.Stderr = os.Stderr
	if err := run.Run(); err != nil {
		return nil, fmt.Errorf("running snapshot: %w", err)
	}

	return os.ReadFile(outPath)
}

// BatchSnapshot generates code for multiple documents into sub-packages
// of a single Go module, compiles once, then invokes the binary per
// document. Amortises go mod tidy + cgo build across all docs (the
// big cost for gtk4).
func (g *Generator) BatchSnapshot(docs []codegen.BatchDoc, width, height int) (map[string][]byte, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	if len(docs) == 1 {
		png, err := g.Snapshot(docs[0].Pkg, docs[0].Lang, width, height)
		if err != nil {
			return nil, err
		}
		return map[string][]byte{docs[0].ID: png}, nil
	}

	goPath, err := exec.LookPath("go")
	if err != nil {
		return nil, fmt.Errorf("go not found in PATH")
	}

	tmpDir, release, err := codegen.BuildDir("snapshot-gtk4-batch", codegen.BatchKey(docs)...)
	if err != nil {
		return nil, fmt.Errorf("creating build dir: %w", err)
	}
	defer release()

	genDoc := func(i int, d codegen.BatchDoc, noWrap bool) (gtk4DocPkg, error) {
		pkgName := fmt.Sprintf("doc%d", i)
		pkgDir := filepath.Join(tmpDir, pkgName)
		opts := map[string]any{"package": pkgName}
		if noWrap {
			opts["gtk4NoWrap"] = true
		}
		if err := g.Generate(&codegen.Request{
			Pkg:     d.Pkg,
			Lang:    d.Lang,
			Options: codegen.OptionsFromMap(opts),
		}, codegen.NewDirSink(pkgDir)); err != nil {
			return gtk4DocPkg{}, fmt.Errorf("generating gtk4 code for %s: %w", d.ID, err)
		}
		return gtk4DocPkg{id: d.ID, pkgName: pkgName}, nil
	}

	var pkgs []gtk4DocPkg
	allWrapped := true
	for i, d := range docs {
		p, err := genDoc(i, d, false)
		if err != nil {
			return nil, err
		}
		if !dirIsWrapped(filepath.Join(tmpDir, p.pkgName)) {
			allWrapped = false
		}
		pkgs = append(pkgs, p)
	}

	// The batch dispatcher harness must speak a single ABI to every doc's
	// BuildUI. When every doc wrapped (the norm — docs use only stdlib
	// widgets), emit a cgo-free harness over gtk4rt. If any doc fell back to
	// inline cgo (a raw gtk4.* widget), regenerate them all in cgo mode so the
	// cgo harness's *C.GtkApplication/*C.GtkWidget signatures line up.
	if allWrapped {
		if err := writeGtk4BatchHarnessWrapped(tmpDir, pkgs); err != nil {
			return nil, err
		}
	} else {
		for i, d := range docs {
			if _, err := genDoc(i, d, true); err != nil {
				return nil, err
			}
		}
		if err := writeGtk4BatchHarness(tmpDir, pkgs); err != nil {
			return nil, err
		}
	}
	if err := writeGtk4GoMod(tmpDir, ""); err != nil {
		return nil, err
	}

	tidy := exec.Command(goPath, "mod", "tidy")
	tidy.Dir = tmpDir
	var tidyOut bytes.Buffer
	tidy.Stdout = &tidyOut
	tidy.Stderr = &tidyOut
	if err := tidy.Run(); err != nil {
		return nil, fmt.Errorf("go mod tidy: %w\n%s", err, tidyOut.String())
	}

	// Beside the directory, not in it: the directory is emptied before each
	// generation, and `go build -o` relinks whenever its output is missing.
	binPath := tmpDir + ".snapshot"
	build := exec.Command(goPath, "build", "-buildvcs=false", "-o", binPath, ".")
	build.Dir = tmpDir
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return nil, fmt.Errorf("go build: %w", err)
	}

	results := make(map[string][]byte, len(pkgs))
	for _, p := range pkgs {
		outPath := filepath.Join(tmpDir, p.pkgName+".png")
		run := exec.Command(binPath, p.pkgName, outPath,
			fmt.Sprintf("%d", width), fmt.Sprintf("%d", height))
		run.Dir = tmpDir
		run.Stderr = os.Stderr
		if err := run.Run(); err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", p.id, err)
		}
		png, err := os.ReadFile(outPath)
		if err != nil {
			return nil, fmt.Errorf("read snapshot %s: %w", p.id, err)
		}
		results[p.id] = png
	}
	return results, nil
}

// dirIsWrapped reports whether the gtk4 code generated into dir is wrapped-mode
// (no inline cgo). It keys off model.go lacking an `import "C"`, so the snapshot
// harness can match the model's BuildUI signature (gtk4rt.Handle vs cgo).
func dirIsWrapped(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "model.go"))
	if err != nil {
		return false
	}
	return !bytes.Contains(data, []byte(`import "C"`))
}

func writeGtk4SnapshotHarness(dir string, wrapped bool) error {
	if wrapped {
		harness := `package main

import (
	"fmt"
	"os"
	"strconv"

	"git.duckfam.us/jonathan/sngl/pkg/go/gtk4rt"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: snapshot <out.png> <width> <height>")
		os.Exit(1)
	}
	outPath := os.Args[1]
	w, err := strconv.Atoi(os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad width: %v\n", err)
		os.Exit(1)
	}
	h, err := strconv.Atoi(os.Args[3])
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad height: %v\n", err)
		os.Exit(1)
	}
	if err := gtk4rt.SnapshotModel(func(app gtk4rt.Handle) gtk4rt.Handle {
		return New().BuildUI(app)
	}, w, h, outPath); err != nil {
		fmt.Fprintln(os.Stderr, "snapshot failed:", err)
		os.Exit(1)
	}
}
`
		return os.WriteFile(filepath.Join(dir, "snapshot_main.go"), []byte(harness), 0o644)
	}
	harness := `package main

` + gtk4SnapshotCgo + `

import (
	"fmt"
	"os"
	"strconv"
	"unsafe"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: snapshot <out.png> <width> <height>")
		os.Exit(1)
	}
	outPath := os.Args[1]
	w64, err := strconv.Atoi(os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad width: %v\n", err)
		os.Exit(1)
	}
	h64, err := strconv.Atoi(os.Args[3])
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad height: %v\n", err)
		os.Exit(1)
	}
	width, height := C.int(w64), C.int(h64)

	C.gtk_init()
	app := C.gtk_application_new(C.CString("dev.sngl.snapshot"), C.G_APPLICATION_NON_UNIQUE)
	C.g_application_register((*C.GApplication)(unsafe.Pointer(app)), nil, nil)

	m := New()
	win := m.BuildUI(app)
	C.gtk_window_set_default_size((*C.GtkWindow)(unsafe.Pointer(win)), width, height)
	C.gtk_window_present((*C.GtkWindow)(unsafe.Pointer(win)))
	C.sngl_pump_until_mapped(win, 1000)

	cpath := C.CString(outPath)
	defer C.free(unsafe.Pointer(cpath))
	rc := C.sngl_snapshot(win, width, height, cpath)
	if rc != 0 {
		fmt.Fprintf(os.Stderr, "snapshot failed: code=%d\n", rc)
		os.Exit(int(rc))
	}
}
`
	return os.WriteFile(filepath.Join(dir, "snapshot_main.go"), []byte(harness), 0o644)
}

type gtk4DocPkg struct {
	id      string
	pkgName string
}

// writeGtk4BatchHarnessWrapped is the cgo-free batch dispatcher for when every
// doc generated in wrapped mode. Each doc's BuildUI takes/returns gtk4rt.Handle
// and the render goes through gtk4rt.SnapshotModel, so the harness compiles
// once against the cached gtk4rt cgo rather than recompiling gtk.h per build.
func writeGtk4BatchHarnessWrapped(dir string, pkgs []gtk4DocPkg) error {
	var imports, cases strings.Builder
	for _, p := range pkgs {
		fmt.Fprintf(&imports, "\t%s \"sngltest/%s\"\n", p.pkgName, p.pkgName)
		fmt.Fprintf(&cases, "\tcase %q:\n\t\tbuild = func(app gtk4rt.Handle) gtk4rt.Handle { return %s.New().BuildUI(app) }\n", p.pkgName, p.pkgName)
	}

	harness := `package main

import (
	"fmt"
	"os"
	"strconv"

	"git.duckfam.us/jonathan/sngl/pkg/go/gtk4rt"

` + imports.String() + `)

func main() {
	if len(os.Args) < 5 {
		fmt.Fprintln(os.Stderr, "usage: snapshot <pkg> <out.png> <width> <height>")
		os.Exit(1)
	}
	pkg := os.Args[1]
	outPath := os.Args[2]
	width, err := strconv.Atoi(os.Args[3])
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad width: %v\n", err)
		os.Exit(1)
	}
	height, err := strconv.Atoi(os.Args[4])
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad height: %v\n", err)
		os.Exit(1)
	}

	var build func(app gtk4rt.Handle) gtk4rt.Handle
	switch pkg {
` + cases.String() + `	default:
		fmt.Fprintf(os.Stderr, "unknown doc: %s\n", pkg)
		os.Exit(1)
	}

	if err := gtk4rt.SnapshotModel(build, width, height, outPath); err != nil {
		fmt.Fprintln(os.Stderr, "snapshot failed:", err)
		os.Exit(1)
	}
}
`
	return os.WriteFile(filepath.Join(dir, "main.go"), []byte(harness), 0o644)
}

func writeGtk4BatchHarness(dir string, pkgs []gtk4DocPkg) error {
	var imports, cases strings.Builder
	for _, p := range pkgs {
		fmt.Fprintf(&imports, "\t%s \"sngltest/%s\"\n", p.pkgName, p.pkgName)
		fmt.Fprintf(&cases, "\tcase %q:\n\t\tm := %s.New()\n\t\twin = m.BuildUI(app)\n", p.pkgName, p.pkgName)
	}

	harness := `package main

` + gtk4SnapshotCgo + `

import (
	"fmt"
	"os"
	"strconv"
	"unsafe"

` + imports.String() + `)

func main() {
	if len(os.Args) < 5 {
		fmt.Fprintln(os.Stderr, "usage: snapshot <pkg> <out.png> <width> <height>")
		os.Exit(1)
	}
	pkg := os.Args[1]
	outPath := os.Args[2]
	w64, err := strconv.Atoi(os.Args[3])
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad width: %v\n", err)
		os.Exit(1)
	}
	h64, err := strconv.Atoi(os.Args[4])
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad height: %v\n", err)
		os.Exit(1)
	}
	width, height := C.int(w64), C.int(h64)

	C.gtk_init()
	app := C.gtk_application_new(C.CString("dev.sngl.snapshot"), C.G_APPLICATION_NON_UNIQUE)
	C.g_application_register((*C.GApplication)(unsafe.Pointer(app)), nil, nil)

	var win *C.GtkWidget
	switch pkg {
` + cases.String() + `	default:
		fmt.Fprintf(os.Stderr, "unknown doc: %s\n", pkg)
		os.Exit(1)
	}

	C.gtk_window_set_default_size((*C.GtkWindow)(unsafe.Pointer(win)), width, height)
	C.gtk_window_present((*C.GtkWindow)(unsafe.Pointer(win)))
	C.sngl_pump_until_mapped(win, 1000)

	cpath := C.CString(outPath)
	defer C.free(unsafe.Pointer(cpath))
	rc := C.sngl_snapshot(win, width, height, cpath)
	if rc != 0 {
		fmt.Fprintf(os.Stderr, "snapshot failed: code=%d\n", rc)
		os.Exit(int(rc))
	}
}
`
	return os.WriteFile(filepath.Join(dir, "main.go"), []byte(harness), 0o644)
}

// writeGtk4GoMod writes a minimal go.mod. gtk4 codegen has no Go-module
// deps when emitting plain widget code (it's pure cgo against the
// system gtk4 lib); the optional goModExtra blob lets callers inject a
// `replace` directive pointing at a host sngl module checkout when
// testagent / i18n runtimes are also linked.
func writeGtk4GoMod(dir, goModExtra string) error {
	if goModExtra == "" {
		_, goModExtra = codegen.DetectHostGoMod()
	}
	mod := "module sngltest\n\ngo 1.23\n"
	if goModExtra != "" {
		mod += "\n" + goModExtra
		if !strings.HasSuffix(mod, "\n") {
			mod += "\n"
		}
	}
	return os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644)
}
