//go:build windows

package cload

import (
	"fmt"
	"syscall"

	"github.com/ebitengine/purego"
)

// open is the DLL a linker names `-l<library>`: user32 is user32.dll.
func open(library string) (uintptr, error) {
	h, err := syscall.LoadLibrary(library + ".dll")
	if err != nil {
		return 0, fmt.Errorf("no library %s: %v", library, err)
	}
	return uintptr(h), nil
}

func bind(fptr any, h uintptr, name string) {
	proc, err := syscall.GetProcAddress(syscall.Handle(h), name)
	if err != nil {
		panic(fmt.Sprintf("cload: no %s: %v", name, err))
	}
	purego.RegisterFunc(fptr, proc)
}
