package android

import (
	_ "embed"
	"path"
	"strings"

	"duckfam.us/sngl/codegen"
)

// settingsKt is SnglSettings, the SharedPreferences sngl:settings keeps
// values in.
//
//go:embed settings.kt
var settingsKt string

// emitSettingsFile writes SnglSettings beside MainScreen.kt.
func emitSettingsFile(req *codegen.Request, sink codegen.Sink, mainScreen, pkg string) error {
	if !codegen.UsesSettings(req.Pkg) {
		return nil
	}
	src := strings.Replace(settingsKt, "package PACKAGE", "package "+pkg, 1)
	return writeAndroidFile(sink, path.Join(path.Dir(mainScreen), "SnglSettings.kt"), []byte(src))
}
