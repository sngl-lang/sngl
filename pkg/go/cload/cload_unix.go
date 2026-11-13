//go:build !windows

package cload

import (
	"fmt"
	"runtime"
	"strconv"

	"github.com/ebitengine/purego"
)

// open is the library a linker names `-l<library>`: its development link,
// where there is one, or the first version of it that opens, since a system
// without the headers has only the versioned file (libm.so.6).
func open(library string) (uintptr, error) {
	ext := ".so"
	if runtime.GOOS == "darwin" {
		ext = ".dylib"
	}
	names := []string{"lib" + library + ext}
	if ext == ".so" {
		for v := range 10 {
			names = append(names, "lib"+library+".so."+strconv.Itoa(v))
		}
	}
	var first error
	for _, n := range names {
		h, err := purego.Dlopen(n, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			return h, nil
		}
		if first == nil {
			first = err
		}
	}
	return 0, fmt.Errorf("no library %s: %v", library, first)
}

func bind(fptr any, h uintptr, name string) {
	purego.RegisterLibFunc(fptr, h, name)
}
