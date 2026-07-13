package p2p

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/logging"
	"github.com/pion/stun/v3"
	"github.com/pion/transport/v4/vnet"
	"github.com/pion/webrtc/v4"

	"netsgo/pkg/protocol"
)

var (
	fullConeNAT           = &vnet.NATType{MappingBehavior: vnet.EndpointIndependent, FilteringBehavior: vnet.EndpointIndependent}
	restrictedConeNAT     = &vnet.NATType{MappingBehavior: vnet.EndpointIndependent, FilteringBehavior: vnet.EndpointAddrDependent}
	portRestrictedConeNAT = &vnet.NATType{MappingBehavior: vnet.EndpointIndependent, FilteringBehavior: vnet.EndpointAddrPortDependent}
	symmetricNAT          = &vnet.NATType{MappingBehavior: vnet.EndpointAddrPortDependent, FilteringBehavior: vnet.EndpointAddrPortDependent}
	stunServers           = []protocol.P2PICEServer{{URLs: []string{fmt.Sprintf("stun:%s:%d", vnetSTUNIP, vnetSTUNPort)}}}
)

func TestSessionExtendedNATMatrix(t *testing.T) {
	tests := []struct {
		name                string
		offerNAT, answerNAT *vnet.NATType
		wantConnect         bool
	}{
		{name: "full cone to address restricted cone", offerNAT: fullConeNAT, answerNAT: restrictedConeNAT, wantConnect: true},
		{name: "two address restricted cone NATs", offerNAT: restrictedConeNAT, answerNAT: restrictedConeNAT, wantConnect: true},
		{name: "two port restricted cone NATs", offerNAT: portRestrictedConeNAT, answerNAT: portRestrictedConeNAT, wantConnect: true},
		{name: "full cone to symmetric NAT", offerNAT: fullConeNAT, answerNAT: symmetricNAT, wantConnect: true},
		{name: "port restricted cone to symmetric NAT", offerNAT: portRestrictedConeNAT, answerNAT: symmetricNAT, wantConnect: false},
		{name: "two symmetric NATs", offerNAT: symmetricNAT, answerNAT: symmetricNAT, wantConnect: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			network := newNATVNet(t, tt.offerNAT, tt.answerNAT)
			defer network.close()
			if tt.wantConnect {
				assertSessionsConnectAcrossVNet(t, network, stunServers)
			} else {
				assertSessionsFailAcrossVNet(t, network, stunServers)
			}
		})
	}
}

func TestSessionPublicEndpointToNATMatrix(t *testing.T) {
	for _, tt := range []struct {
		name string
		nat  *vnet.NATType
	}{
		{name: "public to full cone", nat: fullConeNAT},
		{name: "public to port restricted cone", nat: portRestrictedConeNAT},
		{name: "public to symmetric", nat: symmetricNAT},
	} {
		t.Run(tt.name, func(t *testing.T) {
			network := newPublicToNATVNet(t, tt.nat)
			defer network.close()
			assertSessionsConnectAcrossVNet(t, network, stunServers)
		})
	}
}

func newPublicToNATVNet(t *testing.T, answerNAT *vnet.NATType) *p2pVNet {
	t.Helper()
	logger := logging.NewDefaultLoggerFactory()
	wan, err := vnet.NewRouter(&vnet.RouterConfig{CIDR: "0.0.0.0/0", LoggerFactory: logger})
	if err != nil {
		t.Fatal(err)
	}
	wanNet, err := vnet.NewNet(&vnet.NetConfig{StaticIPs: []string{vnetSTUNIP}})
	if err != nil {
		t.Fatal(err)
	}
	publicNet, err := vnet.NewNet(&vnet.NetConfig{StaticIPs: []string{"29.1.1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := wan.AddNet(wanNet); err != nil {
		t.Fatal(err)
	}
	if err := wan.AddNet(publicNet); err != nil {
		t.Fatal(err)
	}
	answerNet := addNATLAN(t, wan, logger, "10.20.0.1", "28.1.1.1", answerNAT)
	if err := wan.Start(); err != nil {
		t.Fatal(err)
	}
	stunConn, err := wanNet.ListenPacket("udp4", net.JoinHostPort(vnetSTUNIP, fmt.Sprintf("%d", vnetSTUNPort)))
	if err != nil {
		t.Fatal(err)
	}
	go serveVNetSTUN(stunConn)
	return &p2pVNet{router: wan, offerNet: publicNet, answerNet: answerNet, stunConn: stunConn}
}

func TestSessionSTUNFailureMatrix(t *testing.T) {
	t.Run("unreachable STUN leaves isolated NAT peers disconnected", func(t *testing.T) {
		network := newNATVNetWithConditions(t, fullConeNAT, fullConeNAT, 0, 0, nil)
		defer network.close()
		assertSessionsFailAcrossVNet(t, network, stunServers)
	})
	t.Run("malformed STUN responses are ignored", func(t *testing.T) {
		network := newNATVNetWithConditions(t, fullConeNAT, fullConeNAT, 0, 0, serveMalformedVNetSTUN)
		defer network.close()
		assertSessionsFailAcrossVNet(t, network, stunServers)
	})
	t.Run("STUN binding errors leave isolated NAT peers disconnected", func(t *testing.T) {
		network := newNATVNetWithConditions(t, fullConeNAT, fullConeNAT, 0, 0, serveErrorVNetSTUN)
		defer network.close()
		assertSessionsFailAcrossVNet(t, network, stunServers)
	})
}

func TestSessionUDPFirewallBlocksDirectPath(t *testing.T) {
	network := newNATVNet(t, fullConeNAT, fullConeNAT)
	defer network.close()
	network.router.AddChunkFilter(func(chunk vnet.Chunk) bool { return chunk.Network() != "udp" })
	assertSessionsFailAcrossVNet(t, network, stunServers)
}

func serveMalformedVNetSTUN(conn net.PacketConn) {
	buffer := make([]byte, 1500)
	for {
		_, addr, err := conn.ReadFrom(buffer)
		if err != nil {
			return
		}
		_, _ = conn.WriteTo([]byte("not-a-stun-message"), addr)
	}
}

func serveErrorVNetSTUN(conn net.PacketConn) {
	buffer := make([]byte, 1500)
	for {
		n, addr, err := conn.ReadFrom(buffer)
		if err != nil {
			return
		}
		request := &stun.Message{Raw: append([]byte(nil), buffer[:n]...)}
		if request.Decode() != nil || request.Type != stun.BindingRequest {
			continue
		}
		response, err := stun.Build(stun.BindingError, stun.NewTransactionIDSetter(request.TransactionID), &stun.ErrorCodeAttribute{Code: stun.CodeServerError, Reason: []byte("synthetic STUN failure")}, stun.Fingerprint)
		if err == nil {
			_, _ = conn.WriteTo(response.Raw, addr)
		}
	}
}

func TestSessionCandidateDeliveryDelayReorderDuplicateAndLoss(t *testing.T) {
	t.Run("delayed reversed and duplicated candidates still connect", func(t *testing.T) {
		network := newNATVNet(t, fullConeNAT, fullConeNAT)
		defer network.close()
		offerer, answerer := negotiateVNetSessionsBufferedCandidates(t, network, stunServers, func(signals []protocol.P2PSignal) []protocol.P2PSignal {
			var transformed []protocol.P2PSignal
			for i := len(signals) - 1; i >= 0; i-- {
				transformed = append(transformed, signals[i], signals[i])
			}
			return transformed
		})
		waitVNetReady(t, offerer, true)
		waitVNetReady(t, answerer, true)
		assertSessionPayload(t, offerer, answerer, []byte("candidate-reorder-duplicate"))
	})
	t.Run("dropping host candidates keeps STUN reflexive path usable", func(t *testing.T) {
		network := newNATVNet(t, fullConeNAT, fullConeNAT)
		defer network.close()
		offerer, answerer := negotiateVNetSessionsBufferedCandidates(t, network, stunServers, func(signals []protocol.P2PSignal) []protocol.P2PSignal {
			filtered := signals[:0]
			for _, signal := range signals {
				if !strings.Contains(signal.Candidate, " typ host") {
					filtered = append(filtered, signal)
				}
			}
			return filtered
		})
		waitVNetReady(t, offerer, true)
		waitVNetReady(t, answerer, true)
		assertSessionPayload(t, offerer, answerer, []byte("srflx-only"))
	})
}

func negotiateVNetSessionsBufferedCandidates(t *testing.T, network *p2pVNet, servers []protocol.P2PICEServer, transform func([]protocol.P2PSignal) []protocol.P2PSignal) (*Session, *Session) {
	t.Helper()
	offerSignals := make(chan protocol.P2PSignal, protocol.P2PMaxCandidates+4)
	answerSignals := make(chan protocol.P2PSignal, protocol.P2PMaxCandidates+4)
	offerer := newVNetSession(t, protocol.P2PRoleOfferer, network.offerNet, servers, func(signal protocol.P2PSignal) { offerSignals <- signal })
	answerer := newVNetSession(t, protocol.P2PRoleAnswerer, network.answerNet, servers, func(signal protocol.P2PSignal) { answerSignals <- signal })
	t.Cleanup(func() { _ = offerer.Close(); _ = answerer.Close() })
	offer, err := offerer.CreateOffer()
	if err != nil {
		t.Fatal(err)
	}
	answer, err := answerer.AcceptOffer(offer)
	if err != nil {
		t.Fatal(err)
	}
	if err := offerer.AcceptAnswer(answer); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	drain := func(ch <-chan protocol.P2PSignal) []protocol.P2PSignal {
		var signals []protocol.P2PSignal
		for {
			select {
			case signal := <-ch:
				signals = append(signals, signal)
			default:
				return signals
			}
		}
	}
	for _, signal := range transform(drain(offerSignals)) {
		_ = answerer.AddCandidate(signal)
	}
	for _, signal := range transform(drain(answerSignals)) {
		_ = offerer.AddCandidate(signal)
	}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go relayRemainingCandidates(stop, offerSignals, answerer)
	go relayRemainingCandidates(stop, answerSignals, offerer)
	return offerer, answerer
}

func relayRemainingCandidates(stop <-chan struct{}, signals <-chan protocol.P2PSignal, destination *Session) {
	for {
		select {
		case signal := <-signals:
			_ = destination.AddCandidate(signal)
		case <-stop:
			return
		}
	}
}

func TestSessionImpairedNetworkPreservesLargePayload(t *testing.T) {
	network := newNATVNetWithConditions(t, fullConeNAT, fullConeNAT, 8*time.Millisecond, 12*time.Millisecond, serveVNetSTUN)
	defer network.close()
	var impairmentEnabled atomic.Bool
	var packetNumber atomic.Uint64
	network.router.AddChunkFilter(func(chunk vnet.Chunk) bool {
		if !impairmentEnabled.Load() || chunk.Network() != "udp" {
			return true
		}
		return packetNumber.Add(1)%20 != 0
	})
	duplication, err := vnet.NewDuplicationFilter(network.router, vnet.WithDuplicationProbability(0.08), vnet.WithDuplicationExtraDelay(2*time.Millisecond, 15*time.Millisecond), vnet.WithDuplicationSeed(42))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = duplication.Close() }()
	network.router.AddChunkFilter(func(chunk vnet.Chunk) bool {
		if !impairmentEnabled.Load() {
			return true
		}
		return duplication.ChunkFilter()(chunk)
	})
	offerer, answerer := negotiateVNetSessions(t, network, stunServers)
	waitVNetReady(t, offerer, true)
	waitVNetReady(t, answerer, true)
	impairmentEnabled.Store(true)
	payload := bytes.Repeat([]byte("loss-delay-jitter-duplication-"), 24*1024)
	assertSessionPayload(t, offerer, answerer, payload)
}

func assertSessionPayload(t *testing.T, offerer, answerer *Session, payload []byte) {
	t.Helper()
	accepted := make(chan error, 1)
	go func() {
		conn, err := answerer.Accept()
		if err != nil {
			accepted <- err
			return
		}
		defer func() { _ = conn.Close() }()
		got := make([]byte, len(payload))
		if _, err := io.ReadFull(conn, got); err != nil {
			accepted <- err
			return
		}
		if !bytes.Equal(got, payload) {
			accepted <- fmt.Errorf("received payload mismatch")
			return
		}
		_, err = conn.Write(got)
		accepted <- err
	}()
	conn, err := offerer.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(60 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("echoed payload mismatch")
	}
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
}

func TestSessionMultipleInterfaceCandidates(t *testing.T) {
	logger := logging.NewDefaultLoggerFactory()
	router, err := vnet.NewRouter(&vnet.RouterConfig{CIDR: "10.30.0.0/24", LoggerFactory: logger})
	if err != nil {
		t.Fatal(err)
	}
	offerNet, err := vnet.NewNet(&vnet.NetConfig{StaticIPs: []string{"10.30.0.2", "10.30.0.4"}})
	if err != nil {
		t.Fatal(err)
	}
	answerNet, err := vnet.NewNet(&vnet.NetConfig{StaticIPs: []string{"10.30.0.3", "10.30.0.5"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := router.AddNet(offerNet); err != nil {
		t.Fatal(err)
	}
	if err := router.AddNet(answerNet); err != nil {
		t.Fatal(err)
	}
	if err := router.Start(); err != nil {
		t.Fatal(err)
	}
	network := &p2pVNet{router: router, offerNet: offerNet, answerNet: answerNet}
	defer network.close()
	var offerCandidates, answerCandidates atomic.Int32
	offerer, answerer := negotiateVNetSessionsCountingCandidates(t, network, &offerCandidates, &answerCandidates)
	waitVNetReady(t, offerer, true)
	waitVNetReady(t, answerer, true)
	if offerCandidates.Load() < 2 || answerCandidates.Load() < 2 {
		t.Fatalf("multiple interfaces did not produce multiple candidates: offer=%d answer=%d", offerCandidates.Load(), answerCandidates.Load())
	}
	assertSessionPayload(t, offerer, answerer, []byte("multiple-interface-candidates"))
}

func negotiateVNetSessionsCountingCandidates(t *testing.T, network *p2pVNet, offerCount, answerCount *atomic.Int32) (*Session, *Session) {
	t.Helper()
	offerSignals := make(chan protocol.P2PSignal, protocol.P2PMaxCandidates+4)
	answerSignals := make(chan protocol.P2PSignal, protocol.P2PMaxCandidates+4)
	offerer := newVNetSession(t, protocol.P2PRoleOfferer, network.offerNet, nil, func(signal protocol.P2PSignal) { offerCount.Add(1); offerSignals <- signal })
	answerer := newVNetSession(t, protocol.P2PRoleAnswerer, network.answerNet, nil, func(signal protocol.P2PSignal) { answerCount.Add(1); answerSignals <- signal })
	t.Cleanup(func() { _ = offerer.Close(); _ = answerer.Close() })
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go relayRemainingCandidates(stop, offerSignals, answerer)
	go relayRemainingCandidates(stop, answerSignals, offerer)
	offer, err := offerer.CreateOffer()
	if err != nil {
		t.Fatal(err)
	}
	answer, err := answerer.AcceptOffer(offer)
	if err != nil {
		t.Fatal(err)
	}
	if err := offerer.AcceptAnswer(answer); err != nil {
		t.Fatal(err)
	}
	return offerer, answerer
}

func TestSessionIPv6OnlyLoopback(t *testing.T) {
	probe, err := net.ListenPacket("udp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	_ = probe.Close()
	newIPv6Session := func(role string, signal SignalHandler) *Session {
		session, err := newSession(role, nil, signal, func(engine *webrtc.SettingEngine) {
			engine.SetIncludeLoopbackCandidate(true)
			engine.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP6})
			engine.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
			engine.SetICETimeouts(2*time.Second, 2*time.Second, 200*time.Millisecond)
		})
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	var sawIPv6 atomic.Bool
	inspect := func(signal protocol.P2PSignal) {
		candidate, err := ice.UnmarshalCandidate(signal.Candidate)
		if err == nil && candidate.NetworkType() == ice.NetworkTypeUDP6 {
			sawIPv6.Store(true)
		}
	}
	offerSignals := make(chan protocol.P2PSignal, protocol.P2PMaxCandidates+4)
	answerSignals := make(chan protocol.P2PSignal, protocol.P2PMaxCandidates+4)
	offerer := newIPv6Session(protocol.P2PRoleOfferer, func(signal protocol.P2PSignal) { inspect(signal); offerSignals <- signal })
	answerer := newIPv6Session(protocol.P2PRoleAnswerer, func(signal protocol.P2PSignal) { inspect(signal); answerSignals <- signal })
	defer func() { _ = offerer.Close() }()
	defer func() { _ = answerer.Close() }()
	stop := make(chan struct{})
	defer close(stop)
	go relayRemainingCandidates(stop, offerSignals, answerer)
	go relayRemainingCandidates(stop, answerSignals, offerer)
	offer, err := offerer.CreateOffer()
	if err != nil {
		t.Fatal(err)
	}
	answer, err := answerer.AcceptOffer(offer)
	if err != nil {
		t.Fatal(err)
	}
	if err := offerer.AcceptAnswer(answer); err != nil {
		t.Fatal(err)
	}
	if !waitForCandidateTypes(2*time.Second, func() bool { return sawIPv6.Load() }) {
		t.Skip("ICE did not expose an IPv6 loopback candidate")
	}
	waitVNetReady(t, offerer, true)
	waitVNetReady(t, answerer, true)
	assertSessionPayload(t, offerer, answerer, []byte("ipv6-only"))
}

func TestSessionDualStackCandidateCompetition(t *testing.T) {
	probe4, err4 := net.ListenPacket("udp4", "127.0.0.1:0")
	probe6, err6 := net.ListenPacket("udp6", "[::1]:0")
	if err4 != nil || err6 != nil {
		if probe4 != nil {
			_ = probe4.Close()
		}
		if probe6 != nil {
			_ = probe6.Close()
		}
		t.Skipf("dual-stack loopback unavailable: ipv4=%v ipv6=%v", err4, err6)
	}
	_ = probe4.Close()
	_ = probe6.Close()
	var sawIPv4, sawIPv6 atomic.Bool
	inspect := func(signal protocol.P2PSignal) {
		candidate, err := ice.UnmarshalCandidate(signal.Candidate)
		if err != nil {
			return
		}
		switch candidate.NetworkType() {
		case ice.NetworkTypeUDP4:
			sawIPv4.Store(true)
		case ice.NetworkTypeUDP6:
			sawIPv6.Store(true)
		}
	}
	newDualStackSession := func(role string, output chan<- protocol.P2PSignal) *Session {
		session, err := newSession(role, nil, func(signal protocol.P2PSignal) {
			inspect(signal)
			output <- signal
		}, func(engine *webrtc.SettingEngine) {
			engine.SetIncludeLoopbackCandidate(true)
			engine.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6})
			engine.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
			engine.SetICETimeouts(2*time.Second, 2*time.Second, 200*time.Millisecond)
		})
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	offerSignals := make(chan protocol.P2PSignal, protocol.P2PMaxCandidates+4)
	answerSignals := make(chan protocol.P2PSignal, protocol.P2PMaxCandidates+4)
	offerer := newDualStackSession(protocol.P2PRoleOfferer, offerSignals)
	answerer := newDualStackSession(protocol.P2PRoleAnswerer, answerSignals)
	defer func() { _ = offerer.Close() }()
	defer func() { _ = answerer.Close() }()
	stop := make(chan struct{})
	defer close(stop)
	go relayRemainingCandidates(stop, offerSignals, answerer)
	go relayRemainingCandidates(stop, answerSignals, offerer)
	offer, err := offerer.CreateOffer()
	if err != nil {
		t.Fatal(err)
	}
	answer, err := answerer.AcceptOffer(offer)
	if err != nil {
		t.Fatal(err)
	}
	if err := offerer.AcceptAnswer(answer); err != nil {
		t.Fatal(err)
	}
	if !waitForCandidateTypes(2*time.Second, func() bool { return sawIPv4.Load() && sawIPv6.Load() }) {
		t.Skipf("ICE dual-stack gathering unavailable: ipv4=%v ipv6=%v", sawIPv4.Load(), sawIPv6.Load())
	}
	waitVNetReady(t, offerer, true)
	waitVNetReady(t, answerer, true)
	assertSessionPayload(t, offerer, answerer, []byte("dual-stack-candidate-competition"))
}

func waitForCandidateTypes(timeout time.Duration, ready func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ready() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return ready()
}

func TestSessionShortNATMappingLifetimePreservesOrRecoversWithoutCorruption(t *testing.T) {
	shortLived := &vnet.NATType{MappingBehavior: vnet.EndpointIndependent, FilteringBehavior: vnet.EndpointIndependent, MappingLifeTime: 2 * time.Second}
	network := newNATVNet(t, shortLived, shortLived)
	defer network.close()
	offerer, answerer := negotiateVNetSessions(t, network, stunServers)
	waitVNetReady(t, offerer, true)
	waitVNetReady(t, answerer, true)
	assertSessionPayload(t, offerer, answerer, []byte("before-mapping-expiry"))
	time.Sleep(3 * time.Second)
	after := []byte("after-mapping-expiry")
	if err := exchangeSessionPayload(offerer, answerer, after, 3*time.Second); err == nil {
		return
	}
	closeSessionBounded(t, offerer)
	closeSessionBounded(t, answerer)
	freshOfferer, freshAnswerer := negotiateVNetSessions(t, network, stunServers)
	waitVNetReady(t, freshOfferer, true)
	waitVNetReady(t, freshAnswerer, true)
	assertSessionPayload(t, freshOfferer, freshAnswerer, []byte("fresh-session-after-mapping-expiry"))
}

func exchangeSessionPayload(offerer, answerer *Session, payload []byte, timeout time.Duration) error {
	accepted := make(chan error, 1)
	go func() {
		conn, err := answerer.Accept()
		if err != nil {
			accepted <- err
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(timeout))
		got := make([]byte, len(payload))
		if _, err := io.ReadFull(conn, got); err != nil {
			accepted <- err
			return
		}
		if !bytes.Equal(got, payload) {
			accepted <- fmt.Errorf("payload corrupted")
			return
		}
		_, err = conn.Write(got)
		accepted <- err
	}()
	conn, err := offerer.Open()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(payload); err != nil {
		return err
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		return err
	}
	if !bytes.Equal(got, payload) {
		return fmt.Errorf("echoed payload corrupted")
	}
	select {
	case err := <-accepted:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("accept side timed out")
	}
}

func TestSessionConcurrentStreamsUnderLatency(t *testing.T) {
	network := newNATVNetWithConditions(t, restrictedConeNAT, portRestrictedConeNAT, 12*time.Millisecond, 8*time.Millisecond, serveVNetSTUN)
	defer network.close()
	offerer, answerer := negotiateVNetSessions(t, network, stunServers)
	waitVNetReady(t, offerer, true)
	waitVNetReady(t, answerer, true)
	const streams = 24
	serverErrors := make(chan error, streams)
	clientErrors := make(chan error, streams)
	go func() {
		for i := 0; i < streams; i++ {
			conn, err := answerer.Accept()
			if err != nil {
				serverErrors <- err
				continue
			}
			go func(conn net.Conn) {
				defer func() { _ = conn.Close() }()
				_, err := io.Copy(conn, conn)
				serverErrors <- err
			}(conn)
		}
	}()
	var wg sync.WaitGroup
	for i := 0; i < streams; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := offerer.Open()
			if err != nil {
				clientErrors <- err
				return
			}
			defer func() { _ = conn.Close() }()
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			payload := bytes.Repeat([]byte{byte(i)}, 32*1024+i)
			if _, err := conn.Write(payload); err != nil {
				clientErrors <- err
				return
			}
			got := make([]byte, len(payload))
			if _, err := io.ReadFull(conn, got); err != nil {
				clientErrors <- err
				return
			}
			if !bytes.Equal(got, payload) {
				clientErrors <- fmt.Errorf("stream %d payload mismatch", i)
				return
			}
			clientErrors <- nil
		}()
	}
	wg.Wait()
	for i := 0; i < streams; i++ {
		if err := <-clientErrors; err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-serverErrors:
		if err != nil {
			t.Fatal(err)
		}
	default:
	}
}

func TestSessionSustainedLowRateTrafficRemainsExact(t *testing.T) {
	network := newNATVNetWithConditions(t, fullConeNAT, portRestrictedConeNAT, 15*time.Millisecond, 5*time.Millisecond, serveVNetSTUN)
	defer network.close()
	offerer, answerer := negotiateVNetSessions(t, network, stunServers)
	waitVNetReady(t, offerer, true)
	waitVNetReady(t, answerer, true)
	conn, err := offerer.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	accepted, err := answerer.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = accepted.Close() }()
	go func() { _, _ = io.Copy(accepted, accepted) }()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	for i := 0; i < 128; i++ {
		payload := bytes.Repeat([]byte{byte(i)}, 257+i%31)
		if _, err := conn.Write(payload); err != nil {
			t.Fatalf("chunk %d write: %v", i, err)
		}
		got := make([]byte, len(payload))
		if _, err := io.ReadFull(conn, got); err != nil {
			t.Fatalf("chunk %d read: %v", i, err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("chunk %d corrupted", i)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
