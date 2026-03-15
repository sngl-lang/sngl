package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	docBuildCmd.Flags().StringP("docs", "d", "docs", "documentation source directory")
	docBuildCmd.Flags().StringP("out", "o", "_site", "output directory")

	docServeCmd.Flags().StringP("docs", "d", "docs", "documentation source directory")
	docServeCmd.Flags().StringP("out", "o", "_site", "output directory")
	docServeCmd.Flags().IntP("port", "p", 8080, "port to serve on")

	docCmd.AddCommand(docBuildCmd)
	docCmd.AddCommand(docServeCmd)
}

func runDoc(cmd *cobra.Command, args []string) error {
	docsDir, _ := findDocsDir()
	if docsDir == "" {
		return fmt.Errorf("cannot find docs/ directory")
	}

	if len(args) == 0 {
		return showTopicList(docsDir)
	}
	return showTopic(docsDir, args[0])
}

func runDocBuild(cmd *cobra.Command, args []string) error {
	docsDir, _ := cmd.Flags().GetString("docs")
	outDir, _ := cmd.Flags().GetString("out")
	if err := docsite.Build(docsDir, outDir); err != nil {
		return err
	}
	fmt.Printf("Site built in %s/\n", outDir)
	return nil
}

func runDocServe(cmd *cobra.Command, args []string) error {
	docsDir, _ := cmd.Flags().GetString("docs")
	outDir, _ := cmd.Flags().GetString("out")
	port, _ := cmd.Flags().GetInt("port")
	if err := docsite.Build(docsDir, outDir); err != nil {
		return err
	}
	return docsite.Serve(outDir, port)
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

func showTopicList(docsDir string) error {
	topics, err := listTopics(docsDir)
	if err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString("# SNGL Documentation\n\n")
	sb.WriteString("SNGL is a purpose-built language for describing reactive, cross-platform UIs.\n\n")
	sb.WriteString("## Topics\n\n")
	for _, t := range topics {
		sb.WriteString(fmt.Sprintf("- **%s** — %s\n", t.name, t.desc))
	}
	sb.WriteString("\nRun `sngl doc <topic>` to read about a topic.\n")
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

	// Try fuzzy match
	topics, _ := listTopics(docsDir)
	var matches []string
	for _, t := range topics {
		if strings.Contains(strings.ToLower(t.name), strings.ToLower(topic)) {
			matches = append(matches, t.name)
		}
	}
	if len(matches) > 0 {
		return fmt.Errorf("topic %q not found. Did you mean: %s", topic, strings.Join(matches, ", "))
	}
	return fmt.Errorf("topic %q not found. Run 'sngl doc' to see available topics", topic)
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
