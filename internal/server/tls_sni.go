package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"syscall"
	"time"

	"netsgo/internal/connlimit"
	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

type sniAcceptedConn struct {
	conn net.Conn
	raw  net.Conn
}

// A single socket feeds both untouched TLS tunnels and the existing HTTP/TLS
// listener. Pending hellos and passthrough streams remain owned by this listener
// until HTTP accepts them, or until forwarding finishes.
type sniListener struct {
	net.Listener
	ready  chan sniAcceptedConn
	done   chan struct{}
	mu     sync.Mutex
	closed bool
	conns  map[net.Conn]struct{}
	route  func(net.Conn, string) bool
}

func newSNIListener(listener net.Listener, route func(net.Conn, string) bool) *sniListener {
	l := &sniListener{Listener: listener, route: route, ready: make(chan sniAcceptedConn), done: make(chan struct{}), conns: make(map[net.Conn]struct{})}
	go l.acceptLoop()
	return l
}

func (l *sniListener) acceptLoop() {
	var retryDelay time.Duration
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			if errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) ||
				errors.Is(err, syscall.ENOBUFS) || errors.Is(err, syscall.ENOMEM) || errors.Is(err, syscall.ECONNABORTED) {
				if retryDelay == 0 {
					retryDelay = 5 * time.Millisecond
				} else {
					retryDelay = min(2*retryDelay, time.Second)
				}
				timer := time.NewTimer(retryDelay)
				select {
				case <-timer.C:
					continue
				case <-l.done:
					timer.Stop()
					return
				}
			}
			l.stop()
			return
		}
		retryDelay = 0
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			_ = conn.Close()
			return
		}
		l.conns[conn] = struct{}{}
		l.mu.Unlock()
		go l.dispatch(conn)
	}
}

func (l *sniListener) dispatch(raw net.Conn) {
	handedOff := false
	defer func() {
		l.mu.Lock()
		delete(l.conns, raw)
		l.mu.Unlock()
		if !handedOff {
			_ = raw.Close()
		}
	}()
	conn, name, isTLS, err := prereadTLS(raw)
	if err != nil {
		return
	}
	if isTLS && l.route(conn, name) {
		return
	}
	select {
	case l.ready <- sniAcceptedConn{conn: conn, raw: raw}:
		handedOff = true
	case <-l.done:
	}
}

func (l *sniListener) Accept() (net.Conn, error) {
	select {
	case item := <-l.ready:
		l.mu.Lock()
		closed := l.closed
		delete(l.conns, item.raw)
		l.mu.Unlock()
		if closed {
			_ = item.conn.Close()
			return nil, net.ErrClosed
		}
		return item.conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *sniListener) stop() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	close(l.done)
	conns := make([]net.Conn, 0, len(l.conns))
	for conn := range l.conns {
		conns = append(conns, conn)
	}
	l.mu.Unlock()
	_ = l.Listener.Close()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func (l *sniListener) Close() error {
	l.stop()
	// Forwarding routes register with Server's long-lived handler lifecycle.
	// Shutdown(ctx) owns that bounded wait; listener Close must also return
	// promptly when a route is waiting on bandwidth rather than socket I/O.
	return nil
}

func isTLSIngress(config protocol.ProxyConfig) bool {
	return config.Ingress != nil && config.Ingress.Type == protocol.IngressTypeTLSSNI
}

func (s *Server) isManagementSNI(serverName string) (bool, error) {
	var cfg *ServerConfig
	if s.auth.adminStore != nil {
		current, err := s.auth.adminStore.GetServerConfigE()
		if err != nil {
			return false, err
		}
		cfg = &current
	}
	management, _ := splitCanonicalHostPort(effectiveManagementHost(cfg, serverListenAddr(s)))
	return canonicalHTTPDomain(management) == canonicalHTTPDomain(serverName), nil
}

func (s *Server) validateSNIManagementDomain(domain string) error {
	reserved, err := s.isManagementSNI(domain)
	if err != nil {
		return newProxyRequestValidationError(err, "domain", "", http.StatusServiceUnavailable)
	}
	if reserved {
		return newProxyRequestValidationError(fmt.Errorf("domain is reserved for the NetsGo management service"), "domain", protocol.TunnelMutationErrorCodeServerAddrConflict, http.StatusConflict)
	}
	return nil
}

func (s *Server) routeSNIConnection(conn net.Conn, serverName string) bool {
	if serverName == "" {
		return false
	} // Existing IP-based clients have no SNI.
	reserved, err := s.isManagementSNI(serverName)
	if err != nil {
		return true
	}
	if reserved {
		return false
	}
	var route httpTunnelRoute
	if s.store != nil {
		claim, found, err := s.store.findHTTPDomainClaim(serverName)
		if err != nil {
			return true
		}
		if !found || claim.Ingress.Type != protocol.IngressTypeTLSSNI {
			return false
		}
		route.config = storedTunnelToProxyConfig(claim)
		if claim.DesiredState == protocol.ProxyDesiredStateRunning {
			if live, ok := s.findRuntimeDomainRouteForClaim(serverName, &claim, protocol.IngressTypeTLSSNI); ok {
				route = live
			}
		}
	} else {
		live, found := s.findRuntimeDomainRouteForClaim(serverName, nil, protocol.IngressTypeTLSSNI)
		if !found {
			return false
		}
		route = live
	}
	finish, accepted := s.beginLongLivedHandler()
	if !accepted {
		return true
	}
	defer finish()
	if !route.serviceable() || !sourceAddressAllowed(conn.RemoteAddr(), route.sourceCIDRs) {
		return true
	}
	stream, err := s.openStreamToClientForActivation(route.client, route.tunnel, route.activation)
	if err != nil {
		if errors.Is(err, connlimit.ErrReached) || mux.IsClosedStream(err) {
			return true
		}
		s.markTCPProxyRuntimeErrorIfCurrent(route.client, route.activation.config.Name, route.tunnel, nil, route.activation.config, "TLS tunnel forwarding channel failed: "+err.Error())
		return true
	}
	defer func() { _ = stream.Close() }()
	relayDone := make(chan struct{})
	defer close(relayDone)
	go func() {
		select {
		case <-route.activation.done:
		case <-s.done:
		case <-relayDone:
			return
		}
		_ = conn.Close()
		_ = stream.Close()
	}()
	var observer tunnelTrafficObserver
	if s.trafficStore != nil {
		observer = func(ingress, egress uint64) { s.recordTunnelTraffic(route.client.ID, route.config, ingress, egress) }
	}
	_, _ = relayTunnelPayload(stream, conn, route.client.BandwidthRuntime(), route.activation.limits, observer)
	return true
}
