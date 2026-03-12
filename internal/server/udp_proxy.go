package server

import (
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"netsgo/pkg/mux"
)

// UDPProxyState Server 端 UDP 代理的运行时状态
type UDPProxyState struct {
	packetConn   net.PacketConn // 公网 UDP 监听
	sessions     sync.Map       // srcAddr(string) → *UDPSession
	sessionCount atomic.Int64   // 当前活跃会话数（O(1) 计数）
	done         chan struct{}   // 关闭信号
	closeOnce    sync.Once
}

// Close 关闭 UDP 代理状态，释放所有资源
func (s *UDPProxyState) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		if s.packetConn != nil {
			s.packetConn.Close()
		}
		// 关闭所有会话
		s.sessions.Range(func(key, value any) bool {
			sess := value.(*UDPSession)
			sess.Close()
			s.sessions.Delete(key)
			s.sessionCount.Add(-1)
			return true
		})
	})
}

// UDPSession 一个 UDP 虚拟会话（由外部 srcAddr 标识）
type UDPSession struct {
	srcAddr    net.Addr   // 外部来源地址
	stream     net.Conn   // yamux stream（帧化传输）
	lastActive atomic.Int64 // 最后活跃时间戳（UnixNano）
	done       chan struct{}
	closeOnce  sync.Once
}

// Close 关闭会话
func (s *UDPSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		if s.stream != nil {
			s.stream.Close()
		}
	})
}

// Touch 更新最后活跃时间
func (s *UDPSession) Touch() {
	s.lastActive.Store(time.Now().UnixNano())
}

// IdleDuration 返回空闲时长
func (s *UDPSession) IdleDuration() time.Duration {
	last := s.lastActive.Load()
	return time.Since(time.Unix(0, last))
}

// UDP 会话管理常量
const (
	UDPSessionTimeout = 60 * time.Second  // 会话超时时间
	UDPReaperInterval = 10 * time.Second  // 清理器扫描间隔
	MaxUDPSessions    = 1024              // 每个 UDP 代理最大并发会话数
)

// startUDPProxy 启动一条 UDP 代理隧道。
// 在 RemotePort 上监听 UDP，每收到新 srcAddr 的报文就通过 yamux 创建新会话转发给 Agent。
func (s *Server) startUDPProxy(agent *AgentConn, tunnel *ProxyTunnel) error {
	addr := fmt.Sprintf(":%d", tunnel.Config.RemotePort)
	packetConn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("监听 UDP 端口 %d 失败: %w", tunnel.Config.RemotePort, err)
	}

	// 获取实际分配的端口（如果 RemotePort 为 0）
	actualPort := packetConn.LocalAddr().(*net.UDPAddr).Port
	tunnel.Config.RemotePort = actualPort

	state := &UDPProxyState{
		packetConn: packetConn,
		done:       make(chan struct{}),
	}
	tunnel.UDPState = state

	log.Printf("🚇 UDP 代理隧道已创建: %s [:%d → %s:%d] Agent [%s]",
		tunnel.Config.Name, actualPort, tunnel.Config.LocalIP, tunnel.Config.LocalPort, agent.ID)

	// 启动读取入站 UDP 报文的循环
	go s.udpReadLoop(agent, tunnel, state)

	// 启动定时清理过期会话
	go s.udpReaper(state)

	return nil
}

// udpReadLoop 从 packetConn 接收外部 UDP 报文，按 srcAddr 分发到对应的 yamux stream。
func (s *Server) udpReadLoop(agent *AgentConn, tunnel *ProxyTunnel, state *UDPProxyState) {
	buf := make([]byte, mux.MaxUDPPayload)

	for {
		select {
		case <-state.done:
			return
		default:
		}

		n, srcAddr, err := state.packetConn.ReadFrom(buf)
		if err != nil {
			select {
			case <-state.done:
				return // 正常关闭
			default:
				log.Printf("⚠️ UDP 代理 [%s] ReadFrom 失败: %v", tunnel.Config.Name, err)
				return
			}
		}

		key := srcAddr.String()

		// 查找或创建会话
		val, loaded := state.sessions.Load(key)
		if !loaded {
			// 检查会话数量限制（O(1) 原子读取）
			if state.sessionCount.Load() >= int64(MaxUDPSessions) {
				log.Printf("⚠️ UDP 代理 [%s] 会话数达上限 (%d)，丢弃来自 %s 的报文",
					tunnel.Config.Name, MaxUDPSessions, key)
				continue
			}

			// 打开新的 yamux stream
			stream, err := s.openStreamToAgent(agent, tunnel.Config.Name)
			if err != nil {
				log.Printf("⚠️ UDP 代理 [%s] 打开 Stream 失败: %v", tunnel.Config.Name, err)
				continue
			}

			sess := &UDPSession{
				srcAddr: srcAddr,
				stream:  stream,
				done:    make(chan struct{}),
			}
			sess.Touch()

			// 尝试存入；有可能并发创建，用 LoadOrStore 处理竞争
			actual, alreadyExists := state.sessions.LoadOrStore(key, sess)
			if alreadyExists {
				// 另一个 goroutine 已经创建了，关闭我们的
				stream.Close()
				val = actual
			} else {
				state.sessionCount.Add(1)
				val = sess
				// 启动反向读取循环：stream → 回复给 srcAddr
				go s.udpSessionReverse(state, sess, tunnel.Config.Name)
			}
		}

		sess := val.(*UDPSession)
		sess.Touch()

		// 将 UDP 报文帧化后写入 yamux stream
		if err := mux.WriteUDPFrame(sess.stream, buf[:n]); err != nil {
			log.Printf("⚠️ UDP 代理 [%s] 写入 Stream 失败 [%s]: %v",
				tunnel.Config.Name, key, err)
			// 关闭失败的会话
			sess.Close()
			state.sessions.Delete(key)
			state.sessionCount.Add(-1)
		}
	}
}

// udpSessionReverse 从 yamux stream 读取回复帧，通过 packetConn 回传给外部客户端。
func (s *Server) udpSessionReverse(state *UDPProxyState, sess *UDPSession, proxyName string) {
	defer func() {
		sess.Close()
		state.sessions.Delete(sess.srcAddr.String())
		state.sessionCount.Add(-1)
	}()

	for {
		select {
		case <-sess.done:
			return
		case <-state.done:
			return
		default:
		}

		payload, err := mux.ReadUDPFrame(sess.stream)
		if err != nil {
			select {
			case <-sess.done:
			case <-state.done:
			default:
				// 非正常关闭时才打日志（避免超时清理时的噪音）
			}
			return
		}

		sess.Touch()

		if _, err := state.packetConn.WriteTo(payload, sess.srcAddr); err != nil {
			log.Printf("⚠️ UDP 代理 [%s] WriteTo 失败 [%s]: %v",
				proxyName, sess.srcAddr.String(), err)
			return
		}
	}
}

// udpReaper 定时扫描并清理超时的 UDP 会话。
func (s *Server) udpReaper(state *UDPProxyState) {
	ticker := time.NewTicker(UDPReaperInterval)
	defer ticker.Stop()

	for {
		select {
		case <-state.done:
			return
		case <-ticker.C:
			state.sessions.Range(func(key, value any) bool {
				sess := value.(*UDPSession)
				if sess.IdleDuration() > UDPSessionTimeout {
					log.Printf("🧹 UDP 会话超时，清理: %s", key)
					sess.Close()
					state.sessions.Delete(key)
					state.sessionCount.Add(-1)
				}
				return true
			})
		}
	}
}
