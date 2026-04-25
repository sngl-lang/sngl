package golang

import (
	"fmt"
	goast "go/ast"
	"go/types"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
	"golang.org/x/tools/go/packages"
)

func init() {
	codegen.RegisterScheme(&GoImporter{})
}

// GoImporter resolves go:// scheme imports by loading Go packages
// and extracting exported types, functions, and variables.
type GoImporter struct{}

func (g *GoImporter) Scheme() string { return "go" }

func (g *GoImporter) Resolve(uri, dir string) (*ir.NativeImport, error) {
	userPath := strings.TrimSpace(strings.TrimPrefix(uri, "go://"))
	if userPath == "" {
		return nil, fmt.Errorf("go scheme requires a package path (e.g. go://github.com/foo/bar)")
	}

	cfg := &packages.Config{
		Mode: packages.NeedTypes | packages.NeedName | packages.NeedSyntax,
		Dir:  dir,
	}
	pkgs, err := packages.Load(cfg, userPath)
	if err != nil {
		return nil, fmt.Errorf("loading Go package %q: %w", userPath, err)
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no Go package found for %q", userPath)
	}
	if len(pkgs[0].Errors) > 0 {
		return nil, fmt.Errorf("loading %q: %s", userPath, pkgs[0].Errors[0].Msg)
	}

	// Canonical import path — the loader resolves "./foo" or module-relative
	// inputs to the full path. Field/return-type resolution compares against
	// named.Obj().Pkg().Path() which uses the canonical form, so homePkg
	// must too.
	pkgPath := pkgs[0].Types.Path()
	if pkgPath == "" {
		pkgPath = userPath
	}
	scope := pkgs[0].Types.Scope()
	pkgName := pkgs[0].Types.Name()

	// Scan syntax for //sngl:pure annotations and doc comments. Go doc strings
	// are attached either to the enclosing GenDecl (single-spec declarations) or
	// to the individual Spec (grouped declarations); we fall back accordingly.
	funcPure := map[string]bool{}
	funcDoc := map[string]string{}
	typeDoc := map[string]string{}
	varDoc := map[string]string{}
	for _, file := range pkgs[0].Syntax {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *goast.FuncDecl:
				if d.Recv != nil {
					continue
				}
				if d.Doc != nil {
					for _, c := range d.Doc.List {
						if strings.Contains(c.Text, "sngl:pure") {
							funcPure[d.Name.Name] = true
						}
					}
					funcDoc[d.Name.Name] = d.Doc.Text()
				}
			case *goast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *goast.TypeSpec:
						doc := s.Doc.Text()
						if doc == "" {
							doc = d.Doc.Text()
						}
						if doc != "" {
							typeDoc[s.Name.Name] = doc
						}
					case *goast.ValueSpec:
						doc := s.Doc.Text()
						if doc == "" {
							doc = d.Doc.Text()
						}
						if doc == "" {
							continue
						}
						for _, n := range s.Names {
							varDoc[n.Name] = doc
						}
					}
				}
			}
		}
	}

	ni := &ir.NativeImport{ImportPath: pkgPath}

	// First pass: register struct declarations so recursive/forward refs on
	// field and function signatures can resolve to the in-package types.
	structs := map[string]*ir.StructDef{}
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		tn, ok := obj.(*types.TypeName)
		if !ok {
			continue
		}
		if _, ok := tn.Type().Underlying().(*types.Struct); !ok {
			continue
		}
		sd := &ir.StructDef{
			Name:   tn.Name(),
			Native: pkgName + "." + tn.Name(),
			Doc:    typeDoc[tn.Name()],
		}
		structs[tn.Name()] = sd
		ni.Structs = append(ni.Structs, sd)
	}

	// Second pass: populate struct fields, funcs, and vars with fully
	// resolved ir.Type references.
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		switch o := obj.(type) {
		case *types.TypeName:
			if sd, ok := structs[o.Name()]; ok {
				populateStructFields(sd, o, pkgPath, structs)
			}
		case *types.Func:
			if fn := goFuncToFunc(o, pkgPath, pkgName, structs); fn != nil {
				fn.Purity = ir.PurityUnknown
				if funcPure[o.Name()] {
					fn.Purity = ir.PurityPure
				}
				fn.Doc = funcDoc[o.Name()]
				ni.Funcs = append(ni.Funcs, fn)
			}
		case *types.Var:
			v := goVarToVar(o, pkgPath, pkgName, structs)
			v.Doc = varDoc[o.Name()]
			ni.Vars = append(ni.Vars, v)
		}
	}

	return ni, nil
}

// populateStructFields fills sd.Fields from the Go struct type. Fields whose
// Go type cannot be modelled precisely are still included (with TypDyn), but
// marked Unusable so the checker rejects direct access.
func populateStructFields(sd *ir.StructDef, tn *types.TypeName, homePkg string, structs map[string]*ir.StructDef) {
	st, ok := tn.Type().Underlying().(*types.Struct)
	if !ok {
		return
	}
	for f := range st.Fields() {
		if !f.Exported() {
			continue
		}
		t, usable := goTypeToIR(f.Type(), homePkg, structs)
		sf := &ir.StructField{
			Name:       lowerFirst(f.Name()),
			Type:       t,
			NativeName: f.Name(),
		}
		if !usable {
			sf.Unusable = fmt.Sprintf("field %s.%s has type not representable in SNGL", tn.Name(), f.Name())
		}
		sd.Fields = append(sd.Fields, sf)
	}
}

// goFuncToFunc converts a Go function to an *ir.Func.
//
//   - A leading context.Context / *http.Request / http.ResponseWriter param is
//     stripped and HasContextArg is recorded for codegen to re-inject.
//   - A trailing error return is stripped and HasErrorReturn is recorded so
//     codegen can adapt the call (check + unwrap).
//   - Shapes SNGL cannot model precisely (multi-value returns beyond (T, error),
//     parameter or return types that would otherwise collapse to dyn) produce
//     a declaration with Unusable set. The declaration is retained so
//     namespace lookup succeeds, but the checker rejects references.
func goFuncToFunc(fn *types.Func, pkgPath, pkgName string, structs map[string]*ir.StructDef) *ir.Func {
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return nil
	}

	f := &ir.Func{
		Name:       fn.Name(),
		NativePkg:  pkgPath,
		NativeName: pkgName + "." + fn.Name(),
	}

	params := sig.Params()
	stripping := true
	i := 0
	for v := range params.Variables() {
		if stripping {
			if hp := detectHiddenParam(v.Type()); hp != "" {
				if hp == "context.Context" {
					f.HasContextArg = true
				}
				i++
				continue
			}
			stripping = false
		}
		t, usable := goTypeToIR(v.Type(), pkgPath, structs)
		name := v.Name()
		if name == "" {
			name = fmt.Sprintf("arg%d", i)
		}
		if !usable && f.Unusable == "" {
			f.Unusable = fmt.Sprintf("parameter %q has type not representable in SNGL", name)
		}
		f.Params = append(f.Params, &ir.Param{Name: name, Type: t})
		i++
	}

	results := sig.Results()
	switch results.Len() {
	case 0:
		// void
	case 1:
		t, usable := goTypeToIR(results.At(0).Type(), pkgPath, structs)
		if !usable && f.Unusable == "" {
			f.Unusable = "return type not representable in SNGL"
		}
		f.Return = t
	case 2:
		if isErrorType(results.At(1).Type()) {
			t, usable := goTypeToIR(results.At(0).Type(), pkgPath, structs)
			if !usable && f.Unusable == "" {
				f.Unusable = "return type not representable in SNGL"
			}
			f.Return = t
			f.HasErrorReturn = true
		} else if f.Unusable == "" {
			f.Unusable = "functions returning multiple values are not supported"
		}
	default:
		if f.Unusable == "" {
			f.Unusable = "functions returning multiple values are not supported"
		}
	}

	return f
}

func goVarToVar(v *types.Var, pkgPath, pkgName string, structs map[string]*ir.StructDef) *ir.Var {
	t, usable := goTypeToIR(v.Type(), pkgPath, structs)
	out := &ir.Var{
		Name:       v.Name(),
		Type:       t,
		NativePkg:  pkgPath,
		NativeName: pkgName + "." + v.Name(),
	}
	if !usable {
		out.Unusable = fmt.Sprintf("variable %s.%s has type not representable in SNGL", pkgName, v.Name())
	}
	return out
}

// isErrorType reports whether t is the builtin error interface.
func isErrorType(t types.Type) bool {
	return types.Identical(t, types.Universe.Lookup("error").Type())
}

// detectHiddenParam checks if a type is http.ResponseWriter, *http.Request,
// or context.Context, returning the Go type string if so.
func detectHiddenParam(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		if named, ok := ptr.Elem().(*types.Named); ok {
			pkg := named.Obj().Pkg()
			if pkg != nil && pkg.Path() == "net/http" && named.Obj().Name() == "Request" {
				return "*http.Request"
			}
		}
	}
	if named, ok := t.(*types.Named); ok {
		pkg := named.Obj().Pkg()
		if pkg != nil && pkg.Path() == "net/http" && named.Obj().Name() == "ResponseWriter" {
			return "http.ResponseWriter"
		}
	}
	if named, ok := t.(*types.Named); ok {
		pkg := named.Obj().Pkg()
		if pkg != nil && pkg.Path() == "context" && named.Obj().Name() == "Context" {
			return "context.Context"
		}
	}
	return ""
}

// goTypeToIR maps a Go type to an *ir.Type and reports whether it is usable
// in SNGL. homePkg is the path of the package currently being imported.
//
// Usable results include the primitive scalars, slices of usable elements,
// named struct types in the home package, the stdlib time.Time → DateTime
// mapping, and any Go interface (exposed as explicit dyn). All other shapes
// — maps, channels, functions, cross-package struct refs — return
// (TypDyn, false); callers use the bool to mark the enclosing declaration
// Unusable.
func goTypeToIR(t types.Type, homePkg string, structs map[string]*ir.StructDef) (*ir.Type, bool) {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch u.Kind() {
		case types.String:
			return ir.TypString, true
		case types.Bool:
			return ir.TypBool, true
		case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
			types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64:
			return ir.TypInt, true
		case types.Float32, types.Float64:
			return ir.TypFloat, true
		default:
			return ir.TypDyn, false
		}
	case *types.Slice:
		elem, ok := goTypeToIR(u.Elem(), homePkg, structs)
		if !ok {
			return ir.TypDyn, false
		}
		return ir.ListOf(elem), true
	case *types.Pointer:
		return goTypeToIR(u.Elem(), homePkg, structs)
	case *types.Interface:
		return ir.TypDyn, true
	default:
		if named, ok := t.(*types.Named); ok {
			name := named.Obj().Name()
			pkg := named.Obj().Pkg()
			if pkg != nil {
				if pkg.Path() == "time" && name == "Time" {
					return ir.TypDateTime, true
				}
				if _, isStruct := named.Underlying().(*types.Struct); isStruct && pkg.Path() == homePkg {
					if sd, ok := structs[name]; ok {
						return sd.SymType(), true
					}
				}
			}
		}
		return ir.TypDyn, false
	}
}

// lowerFirst converts a Go exported name to a SNGL-style lowercase name.
// All-uppercase names (like "ID") are fully lowered. Otherwise just the first
// letter is lowered.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	if strings.ToUpper(s) == s {
		return strings.ToLower(s)
	}
	return strings.ToLower(s[:1]) + s[1:]
}
