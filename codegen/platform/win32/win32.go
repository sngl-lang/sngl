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
	"os/exec"
	"runtime"
	"slices"

	"duckfam.us/sngl/codegen"
	clang "duckfam.us/sngl/codegen/lang/c"
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
func (g *Generator) SupportedLangs() []string { return []string{"c"} }

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
	default:
		return fmt.Errorf("win32: generating %s is not written yet", lang)
	}
}

// generateC writes req's program as C: main.c, the program and its entry,
// beside the C runtime (pkg/c), the platform's (its c.link's sources) and
// the manifest naming what it links and what it is built for. Built for
// `sngl test`, its entry is the test agent's, serving its tests over the
// channel sngl_win32_agent.c opens.
func generateC(req *codegen.Request, sink codegen.Sink) error {
	pkg := req.Pkg
	if pkg.Run != nil {
		return fmt.Errorf("win32: @run is not yet written")
	}
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
		io.WriteString(fe, "\nint main(int argc, char **argv) {\n\treturn sngl_win32_serve(sngl_tests, sngl_ntests, argc, argv);\n}\n")
	} else if codegen.OptionBool(req.Options, snapshotOption) {
		// Built for `sngl snapshot`: the program built and shown, then drawn
		// at the size asked for into the file its first argument names.
		io.WriteString(fe, "\nint main(int argc, char **argv) {\n\tif (argc < 4) {\n\t\treturn 2;\n\t}\n\tsngl_turn_enter();\n\tsngl_init();\n\tsngl_build();\n\tsngl_turn_leave();\n\treturn sngl_win32_snapshot(argv[1], atoi(argv[2]), atoi(argv[3]));\n}\n")
	} else {
		teardown := ""
		if pkg.Teardown != nil {
			teardown = "\t" + clang.Decl(&pkg.Teardown.DeclInfo) + "();\n"
		}
		// The program is built in a turn, and the loop runs each message's
		// in a turn of its own, which the callback a message reaches opens.
		io.WriteString(fe, "\nint main(void) {\n\tsngl_turn_enter();\n\tsngl_init();\n\tsngl_build();\n\tsngl_turn_leave();\n\tsngl_win32_run();\n"+teardown+"\treturn 0;\n}\n")
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
