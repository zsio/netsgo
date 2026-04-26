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

	webFS, err := web.DistFS()
	if err != nil {
		return fmt.Errorf("failed to load frontend assets: %w", err)
	}
	s.webFS = webFS
	if s.webFS != nil {
		s.webHandler = http.FileServerFS(s.webFS)
	}

	if web.IsDevMode() {
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

	if s.auth.adminStore != nil && !s.auth.adminStore.IsInitialized() {
		return fmt.Errorf("server is not initialized; use install or init flags to complete setup")
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
	if s.tlsEnabled {
		if s.webFS != nil {
			log.Printf("📊 Web UI: https://localhost:%d", s.Port)
		}
		log.Printf("🔌 Control channel: wss://localhost:%d/ws/control", s.Port)
		log.Printf("🔗 Data channel: wss://localhost:%d/ws/data", s.Port)
	} else {
		if s.webFS != nil {
			log.Printf("📊 Web UI: http://localhost:%d", s.Port)
		}
		log.Printf("🔌 Control channel: ws://localhost:%d/ws/control", s.Port)
		log.Printf("🔗 Data channel: ws://localhost:%d/ws/data", s.Port)
	}

	if s.TLS != nil && s.TLS.Mode == TLSModeOff && len(s.TLS.TrustedProxies) == 0 {
		log.Printf("⚠️ TLS mode is off (reverse proxy mode), but --trusted-proxies is not configured")
		log.Printf("⚠️ The X-Forwarded-For header will be ignored, and rate limiting will use the proxy IP instead of the real client IP")
		log.Printf("⚠️ If you are running behind a reverse proxy, configure: --trusted-proxies 127.0.0.1/32")
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

	serving = true
	return s.httpServer.Serve(serveLn)
}

func (s *Server) cleanupFailedStartup() {
	s.closeDone()
	if s.listener != nil {
		_ = s.listener.Close()
		s.listener = nil
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
				if err := s.trafficStore.Flush(); err != nil {
					log.Printf("⚠️ Failed to persist traffic data: %v", err)
				}
			}
		}
	}
}
