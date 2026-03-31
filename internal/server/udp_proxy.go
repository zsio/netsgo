package server

import (
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"netsgo/pkg/mux"
	"netsgo/pkg/protocol"
)

// UDPProxyState Server 端 UDP 代理的运行时状态
type UDPProxyState struct {
	packetConn   net.PacketConn // 公网 UDP 监听
	sessions     sync.Map       // srcAddr(string) → *UDPSession
	sessionCount atomic.Int64   // 当前活跃会话数（O(1) 计数）
	sessionIPMu  sync.Mutex
	sessionIPs   map[string]int // src IP → 活跃会话数
	done         chan struct{}  // 关闭信号
	closeOnce    sync.Once
}

// Close 关闭 UDP 代理状态，释放所有资源
func (s *UDPProxyState) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		if s.packetConn != nil {
			s.packetConn.Close()
		}
		// 关闭所有会话。sess.Close() 会触发 udpSessionReverse 的 ReadUDPFrame 返回错误，
		// 进而执行其 defer 中的 removeSession。两者竞争同一个 key，
		// removeSession 内部的 LoadAndDelete 保证只有一方实际递减计数。
		s.sessions.Range(func(key, value any) bool {
			sess := value.(*UDPSession)
			sess.Close()
			s.removeSession(key.(string))
			return true
		})
	})
}

// removeSession 原子地从 sessions map 中移除会话并递减计数。
// 返回 true 表示本次调用实际完成了移除（调用方是第一个清理者）。
// 使用 LoadAndDelete 保证多 goroutine 竞争时只有一个返回 loaded=true。
func (s *UDPProxyState) removeSession(key string) bool {
	if value, loaded := s.sessions.LoadAndDelete(key); loaded {
		s.sessionCount.Add(-1)
		if sess, ok := value.(*UDPSession); ok && sess.ipKey != "" {
			s.sessionIPMu.Lock()
			if count := s.sessionIPs[sess.ipKey]; count <= 1 {
				delete(s.sessionIPs, sess.ipKey)
			} else {
				s.sessionIPs[sess.ipKey] = count - 1
			}
			s.sessionIPMu.Unlock()
		}
		return true
	}
	return false
}

func (s *UDPProxyState) storeSession(key string, sess *UDPSession) (*UDPSession, bool) {
	actual, loaded := s.sessions.LoadOrStore(key, sess)
	if loaded {
		return actual.(*UDPSession), false
	}

	s.sessionCount.Add(1)
	if sess.ipKey != "" {
		s.sessionIPMu.Lock()
		if s.sessionIPs == nil {
			s.sessionIPs = make(map[string]int)
		}
		s.sessionIPs[sess.ipKey]++
		s.sessionIPMu.Unlock()
	}

	return sess, true
}

func (s *UDPProxyState) sessionCountForIP(ipKey string) int {
	if ipKey == "" {
		return 0
	}

	s.sessionIPMu.Lock()
	defer s.sessionIPMu.Unlock()
	return s.sessionIPs[ipKey]
}

func (s *UDPProxyState) canCreateSessionForIP(ipKey string) bool {
	if s.sessionCount.Load() >= int64(MaxUDPSessions) {
		return false
	}
	return s.sessionCountForIP(ipKey) < MaxUDPSessionsPerIP
}

// UDPSession 一个 UDP 虚拟会话（由外部 srcAddr 标识）
type UDPSession struct {
	srcAddr    net.Addr     // 外部来源地址
	ipKey      string       // 来源 IP（不含端口），用于单 IP 配额
	stream     net.Conn     // yamux stream（帧化传输）
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
	UDPSessionTimeout   = 60 * time.Second // 会话超时时间
	UDPReaperInterval   = 10 * time.Second // 清理器扫描间隔
	MaxUDPSessions      = 1024             // 每个 UDP 代理最大并发会话数
	MaxUDPSessionsPerIP = 128              // 每个源 IP 最大并发会话数
)

func udpSourceIPKey(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	if udpAddr, ok := addr.(*net.UDPAddr); ok {
		if udpAddr.IP != nil {
			return udpAddr.IP.String()
		}
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err == nil {
		return host
	}
	return addr.String()
}

// startUDPProxy 启动一条 UDP 代理隧道。
// 在 RemotePort 上监听 UDP，每收到新 srcAddr 的报文就通过 yamux 创建新会话转发给 Client。
func (s *Server) startUDPProxy(client *ClientConn, tunnel *ProxyTunnel) error {
	addr := fmt.Sprintf(":%d", tunnel.Config.RemotePort)
	packetConn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("监听 UDP 端口 %d 失败: %w", tunnel.Config.RemotePort, err)
	}

	actualPort := packetConn.LocalAddr().(*net.UDPAddr).Port
	tunnel.Config.RemotePort = actualPort

	state := &UDPProxyState{
		packetConn: packetConn,
		sessionIPs: make(map[string]int),
		done:       make(chan struct{}),
	}
	tunnel.UDPState = state

	log.Printf("🚇 UDP 代理隧道已创建: %s [:%d → %s:%d] Client [%s]",
		tunnel.Config.Name, actualPort, tunnel.Config.LocalIP, tunnel.Config.LocalPort, client.ID)

	// 注意：udpReadLoop 必须是单 goroutine。若改为并发，
	// sessionCount 的 Load-then-Add 上限检查需改为 CAS 原子操作。
	go s.udpReadLoop(client, tunnel, state)

	// 启动定时清理过期会话
	go s.udpReaper(state)

	return nil
}

func (s *Server) markUDPProxyRuntimeErrorIfCurrent(
	client *ClientConn,
	tunnel *ProxyTunnel,
	state *UDPProxyState,
	message string,
) {
	if state != nil {
		state.Close()
	}

	client.proxyMu.Lock()
	current, exists := client.proxies[tunnel.Config.Name]
	if !exists ||
		current != tunnel ||
		current.UDPState != state ||
		!isTunnelExposed(current.Config) {
		client.proxyMu.Unlock()
		return
	}
	setProxyConfigStates(&current.Config, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message)
	config := current.Config
	client.proxyMu.Unlock()

	if err := s.persistTunnelStates(client.ID, tunnel.Config.Name, protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, message); err != nil {
		log.Printf("⚠️ UDP 代理 [%s] 持久化 error 状态失败: %v", tunnel.Config.Name, err)
	}
	s.emitTunnelChanged(client.ID, config, "error")
	if err := s.notifyClientProxyClose(client, tunnel.Config.Name, "runtime_error"); err != nil {
		log.Printf("⚠️ UDP 代理 [%s] 通知 client 关闭失败: %v", tunnel.Config.Name, err)
	}
}

// udpReadLoop 从 packetConn 接收外部 UDP 报文，按 srcAddr 分发到对应的 yamux stream。
func (s *Server) udpReadLoop(client *ClientConn, tunnel *ProxyTunnel, state *UDPProxyState) {
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
				s.markUDPProxyRuntimeErrorIfCurrent(
					client,
					tunnel,
					state,
					fmt.Sprintf("UDP 代理读包失败: %v", err),
				)
				return
			}
		}

		key := srcAddr.String()
		ipKey := udpSourceIPKey(srcAddr)

		// 查找或创建会话
		val, loaded := state.sessions.Load(key)
		if !loaded {
			// 检查会话数量限制。
			// sessionCount.Load() 与后续 Add(1) 之间不是原子的；
			// 此处安全的前提是：整个函数只在单个 goroutine 中运行（不并发）。
			if state.sessionCount.Load() >= int64(MaxUDPSessions) {
				log.Printf("⚠️ UDP 代理 [%s] 会话数达上限 (%d)，丢弃来自 %s 的报文",
					tunnel.Config.Name, MaxUDPSessions, key)
				continue
			}
			if !state.canCreateSessionForIP(ipKey) {
				log.Printf("⚠️ UDP 代理 [%s] 单 IP 会话数达上限 (%d)，丢弃来自 %s 的报文",
					tunnel.Config.Name, MaxUDPSessionsPerIP, key)
				continue
			}

			// 打开新的 yamux stream
			stream, err := s.openStreamToClient(client, tunnel.Config.Name)
			if err != nil {
				log.Printf("⚠️ UDP 代理 [%s] 打开 Stream 失败: %v", tunnel.Config.Name, err)
				s.markUDPProxyRuntimeErrorIfCurrent(
					client,
					tunnel,
					state,
					fmt.Sprintf("UDP 代理转发通道失败: %v", err),
				)
				return
			}

			sess := &UDPSession{
				srcAddr: srcAddr,
				ipKey:   ipKey,
				stream:  stream,
				done:    make(chan struct{}),
			}
			sess.Touch()

			// 尝试存入；有可能并发创建，用 storeSession 处理竞争并维护计数。
			actual, added := state.storeSession(key, sess)
			if !added {
				// 另一个 goroutine 已经创建了，关闭我们的
				stream.Close()
				val = actual
			} else {
				val = sess
				// 启动反向读取循环：stream → 回复给 srcAddr
				go s.udpSessionReverse(state, sess, client.ID, tunnel.Config.Name)
			}
		}

		sess := val.(*UDPSession)
		sess.Touch()

		// 将 UDP 报文帧化后写入 yamux stream
		if err := mux.WriteUDPFrame(sess.stream, buf[:n]); err != nil {
			log.Printf("⚠️ UDP 代理 [%s] 写入 Stream 失败 [%s]: %v",
				tunnel.Config.Name, key, err)
			// 关闭失败的会话；removeSession 用 LoadAndDelete 保证原子性，
			// 即使 udpReaper 或 Close() 已先行清理，此处也只会得到 loaded=false，不会双减。
			sess.Close()
			state.removeSession(key)
		} else if s.trafficStore != nil {
			s.trafficStore.RecordBytes(client.ID, tunnel.Config.Name, tunnel.Config.Type, uint64(n), 0)
		}
	}
}

// udpSessionReverse 从 yamux stream 读取回复帧，通过 packetConn 回传给外部客户端。
// 退出机制：goroutine 阻塞于 ReadUDPFrame，由 sess.Close()→stream.Close() 触发退出，
// 而非通过 select 轮询——这是有意为之，不需要为 ReadUDPFrame 单独设置 ReadDeadline。
func (s *Server) udpSessionReverse(state *UDPProxyState, sess *UDPSession, clientID, proxyName string) {
	defer func() {
		sess.Close()
		state.removeSession(sess.srcAddr.String())
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
		if s.trafficStore != nil {
			s.trafficStore.RecordBytes(clientID, proxyName, protocol.ProxyTypeUDP, 0, uint64(len(payload)))
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
					state.removeSession(key.(string))
				}
				return true
			})
		}
	}
}
