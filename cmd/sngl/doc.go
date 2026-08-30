package main

import (
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
	"git.duckfam.us/jonathan/sngl/docs/lookup"
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
	Args: cobra.MaximumNArgs(3),
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

	// Let docs/lookup resolve scheme imports via the CLI's codegen-aware resolver.
	lookup.RegisterResolver(func(cwd string) checker.ImportResolver {
		return &cliResolver{rootDir: cwd}
	})
}

func runDoc(cmd *cobra.Command, args []string) error {
	httpAddr, _ := cmd.Flags().GetString("http")

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

	// Preserve the `sngl doc <pkg> <platform>` shortcut before Lookup burns a
	// miss on the second arg.
	if len(args) >= 2 {
		if plat := codegen.LookupPlatform(args[1]); plat != nil {
			return showPlatformDocs(args[1], plat)
		}
	}

	res, err := lookup.Lookup(first, args[1:]...)
	if err == nil {
		return renderResult(res)
	}
	if !errors.Is(err, lookup.ErrNotFound) {
		return err
	}

	if len(args) == 1 {
		// path.Decl form: "examples/todo.Todo"
		if dotIdx := strings.LastIndex(first, "."); dotIdx > 0 {
			pkgPath := first[:dotIdx]
			declName := first[dotIdx+1:]
			if resolvePackageDir(pkgPath) != "" {
				r, err := lookup.Lookup(pkgPath, declName)
				if err == nil {
					return renderResult(r)
				}
			}
		}

		// Platform name shortcut: "android"
		if plat := codegen.LookupPlatform(first); plat != nil {
			return showPlatformDocs(first, plat)
		}

		if r, err := lookup.Lookup(".", first); err == nil {
			return renderResult(r)
		}

		docsDir, _ := findDocsDir()
		if docsDir != "" {
			if err := showTopic(docsDir, first); err == nil {
				return nil
			}
		}

		// Resolved through the packages rather than a flat merged registry, so
		// the answer can say where the name lives and what importing it costs.
		if origins := lookup.FindInLibrary(first); len(origins) > 0 {
			if len(origins) > 1 {
				return ambiguousLibraryName(first, origins)
			}
			r, err := lookup.Lookup(origins[0].Pkg, first)
			if err != nil {
				return err
			}
			printOrigin(origins[0])
			return renderResult(r)
		}

		return showComponentDoc(first)
	}

	return err
}

// printOrigin says which package a bare name resolved to, and how to reach
// it. Only sngl:builtin needs no import, so for everything else the name
// alone is not enough to write the program.
func printOrigin(o lookup.LibraryOrigin) {
	if imp := o.ImportLine(); imp != "" {
		fmt.Printf("%s\n%s\n\n", o.Pkg, imp)
		return
	}
	fmt.Printf("%s (ambient)\n\n", o.Pkg)
}

func ambiguousLibraryName(name string, origins []lookup.LibraryOrigin) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%q is declared by more than one package:\n", name)
	for _, o := range origins {
		fmt.Fprintf(&sb, "\t%s %s\t%s\n", o.Kind, o.Pkg, name)
	}
	fmt.Fprintf(&sb, "name the one you mean, e.g. sngl doc %s %s", origins[0].Pkg, name)
	return errors.New(sb.String())
}

func renderResult(res lookup.Result) error {
	switch res.Kind {
	case lookup.KindIndex:
		return renderToTerminal(renderIndexMD(res.Index))
	case lookup.KindComponent:
		return renderToTerminal(renderComponentMD(res.Component))
	case lookup.KindType:
		return renderToTerminal(renderTypeMD(res.Type))
	case lookup.KindEnum:
		return renderToTerminal(renderEnumMD(res.Enum))
	case lookup.KindFunc:
		return renderToTerminal(renderFuncMD(res.Func))
	case lookup.KindValue:
		return renderToTerminal(renderValueMD(res.Value))
	case lookup.KindProp:
		return renderToTerminal(renderPropMD(res.Prop))
	case lookup.KindField:
		return renderToTerminal(renderFieldMD(res.Field))
	case lookup.KindMember:
		return renderToTerminal(renderMemberMD(res.Member))
	}
	return fmt.Errorf("unknown lookup result kind: %d", res.Kind)
}

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

func showPlatformDocs(name string, plat codegen.PlatformGenerator) error {
	docs := codegen.PlatformDocs(plat)
	if len(docs) == 0 {
		return fmt.Errorf("platform %q has no package source", name)
	}
	pd := &checker.PackageDocs{}
	for _, doc := range docs {
		d := checker.ExtractPackageDocs(doc)
		pd.Components = append(pd.Components, d.Components...)
		pd.Structs = append(pd.Structs, d.Structs...)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Platform: %s\n\n", name))
	sb.WriteString(fmt.Sprintf("Available as `%s.X` in your code.\n\n", name))

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

func renderNativeStruct(s *ir.StructDef) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# struct %s\n\n", s.Name))
	if s.Foreign.Name != "" {
		sb.WriteString(fmt.Sprintf("_Native:_ `%s`\n\n", s.Foreign.Name))
	}
	if s.Doc != "" {
		sb.WriteString(s.Doc)
		sb.WriteString("\n")
	}
	if len(s.Fields) > 0 {
		sb.WriteString("## Fields\n\n```\n")
		for _, f := range s.Fields {
			line := fmt.Sprintf("%-20s %s", f.Name, f.Type.String())
			if f.Foreign.Unusable != "" {
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
	if f.Foreign.Name != "" {
		sb.WriteString(fmt.Sprintf("_Native:_ `%s`\n\n", f.Foreign.Name))
	}
	if f.Foreign.Unusable != "" {
		sb.WriteString(fmt.Sprintf("_Unusable:_ %s\n\n", f.Foreign.Unusable))
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
	if v.Foreign.Name != "" {
		sb.WriteString(fmt.Sprintf("_Native:_ `%s`\n\n", v.Foreign.Name))
	}
	if v.Foreign.Unusable != "" {
		sb.WriteString(fmt.Sprintf("_Unusable:_ %s\n\n", v.Foreign.Unusable))
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

func renderIndexMD(idx *lookup.DeclIndex) string {
	var sb strings.Builder
	if idx.Library {
		sb.WriteString(fmt.Sprintf("# %s\n\n", idx.Title))
	} else if idx.Native != nil {
		sb.WriteString(fmt.Sprintf("# %s\n\n", idx.Title))
		if idx.Native.ImportPath != "" && idx.Native.ImportPath != idx.Title {
			sb.WriteString(fmt.Sprintf("Import path: `%s`\n\n", idx.Native.ImportPath))
		}
	} else {
		sb.WriteString(fmt.Sprintf("# Package %s\n\n", idx.Title))
	}
	if idx.Description != "" {
		sb.WriteString(idx.Description + "\n\n")
	}
	writeSummarySection(&sb, "Components", idx.Components)
	writeTypeEntrySection(&sb, "Types", idx.Types)
	writeSummarySection(&sb, "Enums", idx.Enums)
	writeSummarySection(&sb, "Constants", idx.Constants)
	writeSummarySection(&sb, "Data", idx.Data)
	writeSummarySection(&sb, "Functions", idx.Functions)
	writeSummarySection(&sb, "Macros", idx.Macros)
	if len(idx.Overrides) > 0 || len(idx.PlatformTypes) > 0 {
		sb.WriteString("---\n\n")
		writeSummarySection(&sb, "Platform Overrides", idx.Overrides)
		writeSummarySection(&sb, "Platform Types", idx.PlatformTypes)
	}
	return sb.String()
}

func writeSummarySection(sb *strings.Builder, title string, items []lookup.DeclSummary) {
	if len(items) == 0 {
		return
	}
	sb.WriteString(fmt.Sprintf("## %s\n\n", title))
	for _, d := range items {
		blurb := lookup.FirstSentence(d.Doc)
		if blurb != "" {
			sb.WriteString(fmt.Sprintf("- **%s** — %s\n", d.Name, blurb))
		} else {
			sb.WriteString(fmt.Sprintf("- **%s**\n", d.Name))
		}
	}
	sb.WriteString("\n")
}

func writeTypeEntrySection(sb *strings.Builder, title string, items []lookup.TypeEntry) {
	if len(items) == 0 {
		return
	}
	sb.WriteString(fmt.Sprintf("## %s\n\n", title))
	for _, t := range items {
		if blurb := lookup.FirstSentence(t.Doc); blurb != "" {
			sb.WriteString(fmt.Sprintf("- **%s** — %s\n", t.Name, blurb))
		} else {
			sb.WriteString(fmt.Sprintf("- **%s**\n", t.Name))
		}
		for _, m := range t.Methods {
			if blurb := lookup.FirstSentence(m.Doc); blurb != "" {
				sb.WriteString(fmt.Sprintf("  - **%s** — %s\n", m.ShortName, blurb))
			} else {
				sb.WriteString(fmt.Sprintf("  - **%s**\n", m.ShortName))
			}
		}
	}
	sb.WriteString("\n")
}

func renderComponentMD(c *lookup.ComponentDetail) string {
	if c.Schema != nil {
		return renderComponentDoc(c.Name, c.Schema)
	}
	if c.AST != nil {
		return renderUserComponentDoc(c.AST, c.Doc)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", c.Name))
	if c.Doc != "" {
		sb.WriteString(c.Doc + "\n")
	}
	return sb.String()
}

func renderTypeMD(t *lookup.TypeDetail) string {
	if t.Native != nil {
		return renderNativeStruct(t.Native)
	}
	if t.Struct != nil {
		return renderStructDoc(t.Struct, "", t.Doc)
	}
	// Primitive receiver (int/float/string/list) — no struct body, just list methods.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", t.Name))
	if t.Doc != "" {
		sb.WriteString(t.Doc + "\n\n")
	}
	if len(t.Methods) > 0 {
		sb.WriteString("## Methods\n\n")
		for _, m := range t.Methods {
			if blurb := lookup.FirstSentence(m.Doc); blurb != "" {
				sb.WriteString(fmt.Sprintf("- **%s** — %s\n", m.ShortName, blurb))
			} else {
				sb.WriteString(fmt.Sprintf("- **%s**\n", m.ShortName))
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func renderEnumMD(e *lookup.EnumDetail) string {
	if e.Native != nil {
		return renderNativeEnum(e.Native)
	}
	return renderEnumDoc(e.AST, e.Doc)
}

func renderFuncMD(f *lookup.FuncDetail) string {
	if f.Native != nil {
		return renderNativeFunc(f.Native)
	}
	return renderFuncDoc(f.AST, f.Doc)
}

func renderValueMD(v *lookup.ValueDetail) string {
	if v.Native != nil {
		return renderNativeVar(v.Native)
	}
	kind := "var"
	if v.IsConst {
		kind = "const"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s %s\n\n", kind, v.Name))
	if v.Doc != "" {
		sb.WriteString(v.Doc + "\n")
	}
	return sb.String()
}

func renderPropMD(p *lookup.PropDetail) string {
	var sb strings.Builder
	if p.Schema != nil {
		sb.WriteString(fmt.Sprintf("# %s.%s\n\n", p.Component, p.Name))
		sb.WriteString(fmt.Sprintf("Type: %s\n\n", (&p.Schema.Type).String()))
		if len(p.Schema.Enum) > 0 {
			sb.WriteString(fmt.Sprintf("Values: %s\n\n", strings.Join(p.Schema.Enum, ", ")))
		}
		if p.Schema.Doc != "" {
			sb.WriteString(p.Schema.Doc + "\n")
		}
		return sb.String()
	}
	sb.WriteString(fmt.Sprintf("# %s.%s (event)\n\n", p.Component, p.Name))
	if p.Event != "" {
		sb.WriteString(fmt.Sprintf("Payload type: %s\n", p.Event))
	}
	return sb.String()
}

func renderFieldMD(f *lookup.FieldDetail) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s.%s\n\n", f.Type, f.Name))
	if f.Expr != nil {
		sb.WriteString(fmt.Sprintf("Type: `%s`\n", parser.FormatType(f.Expr)))
	}
	return sb.String()
}

func renderMemberMD(m *lookup.MemberDetail) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s.%s\n\n", m.Enum, m.Name))
	if m.Doc != "" {
		sb.WriteString(m.Doc + "\n")
	}
	return sb.String()
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
		for _, f := range s.Fields() {
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

	if len(s.Fields()) == 0 {
		return sb.String()
	}

	sb.WriteString("## Fields\n\n```\n")
	for _, f := range s.Fields() {
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
	for _, m := range e.Members() {
		sb.WriteString(m.Name + "\n")
	}
	sb.WriteString("```\n")
	return sb.String()
}

func renderFuncDoc(f *ast.FuncDef, doc string) string {
	var sb strings.Builder
	// A macro is written as a mark, never called, so the declaration's own
	// spelling would only invite a call.
	if checker.IsMacroDecl(f) {
		sb.WriteString(fmt.Sprintf("# macro %s\n\n", f.Name))
		sb.WriteString("```\n" + lookup.MarkSignature(f.Name, f) + "\n```\n\n")
		if doc != "" {
			sb.WriteString(doc + "\n")
		}
		return sb.String()
	}
	sb.WriteString(fmt.Sprintf("# func %s\n\n", f.Name))

	var sig strings.Builder
	sig.WriteString("func " + f.Name)
	sig.WriteString(parser.FormatTypeParams(f.TypeParams))
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

func serveDocHTTP(addr, dir string) error {
	fmt.Fprintf(os.Stderr, "SNGL docs → http://%s\n", addr)
	return http.ListenAndServe(addr, docbrowser.Handler())
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

func showTopic(docsDir, topic string) error {
	candidates := []string{
		filepath.Join(docsDir, topic+".md"),
		filepath.Join(docsDir, topic, "index.md"),
		filepath.Join(docsDir, "reference", topic+".md"),
		filepath.Join(docsDir, "learn", topic+".md"),
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

	dirPath := filepath.Join(docsDir, topic)
	if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
		return showDirTopic(docsDir, dirPath, topic)
	}

	return fmt.Errorf("not found")
}

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

	snapshotDir := filepath.Join("lib", "snapshots")
	if ansi, err := os.ReadFile(filepath.Join(snapshotDir, name+"_bubbletea.txt")); err == nil {
		sb.WriteString("## Preview\n\n```\n")
		sb.Write(ansi)
		sb.WriteString("\n```\n\n")
	}

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

func runSNGLCompile(snglFile, outDir string) error {
	cmd := exec.Command(os.Args[0], "generate", "--out", outDir, snglFile)
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
