package server

import (
	"bytes"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"netsgo/pkg/protocol"
)

type fakeBandwidthClock struct {
	now time.Time
}

func (c *fakeBandwidthClock) Now() time.Time {
	return c.now
}

func (c *fakeBandwidthClock) Sleep(d time.Duration) {
	c.now = c.now.Add(d)
}

func (c *fakeBandwidthClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

func TestDirectionalBandwidthRuntime_UsesDeterministicClock(t *testing.T) {
	clock := &fakeBandwidthClock{now: time.Unix(100, 0)}
	runtime := newDirectionalBandwidthRuntime(protocol.BandwidthSettings{
		IngressBPS: 100,
		EgressBPS:  0,
	}, clock)

	ingress := runtime.Budget(payloadDirectionIngress)
	if got := ingress.Preview(200); got != 100 {
		t.Fatalf("expected initial ingress preview=100, got %d", got)
	}
	if got := ingress.Take(60); got != 60 {
		t.Fatalf("expected ingress take=60, got %d", got)
	}
	if got := ingress.Preview(200); got != 40 {
		t.Fatalf("expected remaining ingress preview=40, got %d", got)
	}

	clock.Advance(500 * time.Millisecond)
	if got := ingress.Preview(200); got != 90 {
		t.Fatalf("expected ingress preview=90 after refill, got %d", got)
	}

	if got := runtime.Budget(payloadDirectionEgress).Preview(4096); got != 4096 {
		t.Fatalf("expected unlimited egress preview=4096, got %d", got)
	}
}

func TestDirectionalBandwidthRuntime_UpdateSwapsUnlimitedAndLimitedModes(t *testing.T) {
	clock := &fakeBandwidthClock{now: time.Unix(200, 0)}
	runtime := newDirectionalBandwidthRuntime(protocol.BandwidthSettings{}, clock)

	if got := runtime.Budget(payloadDirectionIngress).Preview(2048); got != 2048 {
		t.Fatalf("expected unlimited ingress preview=2048, got %d", got)
	}

	runtime.Update(protocol.BandwidthSettings{IngressBPS: 128})
	if got := runtime.Budget(payloadDirectionIngress).Preview(2048); got != 128 {
		t.Fatalf("expected limited ingress preview=128 after update, got %d", got)
	}

	runtime.Update(protocol.BandwidthSettings{})
	if got := runtime.Budget(payloadDirectionIngress).Preview(2048); got != 2048 {
		t.Fatalf("expected unlimited ingress preview=2048 after reset, got %d", got)
	}
}

func TestComposeDirectionalBudget_StricterBottleneckWins(t *testing.T) {
	clock := &fakeBandwidthClock{now: time.Unix(300, 0)}
	clientRuntime := newDirectionalBandwidthRuntime(protocol.BandwidthSettings{
		IngressBPS: 120,
		EgressBPS:  0,
	}, clock)
	tunnelRuntime := newDirectionalBandwidthRuntime(protocol.BandwidthSettings{
		IngressBPS: 80,
		EgressBPS:  0,
	}, clock)

	composed := composeDirectionalBudget(payloadDirectionIngress, clientRuntime, tunnelRuntime)
	if got := composed.Preview(256); got != 80 {
		t.Fatalf("expected stricter ingress budget preview=80, got %d", got)
	}
	if got := composed.Take(256); got != 80 {
		t.Fatalf("expected stricter ingress take=80, got %d", got)
	}
	if got := clientRuntime.Budget(payloadDirectionIngress).Preview(256); got != 40 {
		t.Fatalf("expected client runtime remaining preview=40, got %d", got)
	}
	if got := tunnelRuntime.Budget(payloadDirectionIngress).Preview(256); got != 0 {
		t.Fatalf("expected tunnel runtime remaining preview=0, got %d", got)
	}
}

func TestComposeDirectionalBudget_UnlimitedBudgetIsNoOp(t *testing.T) {
	clock := &fakeBandwidthClock{now: time.Unix(400, 0)}
	clientRuntime := newDirectionalBandwidthRuntime(protocol.BandwidthSettings{
		IngressBPS: 64,
	}, clock)

	composed := composeDirectionalBudget(payloadDirectionIngress, nil, clientRuntime)
	if got := composed.Preview(512); got != 64 {
		t.Fatalf("expected unlimited component to be ignored, got preview=%d", got)
	}
}

func TestBudgetSlot_ConcurrentTakesShareAggregateBudget(t *testing.T) {
	clock := &fakeBandwidthClock{now: time.Unix(450, 0)}
	slot := newBudgetSlot(100, clock)

	start := make(chan struct{})
	results := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			results <- slot.Take(80)
		}()
	}
	close(start)

	first := <-results
	second := <-results
	if first > 80 || second > 80 {
		t.Fatalf("each flow should be capped by its requested budget, got %d and %d", first, second)
	}
	if first+second != 100 {
		t.Fatalf("concurrent flows should share one aggregate 100-byte budget, got %d", first+second)
	}
}

type blockingBandwidthClock struct {
	mu       sync.Mutex
	now      time.Time
	sleeping chan time.Duration
	resume   chan struct{}
}

func newBlockingBandwidthClock(now time.Time) *blockingBandwidthClock {
	return &blockingBandwidthClock{
		now:      now,
		sleeping: make(chan time.Duration, 1),
		resume:   make(chan struct{}),
	}
}

func (c *blockingBandwidthClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *blockingBandwidthClock) Sleep(d time.Duration) {
	c.sleeping <- d
	<-c.resume
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type singleByteEOFReader struct {
	reads atomic.Int32
}

func (r *singleByteEOFReader) Read(p []byte) (int, error) {
	r.reads.Add(1)
	p[0] = 'x'
	return 1, io.EOF
}

func TestCopyWithBandwidth_WaitsBeforeReadingPayload(t *testing.T) {
	clock := newBlockingBandwidthClock(time.Unix(500, 0))
	slot := newBudgetSlot(1, clock)
	if got := slot.Take(1); got != 1 {
		t.Fatalf("failed to drain initial budget, got %d", got)
	}

	src := &singleByteEOFReader{}
	var dst bytes.Buffer
	done := make(chan error, 1)
	go func() {
		n, err := copyWithBandwidth(&dst, src, slot)
		if n != 1 {
			done <- io.ErrShortWrite
			return
		}
		done <- err
	}()

	select {
	case <-clock.sleeping:
	case err := <-done:
		t.Fatalf("copy completed before waiting for bandwidth: %v", err)
	case <-time.After(time.Second):
		t.Fatal("copy did not wait on exhausted bandwidth")
	}
	if got := src.reads.Load(); got != 0 {
		t.Fatalf("source read happened before bandwidth admission, reads=%d", got)
	}

	close(clock.resume)
	if err := <-done; err != nil {
		t.Fatalf("copyWithBandwidth returned error after budget refill: %v", err)
	}
	if got := dst.String(); got != "x" {
		t.Fatalf("copied payload mismatch: %q", got)
	}
}

func TestCopyWithBandwidth_RefundsUnusedReservedBytes(t *testing.T) {
	clock := &fakeBandwidthClock{now: time.Unix(550, 0)}
	slot := newBudgetSlot(10, clock)
	src := &singleByteEOFReader{}
	var dst bytes.Buffer

	n, err := copyWithBandwidth(&dst, src, slot)
	if err != nil {
		t.Fatalf("copyWithBandwidth returned error: %v", err)
	}
	if n != 1 || dst.String() != "x" {
		t.Fatalf("copyWithBandwidth mismatch: n=%d dst=%q", n, dst.String())
	}
	if got := slot.Preview(10); got != 9 {
		t.Fatalf("unused reserved bytes should be refunded, remaining preview=%d", got)
	}
}

type singleByteEOFConn struct {
	net.Conn
	reads atomic.Int32
}

func (c *singleByteEOFConn) Read(p []byte) (int, error) {
	c.reads.Add(1)
	p[0] = 'y'
	return 1, io.EOF
}

func TestCountingConnRead_WaitsBeforeReadingEgressPayload(t *testing.T) {
	clock := newBlockingBandwidthClock(time.Unix(600, 0))
	slot := newBudgetSlot(1, clock)
	if got := slot.Take(1); got != 1 {
		t.Fatalf("failed to drain initial budget, got %d", got)
	}

	probe := &singleByteEOFConn{}
	conn := &countingConn{Conn: probe, egressSlots: []*budgetSlot{slot}}
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 8)
		n, err := conn.Read(buf)
		if n != 1 || buf[0] != 'y' {
			done <- io.ErrShortWrite
			return
		}
		done <- err
	}()

	select {
	case <-clock.sleeping:
	case err := <-done:
		t.Fatalf("read completed before waiting for bandwidth: %v", err)
	case <-time.After(time.Second):
		t.Fatal("read did not wait on exhausted egress bandwidth")
	}
	if got := probe.reads.Load(); got != 0 {
		t.Fatalf("conn read happened before bandwidth admission, reads=%d", got)
	}

	close(clock.resume)
	if err := <-done; err != io.EOF {
		t.Fatalf("countingConn.Read returned %v, want io.EOF with payload", err)
	}
}

func TestCountingConnRead_RefundsUnusedReservedBytes(t *testing.T) {
	clock := &fakeBandwidthClock{now: time.Unix(650, 0)}
	slot := newBudgetSlot(10, clock)
	probe := &singleByteEOFConn{}
	conn := &countingConn{Conn: probe, egressSlots: []*budgetSlot{slot}}
	buf := make([]byte, 8)

	n, err := conn.Read(buf)
	if n != 1 || buf[0] != 'y' {
		t.Fatalf("countingConn.Read mismatch: n=%d buf[0]=%q", n, buf[0])
	}
	if err != io.EOF {
		t.Fatalf("countingConn.Read error: want io.EOF, got %v", err)
	}
	if got := slot.Preview(10); got != 9 {
		t.Fatalf("unused HTTP reserved bytes should be refunded, remaining preview=%d", got)
	}
}

func TestValidateBandwidthSettingsRejectsNegativeValues(t *testing.T) {
	tests := []protocol.BandwidthSettings{
		{IngressBPS: -1},
		{EgressBPS: -1},
	}

	for _, tc := range tests {
		if err := validateBandwidthSettings(tc); err == nil {
			t.Fatalf("expected validation error for settings=%+v", tc)
		}
	}
}
