package checker

import (
	"strings"
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// --- Doc API types ---

// ComponentSchema describes a stdlib component for documentation.
type ComponentSchema struct {
	Doc      string
	Props    map[string]PropSchema
	Events   map[string]string
	Children *ir.Type // nil = no children
}

// PropSchema describes a single component property.
type PropSchema struct {
	Type ir.Type
	Doc  string
	Enum []string
}

// StylePropSchema describes a style property.
type StylePropSchema struct {
	Type ir.Type
	Enum []string
}

// SchemaRegistry maps component names to their schemas.
type SchemaRegistry = map[string]*ComponentSchema

// PackageDocs holds extracted documentation for all declarations in a package.
type PackageDocs struct {
	Components []DeclInfo
	Structs    []DeclInfo
	Enums      []DeclInfo
	Consts     []DeclInfo
	Data       []DeclInfo
	Functions  []DeclInfo
}

// FindDecl searches all declaration categories for the given name.
func (pd *PackageDocs) FindDecl(name string) *DeclInfo {
	for i := range pd.Components {
		if pd.Components[i].Name == name {
			return &pd.Components[i]
		}
	}
	for i := range pd.Structs {
		if pd.Structs[i].Name == name {
			return &pd.Structs[i]
		}
	}
	for i := range pd.Enums {
		if pd.Enums[i].Name == name {
			return &pd.Enums[i]
		}
	}
	for i := range pd.Consts {
		if pd.Consts[i].Name == name {
			return &pd.Consts[i]
		}
	}
	for i := range pd.Data {
		if pd.Data[i].Name == name {
			return &pd.Data[i]
		}
	}
	for i := range pd.Functions {
		if pd.Functions[i].Name == name {
			return &pd.Functions[i]
		}
	}
	return nil
}

// DeclInfo holds metadata for a single declaration.
type DeclInfo struct {
	Name string
	Doc  string
	Decl ast.Stmt // the AST node
}

// --- Public functions ---

var (
	stdlibSchemaOnce     sync.Once
	stdlibSchemaRegistry SchemaRegistry
	stdlibStyleProps     map[string]StylePropSchema
	stdlibSchemaErr      error
)

// LoadStdlib returns the stdlib component schemas and style property schemas.
func LoadStdlib() (SchemaRegistry, map[string]StylePropSchema, error) {
	stdlibSchemaOnce.Do(func() {
		docs := StdlibDocs()
		if len(docs) == 0 {
			stdlibSchemaErr = nil
			stdlibSchemaRegistry = SchemaRegistry{}
			stdlibStyleProps = map[string]StylePropSchema{}
			return
		}

		// Merge all stdlib docs into one for checking.
		merged := &ast.Document{}
		for _, d := range docs {
			merged.Stmts = append(merged.Stmts, d.Stmts...)
		}

		pkg, _ := Check(merged, &Config{})
		stdlibSchemaRegistry = buildSchemaRegistry(pkg, docs)
		stdlibStyleProps = map[string]StylePropSchema{}
	})
	return stdlibSchemaRegistry, stdlibStyleProps, stdlibSchemaErr
}

func buildSchemaRegistry(pkg *ir.Package, docs []*ast.Document) SchemaRegistry {
	reg := SchemaRegistry{}
	if pkg == nil {
		return reg
	}
	// Collect all comments from stdlib docs for DeclDoc lookups.
	var allStmts []ast.Stmt
	for _, d := range docs {
		allStmts = append(allStmts, d.Stmts...)
	}

	for _, comp := range pkg.Components {
		schema := &ComponentSchema{
			Doc:      DeclDoc(allStmts),
			Props:    make(map[string]PropSchema),
			Events:   make(map[string]string),
			Children: comp.ChildrenType,
		}
		for _, p := range comp.Props {
			schema.Props[p.Name] = PropSchema{
				Type: *p.Type,
			}
		}
		for _, e := range comp.Events {
			payload := ""
			if e.Type != nil {
				payload = e.Type.String()
			}
			schema.Events[e.Name] = payload
		}
		reg[comp.Name] = schema
	}
	return reg
}

// StdlibExamples extracts example source blocks from stdlib doc comments.
// Returns a map from component name to list of example sources.
func StdlibExamples() (map[string][]string, error) {
	docs := StdlibDocs()
	result := make(map[string][]string)
	for _, doc := range docs {
		for name, srcs := range PrefixedExamples(doc) {
			result[name] = append(result[name], srcs)
		}
	}
	return result, nil
}

// PrefixedExamples extracts example source blocks from doc comments in a document.
// Returns a map from declaration name to example source.
// Examples are defined as:
//
//	// Example:
//	//
//	//   component main { ... }
//	component MyButton(...) { ... }
func PrefixedExamples(doc *ast.Document) map[string]string {
	result := make(map[string]string)
	stmts := doc.Stmts

	for i, stmt := range stmts {
		comp, ok := stmt.(*ast.ComponentDecl)
		if !ok {
			continue
		}
		// Look backward for doc comment with Example: block.
		example := extractExample(stmts[:i])
		if example != "" {
			result[comp.Name] = example
		}
	}
	return result
}

// extractExample looks backward from a set of stmts for an Example: block in comments.
func extractExample(stmts []ast.Stmt) string {
	// Collect trailing comments (reading backward).
	var comments []*ast.Comment
	for i := len(stmts) - 1; i >= 0; i-- {
		c, ok := stmts[i].(*ast.Comment)
		if !ok {
			break
		}
		comments = append([]*ast.Comment{c}, comments...)
	}

	var inExample bool
	var lines []string
	for _, c := range comments {
		text := strings.TrimPrefix(c.Text, "//")
		text = strings.TrimPrefix(text, " ")
		if text == "Example:" {
			inExample = true
			continue
		}
		if inExample {
			// Dedent by 2 spaces (common example indentation).
			if strings.HasPrefix(text, "  ") {
				text = text[2:]
			}
			lines = append(lines, text)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// ExtractPackageDocs walks a document's statements and returns doc info for each declaration.
func ExtractPackageDocs(doc *ast.Document) *PackageDocs {
	pd := &PackageDocs{}
	stmts := doc.Stmts

	for i, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ComponentDecl:
			pd.Components = append(pd.Components, DeclInfo{
				Name: s.Name,
				Doc:  DeclDoc(stmts[:i]),
				Decl: s,
			})
		case *ast.StructDef:
			pd.Structs = append(pd.Structs, DeclInfo{
				Name: s.Name,
				Doc:  DeclDoc(stmts[:i]),
				Decl: s,
			})
		case *ast.EnumDef:
			pd.Enums = append(pd.Enums, DeclInfo{
				Name: s.Name,
				Doc:  DeclDoc(stmts[:i]),
				Decl: s,
			})
		case *ast.ConstDecl:
			for _, spec := range s.Specs {
				for _, name := range spec.Names {
					pd.Consts = append(pd.Consts, DeclInfo{
						Name: name,
						Doc:  DeclDoc(stmts[:i]),
						Decl: s,
					})
				}
			}
		case *ast.VarDecl:
			for _, spec := range s.Specs {
				for _, name := range spec.Names {
					pd.Data = append(pd.Data, DeclInfo{
						Name: name,
						Doc:  DeclDoc(stmts[:i]),
						Decl: s,
					})
				}
			}
		case *ast.FuncDef:
			pd.Functions = append(pd.Functions, DeclInfo{
				Name: s.Name,
				Doc:  DeclDoc(stmts[:i]),
				Decl: s,
			})
		}
	}
	return pd
}

// DeclDoc extracts the doc comment text for a declaration at the given line.
// It searches backward through stmts for consecutive comment lines immediately
// preceding the declaration.
func DeclDoc(stmts []ast.Stmt) string {
	// Collect comments immediately preceding declLine.
	var lines []string
	for i := len(stmts) - 1; i >= 0; i-- {
		c, ok := stmts[i].(*ast.Comment)
		if !ok {
			break
		}
		if c.Block || c.Inline {
			break
		}
		text := strings.TrimPrefix(c.Text, "//")
		text = strings.TrimPrefix(text, " ")
		lines = append([]string{text}, lines...)
	}
	return strings.TrimSpace(strings.Join(lines, " "))
}
