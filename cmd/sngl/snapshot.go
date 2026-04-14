package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/snapshot"
	"github.com/spf13/cobra"
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot [file|dir...]",
	Short: "Generate platform screenshots",
	Long: `Generate screenshots for .sngl files.

Snapshots example_<name> prefixed components and main-package output targets.
If --platform is omitted, all targets from the output {} block are used.
Existing screenshots are skipped unless --force is set.`,
	RunE: runSnapshot,
}

func init() {
	snapshotCmd.Flags().StringSlice("platform", nil, "platforms to snapshot (default: all from output block)")
	snapshotCmd.Flags().String("out", "", "output directory (default: <dir>/snapshots)")
	snapshotCmd.Flags().Int("width", 800, "viewport width")
	snapshotCmd.Flags().Int("height", 400, "viewport height")
	snapshotCmd.Flags().Bool("recurse", false, "recurse into subdirectories")
	snapshotCmd.Flags().Bool("force", false, "regenerate even if outputs already exist")
}

func runSnapshot(cmd *cobra.Command, args []string) error {
	platforms, _ := cmd.Flags().GetStringSlice("platform")
	outDir, _ := cmd.Flags().GetString("out")
	width, _ := cmd.Flags().GetInt("width")
	height, _ := cmd.Flags().GetInt("height")
	recurse, _ := cmd.Flags().GetBool("recurse")
	force, _ := cmd.Flags().GetBool("force")

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
			if err := snapshotDir(p, platforms, outDir, width, height, recurse, force); err != nil {
				return err
			}
		} else {
			if err := snapshotFile(p, platforms, outDir, width, height, force); err != nil {
				return err
			}
		}
	}
	return nil
}

func snapshotDir(dir string, platforms []string, outDir string, width, height int, recurse, force bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() && recurse {
			if err := snapshotDir(path, platforms, outDir, width, height, recurse, force); err != nil {
				return err
			}
			continue
		}
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sngl") {
			if err := snapshotFile(path, platforms, outDir, width, height, force); err != nil {
				slog.Warn("snapshot failed", "path", path, "err", err)
			}
		}
	}
	return nil
}

func snapshotFile(path string, flagPlatforms []string, outOverride string, width, height int, force bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	doc, err := parser.Parse(path, data)
	if err != nil {
		return err
	}

	// Determine platforms: from flag, or from the file's output {} block.
	platforms := flagPlatforms
	if len(platforms) == 0 {
		pkg, err := checkDoc(doc, filepath.Dir(path), true)
		if err == nil && pkg != nil {
			for _, o := range pkg.Outputs {
				platforms = append(platforms, o.Platform)
			}
		}
	}
	if len(platforms) == 0 {
		return nil
	}

	// Determine output directory.
	fileDir := filepath.Dir(path)
	effectiveOutDir := outOverride
	if effectiveOutDir == "" {
		effectiveOutDir = filepath.Join(fileDir, "snapshots")
	}
	if err := os.MkdirAll(effectiveOutDir, 0o755); err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp("", "sngl-snapshot-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	var docs []snapshot.DocEntry

	// Examples: snapshot each example_* prefixed component.
	examples := checker.PrefixedExamples(doc)
	for name, src := range examples {
		if !force && snapshotAllExist(effectiveOutDir, name, platforms) {
			for _, plat := range platforms {
				slog.Info("exists", "path", filepath.Join(effectiveOutDir, name+"_"+plat+".png"))
			}
			continue
		}

		// Rename to "component main" and wrap with an output block.
		if i := strings.Index(src, "{"); i > 0 {
			src = "component main " + src[i:]
		}
		if !strings.Contains(src, "output {") {
			var wrapped strings.Builder
			wrapped.WriteString("output {\n")
			for _, plat := range platforms {
				fmt.Fprintf(&wrapped, "    %s { %s }\n", snapshot.LangForPlatform(plat), plat)
			}
			wrapped.WriteString("}\n\n")
			wrapped.WriteString(src)
			src = wrapped.String()
		}

		tmpFile := filepath.Join(tmpDir, name+".sngl")
		if err := os.WriteFile(tmpFile, []byte(src), 0o644); err != nil {
			return fmt.Errorf("writing temp file for %s: %w", name, err)
		}
		docs = append(docs, snapshot.DocEntry{ID: name, SourceFile: tmpFile})
	}

	// Main app: if the file has an output {} block, snapshot it too.
	if len(platforms) > 0 {
		basename := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if !force && snapshotAllExist(effectiveOutDir, basename, platforms) {
			for _, plat := range platforms {
				slog.Info("exists", "path", filepath.Join(effectiveOutDir, basename+"_"+plat+".png"))
			}
		} else {
			docs = append(docs, snapshot.DocEntry{ID: basename, SourceFile: path})
		}
	}

	if len(docs) == 0 {
		return nil
	}

	results, err := snapshot.GenerateBatch(snapshot.BatchConfig{
		Docs:      docs,
		Platforms: platforms,
		Width:     width,
		Height:    height,
		OutDir:    effectiveOutDir,
	})
	if err != nil {
		return err
	}
	for _, r := range results {
		slog.Info("wrote", "path", r.Path)
	}
	return nil
}

// snapshotAllExist reports whether all platform PNG outputs already exist for id.
func snapshotAllExist(outDir, id string, platforms []string) bool {
	for _, plat := range platforms {
		if _, err := os.Stat(filepath.Join(outDir, id+"_"+plat+".png")); err != nil {
			return false
		}
	}
	return true
}
