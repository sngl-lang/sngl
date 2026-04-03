package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
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
		outputs, err := ParseOutputs(sourceFile)
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
		outPath := filepath.Join(cfg.OutDir, prefix+"-"+t.platform+".png")
		if err := os.WriteFile(outPath, pngBytes, 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", outPath, err)
		}

		results = append(results, Result{
			Platform: t.platform,
			Lang:     t.lang,
			Path:     outPath,
		})
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
		langT := codegen.LookupLang(lang)
		if langT == nil {
			return nil, fmt.Errorf("lang %q not registered", lang)
		}
		return snapshotter.Snapshot(doc, langT, width, height)
	}

	// Platforms without a Snapshotter: compile to HTML preview and screenshot.
	return snapshotViaHTML(sourceFile, platform, lang, width, height)
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
