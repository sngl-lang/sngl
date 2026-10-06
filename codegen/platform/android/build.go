package android

import (
	"fmt"
	"os"
	"path/filepath"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Build answers `android.buildApk`, android's build command, and the
// snapshotters. It compiles generated sources into a
// signed debug APK, optionally running gomobile bind first if a Go module
// is present.
func (g *Generator) Build(dir string, opts *ir.StructLit) (string, error) {
	var cfg Config
	if err := codegen.ApplyOptions(&cfg, opts); err != nil {
		return "", fmt.Errorf("android: %w", err)
	}
	cfg = cfg.withDefaults()
	useGradle := cfg.UseGradle()
	pkg := cfg.Package
	combo := cfg.combo()

	// If a Go module is present (go+android), run gomobile bind first.
	goLibDir := filepath.Join(dir, "golib")
	if info, err := os.Stat(goLibDir); err == nil && info.IsDir() {
		aarPath := filepath.Join(dir, "golib.aar")
		if err := gomobileBind(goLibDir, aarPath, combo); err != nil {
			return "", fmt.Errorf("gomobile bind: %w", err)
		}
	}

	if useGradle {
		var err error
		pkg, err = readPackage(dir)
		if err != nil {
			return "", err
		}
		if err := gradleBuild(dir, combo); err != nil {
			return "", err
		}
		return filepath.Join(dir, "app", "build", "outputs", "apk", "debug", "app-debug.apk"), nil
	}

	tc, err := resolveToolchain(combo)
	if err != nil {
		return "", fmt.Errorf("resolving toolchain: %w", err)
	}
	return directBuild(dir, tc, pkg)
}
