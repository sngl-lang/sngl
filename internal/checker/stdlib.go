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
// schema registry, style property type map, unit definitions, and stdlib functions.
func LoadStdlib() (SchemaRegistry, map[string]StylePropSchema, []*ast.UnitDef, []*ast.FuncDef, []*ast.StructDef, error) {
	registry := SchemaRegistry{}
	styleProps := map[string]StylePropSchema{}
	var units []*ast.UnitDef
	var funcs []*ast.FuncDef
	var structs []*ast.StructDef

	entries, err := stdlibFS.ReadDir("stdlib")
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("reading stdlib dir: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		f, err := stdlibFS.Open("stdlib/" + name)
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("opening %s: %w", name, err)
		}

		var doc *ast.Document
		doc, err = parser.Parse(name, f)
		f.Close()
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("parsing %s: %w", name, err)
		}

		for _, comp := range doc.Components {
			registry[comp.Name] = componentToSchema(comp, doc.Comments)
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
	return registry, styleProps, units, funcs, structs, nil
}

func componentToSchema(comp *ast.Component, comments []ast.Comment) *ComponentSchema {
	schema := &ComponentSchema{
		Props:    make(map[string]PropSchema),
		Events:   make(map[string]string),
		Children: ChildrenMany, // default
		Doc:      docForPos(comments, comp.Pos.Line),
	}
	for _, p := range comp.PropDecls {
		schema.Props[p.Name] = PropSchema{
			Type: TypeFromHint(p.TypeHint),
			Enum: p.Enum,
			Doc:  docForPos(comments, p.Pos.Line),
		}
	}
	for _, e := range comp.EventDecls {
		schema.Events[e.Name] = e.PayloadType
	}
	switch comp.ChildPolicy {
	case "none":
		schema.Children = ChildrenNone
	case "one":
		schema.Children = ChildrenOne
	case "many":
		schema.Children = ChildrenMany
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
	return strings.Join(docLines, " ")
}
