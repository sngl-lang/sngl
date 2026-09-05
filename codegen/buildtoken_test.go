package codegen

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The bound is the point: without it, every fixture in every platform test
// binary forks its own compiler and linker and the machine runs out of memory
// — which is how CI OOM-killed the compiler.
func TestAcquireBuildToken_BoundsConcurrency(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("SNGL_BUILD_SLOTS", "2")
	buildTokenSlots = syncOnceInt(func() int { return 2 })

	var live, peak atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			release, err := AcquireBuildToken(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			defer release()
			n := live.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			live.Add(-1)
		})
	}
	wg.Wait()

	if got := peak.Load(); got > 2 {
		t.Errorf("%d holders at once, want at most 2", got)
	}
	if peak.Load() == 0 {
		t.Error("no token was ever acquired")
	}
}

func TestAcquireBuildToken_ReleasesForReuse(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	buildTokenSlots = syncOnceInt(func() int { return 1 })

	release, err := AcquireBuildToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	release2, err := AcquireBuildToken(ctx)
	if err != nil {
		t.Fatalf("the single token was not reusable after release: %v", err)
	}
	release2()
}

// A caller that gives up waiting must say so rather than block forever.
func TestAcquireBuildToken_HonoursContext(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	buildTokenSlots = syncOnceInt(func() int { return 1 })

	held, err := AcquireBuildToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := AcquireBuildToken(ctx); err == nil {
		t.Error("acquired a second token while the only one was held")
	}
}

func TestBuildTokenSlots_AtLeastOne(t *testing.T) {
	buildTokenSlots = syncOnceInt(func() int { return computeBuildTokenSlots() })
	if got := BuildTokenSlots(); got < 1 {
		t.Errorf("slots = %d, want at least 1", got)
	}
}

// syncOnceInt lets a test replace the memoized slot count.
func syncOnceInt(f func() int) func() int { return sync.OnceValue(f) }
