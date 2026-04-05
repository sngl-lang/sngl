//go:build js

package golang

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
)

func (t *Translator) BuildWASM(_, _ string, _ []codegen.WASMFunc) ([]byte, error) {
	return nil, fmt.Errorf("WASM compilation not available in browser")
}

func (t *Translator) WASMExecJS() ([]byte, error) {
	return nil, fmt.Errorf("WASM exec JS not available in browser")
}
