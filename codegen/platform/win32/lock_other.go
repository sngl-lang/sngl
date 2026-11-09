//go:build !unix && !js

package win32

// lockPath locks nothing: a host that is no Unix runs a Windows program
// without Wine, and makes no prefix to guard.
func lockPath(string) (func(), error) { return func() {}, nil }
