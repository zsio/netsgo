package server

import (
	"io"
	"net"
)

// PeekConn 包装 net.Conn，支持 peek 首字节后回退。
// 用于单端口协议复用：peek 第一个字节判断是 HTTP 流量还是数据通道连接。
type PeekConn struct {
	net.Conn
	peeked  byte
	hasPeek bool
}

// PeekByte 读取首字节但不消费（后续 Read 仍然会返回该字节）
func (pc *PeekConn) PeekByte() (byte, error) {
	if pc.hasPeek {
		return pc.peeked, nil
	}
	var buf [1]byte
	_, err := io.ReadFull(pc.Conn, buf[:])
	if err != nil {
		return 0, err
	}
	pc.peeked = buf[0]
	pc.hasPeek = true
	return pc.peeked, nil
}

// Read 实现 io.Reader，先返回 peek 过的字节，再正常读取
func (pc *PeekConn) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if pc.hasPeek {
		b[0] = pc.peeked
		pc.hasPeek = false
		if len(b) == 1 {
			return 1, nil
		}
		n, err := pc.Conn.Read(b[1:])
		return n + 1, err
	}
	return pc.Conn.Read(b)
}
