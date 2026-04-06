package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/docsite"
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

	docCmd.Flags().String("http", "", "start doc server at address (e.g., :6060)")

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

	if len(args) == 0 {
		docsDir, _ := findDocsDir()
		return showTopicListWithComponents(docsDir)
	}

	first := args[0]

	// Two-arg form: sngl doc <dir> <decl|platform>
	if len(args) >= 2 {
		dir := resolvePackageDir(first)
		if dir != "" {
			doc, err := parsePackage(dir)
			if err != nil {
				return err
			}
			pkgDocs := checker.ExtractPackageDocs(doc)
			plat := codegen.LookupPlatform(args[1])
			if plat != nil {
				return showPlatformDocs(args[1], plat)
			}
			return showDeclDoc(pkgDocs, args[1], doc.Comments)
		}
	}

	// Single arg: try multiple resolution strategies.

	// 1. Explicit directory or .sngl file
	if dir := resolvePackageDir(first); dir != "" {
		doc, err := parsePackage(dir)
		if err != nil {
			return err
		}
		return showPackageIndex(dir, checker.ExtractPackageDocs(doc))
	}

	// 2. path.Decl syntax: "examples/todo.Todo" → package=examples/todo, decl=Todo
	if dotIdx := strings.LastIndex(first, "."); dotIdx > 0 {
		pkgPath := first[:dotIdx]
		declName := first[dotIdx+1:]
		if dir := resolvePackageDir(pkgPath); dir != "" {
			doc, err := parsePackage(dir)
			if err != nil {
				return err
			}
			return showDeclDoc(checker.ExtractPackageDocs(doc), declName, doc.Comments)
		}
	}

	// 3. Platform name: "android" → show platform docs
	if plat := codegen.LookupPlatform(first); plat != nil {
		return showPlatformDocs(first, plat)
	}

	// 4. Platform name with fallback to current dir: "android" could also mean
	//    "show android extensions for ." — already handled by case 3.

	// 5. Try as a declaration in the current directory
	if doc, err := parsePackage("."); err == nil {
		pd := checker.ExtractPackageDocs(doc)
		if info := pd.FindDecl(first); info != nil {
			return showDeclDoc(pd, first, doc.Comments)
		}
	}

	// 6. Documentation file lookup
	docsDir, _ := findDocsDir()
	if docsDir != "" {
		if err := showTopic(docsDir, first); err == nil {
			return nil
		}
	}

	// 7. Stdlib component reference
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

// showPackageIndex displays all declarations in a user package.
// Platform overrides (sngl.*) and platform-specific types (Options) are
// separated into their own sections at the bottom.
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

// showPlatformDocs shows the declarations from a platform's PkgSource.
func showPlatformDocs(name string, plat codegen.PlatformGenerator) error {
	src := plat.PkgSource()
	if src == "" {
		return fmt.Errorf("platform %q has no package source", name)
	}
	doc, err := parser.Parse(name+".sngl", strings.NewReader(src))
	if err != nil {
		return fmt.Errorf("parsing %s package: %w", name, err)
	}
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
func showDeclDoc(pd *checker.PackageDocs, name string, comments []ast.Comment) error {
	// Support "decl.field" syntax
	declName := name
	fieldName := ""
	if before, after, ok := strings.Cut(name, "."); ok {
		declName = before
		fieldName = after
	}

	info := pd.FindDecl(declName)
	if info == nil {
		// Try stdlib fallback
		return showComponentDoc(name)
	}

	switch decl := info.Decl.(type) {
	case *ast.Component:
		// Reuse stdlib component doc rendering via schema
		registry, _, _, _, _, _, err := checker.LoadStdlib()
		if err == nil {
			if schema, ok := registry[declName]; ok {
				if fieldName != "" {
					return showPropDoc(declName, fieldName, schema)
				}
				return renderToTerminal(renderComponentDoc(declName, schema))
			}
		}
		// User component — render manually
		return renderToTerminal(renderUserComponentDoc(decl, comments))

	case *ast.StructDef:
		return renderToTerminal(renderStructDoc(decl, fieldName, comments))

	case *ast.EnumDef:
		return renderToTerminal(renderEnumDoc(decl, comments))

	default:
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("# %s\n\n", info.Name))
		if info.Doc != "" {
			sb.WriteString(info.Doc + "\n")
		}
		return renderToTerminal(sb.String())
	}
}

func renderUserComponentDoc(comp *ast.Component, comments []ast.Comment) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", comp.Name))

	doc := checker.DeclDoc(comments, comp.Pos.Line)
	if doc != "" {
		sb.WriteString(doc + "\n\n")
	}

	if len(comp.Params) > 0 {
		sb.WriteString("## Parameters\n\n")
		for _, p := range comp.Params {
			pType := p.Default.TypeHint
			if pType == "" {
				pType = "any"
			}
			sb.WriteString(fmt.Sprintf("  %-16s %s\n", p.Name, pType))
		}
		sb.WriteString("\n")
	}

	if len(comp.EventDecls) > 0 {
		sb.WriteString("## Events\n\n")
		for _, e := range comp.EventDecls {
			sb.WriteString(fmt.Sprintf("  @%s\n", e.Name))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func renderStructDoc(s *ast.StructDef, fieldName string, comments []ast.Comment) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# struct %s\n\n", s.Name))

	doc := checker.DeclDoc(comments, s.Pos.Line)
	if doc != "" {
		sb.WriteString(doc + "\n\n")
	}

	if fieldName != "" {
		// Show specific field
		for _, f := range s.Fields {
			if f.Name == fieldName {
				sb.WriteString(fmt.Sprintf("## %s.%s\n\n", s.Name, f.Name))
				sb.WriteString(fmt.Sprintf("Type: %s\n", f.Type))
				return sb.String()
			}
		}
		sb.WriteString(fmt.Sprintf("field %q not found\n", fieldName))
		return sb.String()
	}

	sb.WriteString("## Fields\n\n")
	for _, f := range s.Fields {
		sb.WriteString(fmt.Sprintf("  %-16s %s\n", f.Name, f.Type))
	}
	sb.WriteString("\n")
	return sb.String()
}

func renderEnumDoc(e *ast.EnumDef, comments []ast.Comment) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# enum %s\n\n", e.Name))

	doc := checker.DeclDoc(comments, e.Pos.Line)
	if doc != "" {
		sb.WriteString(doc + "\n\n")
	}

	sb.WriteString("## Values\n\n")
	for _, v := range e.Values {
		sb.WriteString(fmt.Sprintf("  %s\n", v))
	}
	sb.WriteString("\n")
	return sb.String()
}

// serveDocHTTP starts a dynamic doc server that re-parses on each request.
func serveDocHTTP(addr, dir string) error {
	fmt.Printf("Serving docs for %s on http://%s\n", dir, addr)
	return http.ListenAndServe(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		doc, err := parsePackage(dir)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		pd := checker.ExtractPackageDocs(doc)

		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" || path == "index.html" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, "<html><head><title>%s</title></head><body>", dir)
			fmt.Fprintf(w, "<h1>Package %s</h1>", dir)

			writeHTMLSection(w, "Components", pd.Components)
			writeHTMLSection(w, "Types", pd.Structs)
			writeHTMLSection(w, "Enums", pd.Enums)
			writeHTMLSection(w, "Data", pd.Data)
			writeHTMLSection(w, "Functions", pd.Functions)
			writeHTMLSection(w, "Constants", pd.Consts)

			fmt.Fprintf(w, "</body></html>")
			return
		}

		// Lookup declaration
		info := pd.FindDecl(path)
		if info == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "<html><head><title>%s</title></head><body>", info.Name)
		fmt.Fprintf(w, "<h1>%s</h1>", info.Name)
		if info.Doc != "" {
			fmt.Fprintf(w, "<p>%s</p>", info.Doc)
		}
		fmt.Fprintf(w, "<p><a href=\"/\">← Back</a></p>")
		fmt.Fprintf(w, "</body></html>")
	}))
}

func writeHTMLSection(w http.ResponseWriter, title string, items []checker.DeclInfo) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(w, "<h2>%s</h2><ul>", title)
	for _, d := range items {
		fmt.Fprintf(w, "<li><a href=\"/%s\"><b>%s</b></a>", d.Name, d.Name)
		if d.Doc != "" {
			fmt.Fprintf(w, " — %s", d.Doc)
		}
		fmt.Fprintf(w, "</li>")
	}
	fmt.Fprintf(w, "</ul>")
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
	registry, _, _, _, _, _, err := checker.LoadStdlib()
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
	registry, _, _, _, _, _, err := checker.LoadStdlib()
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
	snapshotDir := filepath.Join("internal", "checker", "stdlib", "snapshots")
	if ansi, err := os.ReadFile(filepath.Join(snapshotDir, name+"_bubbletea.txt")); err == nil {
		sb.WriteString("## Preview\n\n```\n")
		sb.Write(ansi)
		sb.WriteString("\n```\n\n")
	}

	// Show example source if available
	examples, _ := checker.StdlibExamples()
	if src, ok := examples[name]; ok {
		sb.WriteString("## Example\n\n```sngl\n")
		sb.WriteString(src)
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
			line := fmt.Sprintf("%-16s %s", p.name, p.ps.Type)
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
	sb.WriteString(fmt.Sprintf("Type: %s\n\n", ps.Type))
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
