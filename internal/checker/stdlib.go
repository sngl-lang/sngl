package checker

import (
	"embed"
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

//go:embed stdlib/*.sngl
var stdlibFS embed.FS

// StdlibFS returns the embedded stdlib filesystem for external consumers
// that need to read raw stdlib source files (e.g., tier comments).
func StdlibFS() embed.FS {
	return stdlibFS
}

// LoadStdlib parses the embedded stdlib files and returns the component
// schema registry, style property type map, unit definitions, stdlib functions,
// stdlib structs, and stdlib components (for abstract body expansion).
func LoadStdlib() (SchemaRegistry, map[string]StylePropSchema, []*ast.UnitDef, []*ast.FuncDef, []*ast.StructDef, []*ast.Component, error) {
	registry := SchemaRegistry{}
	styleProps := map[string]StylePropSchema{}
	var units []*ast.UnitDef
	var funcs []*ast.FuncDef
	var structs []*ast.StructDef
	var components []*ast.Component

	entries, err := stdlibFS.ReadDir("stdlib")
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("reading stdlib dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		f, err := stdlibFS.Open("stdlib/" + name)
		if err != nil {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("opening %s: %w", name, err)
		}

		var doc *ast.Document
		doc, err = parser.Parse(name, f)
		f.Close()
		if err != nil {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("parsing %s: %w", name, err)
		}

		for _, comp := range doc.Components {
			if strings.HasPrefix(comp.Name, "example_") {
				continue
			}
			registry[comp.Name] = componentToSchema(comp, doc.Comments)
			components = append(components, comp)
		}
		units = append(units, doc.Units...)
		for _, sd := range doc.Structs {
			// The "Style" struct defines valid style properties via its fields.
			if sd.Name == "Style" {
				for _, field := range sd.Fields {
					// Unwrap option<T> to get the inner type for validation.
					typeHint := field.Type
					if strings.HasPrefix(typeHint, "option:") {
						typeHint = typeHint[7:]
					}
					styleProps[field.Name] = StylePropSchema{
						Type: TypeFromHint(typeHint),
					}
				}
				continue // don't export Style as a user-visible struct
			}
			structs = append(structs, sd)
		}
		for _, fn := range doc.Functions {
			fn.IsStdlib = true
			funcs = append(funcs, fn)
		}
	}
	return registry, styleProps, units, funcs, structs, components, nil
}

func componentToSchema(comp *ast.Component, comments []ast.Comment) *ComponentSchema {
	schema := &ComponentSchema{
		Props:    make(map[string]PropSchema),
		Events:   make(map[string]string),
		Children: ChildrenNone, // default: no children
		Doc:      docForPos(comments, comp.Pos.Line),
	}
	for _, p := range comp.Params {
		schema.Props[p.Name] = PropSchema{
			Type: TypeFromHint(p.Default.TypeHint),
			Enum: p.Enum,
			Doc:  docForPos(comments, p.Pos.Line),
		}
	}
	for _, e := range comp.EventDecls {
		schema.Events[e.Name] = e.PayloadType
	}
	schema.Children = childrenFromType(comp.ChildrenType)
	if len(comp.Body) > 0 {
		schema.Body = comp.Body
	}
	if len(comp.PlatformBodies) > 0 {
		schema.PlatformBodies = comp.PlatformBodies
	}
	return schema
}

// docForPos returns the doc comment text for a declaration at the given line.
// It collects consecutive // comment lines immediately preceding the declaration.
func docForPos(comments []ast.Comment, line int) string {
	var docLines []string
	target := line - 1
	for i := len(comments) - 1; i >= 0; i-- {
		c := comments[i]
		if c.Pos.Line == target {
			text := strings.TrimPrefix(c.Text, "// ")
			text = strings.TrimPrefix(text, "//")
			text = strings.TrimSpace(text)
			if strings.HasPrefix(text, "---") {
				break
			}
			docLines = append([]string{text}, docLines...)
			target--
		} else if c.Pos.Line < target {
			break
		}
	}
	// Strip "Example:" sections and trailing blank lines.
	var out []string
	for _, l := range docLines {
		if l == "Example:" {
			break
		}
		out = append(out, l)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, " ")
}
