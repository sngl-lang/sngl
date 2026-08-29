package c

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
	"modernc.org/cc/v4"
)

// predefinedPreamble is the minimal predefined C environment needed by cc/v4.
const predefinedPreamble = `
int __predefined_declarator;
#if defined(__i386__) || defined(__arm__)
typedef unsigned __predefined_size_t;
#else
typedef unsigned long long __predefined_size_t;
#endif
`

func init() {
	codegen.RegisterScheme(&CImporter{})
}

// CImporter resolves c: scheme imports by parsing C headers.
type CImporter struct{}

func (c *CImporter) Scheme() string { return "c" }

// Resolve handles two URI forms:
//   - c:path/to/header.h — absolute header file path
//   - c:pkg:name         — pkg-config managed library
func (c *CImporter) Resolve(uri, dir string) (*ir.NativeImport, error) {
	path := uri
	if path == "" {
		return nil, fmt.Errorf("c scheme requires a path (e.g. c:/usr/include/SDL2/SDL.h or c:pkg:sdl2)")
	}

	if after, ok := strings.CutPrefix(path, "pkg:"); ok {
		return c.resolvePkgConfig(after, dir)
	}
	return c.resolveHeader(path, nil, uri)
}

// resolvePkgConfig uses pkg-config to find include paths and the primary header.
func (c *CImporter) resolvePkgConfig(libName, dir string) (*ir.NativeImport, error) {
	if _, err := exec.LookPath("pkg-config"); err != nil {
		return nil, fmt.Errorf("pkg-config required for c:pkg: imports — install it or use a direct header path")
	}

	cflagsOut, err := exec.Command("pkg-config", "--cflags", libName).Output()
	if err != nil {
		return nil, fmt.Errorf("pkg-config --cflags %s: %w", libName, err)
	}
	includePaths := parsePkgConfigCflags(string(cflagsOut))

	libsOut, err := exec.Command("pkg-config", "--libs", libName).Output()
	if err != nil {
		return nil, fmt.Errorf("pkg-config --libs %s: %w", libName, err)
	}
	linkFlags := strings.Fields(strings.TrimSpace(string(libsOut)))

	syntheticInclude := inferPkgHeader(libName)
	syntheticSrc := fmt.Sprintf("#include <%s>\n", syntheticInclude)

	ni, err := c.resolveHeaderWithIncludes(syntheticSrc, includePaths, "c:pkg:"+libName)
	if err != nil {
		return nil, err
	}
	ni.LinkFlags = linkFlags
	return ni, nil
}

// resolveHeader parses a single header file at path.
func (c *CImporter) resolveHeader(path string, includePaths []string, importPath string) (*ir.NativeImport, error) {
	// Read the header directly so cc.Translate can find it without needing its
	// directory on IncludePaths. We pass it as a named Source so error positions
	// reference the real filename.
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading C header %s: %w", path, err)
	}
	// Add the header's directory to IncludePaths so any headers it includes
	// via relative paths can be resolved.
	dir := filepath.Dir(path)
	allIncludes := append([]string{dir}, includePaths...)
	return c.resolveHeaderSource(path, string(content), allIncludes, importPath)
}

// resolveHeaderSource runs cc.Translate on already-loaded source content.
// name is used as the filename in error messages.
func (c *CImporter) resolveHeaderSource(name, content string, includePaths []string, importPath string) (*ir.NativeImport, error) {
	abi, err := cc.NewABI(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, fmt.Errorf("cc.NewABI: %w", err)
	}
	cfg := &cc.Config{
		ABI:          abi,
		IncludePaths: includePaths,
	}
	ast, err := cc.Translate(cfg, []cc.Source{
		{Name: "<predefined>", Value: predefinedPreamble},
		{Name: name, Value: content},
	})
	if err != nil {
		return nil, fmt.Errorf("parsing C header: %w", err)
	}

	return extractDeclarations(ast, importPath)
}

// resolveHeaderWithIncludes runs cc.Translate on a synthetic #include src string.
// Used for pkg-config imports where we emit a synthetic header.
func (c *CImporter) resolveHeaderWithIncludes(src string, includePaths []string, importPath string) (*ir.NativeImport, error) {
	return c.resolveHeaderSource("<import>", src, includePaths, importPath)
}

// extractDeclarations walks the cc AST scope and produces an ir.NativeImport.
func extractDeclarations(ast *cc.AST, importPath string) (*ir.NativeImport, error) {
	ni := &ir.NativeImport{ImportPath: importPath}
	structs := map[string]*ir.StructDef{}

	for _, nodes := range ast.Scope.Nodes {
		for _, node := range nodes {
			switch n := node.(type) {
			case *cc.Declarator:
				if n.IsSynthetic() {
					continue
				}
				name := n.Name()
				if name == "" || strings.HasPrefix(name, "__") {
					continue
				}
				t := n.Type()
				if t == nil {
					continue
				}
				switch t.Kind() {
				case cc.Function:
					ft, ok := t.(*cc.FunctionType)
					if !ok {
						continue
					}
					fn := extractFunc(n, name, ft, ast, structs)
					if fn != nil {
						ni.Funcs = append(ni.Funcs, fn)
					}
				case cc.Struct:
					sd := extractStruct(t, name, ast, structs)
					if sd != nil {
						ni.Structs = appendIfNew(ni.Structs, sd)
					}
				case cc.Enum:
					ed := extractEnum(t, name)
					if ed != nil {
						ni.Enums = append(ni.Enums, ed)
					}
				}
			case *cc.StructOrUnionSpecifier:
				// Struct/union tag declarations in the file scope.
				tagTok := n.Token
				name := tagTok.SrcStr()
				if name == "" || strings.HasPrefix(name, "__") {
					continue
				}
				t := n.Type()
				if t == nil || t.Kind() != cc.Struct {
					continue
				}
				sd := extractStruct(t, name, ast, structs)
				if sd != nil {
					ni.Structs = appendIfNew(ni.Structs, sd)
				}
			}
		}
	}

	// Collect any structs referenced by functions that weren't directly named.
	for _, sd := range structs {
		ni.Structs = appendIfNew(ni.Structs, sd)
	}

	return ni, nil
}

func extractFunc(d *cc.Declarator, name string, ft *cc.FunctionType, ast *cc.AST, structs map[string]*ir.StructDef) *ir.Func {
	fn := &ir.Func{
		Name:    name,
		Foreign: ir.Foreign{Path: "C", Name: "C." + name},
	}

	// Return type.
	if ft.Result() != nil && ft.Result().Kind() != cc.Void {
		ret := mapCType(ft.Result(), ast, structs)
		if ret == nil {
			fn.Foreign.Unusable = fmt.Sprintf("C function %s has unmappable return type", name)
			return fn
		}
		fn.Return = ret
	}

	// Parameters.
	for _, p := range ft.Parameters() {
		pt := mapCType(p.Type(), ast, structs)
		if pt == nil {
			fn.Foreign.Unusable = fmt.Sprintf("C function %s param has unmappable type", name)
			return fn
		}
		pname := p.Name()
		if pname == "" {
			pname = fmt.Sprintf("arg%d", len(fn.Params))
		}
		fn.Params = append(fn.Params, &ir.Param{Name: pname, Type: pt})
	}

	return fn
}

func extractStruct(t cc.Type, name string, ast *cc.AST, structs map[string]*ir.StructDef) *ir.StructDef {
	mapStructType(t, ast, structs)
	return structs[name]
}

func extractEnum(t cc.Type, name string) *ir.EnumDef {
	irType := mapEnumType(t)
	if irType == nil {
		return nil
	}
	ed, ok := irType.Decl.(*ir.EnumDef)
	if !ok {
		return nil
	}
	if ed.Name == "" {
		ed.Name = name
	}
	return ed
}

func appendIfNew(slice []*ir.StructDef, sd *ir.StructDef) []*ir.StructDef {
	for _, existing := range slice {
		if existing.Name == sd.Name {
			return slice
		}
	}
	return append(slice, sd)
}

func parsePkgConfigCflags(cflags string) []string {
	var paths []string
	for field := range strings.FieldsSeq(cflags) {
		if after, ok := strings.CutPrefix(field, "-I"); ok {
			paths = append(paths, after)
		}
	}
	return paths
}

// inferPkgHeader guesses the primary header for a pkg-config library name.
func inferPkgHeader(libName string) string {
	base := libName
	if idx := strings.LastIndex(base, "-"); idx != -1 {
		base = base[:idx]
	}
	base = strings.TrimRight(base, "+")
	if len(base) == 0 {
		return libName + ".h"
	}
	base = strings.ToUpper(base[:1]) + base[1:]
	return base + "/" + base + ".h"
}
