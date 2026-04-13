package http

import (
	"git.duckfam.us/jonathan/sngl/codegen"
)

// renderResult holds the generated Go code and any server-state actions collected.
type renderResult struct {
	code    string
	actions []codegen.HTTPAction
}
