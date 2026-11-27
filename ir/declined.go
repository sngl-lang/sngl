package ir

import (
	"errors"
	"fmt"
)

// Declined is implemented by an error that says a target cannot express a
// program that checked clean: a component the platform has no implementation
// for, a second window on a platform that shows one, a page a static site
// cannot serve. It is the one thing a build of a valid program may fail with
// by design; any other error from codegen is a fault of the compiler, which is
// the line the pipeline fuzzer draws (design/testing.md).
//
// Target names the target that declined, for a matrix to report the skip
// against.
type Declined interface {
	error
	DeclinedBy() string
}

// IsDeclined reports whether err, or an error it wraps, is a target declining
// the program.
func IsDeclined(err error) bool {
	_, ok := errors.AsType[Declined](err)
	return ok
}

// TargetDeclined is the Declined a target answers with when no more particular
// type says why: the message is the diagnostic, Target who gave it.
type TargetDeclined struct {
	Target string
	Msg    string
}

func (e *TargetDeclined) Error() string      { return e.Msg }
func (e *TargetDeclined) DeclinedBy() string { return e.Target }

// Decline is a TargetDeclined with a formatted message.
func Decline(target, format string, args ...any) *TargetDeclined {
	return &TargetDeclined{Target: target, Msg: fmt.Sprintf(format, args...)}
}
