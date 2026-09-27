package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/snapshot"
	"git.duckfam.us/jonathan/sngl/lib"
	"github.com/spf13/cobra"
)

var snapshotExamplesCmd = &cobra.Command{
	Use:   "examples [file|dir...]",
	Short: "Generate screenshots from doc comment examples",
	Long: `Render the example components in .sngl files to PNG screenshots at
deterministic paths.

An example is a component named _example_<name>, which documents <name>:

  component _example_button node {
      button(text="Click me")
  }

Each is rendered in a window of its own, with the imports of the file it was
written in.

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
	for name, src := range examples {
		examples[name] = exampleSource(src, platforms)
	}

	dir := filepath.Dir(path)
	outDir := filepath.Join(dir, "snapshots")

	return renderExamples(examples, outDir, platforms, width, height, force)
}

func snapshotStdlibExamples(platforms []string, width, height int, force bool) error {
	flat := stdlibExampleSources(platforms)
	if len(flat) == 0 {
		fmt.Println("No examples found in stdlib.")
		return nil
	}

	outDir := filepath.Join("lib", "snapshots")

	return renderExamples(flat, outDir, platforms, width, height, force)
}

func stdlibExampleSources(platforms []string) map[string]string {
	flat := map[string]string{}
	for _, pkg := range lib.PublicPackages() {
		for name, srcs := range checker.PackageExamples(pkg) {
			if len(srcs) > 0 {
				flat[name] = exampleSource(srcs[0], platforms)
			}
		}
	}
	return flat
}

// exampleSource is an example as a file snapshot.Generate builds for platforms.
func exampleSource(example string, platforms []string) string {
	var b strings.Builder
	b.WriteString(example)
	b.WriteString("\noutput {\n")
	for _, plat := range platforms {
		fmt.Fprintf(&b, "    %s { %s }\n", snapshot.LangForPlatform(plat), plat)
	}
	b.WriteString("}\n")
	return b.String()
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
