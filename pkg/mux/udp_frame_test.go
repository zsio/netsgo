package mux

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// ============================================================
// WriteUDPFrame / ReadUDPFrame 单元测试
// ============================================================

func TestUDPFrame_RoundTrip(t *testing.T) {
	// 正常往返：写入 → 读取应得到相同数据
	payload := []byte("hello UDP world")

	var buf bytes.Buffer
	if err := WriteUDPFrame(&buf, payload); err != nil {
		t.Fatalf("WriteUDPFrame 失败: %v", err)
	}

	// 验证帧格式: [2B len][payload]
	data := buf.Bytes()
	if len(data) != 2+len(payload) {
		t.Fatalf("帧总长度期望 %d，得到 %d", 2+len(payload), len(data))
	}
	frameLen := binary.BigEndian.Uint16(data[:2])
	if int(frameLen) != len(payload) {
		t.Fatalf("帧长度字段期望 %d，得到 %d", len(payload), frameLen)
	}

	// 读取
	got, err := ReadUDPFrame(&buf)
	if err != nil {
		t.Fatalf("ReadUDPFrame 失败: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("数据不匹配: 期望 %q，得到 %q", payload, got)
	}
}

func TestUDPFrame_MultipleFrames(t *testing.T) {
	// 连续写入多个帧，依次读取
	payloads := [][]byte{
		[]byte("frame-1"),
		[]byte("frame-2-longer-data"),
		[]byte("f3"),
	}

	var buf bytes.Buffer
	for _, p := range payloads {
		if err := WriteUDPFrame(&buf, p); err != nil {
			t.Fatalf("WriteUDPFrame 失败: %v", err)
		}
	}

	for i, expected := range payloads {
		got, err := ReadUDPFrame(&buf)
		if err != nil {
			t.Fatalf("ReadUDPFrame #%d 失败: %v", i, err)
		}
		if !bytes.Equal(got, expected) {
			t.Errorf("帧 #%d 数据不匹配: 期望 %q，得到 %q", i, expected, got)
		}
	}
}

func TestUDPFrame_EmptyPayloadRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteUDPFrame(&buf, []byte{}); err != nil {
		t.Fatalf("空 payload 写入失败: %v", err)
	}

	got, err := ReadUDPFrame(&buf)
	if err != nil {
		t.Fatalf("空 payload 读取失败: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("空 payload 往返后长度期望 0，得到 %d", len(got))
	}
}

func TestWriteUDPFrame_OversizedPayload(t *testing.T) {
	var buf bytes.Buffer
	oversized := make([]byte, MaxUDPPayload+1)
	err := WriteUDPFrame(&buf, oversized)
	if err == nil {
		t.Error("超大 payload 应返回错误")
	}
}

func TestWriteUDPFrame_MaxPayload(t *testing.T) {
	// 最大合法大小不应报错
	var buf bytes.Buffer
	maxPayload := make([]byte, MaxUDPPayload)
	for i := range maxPayload {
		maxPayload[i] = byte(i % 256)
	}

	if err := WriteUDPFrame(&buf, maxPayload); err != nil {
		t.Fatalf("最大 payload 写入失败: %v", err)
	}

	got, err := ReadUDPFrame(&buf)
	if err != nil {
		t.Fatalf("最大 payload 读取失败: %v", err)
	}
	if !bytes.Equal(got, maxPayload) {
		t.Error("最大 payload 往返数据不匹配")
	}
}

func TestReadUDPFrame_EOF(t *testing.T) {
	// 空 reader 应返回 EOF
	var buf bytes.Buffer
	_, err := ReadUDPFrame(&buf)
	if err != io.EOF {
		t.Errorf("空 reader 读取期望 EOF，得到: %v", err)
	}
}

func TestReadUDPFrame_InvalidLength(t *testing.T) {
	// 写入超过上限的长度（非法）
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint16(MaxUDPPayload+1))

	_, err := ReadUDPFrame(&buf)
	if err == nil {
		t.Error("超过上限的帧长度应返回错误")
	}
}

func TestReadUDPFrame_TruncatedPayload(t *testing.T) {
	// 帧头说有 100 字节，但实际只有 5 字节
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint16(100))
	_, _ = buf.Write([]byte("short"))

	_, err := ReadUDPFrame(&buf)
	if err == nil {
		t.Error("截断的 payload 应返回错误")
	}
}

// ============================================================
// UDPRelay 集成测试
// ============================================================

func TestUDPRelay_Bidirectional(t *testing.T) {
	// 测试结构:
	// testWriter ←pipe→ stream (帧化) ←UDPRelay→ udpConn ←UDP→ echoServer

	// 1. 启动本地 UDP echo 服务
	echoConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动 UDP echo 服务失败: %v", err)
	}
	defer func() { _ = echoConn.Close() }()
	echoAddr := echoConn.LocalAddr()

	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := echoConn.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = echoConn.WriteTo(buf[:n], addr) // echo back
		}
	}()

	// 2. 创建 pipe 模拟 yamux stream
	streamSide, testSide := net.Pipe()

	// 3. Dial UDP echo 服务
	udpConn, err := net.Dial("udp", echoAddr.String())
	if err != nil {
		t.Fatalf("Dial UDP 失败: %v", err)
	}

	// 4. 启动 UDPRelay
	relayDone := make(chan struct{})
	go func() {
		UDPRelay(streamSide, udpConn)
		close(relayDone)
	}()

	// 5. 从 testSide 写帧，应该被 relay 到 echo 服务并收到帧化的回复
	testPayload := []byte("ping from test")
	if err := WriteUDPFrame(testSide, testPayload); err != nil {
		t.Fatalf("写入帧失败: %v", err)
	}

	// 读取回复帧
	testSide.SetReadDeadline(time.Now().Add(2 * time.Second))
	reply, err := ReadUDPFrame(testSide)
	if err != nil {
		t.Fatalf("读取回复帧失败: %v", err)
	}

	if !bytes.Equal(reply, testPayload) {
		t.Errorf("回复数据不匹配: 期望 %q，得到 %q", testPayload, reply)
	}

	// 6. 关闭 testSide，Relay 应该结束
	testSide.Close()

	select {
	case <-relayDone:
	case <-time.After(3 * time.Second):
		t.Error("UDPRelay 超时未结束")
	}
}

func TestUDPRelay_MultiplePackets(t *testing.T) {
	// 测试多个报文的连续往返

	// 启动 UDP echo 服务
	echoConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动 UDP echo 服务失败: %v", err)
	}
	defer func() { _ = echoConn.Close() }()

	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := echoConn.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = echoConn.WriteTo(buf[:n], addr)
		}
	}()

	streamSide, testSide := net.Pipe()
	udpConn, _ := net.Dial("udp", echoConn.LocalAddr().String())

	go UDPRelay(streamSide, udpConn)

	// 连续发送 5 个报文
	for i := 0; i < 5; i++ {
		payload := []byte("packet-" + string(rune('A'+i)))
		if err := WriteUDPFrame(testSide, payload); err != nil {
			t.Fatalf("写入帧 #%d 失败: %v", i, err)
		}

		testSide.SetReadDeadline(time.Now().Add(2 * time.Second))
		reply, err := ReadUDPFrame(testSide)
		if err != nil {
			t.Fatalf("读取回复帧 #%d 失败: %v", i, err)
		}
		if !bytes.Equal(reply, payload) {
			t.Errorf("帧 #%d 回复不匹配: 期望 %q，得到 %q", i, payload, reply)
		}
	}

	testSide.Close()
}

func TestUDPRelay_StreamCloseEndsRelay(t *testing.T) {
	// stream 关闭后 Relay 应结束

	echoConn, _ := net.ListenPacket("udp", "127.0.0.1:0")
	defer func() { _ = echoConn.Close() }()

	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := echoConn.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = echoConn.WriteTo(buf[:n], addr)
		}
	}()

	streamSide, testSide := net.Pipe()
	udpConn, _ := net.Dial("udp", echoConn.LocalAddr().String())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		UDPRelay(streamSide, udpConn)
	}()

	// 立即关闭 testSide（stream 对端）
	testSide.Close()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 正常退出
	case <-time.After(3 * time.Second):
		t.Error("stream 关闭后 UDPRelay 超时未结束")
	}
}
