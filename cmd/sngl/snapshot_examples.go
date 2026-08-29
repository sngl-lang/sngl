package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/snapshot"
	"github.com/spf13/cobra"
)

var snapshotExamplesCmd = &cobra.Command{
	Use:   "examples [file|dir...]",
	Short: "Generate screenshots from doc comment examples",
	Long: `Extract example SNGL apps from doc comments in .sngl files and render
them to PNG screenshots at deterministic paths.

Examples are defined in doc comments above component declarations:

  // Example:
  //
  //   component main {
  //       button(text="Click me")
  //   }
  component MyButton(...) { ... }

Screenshots are written to {dir}/snapshots/{component}-{platform}.png.

With --stdlib, generates screenshots for the built-in stdlib components.`,
	RunE: runSnapshotExamples,
}

func init() {
	snapshotExamplesCmd.Flags().StringSlice("platform", []string{"html"}, "platforms to snapshot")
	snapshotExamplesCmd.Flags().Int("width", 800, "viewport width")
	snapshotExamplesCmd.Flags().Int("height", 400, "viewport height")
	snapshotExamplesCmd.Flags().Bool("recurse", false, "recurse into subdirectories")
	snapshotExamplesCmd.Flags().Bool("force", false, "regenerate even if screenshots exist")
	snapshotExamplesCmd.Flags().Bool("stdlib", false, "generate stdlib component examples")
	snapshotCmd.AddCommand(snapshotExamplesCmd)
}

func runSnapshotExamples(cmd *cobra.Command, args []string) error {
	platforms, _ := cmd.Flags().GetStringSlice("platform")
	width, _ := cmd.Flags().GetInt("width")
	height, _ := cmd.Flags().GetInt("height")
	recurse, _ := cmd.Flags().GetBool("recurse")
	force, _ := cmd.Flags().GetBool("force")
	stdlib, _ := cmd.Flags().GetBool("stdlib")

	if stdlib {
		return snapshotStdlibExamples(platforms, width, height, force)
	}

	paths := args
	if len(paths) == 0 {
		paths = []string{"."}
	}

	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := snapshotExamplesDir(p, platforms, width, height, recurse, force); err != nil {
				return err
			}
		} else {
			if err := snapshotExamplesFile(p, platforms, width, height, force); err != nil {
				return err
			}
		}
	}
	return nil
}

func snapshotExamplesDir(dir string, platforms []string, width, height int, recurse, force bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() && recurse {
			if err := snapshotExamplesDir(path, platforms, width, height, recurse, force); err != nil {
				return err
			}
			continue
		}
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sngl") {
			if err := snapshotExamplesFile(path, platforms, width, height, force); err != nil {
				fmt.Fprintf(os.Stderr, "warning: %s: %v\n", path, err)
			}
		}
	}
	return nil
}

func snapshotExamplesFile(path string, platforms []string, width, height int, force bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	doc, err := parser.Parse(path, data)
	if err != nil {
		return err
	}

	examples := checker.PrefixedExamples(doc)
	if len(examples) == 0 {
		return nil
	}

	dir := filepath.Dir(path)
	outDir := filepath.Join(dir, "snapshots")

	return renderExamples(examples, outDir, platforms, width, height, force)
}

func snapshotStdlibExamples(platforms []string, width, height int, force bool) error {
	examples, err := checker.StdlibExamples()
	if err != nil {
		return fmt.Errorf("extracting stdlib examples: %w", err)
	}
	flat := make(map[string]string, len(examples))
	for name, srcs := range examples {
		if len(srcs) > 0 {
			flat[name] = srcs[0]
		}
	}
	if len(flat) == 0 {
		fmt.Println("No examples found in stdlib.")
		return nil
	}

	outDir := filepath.Join("lib", "snapshots")

	return renderExamples(flat, outDir, platforms, width, height, force)
}

func renderExamples(examples map[string]string, outDir string, platforms []string, width, height int, force bool) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp("", "sngl-examples-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	for name, src := range examples {
		if !force {
			allExist := true
			for _, plat := range platforms {
				target := filepath.Join(outDir, name+"_"+plat+".png")
				if _, err := os.Stat(target); err != nil {
					allExist = false
					break
				}
			}
			if allExist {
				for _, plat := range platforms {
					fmt.Printf("%s (exists)\n", filepath.Join(outDir, name+"_"+plat+".png"))
				}
				continue
			}
		}

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
			OutDir:     outDir,
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
