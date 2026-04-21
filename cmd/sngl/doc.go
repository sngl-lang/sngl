package main

import (
	"encoding/json"
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
			return showDeclDoc(pkgDocs, args[1], doc.Stmts)
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
			return showDeclDoc(checker.ExtractPackageDocs(doc), declName, doc.Stmts)
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
			return showDeclDoc(pd, first, doc.Stmts)
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
		return renderToTerminal(renderUserComponentDoc(decl, stmts))

	case *ast.StructDef:
		return renderToTerminal(renderStructDoc(decl, fieldName, stmts))

	case *ast.EnumDef:
		return renderToTerminal(renderEnumDoc(decl, stmts))

	default:
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("# %s\n\n", info.Name))
		if info.Doc != "" {
			sb.WriteString(info.Doc + "\n")
		}
		return renderToTerminal(sb.String())
	}
}

func renderUserComponentDoc(comp *ast.ComponentDecl, stmts []ast.Stmt) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", comp.Name))

	doc := checker.DeclDoc(stmts)
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
		sb.WriteString("## Parameters\n\n")
		for _, p := range params {
			pType := "any"
			if p.Type != nil {
				pType = fmt.Sprint(p.Type)
			}
			sb.WriteString(fmt.Sprintf("  %-16s %s\n", p.Name, pType))
		}
		sb.WriteString("\n")
	}

	if len(events) > 0 {
		sb.WriteString("## Events\n\n")
		for _, e := range events {
			sb.WriteString(fmt.Sprintf("  @%s\n", e.Name))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func renderStructDoc(s *ast.StructDef, fieldName string, stmts []ast.Stmt) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# struct %s\n\n", s.Name))

	doc := checker.DeclDoc(stmts)
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

func renderEnumDoc(e *ast.EnumDef, stmts []ast.Stmt) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# enum %s\n\n", e.Name))

	doc := checker.DeclDoc(stmts)
	if doc != "" {
		sb.WriteString(doc + "\n\n")
	}

	sb.WriteString("## Values\n\n")
	for _, m := range e.Members {
		sb.WriteString(fmt.Sprintf("  %s\n", m.Name))
	}
	sb.WriteString("\n")
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
				fmt.Fprintf(&b, "<tr><td class='pn'>%s</td><td class='pt'>%s</td></tr>", f.Name, fmt.Sprint(f.Type))
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
	snapshotDir := filepath.Join("internal", "checker", "stdlib", "snapshots")
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
