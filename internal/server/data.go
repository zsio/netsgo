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
		log.Printf("❌ 数据通道 WebSocket 升级失败: %v", err)
		return
	}
	defer conn.Close()

	conn.SetReadLimit(wsDataMaxMessageSize)
	conn.SetReadDeadline(time.Now().Add(s.dataHandshakeTimeout))

	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		log.Printf("❌ 数据通道读取握手失败: %v", err)
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
		log.Printf("❌ 数据通道握手解析失败: %v", err)
		s.writeDataHandshakeResult(conn, protocol.DataHandshakeFail)
		return
	}

	value, ok := s.clients.Load(clientID)
	if !ok {
		log.Printf("❌ 数据通道: Client [%s] 未注册", clientID)
		s.writeDataHandshakeResult(conn, protocol.DataHandshakeFail)
		return
	}
	client := value.(*ClientConn)
	generation := client.generation

	if client.dataToken == "" || subtle.ConstantTimeCompare([]byte(client.dataToken), []byte(dataToken)) != 1 {
		log.Printf("❌ 数据通道: DataToken 校验失败 [%s]", clientID)
		s.writeDataHandshakeResult(conn, protocol.DataHandshakeAuthFail)
		return
	}
	if client.getState() == clientStateClosing {
		log.Printf("❌ 数据通道: 会话已进入 closing [%s]", clientID)
		s.writeDataHandshakeResult(conn, protocol.DataHandshakeAuthFail)
		return
	}

	if !s.isCurrentGeneration(clientID, generation) {
		s.writeDataHandshakeResult(conn, protocol.DataHandshakeAuthFail)
		return
	}

	if err := s.writeDataHandshakeResult(conn, protocol.DataHandshakeOK); err != nil {
		log.Printf("❌ 数据通道: 返回握手结果失败 [%s]: %v", clientID, err)
		return
	}

	conn.SetReadDeadline(time.Time{})
	conn.SetWriteDeadline(time.Time{})

	wsConn := mux.NewWSConn(conn)
	session, err := mux.NewServerSession(wsConn, mux.DefaultConfig())
	if err != nil {
		log.Printf("❌ 数据通道: 创建 yamux Session 失败 [%s]: %v", clientID, err)
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
		log.Printf("🔗 数据通道已建立: Client [%s] generation=%d", clientID, generation)
		s.events.PublishJSON("client_online", map[string]any{
			"client_id": client.ID,
			"info":      client.Info,
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

	log.Printf("🔌 数据通道已断开: Client [%s] generation=%d", clientID, generation)
	s.invalidateLogicalSessionIfCurrent(clientID, generation, "data_session_closed")
}

func (s *Server) writeDataHandshakeResult(conn *websocket.Conn, status byte) error {
	conn.SetWriteDeadline(time.Now().Add(s.dataHandshakeAckTimeout))
	defer conn.SetWriteDeadline(time.Time{})
	return conn.WriteMessage(websocket.BinaryMessage, []byte{status})
}

// openStreamToClient 在 Client 的 yamux Session 上打开一个新 Stream，
// 并写入 StreamHeader 告知 Client 这个 stream 属于哪条代理。
func (s *Server) openStreamToClient(client *ClientConn, proxyName string) (net.Conn, error) {
	if client.generation != 0 && !s.isCurrentLive(client.ID, client.generation) {
		return nil, fmt.Errorf("Client [%s] 当前不在线", client.ID)
	}

	client.dataMu.RLock()
	session := client.dataSession
	client.dataMu.RUnlock()

	if session == nil || session.IsClosed() {
		return nil, fmt.Errorf("Client [%s] 数据通道未建立", client.ID)
	}

	stream, err := session.Open()
	if err != nil {
		return nil, fmt.Errorf("OpenStream 失败: %w", err)
	}

	// 写入 StreamHeader: [2B name长度] [NB proxy_name]
	nameBytes := []byte(proxyName)
	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(nameBytes)))
	if _, err := stream.Write(lenBuf[:]); err != nil {
		stream.Close()
		return nil, fmt.Errorf("写入 StreamHeader 长度失败: %w", err)
	}
	if _, err := stream.Write(nameBytes); err != nil {
		stream.Close()
		return nil, fmt.Errorf("写入 StreamHeader 名称失败: %w", err)
	}

	return stream, nil
}
