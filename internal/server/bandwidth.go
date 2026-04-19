package server

import (
	"io"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"netsgo/pkg/protocol"
)

type payloadDirection string

const (
	payloadDirectionIngress payloadDirection = "ingress"
	payloadDirectionEgress  payloadDirection = "egress"
)

type bandwidthClock interface {
	Now() time.Time
	Sleep(time.Duration)
}

type realBandwidthClock struct{}

func (realBandwidthClock) Now() time.Time {
	return time.Now()
}

func (realBandwidthClock) Sleep(d time.Duration) {
	if d > 0 {
		time.Sleep(d)
	}
}

type budgetSlot struct {
	id       uint64
	clock    bandwidthClock
	mu       sync.Mutex
	limit    int64
	capacity float64
	tokens   float64
	last     time.Time
}

var nextBudgetSlotID atomic.Uint64

func newBudgetSlot(limit int64, clock bandwidthClock) *budgetSlot {
	if clock == nil {
		clock = realBandwidthClock{}
	}
	slot := &budgetSlot{
		id:    nextBudgetSlotID.Add(1),
		clock: clock,
	}
	slot.UpdateLimit(limit)
	return slot
}

func (s *budgetSlot) UpdateLimit(limit int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.clock.Now()
	if s.limit > 0 {
		s.refillLocked(now)
	}

	s.limit = limit
	s.last = now
	if limit <= 0 {
		s.capacity = 0
		s.tokens = 0
		return
	}

	s.capacity = float64(limit)
	if s.tokens <= 0 {
		s.tokens = s.capacity
		return
	}
	if s.tokens > s.capacity {
		s.tokens = s.capacity
	}
}

func (s *budgetSlot) Preview(maxBytes int) int {
	if maxBytes <= 0 {
		return 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return int(s.previewLocked(s.clock.Now(), int64(maxBytes)))
}

func (s *budgetSlot) Take(maxBytes int) int {
	if maxBytes <= 0 {
		return 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return int(s.takeLocked(s.clock.Now(), int64(maxBytes)))
}

func (s *budgetSlot) Refund(bytes int) {
	if bytes <= 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.refundLocked(s.clock.Now(), int64(bytes))
}

func (s *budgetSlot) previewLocked(now time.Time, maxBytes int64) int64 {
	if maxBytes <= 0 {
		return 0
	}
	if s.limit <= 0 {
		return maxBytes
	}

	s.refillLocked(now)
	if s.tokens < 1 {
		return 0
	}

	allowed := int64(s.tokens)
	if allowed > maxBytes {
		allowed = maxBytes
	}
	return allowed
}

func (s *budgetSlot) takeLocked(now time.Time, maxBytes int64) int64 {
	allowed := s.previewLocked(now, maxBytes)
	if s.limit > 0 && allowed > 0 {
		s.tokens -= float64(allowed)
		if s.tokens < 0 {
			s.tokens = 0
		}
	}
	return allowed
}

func (s *budgetSlot) refundLocked(now time.Time, bytes int64) {
	if bytes <= 0 || s.limit <= 0 {
		return
	}
	s.refillLocked(now)
	s.tokens = math.Min(s.capacity, s.tokens+float64(bytes))
}

func (s *budgetSlot) waitDurationLocked(now time.Time) time.Duration {
	if s.limit <= 0 {
		return 0
	}
	s.refillLocked(now)
	if s.tokens >= 1 {
		return 0
	}

	deficit := 1 - s.tokens
	seconds := deficit / float64(s.limit)
	if seconds <= 0 {
		return 0
	}
	return time.Duration(math.Ceil(seconds * float64(time.Second)))
}

func (s *budgetSlot) refillLocked(now time.Time) {
	if s.limit <= 0 {
		s.last = now
		return
	}
	if s.last.IsZero() {
		s.last = now
		if s.tokens <= 0 {
			s.tokens = s.capacity
		}
		return
	}
	elapsed := now.Sub(s.last).Seconds()
	if elapsed <= 0 {
		return
	}
	s.tokens = math.Min(s.capacity, s.tokens+(elapsed*float64(s.limit)))
	s.last = now
}

type directionalBandwidthRuntime struct {
	ingress *budgetSlot
	egress  *budgetSlot
}

func newDirectionalBandwidthRuntime(settings protocol.BandwidthSettings, clock bandwidthClock) *directionalBandwidthRuntime {
	if clock == nil {
		clock = realBandwidthClock{}
	}
	return &directionalBandwidthRuntime{
		ingress: newBudgetSlot(settings.IngressBPS, clock),
		egress:  newBudgetSlot(settings.EgressBPS, clock),
	}
}

func (r *directionalBandwidthRuntime) Update(settings protocol.BandwidthSettings) {
	if r == nil {
		return
	}
	r.ingress.UpdateLimit(settings.IngressBPS)
	r.egress.UpdateLimit(settings.EgressBPS)
}

func (r *directionalBandwidthRuntime) slot(direction payloadDirection) *budgetSlot {
	if r == nil {
		return nil
	}
	switch direction {
	case payloadDirectionIngress:
		return r.ingress
	case payloadDirectionEgress:
		return r.egress
	default:
		return nil
	}
}

func (r *directionalBandwidthRuntime) Budget(direction payloadDirection) *budgetSlot {
	return r.slot(direction)
}

type compositeBudget struct {
	slots []*budgetSlot
}

func composeDirectionalBudget(direction payloadDirection, runtimes ...*directionalBandwidthRuntime) *compositeBudget {
	return &compositeBudget{slots: payloadBudgetSlots(direction, runtimes...)}
}

func (b *compositeBudget) Preview(maxBytes int) int {
	if maxBytes <= 0 {
		return 0
	}
	if len(b.slots) == 0 {
		return maxBytes
	}

	now := clockForBudgetSlots(b.slots).Now()
	unlock := lockBudgetSlots(b.slots)
	defer unlock()

	allowed := int64(maxBytes)
	for _, slot := range b.slots {
		if preview := slot.previewLocked(now, allowed); preview < allowed {
			allowed = preview
		}
	}
	return int(allowed)
}

func (b *compositeBudget) Take(maxBytes int) int {
	return takeBandwidthAllowance(maxBytes, b.slots...)
}

func payloadBudgetSlots(direction payloadDirection, runtimes ...*directionalBandwidthRuntime) []*budgetSlot {
	slots := make([]*budgetSlot, 0, len(runtimes))
	for _, runtime := range runtimes {
		if slot := runtime.slot(direction); slot != nil {
			slots = append(slots, slot)
		}
	}
	return slots
}

func lockBudgetSlots(slots []*budgetSlot) func() {
	if len(slots) == 0 {
		return func() {}
	}

	ordered := append([]*budgetSlot(nil), slots...)
	slices.SortFunc(ordered, func(a, b *budgetSlot) int {
		switch {
		case a.id < b.id:
			return -1
		case a.id > b.id:
			return 1
		default:
			return 0
		}
	})
	for _, slot := range ordered {
		slot.mu.Lock()
	}
	return func() {
		for i := len(ordered) - 1; i >= 0; i-- {
			ordered[i].mu.Unlock()
		}
	}
}

func clockForBudgetSlots(slots []*budgetSlot) bandwidthClock {
	for _, slot := range slots {
		if slot != nil && slot.clock != nil {
			return slot.clock
		}
	}
	return realBandwidthClock{}
}

func takeBandwidthAllowance(maxBytes int, slots ...*budgetSlot) int {
	if maxBytes <= 0 {
		return 0
	}
	if len(slots) == 0 {
		return maxBytes
	}

	clock := clockForBudgetSlots(slots)
	for {
		now := clock.Now()
		unlock := lockBudgetSlots(slots)

		allowed := int64(maxBytes)
		for _, slot := range slots {
			if preview := slot.previewLocked(now, allowed); preview < allowed {
				allowed = preview
			}
		}
		if allowed > 0 {
			for _, slot := range slots {
				slot.takeLocked(now, allowed)
			}
			unlock()
			return int(allowed)
		}

		wait := time.Duration(0)
		for _, slot := range slots {
			if slotWait := slot.waitDurationLocked(now); slotWait > wait {
				wait = slotWait
			}
		}
		unlock()

		if wait <= 0 {
			wait = time.Millisecond
		}
		clock.Sleep(wait)
	}
}

func waitForBandwidthAllowance(maxBytes int, slots ...*budgetSlot) int {
	return takeBandwidthAllowance(maxBytes, slots...)
}

func refundBandwidthAllowance(bytes int, slots ...*budgetSlot) {
	if bytes <= 0 || len(slots) == 0 {
		return
	}

	now := clockForBudgetSlots(slots).Now()
	unlock := lockBudgetSlots(slots)
	defer unlock()
	for _, slot := range slots {
		slot.refundLocked(now, int64(bytes))
	}
}

func reserveFullPayloadBandwidth(totalBytes int, slots ...*budgetSlot) {
	remaining := totalBytes
	for remaining > 0 {
		remaining -= takeBandwidthAllowance(remaining, slots...)
	}
}

func writeFull(dst io.Writer, payload []byte) (int, error) {
	written := 0
	for written < len(payload) {
		n, err := dst.Write(payload[written:])
		if n > 0 {
			written += n
		}
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func copyWithBandwidth(dst io.Writer, src io.Reader, slots ...*budgetSlot) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64

	for {
		allowed := waitForBandwidthAllowance(len(buf), slots...)
		nr, er := src.Read(buf[:allowed])
		if unused := allowed - nr; unused > 0 {
			refundBandwidthAllowance(unused, slots...)
		}
		if nr > 0 {
			nw, ew := writeFull(dst, buf[:nr])
			total += int64(nw)
			if unwritten := nr - nw; unwritten > 0 {
				refundBandwidthAllowance(unwritten, slots...)
			}
			if ew != nil {
				return total, ew
			}
			if nw != nr {
				return total, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				return total, nil
			}
			return total, er
		}
	}
}

func relayTunnelPayload(stream io.ReadWriteCloser, extConn io.ReadWriteCloser, clientRuntime, tunnelRuntime *directionalBandwidthRuntime) (int64, int64) {
	ingressSlots := payloadBudgetSlots(payloadDirectionIngress, clientRuntime, tunnelRuntime)
	egressSlots := payloadBudgetSlots(payloadDirectionEgress, clientRuntime, tunnelRuntime)

	var once sync.Once
	closeAll := func() {
		_ = stream.Close()
		_ = extConn.Close()
	}

	var ingressBytes int64
	var egressBytes int64
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		egressBytes, _ = copyWithBandwidth(extConn, stream, egressSlots...)
		once.Do(closeAll)
	}()

	go func() {
		defer wg.Done()
		ingressBytes, _ = copyWithBandwidth(stream, extConn, ingressSlots...)
		once.Do(closeAll)
	}()

	wg.Wait()
	return ingressBytes, egressBytes
}
