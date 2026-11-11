package server

import (
	"crypto/subtle"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"

	"netsgo/internal/connlimit"
	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

const dataStreamDrainTimeout = 2 * time.Second

func (s *Server) handleDataWS(w http.ResponseWriter, r *http.Request) {
	if !websocket.IsWebSocketUpgrade(r) {
		encodeJSON(w, http.StatusUpgradeRequired, map[string]any{
			"error": "websocket upgrade required",
		})
		return
	}

	conn, err := dataUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("❌ data channel WebSocket upgrade failed: %v", err)
		return
	}
	release, accepted := s.trackManagedConn(conn)
	if !accepted {
		return
	}
	defer release()
	defer func() { _ = conn.Close() }()

	conn.SetReadLimit(wsDataMaxMessageSize)
	if err := conn.SetReadDeadline(time.Now().Add(s.sessions.dataHandshakeTimeout)); err != nil {
		log.Printf("❌ data channel: set handshake read deadline failed: %v", err)
		return
	}

	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		log.Printf("❌ data channel handshake read failed: %v", err)
		return
	}
	if messageType != websocket.BinaryMessage {
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseUnsupportedData, "binary handshake required"),
			time.Now().Add(time.Second),
		)
		return
	}

	clientID, dataToken, err := protocol.DecodeDataHandshake(payload)
	if err != nil {
		log.Printf("❌ data channel handshake parse failed: %v", err)
		if writeErr := s.writeDataHandshakeResult(conn, protocol.DataHandshakeFail); writeErr != nil {
			log.Printf("❌ data channel handshake fail response write failed: %v", writeErr)
		}
		return
	}

	value, ok := s.clients.Load(clientID)
	if !ok {
		log.Printf("❌ data channel: Client [%s] not registered", clientID)
		if writeErr := s.writeDataHandshakeResult(conn, protocol.DataHandshakeFail); writeErr != nil {
			log.Printf("❌ data channel missing client response write failed: %v", writeErr)
		}
		return
	}
	client := value.(*ClientConn)
	generation := client.generation

	if client.dataToken == "" || subtle.ConstantTimeCompare([]byte(client.dataToken), []byte(dataToken)) != 1 {
		log.Printf("❌ data channel: DataToken verification failed [%s]", clientID)
		if writeErr := s.writeDataHandshakeResult(conn, protocol.DataHandshakeAuthFail); writeErr != nil {
			log.Printf("❌ data channel auth fail response write failed: %v", writeErr)
		}
		return
	}
	if client.getState() == clientStateClosing {
		log.Printf("❌ data channel: session already closing [%s]", clientID)
		if writeErr := s.writeDataHandshakeResult(conn, protocol.DataHandshakeAuthFail); writeErr != nil {
			log.Printf("❌ data channel closing response write failed: %v", writeErr)
		}
		return
	}

	if !s.isCurrentGeneration(clientID, generation) {
		if writeErr := s.writeDataHandshakeResult(conn, protocol.DataHandshakeAuthFail); writeErr != nil {
			log.Printf("❌ data channel stale generation response write failed: %v", writeErr)
		}
		return
	}
	// The data token proves possession of a pending logical session, not that
	// its current owner is still permitted to operate. Check that policy at the
	// last possible point before the handshake grants a yamux data plane.
	_, releaseOwnerGate, err := s.acquireUserLifecycleRead(client.OwnerUserID, client.OwnerEpoch, true)
	if err != nil {
		log.Printf("❌ data channel: owner is not operational [%s]: %v", clientID, err)
		_ = s.invalidateLogicalSessionIfCurrent(clientID, generation, "owner_not_operational")
		if writeErr := s.writeDataHandshakeResult(conn, protocol.DataHandshakeAuthFail); writeErr != nil {
			log.Printf("❌ data channel owner rejection response write failed: %v", writeErr)
		}
		return
	}
	s.clientTunnelMutationMu.Lock()
	current, currentOK := s.clients.Load(clientID)
	publicationCurrent := currentOK && current == client && client.generation == generation &&
		client.getState() != clientStateClosing && s.clientLifecycleCurrentLocked(client) &&
		client.dataToken != "" && subtle.ConstantTimeCompare([]byte(client.dataToken), []byte(dataToken)) == 1
	if !publicationCurrent {
		s.clientTunnelMutationMu.Unlock()
		releaseOwnerGate()
		if writeErr := s.writeDataHandshakeResult(conn, protocol.DataHandshakeAuthFail); writeErr != nil {
			log.Printf("❌ data channel stale session response write failed: %v", writeErr)
		}
		return
	}

	if err := s.writeDataHandshakeResult(conn, protocol.DataHandshakeOK); err != nil {
		s.clientTunnelMutationMu.Unlock()
		releaseOwnerGate()
		log.Printf("❌ data channel: write handshake result failed [%s]: %v", clientID, err)
		return
	}
	s.clientTunnelMutationMu.Unlock()
	releaseOwnerGate()

	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		log.Printf("❌ data channel: clear read deadline failed [%s]: %v", clientID, err)
		return
	}
	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		log.Printf("❌ data channel: clear write deadline failed [%s]: %v", clientID, err)
		return
	}

	wsConn := mux.NewWSConn(conn)
	session, err := mux.NewServerSession(wsConn, mux.DefaultConfig())
	if err != nil {
		log.Printf("❌ data channel: create yamux session failed [%s]: %v", clientID, err)
		s.invalidateLogicalSessionIfCurrent(clientID, generation, "data_session_start_failed")
		return
	}

	oldSession, promoted, attached := s.attachDataSessionIfCurrent(client, session)
	if !attached {
		_ = session.Close()
		return
	}

	if oldSession != nil && oldSession != session && !oldSession.IsClosed() {
		_ = oldSession.Close()
	}

	if promoted {
		log.Printf("🔗 data channel established: Client [%s] generation=%d", clientID, generation)
		go s.restoreTunnels(client)
	} else {
		go s.reconcileTunnelsForClientGeneration(client, "data_channel_ready")
	}

	var streamWG sync.WaitGroup
	streamWG.Add(1)
	go s.acceptClientOpenedDataStreams(client, session, &streamWG)

	<-session.CloseChan()
	_ = session.Close()
	if !waitForDataStreamHandlers(&streamWG, dataStreamDrainTimeout) {
		log.Printf("⚠️ data channel: timed out waiting for client-opened streams to close [%s] generation=%d", clientID, generation)
	}

	client.dataMu.Lock()
	isCurrentSession := client.dataSession == session
	if isCurrentSession {
		client.dataSession = nil
	}
	client.dataMu.Unlock()

	if !s.isCurrentGeneration(clientID, generation) || !isCurrentSession {
		return
	}

	log.Printf("🔌 data channel disconnected: Client [%s] generation=%d", clientID, generation)
	s.invalidateLogicalSessionIfCurrent(clientID, generation, "data_session_closed")
}

func (s *Server) writeDataHandshakeResult(conn *websocket.Conn, status byte) error {
	if err := conn.SetWriteDeadline(time.Now().Add(s.sessions.dataHandshakeAckTimeout)); err != nil {
		return fmt.Errorf("set handshake ack write deadline failed: %w", err)
	}
	defer func() {
		_ = conn.SetWriteDeadline(time.Time{})
	}()
	return conn.WriteMessage(websocket.BinaryMessage, []byte{status})
}

func (s *Server) acceptClientOpenedDataStreams(client *ClientConn, session *yamux.Session, streamWG *sync.WaitGroup) {
	defer streamWG.Done()
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			return
		}
		admitted, ok := s.admitForwardingConn(stream, &s.dataConnections)
		if !ok {
			continue
		}
		streamWG.Add(1)
		go func() {
			defer streamWG.Done()
			defer func() { _ = admitted.Close() }()
			header, err := protocol.DecodeDataStreamHeader(admitted)
			if err != nil {
				log.Printf("⚠️ data channel: decode client-opened stream header failed [%s]: %v", client.ID, err)
				_ = stream.Close()
				return
			}
			s.handleClientOpenedDataStreamForSession(client, session, admitted, header)
		}()
	}
}

func waitForDataStreamHandlers(streamWG *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		streamWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// openStreamToClient opens a new server-relay stream on the client's yamux session and
// writes a DataStreamHeader that identifies the tunnel/revision before payload bytes.
func (s *Server) openStreamToClient(client *ClientConn, proxyName string) (net.Conn, error) {
	return s.openStreamToClientWithHeaderMatching(client, proxyName, nil, nil, nil)
}

func (s *Server) openStreamToClientForActivation(client *ClientConn, tunnel *ProxyTunnel, activation proxyActivationSnapshot) (net.Conn, error) {
	return s.openStreamToClientWithHeaderMatching(client, activation.config.Name, tunnel, &activation, nil)
}

func (s *Server) openStreamToClientWithHeaderForActivation(client *ClientConn, tunnel *ProxyTunnel, activation proxyActivationSnapshot, mutate func(*protocol.DataStreamHeader)) (net.Conn, error) {
	return s.openStreamToClientWithHeaderMatching(client, activation.config.Name, tunnel, &activation, mutate)
}

func (s *Server) openStreamToClientWithHeaderMatching(
	client *ClientConn,
	proxyName string,
	expectedTunnel *ProxyTunnel,
	activation *proxyActivationSnapshot,
	mutate func(*protocol.DataStreamHeader),
) (net.Conn, error) {
	release, accepted := s.dataConnections.Acquire(s.MaxConnections)
	if !accepted {
		s.reportConnectionLimit(&s.dataConnections)
		return nil, connlimit.ErrReached
	}
	transferred := false
	defer func() {
		if !transferred {
			release()
		}
	}()
	if client.generation != 0 && !s.isCurrentLive(client.ID, client.generation) {
		return nil, fmt.Errorf("client [%s] is not online", client.ID)
	}

	client.dataMu.RLock()
	session := client.dataSession
	client.dataMu.RUnlock()

	if session == nil || session.IsClosed() {
		return nil, fmt.Errorf("client [%s] data channel not established", client.ID)
	}
	if activation != nil {
		client.proxyMu.RLock()
		current := client.proxies[proxyName]
		activationCurrent := proxyActivationCurrentLocked(current, expectedTunnel, *activation)
		client.proxyMu.RUnlock()
		if !activationCurrent {
			return nil, fmt.Errorf("proxy tunnel %q activation is no longer current", proxyName)
		}
	}

	stream, err := session.Open()
	if err != nil {
		return nil, fmt.Errorf("OpenStream failed: %w", err)
	}

	client.proxyMu.Lock()
	tunnel, ok := client.proxies[proxyName]
	var header protocol.DataStreamHeader
	var transportPolicy string
	if ok && activation != nil {
		ok = proxyActivationCurrentLocked(tunnel, expectedTunnel, *activation)
		if ok {
			header, err = dataStreamHeaderForServerRelayConfig(client, activation.config, activation.runtimeRevision, proxyName)
			transportPolicy = activation.config.TransportPolicy
		}
	} else if ok {
		runtimeRevision := ensureTunnelRuntimeRevision(tunnel)
		header, err = dataStreamHeaderForServerRelayConfig(client, tunnel.Config, runtimeRevision, proxyName)
		transportPolicy = tunnel.Config.TransportPolicy
	}
	client.proxyMu.Unlock()
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	if !ok {
		_ = stream.Close()
		if activation != nil {
			return nil, fmt.Errorf("proxy tunnel %q activation is no longer current", proxyName)
		}
		return nil, fmt.Errorf("proxy tunnel %q not found", proxyName)
	}
	if transportPolicy == protocol.TransportPolicyDirectOnly {
		_ = stream.Close()
		return nil, fmt.Errorf("direct_only tunnels must not use server relay")
	}
	if mutate != nil {
		mutate(&header)
	}

	if err := protocol.EncodeDataStreamHeader(stream, header); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("write DataStreamHeader failed: %w", err)
	}

	transferred = true
	return connlimit.Wrap(stream, release), nil
}

func proxyActivationCurrentLocked(current, expected *ProxyTunnel, activation proxyActivationSnapshot) bool {
	if current == nil || expected == nil || current != expected || !isTunnelExposed(current.Config) {
		return false
	}
	if current.Config.ID != activation.config.ID ||
		current.Config.Name != activation.config.Name ||
		current.Config.Revision != activation.config.Revision ||
		current.runtime.Revision != activation.runtimeRevision {
		return false
	}
	if activation.listener != nil && current.Listener != activation.listener {
		return false
	}
	if activation.udpState != nil && current.UDPState != activation.udpState {
		return false
	}
	if activation.done == nil || current.done != activation.done || !proxyActivationDoneOpen(activation.done) {
		return false
	}
	return true
}

func dataStreamHeaderForServerRelayConfig(client *ClientConn, config protocol.ProxyConfig, runtimeRevision uint64, fallbackName string) (protocol.DataStreamHeader, error) {
	tunnelID := config.ID
	if tunnelID == "" {
		tunnelID = fallbackName
	}
	streamID, err := protocol.NewDataStreamID()
	if err != nil {
		return protocol.DataStreamHeader{}, err
	}
	return protocol.DataStreamHeader{
		Kind:             protocol.DataStreamHeaderKindTunnelStream,
		TunnelID:         tunnelID,
		Revision:         int64(runtimeRevision),
		StreamID:         streamID,
		OpenClientID:     client.ID,
		SourceRole:       protocol.DataStreamRoleServer,
		TargetRole:       protocol.DataStreamRoleTarget,
		Direction:        protocol.DataStreamDirectionIngressToTarget,
		Transport:        protocol.ActualTransportServerRelay,
		ServerAuthorized: true,
	}, nil
}
