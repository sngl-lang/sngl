//go:build !unix

package codegen

// lockBuildDirFile is a no-op on platforms without flock; the in-process
// mutex in tryLockBuildDir is the only guard there.
func lockBuildDirFile(path string) (unlock func(), ok bool) {
	return func() {}, true
}
