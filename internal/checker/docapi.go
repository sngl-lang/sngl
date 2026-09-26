package checker

import (
	"path/filepath"
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
	Doc    string
	Props  map[string]PropSchema
	Events map[string]string
	// Slots are in declaration order, unlike Props and Events: a slot list
	// reads as the author arranged it, and the rest slot conventionally ends it.
	Slots    []SlotSchema
	Children *ir.Type // nil = no children
}

// SlotSchema describes one slot of a component for documentation.
type SlotSchema struct {
	Name string
	// Type is the declaration's own type expression rather than a rendering of
	// the resolved ir.SlotDecl, so it documents what the author wrote: the
	// `...` marking the slot bare children land in, and the names on its
	// invocation parameters, which by-name matching makes contract. A caller
	// formats it with parser.FormatType.
	Type ast.TypeExpr
	// Rest says bare children go here. Derivable from Type, and stated so a
	// renderer need not re-read the type to group or order by it.
	Rest bool
}

type PropSchema struct {
	Type ir.Type
	Doc  string
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

// PackageSchema is the resolved API of one library package's exported
// components: the prop and event types a reader needs and the AST does not
// carry. Keyed by declaration name, which is unique within a package.
//
// Addressed by import path because that is the unit a declaration belongs to.
// The whole library merged into one document was the old answer, and it could
// not represent two packages declaring one name -- `time` is a type in
// sngl:time and a function in sngl:i18n -- nor say where anything came from.
//
// Empty for a package that does not load.
func PackageSchema(pkg string) SchemaRegistry {
	pkgSchemaMu.Lock()
	defer pkgSchemaMu.Unlock()
	if reg, ok := pkgSchemaCache[pkg]; ok {
		return reg
	}
	reg := buildSchemaRegistry(LibPackage(pkg), PackageSource(pkg))
	pkgSchemaCache[pkg] = reg
	return reg
}

// PackageExamples is the `_example_`-prefixed components of one library
// package, keyed by the declaration each documents. Each is written the way a
// program outside the package would write it: the package imported under its
// last path segment and every component it declares named through that. Only
// component names are qualified, so an example names the package's types,
// enums and funcs through an expected type or not at all.
func PackageExamples(pkg string) map[string][]string {
	docs := PackageSource(pkg)
	own := map[string]bool{}
	for _, doc := range docs {
		for _, stmt := range doc.Stmts {
			if comp, ok := stmt.(*ast.ComponentDecl); ok && comp.Target == nil {
				own[comp.Name] = true
			}
		}
	}
	path := "sngl:" + pkg
	alias := pkg[strings.LastIndex(pkg, "/")+1:]
	out := map[string][]string{}
	for _, doc := range docs {
		for name, comp := range prefixedExampleDecls(doc) {
			imports := []*ast.Import{{Path: path, Alias: alias}}
			for _, imp := range docImports(doc) {
				if !strings.HasPrefix(imp.Path, "sngl:internal/") {
					imports = append(imports, imp)
				}
			}
			imports, ui := withUI(imports)
			out[name] = append(out[name], exampleProgram(imports, ui, comp, alias, own))
		}
	}
	return out
}

var (
	pkgSchemaMu    sync.Mutex
	pkgSchemaCache = map[string]SchemaRegistry{}
)

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
			Slots:    slotSchemas(comp),
			Children: comp.ChildrenType,
		}
		for _, p := range comp.Props {
			schema.Props[p.Name] = PropSchema{
				Type: *p.Type,
			}
		}
		for _, e := range comp.Events {
			schema.Events[e.Name] = EventSignature(e)
		}
		reg[comp.Name] = schema
	}
	return reg
}

// slotSchemas reads a component's slots off the declaration it was built from.
// A component a plugin synthesized has no AST, so its slots are named from the
// IR and carry no type expression to render.
func slotSchemas(comp *ir.Component) []SlotSchema {
	if comp.AST == nil {
		out := make([]SlotSchema, 0, len(comp.Slots))
		for _, s := range comp.Slots {
			out = append(out, SlotSchema{Name: s.Name, Rest: s.Rest})
		}
		return out
	}
	var out []SlotSchema
	for _, p := range comp.AST.Props.Props {
		pd, ok := p.(ast.Param)
		if !ok {
			continue
		}
		if _, rest, isSlot := ast.SlotType(pd.Type); isSlot {
			out = append(out, SlotSchema{Name: pd.Name, Type: pd.Type, Rest: rest})
		}
	}
	return out
}

// PrefixedExamples extracts `_example_<name>` prefixed components from a
// document. Components named `_example_<name>` or `_example_<name>_<suffix>`
// map to <name>; the first example per name wins. Returns formatted source
// for each example: the document's imports and the example's body placed in
// a window, so the snippet is a complete, runnable app. A window body is not a
// component body, so an example declares no parameters and nests no component
// at its root. The leading underscore marks examples as unexported — they are
// not part of the public API but the doc tooling still extracts them from the
// AST for gallery rendering.
func PrefixedExamples(doc *ast.Document) map[string]string {
	imports, ui := withUI(docImports(doc))
	result := make(map[string]string)
	for name, comp := range prefixedExampleDecls(doc) {
		result[name] = exampleProgram(imports, ui, comp, "", nil)
	}
	return result
}

func prefixedExampleDecls(doc *ast.Document) map[string]*ast.ComponentDecl {
	result := make(map[string]*ast.ComponentDecl)
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
		if _, exists := result[target]; !exists {
			result[target] = comp
		}
	}
	return result
}

func docImports(doc *ast.Document) []*ast.Import {
	var out []*ast.Import
	for _, stmt := range doc.Stmts {
		if imp, ok := stmt.(*ast.Import); ok {
			out = append(out, imp)
		}
	}
	return out
}

// withUI returns imports with sngl:ui added when none of them names it, and
// the name a window is reached through under the result.
func withUI(imports []*ast.Import) ([]*ast.Import, string) {
	if ui := uiAliasFor(imports); ui != "" {
		return imports, ui
	}
	return append(imports, &ast.Import{Path: "sngl:ui", Alias: "ui"}), "ui"
}

// uiAliasFor is the name a window is reached through under imports: "" when
// nothing imports sngl:ui, and "." for a dot import.
func uiAliasFor(imports []*ast.Import) string {
	for _, imp := range imports {
		if imp.Path == "sngl:ui" {
			if imp.Alias == "" {
				return "ui"
			}
			return imp.Alias
		}
	}
	return ""
}

// exampleProgram builds a program from imports and a window holding comp's
// body. A node naming one of own is qualified with alias. The AST is shared
// once parsed, so every node on the path to a rewrite is copied.
func exampleProgram(imports []*ast.Import, ui string, comp *ast.ComponentDecl, alias string, own map[string]bool) string {
	var window ast.TargetExpr = &ast.IdentExpr{Name: "window"}
	if ui != "." {
		window = &ast.SelectExpr{Operand: &ast.IdentExpr{Name: ui}, Field: "window"}
	}
	head := make([]ast.Stmt, 0, len(imports))
	for _, imp := range imports {
		head = append(head, imp)
	}
	body := comp.Body
	body.Stmts = qualifyNodes(comp.Body.Stmts, alias, own)
	body.IsMultiline = true
	win := &ast.Document{Stmts: []ast.Stmt{&ast.VisualNode{Target: window, Block: body}}}
	return strings.TrimSpace(parser.Format(&ast.Document{Stmts: head})) + "\n\n" + strings.TrimSpace(parser.Format(win))
}

func qualifyNodes(stmts []ast.Stmt, alias string, own map[string]bool) []ast.Stmt {
	if len(own) == 0 {
		return stmts
	}
	out := make([]ast.Stmt, len(stmts))
	for i, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.VisualNode:
			n := *s
			if id, ok := s.Target.(*ast.IdentExpr); ok && own[id.Name] {
				n.Target = &ast.SelectExpr{Pos: id.Pos, Operand: &ast.IdentExpr{Pos: id.Pos, Name: alias}, Field: id.Name}
			}
			n.Block.Stmts = qualifyNodes(s.Block.Stmts, alias, own)
			out[i] = &n
		case *ast.CallStmt:
			id, ok := s.Call.Func.(*ast.IdentExpr)
			if !ok || !own[id.Name] {
				out[i] = stmt
				continue
			}
			call := *s.Call
			call.Func = &ast.SelectExpr{Pos: id.Pos, Operand: &ast.IdentExpr{Pos: id.Pos, Name: alias}, Field: id.Name}
			n := *s
			n.Call = &call
			out[i] = &n
		case *ast.IfStmt:
			n := *s
			n.Body.Stmts = qualifyNodes(s.Body.Stmts, alias, own)
			n.Else.Stmts = qualifyNodes(s.Else.Stmts, alias, own)
			out[i] = &n
		case *ast.ForStmt:
			n := *s
			n.Body.Stmts = qualifyNodes(s.Body.Stmts, alias, own)
			n.Else.Stmts = qualifyNodes(s.Else.Stmts, alias, own)
			out[i] = &n
		case *ast.ComponentDecl:
			n := *s
			n.Body.Stmts = qualifyNodes(s.Body.Stmts, alias, own)
			out[i] = &n
		default:
			out[i] = stmt
		}
	}
	return out
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
// PackageDocFile is the one file a package's prose is read from. Go's
// semantics -- every file's package comment counts, concatenated in load
// order -- cannot say which order, and the blank line that makes a comment run
// package prose is easy to leave in by accident: three files in lib/remote
// opened with a file header, and the package read as whichever the directory
// listed first. A stored file is the same accident made by a program, since
// every one opens with a "Code generated" header and gtk4 serves one as part of
// its package.
const PackageDocFile = "doc.sngl"

// DocumentFile is the base name of the file a parsed document came from. An
// *ast.Document does not carry one, so it is recovered from the first
// statement's position; a document with no statements came from nowhere.
func DocumentFile(doc *ast.Document) string {
	if len(doc.Stmts) == 0 {
		return ""
	}
	p := doc.Stmts[0].StmtPos()
	if p == nil {
		return ""
	}
	return filepath.Base(p.File)
}

// PackageDoc returns a document's package comment: a run of line comments
// starting at the top of a PackageDocFile and separated from what follows by a
// blank line. The blank line is what distinguishes it from a doc comment on the
// first declaration, which DeclDoc claims instead. Any other file's leading
// comment is an ordinary comment about that file.
func PackageDoc(doc *ast.Document) string {
	if DocumentFile(doc) != PackageDocFile {
		return ""
	}
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

// EventSignature is how an event's parameters read in documentation, hover
// and completion: the type alone for the one-parameter `@change T`, nothing
// for the loose bare `@tick`, and the parenthesised list otherwise.
func EventSignature(e *ir.EventDecl) string {
	if len(e.Params) == 1 && e.Params[0].Name == "" {
		if t := e.Params[0].Type; t != nil && t.Kind != ir.TypeDyn {
			return t.String()
		}
		return ""
	}
	parts := make([]string, len(e.Params))
	for i, p := range e.Params {
		parts[i] = p.Type.String()
		if p.Name != "" {
			parts[i] = p.Name + " " + parts[i]
		}
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
