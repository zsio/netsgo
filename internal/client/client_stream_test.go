package client

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

func TestClient_HandleStream_Success(t *testing.T) {
	// 1. 启动本地 Mock 业务服务 (Backend)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "ok")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backend data"))
	}))
	defer backend.Close()

	// 解析后端端口
	var localPort int
	fmt.Sscanf(backend.Listener.Addr().String(), "127.0.0.1:%d", &localPort)

	// 2. 初始化 Client
	c := New("ws://localhost:8080", "key")
	proxyName := "test-backend"
	c.proxies.Store(proxyName, protocol.ProxyNewRequest{
		Name:      proxyName,
		LocalIP:   "127.0.0.1",
		LocalPort: localPort,
	})

	// 3. 构建一对互相连接的管道，模拟 Server -> Client 的数据通道 (Yamux)
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	// 初始化 Client 侧 Data Session
	clientSession, _ := mux.NewClientSession(clientConn, mux.DefaultConfig())
	c.dataSession = clientSession
	defer clientSession.Close()

	// 初始化 Server 侧 Data Session
	serverSession, _ := mux.NewServerSession(serverConn, mux.DefaultConfig())
	defer serverSession.Close()

	// 4. Server 主动 OpenStream 模拟外部有流量接入
	var serverStream net.Conn
	var streamErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		serverStream, streamErr = serverSession.Open()
		if streamErr != nil {
			return
		}

		// 往 Stream 里写入 Header (2字节长度 + 名称)
		nameBytes := []byte(proxyName)
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(nameBytes)))
		serverStream.Write(lenBuf[:])
		serverStream.Write(nameBytes)

		// 接着发送真实的 HTTP 请求数据
		reqData := []byte("GET / HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n")
		serverStream.Write(reqData)
	}()

	// 5. Client 被动 AcceptStream 并处理
	clientStream, err := clientSession.AcceptStream()
	if err != nil {
		t.Fatalf("Client AcceptStream 失败: %v", err)
	}

	// 执行要测试的核心逻辑: handleStream
	// 它应该能够读取 proxyName，查找配置，Dial 业务服务，最后 Relay 双向数据
	var relayWg sync.WaitGroup
	relayWg.Add(1)
	go func() {
		defer relayWg.Done()
		c.handleStream(clientStream)
	}()

	// 6. 等待 Server 侧写入流完毕，并读取出返回结果
	wg.Wait()
	if streamErr != nil {
		t.Fatalf("Server OpenStream 失败: %v", streamErr)
	}

	// 从 Server 端的 Stream 中读取 Backend 返回的数据
	respBuf := make([]byte, 1024)
	serverStream.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := serverStream.Read(respBuf)
	if err != nil && err != io.EOF {
		t.Fatalf("读取 Backend 返回失败: %v", err)
	}
	serverStream.Close()

	// 验证 Backend 是否处理了请求返回了 200 OK
	responseStr := string(respBuf[:n])
	if !bytes.Contains([]byte(responseStr), []byte("200 OK")) {
		t.Errorf("期望得到 HTTP 200 OK, 实际得到: %s", responseStr)
	}
	if !bytes.Contains([]byte(responseStr), []byte("X-Backend: ok")) {
		t.Errorf("未找到预期的 Header: X-Backend")
	}

	relayWg.Wait()
}

func TestClient_HandleStream_InvalidHeader(t *testing.T) {
	c := New("ws://localhost:8080", "key")

	clientConn, serverConn := net.Pipe()
	clientSession, _ := mux.NewClientSession(clientConn, mux.DefaultConfig())
	c.dataSession = clientSession

	serverSession, _ := mux.NewServerSession(serverConn, mux.DefaultConfig())

	go func() {
		stream, _ := serverSession.Open()
		// 发送错误的长度
		stream.Write([]byte{0x00, 0x00})
		stream.Close()
	}()

	stream, _ := clientSession.AcceptStream()

	// 如果不崩溃且结束，说明做了防御性校验
	c.handleStream(stream)

	clientConn.Close()
	serverConn.Close()
}

func TestClient_HandleStream_DialFail(t *testing.T) {
	c := New("ws://localhost:8080", "key")
	proxyName := "fail-proxy"
	// 设置一个一定连不上的端口
	c.proxies.Store(proxyName, protocol.ProxyNewRequest{
		Name:      proxyName,
		LocalIP:   "127.0.0.1",
		LocalPort: 99999, // 非法端口/不监听端口
	})

	clientConn, serverConn := net.Pipe()
	clientSession, _ := mux.NewClientSession(clientConn, mux.DefaultConfig())
	c.dataSession = clientSession

	serverSession, _ := mux.NewServerSession(serverConn, mux.DefaultConfig())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		stream, _ := serverSession.Open()
		nameBytes := []byte(proxyName)
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(nameBytes)))
		stream.Write(lenBuf[:])
		stream.Write(nameBytes)

		// 读取应该会 EOF，因为那边 dial 失败会直接 close stream
		buf := make([]byte, 10)
		_, err := stream.Read(buf)
		if err == nil {
			t.Error("期望在目标拒绝连接时收到错误或 EOF")
		}
		stream.Close()
	}()

	stream, _ := clientSession.AcceptStream()
	c.handleStream(stream)

	wg.Wait()
	clientConn.Close()
	serverConn.Close()
}
