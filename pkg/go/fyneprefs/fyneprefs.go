// Package fyneprefs keeps sngl:settings in a fyne app's Preferences, under
// the app's ID: a scope's values are kept under "<scope>/<name>".
package fyneprefs

import (
	"fyne.io/fyne/v2"

	"duckfam.us/sngl/pkg/go/snglsettings"
)

// Of is the place a's Preferences keep values.
func Of(a fyne.App) snglsettings.Place {
	return prefs{a.Preferences()}
}

type prefs struct {
	p fyne.Preferences
}

func key(scope, name string) string {
	if scope == "" {
		return name
	}
	return scope + "/" + name
}

func (p prefs) Read(scope, name string) string { return p.p.String(key(scope, name)) }

func (p prefs) Write(scope, name, text string) { p.p.SetString(key(scope, name), text) }
