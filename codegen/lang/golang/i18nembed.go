package golang

import (
	"io/fs"

	"git.duckfam.us/jonathan/sngl/codegen"
	snglI18n "git.duckfam.us/jonathan/sngl/codegen/i18n"
)

// EmitI18nManifestEmbed writes i18n.manifest.json into sink alongside
// a sidecar i18n_embed.go that embeds the manifest at compile time and
// primes the sngl-i18n runtime from an init() function. Use from
// Go-emitting platforms (bubbletea, fyne, gtk4) when
// PackageUsesI18n(pkg) is true so installed binaries carry their own
// translations rather than relying on a working-directory
// i18n.manifest.json beside the executable.
//
// pkgName is the Go package name the generated file should declare.
// Skips emission silently when no manifest exists in projectFS or
// projectDir; the runtime then defaults to an empty manifest and the
// inlined ICU templates serve as the user-visible strings.
func EmitI18nManifestEmbed(sink codegen.Sink, pkgName string, projectFS fs.FS, projectDir string) error {
	data, err := snglI18n.LoadManifest(projectFS, projectDir)
	if err != nil {
		return err
	}
	if data == nil {
		return nil
	}
	if err := writeSinkFile(sink, "i18n.manifest.json", data); err != nil {
		return err
	}
	sidecar := []byte("package " + pkgName + "\n\n" +
		"import (\n" +
		"\t_ \"embed\"\n\n" +
		"\tsnglI18n \"" + SnglI18nImportPath + "\"\n" +
		")\n\n" +
		"//go:embed i18n.manifest.json\n" +
		"var snglI18nManifestEmbed []byte\n\n" +
		"func init() { _ = snglI18n.SetManifestBytes(snglI18nManifestEmbed) }\n")
	return writeSinkFile(sink, "i18n_embed.go", sidecar)
}

func writeSinkFile(sink codegen.Sink, name string, data []byte) error {
	w, err := sink.Create(name)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}
