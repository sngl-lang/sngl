package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/docs"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/docbrowser"
	"git.duckfam.us/jonathan/sngl/internal/docsite"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
	"github.com/charmbracelet/glamour"
	"github.com/spf13/cobra"
)

var docCmd = &cobra.Command{
	Use:   "doc [dir|topic] [decl]",
	Short: "Show SNGL documentation",
	Long: `Show SNGL documentation, similar to go doc.

  sngl doc                 Show stdlib component index
  sngl doc .               Index declarations in the current directory
  sngl doc . MyComponent   Show docs for a specific declaration
  sngl doc sngl            Index the standard library
  sngl doc sngl button     Show a stdlib declaration
  sngl doc button          Show stdlib component reference
  sngl doc build           Build the HTML documentation site`,
	Args: cobra.MaximumNArgs(2),
	RunE: runDoc,
}

var docBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build the documentation site",
	RunE:  runDocBuild,
}

var docServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Build and serve the documentation site locally",
	RunE:  runDocServe,
}

func init() {
	docBuildCmd.Flags().StringP("out", "o", "_site", "output directory")

	docServeCmd.Flags().StringP("out", "o", "_site", "output directory")
	docServeCmd.Flags().IntP("port", "p", 8080, "port to serve on")

	docCmd.Flags().String("http", "", "start doc server at address (e.g., :3680)")
	docCmd.Flags().Bool("tui", false, "launch interactive TUI documentation browser")

	docCmd.AddCommand(docBuildCmd)
	docCmd.AddCommand(docServeCmd)
}

func runDoc(cmd *cobra.Command, args []string) error {
	httpAddr, _ := cmd.Flags().GetString("http")

	// --http mode: serve docs dynamically
	if httpAddr != "" {
		dir := "."
		if len(args) > 0 {
			dir = args[0]
		}
		return serveDocHTTP(httpAddr, dir)
	}

	tui, _ := cmd.Flags().GetBool("tui")
	if tui || len(args) == 0 {
		return docbrowser.RunWithDir(".")
	}

	first := args[0]

	// Try to resolve the target as if it were an `import` path (stdlib keyword,
	// scheme URI, directory, or an alias defined in the CWD's imports).
	dt, err := resolveDocTarget(first, ".")
	if err != nil && !errors.Is(err, errDocTargetNotFound) {
		return err
	}
	if dt != nil {
		if len(args) >= 2 {
			// Preserve the pre-existing `sngl doc <dir> <platform>` shortcut.
			if plat := codegen.LookupPlatform(args[1]); plat != nil {
				return showPlatformDocs(args[1], plat)
			}
			if dt.Native != nil {
				return showNativeDecl(dt.Title, args[1], dt.Native)
			}
			return showDeclDoc(dt.Docs, args[1], dt.Stmts)
		}
		if dt.Native != nil {
			return showNativeImportIndex(dt.Title, dt.Native)
		}
		if dt.IsStdlib {
			return showStdlibIndex(dt.Docs)
		}
		return showPackageIndex(dt.Title, dt.Docs)
	}

	// Fall back chain for names that aren't importable targets.

	// 1. path.Decl syntax: "examples/todo.Todo" → package=examples/todo, decl=Todo
	if dotIdx := strings.LastIndex(first, "."); dotIdx > 0 {
		pkgPath := first[:dotIdx]
		declName := first[dotIdx+1:]
		if dir := resolvePackageDir(pkgPath); dir != "" {
			doc, err := parsePackage(dir)
			if err != nil {
				return err
			}
			return showDeclDoc(checker.ExtractPackageDocs(doc), declName, doc.Stmts)
		}
	}

	// 2. Platform name: "android" → show platform docs
	if plat := codegen.LookupPlatform(first); plat != nil {
		return showPlatformDocs(first, plat)
	}

	// 3. Try as a declaration in the current directory
	if doc, err := parsePackage("."); err == nil {
		pd := checker.ExtractPackageDocs(doc)
		if info := pd.FindDecl(first); info != nil {
			return showDeclDoc(pd, first, doc.Stmts)
		}
	}

	// 4. Documentation file lookup
	docsDir, _ := findDocsDir()
	if docsDir != "" {
		if err := showTopic(docsDir, first); err == nil {
			return nil
		}
	}

	// 5. Stdlib component reference
	return showComponentDoc(first)
}

// resolvePackageDir resolves a path to a package directory.
// Returns "" if the path doesn't point to a valid directory or .sngl file.
func resolvePackageDir(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return path
	}
	if strings.HasSuffix(path, ".sngl") {
		return filepath.Dir(path)
	}
	return ""
}

// parsePackage parses all .sngl files in a directory (non-recursive).
func parsePackage(dir string) (*ast.Document, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && isSNGLFile(e.Name()) {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .sngl files found in %s", dir)
	}

	f, err := os.Open(files[0])
	if err != nil {
		return nil, err
	}
	doc, err := parseSNGL(files[0], f)
	f.Close()
	if err != nil {
		return nil, err
	}

	// Merge sibling files in the same directory
	for _, path := range files[1:] {
		sf, err := os.Open(path)
		if err != nil {
			continue
		}
		sibling, err := parseSNGL(path, sf)
		sf.Close()
		if err != nil {
			continue
		}
		mergeInto(doc, sibling)
	}

	return doc, nil
}

// docTarget bundles everything `sngl doc` needs to render an import-style
// target: either SNGL-sourced decls (stdlib, local dir, FS-scheme package) or
// a native scheme import (go://, etc.).
type docTarget struct {
	Title    string
	Docs     *checker.PackageDocs // non-nil for SNGL-sourced targets
	Stmts    []ast.Stmt           // statements backing Docs (needed by showDeclDoc)
	Native   *ir.NativeImport     // non-nil for native-scheme targets
	IsStdlib bool
}

// errDocTargetNotFound signals that the input doesn't name an importable
// target. Callers use it to fall back to the legacy resolution chain
// (platform name, docs topic, stdlib-component fuzzy match).
var errDocTargetNotFound = errors.New("doc target not found")

// resolveDocTarget reuses the import-resolution machinery to map a doc query
// to a renderable package. Handles the stdlib keyword, scheme URIs, local
// directories, and aliases declared in cwd's imports.
func resolveDocTarget(target, cwd string) (*docTarget, error) {
	// The stdlib is declared by the checker rather than imported, but we route
	// it through the same mechanism so `sngl doc sngl` shares the code path.
	if target == "sngl" {
		target = "internal://stdlib"
	}

	scheme, uri := checker.ParseScheme(target)

	if scheme == "internal" && uri == "stdlib" {
		pd, stmts := stdlibPackageDocs()
		return &docTarget{Title: "sngl", Docs: pd, Stmts: stmts, IsStdlib: true}, nil
	}

	if scheme != "" {
		resolver := &cliResolver{rootDir: cwd}
		docs, _, err := resolver.ResolveSchemeFS(scheme, uri, cwd)
		if err != nil {
			return nil, err
		}
		if len(docs) > 0 {
			merged := &checker.PackageDocs{}
			var stmts []ast.Stmt
			for _, d := range docs {
				pd := checker.ExtractPackageDocs(d)
				merged.Components = append(merged.Components, pd.Components...)
				merged.Structs = append(merged.Structs, pd.Structs...)
				merged.Enums = append(merged.Enums, pd.Enums...)
				merged.Consts = append(merged.Consts, pd.Consts...)
				merged.Data = append(merged.Data, pd.Data...)
				merged.Functions = append(merged.Functions, pd.Functions...)
				stmts = append(stmts, d.Stmts...)
			}
			return &docTarget{Title: target, Docs: merged, Stmts: stmts}, nil
		}
		native, err := resolver.ResolveScheme(scheme, uri, cwd)
		if err != nil {
			return nil, err
		}
		if native != nil {
			return &docTarget{Title: target, Native: native}, nil
		}
		return nil, fmt.Errorf("scheme %q could not resolve %q", scheme, uri)
	}

	if dir := resolvePackageDir(target); dir != "" {
		doc, err := parsePackage(dir)
		if err != nil {
			return nil, err
		}
		return &docTarget{Title: dir, Docs: checker.ExtractPackageDocs(doc), Stmts: doc.Stmts}, nil
	}

	// Alias lookup: try to resolve `target` as an alias declared in cwd's imports.
	if cwd != "" {
		if doc, err := parsePackage(cwd); err == nil {
			for _, stmt := range doc.Stmts {
				imp, ok := stmt.(*ast.Import)
				if !ok {
					continue
				}
				alias := imp.Alias
				if alias == "" {
					alias = checker.NamespaceFromPath(imp.Path)
				}
				if alias != target {
					continue
				}
				resolved := imp.Path
				if imp.Replace != "" {
					resolved = imp.Replace
				}
				// Prevent self-loop if alias happens to equal the path.
				if resolved == target {
					break
				}
				return resolveDocTarget(resolved, cwd)
			}
		}
	}

	return nil, errDocTargetNotFound
}

// stdlibPackageDocs merges every embedded stdlib document into a single
// PackageDocs and returns the concatenated statements alongside it.
func stdlibPackageDocs() (*checker.PackageDocs, []ast.Stmt) {
	merged := &checker.PackageDocs{}
	var stmts []ast.Stmt
	for _, doc := range checker.StdlibDocs() {
		pd := checker.ExtractPackageDocs(doc)
		merged.Components = append(merged.Components, pd.Components...)
		merged.Structs = append(merged.Structs, pd.Structs...)
		merged.Enums = append(merged.Enums, pd.Enums...)
		merged.Consts = append(merged.Consts, pd.Consts...)
		merged.Data = append(merged.Data, pd.Data...)
		merged.Functions = append(merged.Functions, pd.Functions...)
		stmts = append(stmts, doc.Stmts...)
	}
	sortDecls := func(items []checker.DeclInfo) {
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	}
	sortDecls(merged.Components)
	sortDecls(merged.Structs)
	sortDecls(merged.Enums)
	sortDecls(merged.Consts)
	sortDecls(merged.Data)
	sortDecls(merged.Functions)
	return merged, stmts
}

// showStdlibIndex renders the stdlib as a package-style index.
func showStdlibIndex(pd *checker.PackageDocs) error {
	var sb strings.Builder
	sb.WriteString("# Standard Library (sngl)\n\n")
	sb.WriteString("Built-in components, types, and functions available without import.\n\n")
	writeDeclSection(&sb, "Components", pd.Components)
	writeDeclSection(&sb, "Types", pd.Structs)
	writeDeclSection(&sb, "Enums", pd.Enums)
	writeDeclSection(&sb, "Constants", pd.Consts)
	writeDeclSection(&sb, "Functions", pd.Functions)
	return renderToTerminal(sb.String())
}

// showPackageIndex displays all declarations in a user package.
// Platform overrides (sngl.*) and platform-specific types (Options) are
// separated into their own sections at the bottom. Unexported decls
// are filtered out upstream by ExtractPackageDocs.
func showPackageIndex(dir string, pd *checker.PackageDocs) error {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Package %s\n\n", dir))

	// Separate user declarations from platform overrides
	var userComps, overrideComps []checker.DeclInfo
	for _, d := range pd.Components {
		if strings.HasPrefix(d.Name, "sngl.") {
			overrideComps = append(overrideComps, d)
		} else {
			userComps = append(userComps, d)
		}
	}

	// User-specific types vs platform types (Options is typically a platform type)
	var userStructs, platformStructs []checker.DeclInfo
	for _, d := range pd.Structs {
		if d.Name == "Options" {
			platformStructs = append(platformStructs, d)
		} else {
			userStructs = append(userStructs, d)
		}
	}

	writeDeclSection(&sb, "Components", userComps)
	writeDeclSection(&sb, "Types", userStructs)
	writeDeclSection(&sb, "Enums", pd.Enums)
	writeDeclSection(&sb, "Constants", pd.Consts)
	writeDeclSection(&sb, "Data", pd.Data)
	writeDeclSection(&sb, "Functions", pd.Functions)

	// Platform sections at the bottom
	if len(overrideComps) > 0 || len(platformStructs) > 0 {
		sb.WriteString("---\n\n")
		writeDeclSection(&sb, "Platform Overrides", overrideComps)
		writeDeclSection(&sb, "Platform Types", platformStructs)
	}

	return renderToTerminal(sb.String())
}

// showPlatformDocs shows the declarations from a platform's Package.
func showPlatformDocs(name string, plat codegen.PlatformGenerator) error {
	docs := plat.Package()
	if len(docs) == 0 {
		return fmt.Errorf("platform %q has no package source", name)
	}
	doc := docs[0]
	pd := checker.ExtractPackageDocs(doc)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Platform: %s\n\n", name))
	sb.WriteString(fmt.Sprintf("Available as `%s.X` in your code.\n\n", name))

	// Separate overrides from platform-local components
	var local, overrides []checker.DeclInfo
	for _, d := range pd.Components {
		if strings.HasPrefix(d.Name, "sngl.") {
			overrides = append(overrides, d)
		} else {
			local = append(local, d)
		}
	}

	writeDeclSection(&sb, "Components", local)
	writeDeclSection(&sb, "Types", pd.Structs)
	writeDeclSection(&sb, "Stdlib Overrides", overrides)

	return renderToTerminal(sb.String())
}

// showNativeImportIndex renders an index for a scheme import whose decls come
// from a foreign language (e.g. go://). Each section lists names and the
// first sentence of the imported doc comment when available.
func showNativeImportIndex(title string, ni *ir.NativeImport) error {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", title))
	if ni.ImportPath != "" && ni.ImportPath != title {
		sb.WriteString(fmt.Sprintf("Import path: `%s`\n\n", ni.ImportPath))
	}

	writeNativeSection(&sb, "Types", len(ni.Structs), func(i int) (string, string) {
		return ni.Structs[i].Name, ni.Structs[i].Doc
	})
	writeNativeSection(&sb, "Enums", len(ni.Enums), func(i int) (string, string) {
		return ni.Enums[i].Name, ni.Enums[i].Doc
	})
	writeNativeSection(&sb, "Functions", len(ni.Funcs), func(i int) (string, string) {
		return ni.Funcs[i].Name, ni.Funcs[i].Doc
	})
	writeNativeSection(&sb, "Variables", len(ni.Vars), func(i int) (string, string) {
		return ni.Vars[i].Name, ni.Vars[i].Doc
	})

	return renderToTerminal(sb.String())
}

// writeNativeSection writes a bulleted section of native decls using name +
// leading-sentence blurb, matching the layout of writeDeclSection.
func writeNativeSection(sb *strings.Builder, title string, n int, at func(int) (name, doc string)) {
	if n == 0 {
		return
	}
	type entry struct{ name, doc string }
	items := make([]entry, n)
	for i := range n {
		name, doc := at(i)
		items[i] = entry{name: name, doc: doc}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].name < items[j].name })
	sb.WriteString(fmt.Sprintf("## %s\n\n", title))
	for _, it := range items {
		blurb := firstSentence(it.doc)
		if blurb != "" {
			sb.WriteString(fmt.Sprintf("- **%s** — %s\n", it.name, blurb))
		} else {
			sb.WriteString(fmt.Sprintf("- **%s**\n", it.name))
		}
	}
	sb.WriteString("\n")
}

// showNativeDecl renders a single named declaration from a native import.
func showNativeDecl(title, name string, ni *ir.NativeImport) error {
	for _, s := range ni.Structs {
		if s.Name == name {
			return renderToTerminal(renderNativeStruct(s))
		}
	}
	for _, e := range ni.Enums {
		if e.Name == name {
			return renderToTerminal(renderNativeEnum(e))
		}
	}
	for _, f := range ni.Funcs {
		if f.Name == name {
			return renderToTerminal(renderNativeFunc(f))
		}
	}
	for _, v := range ni.Vars {
		if v.Name == name {
			return renderToTerminal(renderNativeVar(v))
		}
	}
	return fmt.Errorf("%q not found in %s", name, title)
}

func renderNativeStruct(s *ir.StructDef) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# struct %s\n\n", s.Name))
	if s.Native != "" {
		sb.WriteString(fmt.Sprintf("_Native:_ `%s`\n\n", s.Native))
	}
	if s.Doc != "" {
		sb.WriteString(s.Doc)
		sb.WriteString("\n")
	}
	if len(s.Fields) > 0 {
		sb.WriteString("## Fields\n\n```\n")
		for _, f := range s.Fields {
			line := fmt.Sprintf("%-20s %s", f.Name, f.Type.String())
			if f.Unusable != "" {
				line += "  (unusable)"
			}
			sb.WriteString(line + "\n")
		}
		sb.WriteString("```\n")
	}
	return sb.String()
}

func renderNativeEnum(e *ir.EnumDef) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# enum %s\n\n", e.Name))
	if e.Doc != "" {
		sb.WriteString(e.Doc)
		sb.WriteString("\n")
	}
	if len(e.Members) > 0 {
		sb.WriteString("## Members\n\n")
		for _, m := range e.Members {
			sb.WriteString(fmt.Sprintf("- %s\n", m.Name))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func renderNativeFunc(f *ir.Func) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# func %s\n\n", f.Name))
	sb.WriteString("```\n")
	sb.WriteString(nativeFuncSignature(f))
	sb.WriteString("\n```\n\n")
	if f.NativeName != "" {
		sb.WriteString(fmt.Sprintf("_Native:_ `%s`\n\n", f.NativeName))
	}
	if f.Unusable != "" {
		sb.WriteString(fmt.Sprintf("_Unusable:_ %s\n\n", f.Unusable))
	}
	if f.Doc != "" {
		sb.WriteString(f.Doc)
		sb.WriteString("\n")
	}
	return sb.String()
}

func renderNativeVar(v *ir.Var) string {
	var sb strings.Builder
	kw := "var"
	if v.IsConst {
		kw = "const"
	}
	sb.WriteString(fmt.Sprintf("# %s %s\n\n", kw, v.Name))
	if v.Type != nil {
		sb.WriteString(fmt.Sprintf("_Type:_ `%s`\n\n", v.Type.String()))
	}
	if v.NativeName != "" {
		sb.WriteString(fmt.Sprintf("_Native:_ `%s`\n\n", v.NativeName))
	}
	if v.Unusable != "" {
		sb.WriteString(fmt.Sprintf("_Unusable:_ %s\n\n", v.Unusable))
	}
	if v.Doc != "" {
		sb.WriteString(v.Doc)
		sb.WriteString("\n")
	}
	return sb.String()
}

func nativeFuncSignature(f *ir.Func) string {
	parts := make([]string, 0, len(f.Params))
	for _, p := range f.Params {
		parts = append(parts, fmt.Sprintf("%s %s", p.Name, p.Type.String()))
	}
	sig := fmt.Sprintf("func %s(%s)", f.Name, strings.Join(parts, ", "))
	if f.Return != nil {
		sig += " " + f.Return.String()
	}
	return sig
}

// firstSentence trims a doc string to the first sentence (ending with ". ", a
// trailing period, or the first newline), matching how writeDeclSection
// summarizes PackageDocs entries.
func firstSentence(doc string) string {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return ""
	}
	if i := strings.Index(doc, ". "); i > 0 {
		return doc[:i+1]
	}
	if i := strings.IndexByte(doc, '\n'); i > 0 {
		return strings.TrimSpace(doc[:i])
	}
	return doc
}

func writeDeclSection(sb *strings.Builder, title string, items []checker.DeclInfo) {
	if len(items) == 0 {
		return
	}
	sb.WriteString(fmt.Sprintf("## %s\n\n", title))
	for _, d := range items {
		doc := d.Doc
		if i := strings.Index(doc, ". "); i > 0 {
			doc = doc[:i+1]
		}
		if doc != "" {
			sb.WriteString(fmt.Sprintf("- **%s** — %s\n", d.Name, doc))
		} else {
			sb.WriteString(fmt.Sprintf("- **%s**\n", d.Name))
		}
	}
	sb.WriteString("\n")
}

// showDeclDoc displays documentation for a specific declaration.
func showDeclDoc(pd *checker.PackageDocs, name string, stmts []ast.Stmt) error {
	// Try the full name first — this resolves dotted decls like `int.min`
	// (function) or `Alert.toast` (receiver method) before we fall back to
	// treating the dot as a struct field / component prop selector.
	declName := name
	fieldName := ""
	info := pd.FindDecl(declName)
	if info == nil {
		if before, after, ok := strings.Cut(name, "."); ok {
			declName = before
			fieldName = after
			info = pd.FindDecl(declName)
		}
	}
	if info == nil {
		return showComponentDoc(name)
	}

	switch decl := info.Decl.(type) {
	case *ast.ComponentDecl:
		// Reuse stdlib component doc rendering via schema
		registry, _, err := checker.LoadStdlib()
		if err == nil {
			if schema, ok := registry[declName]; ok {
				if fieldName != "" {
					return showPropDoc(declName, fieldName, schema)
				}
				return renderToTerminal(renderComponentDoc(declName, schema))
			}
		}
		// User component — render manually
		return renderToTerminal(renderUserComponentDoc(decl, info.Doc))

	case *ast.StructDef:
		return renderToTerminal(renderStructDoc(decl, fieldName, info.Doc))

	case *ast.EnumDef:
		return renderToTerminal(renderEnumDoc(decl, info.Doc))

	case *ast.FuncDef:
		return renderToTerminal(renderFuncDoc(decl, info.Doc))

	case *ast.ConstDecl, *ast.VarDecl:
		return renderToTerminal(renderValueDoc(info))

	default:
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("# %s\n\n", info.Name))
		if info.Doc != "" {
			sb.WriteString(info.Doc + "\n")
		}
		return renderToTerminal(sb.String())
	}
}

func renderUserComponentDoc(comp *ast.ComponentDecl, doc string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", comp.Name))

	if doc != "" {
		sb.WriteString(doc + "\n\n")
	}

	var params []ast.Param
	var events []ast.EventDecl
	for _, p := range comp.Props.Props {
		switch pd := p.(type) {
		case ast.Param:
			params = append(params, pd)
		case ast.EventDecl:
			events = append(events, pd)
		}
	}

	if len(params) > 0 {
		sb.WriteString("## Parameters\n\n```\n")
		for _, p := range params {
			pType := parser.FormatType(p.Type)
			if pType == "" {
				pType = "any"
			}
			sb.WriteString(fmt.Sprintf("%-16s %s\n", p.Name, pType))
		}
		sb.WriteString("```\n\n")
	}

	if len(events) > 0 {
		sb.WriteString("## Events\n\n```\n")
		for _, e := range events {
			payload := parser.FormatType(e.Type)
			sb.WriteString(fmt.Sprintf("@%-15s %s\n", e.Name, payload))
		}
		sb.WriteString("```\n\n")
	}

	return sb.String()
}

func renderStructDoc(s *ast.StructDef, fieldName, doc string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# struct %s\n\n", s.Name))

	if doc != "" {
		sb.WriteString(doc + "\n\n")
	}

	if fieldName != "" {
		// Show specific field
		for _, f := range s.Fields {
			for _, name := range f.Names {
				if name == fieldName {
					sb.WriteString(fmt.Sprintf("## %s.%s\n\n", s.Name, name))
					sb.WriteString(fmt.Sprintf("Type: `%s`\n", parser.FormatType(f.Type)))
					return sb.String()
				}
			}
		}
		sb.WriteString(fmt.Sprintf("field %q not found\n", fieldName))
		return sb.String()
	}

	if len(s.Fields) == 0 {
		return sb.String()
	}

	sb.WriteString("## Fields\n\n```\n")
	for _, f := range s.Fields {
		tstr := parser.FormatType(f.Type)
		for _, name := range f.Names {
			sb.WriteString(fmt.Sprintf("%-16s %s\n", name, tstr))
		}
	}
	sb.WriteString("```\n")
	return sb.String()
}

func renderEnumDoc(e *ast.EnumDef, doc string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# enum %s\n\n", e.Name))

	if doc != "" {
		sb.WriteString(doc + "\n\n")
	}

	sb.WriteString("## Values\n\n```\n")
	for _, m := range e.Members {
		sb.WriteString(m.Name + "\n")
	}
	sb.WriteString("```\n")
	return sb.String()
}

func renderFuncDoc(f *ast.FuncDef, doc string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# func %s\n\n", f.Name))

	// Signature on one line in a code block
	var sig strings.Builder
	sig.WriteString("func " + f.Name)
	if len(f.TypeParams) > 0 {
		sig.WriteString("<" + strings.Join(f.TypeParams, ", ") + ">")
	}
	sig.WriteString("(")
	for i, p := range f.Params.Params {
		if i > 0 {
			sig.WriteString(", ")
		}
		sig.WriteString(p.Name)
		if p.Type != nil {
			sig.WriteString(" " + parser.FormatType(p.Type))
		}
	}
	sig.WriteString(")")
	if f.ReturnType != nil {
		sig.WriteString(" " + parser.FormatType(f.ReturnType))
	}
	sb.WriteString("```\n" + sig.String() + "\n```\n\n")

	if doc != "" {
		sb.WriteString(doc + "\n\n")
	}

	if len(f.Params.Params) > 0 {
		sb.WriteString("## Parameters\n\n```\n")
		for _, p := range f.Params.Params {
			ptype := parser.FormatType(p.Type)
			if ptype == "" {
				ptype = "any"
			}
			sb.WriteString(fmt.Sprintf("%-16s %s\n", p.Name, ptype))
		}
		sb.WriteString("```\n\n")
	}

	if f.ReturnType != nil {
		sb.WriteString(fmt.Sprintf("**Returns:** `%s`\n", parser.FormatType(f.ReturnType)))
	}

	return sb.String()
}

func renderValueDoc(info *checker.DeclInfo) string {
	var sb strings.Builder
	kind := "var"
	if _, ok := info.Decl.(*ast.ConstDecl); ok {
		kind = "const"
	}
	sb.WriteString(fmt.Sprintf("# %s %s\n\n", kind, info.Name))
	if info.Doc != "" {
		sb.WriteString(info.Doc + "\n")
	}
	return sb.String()
}

// serveDocHTTP starts the web documentation browser.
func serveDocHTTP(addr, dir string) error {
	docs.SetWorkspaceDir(dir)
	fmt.Fprintf(os.Stderr, "SNGL docs → http://%s\n", addr)
	return http.ListenAndServe(addr, docbrowser.Handler())
}

// serveDocHTTPFallback is the previous dynamic doc server (kept for package-specific docs).
func serveDocHTTPFallback(addr, dir string) error {
	return http.ListenAndServe(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		// Build sidebar items: optional package decls + stdlib components.
		type item struct {
			Section string `json:"section,omitempty"`
			Name    string `json:"name,omitempty"`
			Kind    string `json:"kind,omitempty"`
			Doc     string `json:"doc,omitempty"`
			Detail  string `json:"detail,omitempty"`
		}

		var items []item

		// Package declarations (if dir has .sngl files).
		if doc, err := parsePackage(dir); err == nil {
			pd := checker.ExtractPackageDocs(doc)
			abs, _ := filepath.Abs(dir)
			items = append(items, item{Section: filepath.Base(abs)})
			for _, d := range pd.Components {
				items = append(items, item{Name: d.Name, Kind: "component", Doc: d.Doc, Detail: declDetailHTML(d)})
			}
			for _, d := range pd.Structs {
				items = append(items, item{Name: d.Name, Kind: "struct", Doc: d.Doc, Detail: declDetailHTML(d)})
			}
			for _, d := range pd.Enums {
				items = append(items, item{Name: d.Name, Kind: "enum", Doc: d.Doc, Detail: declDetailHTML(d)})
			}
			for _, d := range pd.Functions {
				items = append(items, item{Name: d.Name, Kind: "func", Doc: d.Doc, Detail: declDetailHTML(d)})
			}
			for _, d := range pd.Data {
				items = append(items, item{Name: d.Name, Kind: "var", Doc: d.Doc, Detail: declDetailHTML(d)})
			}
			items = append(items, item{Section: "─── Stdlib ───"})
		}

		// Stdlib components.
		for _, tier := range docs.ComponentsByTier() {
			items = append(items, item{Section: tier.Name})
			for _, c := range tier.Components {
				items = append(items, item{
					Name:   c.Name,
					Kind:   "component",
					Doc:    c.Doc,
					Detail: componentDetailHTML(c),
				})
			}
		}

		data, _ := json.Marshal(items)
		fmt.Fprint(w, docBrowserHTML(string(data)))
	}))
}

func componentDetailHTML(c docs.Component) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<h2>%s</h2>", c.Name)
	if c.Tier != "" {
		fmt.Fprintf(&b, "<span class='tier'>%s</span>", c.Tier)
	}
	if c.Doc != "" {
		fmt.Fprintf(&b, "<p>%s</p>", c.Doc)
	}
	if c.Children != "none" && c.Children != "" {
		fmt.Fprintf(&b, "<p><b>Children:</b> %s</p>", c.Children)
	}
	if len(c.Props) > 0 {
		b.WriteString("<h3>Properties</h3><table>")
		for _, p := range c.Props {
			fmt.Fprintf(&b, "<tr><td class='pn'>%s</td><td class='pt'>%s</td><td>%s</td></tr>", p.Name, p.Type, p.Doc)
		}
		b.WriteString("</table>")
	}
	if len(c.Events) > 0 {
		b.WriteString("<h3>Events</h3><table>")
		for _, e := range c.Events {
			payload := ""
			if e.PayloadType != "" {
				payload = e.PayloadType
			}
			fmt.Fprintf(&b, "<tr><td class='pn'>@%s</td><td class='pt'>%s</td></tr>", e.Name, payload)
		}
		b.WriteString("</table>")
	}
	if len(c.Examples) > 0 {
		fmt.Fprintf(&b, "<h3>Example</h3><pre>%s</pre>", c.Examples[0])
	}
	return b.String()
}

func declDetailHTML(d checker.DeclInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<h2>%s</h2>", d.Name)
	if d.Doc != "" {
		fmt.Fprintf(&b, "<p>%s</p>", d.Doc)
	}
	switch decl := d.Decl.(type) {
	case *ast.ComponentDecl:
		for _, p := range decl.Props.Props {
			if param, ok := p.(ast.Param); ok {
				ptype := "any"
				if param.Type != nil {
					ptype = fmt.Sprint(param.Type)
				}
				b.WriteString("<h3>Parameters</h3><table>")
				fmt.Fprintf(&b, "<tr><td class='pn'>%s</td><td class='pt'>%s</td></tr>", param.Name, ptype)
				b.WriteString("</table>")
			}
		}
	case *ast.StructDef:
		if len(decl.Fields) > 0 {
			b.WriteString("<h3>Fields</h3><table>")
			for _, f := range decl.Fields {
				for _, name := range f.Names {
					fmt.Fprintf(&b, "<tr><td class='pn'>%s</td><td class='pt'>%s</td></tr>", name, fmt.Sprint(f.Type))
				}
			}
			b.WriteString("</table>")
		}
	case *ast.EnumDef:
		if len(decl.Members) > 0 {
			b.WriteString("<h3>Values</h3><ul>")
			for _, m := range decl.Members {
				fmt.Fprintf(&b, "<li>%s</li>", m.Name)
			}
			b.WriteString("</ul>")
		}
	}
	return b.String()
}

func docBrowserHTML(dataJSON string) string {
	return `<!DOCTYPE html><html><head><meta charset="utf-8">
<title>SNGL Documentation</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:#1a1a2e;color:#e0e0e0;height:100vh;overflow:hidden}
#app{display:flex;height:100vh}
#sidebar{width:280px;overflow-y:auto;border-right:1px solid #333;padding:8px 0;flex-shrink:0}
#detail{flex:1;overflow-y:auto;padding:24px 32px}
.section{padding:8px 16px;color:#888;font-size:12px;text-transform:uppercase;letter-spacing:1px;margin-top:8px}
.item{padding:6px 16px;cursor:pointer;display:flex;align-items:center;gap:8px;font-size:14px}
.item:hover{background:#252545}
.item.active{background:#2a2a4a;color:#7ee787;font-weight:600}
.kind{font-size:11px;color:#58a6ff;min-width:14px;text-align:center}
h2{color:#fff;margin-bottom:8px}
h3{color:#ccc;margin:16px 0 8px;font-size:15px}
p{margin:8px 0;line-height:1.5}
.tier{background:#3a2a00;color:#d29922;padding:2px 8px;border-radius:4px;font-size:12px;margin-left:8px}
table{border-collapse:collapse;margin:4px 0;width:100%}
td{padding:4px 12px 4px 0;vertical-align:top;border-bottom:1px solid #2a2a3a}
.pn{color:#7ee787;font-weight:500;white-space:nowrap}
.pt{color:#58a6ff;font-size:13px;white-space:nowrap}
pre{background:#12122a;padding:12px;border-radius:6px;overflow-x:auto;font-size:13px;line-height:1.4}
ul{padding-left:20px}
li{margin:4px 0}
.empty{color:#666;padding:40px;text-align:center;font-size:16px}
</style></head><body>
<div id="app"><nav id="sidebar"></nav><main id="detail"><div class="empty">Select an item</div></main></div>
<script>
const items=` + dataJSON + `;
const sidebar=document.getElementById("sidebar");
const detail=document.getElementById("detail");
let active=-1;
function kindLabel(k){return{component:"c",struct:"s",enum:"e",func:"f",var:"v","const":"c"}[k]||""}
function select_(i){active=i;location.hash=items[i].name||"";render();detail.innerHTML=items[i].detail||"<div class='empty'>No details</div>";detail.scrollTop=0}
function render(){
  sidebar.innerHTML="";
  items.forEach((it,i)=>{
    if(it.section){const d=document.createElement("div");d.className="section";d.textContent=it.section;sidebar.appendChild(d);return}
    const d=document.createElement("div");d.className="item"+(i===active?" active":"");
    d.innerHTML="<span class='kind'>"+kindLabel(it.kind)+"</span>"+it.name;
    d.onclick=()=>select_(i);
    sidebar.appendChild(d);
  });
}
function fromHash(){const h=decodeURIComponent(location.hash.slice(1));if(!h)return false;const i=items.findIndex(x=>x.name===h);if(i>=0){select_(i);return true}return false}
render();
if(!fromHash()){let f=items.findIndex(x=>!x.section);if(f>=0)select_(f)}
window.addEventListener("hashchange",fromHash)
</script></body></html>`
}

func runDocBuild(cmd *cobra.Command, args []string) error {
	outDir, _ := cmd.Flags().GetString("out")
	snglFile := findWebsiteSNGL()
	if snglFile == "" {
		return fmt.Errorf("no website.sngl found")
	}
	if err := runSNGLCompile(snglFile, outDir); err != nil {
		return err
	}
	fmt.Printf("Site built in %s/\n", outDir)
	return nil
}

func runDocServe(cmd *cobra.Command, args []string) error {
	outDir, _ := cmd.Flags().GetString("out")
	port, _ := cmd.Flags().GetInt("port")
	snglFile := findWebsiteSNGL()
	if snglFile == "" {
		return fmt.Errorf("no website.sngl found")
	}
	if err := runSNGLCompile(snglFile, outDir); err != nil {
		return err
	}
	fmt.Printf("Serving on http://localhost:%d\n", port)
	return http.ListenAndServe(fmt.Sprintf(":%d", port), http.FileServer(http.Dir(outDir)))
}

// findDocsDir walks up from cwd looking for a docs/ directory.
func findDocsDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, "docs")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", nil
}

// showTopicListWithComponents shows the topic list and appends a component
// listing grouped by tier.
func showTopicListWithComponents(docsDir string) error {
	var sb strings.Builder
	sb.WriteString("# SNGL Documentation\n\n")
	sb.WriteString("SNGL is a purpose-built language for describing reactive, cross-platform UIs.\n\n")

	if docsDir != "" {
		topics, err := listTopics(docsDir)
		if err == nil && len(topics) > 0 {
			sb.WriteString("## Topics\n\n")
			for _, t := range topics {
				sb.WriteString(fmt.Sprintf("- **%s** — %s\n", t.name, t.desc))
			}
			sb.WriteString("\n")
		}
	}

	// Component listing.
	registry, _, err := checker.LoadStdlib()
	if err == nil && len(registry) > 0 {
		tiers := docsite.AssignTiers(registry)
		tierIdx := map[string]int{}
		for i, t := range docsite.TierOrder {
			tierIdx[t] = i
		}

		type compEntry struct {
			name string
			doc  string
			tier string
		}
		var entries []compEntry
		for name, schema := range registry {
			doc := schema.Doc
			if i := strings.Index(doc, ". "); i > 0 {
				doc = doc[:i+1]
			}
			entries = append(entries, compEntry{name: name, doc: doc, tier: tiers[name]})
		}
		sort.Slice(entries, func(i, j int) bool {
			ti := tierIdx[entries[i].tier]
			tj := tierIdx[entries[j].tier]
			if ti != tj {
				return ti < tj
			}
			return entries[i].name < entries[j].name
		})

		sb.WriteString("## Components\n\n")
		currentTier := ""
		for _, e := range entries {
			if e.tier != currentTier {
				currentTier = e.tier
				sb.WriteString(fmt.Sprintf("### %s\n\n", currentTier))
			}
			if e.doc != "" {
				sb.WriteString(fmt.Sprintf("- **%s** — %s\n", e.name, e.doc))
			} else {
				sb.WriteString(fmt.Sprintf("- **%s**\n", e.name))
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Run `sngl doc <topic>` to read about a topic.\n")
	sb.WriteString("Run `sngl doc <component>` to see component reference.\n")
	sb.WriteString("Run `sngl doc <component>.<prop>` to see a specific property.\n")
	sb.WriteString("Run `sngl doc build` to generate the HTML documentation site.\n")

	return renderToTerminal(sb.String())
}

func showTopic(docsDir, topic string) error {
	// Search for a matching markdown file
	candidates := []string{
		filepath.Join(docsDir, topic+".md"),
		filepath.Join(docsDir, topic, "index.md"),
		filepath.Join(docsDir, "language", topic+".md"),
		filepath.Join(docsDir, "getting-started", topic+".md"),
	}

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fm, body, err := docsite.ParseFrontMatter(data)
		if err != nil {
			return err
		}
		title := fm.Title
		if title == "" {
			title = topic
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("# %s\n\n", title))
		sb.Write(body)
		return renderToTerminal(sb.String())
	}

	// If topic is a directory, list its contents
	dirPath := filepath.Join(docsDir, topic)
	if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
		return showDirTopic(docsDir, dirPath, topic)
	}

	return fmt.Errorf("not found")
}

// showComponentDoc displays component reference for a component or
// component.prop query.
func showComponentDoc(query string) error {
	registry, _, err := checker.LoadStdlib()
	if err != nil {
		return fmt.Errorf("failed to load stdlib: %w", err)
	}

	// Support "component.prop" syntax.
	compName := query
	propName := ""
	if before, after, ok := strings.Cut(query, "."); ok {
		compName = before
		propName = after
	}

	schema, ok := registry[compName]
	if !ok {
		// Try fuzzy match on component names.
		var matches []string
		for name := range registry {
			if strings.Contains(strings.ToLower(name), strings.ToLower(compName)) {
				matches = append(matches, name)
			}
		}
		sort.Strings(matches)
		if len(matches) > 0 {
			return fmt.Errorf("component %q not found. Did you mean: %s", compName, strings.Join(matches, ", "))
		}
		return fmt.Errorf("%q not found. Run 'sngl doc' to see available topics and components", query)
	}

	// Show specific property.
	if propName != "" {
		return showPropDoc(compName, propName, schema)
	}

	return renderToTerminal(renderComponentDoc(compName, schema))
}

func renderComponentDoc(name string, schema *checker.ComponentSchema) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", name))

	// Strip the example from the doc text (it's shown separately as a snapshot)
	doc := schema.Doc
	if idx := strings.Index(doc, " Example:"); idx > 0 {
		doc = strings.TrimSpace(doc[:idx])
	}
	if doc != "" {
		sb.WriteString(doc)
		sb.WriteString("\n\n")
	}

	// Show ANSI snapshot if available
	// Look for ANSI snapshot relative to the stdlib source
	snapshotDir := filepath.Join("lib", "snapshots")
	if ansi, err := os.ReadFile(filepath.Join(snapshotDir, name+"_bubbletea.txt")); err == nil {
		sb.WriteString("## Preview\n\n```\n")
		sb.Write(ansi)
		sb.WriteString("\n```\n\n")
	}

	// Show example source if available
	examples, _ := checker.StdlibExamples()
	if srcs, ok := examples[name]; ok && len(srcs) > 0 {
		sb.WriteString("## Example\n\n```sngl\n")
		sb.WriteString(srcs[0])
		sb.WriteString("\n```\n\n")
	}

	if len(schema.Props) > 0 {
		sb.WriteString("## Properties\n\n```\n")
		type propEntry struct {
			name string
			ps   checker.PropSchema
		}
		var props []propEntry
		for pname, ps := range schema.Props {
			props = append(props, propEntry{name: pname, ps: ps})
		}
		sort.Slice(props, func(i, j int) bool {
			return props[i].name < props[j].name
		})
		for _, p := range props {
			line := fmt.Sprintf("%-16s %s", p.name, (&p.ps.Type).String())
			if len(p.ps.Enum) > 0 {
				line += fmt.Sprintf("  (%s)", strings.Join(p.ps.Enum, ", "))
			}
			sb.WriteString(line + "\n")
			if p.ps.Doc != "" {
				sb.WriteString(fmt.Sprintf("                 %s\n", p.ps.Doc))
			}
		}
		sb.WriteString("```\n\n")
	}

	if len(schema.Events) > 0 {
		sb.WriteString("## Events\n\n```\n")
		type eventEntry struct {
			name    string
			payload string
		}
		var events []eventEntry
		for ename, payload := range schema.Events {
			events = append(events, eventEntry{name: ename, payload: payload})
		}
		sort.Slice(events, func(i, j int) bool {
			return events[i].name < events[j].name
		})
		for _, e := range events {
			sb.WriteString(fmt.Sprintf("%-16s %s\n", e.name, e.payload))
		}
		sb.WriteString("```\n\n")
	}

	sb.WriteString(fmt.Sprintf("**Children:** %s\n", docsite.ChildPolicyString(schema.Children)))

	return sb.String()
}

func showPropDoc(compName, propName string, schema *checker.ComponentSchema) error {
	ps, ok := schema.Props[propName]
	if !ok {
		// Check events too.
		payload, ok := schema.Events[propName]
		if ok {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("# %s.%s (event)\n\n", compName, propName))
			sb.WriteString(fmt.Sprintf("Payload type: %s\n", payload))
			return renderToTerminal(sb.String())
		}

		var available []string
		for pname := range schema.Props {
			available = append(available, pname)
		}
		for ename := range schema.Events {
			available = append(available, ename)
		}
		sort.Strings(available)
		return fmt.Errorf("property %q not found on %s. Available: %s", propName, compName, strings.Join(available, ", "))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s.%s\n\n", compName, propName))
	sb.WriteString(fmt.Sprintf("Type: %s\n\n", (&ps.Type).String()))
	if len(ps.Enum) > 0 {
		sb.WriteString(fmt.Sprintf("Values: %s\n\n", strings.Join(ps.Enum, ", ")))
	}
	if ps.Doc != "" {
		sb.WriteString(ps.Doc + "\n")
	}

	return renderToTerminal(sb.String())
}

func showDirTopic(docsDir, dirPath, topic string) error {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return err
	}

	title := strings.ReplaceAll(topic, "-", " ")
	title = strings.Title(title) //nolint:staticcheck

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", title))
	sb.WriteString("## Pages\n\n")
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		data, err := os.ReadFile(filepath.Join(dirPath, e.Name()))
		if err != nil {
			continue
		}
		fm, _, _ := docsite.ParseFrontMatter(data)
		desc := fm.Description
		if desc == "" {
			desc = fm.Title
		}
		if desc == "" {
			desc = name
		}
		sb.WriteString(fmt.Sprintf("- **%s** — %s\n", name, desc))
	}
	sb.WriteString(fmt.Sprintf("\nRun `sngl doc %s/<page>` or `sngl doc <page>` to read a page.\n", topic))
	return renderToTerminal(sb.String())
}

type topicInfo struct {
	name string
	desc string
}

func listTopics(docsDir string) ([]topicInfo, error) {
	var topics []topicInfo

	entries, err := os.ReadDir(docsDir)
	if err != nil {
		return nil, err
	}

	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "_") || strings.HasPrefix(e.Name(), ".") || e.Name() == "assets" {
			continue
		}

		if e.IsDir() {
			indexPath := filepath.Join(docsDir, e.Name(), "index.md")
			data, err := os.ReadFile(indexPath)
			if err != nil {
				topics = append(topics, topicInfo{
					name: e.Name(),
					desc: strings.ReplaceAll(e.Name(), "-", " "),
				})
				continue
			}
			fm, _, _ := docsite.ParseFrontMatter(data)
			desc := fm.Description
			if desc == "" {
				desc = fm.Title
			}
			topics = append(topics, topicInfo{name: e.Name(), desc: desc})
			continue
		}

		if filepath.Ext(e.Name()) != ".md" {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if name == "index" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(docsDir, e.Name()))
		if err != nil {
			continue
		}
		fm, _, _ := docsite.ParseFrontMatter(data)
		desc := fm.Description
		if desc == "" {
			desc = fm.Title
		}
		topics = append(topics, topicInfo{name: name, desc: desc})
	}

	sort.Slice(topics, func(i, j int) bool {
		return topics[i].name < topics[j].name
	})
	return topics, nil
}

// findWebsiteSNGL walks up from cwd looking for a website.sngl file.
func findWebsiteSNGL() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, "website.sngl")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// runSNGLCompile shells out to sngl compile to build a .sngl file.
func runSNGLCompile(snglFile, outDir string) error {
	cmd := exec.Command(os.Args[0], "compile", "--out", outDir, snglFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func renderToTerminal(md string) error {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(100),
	)
	if err != nil {
		// Fall back to plain output
		fmt.Print(md)
		return nil
	}
	out, err := r.Render(md)
	if err != nil {
		fmt.Print(md)
		return nil
	}
	fmt.Print(out)
	return nil
}
