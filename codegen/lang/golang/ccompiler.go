package golang

import (
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/ir"
)

// EmitCHeader implements codegen.CCompiler for the Go/cgo translator.
// Returns the cgo preamble comment block and `import "C"` line for the
// headers the program's c.link directives name, or "" when there are none.
// <stdlib.h> is always among them, as in gtk4's preamble: lowering frees
// every `C.CString` with `C.free`.
func (t *Translator) EmitCHeader(links []*ir.CLink) string {
	if len(links) == 0 {
		return ""
	}
	if !slices.ContainsFunc(links, func(l *ir.CLink) bool { return l.Include == "stdlib.h" }) {
		links = append(slices.Clip(links), &ir.CLink{Include: "stdlib.h", System: true})
	}
	return "/*\n" + CPreambleLines(links) + "*/\nimport \"C\"\n"
}

// CPreambleLines is what the c.link directives put in a cgo preamble: the
// #cgo flags and the #include lines, one per line, for a target that writes a
// preamble of its own around them.
func CPreambleLines(links []*ir.CLink) string {
	// Each flag once, in the order the headers first name it: a linker
	// resolves a library against the ones after it, so pkg-config's order is
	// meaningful and sorting would break it.
	var cflags, ldflags, pkgs []string
	for _, l := range links {
		for _, p := range l.PkgConfig {
			if !slices.Contains(pkgs, p) {
				pkgs = append(pkgs, p)
			}
		}
		for _, f := range l.CFlags {
			if !slices.Contains(cflags, f) {
				cflags = append(cflags, f)
			}
		}
		for _, f := range l.LDFlags {
			if !slices.Contains(ldflags, f) {
				ldflags = append(ldflags, f)
			}
		}
	}

	var b strings.Builder
	if len(pkgs) > 0 {
		b.WriteString("#cgo pkg-config: " + strings.Join(pkgs, " ") + "\n")
	}
	if len(cflags) > 0 {
		b.WriteString("#cgo CFLAGS: " + strings.Join(cflags, " ") + "\n")
	}
	if len(ldflags) > 0 {
		b.WriteString("#cgo LDFLAGS: " + strings.Join(ldflags, " ") + "\n")
	}
	var seen []string
	for _, l := range links {
		inc := `"` + l.Include + `"`
		if l.System {
			inc = "<" + l.Include + ">"
		}
		if slices.Contains(seen, inc) {
			continue
		}
		seen = append(seen, inc)
		b.WriteString("#include " + inc + "\n")
	}
	return b.String()
}

// CLinks is the c.links a Go program calls the C of (ir.CalledCLinks) but
// those of a package a Go wrapper package calls the C of (`sngl tool
// cgogen`), which links it in its own build: its sources compiled into the
// program as well would define each symbol twice. A program calling C
// directly imports no wrapper package, and links every one.
func CLinks(pkg *ir.Package) []*ir.CLink {
	return ir.CalledCLinksExcept(pkg, func(p *ir.Package) bool { return wrapped(p, pkg.CallsCDirectly) })
}

// wrapped reports whether a wrapper package calls p's C: whether any of its
// functions has a wrapper a build calling C directly or not calls.
func wrapped(p *ir.Package, direct bool) bool {
	return slices.ContainsFunc(p.Funcs, func(fn *ir.Func) bool { return wrapper(&fn.Foreign, direct) != nil })
}

// unwrappedCalls refuses a call of a C function a wrapper package declares
// no wrapper for, where the package's C is that wrapper package's: the
// program links none of it, so it has no C of its own to call it through.
func unwrappedCalls(pkg *ir.Package) error {
	var err error
	_ = ir.Walk(pkg, func(n ir.Node) error {
		c, ok := n.(*ir.Call)
		if !ok || c.Func == nil || err != nil {
			return nil
		}
		fn := c.Func
		if fn.Foreign.Path != ir.CPath || fn.IsCCast() || fn.Foreign.C != nil || wrapper(&fn.Foreign, pkg.CallsCDirectly) != nil || fn.Pkg == nil || !wrapped(fn.Pkg, pkg.CallsCDirectly) {
			return nil
		}
		err = fmt.Errorf("%s.%s has no Go wrapper (sngl tool cgogen lists why), and Go calls %s's C through its wrappers alone", fn.Pkg.Path, fn.Name, fn.Pkg.Path)
		return ir.SkipAll
	})
	return err
}

// CSources implements codegen.CCompiler: cgo compiles every .c file in the
// package's directory, and the preamble includes a header there by its name,
// so each source sits beside the generated Go under its own. The dispatchers
// a C function's callback crosses through join them (ccallback.go).
func (t *Translator) CSources(pkg *ir.Package, generated map[string][]byte) (map[string][]byte, error) {
	if err := unwrappedCalls(pkg); err != nil {
		return nil, err
	}
	links := CLinks(pkg)
	var out map[string][]byte
	for _, l := range links {
		for _, src := range l.Sources {
			if prev, ok := out[src.Name]; ok {
				if string(prev) != src.Text {
					return nil, fmt.Errorf("two c.links ship a source named %s, and one directory holds them all", src.Name)
				}
				continue
			}
			if out == nil {
				out = map[string][]byte{}
			}
			out[src.Name] = []byte(src.Text)
		}
	}
	if sigs := ir.ReachedCCallbacks(pkg, func(fn *ir.Func) bool { return wrapper(&fn.Foreign, pkg.CallsCDirectly) != nil }); len(sigs) > 0 {
		name, err := generatedPackage(generated)
		if err != nil {
			return nil, err
		}
		if _, dup := out[callbacksFile]; dup {
			return nil, fmt.Errorf("a c.link ships %s, which holds the program's C callbacks", callbacksFile)
		}
		if out == nil {
			out = map[string][]byte{}
		}
		out[callbacksFile] = CallbacksSource(name, links, sigs, pkg.CallsCDirectly)
	}
	return out, nil
}
