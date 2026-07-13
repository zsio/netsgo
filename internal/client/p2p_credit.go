package client

import (
	"errors"
	"fmt"
	"math"
	"net"
	"sync"
	"time"

	"netsgo/pkg/protocol"
)

var errP2PCreditClosed = errors.New("p2p credit account closed")

type remoteCreditAccount struct {
	manager                    *clientPeerManager
	grant                      protocol.P2PTunnelGrant
	mu                         sync.Mutex
	cond                       *sync.Cond
	desired, granted, consumed uint64
	sequence, grantSequence    uint64
	closed                     bool
}

func newRemoteCreditAccount(manager *clientPeerManager, grant protocol.P2PTunnelGrant) *remoteCreditAccount {
	a := &remoteCreditAccount{manager: manager, grant: grant}
	a.cond = sync.NewCond(&a.mu)
	return a
}

func (a *remoteCreditAccount) Reserve(bytes int) error {
	if bytes <= 0 || a.grant.TotalBPS == 0 {
		return nil
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return errP2PCreditClosed
	}
	if math.MaxUint64-a.desired < uint64(bytes) {
		a.mu.Unlock()
		return fmt.Errorf("p2p credit demand overflow")
	}
	a.desired += uint64(bytes)
	a.sequence++
	demand := protocol.P2PCreditDemand{SessionID: a.grant.SessionID, GrantID: a.grant.GrantID, TunnelID: a.grant.TunnelID, Revision: a.grant.Revision, Sequence: a.sequence, DesiredBytes: a.desired}
	a.mu.Unlock()
	if err := a.manager.sendCreditDemand(demand); err != nil {
		a.Close()
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for !a.closed && a.granted-a.consumed < uint64(bytes) {
		a.cond.Wait()
	}
	if a.closed {
		return errP2PCreditClosed
	}
	a.consumed += uint64(bytes)
	return nil
}

func (a *remoteCreditAccount) Apply(credit protocol.P2PCreditGrant) error {
	if err := credit.Validate(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if credit.Sequence <= a.grantSequence || credit.GrantedBytes < a.granted || credit.GrantedBytes > a.desired {
		return fmt.Errorf("invalid or stale p2p credit grant")
	}
	a.grantSequence, a.granted = credit.Sequence, credit.GrantedBytes
	a.cond.Broadcast()
	return nil
}

func (a *remoteCreditAccount) Close() {
	a.mu.Lock()
	if !a.closed {
		a.closed = true
		a.cond.Broadcast()
	}
	a.mu.Unlock()
}

type localCreditRequest struct {
	remaining uint64
	done      chan struct{}
}

type ownerCreditScheduler struct {
	manager                                                     *clientPeerManager
	grant                                                       protocol.P2PTunnelGrant
	mu                                                          sync.Mutex
	remoteDesired, remoteGranted, demandSequence, grantSequence uint64
	local                                                       []*localCreditRequest
	preferRemote                                                bool
	wake                                                        chan struct{}
	done                                                        chan struct{}
	once                                                        sync.Once
}

func newOwnerCreditScheduler(manager *clientPeerManager, grant protocol.P2PTunnelGrant) *ownerCreditScheduler {
	s := &ownerCreditScheduler{manager: manager, grant: grant, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go s.run()
	return s
}

func (s *ownerCreditScheduler) Reserve(bytes int) error {
	if bytes <= 0 || s.grant.TotalBPS == 0 {
		return nil
	}
	req := &localCreditRequest{remaining: uint64(bytes), done: make(chan struct{})}
	s.mu.Lock()
	select {
	case <-s.done:
		s.mu.Unlock()
		return errP2PCreditClosed
	default:
	}
	s.local = append(s.local, req)
	s.mu.Unlock()
	s.notify()
	select {
	case <-req.done:
		return nil
	case <-s.done:
		return errP2PCreditClosed
	}
}

func (s *ownerCreditScheduler) ApplyDemand(demand protocol.P2PCreditDemand) error {
	if err := demand.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if demand.Sequence <= s.demandSequence || demand.DesiredBytes < s.remoteDesired {
		return fmt.Errorf("invalid or stale p2p credit demand")
	}
	s.demandSequence, s.remoteDesired = demand.Sequence, demand.DesiredBytes
	s.notify()
	return nil
}

func (s *ownerCreditScheduler) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *ownerCreditScheduler) run() {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	last := time.Now()
	var tokens float64
	for {
		select {
		case now := <-ticker.C:
			elapsed := now.Sub(last)
			last = now
			burst := math.Max(1, float64(s.grant.TotalBPS)/10)
			tokens = math.Min(burst, tokens+float64(s.grant.TotalBPS)*elapsed.Seconds())
			available := uint64(tokens)
			if available == 0 {
				continue
			}
			used := s.allocate(available)
			tokens -= float64(used)
		case <-s.wake:
		case <-s.done:
			return
		}
	}
}

func (s *ownerCreditScheduler) allocate(available uint64) uint64 {
	s.mu.Lock()
	remoteOutstanding := s.remoteDesired - s.remoteGranted
	var localOutstanding uint64
	for _, req := range s.local {
		if math.MaxUint64-localOutstanding < req.remaining {
			localOutstanding = math.MaxUint64
			break
		}
		localOutstanding += req.remaining
	}
	remoteGrant, localGrant := fairCreditAllocation(available, remoteOutstanding, localOutstanding)
	if remoteOutstanding > 0 && localOutstanding > 0 {
		if !s.preferRemote {
			localGrant, remoteGrant = fairCreditAllocation(available, localOutstanding, remoteOutstanding)
		}
		s.preferRemote = !s.preferRemote
	}
	allocatedLocal := localGrant
	s.remoteGranted += remoteGrant
	for localGrant > 0 && len(s.local) > 0 {
		req := s.local[0]
		amount := minCredit(localGrant, req.remaining)
		req.remaining -= amount
		localGrant -= amount
		if req.remaining == 0 {
			close(req.done)
			s.local = s.local[1:]
		}
	}
	grantedTotal := s.remoteGranted
	if remoteGrant > 0 {
		s.grantSequence++
	}
	grantSequence := s.grantSequence
	s.mu.Unlock()
	if remoteGrant > 0 {
		if err := s.manager.sendCreditGrant(protocol.P2PCreditGrant{SessionID: s.grant.SessionID, GrantID: s.grant.GrantID, TunnelID: s.grant.TunnelID, Revision: s.grant.Revision, Sequence: grantSequence, GrantedBytes: grantedTotal}); err != nil {
			s.mu.Lock()
			s.remoteGranted -= remoteGrant
			s.mu.Unlock()
		}
	}
	return remoteGrant + allocatedLocal
}

func (s *ownerCreditScheduler) Close() { s.once.Do(func() { close(s.done) }) }

type creditLimitedConn struct {
	net.Conn
	reserve    func(int) error
	frameAware bool
}

func (c *creditLimitedConn) Write(p []byte) (int, error) {
	if !c.frameAware {
		if err := c.reserve(len(p)); err != nil {
			return 0, err
		}
	}
	return c.Conn.Write(p)
}
func (c *creditLimitedConn) ReservePayload(bytes int) error { return c.reserve(bytes) }
