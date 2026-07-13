package p2p

import (
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/logging"
	"github.com/pion/stun/v3"
	"github.com/pion/transport/v4/vnet"
	"github.com/pion/webrtc/v4"

	"netsgo/pkg/protocol"
)

const (
	vnetSTUNIP   = "1.2.3.4"
	vnetSTUNPort = 3478
)

type p2pVNet struct {
	router    *vnet.Router
	offerNet  *vnet.Net
	answerNet *vnet.Net
	stunConn  net.PacketConn
}

func (n *p2pVNet) close() {
	if n.stunConn != nil {
		_ = n.stunConn.Close()
	}
	_ = n.router.Stop()
}

func TestSessionNetworkMatrix(t *testing.T) {
	t.Run("same routed network without STUN", func(t *testing.T) {
		network := newSameLANVNet(t)
		defer network.close()
		assertSessionsConnectAcrossVNet(t, network, nil)
	})

	t.Run("separate full cone NATs with STUN", func(t *testing.T) {
		fullCone := &vnet.NATType{MappingBehavior: vnet.EndpointIndependent, FilteringBehavior: vnet.EndpointIndependent}
		network := newNATVNet(t, fullCone, fullCone)
		defer network.close()
		assertSessionsConnectAcrossVNet(t, network, []protocol.P2PICEServer{{URLs: []string{fmt.Sprintf("stun:%s:%d", vnetSTUNIP, vnetSTUNPort)}}})
	})

	t.Run("separate symmetric NATs cannot direct connect with STUN only", func(t *testing.T) {
		strict := &vnet.NATType{MappingBehavior: vnet.EndpointAddrPortDependent, FilteringBehavior: vnet.EndpointAddrPortDependent}
		network := newNATVNet(t, strict, strict)
		defer network.close()
		assertSessionsFailAcrossVNet(t, network, []protocol.P2PICEServer{{URLs: []string{fmt.Sprintf("stun:%s:%d", vnetSTUNIP, vnetSTUNPort)}}})
	})
}

func newSameLANVNet(t *testing.T) *p2pVNet {
	t.Helper()
	logger := logging.NewDefaultLoggerFactory()
	router, err := vnet.NewRouter(&vnet.RouterConfig{CIDR: "10.10.0.0/24", LoggerFactory: logger})
	if err != nil {
		t.Fatal(err)
	}
	offerNet, err := vnet.NewNet(&vnet.NetConfig{StaticIPs: []string{"10.10.0.2"}})
	if err != nil {
		t.Fatal(err)
	}
	answerNet, err := vnet.NewNet(&vnet.NetConfig{StaticIPs: []string{"10.10.0.3"}})
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
	return &p2pVNet{router: router, offerNet: offerNet, answerNet: answerNet}
}

func newNATVNet(t *testing.T, offerNAT, answerNAT *vnet.NATType) *p2pVNet {
	return newNATVNetWithConditions(t, offerNAT, answerNAT, 0, 0, serveVNetSTUN)
}

func newNATVNetWithConditions(t *testing.T, offerNAT, answerNAT *vnet.NATType, minDelay, maxJitter time.Duration, serveSTUN func(net.PacketConn)) *p2pVNet {
	t.Helper()
	logger := logging.NewDefaultLoggerFactory()
	wan, err := vnet.NewRouter(&vnet.RouterConfig{CIDR: "0.0.0.0/0", MinDelay: minDelay, MaxJitter: maxJitter, LoggerFactory: logger})
	if err != nil {
		t.Fatal(err)
	}
	wanNet, err := vnet.NewNet(&vnet.NetConfig{StaticIPs: []string{vnetSTUNIP}})
	if err != nil {
		t.Fatal(err)
	}
	if err := wan.AddNet(wanNet); err != nil {
		t.Fatal(err)
	}
	offerNet := addNATLAN(t, wan, logger, "192.168.10.1", "27.1.1.1", offerNAT)
	answerNet := addNATLAN(t, wan, logger, "10.20.0.1", "28.1.1.1", answerNAT)
	if err := wan.Start(); err != nil {
		t.Fatal(err)
	}
	stunConn, err := wanNet.ListenPacket("udp4", net.JoinHostPort(vnetSTUNIP, fmt.Sprintf("%d", vnetSTUNPort)))
	if err != nil {
		t.Fatal(err)
	}
	if serveSTUN != nil {
		go serveSTUN(stunConn)
	}
	return &p2pVNet{router: wan, offerNet: offerNet, answerNet: answerNet, stunConn: stunConn}
}

func addNATLAN(t *testing.T, wan *vnet.Router, logger logging.LoggerFactory, localIP, globalIP string, natType *vnet.NATType) *vnet.Net {
	t.Helper()
	lan, err := vnet.NewRouter(&vnet.RouterConfig{StaticIPs: []string{globalIP}, CIDR: localIP + "/24", NATType: natType, LoggerFactory: logger})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := vnet.NewNet(&vnet.NetConfig{StaticIPs: []string{localIP}})
	if err != nil {
		t.Fatal(err)
	}
	if err := lan.AddNet(endpoint); err != nil {
		t.Fatal(err)
	}
	if err := wan.AddRouter(lan); err != nil {
		t.Fatal(err)
	}
	return endpoint
}

func serveVNetSTUN(conn net.PacketConn) {
	buf := make([]byte, 1500)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		request := &stun.Message{Raw: append([]byte(nil), buf[:n]...)}
		if err := request.Decode(); err != nil || request.Type != stun.BindingRequest {
			continue
		}
		udpAddr, ok := addr.(*net.UDPAddr)
		if !ok {
			continue
		}
		response, err := stun.Build(stun.BindingSuccess, stun.NewTransactionIDSetter(request.TransactionID), &stun.XORMappedAddress{IP: udpAddr.IP, Port: udpAddr.Port}, stun.Fingerprint)
		if err == nil {
			_, _ = conn.WriteTo(response.Raw, addr)
		}
	}
}

func newVNetSession(t *testing.T, role string, network *vnet.Net, servers []protocol.P2PICEServer, signal SignalHandler) *Session {
	t.Helper()
	session, err := newSession(role, servers, signal, func(engine *webrtc.SettingEngine) {
		engine.SetNet(network)
		engine.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
		engine.SetICETimeouts(700*time.Millisecond, 700*time.Millisecond, 200*time.Millisecond)
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func negotiateVNetSessions(t *testing.T, network *p2pVNet, servers []protocol.P2PICEServer) (*Session, *Session) {
	t.Helper()
	offerSignals := make(chan protocol.P2PSignal, protocol.P2PMaxCandidates+4)
	answerSignals := make(chan protocol.P2PSignal, protocol.P2PMaxCandidates+4)
	offerer := newVNetSession(t, protocol.P2PRoleOfferer, network.offerNet, servers, func(signal protocol.P2PSignal) { offerSignals <- signal })
	answerer := newVNetSession(t, protocol.P2PRoleAnswerer, network.answerNet, servers, func(signal protocol.P2PSignal) { answerSignals <- signal })
	t.Cleanup(func() {
		_ = offerer.Close()
		_ = answerer.Close()
	})
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			select {
			case signal := <-offerSignals:
				_ = answerer.AddCandidate(signal)
			case <-stop:
				return
			}
		}
	}()
	go func() {
		for {
			select {
			case signal := <-answerSignals:
				_ = offerer.AddCandidate(signal)
			case <-stop:
				return
			}
		}
	}()
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

func assertSessionsConnectAcrossVNet(t *testing.T, network *p2pVNet, servers []protocol.P2PICEServer) {
	t.Helper()
	offerer, answerer := negotiateVNetSessions(t, network, servers)
	waitVNetReady(t, offerer, true)
	waitVNetReady(t, answerer, true)
	accepted := make(chan error, 1)
	go func() {
		conn, err := answerer.Accept()
		if err != nil {
			accepted <- err
			return
		}
		defer func() { _ = conn.Close() }()
		payload := make([]byte, 4)
		_, err = io.ReadFull(conn, payload)
		if err == nil {
			_, err = conn.Write(payload)
		}
		accepted <- err
	}()
	conn, err := offerer.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("vnet")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "vnet" {
		t.Fatalf("vnet payload=%q err=%v", got, err)
	}
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
	closeSessionBounded(t, offerer)
	closeSessionBounded(t, answerer)
}

func closeSessionBounded(t *testing.T, session *Session) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- session.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("close P2P session: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("P2P session close blocked")
	}
}

func assertSessionsFailAcrossVNet(t *testing.T, network *p2pVNet, servers []protocol.P2PICEServer) {
	t.Helper()
	offerer, answerer := negotiateVNetSessions(t, network, servers)
	waitVNetReady(t, offerer, false)
	waitVNetReady(t, answerer, false)
}

func waitVNetReady(t *testing.T, session *Session, wantAvailable bool) {
	t.Helper()
	select {
	case <-session.Ready():
		if session.Available() != wantAvailable {
			t.Fatalf("session availability=%v want=%v err=%v", session.Available(), wantAvailable, session.Err())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("session readiness timeout; available=%v err=%v", session.Available(), session.Err())
	}
}
