package android

import (
	"io/fs"

	"duckfam.us/sngl/codegen"
	snglI18n "duckfam.us/sngl/codegen/i18n"
	"duckfam.us/sngl/codegen/lang/kotlin"
	"duckfam.us/sngl/codegen/platform/android/i18nruntime"
	"duckfam.us/sngl/ir"
)

// emitI18nRuntimeFile writes the Kotlin i18n runtime into sink.
// Path: app/src/main/kotlin/us/duckfam/git/jonathan/sngl/i18n/I18n.kt
// A Gradle project keeps it in its source set; the gradle-free build compiles
// every .kt file at the root.
func emitI18nRuntimeFile(sink codegen.Sink, gradle bool) error {
	path := "I18n.kt"
	if gradle {
		path = "app/src/main/kotlin/" + pkgToPath(kotlin.SnglI18nKotlinPackage) + "/I18n.kt"
	}
	return writeAndroidFile(sink, path, []byte(i18nruntime.I18nKt))
}

// emitI18nManifestFile reads the project-root i18n.manifest.json and writes
// it into the app's assets (assetsDir) in sink. Does nothing when
// the manifest is absent. Uses the shared codegen/i18n loader.
func emitI18nManifestFile(sink codegen.Sink, cfg Config, projectFS fs.FS, assetsDir string) error {
	data, err := snglI18n.LoadManifest(projectFS, cfg.ProjectDir)
	if err != nil || data == nil {
		return err
	}
	return writeAndroidFile(sink, assetsDir+"/"+snglI18n.ManifestFileName, data)
}

// hasI18nCalls reports whether the IR package uses i18n. When true the
// android platform must inject the Kotlin i18n runtime and manifest. The
// answer is stamped onto the package by the StampUsage lowering pass.
func hasI18nCalls(pkg *ir.Package) bool {
	return pkg != nil && pkg.UsesI18n
}
