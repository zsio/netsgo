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

// UDPRelay 在 yamux stream（帧化）和 UDP 连接（原始报文）之间双向转发。
// stream 侧使用 WriteUDPFrame/ReadUDPFrame 帧化协议，
// udpConn 侧使用 Read/Write 原始报文。
// 任一方向结束后关闭两端。
func UDPRelay(stream io.ReadWriteCloser, udpConn net.Conn) {
	var once sync.Once
	closeAll := func() {
		stream.Close()
		udpConn.Close()
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// stream → UDP: 从 stream 读帧，写入 UDP 连接
	go func() {
		defer wg.Done()
		for {
			payload, err := ReadUDPFrame(stream)
			if err != nil {
				once.Do(closeAll)
				return
			}
			if _, err := udpConn.Write(payload); err != nil {
				once.Do(closeAll)
				return
			}
		}
	}()

	// UDP → stream: 从 UDP 连接读报文，帧化写入 stream
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

	wg.Wait()
}
