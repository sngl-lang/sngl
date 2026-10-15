//go:build !unix

package headless

// closeInheritedOnExec has nothing to do off unix, where cage does not run.
func closeInheritedOnExec() {}
