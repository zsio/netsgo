package mux

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"net"
	"sync"
	"testing"
)

// TestRelay_LargeData 验证 2MB 数据通过 Relay 转发后完整无损
func TestRelay_LargeData(t *testing.T) {
	const dataSize = 2 * 1024 * 1024 // 2MB

	// 生成随机测试数据
	testData := make([]byte, dataSize)
	if _, err := rand.Read(testData); err != nil {
		t.Fatalf("生成随机数据失败: %v", err)
	}
	expectedHash := sha256.Sum256(testData)

	// 构建: src → Relay(a, b) → dst
	srcConn, relayA := net.Pipe()
	relayB, dstConn := net.Pipe()

	// 收集目标端接收的数据
	var received bytes.Buffer
	var recvWg sync.WaitGroup
	recvWg.Add(1)
	go func() {
		defer recvWg.Done()
		io.Copy(&received, dstConn)
	}()

	// 启动 Relay
	var relayWg sync.WaitGroup
	relayWg.Add(1)
	go func() {
		defer relayWg.Done()
		Relay(relayA, relayB)
	}()

	// 源端写入数据后关闭
	_, err := srcConn.Write(testData)
	if err != nil {
		t.Fatalf("写入测试数据失败: %v", err)
	}
	srcConn.Close()

	// 等待 Relay 结束
	relayWg.Wait()
	// dstConn 的读端也会因 relayB 关闭而 EOF
	recvWg.Wait()

	// 验证数据完整性
	if received.Len() != dataSize {
		t.Fatalf("数据长度不匹配: 期望 %d, 实际 %d", dataSize, received.Len())
	}

	actualHash := sha256.Sum256(received.Bytes())
	if expectedHash != actualHash {
		t.Fatal("SHA256 校验失败: 数据在传输过程中损坏")
	}
}

// TestRelay_LargeData_ReverseDirection 验证反方向 (B→A) 也能正确传输 2MB 数据
func TestRelay_LargeData_ReverseDirection(t *testing.T) {
	const dataSize = 2 * 1024 * 1024 // 2MB

	testData := make([]byte, dataSize)
	rand.Read(testData)
	expectedHash := sha256.Sum256(testData)

	// 构建: src(dstConn) → Relay(relayB, relayA) → dst(srcConn)
	// 反方向: 数据从 relayB 侧入口，从 relayA 侧出口
	srcConn, relayA := net.Pipe()
	relayB, dstConn := net.Pipe()

	var received bytes.Buffer
	var recvWg sync.WaitGroup
	recvWg.Add(1)
	go func() {
		defer recvWg.Done()
		io.Copy(&received, srcConn) // 这次从 srcConn 读取（反向）
	}()

	var relayWg sync.WaitGroup
	relayWg.Add(1)
	go func() {
		defer relayWg.Done()
		Relay(relayA, relayB)
	}()

	// 从 dstConn 写入（反方向）
	dstConn.Write(testData)
	dstConn.Close()

	relayWg.Wait()
	recvWg.Wait()

	if received.Len() != dataSize {
		t.Fatalf("反向数据长度不匹配: 期望 %d, 实际 %d", dataSize, received.Len())
	}

	actualHash := sha256.Sum256(received.Bytes())
	if expectedHash != actualHash {
		t.Fatal("反向 SHA256 校验失败: 数据在传输过程中损坏")
	}
}

// TestRelay_ConcurrentStreams 验证同一 yamux Session 上 20 个并发 Stream 各自独立正确
func TestRelay_ConcurrentStreams(t *testing.T) {
	const streamCount = 20
	const perStreamSize = 100 * 1024 // 100KB

	clientConn, serverConn := net.Pipe()
	clientSession, _ := NewClientSession(clientConn, DefaultConfig())
	serverSession, _ := NewServerSession(serverConn, DefaultConfig())

	defer clientSession.Close()
	defer serverSession.Close()

	type result struct {
		idx  int
		hash [32]byte
		data []byte
	}

	results := make(chan result, streamCount)
	var wg sync.WaitGroup

	// Server 侧 Accept 并将数据 echo 回去
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < streamCount; i++ {
			stream, err := serverSession.Accept()
			if err != nil {
				return
			}
			go func() {
				io.Copy(stream, stream) // echo
				stream.Close()
			}()
		}
	}()

	// Client 侧：并发打开 20 个 Stream，各发各的数据，验证 echo 回来的一致
	for i := 0; i < streamCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			stream, err := clientSession.Open()
			if err != nil {
				t.Errorf("Stream %d open failed: %v", idx, err)
				return
			}

			// 生成随机数据
			data := make([]byte, perStreamSize)
			rand.Read(data)
			expectedHash := sha256.Sum256(data)

			// 写入并半关闭写入方向
			go func() {
				stream.Write(data)
				stream.Close()
			}()

			// 读取 echo
			var buf bytes.Buffer
			io.Copy(&buf, stream)

			actualHash := sha256.Sum256(buf.Bytes())
			if expectedHash != actualHash {
				t.Errorf("Stream %d 数据不一致", idx)
			}

			results <- result{idx: idx, hash: actualHash}
		}(i)
	}

	wg.Wait()
	close(results)

	count := 0
	for range results {
		count++
	}
	if count != streamCount {
		t.Errorf("期望 %d 个 Stream 完成，实际 %d", streamCount, count)
	}
}
