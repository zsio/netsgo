package server

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

// ============================================================
// 数据通道处理测试 (handleDataConn & DataHandshakeBytes)
// ============================================================

const testDataToken = "test-data-token-abc123"

func TestDataChannel_HandshakeSuccess(t *testing.T) {
	s := New(0)
	// 注册一个预设的 Client
	clientID := "test-client-123"
	cc := &ClientConn{
		ID:        clientID,
		proxies:   make(map[string]*ProxyTunnel),
		dataToken: testDataToken, // P3: 设置 DataToken
	}
	s.clients.Store(clientID, cc)

	// 用 net.Pipe 模拟网络连接
	client, serverConn := net.Pipe()
	defer client.Close()
	defer serverConn.Close()

	// 服务器端开启处理数据通道
	go func() {
		// Mock peekByte 已消费魔数后的处理
		s.handleDataConn(serverConn)
	}()

	// 客户端发送合法的握手包体
	// DataHandshakeBytes 返回 [1B 魔数] [2B ClientID长度] [NB ClientID] [2B DataToken长度] [NB DataToken]
	// server 侧 handleDataConn 预期消费的只有魔数之后的部分
	handshakePkg := DataHandshakeBytes(clientID, testDataToken)
	// 去掉第一个魔数字节
	payload := handshakePkg[1:]

	if _, err := client.Write(payload); err != nil {
		t.Fatalf("客户端发送握手失败: %v", err)
	}

	// 读取服务端的响应
	respBuf := make([]byte, 1)
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Read(respBuf); err != nil {
		t.Fatalf("读取服务端响应失败: %v", err)
	}

	if respBuf[0] != protocol.DataHandshakeOK {
		t.Errorf("期望握手成功 OK(%d)，得到 %d", protocol.DataHandshakeOK, respBuf[0])
	}

	// 验证远端 Server 已经正确为 cc 注册了 dataSession
	time.Sleep(50 * time.Millisecond)
	cc.dataMu.RLock()
	hasSession := cc.dataSession != nil && !cc.dataSession.IsClosed()
	cc.dataMu.RUnlock()
	if !hasSession {
		t.Error("Server 成功握手后未给 Client 赋值 dataSession")
	}
}

func TestDataChannel_Handshake_InvalidLength(t *testing.T) {
	s := New(0)

	client, serverConn := net.Pipe()
	defer client.Close()
	defer serverConn.Close()

	go s.handleDataConn(serverConn)

	// 发送错误长度 0
	badLen := []byte{0x00, 0x00}
	client.Write(badLen)

	respBuf := make([]byte, 1)
	client.SetReadDeadline(time.Now().Add(1 * time.Second))
	client.Read(respBuf)

	if respBuf[0] != protocol.DataHandshakeFail {
		t.Errorf("期望失败 Fail(%d)，得到 %d", protocol.DataHandshakeFail, respBuf[0])
	}
}

func TestDataChannel_Handshake_UnregisteredClient(t *testing.T) {
	s := New(0)

	client, serverConn := net.Pipe()
	defer client.Close()
	defer serverConn.Close()

	go s.handleDataConn(serverConn)

	unregisteredID := "ghost-client"
	handshakePkg := DataHandshakeBytes(unregisteredID, "some-token")[1:]
	client.Write(handshakePkg)

	respBuf := make([]byte, 1)
	client.SetReadDeadline(time.Now().Add(1 * time.Second))
	client.Read(respBuf)

	if respBuf[0] != protocol.DataHandshakeFail {
		t.Errorf("期望未注册 Client 握手失败(%d)，得到 %d", protocol.DataHandshakeFail, respBuf[0])
	}
}

func TestDataChannel_Handshake_ReconnectClosesOldSession(t *testing.T) {
	s := New(0)
	clientID := "reconnect-client"
	cc := &ClientConn{
		ID:        clientID,
		proxies:   make(map[string]*ProxyTunnel),
		dataToken: testDataToken,
	}
	s.clients.Store(clientID, cc)

	// --- 第一次握手 ---
	client1, serverConn1 := net.Pipe()
	go s.handleDataConn(serverConn1)

	client1.Write(DataHandshakeBytes(clientID, testDataToken)[1:])
	resp1 := make([]byte, 1)
	client1.Read(resp1)

	time.Sleep(50 * time.Millisecond) // 等待 session 初始化
	cc.dataMu.RLock()
	session1 := cc.dataSession
	cc.dataMu.RUnlock()

	if session1 == nil {
		t.Fatal("第一次握手失败，session为空")
	}

	// --- 第二次握手 ---
	client2, serverConn2 := net.Pipe()
	go s.handleDataConn(serverConn2)

	client2.Write(DataHandshakeBytes(clientID, testDataToken)[1:])
	resp2 := make([]byte, 1)
	client2.Read(resp2)

	time.Sleep(50 * time.Millisecond)
	cc.dataMu.RLock()
	session2 := cc.dataSession
	cc.dataMu.RUnlock()

	if session1 == session2 {
		t.Error("第二次握手应该生成了新的 session 对象")
	}
	if !session1.IsClosed() {
		t.Error("第二次接入时，应当关闭旧的 dataSession1")
	}

	client1.Close()
	client2.Close()
	serverConn1.Close()
	serverConn2.Close()
}

func TestDataHandshakeBytes(t *testing.T) {
	clientID := "my-client-id-1234"
	dataToken := "test-token-xyz"
	res := DataHandshakeBytes(clientID, dataToken)

	if res[0] != protocol.DataChannelMagic {
		t.Fatalf("首字节异常: 期望 %d, 得到 %d", protocol.DataChannelMagic, res[0])
	}

	idLen := binary.BigEndian.Uint16(res[1:3])
	if int(idLen) != len(clientID) {
		t.Fatalf("ClientID 长度编码异常: 期望 %d, 得到 %d", len(clientID), idLen)
	}

	parsedID := string(res[3 : 3+idLen])
	if parsedID != clientID {
		t.Fatalf("ClientID 异常: 期望 %q, 得到 %q", clientID, parsedID)
	}

	// 验证 DataToken 段
	offset := 3 + int(idLen)
	tokenLen := binary.BigEndian.Uint16(res[offset : offset+2])
	if int(tokenLen) != len(dataToken) {
		t.Fatalf("DataToken 长度编码异常: 期望 %d, 得到 %d", len(dataToken), tokenLen)
	}

	parsedToken := string(res[offset+2 : offset+2+int(tokenLen)])
	if parsedToken != dataToken {
		t.Fatalf("DataToken 异常: 期望 %q, 得到 %q", dataToken, parsedToken)
	}
}

// ==================== P3: DataToken 校验测试 ====================

func TestDataChannel_Handshake_WrongToken(t *testing.T) {
	s := New(0)
	clientID := "token-test-client"
	cc := &ClientConn{
		ID:        clientID,
		proxies:   make(map[string]*ProxyTunnel),
		dataToken: "correct-token",
	}
	s.clients.Store(clientID, cc)

	client, serverConn := net.Pipe()
	defer client.Close()
	defer serverConn.Close()

	go s.handleDataConn(serverConn)

	// 发送错误的 DataToken
	handshakePkg := DataHandshakeBytes(clientID, "wrong-token")[1:]
	client.Write(handshakePkg)

	respBuf := make([]byte, 1)
	client.SetReadDeadline(time.Now().Add(1 * time.Second))
	client.Read(respBuf)

	if respBuf[0] != protocol.DataHandshakeAuthFail {
		t.Errorf("错误 DataToken 应返回 AuthFail(0x%02x)，得到 0x%02x",
			protocol.DataHandshakeAuthFail, respBuf[0])
	}
}

func TestDataChannel_Handshake_EmptyToken(t *testing.T) {
	s := New(0)
	clientID := "empty-token-client"
	cc := &ClientConn{
		ID:        clientID,
		proxies:   make(map[string]*ProxyTunnel),
		dataToken: "some-valid-token",
	}
	s.clients.Store(clientID, cc)

	client, serverConn := net.Pipe()
	defer client.Close()
	defer serverConn.Close()

	go s.handleDataConn(serverConn)

	// 发送空 DataToken（tokenLen=0 会被 tokenLen == 0 检查拒绝）
	idBytes := []byte(clientID)
	payload := make([]byte, 2+len(idBytes)+2)
	binary.BigEndian.PutUint16(payload[0:2], uint16(len(idBytes)))
	copy(payload[2:], idBytes)
	// tokenLen = 0
	binary.BigEndian.PutUint16(payload[2+len(idBytes):], 0)
	client.Write(payload)

	respBuf := make([]byte, 1)
	client.SetReadDeadline(time.Now().Add(1 * time.Second))
	client.Read(respBuf)

	if respBuf[0] != protocol.DataHandshakeFail {
		t.Errorf("空 DataToken 应返回 Fail(0x%02x)，得到 0x%02x",
			protocol.DataHandshakeFail, respBuf[0])
	}
}

func TestDataChannel_Handshake_ClientHasNoToken(t *testing.T) {
	s := New(0)
	clientID := "no-token-client"
	cc := &ClientConn{
		ID:        clientID,
		proxies:   make(map[string]*ProxyTunnel),
		dataToken: "", // Client 没有 DataToken（不应该发生，但需要防御）
	}
	s.clients.Store(clientID, cc)

	client, serverConn := net.Pipe()
	defer client.Close()
	defer serverConn.Close()

	go s.handleDataConn(serverConn)

	// 发送任意 token，cc.dataToken 为空也应拒绝
	handshakePkg := DataHandshakeBytes(clientID, "any-token")[1:]
	client.Write(handshakePkg)

	respBuf := make([]byte, 1)
	client.SetReadDeadline(time.Now().Add(1 * time.Second))
	client.Read(respBuf)

	if respBuf[0] != protocol.DataHandshakeAuthFail {
		t.Errorf("Client 无 DataToken 时应返回 AuthFail(0x%02x)，得到 0x%02x",
			protocol.DataHandshakeAuthFail, respBuf[0])
	}
}

// ============================================================
// openStreamToClient 测试
// ============================================================

func TestOpenStreamToClient_Success(t *testing.T) {
	s := New(0)
	clientID := "stream-client"
	cc := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, cc)

	// 伪造一个已建立的数据通道 Session (通过自己构造 Yamux 互联)
	clientPipe, serverPipe := net.Pipe()
	defer clientPipe.Close()
	defer serverPipe.Close()

	go func() {
		// Server 端初始化 Yamux as Server
		cc.dataMu.Lock()
		cc.dataSession, _ = mux.NewServerSession(serverPipe, mux.DefaultConfig())
		cc.dataMu.Unlock()
	}()

	// Client 端初始化 Yamux as Client，模拟收到握手
	clientSession, err := mux.NewClientSession(clientPipe, mux.DefaultConfig())
	if err != nil {
		t.Fatalf("创建客户端 Yamux Session 失败: %v", err)
	}
	defer clientSession.Close()

	// 等待服务端 Session 被创建赋值
	time.Sleep(50 * time.Millisecond)

	// 服务器端主动 OpenStreamToClient()
	var stream net.Conn
	var openErr error
	proxyName := "test-tunnel"
	go func() {
		stream, openErr = s.openStreamToClient(cc, proxyName)
	}()

	// 客户端侧 AcceptStream 并读取 StreamHeader
	clientStream, err := clientSession.Accept()
	if err != nil {
		t.Fatalf("客户端接受 Stream 失败: %v", err)
	}
	defer clientStream.Close()

	// 校验通过 Stream 传过来的 header (2字节长度 + Name)
	var lenBuf [2]byte
	if _, err := clientStream.Read(lenBuf[:]); err != nil {
		t.Fatalf("读取 proxyName 长度失败: %v", err)
	}
	nameLen := binary.BigEndian.Uint16(lenBuf[:])
	nameBuf := make([]byte, nameLen)
	if _, err := clientStream.Read(nameBuf); err != nil {
		t.Fatalf("读取 proxyName 内容失败: %v", err)
	}

	if string(nameBuf) != proxyName {
		t.Errorf("ProxyName 期望 %q, 得到 %q", proxyName, string(nameBuf))
	}

	time.Sleep(50 * time.Millisecond)
	if openErr != nil {
		t.Errorf("openStreamToClient 报错: %v", openErr)
	}
	if stream == nil {
		t.Fatal("openStream 期望返回有效 Conn")
	}
	stream.Close()
}

func TestOpenStreamToClient_NoDataSession(t *testing.T) {
	s := New(0)
	clientID := "no-data-client"
	cc := &ClientConn{
		ID:      clientID,
		proxies: make(map[string]*ProxyTunnel),
	}
	s.clients.Store(clientID, cc)

	_, err := s.openStreamToClient(cc, "test-proxy")
	if err == nil {
		t.Error("期望没有建立数据通道时报错，但返回了 nil error")
	}
}
