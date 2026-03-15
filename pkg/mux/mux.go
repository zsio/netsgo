// Package mux 提供基于 hashicorp/yamux 的多路复用封装。
// 在单条 TCP 连接上虚拟出多个互相隔离的 Stream，用于数据通道的高并发转发。
package mux

import (
	"io"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// Config 多路复用配置
type Config struct {
	KeepAlive         bool          // 是否启用保活
	KeepAliveInterval time.Duration // 保活间隔
	MaxStreamWindow   uint32        // 单 Stream 最大窗口大小
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		KeepAlive:         true,
		KeepAliveInterval: 30 * time.Second,
		MaxStreamWindow:   256 * 1024, // 256KB
	}
}

// toYamuxConfig 转换为 yamux 原生配置
func (c *Config) toYamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	if c == nil {
		return cfg
	}
	cfg.EnableKeepAlive = c.KeepAlive
	if c.KeepAliveInterval > 0 {
		cfg.KeepAliveInterval = c.KeepAliveInterval
	}
	if c.MaxStreamWindow > 0 {
		cfg.MaxStreamWindowSize = c.MaxStreamWindow
	}
	return cfg
}

// NewServerSession 在已有连接上创建 Server 侧 yamux Session。
// Server 侧负责 AcceptStream，等待 Client 侧 OpenStream。
// 在本项目中，Server 是 OpenStream 的发起方（因为外部访客触发），
// 但 yamux 的 Server/Client 角色仅决定哪端初始化帧协议，双端都可以 Open/Accept。
func NewServerSession(conn io.ReadWriteCloser, cfg *Config) (*yamux.Session, error) {
	return yamux.Server(conn, cfg.toYamuxConfig())
}

// NewClientSession 在已有连接上创建 Client 侧 yamux Session。
func NewClientSession(conn io.ReadWriteCloser, cfg *Config) (*yamux.Session, error) {
	return yamux.Client(conn, cfg.toYamuxConfig())
}

// Relay 在两个 ReadWriteCloser 之间双向转发数据。
// 任一方向的 io.Copy 结束后，立即关闭两端连接以让另一方向也结束。
// 返回 a→b 方向和 b→a 方向分别传输的字节数。
func Relay(a, b io.ReadWriteCloser) (atob, btoa int64) {
	var once sync.Once
	closeAll := func() {
		a.Close()
		b.Close()
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// a → b
	go func() {
		defer wg.Done()
		atob, _ = io.Copy(b, a)
		once.Do(closeAll)
	}()

	// b → a
	go func() {
		defer wg.Done()
		btoa, _ = io.Copy(a, b)
		once.Do(closeAll)
	}()

	wg.Wait()
	return atob, btoa
}
