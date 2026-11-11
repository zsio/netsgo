package connlimit

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentAdmissionsStayBoundedAndReleaseOnce(t *testing.T) {
	var limit Limiter
	var simultaneous atomic.Int64
	var peak atomic.Int64
	var wg sync.WaitGroup
	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				release, ok := limit.Acquire(7)
				if !ok {
					continue
				}
				n := simultaneous.Add(1)
				for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
				}
				simultaneous.Add(-1)
				release()
				release()
			}
		}()
	}
	wg.Wait()
	if peak.Load() > 7 || limit.Active() != 0 {
		t.Fatalf("peak=%d active=%d", peak.Load(), limit.Active())
	}
}

func TestAdmissionRejectsOnlyNewConnAndReturnsCapacity(t *testing.T) {
	var limit Limiter
	a, b := net.Pipe()
	defer b.Close()
	admitted, ok := limit.Admit(a, 1)
	if !ok {
		t.Fatal("first admission failed")
	}
	defer admitted.Close()
	if again, ok := limit.Admit(admitted, 1); !ok || again != admitted {
		t.Fatal("same owner counted twice")
	}
	x, y := net.Pipe()
	defer y.Close()
	if _, ok := limit.Admit(x, 1); ok {
		t.Fatal("excess connection admitted")
	}
	if _, err := y.Write([]byte("x")); err == nil {
		t.Fatal("rejected peer was not closed")
	}
	if limit.Active() != 1 {
		t.Fatal("existing admission lost")
	}
	_ = admitted.Close()
	_ = admitted.Close()
	if release, ok := limit.Acquire(1); !ok {
		t.Fatal("capacity not returned")
	} else {
		release()
	}
	if limit.Active() != 0 {
		t.Fatal("capacity leaked")
	}
}
