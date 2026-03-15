package server

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

// handleDataConn 处理 Client 的数据通道连接。
// 此时魔数字节 (0x4E) 已被 peek 消费，连接的下一个字节是 ClientID 长度。
func (s *Server) handleDataConn(conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("⚠️ 数据通道 panic: %v", r)
			conn.Close()
		}
	}()

	// 设置握手阶段超时（类比 P16 控制通道认证超时），
	// 防止恶意客户端连接后不发送握手数据占用 goroutine
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	// 1. 读取 ClientID 长度 (2 bytes, big-endian uint16)
	var lenBuf [2]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		log.Printf("❌ 数据通道: 读取 ClientID 长度失败: %v", err)
		conn.Write([]byte{protocol.DataHandshakeFail})
		conn.Close()
		return
	}
	idLen := binary.BigEndian.Uint16(lenBuf[:])
	if idLen == 0 || idLen > 1024 {
		log.Printf("❌ 数据通道: ClientID 长度异常: %d", idLen)
		conn.Write([]byte{protocol.DataHandshakeFail})
		conn.Close()
		return
	}

	// 2. 读取 ClientID
	idBuf := make([]byte, idLen)
	if _, err := io.ReadFull(conn, idBuf); err != nil {
		log.Printf("❌ 数据通道: 读取 ClientID 失败: %v", err)
		conn.Write([]byte{protocol.DataHandshakeFail})
		conn.Close()
		return
	}
	clientID := string(idBuf)

	// 3. P3: 读取 DataToken 长度 (2 bytes, big-endian uint16)
	var tokenLenBuf [2]byte
	if _, err := io.ReadFull(conn, tokenLenBuf[:]); err != nil {
		log.Printf("❌ 数据通道: 读取 DataToken 长度失败 [%s]: %v", clientID, err)
		conn.Write([]byte{protocol.DataHandshakeFail})
		conn.Close()
		return
	}
	tokenLen := binary.BigEndian.Uint16(tokenLenBuf[:])
	if tokenLen == 0 || tokenLen > 256 {
		log.Printf("❌ 数据通道: DataToken 长度异常 [%s]: %d", clientID, tokenLen)
		conn.Write([]byte{protocol.DataHandshakeFail})
		conn.Close()
		return
	}

	// 4. P3: 读取 DataToken
	tokenBuf := make([]byte, tokenLen)
	if _, err := io.ReadFull(conn, tokenBuf); err != nil {
		log.Printf("❌ 数据通道: 读取 DataToken 失败 [%s]: %v", clientID, err)
		conn.Write([]byte{protocol.DataHandshakeFail})
		conn.Close()
		return
	}
	dataToken := string(tokenBuf)

	// 5. 查找对应的 ClientConn
	val, ok := s.clients.Load(clientID)
	if !ok {
		log.Printf("❌ 数据通道: Client [%s] 未注册", clientID)
		conn.Write([]byte{protocol.DataHandshakeFail})
		conn.Close()
		return
	}
	client := val.(*ClientConn)

	// 6. P3: 校验 DataToken
	if client.dataToken == "" || client.dataToken != dataToken {
		log.Printf("❌ 数据通道: DataToken 校验失败 [%s]", clientID)
		conn.Write([]byte{protocol.DataHandshakeAuthFail})
		conn.Close()
		return
	}

	// 握手校验完成，清除 deadline（yamux 自行管理超时）
	conn.SetDeadline(time.Time{})

	// 7. 如果 Client 已经有数据通道，关闭旧的
	client.dataMu.Lock()
	if client.dataSession != nil && !client.dataSession.IsClosed() {
		client.dataSession.Close()
	}

	// 8. 回复握手成功
	if _, err := conn.Write([]byte{protocol.DataHandshakeOK}); err != nil {
		log.Printf("❌ 数据通道: 回复握手失败 [%s]: %v", clientID, err)
		client.dataMu.Unlock()
		conn.Close()
		return
	}

	// 9. 在该 TCP 连接上建立 yamux Server Session
	session, err := mux.NewServerSession(conn, mux.DefaultConfig())
	if err != nil {
		log.Printf("❌ 数据通道: 创建 yamux Session 失败 [%s]: %v", clientID, err)
		client.dataMu.Unlock()
		conn.Close()
		return
	}
	client.dataSession = session
	client.dataMu.Unlock()

	log.Printf("🔗 数据通道已建立: Client [%s]", clientID)

	// 10. 阻塞等待 session 关闭（保持连接存活）
	<-session.CloseChan()
	log.Printf("🔌 数据通道已断开: Client [%s]", clientID)
}

// DataHandshakeBytes 构造 Client 侧数据通道握手包
// 格式: [1B 魔数] [2B ClientID长度 big-endian] [NB ClientID] [2B DataToken长度] [NB DataToken]
func DataHandshakeBytes(clientID, dataToken string) []byte {
	idBytes := []byte(clientID)
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

// openStreamToClient 在 Client 的 yamux Session 上打开一个新 Stream，
// 并写入 StreamHeader 告知 Client 这个 stream 属于哪条代理。
func (s *Server) openStreamToClient(client *ClientConn, proxyName string) (net.Conn, error) {
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
