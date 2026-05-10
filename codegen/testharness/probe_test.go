package testharness

import "testing"

func TestProbe_unregistered(t *testing.T) {
	got := Probe("does-not-exist")
	if got.OK {
		t.Fatalf("unregistered platform must return OK=false, got %+v", got)
	}
	if got.Reason == "" {
		t.Fatalf("unregistered platform must include a reason")
	}
}

func TestProbe_registered_ok(t *testing.T) {
	Register("p0test-ok", func() Available { return Available{OK: true} })
	t.Cleanup(func() { unregisterForTest("p0test-ok") })
	got := Probe("p0test-ok")
	if !got.OK {
		t.Fatalf("expected OK=true, got %+v", got)
	}
}

func TestProbe_registered_unavailable(t *testing.T) {
	Register("p0test-bad", func() Available { return Available{OK: false, Reason: "missing dep"} })
	t.Cleanup(func() { unregisterForTest("p0test-bad") })
	got := Probe("p0test-bad")
	if got.OK || got.Reason != "missing dep" {
		t.Fatalf("expected unavailable with reason, got %+v", got)
	}
}
