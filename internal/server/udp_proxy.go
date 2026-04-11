package server

import (
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

// UDPProxyState is the runtime state of a server-side UDP proxy.
type UDPProxyState struct {
	packetConn   net.PacketConn // public-facing UDP listener
	sessions     sync.Map       // srcAddr(string) → *UDPSession
	sessionCount atomic.Int64   // current active session count (O(1))
	sessionIPMu  sync.Mutex
	sessionIPs   map[string]int // src IP → active session count
	done         chan struct{}  // shutdown signal
	closeOnce    sync.Once
}

// Close shuts down the UDP proxy state and releases all resources.
func (s *UDPProxyState) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		if s.packetConn != nil {
			s.packetConn.Close()
		}
		// Close all sessions. sess.Close() triggers ReadUDPFrame in udpSessionReverse to
		// return an error, which then calls removeSession in its defer. Both sides race for
		// the same key; LoadAndDelete inside removeSession ensures only one side decrements
		// the count.
		s.sessions.Range(func(key, value any) bool {
			sess := value.(*UDPSession)
			sess.Close()
			s.removeSession(key.(string))
			return true
		})
	})
}

// removeSession atomically removes a session from the sessions map and decrements the count.
// Returns true if this call actually performed the removal (i.e. was the first cleaner).
// Uses LoadAndDelete so that only one goroutine gets loaded=true when multiple compete.
func (s *UDPProxyState) removeSession(key string) bool {
	if value, loaded := s.sessions.LoadAndDelete(key); loaded {
		s.sessionCount.Add(-1)
		if sess, ok := value.(*UDPSession); ok && sess.ipKey != "" {
			s.sessionIPMu.Lock()
			if count := s.sessionIPs[sess.ipKey]; count <= 1 {
				delete(s.sessionIPs, sess.ipKey)
			} else {
				s.sessionIPs[sess.ipKey] = count - 1
			}
			s.sessionIPMu.Unlock()
		}
		return true
	}
	return false
}

func (s *UDPProxyState) storeSession(key string, sess *UDPSession) (*UDPSession, bool) {
	actual, loaded := s.sessions.LoadOrStore(key, sess)
	if loaded {
		return actual.(*UDPSession), false
	}

	s.sessionCount.Add(1)
	if sess.ipKey != "" {
		s.sessionIPMu.Lock()
		if s.sessionIPs == nil {
			s.sessionIPs = make(map[string]int)
		}
		s.sessionIPs[sess.ipKey]++
		s.sessionIPMu.Unlock()
	}

	return sess, true
}

func (s *UDPProxyState) sessionCountForIP(ipKey string) int {
	if ipKey == "" {
		return 0
	}

	s.sessionIPMu.Lock()
	defer s.sessionIPMu.Unlock()
	return s.sessionIPs[ipKey]
}

func (s *UDPProxyState) canCreateSessionForIP(ipKey string) bool {
	if s.sessionCount.Load() >= int64(MaxUDPSessions) {
		return false
	}
	return s.sessionCountForIP(ipKey) < MaxUDPSessionsPerIP
}

// UDPSession represents a single virtual UDP session identified by an external srcAddr.
type UDPSession struct {
	srcAddr    net.Addr     // external source address
	ipKey      string       // source IP (without port), used for per-IP quota
	stream     net.Conn     // yamux stream (framed transport)
	lastActive atomic.Int64 // last active timestamp (UnixNano)
	done       chan struct{}
	closeOnce  sync.Once
}

// Close shuts down the session.
func (s *UDPSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		if s.stream != nil {
			s.stream.Close()
		}
	})
}

// Touch updates the last active timestamp.
func (s *UDPSession) Touch() {
	s.lastActive.Store(time.Now().UnixNano())
}

// IdleDuration returns how long the session has been idle.
func (s *UDPSession) IdleDuration() time.Duration {
	last := s.lastActive.Load()
	return time.Since(time.Unix(0, last))
}

// UDP session management constants.
const (
	UDPSessionTimeout   = 60 * time.Second // session idle timeout
	UDPReaperInterval   = 10 * time.Second // reaper scan interval
	MaxUDPSessions      = 1024             // max concurrent sessions per UDP proxy
	MaxUDPSessionsPerIP = 128              // max concurrent sessions per source IP
)

func udpSourceIPKey(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	if udpAddr, ok := addr.(*net.UDPAddr); ok {
		if udpAddr.IP != nil {
			return udpAddr.IP.String()
		}
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err == nil {
		return host
	}
	return addr.String()
}

// startUDPProxy starts a UDP proxy tunnel.
// It listens for UDP on RemotePort and creates a new yamux session for each new srcAddr,
// forwarding packets to the client.
func (s *Server) startUDPProxy(client *ClientConn, tunnel *ProxyTunnel) error {
	addr := fmt.Sprintf(":%d", tunnel.Config.RemotePort)
	packetConn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on UDP port %d: %w", tunnel.Config.RemotePort, err)
	}

	actualPort := packetConn.LocalAddr().(*net.UDPAddr).Port
	tunnel.Config.RemotePort = actualPort

	state := &UDPProxyState{
		packetConn: packetConn,
		sessionIPs: make(map[string]int),
		done:       make(chan struct{}),
	}
	tunnel.UDPState = state

	log.Printf("🚇 UDP proxy tunnel created: %s [:%d → %s:%d] Client [%s]",
		tunnel.Config.Name, actualPort, tunnel.Config.LocalIP, tunnel.Config.LocalPort, client.ID)

	// Note: udpReadLoop must run in a single goroutine. If made concurrent,
	// the Load-then-Add upper-bound check on sessionCount must be changed to a CAS atomic operation.
	go s.udpReadLoop(client, tunnel, state)

	// Start the periodic stale-session reaper.
	go s.udpReaper(state)

	return nil
}

func (s *Server) markUDPProxyRuntimeErrorIfCurrent(
	client *ClientConn,
	tunnel *ProxyTunnel,
	state *UDPProxyState,
	message string,
) {
	if state != nil {
		state.Close()
	}

	client.proxyMu.Lock()
	current, exists := client.proxies[tunnel.Config.Name]
	if !exists ||
		current != tunnel ||
		current.UDPState != state ||
		!isTunnelExposed(current.Config) {
		client.proxyMu.Unlock()
		return
	}
	setProxyConfigStates(&current.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message)
	config := current.Config
	client.proxyMu.Unlock()

	if err := s.persistTunnelStates(client.ID, tunnel.Config.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message); err != nil {
		log.Printf("⚠️ UDP proxy [%s] failed to persist error state: %v", tunnel.Config.Name, err)
	}
	s.emitTunnelChanged(client.ID, config, "error")
	if err := s.notifyClientProxyClose(client, tunnel.Config.Name, "runtime_error"); err != nil {
		log.Printf("⚠️ UDP proxy [%s] failed to notify client of close: %v", tunnel.Config.Name, err)
	}
}

// udpReadLoop reads incoming UDP packets from packetConn and dispatches them
// to the appropriate yamux stream by srcAddr.
func (s *Server) udpReadLoop(client *ClientConn, tunnel *ProxyTunnel, state *UDPProxyState) {
	buf := make([]byte, mux.MaxUDPPayload)

	for {
		select {
		case <-state.done:
			return
		default:
		}

		n, srcAddr, err := state.packetConn.ReadFrom(buf)
		if err != nil {
			select {
			case <-state.done:
				return // normal shutdown
			default:
				log.Printf("⚠️ UDP proxy [%s] ReadFrom failed: %v", tunnel.Config.Name, err)
				s.markUDPProxyRuntimeErrorIfCurrent(
					client,
					tunnel,
					state,
					fmt.Sprintf("UDP proxy read failed: %v", err),
				)
				return
			}
		}

		key := srcAddr.String()
		ipKey := udpSourceIPKey(srcAddr)

		// Look up or create a session.
		val, loaded := state.sessions.Load(key)
		if !loaded {
			// Check session count limit.
			// sessionCount.Load() and the subsequent Add(1) are not atomic;
			// this is safe because the entire function runs in a single goroutine (non-concurrent).
			if state.sessionCount.Load() >= int64(MaxUDPSessions) {
				log.Printf("⚠️ UDP proxy [%s] session limit reached (%d), dropping packet from %s",
					tunnel.Config.Name, MaxUDPSessions, key)
				continue
			}
			if !state.canCreateSessionForIP(ipKey) {
				log.Printf("⚠️ UDP proxy [%s] per-IP session limit reached (%d), dropping packet from %s",
					tunnel.Config.Name, MaxUDPSessionsPerIP, key)
				continue
			}

			// Open a new yamux stream.
			stream, err := s.openStreamToClient(client, tunnel.Config.Name)
			if err != nil {
				log.Printf("⚠️ UDP proxy [%s] failed to open stream: %v", tunnel.Config.Name, err)
				s.markUDPProxyRuntimeErrorIfCurrent(
					client,
					tunnel,
					state,
					fmt.Sprintf("UDP proxy forwarding channel failed: %v", err),
				)
				return
			}

			sess := &UDPSession{
				srcAddr: srcAddr,
				ipKey:   ipKey,
				stream:  stream,
				done:    make(chan struct{}),
			}
			sess.Touch()

			// Attempt to store; a concurrent creation is possible, use storeSession to handle
			// the race and maintain counts.
			actual, added := state.storeSession(key, sess)
			if !added {
				// Another goroutine already created one; close ours.
				stream.Close()
				val = actual
			} else {
				val = sess
				// Start the reverse read loop: stream → reply to srcAddr.
				go s.udpSessionReverse(state, sess, client.ID, tunnel.Config.Name)
			}
		}

		sess := val.(*UDPSession)
		sess.Touch()

		// Frame and write the UDP packet into the yamux stream.
		if err := mux.WriteUDPFrame(sess.stream, buf[:n]); err != nil {
			log.Printf("⚠️ UDP proxy [%s] failed to write to stream [%s]: %v",
				tunnel.Config.Name, key, err)
			// Close the failed session; removeSession uses LoadAndDelete for atomicity —
			// even if udpReaper or Close() already cleaned it up, we will just get loaded=false
			// and avoid a double-decrement.
			sess.Close()
			state.removeSession(key)
		} else if s.trafficStore != nil {
			s.trafficStore.RecordBytes(client.ID, tunnel.Config.Name, tunnel.Config.Type, uint64(n), 0)
		}
	}
}

// udpSessionReverse reads reply frames from the yamux stream and writes them back to the
// external client via packetConn.
// Exit mechanism: the goroutine blocks on ReadUDPFrame and exits when sess.Close()→stream.Close()
// is called — this is intentional and no separate ReadDeadline is needed for ReadUDPFrame.
func (s *Server) udpSessionReverse(state *UDPProxyState, sess *UDPSession, clientID, proxyName string) {
	defer func() {
		sess.Close()
		state.removeSession(sess.srcAddr.String())
	}()

	for {
		select {
		case <-sess.done:
			return
		case <-state.done:
			return
		default:
		}

		payload, err := mux.ReadUDPFrame(sess.stream)
		if err != nil {
			select {
			case <-sess.done:
			case <-state.done:
			default:
				// Only log on unexpected close (avoids noise from timeout-triggered cleanup).
			}
			return
		}

		sess.Touch()

		if _, err := state.packetConn.WriteTo(payload, sess.srcAddr); err != nil {
			log.Printf("⚠️ UDP proxy [%s] WriteTo failed [%s]: %v",
				proxyName, sess.srcAddr.String(), err)
			return
		}
		if s.trafficStore != nil {
			s.trafficStore.RecordBytes(clientID, proxyName, protocol.ProxyTypeUDP, 0, uint64(len(payload)))
		}
	}
}

// udpReaper periodically scans and cleans up timed-out UDP sessions.
func (s *Server) udpReaper(state *UDPProxyState) {
	ticker := time.NewTicker(UDPReaperInterval)
	defer ticker.Stop()

	for {
		select {
		case <-state.done:
			return
		case <-ticker.C:
			state.sessions.Range(func(key, value any) bool {
				sess := value.(*UDPSession)
				if sess.IdleDuration() > UDPSessionTimeout {
					log.Printf("🧹 UDP session timed out, cleaning up: %s", key)
					sess.Close()
					state.removeSession(key.(string))
				}
				return true
			})
		}
	}
}
