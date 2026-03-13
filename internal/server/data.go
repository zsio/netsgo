package server

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

// handleDataConn 处理 Agent 的数据通道连接。
// 此时魔数字节 (0x4E) 已被 peek 消费，连接的下一个字节是 AgentID 长度。
func (s *Server) handleDataConn(conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("⚠️ 数据通道 panic: %v", r)
			conn.Close()
		}
	}()

	// 1. 读取 AgentID 长度 (2 bytes, big-endian uint16)
	var lenBuf [2]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		log.Printf("❌ 数据通道: 读取 AgentID 长度失败: %v", err)
		conn.Write([]byte{protocol.DataHandshakeFail})
		conn.Close()
		return
	}
	idLen := binary.BigEndian.Uint16(lenBuf[:])
	if idLen == 0 || idLen > 1024 {
		log.Printf("❌ 数据通道: AgentID 长度异常: %d", idLen)
		conn.Write([]byte{protocol.DataHandshakeFail})
		conn.Close()
		return
	}

	// 2. 读取 AgentID
	idBuf := make([]byte, idLen)
	if _, err := io.ReadFull(conn, idBuf); err != nil {
		log.Printf("❌ 数据通道: 读取 AgentID 失败: %v", err)
		conn.Write([]byte{protocol.DataHandshakeFail})
		conn.Close()
		return
	}
	agentID := string(idBuf)

	// 3. P3: 读取 DataToken 长度 (2 bytes, big-endian uint16)
	var tokenLenBuf [2]byte
	if _, err := io.ReadFull(conn, tokenLenBuf[:]); err != nil {
		log.Printf("❌ 数据通道: 读取 DataToken 长度失败 [%s]: %v", agentID, err)
		conn.Write([]byte{protocol.DataHandshakeFail})
		conn.Close()
		return
	}
	tokenLen := binary.BigEndian.Uint16(tokenLenBuf[:])
	if tokenLen == 0 || tokenLen > 256 {
		log.Printf("❌ 数据通道: DataToken 长度异常 [%s]: %d", agentID, tokenLen)
		conn.Write([]byte{protocol.DataHandshakeFail})
		conn.Close()
		return
	}

	// 4. P3: 读取 DataToken
	tokenBuf := make([]byte, tokenLen)
	if _, err := io.ReadFull(conn, tokenBuf); err != nil {
		log.Printf("❌ 数据通道: 读取 DataToken 失败 [%s]: %v", agentID, err)
		conn.Write([]byte{protocol.DataHandshakeFail})
		conn.Close()
		return
	}
	dataToken := string(tokenBuf)

	// 5. 查找对应的 AgentConn
	val, ok := s.agents.Load(agentID)
	if !ok {
		log.Printf("❌ 数据通道: Agent [%s] 未注册", agentID)
		conn.Write([]byte{protocol.DataHandshakeFail})
		conn.Close()
		return
	}
	agent := val.(*AgentConn)

	// 6. P3: 校验 DataToken
	if agent.dataToken == "" || agent.dataToken != dataToken {
		log.Printf("❌ 数据通道: DataToken 校验失败 [%s]", agentID)
		conn.Write([]byte{protocol.DataHandshakeAuthFail})
		conn.Close()
		return
	}

	// 7. 如果 Agent 已经有数据通道，关闭旧的
	agent.dataMu.Lock()
	if agent.dataSession != nil && !agent.dataSession.IsClosed() {
		agent.dataSession.Close()
	}

	// 8. 回复握手成功
	if _, err := conn.Write([]byte{protocol.DataHandshakeOK}); err != nil {
		log.Printf("❌ 数据通道: 回复握手失败 [%s]: %v", agentID, err)
		agent.dataMu.Unlock()
		conn.Close()
		return
	}

	// 9. 在该 TCP 连接上建立 yamux Server Session
	session, err := mux.NewServerSession(conn, mux.DefaultConfig())
	if err != nil {
		log.Printf("❌ 数据通道: 创建 yamux Session 失败 [%s]: %v", agentID, err)
		agent.dataMu.Unlock()
		conn.Close()
		return
	}
	agent.dataSession = session
	agent.dataMu.Unlock()

	log.Printf("🔗 数据通道已建立: Agent [%s]", agentID)

	// 10. 阻塞等待 session 关闭（保持连接存活）
	<-session.CloseChan()
	log.Printf("🔌 数据通道已断开: Agent [%s]", agentID)
}

// DataHandshakeBytes 构造 Agent 侧数据通道握手包
// 格式: [1B 魔数] [2B AgentID长度 big-endian] [NB AgentID] [2B DataToken长度] [NB DataToken]
func DataHandshakeBytes(agentID, dataToken string) []byte {
	idBytes := []byte(agentID)
	tokenBytes := []byte(dataToken)
	buf := make([]byte, 1+2+len(idBytes)+2+len(tokenBytes))
	buf[0] = protocol.DataChannelMagic
	binary.BigEndian.PutUint16(buf[1:3], uint16(len(idBytes)))
	copy(buf[3:], idBytes)
	offset := 3 + len(idBytes)
	binary.BigEndian.PutUint16(buf[offset:offset+2], uint16(len(tokenBytes)))
	copy(buf[offset+2:], tokenBytes)
	return buf
}

// openStreamToAgent 在 Agent 的 yamux Session 上打开一个新 Stream，
// 并写入 StreamHeader 告知 Agent 这个 stream 属于哪条代理。
func (s *Server) openStreamToAgent(agent *AgentConn, proxyName string) (net.Conn, error) {
	agent.dataMu.RLock()
	session := agent.dataSession
	agent.dataMu.RUnlock()

	if session == nil || session.IsClosed() {
		return nil, fmt.Errorf("Agent [%s] 数据通道未建立", agent.ID)
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
