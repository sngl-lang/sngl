// Package snglopt holds what generated Go calls on an option, which Go holds
// as a pointer: sngl:builtin's option[T].or.
package snglopt

// Or is the value p points to, or otherwise where p is nil.
func Or[T any](p *T, otherwise T) T {
	if p != nil {
		return *p
	}
	return otherwise
}
