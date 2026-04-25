package android

import (
	"embed"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
)

//go:embed templates/*
var templateFS embed.FS

// templateData is the data passed to all scaffold templates.
type templateData struct {
	Package         string // e.g. "test.sngl.app"
	AppName         string // e.g. "My App"
	HasIcon         bool
	HasAdaptiveIcon bool   // true when icon is SVG (produces VectorDrawable foreground)
	Color           string // e.g. "#6750A4" (empty if not set)
	Gradle          bool   // true for Gradle scaffold
	HasGoLib        bool   // true when Go module (golib.aar) is included
	// Activities, when non-empty, replaces the default MainActivity
	// declaration in AndroidManifest.xml with one entry per element.
	// Used by batch snapshot to declare N activities in a single APK.
	Activities []ManifestActivity
}

// ManifestActivity describes one <activity> entry to emit into the manifest
// when batch-snapshotting. Exactly one entry should have IsLauncher=true.
type ManifestActivity struct {
	Name       string
	IsLauncher bool
}

func newTemplateData(cfg Config) templateData {
	return templateData{
		Package:         cfg.Package,
		AppName:         appLabel(cfg),
		HasIcon:         cfg.Icon != "",
		HasAdaptiveIcon: cfg.Icon != "" && strings.HasSuffix(strings.ToLower(cfg.Icon), ".svg"),
		Color:           cfg.Color,
		Gradle:          cfg.Gradle,
		HasGoLib:        cfg.GoLib,
	}
}

// scaffoldFiles generates all scaffold files for a full Gradle project.
// Templates that call {{skip}} are automatically omitted.
func scaffoldFiles(cfg Config) []*codegen.OutputFile {
	data := newTemplateData(cfg)
	pkgPath := pkgToPath(cfg.Package)

	files := codegen.RenderTemplates(templateFS, "templates", data)

	// Remap output paths for the Gradle project layout:
	// - MainActivity.kt / Theme.kt → app/src/main/java/{pkg}/...
	// - AndroidManifest.xml → app/src/main/...
	// - res/ → app/src/main/res/...
	// - gradle/* → root (strip gradle/ prefix for build files)
	for _, f := range files {
		switch {
		case f.Name == "MainActivity.kt":
			f.Name = "app/src/main/java/" + pkgPath + "/MainActivity.kt"
		case f.Name == "Theme.kt":
			f.Name = "app/src/main/java/" + pkgPath + "/ui/theme/Theme.kt"
		case f.Name == "AndroidManifest.xml":
			f.Name = "app/src/main/" + f.Name
		case strings.HasPrefix(f.Name, "res/"):
			f.Name = "app/src/main/" + f.Name
		case f.Name == "gradle/app.build.gradle.kts":
			f.Name = "app/build.gradle.kts"
		case f.Name == "gradle/build.gradle.kts":
			f.Name = "build.gradle.kts"
		case f.Name == "gradle/settings.gradle.kts":
			f.Name = "settings.gradle.kts"
		case f.Name == "gradle/gradle.properties":
			f.Name = "gradle.properties"
		case f.Name == "gradle/gradlew":
			f.Name = "gradlew"
		case f.Name == "gradle/wrapper/gradle-wrapper.properties":
			// keep as-is
		}
	}

	return files
}

// directBuildFiles returns the minimal files for a gradle-free build.
// Uses the same templates but with Gradle=false so gradle files are skipped.
func directBuildFiles(cfg Config) []*codegen.OutputFile {
	data := newTemplateData(cfg)
	data.Gradle = false
	return codegen.RenderTemplates(templateFS, "templates", data)
}

// directBuildFilesWithActivities is the batch-snapshot variant of
// directBuildFiles: the manifest declares the given Activities (instead of a
// single default MainActivity) and the canonical MainActivity.kt is omitted —
// callers emit per-doc Activity sources separately.
func directBuildFilesWithActivities(cfg Config, activities []ManifestActivity) []*codegen.OutputFile {
	data := newTemplateData(cfg)
	data.Gradle = false
	data.Activities = activities
	files := codegen.RenderTemplates(templateFS, "templates", data)
	out := files[:0]
	for _, f := range files {
		if f.Name == "MainActivity.kt" {
			continue
		}
		out = append(out, f)
	}
	return out
}

func appLabel(cfg Config) string {
	if cfg.AppName != "" {
		return cfg.AppName
	}
	return appNameFromPkg(cfg.Package)
}

func appNameFromPkg(pkg string) string {
	parts := strings.Split(pkg, ".")
	if len(parts) > 0 {
		return exportName(parts[len(parts)-1])
	}
	return "App"
}
