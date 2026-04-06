package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/snapshot"
	"github.com/spf13/cobra"
)

var docExamplesCmd = &cobra.Command{
	Use:   "doc-examples",
	Short: "Render component example screenshots from doc comments",
	Long: `Extract example SNGL apps from component doc comments and render
them to PNG screenshots at deterministic paths.

Examples are defined in doc comments using the format:

  // Example:
  //
  //   component main {
  //       button(text="Click me")
  //   }

Screenshots are written to {out}/assets/gallery/{component}-{platform}.png.
The docsgen tool will use these pre-rendered images if they exist.`,
	RunE: runDocExamples,
}

func init() {
	docExamplesCmd.Flags().StringP("out", "o", "_site", "output directory")
	docExamplesCmd.Flags().StringSlice("platform", []string{"html"}, "platforms to snapshot")
	docExamplesCmd.Flags().Int("width", 800, "viewport width")
	docExamplesCmd.Flags().Int("height", 400, "viewport height")
	docCmd.AddCommand(docExamplesCmd)
}

func runDocExamples(cmd *cobra.Command, args []string) error {
	outDir, _ := cmd.Flags().GetString("out")
	platforms, _ := cmd.Flags().GetStringSlice("platform")
	width, _ := cmd.Flags().GetInt("width")
	height, _ := cmd.Flags().GetInt("height")

	// Extract examples from stdlib doc comments.
	examples, err := checker.StdlibExamples()
	if err != nil {
		return fmt.Errorf("extracting stdlib examples: %w", err)
	}

	if len(examples) == 0 {
		fmt.Println("No examples found in stdlib doc comments.")
		return nil
	}

	galleryDir := filepath.Join(outDir, "assets", "gallery")
	if err := os.MkdirAll(galleryDir, 0o755); err != nil {
		return err
	}

	// Write each example to a temp file, snapshot it, then clean up.
	tmpDir, err := os.MkdirTemp("", "sngl-doc-examples-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	for name, src := range examples {
		// Wrap in output block if the example doesn't have one.
		if !strings.Contains(src, "output {") {
			var wrapped strings.Builder
			wrapped.WriteString("output {\n")
			for _, plat := range platforms {
				lang := langForPlatform(plat)
				fmt.Fprintf(&wrapped, "    %s { %s }\n", lang, plat)
			}
			wrapped.WriteString("}\n\n")
			wrapped.WriteString(src)
			src = wrapped.String()
		}

		tmpFile := filepath.Join(tmpDir, name+".sngl")
		if err := os.WriteFile(tmpFile, []byte(src), 0o644); err != nil {
			return fmt.Errorf("writing temp file for %s: %w", name, err)
		}

		results, err := snapshot.Generate(snapshot.Config{
			SourceFile: tmpFile,
			Platforms:  platforms,
			Width:      width,
			Height:     height,
			OutDir:     galleryDir,
			Prefix:     name,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", name, err)
			continue
		}
		for _, r := range results {
			fmt.Println(r.Path)
		}
	}

	return nil
}

func langForPlatform(platform string) string {
	switch platform {
	case "html":
		return "js"
	case "bubbletea", "fyne":
		return "go"
	case "android":
		return "kotlin"
	default:
		return "js"
	}
}
