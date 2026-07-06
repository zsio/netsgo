// Package mux — UDP 帧编解码及中继。
// 将 UDP 报文帧化（[2B len][payload]）后在 yamux stream 上传输，
// 实现 UDP-over-TCP 的封装。
package mux

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// MaxUDPPayload UDP 报文最大有效载荷
const MaxUDPPayload = 65507

// WriteUDPFrame 将一个 UDP 报文帧化写入 writer。
// 格式: [2B payload_len big-endian] [NB payload]
func WriteUDPFrame(w io.Writer, payload []byte) error {
	if len(payload) > MaxUDPPayload {
		return fmt.Errorf("UDP payload too large: %d > %d", len(payload), MaxUDPPayload)
	}

	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(payload)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// ReadUDPFrame 从 reader 读取一个 UDP 帧并返回 payload。
// 返回 io.EOF 表示流已关闭。
func ReadUDPFrame(r io.Reader) ([]byte, error) {
	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	payloadLen := binary.BigEndian.Uint16(lenBuf[:])
	if int(payloadLen) > MaxUDPPayload {
		return nil, fmt.Errorf("invalid UDP frame length: %d", payloadLen)
	}
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// udpReplyGracePeriod is the time the UDP→stream direction keeps reading
// reply packets after the stream→UDP direction has ended (stream EOF or
// write error). UDP is connectionless: a request packet may already be in
// flight to the backend, and the reply may arrive shortly after the stream
// side stops sending. Without this grace window, closing the UDP conn
// immediately on stream EOF can drop the reply (e.g. socat RECVFROM gets
// EPIPE writing the response), causing intermittent data-path failures.
const udpReplyGracePeriod = 5 * time.Second

// UDPRelay 在 yamux stream（帧化）和 UDP 连接（原始报文）之间双向转发。
// stream 侧使用 WriteUDPFrame/ReadUDPFrame 帧化协议，
// udpConn 侧使用 Read/Write 原始报文。
//
// 退出语义：
//   - UDP→stream 方向出错时立即关闭两端（回包通道已断，会话结束）。
//   - stream→UDP 方向出错（EOF/写失败）时，只关闭 stream，不关 UDP conn。
//     同时给 UDP conn 设一个 ReadDeadline（grace period），让 UDP→stream
//     方向有机会读完在途回包。grace period 到期后 Read 返回 timeout 错误，
//     触发 closeAll 关闭两端。
func UDPRelay(stream io.ReadWriteCloser, udpConn net.Conn) {
	var once sync.Once
	closeAll := func() {
		_ = stream.Close()
		_ = udpConn.Close()
	}

	// streamEnd is closed when the stream→UDP direction finishes.
	// The supervisor goroutine uses it to arm the grace-period deadline.
	streamEnd := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(2)

	// stream → UDP: 从 stream 读帧，写入 UDP 连接。
	// 出错时只关闭 stream（发 EOF 给对端），不关 UDP conn，
	// 让 UDP→stream 方向有 grace period 读完在途回包。
	go func() {
		defer wg.Done()
		defer close(streamEnd)
		for {
			payload, err := ReadUDPFrame(stream)
			if err != nil {
				_ = stream.Close()
				return
			}
			if _, err := udpConn.Write(payload); err != nil {
				_ = stream.Close()
				return
			}
		}
	}()

	// UDP → stream: 从 UDP 连接读报文，帧化写入 stream。
	// 出错（含 grace period 超时）时关闭两端。
	go func() {
		defer wg.Done()
		buf := make([]byte, MaxUDPPayload)
		for {
			n, err := udpConn.Read(buf)
			if err != nil {
				once.Do(closeAll)
				return
			}
			if err := WriteUDPFrame(stream, buf[:n]); err != nil {
				once.Do(closeAll)
				return
			}
		}
	}()

	// Supervisor: wait for stream→UDP to finish, then arm a read deadline
	// on the UDP conn. This ensures the UDP→stream goroutine wakes up
	// (with a timeout error) after the grace period instead of blocking
	// forever on udpConn.Read, which has no deadline by default.
	go func() {
		<-streamEnd
		_ = udpConn.SetReadDeadline(time.Now().Add(udpReplyGracePeriod))
	}()

	wg.Wait()
}
