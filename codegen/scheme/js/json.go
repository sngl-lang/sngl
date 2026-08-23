package js

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// loadJSON reads a .json file and synthesises a SNGL IR var named after
// the file's basename, typed as a struct (object), list, or primitive
// matching the JSON shape. abs is the resolved virtual path (used in
// diagnostics + ast.Pos); rel is the io/fs.FS path used for reading.
func loadJSON(spec, abs, rel string, fsys fs.FS) (*ir.NativeImport, error) {
	data, err := fs.ReadFile(fsys, rel)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", rel, err)
	}
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", rel, err)
	}

	base := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	rootName := upperFirst(sanitizeIdent(base))
	if rootName == "" {
		rootName = "JSON"
	}

	pos := ast.Pos{File: abs, Line: 1, Column: 1}
	g := jsonGen{path: abs, pos: pos, used: map[string]int{}}
	t, structs := g.infer(raw, rootName)

	v := &ir.Var{
		Name:       sanitizeIdent(base),
		Type:       t,
		IsConst:    true,
		NativePkg:  spec,
		NativeName: "default",
	}
	return &ir.NativeImport{
		ImportPath: spec,
		Structs:    structs,
		Vars:       []*ir.Var{v},
	}, nil
}

type jsonGen struct {
	path    string
	pos     ast.Pos
	used    map[string]int
	structs []*ir.StructDef
}

func (g *jsonGen) infer(v any, name string) (*ir.Type, []*ir.StructDef) {
	t := g.inferType(v, name)
	return t, g.structs
}

func (g *jsonGen) inferType(v any, name string) *ir.Type {
	switch x := v.(type) {
	case nil:
		return ir.TypNull
	case bool:
		return ir.TypBool
	case float64:
		if x == float64(int64(x)) {
			return ir.TypInt
		}
		return ir.TypFloat
	case string:
		return ir.TypString
	case []any:
		if len(x) == 0 {
			return ir.ListOf(ir.TypDyn)
		}
		elemType := g.inferType(x[0], singularize(name))
		return ir.ListOf(elemType)
	case map[string]any:
		sd := &ir.StructDef{
			Name:   g.uniqueName(name),
			Native: name,
			Origin: jsTypeID{Module: g.path, Name: name},
			AST:    &ast.StructDef{Pos: g.pos, Name: name},
		}
		g.structs = append(g.structs, sd)
		for key, val := range x {
			fieldType := g.inferType(val, upperFirst(sanitizeIdent(key)))
			sd.Fields = append(sd.Fields, &ir.StructField{
				Name:       sanitizeIdent(key),
				Type:       fieldType,
				NativeName: key,
			})
		}
		return sd.SymType()
	}
	return ir.TypDyn
}

func (g *jsonGen) uniqueName(name string) string {
	if g.used[name] == 0 {
		g.used[name] = 1
		return name
	}
	g.used[name]++
	return fmt.Sprintf("%s%d", name, g.used[name])
}

func sanitizeIdent(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 {
				b.WriteRune('_')
			}
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func singularize(name string) string {
	if strings.HasSuffix(name, "ies") && len(name) > 3 {
		return name[:len(name)-3] + "y"
	}
	if strings.HasSuffix(name, "s") && len(name) > 1 {
		return name[:len(name)-1]
	}
	return name + "Item"
}
