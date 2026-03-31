package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"

	"netsgo/pkg/protocol"
)

const wsMaxMessageSize = 1 << 20

const wsDataMaxMessageSize = 512 * 1024

func checkWSOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}

var controlUpgrader = websocket.Upgrader{
	CheckOrigin:  checkWSOrigin,
	Subprotocols: []string{protocol.WSSubProtocolControl},
}

var dataUpgrader = websocket.Upgrader{
	HandshakeTimeout:  10 * time.Second,
	ReadBufferSize:    32 * 1024,
	WriteBufferSize:   32 * 1024,
	CheckOrigin:       checkWSOrigin,
	EnableCompression: false,
	Subprotocols:      []string{protocol.WSSubProtocolData},
}

func generateDataToken() string {
	buf := make([]byte, 32)
	rand.Read(buf)
	return hex.EncodeToString(buf)
}

func (s *Server) handleControlWS(w http.ResponseWriter, r *http.Request) {
	conn, err := controlUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("❌ WebSocket 升级失败: %v", err)
		return
	}
	release := s.trackManagedConn(conn)
	defer release()
	defer conn.Close()

	conn.SetReadLimit(wsMaxMessageSize)

	log.Printf("📡 新的控制通道连接: %s", r.RemoteAddr)

	client, err := s.handleAuth(conn, r.RemoteAddr)
	if err != nil {
		log.Printf("❌ Client 认证失败 [%s]: %v", r.RemoteAddr, err)
		return
	}

	info := client.GetInfo()
	log.Printf("✅ Client 已认证: %s (%s/%s) [ID: %s, generation=%d]", info.Hostname, info.OS, info.Arch, client.ID, client.generation)

	if s.store != nil {
		if err := s.store.UpdateHostname(client.ID, info.Hostname); err != nil {
			log.Printf("⚠️ 更新隧道展示主机名失败 [%s]: %v", client.ID, err)
		}
	}

	defer s.invalidateLogicalSessionIfCurrent(client.ID, client.generation, "control_loop_exit")

	s.controlLoop(client)
}

func (s *Server) handleAuth(conn *websocket.Conn, remoteAddr string) (*ClientConn, error) {
	ip := remoteIP(remoteAddr)
	if s.auth.clientLimiter != nil {
		if allowed, retryAfter := s.auth.clientLimiter.Allow(ip); !allowed {
			log.Printf("🚫 Client 认证被限速 [%s]: 需等待 %v", remoteAddr, retryAfter)
			slog.Warn("Client 认证被限速", "ip", ip, "module", "security")
			_ = writeAuthResult(conn, protocol.AuthResponse{
				Success:   false,
				Message:   "认证失败",
				Code:      protocol.AuthCodeRateLimited,
				Retryable: true,
			})
			return nil, fmt.Errorf("认证失败")
		}
	}

	authTimeout := s.auth.authTimeout
	if authTimeout == 0 {
		authTimeout = 30 * time.Second
	}
	conn.SetReadDeadline(time.Now().Add(authTimeout))

	var msg protocol.Message
	if err := conn.ReadJSON(&msg); err != nil {
		return nil, fmt.Errorf("读取认证消息失败: %w", err)
	}

	conn.SetReadDeadline(time.Time{})
	if msg.Type != protocol.MsgTypeAuth {
		return nil, fmt.Errorf("期望认证消息，收到: %s", msg.Type)
	}

	var authReq protocol.AuthRequest
	if err := msg.ParsePayload(&authReq); err != nil {
		return nil, fmt.Errorf("解析认证数据失败: %w", err)
	}
	if authReq.InstallID == "" {
		return nil, fmt.Errorf("认证失败: install_id 不能为空")
	}

	var newToken string
	var clientID string

	if s.auth.adminStore != nil {
		if !s.auth.adminStore.IsInitialized() {
			log.Printf("⚠️ 服务未初始化，拒绝 Client 连接 [%s]", remoteAddr)
			slog.Warn("服务未初始化时拒绝 Client 连接", "ip", ip, "module", "security")
			if s.auth.clientLimiter != nil {
				s.auth.clientLimiter.RecordFailure(ip)
			}
			_ = writeAuthResult(conn, protocol.AuthResponse{
				Success:   false,
				Message:   "认证失败",
				Code:      protocol.AuthCodeServerUninitialized,
				Retryable: true,
			})
			return nil, fmt.Errorf("认证失败")
		}

		if authReq.Token != "" {
			clientToken, err := s.auth.adminStore.ValidateClientToken(authReq.Token, authReq.InstallID)
			if err != nil {
				log.Printf("⚠️ Client Token 验证失败 [%s]: %v", remoteAddr, err)
				if s.auth.clientLimiter != nil {
					s.auth.clientLimiter.RecordFailure(ip)
				}
				code := protocol.AuthCodeInvalidToken
				if errors.Is(err, ErrClientTokenRevoked) {
					code = protocol.AuthCodeRevokedToken
				}
				_ = writeAuthResult(conn, protocol.AuthResponse{
					Success:    false,
					Message:    "认证失败",
					Code:       code,
					ClearToken: true,
				})
				return nil, fmt.Errorf("认证失败")
			}

			clientID = clientToken.ClientID
			if current, loaded := s.clients.Load(clientID); loaded {
				currentClient := current.(*ClientConn)
				if currentClient.getState() != clientStateClosing {
					log.Printf("⚠️ Token 并发连接被拒: client_id=%s, install_id=%s, remote=%s", clientID, authReq.InstallID, remoteAddr)
					_ = writeAuthResult(conn, protocol.AuthResponse{
						Success:   false,
						Message:   "认证失败",
						Code:      protocol.AuthCodeConcurrentSession,
						Retryable: true,
					})
					return nil, fmt.Errorf("认证失败")
				}
			}

			log.Printf("🔑 Client Token 认证通过 [install_id=%s]", authReq.InstallID)
			if s.auth.clientLimiter != nil {
				s.auth.clientLimiter.ResetFailures(ip)
			}
		} else {
			record, err := s.auth.adminStore.GetOrCreateClient(authReq.InstallID, authReq.Client, remoteAddr)
			if err != nil {
				return nil, fmt.Errorf("登记 Client 失败: %w", err)
			}
			clientID = record.ID

			if current, loaded := s.clients.Load(clientID); loaded {
				currentClient := current.(*ClientConn)
				if currentClient.getState() != clientStateClosing {
					_ = writeAuthResult(conn, protocol.AuthResponse{
						Success:   false,
						Message:   "认证失败",
						Code:      protocol.AuthCodeConcurrentSession,
						Retryable: true,
					})
					return nil, fmt.Errorf("认证失败")
				}
			}

			tokenStr, _, err := s.auth.adminStore.ExchangeToken(authReq.Key, authReq.InstallID, clientID, remoteAddr)
			if err != nil {
				log.Printf("❌ Client Key 兑换 Token 失败 [%s]: %v", remoteAddr, err)
				if s.auth.clientLimiter != nil {
					s.auth.clientLimiter.RecordFailure(ip)
				}
				_ = writeAuthResult(conn, protocol.AuthResponse{
					Success: false,
					Message: "认证失败",
					Code:    protocol.AuthCodeInvalidKey,
				})
				return nil, fmt.Errorf("认证失败")
			}
			newToken = tokenStr
			log.Printf("🔑 Client Key 兑换 Token 成功 [install_id=%s]", authReq.InstallID)
			if s.auth.clientLimiter != nil {
				s.auth.clientLimiter.ResetFailures(ip)
			}
		}
	}

	if clientID == "" {
		clientID = "unmanaged-" + authReq.InstallID
	}

	client := &ClientConn{
		ID:         clientID,
		InstallID:  authReq.InstallID,
		Info:       authReq.Client,
		RemoteAddr: remoteAddr,
		conn:       conn,
		proxies:    make(map[string]*ProxyTunnel),
		dataToken:  generateDataToken(),
		generation: s.nextClientGeneration(),
		state:      clientStatePendingData,
	}
	s.clients.Store(clientID, client)

	authResp := protocol.AuthResponse{
		Success:   true,
		Message:   "认证成功",
		ClientID:  clientID,
		Token:     newToken,
		DataToken: client.dataToken,
		Code:      protocol.AuthCodeOK,
	}
	if err := writeAuthResult(conn, authResp); err != nil {
		if current, ok := s.clients.Load(clientID); ok && current == client {
			_ = s.invalidateLogicalSessionIfCurrent(clientID, client.generation, "auth_response_failed")
		}
		return nil, fmt.Errorf("发送认证响应失败: %w", err)
	}

	s.startPendingDataTimer(client)
	return client, nil
}

func writeAuthResult(conn *websocket.Conn, authResp protocol.AuthResponse) error {
	message, err := protocol.NewMessage(protocol.MsgTypeAuthResp, authResp)
	if err != nil {
		return err
	}
	return conn.WriteJSON(message)
}
