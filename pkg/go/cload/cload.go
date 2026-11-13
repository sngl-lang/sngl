// Package cload opens a shared library and binds its functions to a Go
// program's func variables through purego, for a program calling C with no
// cgo (the Go language's `cgo` option set to "purego").
package cload

import (
	"fmt"
	"sync"
)

var (
	mu   sync.Mutex
	libs = map[string]uintptr{}
)

// Bind makes *fptr, a func variable, call the C function name in the library
// a c.link names as a linker would (`m`, `gtk-4`), opening it the first time.
// A library or function that is not there panics, as a program linked against
// it would not start.
func Bind(fptr any, library, name string) {
	mu.Lock()
	h, ok := libs[library]
	if !ok {
		var err error
		h, err = open(library)
		if err != nil {
			mu.Unlock()
			panic(fmt.Sprintf("cload: %v", err))
		}
		libs[library] = h
	}
	mu.Unlock()
	bind(fptr, h, name)
}
