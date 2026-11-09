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
	"slices"

	"duckfam.us/sngl/codegen"
	clang "duckfam.us/sngl/codegen/lang/c"
	"duckfam.us/sngl/ir"
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

// ProbeTest implements codegen.TestProber: no host runs win32's tests yet,
// since its test agent, which runs under Wine and talks over TCP, is not
// written, and the C language's own launcher would run a Windows program's
// agent over pipes.
func (g *Generator) ProbeTest() (bool, string) {
	return false, "win32's test agent is not written yet"
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
// the manifest naming what it links and what it is built for.
func generateC(req *codegen.Request, sink codegen.Sink) error {
	pkg := req.Pkg
	if pkg.Run != nil {
		return fmt.Errorf("win32: @run is not yet written")
	}
	fe := clang.NewFile(sink, codegen.FileOptions{Name: "main.c", Source: req.Source, Platform: platformName}, pkg, platformName)
	links := reachedLinks(pkg)
	sources, err := clang.Links(fe.Context(), links)
	if err != nil {
		return fmt.Errorf("win32: %w", err)
	}
	if err := clang.WriteProgram(fe, pkg); err != nil {
		return fmt.Errorf("win32: %w", err)
	}
	teardown := ""
	if pkg.Teardown != nil {
		teardown = "\t" + clang.Decl(&pkg.Teardown.DeclInfo) + "();\n"
	}
	// The program is built in a turn, and the loop runs each message's in
	// a turn of its own, which the callback a message reaches opens.
	io.WriteString(fe, "\nint main(void) {\n\tsngl_turn_enter();\n\tsngl_init();\n\tsngl_build();\n\tsngl_turn_leave();\n\tsngl_win32_run();\n"+teardown+"\treturn 0;\n}\n")
	if err := fe.Close(); err != nil {
		return fmt.Errorf("win32: %w", err)
	}
	manifest := clang.Manifest(links)
	manifest = append(manifest, []byte("target windows/"+arch(req)+"\n")...)
	if err := writeFile(sink, clang.BuildFile, manifest); err != nil {
		return err
	}
	// The platform's runtime is a c.link's sources, the same for every
	// program, so a golden holds their digest too.
	for name, data := range sources {
		codegen.MarkBoilerplate(sink, name)
		if err := writeFile(sink, name, data); err != nil {
			return err
		}
	}
	// The runtime is the same for every program, so a golden holds its
	// digest rather than its text.
	for name, data := range clang.Runtime() {
		codegen.MarkBoilerplate(sink, name)
		if err := writeFile(sink, name, data); err != nil {
			return err
		}
	}
	return nil
}

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
