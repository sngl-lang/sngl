// Package cgogen writes the Go package that wraps a SNGL package's C
// declarations, so a Go program calling them has no `import "C"` of its own
// and cgo runs once, over an unchanging package, rather than on every build
// of the program.
//
// Each `#[cnative]` function that is no cast becomes one exported Go
// function per C name (`sngl_win32_append` → `SnglWin32Append`), taking what
// the SNGL declaration takes as Go spells it. Its body is the call a Go
// program in direct mode would make: the tool runs lower.CMarshal over a call
// of the declaration and writes it with the Go language's own emitter, so a
// string, a struct and a callback cross exactly as they do there. A handle
// type the functions cross is an alias of cgo's (`type HWND = C.HWND`), and
// a C struct with fields the program's own Go struct, declared here.
//
// Where a wrapper is, the declaration says so with `#[go.native(path,
// name)]` beside its `cnative` mark (Marks), which the Go language reads in
// `wrapper` mode.
package cgogen

import (
	"cmp"
	"fmt"
	"go/format"
	"maps"
	"path"
	"slices"
	"strings"
	"unicode"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
)

// Options say what to wrap and where the package goes.
type Options struct {
	// Pkgs are the SNGL packages wrapped into the one Go package: a
	// platform and the packages beside it whose handles it crosses
	// (sngl:platform/win32 and sngl:platform/win32/api), since a handle is
	// a Go type of the cgo package that declares it.
	Pkgs []*ir.Package
	// ImportPath is the Go package's import path, which each mark names.
	ImportPath string
	// GOOS, where set, is the only one the package builds for: a platform's
	// headers exist for its own system alone.
	GOOS string
	// Sources is the path from the Go package's directory to each SNGL
	// package's own, where its c.link's sources are relative to: cgo
	// compiles only the .c files of the package's directory, so each
	// source is a .c file there that includes it from where it is.
	Sources map[*ir.Package]string
}

// Mark is the `#[go.native(Path, Name)]` the declaration at Pos gets: the
// wrapper, or the type, it is in Go.
type Mark struct {
	Pos        ast.Pos
	Path, Name string
}

// Result is the Go package's files, by name, and the mark each wrapped
// declaration gets.
type Result struct {
	Files map[string][]byte
	Marks []Mark
	// Skipped are the declarations no wrapper is written for, each with
	// why: a Go program calls one directly, in its own cgo.
	Skipped []string
}

// Generate writes the wrapper package for opts.
func Generate(opts Options) (*Result, error) {
	if len(opts.Pkgs) == 0 {
		return nil, fmt.Errorf("cgogen: no package to wrap")
	}
	g := &gen{opts: opts, res: &Result{Files: map[string][]byte{}}, types: map[string]*ir.StructDef{}}
	g.handle = handleType(opts.Pkgs)
	for _, p := range opts.Pkgs {
		g.collect(p)
	}
	if err := g.write(); err != nil {
		return nil, err
	}
	g.res.Marks = sortMarks(g.res.Marks)
	return g.res, nil
}

type gen struct {
	opts Options
	res  *Result
	// handle is the type a platform holds its nodes as, which a node or a
	// component a declaration takes or returns crosses as.
	handle *ir.Type
	// funcs are the wrappers, by Go name, each with the declaration it
	// calls.
	funcs map[string]*wrapper
	// types are the handle types and C structs crossed, by Go name: one
	// package's declaration, which another importing it reached as a
	// declaration of its own copy of the package.
	types map[string]*ir.StructDef
	links []*ir.CLink
	sigs  []*ir.FuncSig
}

type wrapper struct {
	decl *ir.Func
	fn   *ir.Func
	sig  string
}

// handleType is the return type of the platform's #[gen.protocol(handle)]
// among pkgs, or nil where none is a platform placing its nodes.
func handleType(pkgs []*ir.Package) *ir.Type {
	for _, p := range pkgs {
		for _, c := range p.Components {
			// The protocol's own record of the function is the mark's, made
			// before the types resolved; the package's is the one checked.
			h := c.Protocol.Op(ir.ProtocolHandle)
			if h == nil {
				continue
			}
			for _, fn := range p.Funcs {
				if fn.Name == h.Name && fn.Return != nil && fn.Return.Kind != ir.TypeInvalid {
					return fn.Return
				}
			}
		}
	}
	return nil
}

// GoName is the exported Go name a C identifier is wrapped as:
// `sngl_win32_append` is `SnglWin32Append`, `CreateWindowExW` itself.
func GoName(c string) string {
	var b strings.Builder
	for part := range strings.SplitSeq(c, "_") {
		if part == "" {
			continue
		}
		r := []rune(part)
		r[0] = unicode.ToUpper(r[0])
		b.WriteString(string(r))
	}
	return b.String()
}

func (g *gen) collect(p *ir.Package) {
	for _, l := range p.CLinks {
		if !slices.Contains(g.links, l) {
			g.links = append(g.links, l)
		}
	}
	for _, fn := range p.Funcs {
		if fn.Foreign.Path != ir.CPath || fn.IsCCast() || fn.Foreign.C != nil {
			continue
		}
		if why := g.unwrappable(fn); why != "" {
			g.res.Skipped = append(g.res.Skipped, fmt.Sprintf("%s (%s): %s", fn.Name, fn.Foreign.Name, why))
			continue
		}
		g.add(fn)
	}
}

// unwrappable is why fn gets no wrapper, or "".
func (g *gen) unwrappable(fn *ir.Func) string {
	for _, p := range fn.Params {
		if p.Type != nil && p.Type.Kind == ir.TypeRef {
			return "a ref parameter, which crosses as a record of the results only lowering declares"
		}
		if p.Type != nil && p.Type.Kind == ir.TypeOption {
			return "an option parameter, which crosses as NULL only where the call writes null"
		}
	}
	return ""
}

// add wraps fn, once per C name: a second declaration of one C function
// (win32's append, once per kind of parent) shares the first's wrapper
// where it takes and returns the same Go types.
func (g *gen) add(fn *ir.Func) {
	name := GoName(fn.Foreign.Name)
	w := g.wrapperFunc(fn, name)
	sig := g.goSig(w, false)
	if g.funcs == nil {
		g.funcs = map[string]*wrapper{}
	}
	if prev, ok := g.funcs[name]; ok {
		if g.goSig(prev.fn, false) != sig {
			g.res.Skipped = append(g.res.Skipped, fmt.Sprintf("%s (%s): its Go signature differs from %s's, which wraps the C function", fn.Name, fn.Foreign.Name, prev.decl.Name))
			return
		}
	} else {
		g.funcs[name] = &wrapper{decl: fn, fn: w, sig: g.goSig(w, true)}
	}
	if fn.AST != nil {
		g.res.Marks = append(g.res.Marks, g.mark(fn.AST.Pos, name))
	}
}

// wrapperFunc is the SNGL function the wrapper is: fn's parameters but the
// ones the compiler passes, a node as the platform's handle, and a body
// calling fn with them.
func (g *gen) wrapperFunc(fn *ir.Func, name string) *ir.Func {
	w := &ir.Func{Return: g.retype(fn.Return)}
	w.Name = name
	call := &ir.Call{Func: fn, Type: fn.Return}
	// A method's receiver is the call's first argument, which no parameter
	// declares.
	if fn.IsMethod() {
		recv := &ir.Param{Name: "recv", Type: g.retype(fn.Receiver)}
		w.Params = append(w.Params, recv)
		call.Args = append(call.Args, ir.CallArg{Value: &ir.Ident{Name: recv.Name, Sym: recv, Type: recv.Type}})
	}
	for _, p := range fn.Params {
		if p.CompilerPassed() {
			continue
		}
		np := &ir.Param{Name: goLocal(p.Name), Type: g.retype(p.Type)}
		w.Params = append(w.Params, np)
		call.Args = append(call.Args, ir.CallArg{Value: &ir.Ident{Name: np.Name, Sym: np, Type: np.Type}})
		if p.Type != nil && p.Type.Kind == ir.TypeFunc && p.Type.Sig != nil {
			g.sigs = append(g.sigs, p.Type.Sig)
		}
	}
	if fn.Return == nil || fn.Return.Kind == ir.TypeVoid {
		w.Block = []ir.Stmt{&ir.CallStmt{Call: call}}
	} else {
		w.Block = []ir.Stmt{&ir.Return{Value: call}}
	}
	return w
}

// retype is t with each node the platform's handle.
func (g *gen) retype(t *ir.Type) *ir.Type {
	if t == nil {
		return nil
	}
	if t.Kind == ir.TypeComponent && g.handle != nil {
		return g.handle
	}
	return t.MapChildren(g.retype)
}

var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
	// The names the body itself declares or calls.
	"C": true, "unsafe": true, "cbind": true,
}

func goLocal(name string) string {
	if goKeywords[name] || strings.HasPrefix(name, "__sngl") || strings.HasPrefix(name, "sngl") {
		return name + "_"
	}
	return name
}

// goType is t as the wrapper package spells it: a handle or a C struct by
// its name here, recording it, and anything else as the Go language does.
func (g *gen) goType(t *ir.Type) string {
	if t == nil || t.Kind == ir.TypeVoid {
		return ""
	}
	if sd, ok := t.Decl.(*ir.StructDef); ok && t.Kind == ir.TypeStruct && sd.Foreign.Path == ir.CPath && sd.Foreign.C != nil {
		ct := golang.CTypeToGo(sd.Foreign.C)
		if !strings.Contains(ct, "C.") {
			return ct
		}
		name := golang.ExportName(sd.Name)
		if _, ok := g.types[name]; !ok {
			g.types[name] = sd
			for _, f := range sd.Fields {
				g.goType(f.Type)
			}
		}
		return name
	}
	if t.Kind == ir.TypeFunc && t.Sig != nil {
		var ps []string
		for _, p := range t.Sig.Params {
			ps = append(ps, g.goType(p.Type))
		}
		r := g.goType(t.Sig.Return)
		if r != "" {
			r = " " + r
		}
		return "func(" + strings.Join(ps, ", ") + ")" + r
	}
	return golang.IRTypeToGo(t)
}

// goSig is w's Go signature, with its parameters' names where named says
// so: two declarations of one C function share a wrapper where their types
// agree, whatever each calls its parameters.
func (g *gen) goSig(w *ir.Func, named bool) string {
	var ps []string
	for _, p := range w.Params {
		if named {
			ps = append(ps, p.Name+" "+g.goType(p.Type))
		} else {
			ps = append(ps, g.goType(p.Type))
		}
	}
	r := g.goType(w.Return)
	if r != "" {
		r = " " + r
	}
	return "(" + strings.Join(ps, ", ") + ")" + r
}

// pkgName is the Go package's name, the last element of its import path.
func (g *gen) pkgName() string {
	return path.Base(g.opts.ImportPath)
}

func (g *gen) header(cgo string) string {
	var b strings.Builder
	b.WriteString(header + "\n\n")
	if g.opts.GOOS != "" {
		b.WriteString("//go:build " + g.opts.GOOS + "\n\n")
	}
	b.WriteString("package " + g.pkgName() + "\n\n")
	if cgo != "" {
		b.WriteString("/*\n" + cgo + "*/\nimport \"C\"\n\n")
	}
	return b.String()
}

func (g *gen) write() error {
	wrappers := make([]*ir.Func, 0, len(g.funcs))
	for _, name := range slices.Sorted(maps.Keys(g.funcs)) {
		wrappers = append(wrappers, g.funcs[name].fn)
	}
	if err := lower.CWrappers(&ir.Package{Funcs: wrappers}); err != nil {
		return fmt.Errorf("cgogen: %w", err)
	}
	gc := golang.NewIRContext(&codegen.ExprCtx{Pkg: &ir.Package{}})
	var body strings.Builder
	for _, name := range slices.Sorted(maps.Keys(g.funcs)) {
		w := g.funcs[name]
		fmt.Fprintf(&body, "\n// %s calls %s.\nfunc %s%s {\n", name, w.decl.Foreign.Name, name, w.sig)
		lc := gc
		for _, p := range w.fn.Params {
			lc = lc.WithLocal(p.Name)
		}
		for _, s := range w.fn.Block {
			for _, l := range lc.EvalStmt(s) {
				body.WriteString("\t" + l + "\n")
			}
		}
		body.WriteString("}\n")
	}
	var types strings.Builder
	for _, name := range slices.Sorted(maps.Keys(g.types)) {
		sd := g.types[name]
		if sd.AST != nil {
			g.res.Marks = append(g.res.Marks, g.mark(sd.AST.Pos, name))
		}
		if len(sd.Fields) == 0 {
			fmt.Fprintf(&types, "\n// %s is C's %s.\ntype %s = %s\n", name, sd.Foreign.Name, name, golang.CTypeToGo(sd.Foreign.C))
			continue
		}
		fmt.Fprintf(&types, "\n// %s is C's %s, field by field.\ntype %s struct {\n", name, sd.Foreign.Name, name)
		for _, f := range sd.Fields {
			fmt.Fprintf(&types, "\t%s %s\n", golang.ExportName(f.Name), g.goType(f.Type))
		}
		types.WriteString("}\n")
	}
	links := g.links
	if !slices.ContainsFunc(links, func(l *ir.CLink) bool { return l.Include == "stdlib.h" }) {
		links = append(slices.Clip(links), &ir.CLink{Include: "stdlib.h", System: true})
	}
	cgo := g.cflags() + golang.CPreambleLines(links)
	var src strings.Builder
	src.WriteString(g.header(cgo))
	if imps := gc.Imports(); len(imps) > 0 {
		src.WriteString("import (\n")
		for _, imp := range imps {
			src.WriteString("\t" + fmt.Sprintf("%q", imp) + "\n")
		}
		src.WriteString(")\n")
	}
	src.WriteString(types.String() + body.String())
	if len(g.res.Skipped) > 0 {
		src.WriteString("\n// Not wrapped, which a Go program calls directly:\n//\n")
		for _, s := range g.res.Skipped {
			src.WriteString("//   - " + s + "\n")
		}
	}
	out, err := format.Source([]byte(src.String()))
	if err != nil {
		return fmt.Errorf("cgogen: the wrappers do not parse: %w\n%s", err, src.String())
	}
	g.res.Files["wrappers.go"] = out
	if cb := golang.CallbacksSource(g.pkgName(), links, g.sigs); cb != nil {
		cb = []byte(strings.Replace(string(cb), "// Code generated by sngl; DO NOT EDIT.\n\n", strings.TrimSuffix(g.header(""), "package "+g.pkgName()+"\n\n"), 1))
		out, err := format.Source(cb)
		if err != nil {
			return fmt.Errorf("cgogen: the callbacks do not parse: %w", err)
		}
		g.res.Files["callbacks.go"] = out
	}
	for p, dir := range g.opts.Sources {
		for _, l := range p.CLinks {
			for _, s := range l.Sources {
				if !strings.HasSuffix(s.Name, ".c") {
					continue
				}
				var b strings.Builder
				b.WriteString(header + "\n\n")
				if g.opts.GOOS != "" {
					b.WriteString("//go:build " + g.opts.GOOS + "\n\n")
				}
				fmt.Fprintf(&b, "#include %q\n", path.Join(dir, s.Path))
				g.res.Files[path.Base(s.Name)] = []byte(b.String())
			}
		}
	}
	return nil
}

// cflags is the #cgo line that finds each package's headers where its
// sources are, which the preamble includes by name.
func (g *gen) cflags() string {
	var dirs []string
	for _, p := range slices.SortedFunc(maps.Keys(g.opts.Sources), func(a, b *ir.Package) int { return cmp.Compare(a.Path, b.Path) }) {
		for _, l := range p.CLinks {
			for _, s := range l.Sources {
				d := "-I${SRCDIR}/" + path.Join(g.opts.Sources[p], path.Dir(s.Path))
				if !slices.Contains(dirs, d) {
					dirs = append(dirs, d)
				}
			}
		}
	}
	if len(dirs) == 0 {
		return ""
	}
	return "#cgo CFLAGS: " + strings.Join(dirs, " ") + "\n"
}

// mark is the go.native mark of the declaration at pos, naming name in the
// wrapper package the way every go.native names a Go one: qualified by the
// package, as a call writes it.
func (g *gen) mark(pos ast.Pos, name string) Mark {
	return Mark{Pos: pos, Path: g.opts.ImportPath, Name: path.Base(g.opts.ImportPath) + "." + name}
}
