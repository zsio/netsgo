package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"netsgo/pkg/datadir"
	"netsgo/web"
)

func (s *Server) initStore() error {
	path := s.serverDBPath()
	s.serverDB = nil
	s.serverDBCloseOnce = sync.Once{}
	s.serverDBCloseErr = nil

	// Server owns this shared handle. Domain stores created below borrow it and
	// must not close it independently; closeServerDB is the single close path.
	db, err := openServerDB(path)
	if err != nil {
		return err
	}
	s.serverDB = db

	store, err := newTunnelStoreWithDB(path, db, false)
	if err != nil {
		_ = s.closeServerDB()
		return err
	}
	s.store = store
	log.Printf("📦 SQLite server store: %s", path)

	adminStore, err := newAdminStoreWithDB(path, db, false)
	if err != nil {
		_ = s.closeServerDB()
		return err
	}
	s.auth.adminStore = adminStore

	trafficStore := newTrafficStoreWithDB(path, db, false)
	s.trafficStore = trafficStore
	store.attachTrafficStore(trafficStore, s.trafficAccumulator)

	return nil
}

func (s *Server) getDataDir() string {
	if s.DataDir != "" {
		return s.DataDir
	}
	return datadir.DefaultDataDir()
}

func (s *Server) serverDataDir() string {
	return filepath.Join(s.getDataDir(), "server")
}

func (s *Server) serverDBPath() string {
	return filepath.Join(s.serverDataDir(), serverDBFileName)
}

func (s *Server) getStorePath() string {
	if s.store != nil {
		return s.store.path
	}
	return s.serverDBPath()
}

func (s *Server) Start() error {
	s.startTime = time.Now()
	s.done = make(chan struct{})
	s.doneCloseOnce = sync.Once{}
	s.sessions = newSessionManager()
	serving := false
	defer func() {
		if !serving {
			s.cleanupFailedStartup()
		}
	}()

	s.devMode = web.IsDevMode()

	webFS, err := web.DistFS()
	if err != nil {
		return fmt.Errorf("failed to load frontend assets: %w", err)
	}
	s.webFS = webFS
	if s.webFS != nil {
		s.webHandler = http.FileServerFS(s.webFS)
	}

	if s.devMode {
		log.Printf("🔧 Dev mode: frontend assets are not embedded; start the frontend separately with cd web && bun run dev")
	} else if s.webFS != nil {
		log.Printf("📦 Frontend assets are embedded in the binary")
	}

	if err := s.initStore(); err != nil {
		return fmt.Errorf("failed to initialize tunnel store: %w", err)
	}

	if s.auth.adminStore != nil {
		if err := s.auth.adminStore.CleanExpiredTokens(); err != nil {
			return fmt.Errorf("failed to clean expired tokens: %w", err)
		}
	}

	if s.auth.adminStore != nil {
		initialized, err := s.auth.adminStore.IsInitializedE()
		if err != nil {
			return fmt.Errorf("failed to read initialization state: %w", err)
		}
		if !initialized {
			return fmt.Errorf("server is not initialized; use install or init flags to complete setup")
		}
	}
	var serverConfig *ServerConfig
	if s.auth.adminStore != nil {
		cfg, err := s.auth.adminStore.GetServerConfigE()
		if err != nil {
			return fmt.Errorf("failed to read server config: %w", err)
		}
		serverConfig = &cfg
	}

	s.auth.initRateLimiters()

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.Port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", s.Port, err)
	}
	s.listener = ln

	addr := ln.Addr().(*net.TCPAddr)
	if s.Port == 0 {
		s.Port = addr.Port
	}
	if stunConn, listenErr := net.ListenPacket("udp", fmt.Sprintf(":%d", s.Port)); listenErr != nil {
		log.Printf("⚠️ P2P STUN listener unavailable on UDP :%d: %v", s.Port, listenErr)
	} else {
		s.stunConn = stunConn
		go s.serveSTUN(stunConn)
	}

	serveLn := ln
	if s.TLS != nil && s.TLS.IsEnabled() {
		dataDir := s.getDataDir()
		tlsConfig, fingerprint, err := s.TLS.loadOrBuildTLSConfig(dataDir)
		if err != nil {
			return fmt.Errorf("TLS initialization failed: %w", err)
		}
		s.TLSFingerprint = fingerprint
		s.tlsEnabled = true
		serveLn = tls.NewListener(ln, tlsConfig)
	}

	log.Printf("🚀 NetsGo Server started, listening on :%d", s.Port)
	logServerEndpoints(s.Port, s.tlsEnabled, s.webFS != nil, serverConfig)

	if s.TLS != nil && len(s.TLS.TrustedProxies) == 0 {
		log.Printf("⚠️ Forwarded client headers are trusted from loopback proxies only")
		log.Printf("⚠️ HTTP tunnel source allowlists and rate limiting behind non-local reverse proxies require --trusted-proxies")
	}

	s.httpServer = &http.Server{
		Handler:           s.newHTTPHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	if s.auth.adminStore != nil {
		go s.tokenCleanupLoop()
	}
	go s.serverStatusLoop()
	go s.trafficRollupLoop()
	go s.trafficPersistLoop()
	go s.trafficRealtimeLoop()
	go s.unifiedTunnelReconcileLoop()
	go s.p2pLeaseLoop()

	serving = true
	return s.httpServer.Serve(serveLn)
}

func logServerEndpoints(port int, tlsEnabled bool, hasWebUI bool, cfg *ServerConfig) {
	local := localEndpointURLs(port, tlsEnabled)
	if hasWebUI {
		log.Printf("📊 Local Web UI: %s", local.Web)
	}
	log.Printf("🔌 Local control channel: %s", local.Control)
	log.Printf("🔗 Local data channel: %s", local.Data)

	publicAddr, ok := configuredPublicServerAddr(cfg)
	if !ok {
		return
	}
	public, err := publicEndpointURLs(publicAddr)
	if err != nil {
		log.Printf("⚠️ Configured public server address is invalid and was not logged: %v", err)
		return
	}
	if hasWebUI {
		log.Printf("📊 Configured public Web UI: %s", public.Web)
	}
	log.Printf("🔌 Configured public control channel: %s", public.Control)
	log.Printf("🔗 Configured public data channel: %s", public.Data)
}

func (s *Server) cleanupFailedStartup() {
	s.closeDone()
	if s.listener != nil {
		_ = s.listener.Close()
		s.listener = nil
	}
	if s.stunConn != nil {
		_ = s.stunConn.Close()
		s.stunConn = nil
	}
	if s.auth != nil && s.auth.adminStore != nil {
		if err := s.auth.adminStore.Close(); err != nil {
			log.Printf("⚠️ Failed to close admin store after startup failure: %v", err)
		}
	}
	if s.store != nil {
		if err := s.store.Close(); err != nil {
			log.Printf("⚠️ Failed to close tunnel store after startup failure: %v", err)
		}
	}
	if s.trafficStore != nil {
		if err := s.trafficStore.Close(); err != nil {
			log.Printf("⚠️ Failed to close traffic store after startup failure: %v", err)
		}
	}
	if err := s.closeServerDB(); err != nil {
		log.Printf("⚠️ Failed to close server DB after startup failure: %v", err)
	}
}

func (s *Server) closeDone() {
	if s.done == nil {
		return
	}
	s.doneCloseOnce.Do(func() {
		close(s.done)
	})
}

func (s *Server) Shutdown(ctx context.Context) (err error) {
	log.Printf("🛑 Starting graceful shutdown...")
	defer func() {
		if s.auth != nil && s.auth.adminStore != nil {
			if closeErr := s.auth.adminStore.Close(); closeErr != nil {
				log.Printf("⚠️ Failed to close admin store: %v", closeErr)
				if err == nil {
					err = closeErr
				}
			}
		}
		if s.store != nil {
			if closeErr := s.store.Close(); closeErr != nil {
				log.Printf("⚠️ Failed to close tunnel store: %v", closeErr)
				if err == nil {
					err = closeErr
				}
			}
		}
		if s.trafficStore != nil {
			if closeErr := s.trafficStore.Close(); closeErr != nil {
				log.Printf("⚠️ Failed to close traffic store: %v", closeErr)
				if err == nil {
					err = closeErr
				}
			}
		}
		if closeErr := s.closeServerDB(); closeErr != nil {
			log.Printf("⚠️ Failed to close server DB: %v", closeErr)
			if err == nil {
				err = closeErr
			}
		}
	}()

	s.closeDone()
	if s.stunConn != nil {
		_ = s.stunConn.Close()
		s.stunConn = nil
	}

	if s.events != nil {
		s.events.Close()
		log.Printf("📡 SSE event bus closed")
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
		log.Printf("🔌 Disconnected %d client connections", clientCount)
	}

	s.closeManagedConns("server_shutdown")

	if err := s.waitForLongLivedHandlers(ctx); err != nil {
		log.Printf("⚠️ Timed out waiting for long-lived handlers to exit: %v", err)
		return err
	}

	if s.trafficStore != nil {
		s.flushTrafficObservations()
		if err := s.trafficStore.Flush(); err != nil {
			log.Printf("⚠️ Failed to persist traffic data: %v", err)
		}
	}

	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(ctx); err != nil {
			log.Printf("⚠️ HTTP server shutdown failed: %v", err)
			return err
		}
	}

	log.Printf("✅ Graceful shutdown complete")
	return nil
}

func (s *Server) closeServerDB() error {
	if s == nil || s.serverDB == nil {
		return nil
	}
	s.serverDBCloseOnce.Do(func() {
		s.serverDBCloseErr = s.serverDB.Close()
	})
	return s.serverDBCloseErr
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
					log.Printf("⚠️ Failed to clean expired tokens: %v", err)
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
				s.flushTrafficObservations()
				if err := s.trafficStore.Compact(time.Now()); err != nil {
					log.Printf("⚠️ Failed to compact traffic data: %v", err)
				}
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
				s.flushTrafficObservations()
				if err := s.trafficStore.Flush(); err != nil {
					log.Printf("⚠️ Failed to persist traffic data: %v", err)
				}
			}
		}
	}
}
