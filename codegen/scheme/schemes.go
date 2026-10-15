// Package scheme imports all built-in import schemes so their init()
// registrations run.
package scheme

import (
	_ "duckfam.us/sngl/codegen/scheme/file"
	_ "duckfam.us/sngl/codegen/scheme/git"
	_ "duckfam.us/sngl/codegen/scheme/golang"
	_ "duckfam.us/sngl/codegen/scheme/http"
	_ "duckfam.us/sngl/codegen/scheme/js"
	_ "duckfam.us/sngl/codegen/scheme/markdown"
)
