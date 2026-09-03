package interp

// A sequence is what sngl:seq's count/range/step produce: an iter<int> that
// computes its elements from three ints instead of holding them. A program
// can hold one, pass it to a function and iterate it, and none of that
// builds a list -- which is the whole point, since the interpreter used to
// answer these intrinsics with a []any and a loop to a million allocated a
// million boxed ints.
//
// It answers by index rather than by yielding, because both loop sites (a
// statement loop in exec.go and a view loop in view.go) need the length
// before the first element: an empty iterable runs the `else` block.
type sequence struct {
	start, end, step int
}

// Len is how many elements the sequence has. A step of 0 has none -- the
// alternative is a loop that never ends, which is what sngl:seq documents.
func (s sequence) Len() int {
	switch {
	case s.step > 0 && s.end > s.start:
		return (s.end - s.start + s.step - 1) / s.step
	case s.step < 0 && s.end < s.start:
		return (s.start - s.end - s.step - 1) / -s.step
	}
	return 0
}

// At is the i'th element, which is arithmetic rather than a lookup.
func (s sequence) At(i int) int { return s.start + i*s.step }

// asIterable reports how to walk a runtime iterable by index: a list, or a
// sequence. The two loop sites share it so neither has to know that an
// iter<int> is not a []any.
func asIterable(v any) (n int, at func(int) any, ok bool) {
	switch t := v.(type) {
	case []any:
		return len(t), func(i int) any { return t[i] }, true
	case sequence:
		return t.Len(), func(i int) any { return t.At(i) }, true
	}
	return 0, nil, false
}
