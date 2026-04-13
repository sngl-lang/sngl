package golang

import (
	"fmt"
	goast "go/ast"
	"go/types"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"golang.org/x/tools/go/packages"
)

func init() {
	codegen.RegisterScheme(&GoImporter{})
}

// GoImporter resolves go:// scheme imports by loading Go packages
// and extracting exported types, functions, and variables.
type GoImporter struct{}

func (g *GoImporter) Scheme() string { return "go" }

func (g *GoImporter) Resolve(uri, dir string) (*codegen.NativeDecls, error) {
	pkgPath := strings.TrimPrefix(uri, "go://")

	cfg := &packages.Config{
		Mode: packages.NeedTypes | packages.NeedName | packages.NeedSyntax,
		Dir:  dir,
	}
	pkgs, err := packages.Load(cfg, pkgPath)
	if err != nil {
		return nil, fmt.Errorf("loading Go package %q: %w", pkgPath, err)
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no Go package found for %q", pkgPath)
	}
	if len(pkgs[0].Errors) > 0 {
		return nil, fmt.Errorf("loading %q: %s", pkgPath, pkgs[0].Errors[0].Msg)
	}

	decls := &codegen.NativeDecls{
		ImportPath: pkgPath,
	}
	scope := pkgs[0].Types.Scope()
	pkgName := pkgs[0].Types.Name()

	// Scan syntax for //sngl:pure annotations on function declarations.
	funcPure := map[string]bool{}
	for _, file := range pkgs[0].Syntax {
		for _, decl := range file.Decls {
			fd, ok := decl.(*goast.FuncDecl)
			if !ok || fd.Doc == nil {
				continue
			}
			for _, comment := range fd.Doc.List {
				if strings.Contains(comment.Text, "sngl:pure") {
					funcPure[fd.Name.Name] = true
				}
			}
		}
	}

	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		switch o := obj.(type) {
		case *types.TypeName:
			if ns := goTypeToNativeStruct(o); ns != nil {
				decls.Structs = append(decls.Structs, *ns)
			}
		case *types.Func:
			if nf := goFuncToNativeFunc(o, pkgPath, pkgName); nf != nil {
				nf.Pure = funcPure[o.Name()]
				decls.Funcs = append(decls.Funcs, *nf)
			}
		case *types.Var:
			hint := goTypeToHint(o.Type())
			decls.Vars = append(decls.Vars, codegen.NativeVar{
				Name:       o.Name(),
				Type:       hint,
				NativePkg:  pkgPath,
				NativeType: pkgName + "." + o.Name(),
			})
		}
	}

	return decls, nil
}

// goTypeToNativeStruct converts a Go named struct type to a codegen.NativeStruct.
func goTypeToNativeStruct(tn *types.TypeName) *codegen.NativeStruct {
	st, ok := tn.Type().Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	ns := &codegen.NativeStruct{Name: tn.Name()}
	for f := range st.Fields() {
		if !f.Exported() {
			continue
		}
		hint := goTypeToHint(f.Type())
		ns.Fields = append(ns.Fields, codegen.NativeField{
			Name: lowerFirst(f.Name()),
			Type: hint,
		})
	}
	return ns
}

// goFuncToNativeFunc converts a Go function to a codegen.NativeFunc.
// If the first parameter is *http.Request or context.Context, it is stripped
// from the SNGL-visible signature.
func goFuncToNativeFunc(fn *types.Func, pkgPath, pkgName string) *codegen.NativeFunc {
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return nil
	}

	var paramTypes []string
	params := sig.Params()
	stripping := true
	for v := range params.Variables() {
		if stripping {
			if hp := detectHiddenParam(v.Type()); hp != "" {
				continue
			}
			stripping = false
		}
		paramTypes = append(paramTypes, goTypeToHint(v.Type()))
	}

	var returnType string
	results := sig.Results()
	if results.Len() == 1 {
		returnType = goTypeToHint(results.At(0).Type())
	}

	return &codegen.NativeFunc{
		Name:       fn.Name(),
		ParamTypes: paramTypes,
		ReturnType: returnType,
		NativePkg:  pkgPath,
		NativeType: pkgName + "." + fn.Name(),
	}
}

// detectHiddenParam checks if a type is http.ResponseWriter, *http.Request,
// or context.Context, returning the Go type string if so.
func detectHiddenParam(t types.Type) string {
	// Check for *http.Request (pointer to named type)
	if ptr, ok := t.(*types.Pointer); ok {
		if named, ok := ptr.Elem().(*types.Named); ok {
			pkg := named.Obj().Pkg()
			if pkg != nil && pkg.Path() == "net/http" && named.Obj().Name() == "Request" {
				return "*http.Request"
			}
		}
	}
	// Check for http.ResponseWriter (interface)
	if named, ok := t.(*types.Named); ok {
		pkg := named.Obj().Pkg()
		if pkg != nil && pkg.Path() == "net/http" && named.Obj().Name() == "ResponseWriter" {
			return "http.ResponseWriter"
		}
	}
	// Check for context.Context (interface)
	if named, ok := t.(*types.Named); ok {
		pkg := named.Obj().Pkg()
		if pkg != nil && pkg.Path() == "context" && named.Obj().Name() == "Context" {
			return "context.Context"
		}
	}
	return ""
}

// goTypeToHint maps a Go type to a SNGL type hint string.
func goTypeToHint(t types.Type) string {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch u.Kind() {
		case types.String:
			return "string"
		case types.Bool:
			return "bool"
		case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
			types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64:
			return "int"
		case types.Float32, types.Float64:
			return "float"
		default:
			return "dyn"
		}
	case *types.Slice:
		elem := goTypeToHint(u.Elem())
		return "list:" + elem
	case *types.Pointer:
		return goTypeToHint(u.Elem())
	default:
		// Check if it's a named type
		if named, ok := t.(*types.Named); ok {
			name := named.Obj().Name()
			pkg := named.Obj().Pkg()
			if pkg != nil {
				if pkg.Path() == "time" && name == "Time" {
					return "dateTime"
				}
				return pkg.Name() + "." + name // "ast.File" instead of "file"
			}
			return lowerFirst(name)
		}
		return "dyn"
	}
}

// lowerFirst converts a Go exported name to a SNGL-style lowercase name.
// All-uppercase names (like "ID") are fully lowered. Otherwise just the first
// letter is lowered.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	// All uppercase → all lowercase (e.g., "ID" → "id", "URL" → "url")
	if strings.ToUpper(s) == s {
		return strings.ToLower(s)
	}
	return strings.ToLower(s[:1]) + s[1:]
}
