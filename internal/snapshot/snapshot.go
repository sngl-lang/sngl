package snapshot

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"git.duckfam.us/jonathan/sngl/internal/testutil/webtest"
)

// Config controls snapshot generation.
type Config struct {
	SourceFile string   // path to .sngl file
	Platforms  []string // e.g. ["html", "bubbletea"]; empty = all from output block
	Width      int      // viewport width (default 1280)
	Height     int      // viewport height (default 720)
	OutDir     string   // directory to write PNGs
}

// Result describes a generated screenshot.
type Result struct {
	Platform string
	Lang     string
	Path     string // output PNG path
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
			targets = append(targets, target{platform: p, lang: langForPlatform(p)})
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

	// Compile HTML for each platform and build handler.
	mux := http.NewServeMux()
	for _, t := range targets {
		html, err := CompilePreviewHTML(sourceFile, t.platform, t.lang)
		if err != nil {
			return nil, fmt.Errorf("compiling %s: %w", t.platform, err)
		}
		content := html
		path := "/" + t.platform
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(content)
		})
	}

	engine := webtest.New(mux)
	defer engine.Close()

	browser, err := engine.StartHeadless(cfg.Width, cfg.Height)
	if err != nil {
		return nil, fmt.Errorf("starting browser: %w", err)
	}
	defer browser.Close()

	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return nil, err
	}

	var results []Result
	for _, t := range targets {
		url := engine.BaseURL() + "/" + t.platform
		if err := browser.NavigateRaw(url); err != nil {
			return nil, fmt.Errorf("navigating to %s: %w", t.platform, err)
		}
		_ = browser.WaitStable(300 * time.Millisecond)

		pngBytes, err := browser.ScreenshotRaw()
		if err != nil {
			return nil, fmt.Errorf("screenshot %s: %w", t.platform, err)
		}

		outPath := filepath.Join(cfg.OutDir, t.platform+".png")
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

func langForPlatform(platform string) string {
	switch platform {
	case "bubbletea":
		return "go"
	default:
		return "js"
	}
}
