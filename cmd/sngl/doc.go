package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/docsite"
	"github.com/charmbracelet/glamour"
	"github.com/spf13/cobra"
)

var docCmd = &cobra.Command{
	Use:   "doc [topic]",
	Short: "Show SNGL documentation",
	Long:  "Show SNGL language documentation in the terminal.\nUse 'sngl doc build' to generate the HTML documentation site.",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runDoc,
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

	docCmd.AddCommand(docBuildCmd)
	docCmd.AddCommand(docServeCmd)
}

func runDoc(cmd *cobra.Command, args []string) error {
	docsDir, _ := findDocsDir()

	if len(args) == 0 {
		return showTopicListWithComponents(docsDir)
	}
	topic := args[0]

	// Try documentation file lookup first.
	if docsDir != "" {
		if err := showTopic(docsDir, topic); err == nil {
			return nil
		}
	}

	// Fall back to component reference.
	return showComponentDoc(topic)
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
	if schema.Doc != "" {
		sb.WriteString(schema.Doc)
		sb.WriteString("\n\n")
	}

	if len(schema.Props) > 0 {
		sb.WriteString("## Properties\n\n")
		// Sort props by name.
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
			line := fmt.Sprintf("  %-16s %s", p.name, p.ps.Type)
			if len(p.ps.Enum) > 0 {
				line += fmt.Sprintf("  (%s)", strings.Join(p.ps.Enum, ", "))
			}
			sb.WriteString(line + "\n")
			if p.ps.Doc != "" {
				sb.WriteString(fmt.Sprintf("                   %s\n", p.ps.Doc))
			}
		}
		sb.WriteString("\n")
	}

	if len(schema.Events) > 0 {
		sb.WriteString("## Events\n\n")
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
			sb.WriteString(fmt.Sprintf("  %-16s %s\n", e.name, e.payload))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(fmt.Sprintf("## Children: %s\n", docsite.ChildPolicyString(schema.Children)))

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
