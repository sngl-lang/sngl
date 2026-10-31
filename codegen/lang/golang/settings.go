package golang

import (
	"strconv"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// SettingsApp is what a program's settings are kept under: output's name, or
// "sngl-app" for a program that gave none.
func SettingsApp(name string) string {
	if name == "" {
		return "sngl-app"
	}
	return name
}

// OpenSettings is the statement at the head of a main that opens place for
// sngl:settings, or "" where pkg keeps none. Only a main opens one, which a
// test and the test agent never run, so they keep values in memory.
func OpenSettings(pkg *ir.Package, place string) string {
	if pkg == nil || !codegen.UsesSettings(pkg) {
		return ""
	}
	return "\tsnglsettings.Open(" + place + ")\n"
}

// SettingsFile is the place gtk4 and bubbletea keep values: a file under the
// user's configuration directory, named for the app.
func SettingsFile(name string) string {
	return "snglsettings.File(" + strconv.Quote(SettingsApp(name)) + ")"
}
