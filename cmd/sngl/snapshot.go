package main

import (
	"fmt"
	"git.duckfam.us/jonathan/sngl/internal/build"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/snapshot"
	"github.com/spf13/cobra"
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot [file|dir...]",
	Short: "Generate platform screenshots",
	Long: `Generate screenshots for .sngl files.

Snapshots _example_<name> components and main-package output targets.
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
	if recurse {
		expanded, err := recurseDirs(paths)
		if err != nil {
			return err
		}
		paths = expanded
	}

	units, err := resolveUnits(paths)
	if err != nil {
		return err
	}
	for _, u := range units {
		if err := snapshotUnit(u, platforms, outDir, width, height, force); err != nil {
			slog.Warn("snapshot failed", "path", u.headline(), "err", err)
		}
	}
	return nil
}

// recurseDirs expands each directory argument into itself and every
// subdirectory. resolveUnits reads a directory as one package and does not
// descend, so --recurse is a widening of the argument list rather than
// anything the unit resolver has to know about.
func recurseDirs(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			out = append(out, p)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func snapshotUnit(u unit, flagPlatforms []string, outOverride string, width, height int, force bool) error {
	doc, err := u.doc()
	if err != nil {
		return err
	}
	path := u.headline()

	platforms := flagPlatforms
	if len(platforms) == 0 {
		pkg, err := checkDoc(doc, u.dir, true)
		if err == nil && pkg != nil {
			for _, o := range pkg.Outputs {
				platforms = append(platforms, o.Platform)
			}
		}
	}
	if len(platforms) == 0 {
		return nil
	}

	fileDir := u.dir
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

	examples := checker.PrefixedExamples(doc)
	imports := checker.DocumentExampleImports(doc)
	for name, src := range examples {
		if !force && snapshotAllExist(effectiveOutDir, name, platforms) {
			for _, plat := range platforms {
				slog.Info("exists", "path", filepath.Join(effectiveOutDir, name+"_"+plat+".png"))
			}
			continue
		}

		tmpFile := filepath.Join(tmpDir, name+".sngl")
		if err := os.WriteFile(tmpFile, []byte(exampleSource(imports, src, platforms)), 0o644); err != nil {
			return fmt.Errorf("writing temp file for %s: %w", name, err)
		}
		docs = append(docs, snapshot.DocEntry{ID: name, SourceFile: tmpFile})
	}

	// Without the output {} guard, a unit holding only component definitions
	// would be added as a main doc and fail to type-check.
	if len(platforms) > 0 && declaresOutput(doc) {
		basename := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if !u.solo {
			basename = filepath.Base(u.dir)
		}
		if !force && snapshotAllExist(effectiveOutDir, basename, platforms) {
			for _, plat := range platforms {
				slog.Info("exists", "path", filepath.Join(effectiveOutDir, basename+"_"+plat+".png"))
			}
		} else {
			docs = append(docs, snapshot.DocEntry{ID: basename, SourceFile: path, Doc: doc, Dir: u.dir, Resolver: build.NewResolver(u.dir)})
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

// declaresOutput reports whether the document carries an `output` block. A
// package's is in whichever file wrote it, so this reads the merged document
// rather than any one file's text.
func declaresOutput(doc *ast.Document) bool {
	for _, stmt := range doc.Stmts {
		if vn, ok := stmt.(*ast.VisualNode); ok {
			if id, ok := vn.Target.(*ast.IdentExpr); ok && id.Name == "output" {
				return true
			}
		}
	}
	return false
}

func snapshotAllExist(outDir, id string, platforms []string) bool {
	for _, plat := range platforms {
		if _, err := os.Stat(filepath.Join(outDir, id+"_"+plat+".png")); err != nil {
			return false
		}
	}
	return true
}
