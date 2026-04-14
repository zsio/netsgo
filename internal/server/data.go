package server

import (
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

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
	release := s.trackManagedConn(conn)
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

	if err := s.writeDataHandshakeResult(conn, protocol.DataHandshakeOK); err != nil {
		log.Printf("❌ data channel: write handshake result failed [%s]: %v", clientID, err)
		return
	}

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

	if !s.isCurrentGeneration(clientID, generation) {
		_ = session.Close()
		return
	}

	client.dataMu.Lock()
	oldSession := client.dataSession
	client.dataSession = session
	client.dataMu.Unlock()

	if oldSession != nil && oldSession != session && !oldSession.IsClosed() {
		_ = oldSession.Close()
	}

	promoted := s.promotePendingToLiveIfCurrent(client)
	if promoted {
		info := client.GetInfo()
		log.Printf("🔗 data channel established: Client [%s] generation=%d", clientID, generation)
		s.events.PublishJSON("client_online", map[string]any{
			"client_id": client.ID,
			"info":      info,
		})
		go s.restoreTunnels(client)
	}

	<-session.CloseChan()

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

// openStreamToClient opens a new stream on the client's yamux session and
// writes a StreamHeader to tell the client which proxy this stream belongs to.
func (s *Server) openStreamToClient(client *ClientConn, proxyName string) (net.Conn, error) {
	if client.generation != 0 && !s.isCurrentLive(client.ID, client.generation) {
		return nil, fmt.Errorf("client [%s] is not online", client.ID)
	}

	client.dataMu.RLock()
	session := client.dataSession
	client.dataMu.RUnlock()

	if session == nil || session.IsClosed() {
		return nil, fmt.Errorf("client [%s] data channel not established", client.ID)
	}

	stream, err := session.Open()
	if err != nil {
		return nil, fmt.Errorf("OpenStream failed: %w", err)
	}

	// Write StreamHeader: [2B name length] [NB proxy_name]
	nameBytes := []byte(proxyName)
	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(nameBytes)))
	if _, err := stream.Write(lenBuf[:]); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("write StreamHeader length failed: %w", err)
	}
	if _, err := stream.Write(nameBytes); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("write StreamHeader name failed: %w", err)
	}

	return stream, nil
}
