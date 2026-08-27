package codegen

import "fmt"

// UnimplementedComponent is returned by a platform generator for a library
// component the platform declares no implementation for. It is a typed error
// rather than a message so a caller running a matrix across platforms can tell
// "this target does not support this component" from "this target is broken",
// without matching on the text: the SNGL test command reports it as a skip for
// that platform, and a build of one target reports it as the error it is.
type UnimplementedComponent struct {
	Component string
	Platform  string
}

func (e *UnimplementedComponent) Error() string {
	return fmt.Sprintf("component %q has no %s implementation", e.Component, e.Platform)
}
