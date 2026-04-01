package golang

import (
	"fmt"
	"go/types"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
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

func (g *GoImporter) Resolve(uri, dir string) (*ast.NativeDecls, error) {
	pkgPath := strings.TrimPrefix(uri, "go://")

	cfg := &packages.Config{
		Mode: packages.NeedTypes | packages.NeedName,
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

	decls := &ast.NativeDecls{}
	scope := pkgs[0].Types.Scope()

	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		switch o := obj.(type) {
		case *types.TypeName:
			if sd := goTypeToStruct(o); sd != nil {
				decls.Structs = append(decls.Structs, sd)
			}
		case *types.Func:
			if d := goFuncToData(o); d != nil {
				decls.Data = append(decls.Data, d)
			}
		case *types.Var:
			decls.Data = append(decls.Data, &ast.Data{
				Name:   o.Name(),
				Extern: true,
				Init:   ast.Expr{TypeHint: goTypeToHint(o.Type())},
			})
		}
	}

	return decls, nil
}

// goTypeToStruct converts a Go named struct type to an ast.StructDef.
func goTypeToStruct(tn *types.TypeName) *ast.StructDef {
	st, ok := tn.Type().Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	sd := &ast.StructDef{Name: tn.Name()}
	for f := range st.Fields() {
		f := f
		if !f.Exported() {
			continue
		}
		sd.Fields = append(sd.Fields, &ast.StructField{
			Name: lowerFirst(f.Name()),
			Type: goTypeToHint(f.Type()),
		})
	}
	return sd
}

// goFuncToData converts a Go function to an ast.Data with extern/func flags.
func goFuncToData(fn *types.Func) *ast.Data {
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return nil
	}

	var paramTypes []string
	params := sig.Params()
	for v := range params.Variables() {
		paramTypes = append(paramTypes, goTypeToHint(v.Type()))
	}

	var returnType string
	results := sig.Results()
	if results.Len() == 1 {
		returnType = goTypeToHint(results.At(0).Type())
	}

	// Build a type hint for the func type
	hint := "func"
	if len(paramTypes) > 0 {
		hint += ":" + strings.Join(paramTypes, ":")
	}
	if returnType != "" {
		hint += "~" + returnType
	}

	return &ast.Data{
		Name:       fn.Name(),
		Extern:     true,
		IsFunc:     true,
		ParamTypes: paramTypes,
		ReturnType: returnType,
		Init:       ast.Expr{TypeHint: hint},
	}
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
			if pkg != nil && pkg.Path() == "time" && name == "Time" {
				return "dateTime"
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
