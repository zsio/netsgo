package server

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"netsgo/web"
)

func (s *Server) initStore() error {
	path := s.StorePath
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".netsgo", "tunnels.json")
	}
	store, err := NewTunnelStore(path)
	if err != nil {
		return err
	}
	s.store = store
	log.Printf("📦 隧道配置存储: %s", path)

	adminPath := s.StorePath
	if adminPath == "" {
		home, _ := os.UserHomeDir()
		adminPath = filepath.Join(home, ".netsgo", "admin.json")
	} else {
		adminPath = filepath.Join(filepath.Dir(s.StorePath), "admin.json")
	}
	adminStore, err := NewAdminStore(adminPath)
	if err != nil {
		return err
	}
	s.auth.adminStore = adminStore
	log.Printf("📦 系统管理存储: %s", adminPath)

	trafficPath := filepath.Join(s.getDataDir(), "traffic.json")
	trafficStore, err := NewTrafficStore(trafficPath)
	if err != nil {
		return err
	}
	s.trafficStore = trafficStore
	log.Printf("📦 流量历史存储: %s", trafficPath)

	return nil
}

func (s *Server) getDataDir() string {
	if s.StorePath != "" {
		return filepath.Dir(s.StorePath)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".netsgo")
}

func (s *Server) Start() error {
	s.startTime = time.Now()
	s.done = make(chan struct{})
	s.sessions = newSessionManager()

	webFS, err := web.DistFS()
	if err != nil {
		return fmt.Errorf("加载前端资源失败: %w", err)
	}
	s.webFS = webFS
	if s.webFS != nil {
		s.webHandler = http.FileServerFS(s.webFS)
	}

	if web.IsDevMode() {
		log.Printf("🔧 开发模式：前端资源未嵌入，请使用 cd web && bun run dev 独立启动前端")
	} else if s.webFS != nil {
		log.Printf("📦 前端资源已嵌入到二进制中")
	}

	if err := s.initStore(); err != nil {
		return fmt.Errorf("初始化隧道存储失败: %w", err)
	}

	if s.auth.adminStore != nil {
		if err := s.auth.adminStore.CleanExpiredTokens(); err != nil {
			return fmt.Errorf("清理过期 token 失败: %w", err)
		}
		go s.tokenCleanupLoop()
	}

	if s.auth.adminStore != nil && !s.auth.adminStore.IsInitialized() {
		if s.auth.setupToken == "" {
			if s.SetupToken != "" {
				s.auth.setupToken = s.SetupToken
			} else {
				buf := make([]byte, 32)
				if _, err := rand.Read(buf); err != nil {
					return fmt.Errorf("生成 Setup Token 失败: %w", err)
				}
				s.auth.setupToken = hex.EncodeToString(buf)
			}
		}
		s.emitSetupTokenBanner(os.Stderr)
	}

	s.auth.initRateLimiters()

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.Port))
	if err != nil {
		return fmt.Errorf("监听端口 %d 失败: %w", s.Port, err)
	}
	s.listener = ln

	addr := ln.Addr().(*net.TCPAddr)
	if s.Port == 0 {
		s.Port = addr.Port
	}

	var serveLn net.Listener = ln
	if s.TLS != nil && s.TLS.IsEnabled() {
		dataDir := s.getDataDir()
		tlsConfig, fingerprint, err := s.TLS.loadOrBuildTLSConfig(dataDir)
		if err != nil {
			ln.Close()
			return fmt.Errorf("TLS 初始化失败: %w", err)
		}
		s.TLSFingerprint = fingerprint
		s.tlsEnabled = true
		serveLn = tls.NewListener(ln, tlsConfig)
	}

	log.Printf("🚀 NetsGo Server 已启动，监听 :%d", s.Port)
	if s.tlsEnabled {
		if s.webFS != nil {
			log.Printf("📊 Web 面板: https://localhost:%d", s.Port)
		}
		log.Printf("🔌 控制通道: wss://localhost:%d/ws/control", s.Port)
		log.Printf("🔗 数据通道: wss://localhost:%d/ws/data", s.Port)
	} else {
		if s.webFS != nil {
			log.Printf("📊 Web 面板: http://localhost:%d", s.Port)
		}
		log.Printf("🔌 控制通道: ws://localhost:%d/ws/control", s.Port)
		log.Printf("🔗 数据通道: ws://localhost:%d/ws/data", s.Port)
	}

	if s.TLS != nil && s.TLS.Mode == TLSModeOff && len(s.TLS.TrustedProxies) == 0 {
		log.Printf("⚠️ TLS 模式为 off（反向代理模式），但未配置 --trusted-proxies")
		log.Printf("⚠️ X-Forwarded-For 头将被忽略，速率限制将按代理 IP 而非真实客户端 IP 计算")
		log.Printf("⚠️ 如果在反向代理后运行，请配置: --trusted-proxies 127.0.0.1/32")
	}

	s.httpServer = &http.Server{
		Handler:           s.newHTTPHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go s.serverStatusLoop()
	go s.trafficRollupLoop()
	go s.trafficPersistLoop()

	return s.httpServer.Serve(serveLn)
}

func (s *Server) emitSetupTokenBanner(w io.Writer) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "┌──────────────────────────────────────────────────────────────────┐")
	fmt.Fprintln(w, "│  ⚠️  服务尚未初始化                                              │")
	fmt.Fprintln(w, "│  请使用以下 Setup Token 完成初始化:                               │")
	fmt.Fprintf(w, "│  SETUP_TOKEN=%s │\n", s.auth.setupToken)
	fmt.Fprintln(w, "└──────────────────────────────────────────────────────────────────┘")
	fmt.Fprintln(w)
}

func (s *Server) Shutdown(ctx context.Context) error {
	log.Printf("🛑 开始优雅关闭...")

	close(s.done)

	if s.events != nil {
		s.events.Close()
		log.Printf("📡 SSE 事件总线已关闭")
	}

	clientCount := 0
	s.clients.Range(func(key, value any) bool {
		client := value.(*ClientConn)
		clientCount++
		s.invalidateLogicalSessionIfCurrent(client.ID, client.generation, "server_shutdown")
		s.clients.Delete(key)
		return true
	})
	if clientCount > 0 {
		log.Printf("🔌 已断开 %d 个 Client 连接", clientCount)
	}

	s.closeManagedConns("server_shutdown")

	if err := s.waitForLongLivedHandlers(ctx); err != nil {
		log.Printf("⚠️ 等待长连接处理退出超时: %v", err)
		return err
	}

	if s.trafficStore != nil {
		if err := s.trafficStore.Flush(); err != nil {
			log.Printf("⚠️ 流量数据持久化失败: %v", err)
		}
	}

	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			log.Printf("⚠️ HTTP 服务器关闭出错: %v", err)
			return err
		}
	}

	log.Printf("✅ 优雅关闭完成")
	return nil
}

func (s *Server) tokenCleanupLoop() {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			if s.auth.adminStore != nil {
				if err := s.auth.adminStore.CleanExpiredTokens(); err != nil {
					log.Printf("⚠️ 清理过期 Token 失败: %v", err)
				}
			}
		}
	}
}

func (s *Server) trafficRollupLoop() {
	ticker := time.NewTicker(trafficFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			if s.trafficStore != nil {
				s.trafficStore.Compact(time.Now())
			}
		}
	}
}

func (s *Server) trafficPersistLoop() {
	ticker := time.NewTicker(trafficFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			if s.trafficStore != nil {
				if err := s.trafficStore.Flush(); err != nil {
					log.Printf("⚠️ 流量数据持久化失败: %v", err)
				}
			}
		}
	}
}
