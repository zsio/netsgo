package client

import (
	"errors"
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

func TestFairCreditAllocationIsWorkConservingForOneDirection(t *testing.T) {
	in, out := fairCreditAllocation(1000, 1000, 0)
	if in != 1000 || out != 0 {
		t.Fatalf("allocation=(%d,%d)", in, out)
	}
}

func TestFairCreditAllocationSplitsCompetingDirections(t *testing.T) {
	in, out := fairCreditAllocation(1000, 10000, 10000)
	if in != 500 || out != 500 {
		t.Fatalf("allocation=(%d,%d)", in, out)
	}
}

func TestFairCreditAllocationLendsUnusedShare(t *testing.T) {
	in, out := fairCreditAllocation(1000, 100, 10000)
	if in != 100 || out != 900 {
		t.Fatalf("allocation=(%d,%d)", in, out)
	}
}

func TestFairCreditAllocationBoundsBackloggedDirection(t *testing.T) {
	in, out := fairCreditAllocation(10*sharedCreditMaxBlock, ^uint64(0), 1)
	if out != 1 {
		t.Fatalf("new reverse demand was not served: %d", out)
	}
	if in > sharedCreditMaxBlock {
		t.Fatalf("continuous grant exceeded max block: %d", in)
	}
}

func TestOwnerCreditSchedulerAppliesConfiguredRate(t *testing.T) {
	scheduler := newOwnerCreditScheduler(nil, protocol.P2PTunnelGrant{TotalBPS: 10_000})
	defer scheduler.Close()
	start := time.Now()
	if err := scheduler.Reserve(1_000); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed < 70*time.Millisecond || elapsed > time.Second {
		t.Fatalf("reserve duration outside tolerance: %v", elapsed)
	}
}

func TestOwnerCreditSchedulerCloseUnblocksWaiter(t *testing.T) {
	scheduler := newOwnerCreditScheduler(nil, protocol.P2PTunnelGrant{TotalBPS: 1})
	done := make(chan error, 1)
	go func() { done <- scheduler.Reserve(1000) }()
	time.Sleep(20 * time.Millisecond)
	scheduler.Close()
	select {
	case err := <-done:
		if !errors.Is(err, errP2PCreditClosed) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("credit waiter remained blocked after close")
	}
}
