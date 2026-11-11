// Package win32 is the SNGL platform generator for classic Windows programs.
//
// It places nothing: the platform asks for `placement`, which leaves a
// program of plain functions, structs and calls of its protocol functions
// (lib/platform/win32), and each language writes that whole. What is left
// here is the program's entry, per language, and the files beside it.
package win32

import (
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	clang "duckfam.us/sngl/codegen/lang/c"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/ir"
	"duckfam.us/sngl/lib"
)

func init() {
	codegen.RegisterNative(platformName, &Generator{})
}

const platformName = "win32"

// Generator implements codegen.PlatformGenerator for Win32.
type Generator struct{}

func (g *Generator) PlatformIdentifier() string { return platformName }
func (g *Generator) Description() string {
	return "Classic Windows GUI over user32, gdi32 and comctl32."
}

// C is the default until a Go program has a test agent (TODO.md Part 4).
func (g *Generator) SupportedLangs() []string { return []string{"c", "go"} }

var _ codegen.TestProber = (*Generator)(nil)

// ProbeTest implements codegen.TestProber: the test agent is cross-compiled
// with MinGW's compiler and, off Windows, run under Wine.
func (g *Generator) ProbeTest() (bool, string) {
	if runtime.GOOS == "windows" {
		return true, ""
	}
	if _, err := exec.LookPath("wine"); err != nil {
		return false, "wine is not on PATH"
	}
	if _, err := exec.LookPath("wineboot"); err != nil {
		return false, "wineboot is not on PATH"
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
		return fmt.Errorf("win32: generating %s is not written yet", lang)
	}
}

// generateC writes req's program as C: main.c, the program and its entry,
// beside the C runtime (pkg/c), the platform's (its c.link's sources) and
// the manifest naming what it links and what it is built for. Built for
// `sngl test`, its entry is the test agent's, serving its tests over the
// channel sngl_win32_agent.c opens.
// appMain is the program's entry. The program is built in a turn, and the
// loop runs each message's in a turn of its own, which the callback a
// message reaches opens; the application tears down as the loop ends.
//
// Where the platform node wrote `@run`, the windows the build attaches are
// held hidden and the handler is called, in a turn, with the command line
// and a `run` presenting them and running the loop. A handler that returns
// without calling it gets the loop with nothing shown, which only
// sngl_win32_quit ends; a second run is refused (design/windows.md).
func appMain(c *clang.Context, pkg *ir.Package) string {
	teardown := ""
	if pkg.Teardown != nil {
		teardown = "\t" + clang.Decl(&pkg.Teardown.DeclInfo) + "();\n"
	}
	build := "\tsngl_win32_carry(sngl_win32_assets);\n\tsngl_turn_enter();\n\tsngl_init();\n\tsngl_build();\n\tsngl_turn_leave();\n"
	if pkg.Run == nil {
		return "\nint main(void) {\n" + build + "\tsngl_win32_run();\n" + teardown + "\treturn 0;\n}\n"
	}
	if len(pkg.Run.Block) == 0 || len(pkg.Run.Params) != 2 {
		return "\nint main(void) {\n\tsngl_win32_hold();\n" + build + "\tsngl_win32_run();\n" + teardown + "\treturn 0;\n}\n"
	}
	c.Include("stdlib.h", true)
	c.Include("string.h", true)
	args, run := c.TypeName(pkg.Run.Params[0].Type), c.TypeName(pkg.Run.Params[1].Type)
	var b strings.Builder
	b.WriteString("\nstatic bool sngl_win32_started;\n\n")
	b.WriteString("static void sngl_win32_start(void *env) {\n\t(void)env;\n\tif (sngl_win32_started) {\n")
	fmt.Fprintf(&b, "\t\tsngl_win32_refuse_run_again(%s);\n\t}\n", strconv.Quote(pkg.RunPos.String()))
	b.WriteString("\tsngl_win32_started = true;\n\tsngl_win32_present();\n\tsngl_win32_run();\n")
	b.WriteString(teardown)
	b.WriteString("}\n\n")
	b.WriteString("int main(int argc, char **argv) {\n\tsngl_win32_hold();\n")
	b.WriteString(build)
	fmt.Fprintf(&b, "\t%s args = {0};\n", args)
	b.WriteString("\tif (argc > 1) {\n\t\targs.data = malloc(sizeof(sngl_string) * (size_t)(argc - 1));\n")
	b.WriteString("\t\tfor (int i = 1; i < argc; i++) {\n\t\t\targs.data[args.len++] = (sngl_string){argv[i], strlen(argv[i])};\n\t\t}\n\t\targs.cap = args.len;\n\t}\n")
	fmt.Fprintf(&b, "\tsngl_turn_enter();\n\t%s(args, (%s){sngl_win32_start, NULL});\n\tsngl_turn_leave();\n", clang.Decl(&pkg.Run.DeclInfo), run)
	b.WriteString("\tif (!sngl_win32_started) {\n\t\tsngl_win32_started = true;\n\t\tsngl_win32_run();\n")
	b.WriteString(strings.ReplaceAll(teardown, "\t", "\t\t"))
	b.WriteString("\t}\n\treturn 0;\n}\n")
	return b.String()
}

func generateC(req *codegen.Request, sink codegen.Sink) error {
	pkg := req.Pkg
	agent := codegen.OptionString(req.Options, "testMode") == "agent"
	fe := clang.NewFile(sink, codegen.FileOptions{Name: "main.c", Source: req.Source, Platform: platformName}, pkg, platformName)
	links := reachedLinks(pkg)
	sources, err := clang.Links(fe.Context(), links)
	if err != nil {
		return fmt.Errorf("win32: %w", err)
	}
	if agent {
		fe.RequireImport(clang.TestHeader)
		fe.RequireImport("sngl_win32_agent.h")
	}
	if err := clang.WriteProgram(fe, pkg); err != nil {
		return fmt.Errorf("win32: %w", err)
	}
	if agent {
		if err := clang.WriteTests(fe, pkg, "sngl_win32_step()"); err != nil {
			return fmt.Errorf("win32: %w", err)
		}
		io.WriteString(fe, "\nint main(int argc, char **argv) {\n\tsngl_win32_carry(sngl_win32_assets);\n\treturn sngl_win32_serve(sngl_tests, sngl_ntests, argc, argv);\n}\n")
	} else if codegen.OptionBool(req.Options, snapshotOption) {
		// Built for `sngl snapshot`: the program built and shown, then drawn
		// at the size asked for into the file its first argument names.
		io.WriteString(fe, "\nint main(int argc, char **argv) {\n\tif (argc < 4) {\n\t\treturn 2;\n\t}\n\tsngl_win32_carry(sngl_win32_assets);\n\tsngl_turn_enter();\n\tsngl_init();\n\tsngl_build();\n\tsngl_turn_leave();\n\treturn sngl_win32_snapshot(argv[1], atoi(argv[2]), atoi(argv[3]));\n}\n")
	} else {
		io.WriteString(fe, appMain(fe.Context(), pkg))
	}
	if err := fe.Close(); err != nil {
		return fmt.Errorf("win32: %w", err)
	}
	manifest := clang.Manifest(links)
	if agent {
		manifest = append(manifest, "flag -lws2_32\n"...)
	}
	manifest = append(manifest, "target windows/"+arch(req)+"\n"...)
	if err := writeFile(sink, clang.BuildFile, manifest); err != nil {
		return err
	}
	// The files the program carries, which the runtime's images show.
	assets, err := clang.Assets(req.FileAssets, "sngl_win32.h", "sngl_win32_asset", "sngl_win32_assets")
	if err != nil {
		return fmt.Errorf("win32: %w", err)
	}
	if err := writeFile(sink, "sngl_win32_assets.c", assets); err != nil {
		return err
	}
	// The platform's runtime is a c.link's sources, the same for every
	// program, so a golden holds their digest too; and so is the agent's
	// channel and the C runtime's.
	runtime := clang.Runtime()
	if agent {
		maps.Copy(runtime, clang.TestRuntime())
		for _, name := range agentFiles {
			data, err := fs.ReadFile(lib.FS, "platform/win32/rt/"+name)
			if err != nil {
				return fmt.Errorf("win32: its test agent: %w", err)
			}
			runtime[name] = data
		}
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

// wrappers is the Go package wrapping the platform's C (sngl tool cgogen),
// which a Go program calls the runtime through.
const wrappers = "duckfam.us/sngl/lib/platform/win32/cgo"

// generateGo writes req's program as Go: main.go, the program
// (golang.WriteProgram) and its entry, which calls the runtime through the
// wrappers.
func generateGo(req *codegen.Request, sink codegen.Sink) error {
	pkg := req.Pkg
	fe := req.Lang.NewFileEmitter(sink, codegen.FileOptions{Name: "main.go", Source: req.Source, Platform: platformName, PackageName: "main"})
	if err := golang.WriteProgram(fe, pkg, platformName); err != nil {
		return fmt.Errorf("win32: %w", err)
	}
	cgo := fe.RequireImport(wrappers)
	fe.RequireImport("runtime")
	if codegen.OptionBool(req.Options, snapshotOption) {
		// Built for `sngl snapshot`, as C's is.
		fe.RequireImport("os")
		fe.RequireImport("strconv")
		io.WriteString(fe, "\nfunc main() {\n\truntime.LockOSThread()\n\tif len(os.Args) < 4 {\n\t\tos.Exit(2)\n\t}\n\tw, _ := strconv.Atoi(os.Args[2])\n\th, _ := strconv.Atoi(os.Args[3])\n\tsnglInit()\n\tsnglBuild()\n\tos.Exit("+cgo+".SnglWin32Snapshot(os.Args[1], w, h))\n}\n")
	} else {
		io.WriteString(fe, "\nfunc main() {\n\truntime.LockOSThread()\n\tsnglInit()\n\tsnglBuild()\n\t"+cgo+".SnglWin32Run()\n}\n")
	}
	if err := fe.Close(); err != nil {
		return err
	}
	// What the program is built for, which the Go build reads as C's does.
	if err := writeFile(sink, clang.BuildFile, []byte("target windows/"+arch(req)+"\n")); err != nil {
		return err
	}
	return carryGo(sink, req.FileAssets)
}

// carryGo writes the files a Go program carries beside it, and assets.go,
// which embeds each and hands it to the runtime before the program builds:
// the table C's build links, made at run time.
func carryGo(sink codegen.Sink, assets []codegen.FileAsset) error {
	if len(assets) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("// Code generated by sngl (win32). DO NOT EDIT.\n\npackage main\n\nimport (\n\t_ \"embed\"\n\n\t\"" + wrappers + "\"\n)\n")
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
		fmt.Fprintf(&init, "\tcgo.SnglWin32CarryFile(%s, snglAsset%d, len(snglAsset%d))\n", strconv.Quote(name), i, i)
	}
	fmt.Fprintf(&b, "\nfunc init() {\n%s}\n", init.String())
	return writeFile(sink, "assets.go", []byte(b.String()))
}

// snapshotOption builds the program for `sngl snapshot` (snapshot.go).
const snapshotOption = "snapshot"

// agentFiles are the test agent's channel, written beside a program built
// for `sngl test`.
var agentFiles = []string{"sngl_win32_agent.c", "sngl_win32_agent.h"}

// reachedLinks are the c.links of the program and of each package whose C
// it calls, among them the platform's own, whose runtime the protocol calls
// call: the program imports none of the target's packages, and what it
// calls of them is what placement wrote.
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

// arch is the architecture the program is built for: the platform's
// option, amd64 where it sets none.
func arch(req *codegen.Request) string {
	if a := codegen.OptionString(req.Options, "arch"); a != "" {
		return a
	}
	return "amd64"
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
