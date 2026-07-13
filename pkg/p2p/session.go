// Package p2p provides the Pion-backed peer data transport. It deliberately
// exposes logical byte streams instead of WebRTC concepts to tunnel runtimes.
package p2p

import (
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/hashicorp/yamux"
	"github.com/pion/webrtc/v4"

	netsgomux "netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

const dataChannelID uint16 = 0

type SignalHandler func(protocol.P2PSignal)

type Session struct {
	pc     *webrtc.PeerConnection
	role   string
	signal SignalHandler

	mu            sync.RWMutex
	mux           *yamux.Session
	transport     io.ReadWriteCloser
	openErr       error
	pending       []webrtc.ICECandidateInit
	ready         chan struct{}
	once          sync.Once
	terminated    chan struct{}
	terminateOnce sync.Once
}

func NewSession(role string, iceServers []protocol.P2PICEServer, signal SignalHandler) (*Session, error) {
	return newSession(role, iceServers, signal, nil)
}

func newSession(role string, iceServers []protocol.P2PICEServer, signal SignalHandler, configure func(*webrtc.SettingEngine)) (*Session, error) {
	if role != protocol.P2PRoleOfferer && role != protocol.P2PRoleAnswerer {
		return nil, fmt.Errorf("unsupported p2p role %q", role)
	}
	settingEngine := webrtc.SettingEngine{}
	settingEngine.DetachDataChannels()
	if configure != nil {
		configure(&settingEngine)
	}
	api := webrtc.NewAPI(webrtc.WithSettingEngine(settingEngine))
	config := webrtc.Configuration{ICEServers: make([]webrtc.ICEServer, 0, len(iceServers))}
	for _, server := range iceServers {
		config.ICEServers = append(config.ICEServers, webrtc.ICEServer{URLs: append([]string(nil), server.URLs...)})
	}
	pc, err := api.NewPeerConnection(config)
	if err != nil {
		return nil, fmt.Errorf("create peer connection: %w", err)
	}
	s := &Session{pc: pc, role: role, signal: signal, ready: make(chan struct{}), terminated: make(chan struct{})}
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil || s.signal == nil {
			return
		}
		json := candidate.ToJSON()
		mid := ""
		if json.SDPMid != nil {
			mid = *json.SDPMid
		}
		mline := uint16(0)
		if json.SDPMLineIndex != nil {
			mline = *json.SDPMLineIndex
		}
		s.signal(protocol.P2PSignal{Kind: protocol.P2PSignalCandidate, Candidate: json.Candidate, SDPMid: mid, MLine: mline})
	})
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			s.fail(fmt.Errorf("peer connection %s", state.String()))
		}
	})
	negotiated, ordered, dcID := true, true, dataChannelID
	dc, err := pc.CreateDataChannel("netsgo-stream", &webrtc.DataChannelInit{Negotiated: &negotiated, Ordered: &ordered, ID: &dcID})
	if err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("create data channel: %w", err)
	}
	dc.OnOpen(func() { s.attach(dc) })
	return s, nil
}

func (s *Session) fail(err error) {
	s.mu.Lock()
	if s.openErr == nil {
		s.openErr = err
	}
	s.mu.Unlock()
	s.once.Do(func() { close(s.ready) })
	s.terminateOnce.Do(func() { close(s.terminated) })
}

func (s *Session) attach(dc *webrtc.DataChannel) {
	conn, err := dc.Detach()
	if err == nil {
		stream := newDataChannelByteStream(conn)
		s.mu.Lock()
		s.transport = stream
		s.mu.Unlock()
		if s.role == protocol.P2PRoleOfferer {
			s.mu.Lock()
			s.mux, err = netsgomux.NewClientSession(stream, nil)
			s.mu.Unlock()
		} else {
			s.mu.Lock()
			s.mux, err = netsgomux.NewServerSession(stream, nil)
			s.mu.Unlock()
		}
	}
	if err != nil {
		s.mu.Lock()
		s.openErr = err
		s.mu.Unlock()
		s.terminateOnce.Do(func() { close(s.terminated) })
	}
	s.once.Do(func() { close(s.ready) })
}

func (s *Session) CreateOffer() (string, error) {
	offer, err := s.pc.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("create offer: %w", err)
	}
	if err := s.pc.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("set local offer: %w", err)
	}
	return offer.SDP, nil
}

func (s *Session) AcceptOffer(sdp string) (string, error) {
	if err := s.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}); err != nil {
		return "", fmt.Errorf("set remote offer: %w", err)
	}
	if err := s.flushPendingCandidates(); err != nil {
		return "", err
	}
	answer, err := s.pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("create answer: %w", err)
	}
	if err := s.pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("set local answer: %w", err)
	}
	return answer.SDP, nil
}

func (s *Session) AcceptAnswer(sdp string) error {
	if err := s.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}); err != nil {
		return fmt.Errorf("set remote answer: %w", err)
	}
	return s.flushPendingCandidates()
}

func (s *Session) AddCandidate(candidate protocol.P2PSignal) error {
	if candidate.Kind != protocol.P2PSignalCandidate {
		return fmt.Errorf("signal is not a candidate")
	}
	mid := candidate.SDPMid
	mline := candidate.MLine
	init := webrtc.ICECandidateInit{Candidate: candidate.Candidate, SDPMid: &mid, SDPMLineIndex: &mline}
	if s.pc.RemoteDescription() == nil {
		s.mu.Lock()
		if len(s.pending) >= protocol.P2PMaxCandidates {
			s.mu.Unlock()
			return fmt.Errorf("too many pending ICE candidates")
		}
		s.pending = append(s.pending, init)
		s.mu.Unlock()
		return nil
	}
	if err := s.pc.AddICECandidate(init); err != nil {
		return fmt.Errorf("add ICE candidate: %w", err)
	}
	return nil
}

func (s *Session) flushPendingCandidates() error {
	s.mu.Lock()
	pending := s.pending
	s.pending = nil
	s.mu.Unlock()
	for _, candidate := range pending {
		if err := s.pc.AddICECandidate(candidate); err != nil {
			return fmt.Errorf("add pending ICE candidate: %w", err)
		}
	}
	return nil
}

func (s *Session) Ready() <-chan struct{} { return s.ready }

// Terminated closes when the peer connection can no longer carry new or
// existing streams. It is distinct from Ready because a connected session can
// fail later.
func (s *Session) Terminated() <-chan struct{} { return s.terminated }

func (s *Session) Err() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.openErr
}

func (s *Session) Available() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mux != nil && !s.mux.IsClosed() && s.openErr == nil
}

func (s *Session) Open() (net.Conn, error) {
	s.mu.RLock()
	muxSession, openErr := s.mux, s.openErr
	s.mu.RUnlock()
	if openErr != nil {
		return nil, openErr
	}
	if muxSession == nil || muxSession.IsClosed() {
		return nil, fmt.Errorf("p2p session is not ready")
	}
	return muxSession.Open()
}

func (s *Session) Accept() (net.Conn, error) {
	s.mu.RLock()
	muxSession, openErr := s.mux, s.openErr
	s.mu.RUnlock()
	if openErr != nil {
		return nil, openErr
	}
	if muxSession == nil || muxSession.IsClosed() {
		return nil, fmt.Errorf("p2p session is not ready")
	}
	return muxSession.Accept()
}

func (s *Session) Close() error {
	s.mu.Lock()
	muxSession := s.mux
	transport := s.transport
	s.mux = nil
	s.transport = nil
	s.mu.Unlock()
	pcErr := s.pc.Close()
	if transport != nil {
		_ = transport.Close()
	}
	if muxSession != nil {
		_ = muxSession.Close()
	}
	s.once.Do(func() { close(s.ready) })
	s.terminateOnce.Do(func() { close(s.terminated) })
	return pcErr
}
