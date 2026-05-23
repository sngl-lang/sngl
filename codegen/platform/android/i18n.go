package android

import (
	"io/fs"
	"os"
	"path/filepath"

	"git.duckfam.us/jonathan/sngl/codegen"
	snglI18n "git.duckfam.us/jonathan/sngl/codegen/i18n"
	"git.duckfam.us/jonathan/sngl/codegen/lang/kotlin"
	"git.duckfam.us/jonathan/sngl/codegen/platform/android/i18nruntime"
	"git.duckfam.us/jonathan/sngl/ir"
)

// emitI18nRuntimeFile writes the Kotlin i18n runtime into sink.
// Path: app/src/main/kotlin/us/duckfam/git/jonathan/sngl/i18n/I18n.kt
func emitI18nRuntimeFile(sink codegen.Sink) error {
	path := "app/src/main/kotlin/" +
		pkgToPath(kotlin.SnglI18nKotlinPackage) +
		"/I18n.kt"
	return writeAndroidFile(sink, path, []byte(i18nruntime.I18nKt))
}

// emitI18nManifestFile reads the project-root i18n.manifest.json and writes
// it to app/src/main/assets/i18n.manifest.json in sink.
// Does nothing when the manifest is absent.
func emitI18nManifestFile(sink codegen.Sink, cfg Config, projectFS fs.FS) error {
	const name = "i18n.manifest.json"
	var data []byte
	// Prefer projectFS when available (in-memory builds, playground).
	if projectFS != nil {
		if b, err := fs.ReadFile(projectFS, name); err == nil {
			data = b
		}
	}
	if data == nil && cfg.ProjectDir != "" {
		if b, err := os.ReadFile(filepath.Join(cfg.ProjectDir, name)); err == nil {
			data = b
		}
	}
	if data == nil {
		return nil
	}
	return writeAndroidFile(sink, "app/src/main/assets/"+name, data)
}

// hasI18nCalls reports whether the IR package contains any call to an i18n
// intrinsic (i18n.tr, i18n.format, i18n.numberInt, etc.). When true the
// android platform must inject the Kotlin i18n runtime and manifest.
func hasI18nCalls(pkg *ir.Package) bool {
	if pkg == nil {
		return false
	}
	found := false
	ir.WalkExprs(pkg, func(e ir.Expr) bool {
		c, ok := e.(*ir.Call)
		if !ok {
			return false
		}
		if snglI18n.IsCall(c) {
			found = true
			return true // signal stop
		}
		return false
	})
	return found
}

// i18nRuntimeFile returns the OutputFile that should be written into the
// generated module's source tree for the Kotlin i18n runtime.
// Path: app/src/main/kotlin/us/duckfam/git/jonathan/sngl/i18n/I18n.kt
func i18nRuntimeFile() *codegen.OutputFile {
	path := "app/src/main/kotlin/" +
		pkgToPath(kotlin.SnglI18nKotlinPackage) +
		"/I18n.kt"
	return codegen.BytesFile(path, []byte(i18nruntime.I18nKt))
}

// i18nManifestFile reads the project-root i18n.manifest.json and returns an
// OutputFile that places it at app/src/main/assets/i18n.manifest.json.
// Returns nil when the manifest is absent.
func i18nManifestFile(cfg Config, projectFS fs.FS) *codegen.OutputFile {
	const name = "i18n.manifest.json"
	var data []byte
	// Prefer projectFS when available (in-memory builds, playground).
	if projectFS != nil {
		if b, err := fs.ReadFile(projectFS, name); err == nil {
			data = b
		}
	}
	if data == nil && cfg.ProjectDir != "" {
		if b, err := os.ReadFile(filepath.Join(cfg.ProjectDir, name)); err == nil {
			data = b
		}
	}
	if data == nil {
		return nil
	}
	return codegen.BytesFile("app/src/main/assets/"+name, data)
}

