package checker

import (
	"slices"
	"strings"
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// ComponentSchema describes a stdlib component for documentation.
// Only exported components appear in the schema registry.
type ComponentSchema struct {
	Doc      string
	Props    map[string]PropSchema
	Events   map[string]string
	Children *ir.Type // nil = no children
}

type PropSchema struct {
	Type ir.Type
	Doc  string
	Enum []string
}

type StylePropSchema struct {
	Type ir.Type
	Enum []string
}

type SchemaRegistry = map[string]*ComponentSchema

type PackageDocs struct {
	// Doc is the package comment, from the first file that carries one.
	Doc string

	Components []DeclInfo
	Structs    []DeclInfo
	Enums      []DeclInfo
	Consts     []DeclInfo
	Data       []DeclInfo
	Functions  []DeclInfo
}

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

// DeclInfo holds metadata for a single declaration. ExtractPackageDocs
// only returns DeclInfos for exported decls.
type DeclInfo struct {
	Name string
	Doc  string
	Decl ast.Stmt // the AST node
}

var (
	stdlibSchemaOnce     sync.Once
	stdlibSchemaRegistry SchemaRegistry
	stdlibStyleProps     map[string]StylePropSchema
	stdlibSchemaErr      error
	stdlibIRPackage      *ir.Package
)

func LoadStdlib() (SchemaRegistry, map[string]StylePropSchema, error) {
	stdlibSchemaOnce.Do(func() {
		docs := StdlibDocs()
		if len(docs) == 0 {
			stdlibSchemaErr = nil
			stdlibSchemaRegistry = SchemaRegistry{}
			stdlibStyleProps = map[string]StylePropSchema{}
			return
		}

		merged := &ast.Document{}
		for _, d := range docs {
			merged.Stmts = append(merged.Stmts, d.Stmts...)
		}

		// lib/ checked as the document, not loaded as a package.
		pkg, _ := Check(merged, &Config{libSource: true})
		stdlibIRPackage = pkg
		stdlibSchemaRegistry = buildSchemaRegistry(pkg, docs)
		stdlibStyleProps = map[string]StylePropSchema{}
	})
	return stdlibSchemaRegistry, stdlibStyleProps, stdlibSchemaErr
}

// StdlibIRPackage returns the type-checked IR package for the stdlib. Useful
// when callers need resolved types (e.g. function return types) that aren't
// preserved in the parsed AST.
func StdlibIRPackage() *ir.Package {
	_, _, _ = LoadStdlib()
	return stdlibIRPackage
}

func buildSchemaRegistry(pkg *ir.Package, docs []*ast.Document) SchemaRegistry {
	reg := SchemaRegistry{}
	if pkg == nil {
		return reg
	}

	// Index per-component doc strings by walking each source doc independently
	// so `stmts[:i]` bounds the comment search to what precedes each decl.
	compDocs := map[string]string{}
	for _, d := range docs {
		for i, stmt := range d.Stmts {
			cd, ok := stmt.(*ast.ComponentDecl)
			if !ok {
				continue
			}
			if _, seen := compDocs[cd.Name]; seen {
				continue
			}
			compDocs[cd.Name] = DeclDoc(d.Stmts[:i], cd.Pos.Line)
		}
	}

	for _, comp := range pkg.Components {
		if !comp.IsExported() {
			continue
		}
		schema := &ComponentSchema{
			Doc:      compDocs[comp.Name],
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

// PrefixedExamples extracts `_example_<name>` prefixed components from a
// document. Components named `_example_<name>` or `_example_<name>_<suffix>`
// map to <name>; the first example per name wins. Returns formatted source
// for each example, with the wrapper renamed to `main` so the snippet is a
// complete, runnable app. The leading underscore marks examples as
// unexported — they are not part of the public API but the doc tooling
// still extracts them from the AST for gallery rendering.
func PrefixedExamples(doc *ast.Document) map[string]string {
	result := make(map[string]string)
	for _, stmt := range doc.Stmts {
		comp, ok := stmt.(*ast.ComponentDecl)
		if !ok {
			continue
		}
		target, ok := strings.CutPrefix(comp.Name, "_example_")
		if !ok {
			continue
		}
		if i := strings.Index(target, "_"); i >= 0 {
			target = target[:i]
		}
		if _, exists := result[target]; exists {
			continue
		}
		display := *comp
		display.Name = "main"
		exDoc := &ast.Document{Stmts: []ast.Stmt{&display}}
		result[target] = strings.TrimSpace(parser.Format(exDoc))
	}
	return result
}

// ExtractPackageDocs walks a document's statements and returns doc info
// for each exported declaration. Unexported decls are dropped so that
// callers never have to know or enforce the export rule themselves — it
// lives on the IR decl types' IsExported methods.
func ExtractPackageDocs(doc *ast.Document) *PackageDocs {
	pd := &PackageDocs{Doc: PackageDoc(doc)}
	stmts := doc.Stmts

	for i, stmt := range stmts {
		line := 0
		if p := stmt.StmtPos(); p != nil {
			line = p.Line
		}
		switch s := stmt.(type) {
		case *ast.ComponentDecl:
			if !(&ir.Component{Name: s.Name}).IsExported() {
				continue
			}
			pd.Components = append(pd.Components, DeclInfo{
				Name: s.Name,
				Doc:  DeclDoc(stmts[:i], line),
				Decl: s,
			})
		case *ast.StructDef:
			if !(&ir.StructDef{Name: s.Name}).IsExported() {
				continue
			}
			pd.Structs = append(pd.Structs, DeclInfo{
				Name: s.Name,
				Doc:  DeclDoc(stmts[:i], line),
				Decl: s,
			})
		case *ast.EnumDef:
			if !(&ir.EnumDef{Name: s.Name}).IsExported() {
				continue
			}
			pd.Enums = append(pd.Enums, DeclInfo{
				Name: s.Name,
				Doc:  DeclDoc(stmts[:i], line),
				Decl: s,
			})
		case *ast.ConstDecl:
			for _, spec := range s.Specs {
				for _, name := range spec.Names {
					if !(&ir.Var{Name: name, IsConst: true}).IsExported() {
						continue
					}
					pd.Consts = append(pd.Consts, DeclInfo{
						Name: name,
						Doc:  DeclDoc(stmts[:i], line),
						Decl: s,
					})
				}
			}
		case *ast.VarDecl:
			for _, spec := range s.Specs {
				for _, name := range spec.Names {
					if !(&ir.Var{Name: name}).IsExported() {
						continue
					}
					pd.Data = append(pd.Data, DeclInfo{
						Name: name,
						Doc:  DeclDoc(stmts[:i], line),
						Decl: s,
					})
				}
			}
		case *ast.FuncDef:
			recv, method, _ := ast.SplitMethodName(s.Name)
			if !(&ir.Func{Receiver: recv, Name: method}).IsExported() {
				continue
			}
			pd.Functions = append(pd.Functions, DeclInfo{
				Name: s.Name,
				Doc:  DeclDoc(stmts[:i], line),
				Decl: s,
			})
		}
	}
	return pd
}

// DeclDoc extracts the doc comment text for a declaration at declLine.
// It searches backward through stmts for consecutive comment lines immediately
// preceding the declaration. A blank line (non-adjacent Pos.Line) breaks the
// group — so section headers like `// --- Core ---` or inner-struct comments
// separated from a decl by a blank line are not folded into the decl's doc.
// A declLine of 0 disables the adjacency check against the decl itself.
// PackageDoc returns a document's package comment: a run of line comments
// starting at the top of the file and separated from what follows by a blank
// line. The blank line is what distinguishes it from a doc comment on the
// first declaration, which DeclDoc claims instead.
func PackageDoc(doc *ast.Document) string {
	var lines []string
	last := 0
	i := 0
	for ; i < len(doc.Stmts); i++ {
		c, ok := doc.Stmts[i].(*ast.Comment)
		if !ok || c.Block || c.Inline {
			break
		}
		if last != 0 && c.Pos.Line != last+1 {
			break // blank line: the run ends here
		}
		last = c.Pos.Line
		text := strings.TrimPrefix(c.Text, "//")
		lines = append(lines, strings.TrimPrefix(text, " "))
	}
	if len(lines) == 0 {
		return ""
	}
	// Whatever follows must be separated by a blank line. Without one the run
	// is a doc comment on the declaration below it, which DeclDoc claims.
	if i < len(doc.Stmts) {
		if next := doc.Stmts[i].StmtPos(); next == nil || next.Line <= last+1 {
			return ""
		}
	}
	return strings.TrimSpace(strings.Join(lines, " "))
}

func DeclDoc(stmts []ast.Stmt, declLine int) string {
	var lines []string
	nextLine := declLine
	for _, stmt := range slices.Backward(stmts) {
		c, ok := stmt.(*ast.Comment)
		if !ok {
			break
		}
		if c.Block || c.Inline {
			break
		}
		if nextLine != 0 && c.Pos.Line+1 < nextLine {
			break
		}
		nextLine = c.Pos.Line
		text := strings.TrimPrefix(c.Text, "//")
		text = strings.TrimPrefix(text, " ")
		lines = append([]string{text}, lines...)
	}
	return strings.TrimSpace(strings.Join(lines, " "))
}
