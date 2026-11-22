// Package gtk4 is the SNGL platform generator for GTK 4.
//
// It places nothing: the platform asks for `placement`, which leaves a
// program of plain functions, structs and calls of its protocol functions
// (lib/platform/gtk4), and each language writes that whole. What is left
// here is the program's entry, per language, and the files beside it, and
// the gir: scheme's package, which a program importing `gir:Gtk-4.0` is
// declared from (declgen.go).
package gtk4

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"duckfam.us/sngl/codegen"
	clang "duckfam.us/sngl/codegen/lang/c"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/ir"
)

func init() {
	g := &Generator{}
	codegen.RegisterNative(platformName, g)
	registerGIRScheme(&g.gir)
}

const platformName = "gtk4"

// Generator implements codegen.PlatformGenerator for GTK 4.
type Generator struct {
	gir girState
}

func (g *Generator) PlatformIdentifier() string { return platformName }
func (g *Generator) Description() string {
	return "Native Linux/GNOME desktop GUI using GTK 4."
}

var _ codegen.OptionConfigurable = (*Generator)(nil)

// Configure implements codegen.OptionConfigurable: the "gir" option selects
// the GIR a program's gir: import is declared from -- "builtin" for the
// bundled subset, a path to a Gtk-4.0.gir, or unset to probe the system's
// locations and fall back to the bundled subset.
func (g *Generator) Configure(opts map[string]string) error {
	g.gir.setOption(opts["gir"])
	return nil
}

// LibraryState is the "gir" option, which decides what a gir: import
// declares.
func (g *Generator) LibraryState() string { return g.gir.option() }

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

var _ codegen.ProgramRunner = (*Generator)(nil)

// RunProgram implements codegen.ProgramRunner for a C program, which the
// host's compiler builds against the host's GTK and which runs in the display
// this process has; a Go program is built and run as every Go target's is.
func (g *Generator) RunProgram(ctx context.Context, dir string, runs []codegen.ProgramRun) (string, []codegen.ProgramRan, error) {
	if ok, reason := g.ProbeTest(); !ok {
		return "", nil, &codegen.SkipError{Reason: reason}
	}
	cc, err := clang.ForTarget(clang.TargetOf(dir), "")
	if err != nil {
		return "", nil, &codegen.SkipError{Reason: err.Error()}
	}
	bin := filepath.Join(dir, "app")
	if out, err := cc.BuildDir(dir, bin, "c17", ""); err != nil {
		return out, nil, err
	}
	bin = cc.Exe(bin)
	var ran []codegen.ProgramRan
	for _, r := range runs {
		rctx, cancel := context.WithTimeout(ctx, time.Minute)
		cmd := exec.CommandContext(rctx, bin, r.Args...)
		cmd.WaitDelay = 5 * time.Second
		cmd.Dir = dir
		cmd.Stdin = bytes.NewReader(r.Stdin)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		cancel()
		ran = append(ran, codegen.ProgramRan{Stdout: stdout.String(), Stderr: stderr.String(), Err: err})
	}
	return "", ran, nil
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
	links := ir.CalledCLinks(pkg)
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
		// A test waits by running what GTK has ready, as the loop would, and
		// starts with the last one's windows gone.
		reset, err := cEntry(pkg, "_reset")
		if err != nil {
			return err
		}
		if err := clang.WriteTests(fe, pkg, "g_main_context_iteration(NULL, FALSE)", reset+"()"); err != nil {
			return fmt.Errorf("%s: %w", platformName, err)
		}
		main, err := cTestMain(pkg)
		if err != nil {
			return err
		}
		io.WriteString(fe, main)
	} else if codegen.OptionBool(req.Options, snapshotOption) {
		// Built for `sngl snapshot`: the program built, then its first
		// window drawn at the size asked for into the file its first
		// argument names.
		init, err := cEntry(pkg, "_init")
		if err != nil {
			return err
		}
		capture, err := cEntry(pkg, "_captureFile")
		if err != nil {
			return err
		}
		io.WriteString(fe, "\nint main(int argc, char **argv) {\n\tif (argc < 4) {\n\t\treturn 2;\n\t}\n\t"+init+"();\n\tsngl_gtk_carry(sngl_assets);\n\tsngl_turn_enter();\n\tsngl_init();\n\tsngl_build();\n\tsngl_turn_leave();\n\treturn (int)"+capture+"((sngl_string){argv[1], strlen(argv[1])}, atoi(argv[2]), atoi(argv[3]));\n}\n")
		fe.Context().Include("stdlib.h", true)
		fe.Context().Include("string.h", true)
	} else {
		main, err := appMain(fe.Context(), pkg)
		if err != nil {
			return err
		}
		io.WriteString(fe, clang.WithOpenSettings(main, clang.OpenSettings(pkg, golang.SettingsApp(codegen.OptionString(req.Options, "name")))))
	}
	if err := fe.Close(); err != nil {
		return fmt.Errorf("%s: %w", platformName, err)
	}
	curl, err := clang.HTTPRuntime(fe.Context(), sources)
	if err != nil {
		return fmt.Errorf("%s: %w", platformName, err)
	}
	words, err := clang.LoadManifest(req.ProjectFS, "")
	if err != nil {
		return fmt.Errorf("%s: i18n manifest: %w", platformName, err)
	}
	if clang.I18nRuntime(fe.Context(), sources, words) {
		curl = append(curl, "icu-i18n")
	}
	if err := writeFile(sink, clang.BuildFile, clang.Manifest(links, curl...)); err != nil {
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

// cEntry is the C name of the platform's #[gen.entry] function name, which
// LibFuncs promotes into every build of a program for the platform.
func cEntry(pkg *ir.Package, name string) (string, error) {
	e := pkg.Entry(name)
	if e == nil {
		return "", fmt.Errorf("%s: the platform declares no entry %s", platformName, name)
	}
	return clang.Decl(&e.DeclInfo), nil
}

// appMain is a C program's entry: the platform's #[gen.entry] functions,
// each the application's SNGL. The program is built in a turn once GTK is
// ready, and the application tears down as the loop ends.
//
// Where the platform node wrote `@run`, the tree is built before the loop,
// the windows it attaches held hidden, and the handler is called, in a turn,
// with the command line and a `run` presenting them and running the loop. A
// handler that returns without calling it gets the loop with nothing shown,
// which only quit ends; a second run is refused (design/windows.md). The
// hold follows sngl_init, which sets the platform's vars as it does the
// program's.
func appMain(c *clang.Context, pkg *ir.Package) (string, error) {
	names := map[string]string{}
	for _, n := range []string{"_main", "_init", "_hold", "_present", "_run"} {
		e, err := cEntry(pkg, n)
		if err != nil {
			return "", err
		}
		names[n] = e
	}
	teardown := ""
	if pkg.Teardown != nil {
		teardown = "\t" + clang.Decl(&pkg.Teardown.DeclInfo) + "();\n"
	}
	build := "\tsngl_turn_enter();\n\tsngl_init();\n\tsngl_build();\n\tsngl_turn_leave();\n"
	if pkg.Run == nil {
		e := pkg.Entry("_main")
		return "\nstatic void sngl_start(void *env) {\n\t(void)env;\n" + build + "}\n" +
			"\nint main(void) {\n\tsngl_gtk_carry(sngl_assets);\n\tint status = (int)" + names["_main"] + "((" + c.TypeName(e.Params[0].Type) + "){sngl_start, NULL});\n" + teardown + "\treturn status;\n}\n", nil
	}
	held := "\t" + names["_init"] + "();\n\tsngl_gtk_carry(sngl_assets);\n\tsngl_turn_enter();\n\tsngl_init();\n\t" + names["_hold"] + "();\n\tsngl_build();\n\tsngl_turn_leave();\n"
	if len(pkg.Run.Block) == 0 || len(pkg.Run.Params) != 2 {
		return "\nint main(void) {\n" + held + "\tint status = (int)" + names["_run"] + "();\n" + teardown + "\treturn status;\n}\n", nil
	}
	c.Include("stdio.h", true)
	c.Include("stdlib.h", true)
	c.Include("string.h", true)
	args, run := c.TypeName(pkg.Run.Params[0].Type), c.TypeName(pkg.Run.Params[1].Type)
	var b strings.Builder
	b.WriteString("\nstatic bool sngl_gtk_started;\n\n")
	b.WriteString("static void sngl_gtk_start(void *env) {\n\t(void)env;\n\tif (sngl_gtk_started) {\n")
	fmt.Fprintf(&b, "\t\tfprintf(stderr, \"%%s: run called again; an application runs once per process\\n\", %s);\n\t\texit(2);\n\t}\n", strconv.Quote(pkg.RunPos.String()))
	fmt.Fprintf(&b, "\tsngl_gtk_started = true;\n\t%s();\n\t%s();\n", names["_present"], names["_run"])
	b.WriteString(teardown)
	b.WriteString("}\n\n")
	b.WriteString("int main(int argc, char **argv) {\n")
	b.WriteString(held)
	fmt.Fprintf(&b, "\t%s args = {0};\n", args)
	b.WriteString("\tif (argc > 1) {\n\t\targs.data = malloc(sizeof(sngl_string) * (size_t)(argc - 1));\n")
	b.WriteString("\t\tfor (int i = 1; i < argc; i++) {\n\t\t\targs.data[args.len++] = (sngl_string){argv[i], strlen(argv[i])};\n\t\t}\n\t\targs.cap = args.len;\n\t}\n")
	fmt.Fprintf(&b, "\tsngl_turn_enter();\n\t%s(args, (%s){sngl_gtk_start, NULL});\n\tsngl_turn_leave();\n", clang.Decl(&pkg.Run.DeclInfo), run)
	fmt.Fprintf(&b, "\tif (!sngl_gtk_started) {\n\t\tsngl_gtk_started = true;\n\t\t%s();\n", names["_run"])
	b.WriteString(strings.ReplaceAll(teardown, "\t", "\t\t"))
	b.WriteString("\t}\n\treturn 0;\n}\n")
	return b.String(), nil
}

// cTestMain is a C program's entry built for `sngl test`, serving its tests
// over stdin and stdout. A critical GLib logs is a program misusing GTK -- a
// widget appended that is not one, a pointer gone stale -- which GTK survives
// and a test reading the program's own state would not notice, so it fails
// the test running. A snapshot is the first window the program, or the
// test's component, is shown in (the platform's _captureWindow), drawn at
// 800 by 600 (rt's sngl_draw) and put back (_captureDone).
func cTestMain(pkg *ir.Package) (string, error) {
	var names []any
	for _, n := range []string{"_captureWindow", "_captureDone", "_init"} {
		e, err := cEntry(pkg, n)
		if err != nil {
			return "", err
		}
		names = append(names, e)
	}
	return fmt.Sprintf(`
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
	GtkWidget *win = %[1]s(800, 600);
	if (win == NULL) {
		*why = "nothing is shown";
		return false;
	}
	bool ok = sngl_draw(win, 800, 600, rgba, width, height, why);
	%[2]s(win);
	return ok;
}

int main(void) {
	g_log_set_writer_func(sngl_test_log_writer, NULL, NULL);
	%[3]s();
	sngl_gtk_carry(sngl_assets);
	sngl_test_capture = sngl_test_draw;
	return sngl_test_main(sngl_tests, sngl_ntests);
}
`, names...), nil
}

// generateGo writes req's program as Go: main.go, the program
// (golang.WriteProgram) and its entry, which runs the application through
// the runtime (shimFile), building the program once GTK is ready.
func generateGo(req *codegen.Request, sink codegen.Sink) error {
	pkg := req.Pkg
	fe := req.Lang.NewFileEmitter(sink, codegen.FileOptions{Name: "main.go", Source: req.Source, Platform: platformName, PackageName: "main"})
	if err := golang.WriteProgram(fe, pkg, platformName); err != nil {
		return fmt.Errorf("%s: %w", platformName, err)
	}
	agent := codegen.OptionString(req.Options, "testMode") == "agent"
	// A main opens the place sngl:settings keeps values; a test never does.
	open := golang.OpenSettings(pkg, golang.SettingsFile(codegen.OptionString(req.Options, "name")))
	if open != "" {
		fe.RequireImport(golang.SettingsImportPath)
	}
	opened := func(main string) string {
		return strings.Replace(main, "\truntime.LockOSThread()\n", "\truntime.LockOSThread()\n"+open, 1)
	}
	if agent {
		if err := golang.WriteTests(fe, pkg, "snglEntryReset()"); err != nil {
			return fmt.Errorf("%s: %w", platformName, err)
		}
	}
	fe.RequireImport("os")
	fe.RequireImport("runtime")
	if agent {
		for _, p := range []string{"bytes", "encoding/binary", "errors", "fmt", "image", "image/png", "duckfam.us/sngl/pkg/go/testagent"} {
			fe.RequireImport(p)
		}
		io.WriteString(fe, goAgentMain)
	} else {
		switch {
		case codegen.OptionBool(req.Options, snapshotOption):
			// Built for `sngl snapshot`, as C's is.
			fe.RequireImport("strconv")
			io.WriteString(fe, "\nfunc main() {\n\truntime.LockOSThread()\n\tif len(os.Args) < 4 {\n\t\tos.Exit(2)\n\t}\n\tw, _ := strconv.Atoi(os.Args[2])\n\th, _ := strconv.Atoi(os.Args[3])\n\tsnglEntryInit()\n\tsnglInit()\n\tsnglBuild()\n\tos.Exit(snglEntryCaptureFile(os.Args[1], w, h))\n}\n")
		case golang.RunsHandler(pkg):
			fe.RequireImport("fmt")
			io.WriteString(fe, opened(strings.ReplaceAll(goRunMain, "POS", strconv.Quote(pkg.RunPos.String()))))
		case pkg.Run != nil:
			io.WriteString(fe, opened("\nfunc main() {\n\truntime.LockOSThread()\n\tsnglEntryInit()\n\tsnglInit()\n\tsnglEntryHold()\n\tsnglBuild()\n\tstatus := snglEntryRun()\n\tsnglTeardown()\n\tos.Exit(status)\n}\n"))
		default:
			// The application's loop is the platform's own SNGL, its
			// #[gen.entry].
			io.WriteString(fe, opened("\nfunc main() {\n\truntime.LockOSThread()\n\tstatus := snglEntryMain(func() {\n\t\tsnglInit()\n\t\tsnglBuild()\n\t})\n\tsnglTeardown()\n\tos.Exit(status)\n}\n"))
		}
	}
	if err := fe.Close(); err != nil {
		return err
	}
	if err := writeFile(sink, shimFile, []byte(shim(pkg.CCalls == ir.CCallsDirect))); err != nil {
		return err
	}
	if golang.PackageUsesI18n(pkg) {
		if err := golang.EmitI18nManifestEmbed(sink, "main", req.ProjectFS, ""); err != nil {
			return fmt.Errorf("%s: i18n manifest embed: %w", platformName, err)
		}
	}
	return carryGo(sink, req.FileAssets)
}

// goRunMain is a Go program's entry where the platform node wrote `@run`,
// as C's is (appMain): the tree is built before the loop, the windows it
// attaches held hidden, and the handler is called with the command line and
// a `run` presenting them and running the loop. A handler that returns
// without calling it gets the loop with nothing shown; a second run is
// refused.
const goRunMain = `
var snglStarted bool

func snglStart() {
	if snglStarted {
		fmt.Fprintf(os.Stderr, "%s: run called again; an application runs once per process\n", POS)
		os.Exit(2)
	}
	snglStarted = true
	snglEntryPresent()
	snglEntryRun()
	snglTeardown()
}

func main() {
	runtime.LockOSThread()
	snglEntryInit()
	snglInit()
	snglEntryHold()
	snglBuild()
	snglRun(os.Args[1:], snglStart)
	if !snglStarted {
		snglStarted = true
		snglEntryRun()
		snglTeardown()
	}
}
`

// goAgentMain is a Go program's entry built for `sngl test`: the test agent,
// serving the driver over stdin and stdout on a goroutine, and running each
// run of tests on the thread GTK was made ready on, which main locks.
const goAgentMain = `
func main() {
	runtime.LockOSThread()
	snglEntryInit()
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

// snglCapture is the first window shown, drawn at 800 by 600 as C's agent
// draws it, as a PNG.
func snglCapture() (string, []byte, error) {
	f, err := os.CreateTemp("", "sngl-snapshot-*")
	if err != nil {
		return "", nil, err
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	if snglEntryCaptureFile(path, 800, 600) != 0 {
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
	b.WriteString("// Code generated by sngl (" + platformName + "). DO NOT EDIT.\n\npackage main\n\nimport _ \"embed\"\n")
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
		fmt.Fprintf(&init, "\tsnglGtkCarryFile(%s, snglAsset%d, len(snglAsset%d))\n", strconv.Quote(name), i, i)
	}
	fmt.Fprintf(&b, "\nfunc init() {\n%s}\n", init.String())
	return writeFile(sink, "assets.go", []byte(b.String()))
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
