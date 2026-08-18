package cbind

import "testing"

func TestRegisterDispatch(t *testing.T) {
	var a, b int
	ia := Register(func() { a++ })
	ib := Register(func() { b++ })
	if ia == ib {
		t.Fatalf("Register returned duplicate indices %d, %d", ia, ib)
	}

	Dispatch(ia)
	Dispatch(ia)
	Dispatch(ib)
	if a != 2 || b != 1 {
		t.Fatalf("dispatch counts a=%d b=%d, want a=2 b=1", a, b)
	}
}

func TestDispatchOutOfRangeIsNoop(t *testing.T) {
	// Must not panic for stray indices (e.g. a callback firing after teardown).
	Dispatch(-1)
	Dispatch(1 << 20)
}

func TestDispatchReentrantRegister(t *testing.T) {
	// A handler may Register more callbacks: Dispatch releases the lock before
	// invoking fn, so this must not deadlock.
	var ran bool
	idx := Register(func() { Register(func() {}); ran = true })
	Dispatch(idx)
	if !ran {
		t.Fatal("reentrant handler did not run")
	}
}
