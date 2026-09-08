package docsite

import "git.duckfam.us/jonathan/sngl/ir"

// Starter is the components the /components page leads with, in reading order:
// what a first application reaches for. Everything else is a click away in the
// package tree, so this is a front door and not a catalogue.
//
// Hand-picked, which is why TestStarterComponentsExist holds it to sngl:ui.
// The tier table this replaced was hand-picked too and nothing checked it: it
// had come to list nine components that do not exist (slider, dialog, toast,
// grid, list, map, video, audio, webview) and to miss nine that do.
var Starter = []string{
	"text", "button", "input", "checkbox", "select",
	"vbox", "hbox", "card", "image", "link",
	"table", "modal",
}

// StarterPkg is the package Starter names, and the one the page links on to.
const StarterPkg = "ui"

// ChildPolicyString returns a human-readable string for a component's children policy.
func ChildPolicyString(children *ir.Type) string {
	// The three shapes childrenTypeFor builds from the rest slot's count
	// bound. Answering "many" for all of them documented `scroll` and
	// `tooltip` as taking any number of children when each takes one.
	switch {
	case children == nil:
		return "none"
	case children.Kind == ir.TypeList:
		return "many"
	case children.Kind == ir.TypeOption:
		return "at most one"
	}
	return "exactly one"
}
