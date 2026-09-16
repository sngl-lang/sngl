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

// A released index dispatches to nothing, and is never handed to a later
// Register: a stray C callback arriving after teardown must find an empty slot
// rather than somebody else's closure.
func TestReleaseDropsTheSlotAndTheIndex(t *testing.T) {
	var a, b int
	ia := Register(func() { a++ })
	Release(ia)
	Dispatch(ia)
	if a != 0 {
		t.Fatalf("a released slot still dispatched (a=%d)", a)
	}

	ib := Register(func() { b++ })
	if ib == ia {
		t.Fatalf("Register reused the released index %d", ia)
	}
	Dispatch(ia)
	if b != 0 {
		t.Fatalf("the released index reached the new registration (b=%d)", b)
	}

	Release(ia) // twice, and one never registered, are both no-ops
	Release(1 << 20)
}

// Registering without releasing retains whatever the closure captured. This is
// the property gtk4rt.Post depends on: it registers once per idle tick, so the
// dispatch has to drop the slot or a program leaks one closure per tick.
func TestDispatchOnceDropsTheSlot(t *testing.T) {
	var n int
	idx := Register(func() { n++ })
	DispatchOnce(idx)
	DispatchOnce(idx)
	if n != 1 {
		t.Fatalf("DispatchOnce ran the fn %d times, want 1", n)
	}
	if _, held := slots[idx]; held {
		t.Fatal("DispatchOnce left the slot registered")
	}
}
