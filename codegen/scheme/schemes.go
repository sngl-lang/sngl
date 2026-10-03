// Package scheme imports all built-in import schemes so their init()
// registrations run.
package scheme

import (
	_ "git.duckfam.us/jonathan/sngl/codegen/scheme/file"
	_ "git.duckfam.us/jonathan/sngl/codegen/scheme/git"
	_ "git.duckfam.us/jonathan/sngl/codegen/scheme/golang"
	_ "git.duckfam.us/jonathan/sngl/codegen/scheme/http"
	_ "git.duckfam.us/jonathan/sngl/codegen/scheme/js"
	_ "git.duckfam.us/jonathan/sngl/codegen/scheme/markdown"
)
