// Package gtk4next is the SNGL platform generator for GTK 4 as gtk4 is
// becoming, built beside codegen/platform/gtk4 until it does all gtk4 does
// (TODO.md, Part 5).
//
// It places nothing: the platform asks for `placement`, which leaves a
// program of plain functions, structs and calls of its protocol functions
// (lib/platform/gtk4next), and each language writes that whole. What is left
// here is the program's entry, per language, and the files beside it.
package gtk4next

import (
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	clang "duckfam.us/sngl/codegen/lang/c"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/ir"
)

func init() {
	codegen.RegisterNative(platformName, &Generator{})
}

const platformName = "gtk4next"

// Generator implements codegen.PlatformGenerator for GTK 4.
type Generator struct{}

func (g *Generator) PlatformIdentifier() string { return platformName }
func (g *Generator) Description() string {
	return "GTK 4, placed in SNGL over its C runtime; to replace gtk4."
}

func (g *Generator) SupportedLangs() []string { return []string{"go", "c"} }

var _ codegen.TestProber = (*Generator)(nil)

// ProbeTest implements codegen.TestProber: a program builds against the
// host's GTK, which pkg-config finds.
func (g *Generator) ProbeTest() (bool, string) {
	if _, err := exec.LookPath("pkg-config"); err != nil {
		return false, "pkg-config is not on PATH"
	}
	if err := exec.Command("pkg-config", "--exists", "gtk4").Run(); err != nil {
		return false, "gtk4 dev libraries not installed (pkg-config)"
	}
	return true, ""
}

func (g *Generator) Generate(req *codegen.Request, sink codegen.Sink) error {
	switch lang := req.Lang.LanguageIdentifier(); lang {
	case "c":
		return generateC(req, sink)
	case "go":
		return generateGo(req, sink)
	default:
		return fmt.Errorf("%s: generating %s is not written yet", platformName, lang)
	}
}

// generateC writes req's program as C: main.c, the program and its entry,
// beside the C runtime (pkg/c), the platform's (its c.link's sources) and the
// manifest naming what it links. The program is built in a turn once GTK is
// ready, and the application tears down as the loop ends.
func generateC(req *codegen.Request, sink codegen.Sink) error {
	pkg := req.Pkg
	agent := codegen.OptionString(req.Options, "testMode") == "agent"
	fe := clang.NewFile(sink, codegen.FileOptions{Name: "main.c", Source: req.Source, Platform: platformName}, pkg, platformName)
	links := reachedLinks(pkg)
	sources, err := clang.Links(fe.Context(), links)
	if err != nil {
		return fmt.Errorf("%s: %w", platformName, err)
	}
	if agent {
		fe.RequireImport(clang.TestHeader)
	}
	if err := clang.WriteProgram(fe, pkg); err != nil {
		return fmt.Errorf("%s: %w", platformName, err)
	}
	if agent {
		// A test waits by running what GTK has ready, as the loop would.
		if err := clang.WriteTests(fe, pkg, "g_main_context_iteration(NULL, FALSE)"); err != nil {
			return fmt.Errorf("%s: %w", platformName, err)
		}
		io.WriteString(fe, cTestMain)
	} else {
		teardown := ""
		if pkg.Teardown != nil {
			teardown = "\t" + clang.Decl(&pkg.Teardown.DeclInfo) + "();\n"
		}
		io.WriteString(fe, "\nstatic void sngl_start(void) {\n\tsngl_turn_enter();\n\tsngl_init();\n\tsngl_build();\n\tsngl_turn_leave();\n}\n")
		io.WriteString(fe, "\nint main(void) {\n\tsngl_gtk_carry(sngl_assets);\n\tint status = sngl_app_run(sngl_start);\n"+teardown+"\treturn status;\n}\n")
	}
	if err := fe.Close(); err != nil {
		return fmt.Errorf("%s: %w", platformName, err)
	}
	if err := writeFile(sink, clang.BuildFile, clang.Manifest(links)); err != nil {
		return err
	}
	// The files the program carries, which the runtime's images show.
	assets, err := clang.Assets(req.FileAssets, "sngl_gtk.h", "SnglAsset", "sngl_assets")
	if err != nil {
		return fmt.Errorf("%s: %w", platformName, err)
	}
	if err := writeFile(sink, "sngl_assets.c", assets); err != nil {
		return err
	}
	// The platform's runtime is a c.link's sources, the same for every
	// program, so a golden holds their digest too; and so is the C runtime's.
	runtime := clang.Runtime()
	if agent {
		maps.Copy(runtime, clang.TestRuntime())
	}
	maps.Copy(runtime, sources)
	for name, data := range runtime {
		codegen.MarkBoilerplate(sink, name)
		if err := writeFile(sink, name, data); err != nil {
			return err
		}
	}
	return nil
}

// cTestMain is a C program's entry built for `sngl test`, serving its tests
// over stdin and stdout. A critical GLib logs is a program misusing GTK -- a
// widget appended that is not one, a pointer gone stale -- which GTK survives
// and a test reading the program's own state would not notice, so it fails
// the test running. A snapshot is the window the program, or the test's
// component, was last shown in, drawn at 800 by 600.
const cTestMain = `
static GLogWriterOutput sngl_test_log_writer(GLogLevelFlags level, const GLogField *fields, gsize n, gpointer data) {
	if (level & G_LOG_LEVEL_CRITICAL) {
		for (gsize i = 0; i < n; i++) {
			if (strcmp(fields[i].key, "MESSAGE") == 0 && fields[i].length < 0) {
				sngl_test_fault(fields[i].value);
			}
		}
	}
	return g_log_writer_default(level, fields, n, data);
}

static bool sngl_test_draw(unsigned char **rgba, int *width, int *height, const char **why) {
	return sngl_capture(NULL, 800, 600, rgba, width, height, why);
}

int main(void) {
	g_log_set_writer_func(sngl_test_log_writer, NULL, NULL);
	gtk_init();
	sngl_gtk_carry(sngl_assets);
	sngl_test_capture = sngl_test_draw;
	return sngl_test_main(sngl_tests, sngl_ntests);
}
`

// wrappers is the Go package wrapping the platform's C (sngl tool cgogen),
// which a Go program calls the runtime and GTK through.
const wrappers = "duckfam.us/sngl/lib/platform/gtk4next/cgo"

// generateGo writes req's program as Go: main.go, the program
// (golang.WriteProgram) and its entry, which runs the application through
// the wrappers, building the program once GTK is ready.
func generateGo(req *codegen.Request, sink codegen.Sink) error {
	pkg := req.Pkg
	fe := req.Lang.NewFileEmitter(sink, codegen.FileOptions{Name: "main.go", Source: req.Source, Platform: platformName, PackageName: "main"})
	if err := golang.WriteProgram(fe, pkg, platformName); err != nil {
		return fmt.Errorf("%s: %w", platformName, err)
	}
	agent := codegen.OptionString(req.Options, "testMode") == "agent"
	if agent {
		if err := golang.WriteTests(fe, pkg); err != nil {
			return fmt.Errorf("%s: %w", platformName, err)
		}
	}
	cgo := fe.RequireImport(wrappers)
	fe.RequireImport("os")
	fe.RequireImport("runtime")
	if agent {
		for _, p := range []string{"bytes", "encoding/binary", "errors", "fmt", "image", "image/png", "duckfam.us/sngl/pkg/go/testagent"} {
			fe.RequireImport(p)
		}
		io.WriteString(fe, strings.ReplaceAll(goAgentMain, "cgo.", cgo+"."))
	} else {
		io.WriteString(fe, "\nfunc main() {\n\truntime.LockOSThread()\n\tstatus := "+cgo+".SnglGtkMain(func() {\n\t\tsnglInit()\n\t\tsnglBuild()\n\t})\n\tsnglTeardown()\n\tos.Exit(status)\n}\n")
	}
	if err := fe.Close(); err != nil {
		return err
	}
	return carryGo(sink, req.FileAssets)
}

// goAgentMain is a Go program's entry built for `sngl test`: the test agent,
// serving the driver over stdin and stdout on a goroutine, and running each
// run of tests on the thread GTK was made ready on, which main locks.
const goAgentMain = `
func main() {
	runtime.LockOSThread()
	cgo.SnglGtkInit()
	testagent.RegisterSnapshot(snglCapture)
	rpcIn, rpcOut := os.Stdin, os.Stdout
	if devnull, err := os.Open(os.DevNull); err == nil {
		os.Stdin = devnull
	}
	os.Stdout = os.Stderr
	runs := make(chan func(), 8)
	done := make(chan struct{})
	go func() {
		testagent.Serve(rpcIn, rpcOut, func(run func()) { runs <- run })
		close(done)
	}()
	for {
		select {
		case run := <-runs:
			run()
		case <-done:
			return
		}
	}
}

// snglCapture is what was shown last, drawn at 800 by 600 as C's agent
// draws it, as a PNG.
func snglCapture() (string, []byte, error) {
	f, err := os.CreateTemp("", "sngl-snapshot-*")
	if err != nil {
		return "", nil, err
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	if cgo.SnglGtkCaptureFile(path, 800, 600) != 0 {
		return "", nil, errors.New("nothing is shown to draw")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	if len(raw) < 8 {
		return "", nil, fmt.Errorf("a capture of %d bytes", len(raw))
	}
	w, h := int(binary.LittleEndian.Uint32(raw[0:4])), int(binary.LittleEndian.Uint32(raw[4:8]))
	if pix := raw[8:]; w <= 0 || h <= 0 || len(pix) != w*h*4 {
		return "", nil, fmt.Errorf("a capture of %d bytes of pixels for %d by %d", len(pix), w, h)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, &image.NRGBA{Pix: raw[8:], Stride: w * 4, Rect: image.Rect(0, 0, w, h)}); err != nil {
		return "", nil, err
	}
	return "image/png", buf.Bytes(), nil
}
`

// carryGo writes assets.go, which embeds each file the program carries and
// hands it to the runtime before main runs.
func carryGo(sink codegen.Sink, assets []codegen.FileAsset) error {
	if len(assets) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("// Code generated by sngl (" + platformName + "). DO NOT EDIT.\n\npackage main\n\nimport (\n\t_ \"embed\"\n\n\t\"" + wrappers + "\"\n)\n")
	var init strings.Builder
	for i, fa := range assets {
		data := fa.Data
		if data == nil {
			d, err := os.ReadFile(fa.SrcPath)
			if err != nil {
				return err
			}
			data = d
		}
		name := filepath.ToSlash(fa.OutPath)
		if err := writeFile(sink, name, data); err != nil {
			return err
		}
		fmt.Fprintf(&b, "\n//go:embed %s\nvar snglAsset%d string\n", name, i)
		fmt.Fprintf(&init, "\tcgo.SnglGtkCarryFile(%s, snglAsset%d, len(snglAsset%d))\n", strconv.Quote(name), i, i)
	}
	fmt.Fprintf(&b, "\nfunc init() {\n%s}\n", init.String())
	return writeFile(sink, "assets.go", []byte(b.String()))
}

// reachedLinks are the c.links of the program and of each package whose C
// it calls, among them the platform's own, whose runtime the protocol calls
// call: the program imports none of the target's packages, and what it calls
// of them is what placement wrote.
func reachedLinks(pkg *ir.Package) []*ir.CLink {
	out := ir.ReachedCLinks(pkg)
	add := func(p *ir.Package) {
		for _, l := range ir.ReachedCLinks(p) {
			if !slices.Contains(out, l) {
				out = append(out, l)
			}
		}
	}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Call:
			if x.Func != nil && x.Func.Foreign.Path == ir.CPath {
				add(x.Func.Pkg)
			}
		case *ir.CConvert:
			if x.Via != nil {
				add(x.Via.Pkg)
			}
		}
		return nil
	})
	return out
}

func writeFile(sink codegen.Sink, name string, content []byte) error {
	wc, err := sink.Create(name)
	if err != nil {
		return err
	}
	if _, err := wc.Write(content); err != nil {
		wc.Close()
		return err
	}
	return wc.Close()
}
