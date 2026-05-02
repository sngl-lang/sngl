package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/optimize"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Config controls snapshot generation.
type Config struct {
	SourceFile string   // path to .sngl file
	Platforms  []string // e.g. ["html", "bubbletea"]; empty = all from output block
	Width      int      // viewport width (default 1280)
	Height     int      // viewport height (default 720)
	OutDir     string   // directory to write PNGs
	Prefix     string   // filename prefix (default: source file base name without extension)
}

// Result describes a generated screenshot.
type Result struct {
	Platform string
	Lang     string
	Path     string // output PNG path
}

// htmlSnapshotter is satisfied by HTML Generator's SnapshotHTML method.
type htmlSnapshotter interface {
	SnapshotHTML(html []byte, width, height int) ([]byte, error)
}

// Generate produces screenshots for each platform target.
func Generate(cfg Config) ([]Result, error) {
	if cfg.Width == 0 {
		cfg.Width = 1280
	}
	if cfg.Height == 0 {
		cfg.Height = 720
	}

	sourceFile, err := filepath.Abs(cfg.SourceFile)
	if err != nil {
		return nil, err
	}

	type target struct {
		platform string
		lang     string
	}

	var targets []target
	if len(cfg.Platforms) > 0 {
		for _, p := range cfg.Platforms {
			targets = append(targets, target{platform: p, lang: LangForPlatform(p)})
		}
	} else {
		outputs, err := CheckOutputs(sourceFile)
		if err != nil {
			return nil, fmt.Errorf("parsing outputs: %w", err)
		}
		if len(outputs) == 0 {
			return nil, fmt.Errorf("no output targets defined in %s", cfg.SourceFile)
		}
		for _, o := range outputs {
			targets = append(targets, target{platform: o.Platform, lang: o.Lang})
		}
	}

	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return nil, err
	}

	var results []Result
	for _, t := range targets {
		pngBytes, err := snapshotTarget(sourceFile, t.platform, t.lang, cfg.Width, cfg.Height)
		if err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", t.platform, err)
		}

		prefix := cfg.Prefix
		if prefix == "" {
			prefix = strings.TrimSuffix(filepath.Base(cfg.SourceFile), filepath.Ext(cfg.SourceFile))
		}
		outPath := filepath.Join(cfg.OutDir, prefix+"_"+t.platform+".png")
		if err := os.WriteFile(outPath, pngBytes, 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", outPath, err)
		}

		results = append(results, Result{
			Platform: t.platform,
			Lang:     t.lang,
			Path:     outPath,
		})

		// If the platform supports text snapshots, write a .txt alongside the PNG.
		textBytes, textErr := textSnapshotTarget(sourceFile, t.platform, t.lang, cfg.Width, cfg.Height)
		if textErr == nil && len(textBytes) > 0 {
			txtPath := filepath.Join(cfg.OutDir, prefix+"_"+t.platform+".txt")
			os.WriteFile(txtPath, textBytes, 0o644)
			results = append(results, Result{
				Platform: t.platform,
				Lang:     t.lang,
				Path:     txtPath,
			})
		}
	}

	return results, nil
}

// snapshotTarget captures a screenshot for a single platform target.
func snapshotTarget(sourceFile, platform, lang string, width, height int) ([]byte, error) {
	plat := codegen.LookupPlatform(platform)

	// If the platform implements Snapshotter, use native capture.
	if snapshotter, ok := plat.(codegen.Snapshotter); ok {
		doc, err := ParseSNGL(sourceFile)
		if err != nil {
			return nil, err
		}
		dir := filepath.Dir(sourceFile)
		pkg, err := checkAndReturn(doc, dir)
		if err != nil {
			return nil, err
		}
		optCfg := &optimize.Config{
			Platform: platform,
			Language: lang,
			Dir:      dir,
		}
		if err := optimize.Optimize(pkg, optCfg); err != nil {
			return nil, fmt.Errorf("optimize: %w", err)
		}
		langT := codegen.LookupLang(lang)
		if langT == nil {
			return nil, fmt.Errorf("lang %q not registered", lang)
		}
		caps := plat.Capabilities().Merge(langT.Capabilities())
		if err := lower.Lower(pkg, caps, lower.Options{}); err != nil {
			return nil, fmt.Errorf("lower: %w", err)
		}
		if caps != (lower.Caps{}) {
			if err := optimize.Optimize(pkg, optCfg); err != nil {
				return nil, fmt.Errorf("optimize2: %w", err)
			}
		}
		return snapshotter.Snapshot(pkg, langT, width, height)
	}

	// Platforms without a Snapshotter: compile to HTML preview and screenshot.
	return snapshotViaHTML(sourceFile, platform, lang, width, height)
}

// textSnapshotTarget returns ANSI text for platforms that support TextSnapshotter.
func textSnapshotTarget(sourceFile, platform, lang string, width, height int) ([]byte, error) {
	plat := codegen.LookupPlatform(platform)
	ts, ok := plat.(codegen.TextSnapshotter)
	if !ok {
		return nil, fmt.Errorf("platform %q does not support text snapshots", platform)
	}
	doc, err := ParseSNGL(sourceFile)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(sourceFile)
	pkg, err := checkAndReturn(doc, dir)
	if err != nil {
		return nil, err
	}
	optCfg := &optimize.Config{
		Platform: platform,
		Language: lang,
		Dir:      dir,
	}
	if err := optimize.Optimize(pkg, optCfg); err != nil {
		return nil, fmt.Errorf("optimize: %w", err)
	}
	langT := codegen.LookupLang(lang)
	if langT == nil {
		return nil, fmt.Errorf("lang %q not registered", lang)
	}
	caps := plat.Capabilities().Merge(langT.Capabilities())
	if err := lower.Lower(pkg, caps, lower.Options{}); err != nil {
		return nil, fmt.Errorf("lower: %w", err)
	}
	if caps != (lower.Caps{}) {
		if err := optimize.Optimize(pkg, optCfg); err != nil {
			return nil, fmt.Errorf("optimize2: %w", err)
		}
	}
	return ts.SnapshotText(pkg, langT, width, height)
}

// snapshotViaHTML compiles a preview HTML and uses the HTML platform to screenshot it.
func snapshotViaHTML(sourceFile, platform, lang string, width, height int) ([]byte, error) {
	html, err := CompilePreviewHTML(sourceFile, platform, lang)
	if err != nil {
		return nil, fmt.Errorf("compiling %s: %w", platform, err)
	}

	htmlPlat := codegen.LookupPlatform("html")
	hs, ok := htmlPlat.(htmlSnapshotter)
	if !ok {
		return nil, fmt.Errorf("html platform does not implement SnapshotHTML")
	}

	return hs.SnapshotHTML(html, width, height)
}

// BatchConfig controls batch snapshot generation for multiple documents.
type BatchConfig struct {
	Docs      []DocEntry // documents to snapshot
	Platforms []string   // platforms to snapshot for
	Width     int
	Height    int
	OutDir    string
}

// DocEntry identifies a document for batch snapshotting.
type DocEntry struct {
	ID         string // unique identifier for output naming
	SourceFile string // path to .sngl file
}

// GenerateBatch produces screenshots for multiple documents, using batch
// snapshotting when the platform supports it. This amortises expensive
// build steps (go mod tidy, compilation) across all documents.
func GenerateBatch(cfg BatchConfig) ([]Result, error) {
	if cfg.Width == 0 {
		cfg.Width = 1280
	}
	if cfg.Height == 0 {
		cfg.Height = 720
	}
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return nil, err
	}

	// Parse and check all documents upfront.
	type parsedDoc struct {
		entry DocEntry
		pkg   *ir.Package
	}
	var parsed []parsedDoc
	for _, entry := range cfg.Docs {
		sourceFile, err := filepath.Abs(entry.SourceFile)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.ID, err)
		}
		doc, err := ParseSNGL(sourceFile)
		if err != nil {
			return nil, fmt.Errorf("%s: parse: %w", entry.ID, err)
		}
		dir := filepath.Dir(sourceFile)
		pkg, err := checkAndReturn(doc, dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.ID, err)
		}
		parsed = append(parsed, parsedDoc{entry: entry, pkg: pkg})
	}

	var results []Result

	for _, platform := range cfg.Platforms {
		lang := LangForPlatform(platform)
		langT := codegen.LookupLang(lang)
		if langT == nil {
			return nil, fmt.Errorf("lang %q not registered", lang)
		}
		plat := codegen.LookupPlatform(platform)

		// Try batch path first.
		if bs, ok := plat.(codegen.BatchSnapshotter); ok && len(parsed) > 1 {
			var batchDocs []codegen.BatchDoc
			for _, p := range parsed {
				batchDocs = append(batchDocs, codegen.BatchDoc{
					ID:   p.entry.ID,
					Pkg:  p.pkg,
					Lang: langT,
				})
			}

			pngs, err := bs.BatchSnapshot(batchDocs, cfg.Width, cfg.Height)
			if err != nil {
				return nil, fmt.Errorf("batch snapshot %s: %w", platform, err)
			}
			for _, p := range parsed {
				png, ok := pngs[p.entry.ID]
				if !ok {
					continue
				}
				outPath := filepath.Join(cfg.OutDir, p.entry.ID+"_"+platform+".png")
				if err := os.WriteFile(outPath, png, 0o644); err != nil {
					return nil, fmt.Errorf("writing %s: %w", outPath, err)
				}
				results = append(results, Result{Platform: platform, Lang: lang, Path: outPath})
			}

			// Batch text snapshots.
			if bts, ok := plat.(codegen.BatchTextSnapshotter); ok {
				texts, err := bts.BatchSnapshotText(batchDocs, cfg.Width, cfg.Height)
				if err == nil {
					for _, p := range parsed {
						text, ok := texts[p.entry.ID]
						if !ok || len(text) == 0 {
							continue
						}
						txtPath := filepath.Join(cfg.OutDir, p.entry.ID+"_"+platform+".txt")
						os.WriteFile(txtPath, text, 0o644)
						results = append(results, Result{Platform: platform, Lang: lang, Path: txtPath})
					}
				}
			}
			continue
		}

		// Fall back to individual snapshots.
		for _, p := range parsed {
			snapshotter, ok := plat.(codegen.Snapshotter)
			if !ok {
				// No native snapshotter — try HTML fallback.
				html, err := CompilePreviewHTML(p.entry.SourceFile, platform, lang)
				if err != nil {
					return nil, fmt.Errorf("snapshot %s/%s: %w", p.entry.ID, platform, err)
				}
				htmlPlat := codegen.LookupPlatform("html")
				hs, ok := htmlPlat.(htmlSnapshotter)
				if !ok {
					return nil, fmt.Errorf("html platform does not implement SnapshotHTML")
				}
				png, err := hs.SnapshotHTML(html, cfg.Width, cfg.Height)
				if err != nil {
					return nil, fmt.Errorf("snapshot %s/%s: %w", p.entry.ID, platform, err)
				}
				outPath := filepath.Join(cfg.OutDir, p.entry.ID+"_"+platform+".png")
				if err := os.WriteFile(outPath, png, 0o644); err != nil {
					return nil, fmt.Errorf("writing %s: %w", outPath, err)
				}
				results = append(results, Result{Platform: platform, Lang: lang, Path: outPath})
				continue
			}

			batchOptCfg := &optimize.Config{
				Platform: platform,
				Language: lang,
				Dir:      filepath.Dir(p.entry.SourceFile),
			}
			if err := optimize.Optimize(p.pkg, batchOptCfg); err != nil {
				return nil, fmt.Errorf("snapshot %s/%s: optimize: %w", p.entry.ID, platform, err)
			}
			batchCaps := plat.Capabilities().Merge(langT.Capabilities())
			if err := lower.Lower(p.pkg, batchCaps, lower.Options{}); err != nil {
				return nil, fmt.Errorf("snapshot %s/%s: lower: %w", p.entry.ID, platform, err)
			}
			if batchCaps != (lower.Caps{}) {
				if err := optimize.Optimize(p.pkg, batchOptCfg); err != nil {
					return nil, fmt.Errorf("snapshot %s/%s: optimize2: %w", p.entry.ID, platform, err)
				}
			}
			png, err := snapshotter.Snapshot(p.pkg, langT, cfg.Width, cfg.Height)
			if err != nil {
				return nil, fmt.Errorf("snapshot %s/%s: %w", p.entry.ID, platform, err)
			}
			outPath := filepath.Join(cfg.OutDir, p.entry.ID+"_"+platform+".png")
			if err := os.WriteFile(outPath, png, 0o644); err != nil {
				return nil, fmt.Errorf("writing %s: %w", outPath, err)
			}
			results = append(results, Result{Platform: platform, Lang: lang, Path: outPath})

			// Text snapshots.
			if ts, ok := plat.(codegen.TextSnapshotter); ok {
				text, err := ts.SnapshotText(p.pkg, langT, cfg.Width, cfg.Height)
				if err == nil && len(text) > 0 {
					txtPath := filepath.Join(cfg.OutDir, p.entry.ID+"_"+platform+".txt")
					os.WriteFile(txtPath, text, 0o644)
					results = append(results, Result{Platform: platform, Lang: lang, Path: txtPath})
				}
			}
		}
	}

	return results, nil
}

// LangForPlatform returns the default language for a platform.
func LangForPlatform(platform string) string {
	switch platform {
	case "bubbletea", "fyne":
		return "go"
	case "android":
		return "kotlin"
	default:
		return "js"
	}
}
