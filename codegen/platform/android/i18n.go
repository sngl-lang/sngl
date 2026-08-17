package android

import (
	"io/fs"

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
// it to app/src/main/assets/i18n.manifest.json in sink. Does nothing when
// the manifest is absent. Uses the shared codegen/i18n loader.
func emitI18nManifestFile(sink codegen.Sink, cfg Config, projectFS fs.FS) error {
	data, err := snglI18n.LoadManifest(projectFS, cfg.ProjectDir)
	if err != nil || data == nil {
		return err
	}
	return writeAndroidFile(sink, "app/src/main/assets/"+snglI18n.ManifestFileName, data)
}

// hasI18nCalls reports whether the IR package uses i18n. When true the
// android platform must inject the Kotlin i18n runtime and manifest. The
// answer is stamped onto the package by the StampUsage lowering pass.
func hasI18nCalls(pkg *ir.Package) bool {
	return pkg != nil && pkg.UsesI18n
}
