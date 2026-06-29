//go:build e2e

package e2e_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"netsgo/internal/socks5wire"
)

const (
	defaultAdminUser       = "admin"
	defaultManagementHost  = "panel.system.local"
	defaultTunnelHost      = "app.system.local"
	defaultTargetHostname  = "system-target-client"
	defaultIngressHostname = "system-ingress-client"
	backendHost            = "tcp-backend"
	backendPort            = 18083
	backendResponse        = "system tcp backend response"
	backendAltHost         = "tcp-backend-alt"
	backendAltPort         = 18085
	backendAltResponse     = "system tcp alt response"
	backendSlowHost        = "tcp-backend-slow"
	backendSlowPort        = 18086
	backendEchoHost        = "tcp-backend-echo"
	backendEchoPort        = 18087
	udpBackendHost         = "udp-backend"
	udpBackendPort         = 18084
)

type systemHarness struct {
	projectName             string
	composeFiles            []string
	composeEnv              []string
	baseURL                 string
	directBaseURL           string
	managementHost          string
	tunnelHost              string
	directTunnelHost        string
	authTunnelHost          string
	adminUser               string
	adminPass               string
	targetHostname          string
	ingressHostname         string
	serverTCPPort           int
	serverUDPPort           int
	serverSOCKS5Port        int
	serverTCPAltPort        int
	c2cSOCKS5Port           int
	c2cDenyPort             int
	c2cTCPPort              int
	c2cTCPAltPort           int
	c2cTCPSlowPort          int
	c2cUDPPort              int
	c2cSOCKS5AuthPort       int
	c2cSOCKS5SourceDenyPort int
	adminToken              string
	targetClientID          string
	ingressClientID         string
}

type apiClient struct {
	ID     string `json:"id"`
	Online bool   `json:"online"`
	Info   struct {
		Hostname string `json:"hostname"`
	} `json:"info"`
}

type apiKeyResponse struct {
	RawKey string `json:"raw_key"`
}

type tunnelResponse struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	RuntimeState string          `json:"runtime_state"`
	Issues       json.RawMessage `json:"issues,omitempty"`
}

type tunnelIssueResponse struct {
	Code     string          `json:"code"`
	Scope    string          `json:"scope"`
	ClientID string          `json:"client_id,omitempty"`
	Details  json.RawMessage `json:"details,omitempty"`
}

func TestSystemE2E(t *testing.T) {
	h := newSystemHarness(t)
	h.startInfrastructure(t)
	h.expectAdminLoginRejected(t)
	h.adminToken = h.waitForAdminToken(t, 90*time.Second)
	h.expectAdminAPIAuthorization(t)
	clientKey := h.createAPIKey(t)
	h.startClients(t, clientKey)
	h.targetClientID, h.ingressClientID = h.waitForClientPair(t, 90*time.Second)

	var (
		httpProxyTunnel  tunnelResponse
		httpDirectTunnel tunnelResponse
		serverTCP        tunnelResponse
		largeTCP         tunnelResponse
		serverUDP        tunnelResponse
		serverSOCKS5     tunnelResponse
		c2cSOCKS5        tunnelResponse
		c2cTCP           tunnelResponse
		c2cTCPAlt        tunnelResponse
		c2cTCPSlow       tunnelResponse
		c2cUDP           tunnelResponse
	)

	t.Run("HTTP server_expose works through reverse proxy and direct server", func(t *testing.T) {
		httpProxyTunnel = h.createHTTPServerExposeTunnel(t, "system-http-proxy", h.tunnelHost, `{"type":"none"}`)
		h.waitTunnelState(t, httpProxyTunnel.ID, "active", 90*time.Second)
		h.expectTunnelNoIssues(t, httpProxyTunnel.ID)
		h.expectHTTPContainsAt(t, h.baseURL, h.tunnelHost, backendResponse, 60*time.Second)

		httpDirectTunnel = h.createHTTPServerExposeTunnel(t, "system-http-direct", h.directTunnelHost, `{"type":"none"}`)
		h.waitTunnelState(t, httpDirectTunnel.ID, "active", 90*time.Second)
		h.expectTunnelNoIssues(t, httpDirectTunnel.ID)
		h.expectHTTPContainsAt(t, h.directBaseURL, h.directTunnelHost, backendResponse, 60*time.Second)

		h.compose(t, h.composeEnv, "restart", "proxy")
		h.expectHTTPContainsAt(t, h.baseURL, h.tunnelHost, backendResponse, 90*time.Second)
	})

	t.Run("HTTP Basic auth protects server_expose route", func(t *testing.T) {
		tunnel := h.createHTTPServerExposeTunnel(t, "system-http-basic-auth", h.authTunnelHost, `{"type":"basic","username":"alice","password":"secret"}`)
		h.waitTunnelState(t, tunnel.ID, "active", 90*time.Second)
		h.expectTunnelNoIssues(t, tunnel.ID)
		h.expectHTTPStatusAt(t, h.baseURL, h.authTunnelHost, nil, http.StatusUnauthorized, "", 30*time.Second)
		h.expectHTTPStatusAt(t, h.baseURL, h.authTunnelHost, &basicAuth{username: "alice", password: "wrong"}, http.StatusUnauthorized, "", 30*time.Second)
		h.expectHTTPStatusAt(t, h.baseURL, h.authTunnelHost, &basicAuth{username: "alice", password: "secret"}, http.StatusOK, backendResponse, 30*time.Second)
	})

	t.Run("TCP and UDP server_expose reach target client backends", func(t *testing.T) {
		serverTCP = h.createTCPServerExposeTunnel(t, "system-tcp-server", h.serverTCPPort, backendHost, backendPort)
		serverUDP = h.createUDPServerExposeTunnel(t, "system-udp-server", h.serverUDPPort, udpBackendHost, udpBackendPort)
		largeTCP = h.createTCPServerExposeTunnel(t, "system-tcp-large-echo", h.serverTCPAltPort, backendEchoHost, backendEchoPort)
		h.waitTunnelState(t, serverTCP.ID, "active", 90*time.Second)
		h.waitTunnelState(t, serverUDP.ID, "active", 90*time.Second)
		h.waitTunnelState(t, largeTCP.ID, "active", 90*time.Second)
		h.expectTunnelNoIssues(t, serverTCP.ID)
		h.expectTunnelNoIssues(t, serverUDP.ID)
		h.expectTunnelNoIssues(t, largeTCP.ID)
		h.expectTCPHTTPContains(t, h.serverTCPPort, backendHost, backendResponse)
		h.expectUDPEcho(t, h.serverUDPPort, []byte("system server udp probe"), 30*time.Second)
		h.expectLargeTCPUpload(t, h.serverTCPAltPort, 1<<20, 60*time.Second)
	})

	t.Run("SOCKS5 server_expose CONNECT reaches target client backend", func(t *testing.T) {
		serverSOCKS5 = h.createSOCKS5ServerExposeTunnel(t, "system-socks5-server", h.serverSOCKS5Port)
		h.waitTunnelState(t, serverSOCKS5.ID, "active", 90*time.Second)
		h.expectTunnelNoIssues(t, serverSOCKS5.ID)
		h.expectSOCKS5HTTPContains(t, h.serverSOCKS5Port, backendHost, backendPort, nil, backendResponse)
	})

	t.Run("client_to_client TCP UDP and multiple tunnel isolation", func(t *testing.T) {
		c2cTCP = h.createTCPClientToClientTunnel(t, "system-c2c-tcp", h.c2cTCPPort, backendHost, backendPort)
		c2cTCPAlt = h.createTCPClientToClientTunnel(t, "system-c2c-tcp-alt", h.c2cTCPAltPort, backendAltHost, backendAltPort)
		c2cTCPSlow = h.createTCPClientToClientTunnel(t, "system-c2c-tcp-slow", h.c2cTCPSlowPort, backendSlowHost, backendSlowPort)
		c2cUDP = h.createUDPClientToClientTunnel(t, "system-c2c-udp", h.c2cUDPPort, udpBackendHost, udpBackendPort)
		for _, tunnel := range []tunnelResponse{c2cTCP, c2cTCPAlt, c2cTCPSlow, c2cUDP} {
			h.waitTunnelState(t, tunnel.ID, "active", 90*time.Second)
			h.expectTunnelNoIssues(t, tunnel.ID)
		}

		h.expectTCPHTTPContains(t, h.c2cTCPPort, backendHost, backendResponse)
		h.expectTCPHTTPContains(t, h.c2cTCPAltPort, backendAltHost, backendAltResponse)
		h.expectUDPEcho(t, h.c2cUDPPort, []byte("system udp probe"), 30*time.Second)
		h.expectConcurrent(t, "same TCP tunnel concurrent streams", 16, func(_ int) error {
			return h.requestTCPHTTP(h.c2cTCPPort, backendHost, backendResponse, 8*time.Second)
		})
		h.expectConcurrent(t, "parallel TCP tunnels stay isolated", 12, func(i int) error {
			if i%2 == 0 {
				return h.requestTCPHTTP(h.c2cTCPPort, backendHost, backendResponse, 8*time.Second)
			}
			return h.requestTCPHTTP(h.c2cTCPAltPort, backendAltHost, backendAltResponse, 8*time.Second)
		})
		h.expectBoundedLatency(t, "fast c2c TCP request", 5, 5*time.Second, func() error {
			return h.requestTCPHTTP(h.c2cTCPPort, backendHost, backendResponse, 5*time.Second)
		})
		h.expectFastTunnelWhileSlowTunnelBusy(t)

		h.compose(t, h.composeEnv, "restart", "ingress-client")
		h.targetClientID, h.ingressClientID = h.waitForClientPair(t, 90*time.Second)
		h.waitTunnelState(t, c2cTCP.ID, "active", 90*time.Second)
		h.waitTunnelState(t, c2cUDP.ID, "active", 90*time.Second)
		h.expectTunnelNoIssues(t, c2cTCP.ID)
		h.expectTunnelNoIssues(t, c2cUDP.ID)
		h.expectTCPHTTPContains(t, h.c2cTCPPort, backendHost, backendResponse)
		h.expectUDPEcho(t, h.c2cUDPPort, []byte("system udp after ingress restart"), 30*time.Second)
	})

	t.Run("SOCKS5 client_to_client auth policy and target restart recovery", func(t *testing.T) {
		c2cSOCKS5 = h.createSOCKS5ClientToClientTunnel(t, "system-socks5-c2c", h.c2cSOCKS5Port, backendHost, backendPort, `{"type":"none"}`, []string{"0.0.0.0/0", "::/0"}, []string{backendHost}, []int{backendPort})
		h.waitTunnelState(t, c2cSOCKS5.ID, "active", 90*time.Second)
		h.expectTunnelNoIssues(t, c2cSOCKS5.ID)
		h.expectSOCKS5HTTPContains(t, h.c2cSOCKS5Port, backendHost, backendPort, nil, backendResponse)
		h.expectConcurrent(t, "same SOCKS5 tunnel concurrent CONNECT streams", 12, func(_ int) error {
			return h.requestSOCKS5HTTP(h.c2cSOCKS5Port, backendHost, backendPort, nil, backendResponse, 8*time.Second)
		})

		authTunnel := h.createSOCKS5ClientToClientTunnel(t, "system-socks5-auth", h.c2cSOCKS5AuthPort, backendHost, backendPort, `{"type":"username_password","username":"alice","password":"secret"}`, []string{"0.0.0.0/0", "::/0"}, []string{backendHost}, []int{backendPort})
		h.waitTunnelState(t, authTunnel.ID, "active", 90*time.Second)
		h.expectTunnelNoIssues(t, authTunnel.ID)
		h.expectSOCKS5NoAcceptableMethod(t, h.c2cSOCKS5AuthPort)
		h.expectSOCKS5AuthFailure(t, h.c2cSOCKS5AuthPort, "alice", "wrong")
		h.expectSOCKS5HTTPContains(t, h.c2cSOCKS5AuthPort, backendHost, backendPort, &socks5Credentials{username: "alice", password: "secret"}, backendResponse)

		targetDeny := h.createSOCKS5ClientToClientTunnel(t, "system-socks5-target-deny", h.c2cDenyPort, backendHost, backendPort, `{"type":"none"}`, []string{"0.0.0.0/0", "::/0"}, []string{"blocked.example"}, []int{backendPort})
		h.waitTunnelState(t, targetDeny.ID, "active", 90*time.Second)
		h.expectTunnelNoIssues(t, targetDeny.ID)
		rep := h.socks5ConnectReply(t, h.c2cDenyPort, backendHost, backendPort, nil)
		if rep != socks5wire.RepNotAllowed {
			t.Fatalf("SOCKS5 target policy deny: want REP %#x, got %#x", socks5wire.RepNotAllowed, rep)
		}

		sourceDeny := h.createSOCKS5ClientToClientTunnel(t, "system-socks5-source-deny", h.c2cSOCKS5SourceDenyPort, backendHost, backendPort, `{"type":"none"}`, []string{"192.0.2.0/24"}, []string{backendHost}, []int{backendPort})
		h.waitTunnelState(t, sourceDeny.ID, "active", 90*time.Second)
		h.expectTunnelNoIssues(t, sourceDeny.ID)
		h.expectSOCKS5SourceRejected(t, h.c2cSOCKS5SourceDenyPort)

		h.compose(t, h.composeEnv, "restart", "target-client")
		h.targetClientID, h.ingressClientID = h.waitForClientPair(t, 90*time.Second)
		h.waitTunnelState(t, c2cSOCKS5.ID, "active", 90*time.Second)
		h.expectTunnelNoIssues(t, c2cSOCKS5.ID)
		h.expectSOCKS5HTTPContains(t, h.c2cSOCKS5Port, backendHost, backendPort, nil, backendResponse)
	})

	t.Run("server restart restores persisted tunnels and data paths", func(t *testing.T) {
		requiredTunnels := []tunnelResponse{httpProxyTunnel, httpDirectTunnel, serverTCP, largeTCP, serverUDP, serverSOCKS5, c2cSOCKS5, c2cTCP, c2cUDP}
		for _, tunnel := range requiredTunnels {
			if tunnel.ID == "" {
				t.Fatalf("required persisted tunnel was not created before restart: %+v", tunnel)
			}
		}

		h.compose(t, h.composeEnv, "restart", "server")
		h.adminToken = h.waitForAdminToken(t, 90*time.Second)
		h.targetClientID, h.ingressClientID = h.waitForClientPair(t, 120*time.Second)
		for _, tunnel := range requiredTunnels {
			h.waitTunnelState(t, tunnel.ID, "active", 120*time.Second)
			h.expectTunnelNoIssues(t, tunnel.ID)
		}
		h.expectHTTPContainsAt(t, h.baseURL, h.tunnelHost, backendResponse, 90*time.Second)
		h.expectHTTPContainsAt(t, h.directBaseURL, h.directTunnelHost, backendResponse, 90*time.Second)
		h.expectTCPHTTPContains(t, h.serverTCPPort, backendHost, backendResponse)
		h.expectLargeTCPUpload(t, h.serverTCPAltPort, 1<<20, 60*time.Second)
		h.expectUDPEcho(t, h.serverUDPPort, []byte("system server udp after server restart"), 60*time.Second)
		h.expectSOCKS5HTTPContains(t, h.serverSOCKS5Port, backendHost, backendPort, nil, backendResponse)
		h.expectSOCKS5HTTPContains(t, h.c2cSOCKS5Port, backendHost, backendPort, nil, backendResponse)
		h.expectTCPHTTPContains(t, h.c2cTCPPort, backendHost, backendResponse)
		h.expectUDPEcho(t, h.c2cUDPPort, []byte("system udp after server restart"), 60*time.Second)
	})
}

func TestSystemSingleTargetClientE2E(t *testing.T) {
	h := newSystemHarness(t)
	h.startInfrastructure(t)
	h.expectAdminLoginRejected(t)
	h.adminToken = h.waitForAdminToken(t, 90*time.Second)
	h.expectAdminAPIAuthorization(t)
	clientKey := h.createAPIKey(t)
	h.startTargetClient(t, clientKey)
	h.targetClientID = h.waitForClientOnline(t, h.targetHostname, 90*time.Second)

	httpTunnel := h.createHTTPServerExposeTunnel(t, "single-target-http", h.tunnelHost, `{"type":"none"}`)
	serverTCP := h.createTCPServerExposeTunnel(t, "single-target-tcp", h.serverTCPPort, backendHost, backendPort)
	serverUDP := h.createUDPServerExposeTunnel(t, "single-target-udp", h.serverUDPPort, udpBackendHost, udpBackendPort)
	serverSOCKS5 := h.createSOCKS5ServerExposeTunnel(t, "single-target-socks5", h.serverSOCKS5Port)

	for _, tunnel := range []tunnelResponse{httpTunnel, serverTCP, serverUDP, serverSOCKS5} {
		h.waitTunnelState(t, tunnel.ID, "active", 90*time.Second)
		h.expectTunnelNoIssues(t, tunnel.ID)
	}

	h.expectHTTPContainsAt(t, h.baseURL, h.tunnelHost, backendResponse, 60*time.Second)
	h.expectTCPHTTPContains(t, h.serverTCPPort, backendHost, backendResponse)
	h.expectUDPEcho(t, h.serverUDPPort, []byte("single target udp probe"), 30*time.Second)
	h.expectSOCKS5HTTPContains(t, h.serverSOCKS5Port, backendHost, backendPort, nil, backendResponse)

	t.Run("unsupported target clean reject leaves no server listener", func(t *testing.T) {
		rejectPort := h.c2cTCPAltPort
		body := fmt.Sprintf(`{
			"name":"single-target-clean-reject",
			"topology":"server_expose",
			"ingress":{"location":"server","type":"tcp_listen","config":{
				"bind_ip":"0.0.0.0",
				"port":%d,
				"allowed_source_cidrs":["0.0.0.0/0","::/0"]
			}},
			"target":{"location":"client","client_id":%q,"type":"future_target","config":{"host":%q,"port":%d}},
			"transport_policy":"server_relay_only"
		}`, rejectPort, h.targetClientID, backendHost, backendPort)
		h.expectTunnelCreateRejected(t, body, http.StatusBadRequest, "unsupported_endpoint_type", "target.type")
		h.expectNoTunnelNamed(t, "single-target-clean-reject")
		h.expectServerListenerCount(t, "tcp", rejectPort, 0)
	})

	t.Run("unsupported server ingress clean reject leaves no server listener", func(t *testing.T) {
		rejectPort := h.c2cTCPSlowPort
		body := fmt.Sprintf(`{
			"name":"single-target-ingress-clean-reject",
			"topology":"server_expose",
			"ingress":{"location":"server","type":"future_ingress","config":{
				"bind_ip":"0.0.0.0",
				"port":%d,
				"allowed_source_cidrs":["0.0.0.0/0","::/0"]
			}},
			"target":{"location":"client","client_id":%q,"type":"tcp_service","config":{"host":%q,"port":%d}},
			"transport_policy":"server_relay_only"
		}`, rejectPort, h.targetClientID, backendHost, backendPort)
		h.expectTunnelCreateRejected(t, body, http.StatusBadRequest, "unsupported_endpoint_type", "ingress.type")
		h.expectNoTunnelNamed(t, "single-target-ingress-clean-reject")
		h.expectServerListenerCount(t, "tcp", rejectPort, 0)
	})
}

func TestSystemClientToClientCleanRejectE2E(t *testing.T) {
	h := newSystemHarness(t)
	h.startInfrastructure(t)
	h.expectAdminLoginRejected(t)
	h.adminToken = h.waitForAdminToken(t, 90*time.Second)
	h.expectAdminAPIAuthorization(t)
	clientKey := h.createAPIKey(t)
	h.startClients(t, clientKey)
	h.targetClientID, h.ingressClientID = h.waitForClientPair(t, 90*time.Second)

	t.Run("supported client_to_client TCP still works", func(t *testing.T) {
		tunnel := h.createTCPClientToClientTunnel(t, "c2c-clean-reject-happy-path", h.c2cTCPPort, backendHost, backendPort)
		h.waitTunnelState(t, tunnel.ID, "active", 90*time.Second)
		h.expectTunnelNoIssues(t, tunnel.ID)
		h.expectTCPHTTPContains(t, h.c2cTCPPort, backendHost, backendResponse)
	})

	t.Run("unsupported client ingress clean reject leaves no ingress listener", func(t *testing.T) {
		rejectPort := h.c2cTCPAltPort
		body := fmt.Sprintf(`{
			"name":"c2c-clean-reject",
			"topology":"client_to_client",
			"ingress":{"location":"client","client_id":%q,"type":"future_ingress","config":{
				"bind_ip":"0.0.0.0",
				"port":%d,
				"allowed_source_cidrs":["0.0.0.0/0","::/0"]
			}},
			"target":{"location":"client","client_id":%q,"type":"tcp_service","config":{"host":%q,"port":%d}},
			"transport_policy":"server_relay_only"
		}`, h.ingressClientID, rejectPort, h.targetClientID, backendHost, backendPort)
		h.expectTunnelCreateRejected(t, body, http.StatusBadRequest, "unsupported_endpoint_type", "ingress.type")
		h.expectNoTunnelNamed(t, "c2c-clean-reject")
		h.expectIngressListenerCount(t, "tcp", rejectPort, 0)
	})

	t.Run("unsupported client target type clean reject leaves no ingress listener", func(t *testing.T) {
		rejectPort := h.c2cDenyPort
		body := fmt.Sprintf(`{
			"name":"c2c-target-clean-reject",
			"topology":"client_to_client",
			"ingress":{"location":"client","client_id":%q,"type":"tcp_listen","config":{
				"bind_ip":"0.0.0.0",
				"port":%d,
				"allowed_source_cidrs":["0.0.0.0/0","::/0"]
			}},
			"target":{"location":"client","client_id":%q,"type":"future_target","config":{"host":%q,"port":%d}},
			"transport_policy":"server_relay_only"
		}`, h.ingressClientID, rejectPort, h.targetClientID, backendHost, backendPort)
		h.expectTunnelCreateRejected(t, body, http.StatusBadRequest, "unsupported_endpoint_type", "target.type")
		h.expectNoTunnelNamed(t, "c2c-target-clean-reject")
		h.expectIngressListenerCount(t, "tcp", rejectPort, 0)
	})
}

func TestSystemCapabilityLossReconcileE2E(t *testing.T) {
	h := newSystemHarness(t)
	lossImage := os.Getenv("NETSGO_E2E_CAPABILITY_LOSS_IMAGE")
	if lossImage == "" {
		t.Skip("NETSGO_E2E_CAPABILITY_LOSS_IMAGE is required")
	}
	h.startInfrastructure(t)
	h.adminToken = h.waitForAdminToken(t, 90*time.Second)
	clientKey := h.createAPIKey(t)
	h.startClients(t, clientKey)
	h.targetClientID, h.ingressClientID = h.waitForClientPair(t, 90*time.Second)

	tunnel := h.createTCPServerExposeTunnel(t, "capability-loss-server-tcp", h.serverTCPPort, backendHost, backendPort)
	h.waitTunnelState(t, tunnel.ID, "active", 90*time.Second)
	h.expectTunnelNoIssues(t, tunnel.ID)
	h.expectServerListenerCount(t, "tcp", h.serverTCPPort, 1)
	h.expectTCPHTTPContains(t, h.serverTCPPort, backendHost, backendResponse)

	env := append([]string{}, h.composeEnv...)
	env = append(env, "NETSGO_CLIENT_KEY="+clientKey)
	env = append(env, "NETSGO_TARGET_CLIENT_IMAGE="+lossImage)
	h.compose(t, env, "up", "-d", "--force-recreate", "--no-deps", "--no-build", "--remove-orphans", "target-client")
	h.targetClientID = h.waitForClientOnline(t, h.targetHostname, 90*time.Second)

	h.waitTunnelState(t, tunnel.ID, "error", 120*time.Second)
	h.expectTunnelIssue(t, tunnel.ID, "capability_not_supported", "target_client")
	h.expectServerListenerCount(t, "tcp", h.serverTCPPort, 0)
}

func newSystemHarness(t *testing.T) *systemHarness {
	t.Helper()
	filesRaw := getenvDefault("NETSGO_E2E_COMPOSE_FILES", "")
	if filesRaw == "" {
		t.Skip("NETSGO_E2E_COMPOSE_FILES is required for system E2E")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("docker CLI not found: %v", err)
	}
	adminPass := getenvDefault("NETSGO_ADMIN_PASS", "")
	if adminPass == "" {
		t.Fatal("NETSGO_ADMIN_PASS is required for system E2E")
	}
	proxyPort := getenvDefault("PROXY_PORT", "19080")
	upstreamPort := getenvDefault("UPSTREAM_PORT", "19081")
	h := &systemHarness{
		projectName:             getenvDefault("NETSGO_E2E_COMPOSE_PROJECT", "netsgo-system-e2e"),
		composeFiles:            splitCSV(filesRaw),
		managementHost:          getenvDefault("NETSGO_E2E_MANAGEMENT_HOST", defaultManagementHost),
		tunnelHost:              getenvDefault("NETSGO_E2E_TUNNEL_HOST", defaultTunnelHost),
		directTunnelHost:        getenvDefault("NETSGO_E2E_DIRECT_TUNNEL_HOST", "direct.system.local"),
		authTunnelHost:          getenvDefault("NETSGO_E2E_AUTH_TUNNEL_HOST", "auth.system.local"),
		adminUser:               getenvDefault("NETSGO_ADMIN_USER", defaultAdminUser),
		adminPass:               adminPass,
		targetHostname:          getenvDefault("NETSGO_TARGET_CLIENT_HOSTNAME", defaultTargetHostname),
		ingressHostname:         getenvDefault("NETSGO_INGRESS_CLIENT_HOSTNAME", defaultIngressHostname),
		serverTCPPort:           mustAtoi(t, getenvDefault("SERVER_TCP_PORT", "19093")),
		serverUDPPort:           mustAtoi(t, getenvDefault("SERVER_UDP_PORT", "19094")),
		serverSOCKS5Port:        mustAtoi(t, getenvDefault("SERVER_SOCKS5_PORT", "19095")),
		serverTCPAltPort:        mustAtoi(t, getenvDefault("SERVER_TCP_ALT_PORT", "19104")),
		c2cSOCKS5Port:           mustAtoi(t, getenvDefault("C2C_SOCKS5_PORT", "19096")),
		c2cDenyPort:             mustAtoi(t, getenvDefault("C2C_SOCKS5_DENY_PORT", "19097")),
		c2cTCPPort:              mustAtoi(t, getenvDefault("C2C_TCP_PORT", "19098")),
		c2cTCPAltPort:           mustAtoi(t, getenvDefault("C2C_TCP_ALT_PORT", "19099")),
		c2cTCPSlowPort:          mustAtoi(t, getenvDefault("C2C_TCP_SLOW_PORT", "19100")),
		c2cUDPPort:              mustAtoi(t, getenvDefault("C2C_UDP_PORT", "19101")),
		c2cSOCKS5AuthPort:       mustAtoi(t, getenvDefault("C2C_SOCKS5_AUTH_PORT", "19102")),
		c2cSOCKS5SourceDenyPort: mustAtoi(t, getenvDefault("C2C_SOCKS5_SOURCE_DENY_PORT", "19103")),
		baseURL:                 fmt.Sprintf("http://127.0.0.1:%s", proxyPort),
		directBaseURL:           fmt.Sprintf("http://127.0.0.1:%s", upstreamPort),
	}
	if len(h.composeFiles) == 0 {
		t.Skip("NETSGO_E2E_COMPOSE_FILES contains no files")
	}
	h.composeEnv = append(os.Environ(),
		"NETSGO_ADMIN_USER="+h.adminUser,
		"NETSGO_ADMIN_PASS="+h.adminPass,
		"NETSGO_SERVER_ADDR=http://"+h.managementHost,
		"NETSGO_TARGET_CLIENT_HOSTNAME="+h.targetHostname,
		"NETSGO_INGRESS_CLIENT_HOSTNAME="+h.ingressHostname,
	)
	t.Cleanup(func() {
		if t.Failed() {
			h.dumpCompose(t, "ps")
			h.dumpCompose(t, "logs", "--no-color", "--tail", "250")
		}
		if keep := getenvDefault("NETSGO_E2E_KEEP_STACK", ""); keep == "1" || strings.EqualFold(keep, "true") {
			t.Logf("keeping Compose stack %s", h.projectName)
			return
		}
		h.compose(t, h.composeEnv, "down", "-v", "--remove-orphans")
	})
	return h
}

func (h *systemHarness) startInfrastructure(t *testing.T) {
	t.Helper()
	h.compose(t, h.composeEnv, "down", "-v", "--remove-orphans")
	buildFlag := "--build"
	// Cross-version harnesses pass prebuilt images and must not let Compose
	// rebuild versioned image tags from the current checkout.
	if v := os.Getenv("NETSGO_E2E_COMPOSE_BUILD"); v == "0" || strings.EqualFold(v, "false") || strings.EqualFold(v, "no") {
		buildFlag = "--no-build"
	}
	h.compose(t, h.composeEnv, "up", "-d", buildFlag, "--remove-orphans", "server", "proxy", "tcp-backend", "tcp-backend-alt", "tcp-backend-slow", "tcp-backend-echo", "udp-backend")
}

func (h *systemHarness) startClients(t *testing.T, clientKey string) {
	t.Helper()
	env := append([]string{}, h.composeEnv...)
	env = append(env, "NETSGO_CLIENT_KEY="+clientKey)
	h.compose(t, env, "up", "-d", "--remove-orphans", "target-client", "ingress-client")
}

func (h *systemHarness) startTargetClient(t *testing.T, clientKey string) {
	t.Helper()
	env := append([]string{}, h.composeEnv...)
	env = append(env, "NETSGO_CLIENT_KEY="+clientKey)
	h.compose(t, env, "up", "-d", "--remove-orphans", "target-client")
}

func (h *systemHarness) compose(t *testing.T, env []string, args ...string) {
	t.Helper()
	cmdArgs := []string{"compose"}
	for _, file := range h.composeFiles {
		cmdArgs = append(cmdArgs, "-f", file)
	}
	cmdArgs = append(cmdArgs, "-p", h.projectName)
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("docker", cmdArgs...)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v failed: %v\n%s", cmdArgs, err, output)
	}
}

func (h *systemHarness) composeOutput(t *testing.T, env []string, args ...string) []byte {
	t.Helper()
	cmdArgs := []string{"compose"}
	for _, file := range h.composeFiles {
		cmdArgs = append(cmdArgs, "-f", file)
	}
	cmdArgs = append(cmdArgs, "-p", h.projectName)
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("docker", cmdArgs...)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v failed: %v\n%s", cmdArgs, err, output)
	}
	return output
}

func (h *systemHarness) dumpCompose(t *testing.T, args ...string) {
	t.Helper()
	cmdArgs := []string{"compose"}
	for _, file := range h.composeFiles {
		cmdArgs = append(cmdArgs, "-f", file)
	}
	cmdArgs = append(cmdArgs, "-p", h.projectName)
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("docker", cmdArgs...)
	cmd.Env = h.composeEnv
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("docker %v failed while dumping diagnostics: %v", cmdArgs, err)
	}
	if len(output) > 0 {
		t.Logf("docker %v output:\n%s", cmdArgs, output)
	}
}

func (h *systemHarness) waitForAdminToken(t *testing.T, timeout time.Duration) string {
	t.Helper()
	var token string
	h.poll(t, timeout, func() (bool, string) {
		payload := fmt.Sprintf(`{"username":%q,"password":%q}`, h.adminUser, h.adminPass)
		resp, err := h.apiRequest(http.MethodPost, "/api/auth/login", "", []byte(payload))
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return false, fmt.Sprintf("login status %d body=%s", resp.StatusCode, body)
		}
		var out struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return false, err.Error()
		}
		token = out.Token
		return token != "", "empty token"
	})
	return token
}

func (h *systemHarness) expectAdminLoginRejected(t *testing.T) {
	t.Helper()
	h.poll(t, 90*time.Second, func() (bool, string) {
		payload := fmt.Sprintf(`{"username":%q,"password":"definitely-wrong"}`, h.adminUser)
		resp, err := h.apiRequest(http.MethodPost, "/api/auth/login", "", []byte(payload))
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			body, _ := io.ReadAll(resp.Body)
			return false, fmt.Sprintf("wrong admin login: want 401, got %d body=%s", resp.StatusCode, body)
		}
		return true, ""
	})
}

func (h *systemHarness) expectAdminAPIAuthorization(t *testing.T) {
	t.Helper()
	resp, err := h.apiRequest(http.MethodGet, "/api/clients", "", nil)
	if err != nil {
		t.Fatalf("unauthenticated admin API request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unauthenticated admin API: want 401, got %d body=%s", resp.StatusCode, body)
	}
}

func (h *systemHarness) createAPIKey(t *testing.T) string {
	t.Helper()
	body := []byte(`{"name":"system-e2e","permissions":["connect"]}`)
	resp, err := h.apiRequest(http.MethodPost, "/api/admin/keys", h.adminToken, body)
	if err != nil {
		t.Fatalf("create API key: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("create API key: want 201, got %d body=%s", resp.StatusCode, payload)
	}
	var out apiKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode API key response: %v", err)
	}
	if out.RawKey == "" {
		t.Fatal("API key response missing raw_key")
	}
	return out.RawKey
}

func (h *systemHarness) waitForClientPair(t *testing.T, timeout time.Duration) (targetID, ingressID string) {
	t.Helper()
	h.poll(t, timeout, func() (bool, string) {
		clients := h.listClients(t)
		for _, client := range clients {
			if !client.Online {
				continue
			}
			switch client.Info.Hostname {
			case h.targetHostname:
				targetID = client.ID
			case h.ingressHostname:
				ingressID = client.ID
			}
		}
		if targetID != "" && ingressID != "" {
			return true, ""
		}
		return false, fmt.Sprintf("target=%q ingress=%q", targetID, ingressID)
	})
	return targetID, ingressID
}

func (h *systemHarness) waitForClientOnline(t *testing.T, hostname string, timeout time.Duration) string {
	t.Helper()
	var clientID string
	h.poll(t, timeout, func() (bool, string) {
		clients := h.listClients(t)
		for _, client := range clients {
			if client.Online && client.Info.Hostname == hostname {
				clientID = client.ID
				return true, ""
			}
		}
		return false, fmt.Sprintf("client %q not online", hostname)
	})
	return clientID
}

func (h *systemHarness) listClients(t *testing.T) []apiClient {
	t.Helper()
	resp, err := h.apiRequest(http.MethodGet, "/api/clients", h.adminToken, nil)
	if err != nil {
		t.Fatalf("list clients: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("list clients: want 200, got %d body=%s", resp.StatusCode, payload)
	}
	var clients []apiClient
	if err := json.NewDecoder(resp.Body).Decode(&clients); err != nil {
		t.Fatalf("decode clients: %v", err)
	}
	return clients
}

func (h *systemHarness) createTunnel(t *testing.T, body string) tunnelResponse {
	t.Helper()
	resp, err := h.apiRequest(http.MethodPost, "/api/tunnels", h.adminToken, []byte(body))
	if err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("create tunnel: want 201, got %d body=%s", resp.StatusCode, payload)
	}
	var tunnel tunnelResponse
	if err := json.NewDecoder(resp.Body).Decode(&tunnel); err != nil {
		t.Fatalf("decode tunnel create response: %v", err)
	}
	if tunnel.ID == "" {
		t.Fatalf("create tunnel response missing id: %+v", tunnel)
	}
	return tunnel
}

func (h *systemHarness) expectTunnelCreateRejected(t *testing.T, body string, wantStatus int, wantCode, wantField string) {
	t.Helper()
	resp, err := h.apiRequest(http.MethodPost, "/api/tunnels", h.adminToken, []byte(body))
	if err != nil {
		t.Fatalf("create rejected tunnel request: %v", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("create rejected tunnel: want status %d, got %d body=%s", wantStatus, resp.StatusCode, payload)
	}
	var got struct {
		Code      string `json:"code"`
		ErrorCode string `json:"error_code"`
		Field     string `json:"field"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode rejected tunnel response: %v body=%s", err, payload)
	}
	if got.Code != wantCode && got.ErrorCode != wantCode {
		t.Fatalf("rejected tunnel code: want %q, got %+v body=%s", wantCode, got, payload)
	}
	if got.Field != wantField {
		t.Fatalf("rejected tunnel field: want %q, got %+v body=%s", wantField, got, payload)
	}
}

func (h *systemHarness) expectNoTunnelNamed(t *testing.T, name string) {
	t.Helper()
	resp, err := h.apiRequest(http.MethodGet, "/api/tunnels", h.adminToken, nil)
	if err != nil {
		t.Fatalf("list tunnels: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("list tunnels: want 200, got %d body=%s", resp.StatusCode, payload)
	}
	var tunnels []tunnelResponse
	if err := json.NewDecoder(resp.Body).Decode(&tunnels); err != nil {
		t.Fatalf("decode tunnel list: %v", err)
	}
	for _, tunnel := range tunnels {
		if tunnel.Name == name {
			t.Fatalf("rejected tunnel %q must not be persisted: %+v", name, tunnel)
		}
	}
}

func (h *systemHarness) waitTunnelState(t *testing.T, id, state string, timeout time.Duration) {
	t.Helper()
	var last tunnelResponse
	h.poll(t, timeout, func() (bool, string) {
		resp, err := h.apiRequest(http.MethodGet, "/api/tunnels/"+id, h.adminToken, nil)
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			payload, _ := io.ReadAll(resp.Body)
			return false, fmt.Sprintf("GET tunnel status %d body=%s", resp.StatusCode, payload)
		}
		if err := json.NewDecoder(resp.Body).Decode(&last); err != nil {
			return false, err.Error()
		}
		if last.RuntimeState == state {
			return true, ""
		}
		return false, fmt.Sprintf("runtime_state=%q issues=%s", last.RuntimeState, last.Issues)
	})
}

func (h *systemHarness) expectServerListenerCount(t *testing.T, proto string, port int, want int) {
	t.Helper()
	h.expectServiceListenerCount(t, "server", proto, port, want)
}

func (h *systemHarness) expectIngressListenerCount(t *testing.T, proto string, port int, want int) {
	t.Helper()
	h.expectServiceListenerCount(t, "ingress-client", proto, port, want)
}

func (h *systemHarness) expectServiceListenerCount(t *testing.T, service string, proto string, port int, want int) {
	t.Helper()
	container := strings.TrimSpace(string(h.composeOutput(t, h.composeEnv, "ps", "-q", service)))
	if container == "" {
		t.Fatalf("%s container not found", service)
	}
	cmd := "netstat -ltn 2>/dev/null || netstat -ln 2>/dev/null"
	awkProto := "^tcp"
	if proto == "udp" {
		cmd = "netstat -lun 2>/dev/null || netstat -ln 2>/dev/null"
		awkProto = "^udp"
	}
	output, err := exec.Command("docker", "exec", container, "sh", "-c", cmd).CombinedOutput()
	if err != nil {
		t.Fatalf("docker exec netstat failed: %v\n%s", err, output)
	}
	count := 0
	portSuffix := ":" + strconv.Itoa(port)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.HasPrefix(fields[0], strings.TrimPrefix(awkProto, "^")) {
			continue
		}
		if strings.HasSuffix(fields[3], portSuffix) {
			count++
		}
	}
	if count != want {
		t.Fatalf("%s listener count on %s:%d: got %d want %d\n%s", proto, service, port, count, want, output)
	}
}

func (h *systemHarness) expectTunnelNoIssues(t *testing.T, id string) {
	t.Helper()
	resp, err := h.apiRequest(http.MethodGet, "/api/tunnels/"+id, h.adminToken, nil)
	if err != nil {
		t.Fatalf("get tunnel %s: %v", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET tunnel %s: want 200, got %d body=%s", id, resp.StatusCode, payload)
	}
	var tunnel tunnelResponse
	if err := json.NewDecoder(resp.Body).Decode(&tunnel); err != nil {
		t.Fatalf("decode tunnel %s: %v", id, err)
	}
	var issues []json.RawMessage
	if len(tunnel.Issues) > 0 {
		if err := json.Unmarshal(tunnel.Issues, &issues); err != nil {
			t.Fatalf("decode tunnel issues for %s: %v raw=%s", id, err, tunnel.Issues)
		}
	}
	if len(issues) != 0 {
		t.Fatalf("tunnel %s has issues: %s", id, tunnel.Issues)
	}
}

func (h *systemHarness) expectTunnelIssue(t *testing.T, id, wantCode, wantScope string) {
	t.Helper()
	resp, err := h.apiRequest(http.MethodGet, "/api/tunnels/"+id, h.adminToken, nil)
	if err != nil {
		t.Fatalf("get tunnel %s: %v", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET tunnel %s: want 200, got %d body=%s", id, resp.StatusCode, payload)
	}
	var tunnel tunnelResponse
	if err := json.NewDecoder(resp.Body).Decode(&tunnel); err != nil {
		t.Fatalf("decode tunnel %s: %v", id, err)
	}
	var issues []tunnelIssueResponse
	if err := json.Unmarshal(tunnel.Issues, &issues); err != nil {
		t.Fatalf("decode tunnel issues for %s: %v raw=%s", id, err, tunnel.Issues)
	}
	for _, issue := range issues {
		if issue.Code == wantCode && issue.Scope == wantScope {
			return
		}
	}
	t.Fatalf("tunnel %s missing issue code=%q scope=%q: %s", id, wantCode, wantScope, tunnel.Issues)
}

func (h *systemHarness) createHTTPServerExposeTunnel(t *testing.T, name, host, authJSON string) tunnelResponse {
	t.Helper()
	return h.createTunnel(t, fmt.Sprintf(`{
		"name":%q,
		"topology":"server_expose",
		"ingress":{"location":"server","type":"http_host","config":{
			"domain":%q,
			"allowed_source_cidrs":["0.0.0.0/0","::/0"],
			"auth":%s
		}},
		"target":{"location":"client","client_id":%q,"type":"tcp_service","config":{"host":%q,"port":%d}},
		"transport_policy":"server_relay_only"
	}`, name, host, authJSON, h.targetClientID, backendHost, backendPort))
}

func (h *systemHarness) createTCPServerExposeTunnel(t *testing.T, name string, ingressPort int, targetHost string, targetPort int) tunnelResponse {
	t.Helper()
	return h.createTunnel(t, fmt.Sprintf(`{
		"name":%q,
		"topology":"server_expose",
		"ingress":{"location":"server","type":"tcp_listen","config":{
			"bind_ip":"0.0.0.0",
			"port":%d,
			"allowed_source_cidrs":["0.0.0.0/0","::/0"]
		}},
		"target":{"location":"client","client_id":%q,"type":"tcp_service","config":{"host":%q,"port":%d}},
		"transport_policy":"server_relay_only"
	}`, name, ingressPort, h.targetClientID, targetHost, targetPort))
}

func (h *systemHarness) createUDPServerExposeTunnel(t *testing.T, name string, ingressPort int, targetHost string, targetPort int) tunnelResponse {
	t.Helper()
	return h.createTunnel(t, fmt.Sprintf(`{
		"name":%q,
		"topology":"server_expose",
		"ingress":{"location":"server","type":"udp_listen","config":{
			"bind_ip":"0.0.0.0",
			"port":%d,
			"allowed_source_cidrs":["0.0.0.0/0","::/0"]
		}},
		"target":{"location":"client","client_id":%q,"type":"udp_service","config":{"host":%q,"port":%d}},
		"transport_policy":"server_relay_only"
	}`, name, ingressPort, h.targetClientID, targetHost, targetPort))
}

func (h *systemHarness) createSOCKS5ServerExposeTunnel(t *testing.T, name string, port int) tunnelResponse {
	t.Helper()
	return h.createTunnel(t, fmt.Sprintf(`{
		"name":%q,
		"topology":"server_expose",
		"ingress":{"location":"server","type":"socks5_listen","config":{
			"bind_ip":"0.0.0.0",
			"port":%d,
			"allowed_source_cidrs":["0.0.0.0/0","::/0"],
			"auth":{"type":"none"}
		}},
		"target":{"location":"client","client_id":%q,"type":"socks5_connect_handler","config":{
			"allowed_target_cidrs":["0.0.0.0/0","::/0"],
			"allowed_target_hosts":[%q],
			"allowed_target_ports":[%d],
			"dial_timeout_seconds":5
		}},
		"transport_policy":"server_relay_only",
		"confirm_no_auth_risk":true
	}`, name, port, h.targetClientID, backendHost, backendPort))
}

func (h *systemHarness) createTCPClientToClientTunnel(t *testing.T, name string, ingressPort int, targetHost string, targetPort int) tunnelResponse {
	t.Helper()
	return h.createTunnel(t, fmt.Sprintf(`{
		"name":%q,
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":%q,"type":"tcp_listen","config":{
			"bind_ip":"0.0.0.0",
			"port":%d,
			"allowed_source_cidrs":["0.0.0.0/0","::/0"]
		}},
		"target":{"location":"client","client_id":%q,"type":"tcp_service","config":{"host":%q,"port":%d}},
		"transport_policy":"server_relay_only"
	}`, name, h.ingressClientID, ingressPort, h.targetClientID, targetHost, targetPort))
}

func (h *systemHarness) createUDPClientToClientTunnel(t *testing.T, name string, ingressPort int, targetHost string, targetPort int) tunnelResponse {
	t.Helper()
	return h.createTunnel(t, fmt.Sprintf(`{
		"name":%q,
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":%q,"type":"udp_listen","config":{
			"bind_ip":"0.0.0.0",
			"port":%d,
			"allowed_source_cidrs":["0.0.0.0/0","::/0"]
		}},
		"target":{"location":"client","client_id":%q,"type":"udp_service","config":{"host":%q,"port":%d}},
		"transport_policy":"server_relay_only"
	}`, name, h.ingressClientID, ingressPort, h.targetClientID, targetHost, targetPort))
}

func (h *systemHarness) createSOCKS5ClientToClientTunnel(t *testing.T, name string, ingressPort int, targetHost string, targetPort int, authJSON string, sourceCIDRs []string, allowedHosts []string, allowedPorts []int) tunnelResponse {
	t.Helper()
	return h.createTunnel(t, fmt.Sprintf(`{
		"name":%q,
		"topology":"client_to_client",
		"ingress":{"location":"client","client_id":%q,"type":"socks5_listen","config":{
			"bind_ip":"0.0.0.0",
			"port":%d,
			"allowed_source_cidrs":%s,
			"auth":%s
		}},
		"target":{"location":"client","client_id":%q,"type":"socks5_connect_handler","config":{
			"allowed_target_cidrs":["0.0.0.0/0","::/0"],
			"allowed_target_hosts":%s,
			"allowed_target_ports":%s,
			"dial_timeout_seconds":5
		}},
		"transport_policy":"server_relay_only"
	}`, name, h.ingressClientID, ingressPort, mustJSON(t, sourceCIDRs), authJSON, h.targetClientID, mustJSON(t, allowedHosts), mustJSON(t, allowedPorts)))
}

func (h *systemHarness) expectHTTPContains(t *testing.T, host, expected string, timeout time.Duration) {
	t.Helper()
	h.expectHTTPContainsAt(t, h.baseURL, host, expected, timeout)
}

func (h *systemHarness) expectHTTPContainsAt(t *testing.T, baseURL, host, expected string, timeout time.Duration) {
	t.Helper()
	h.poll(t, timeout, func() (bool, string) {
		req, err := http.NewRequest(http.MethodGet, baseURL+"/", nil)
		if err != nil {
			return false, err.Error()
		}
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		payload, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Sprintf("status=%d body=%s", resp.StatusCode, payload)
		}
		return bytes.Contains(payload, []byte(expected)), string(payload)
	})
}

type basicAuth struct {
	username string
	password string
}

func (h *systemHarness) expectHTTPStatusAt(t *testing.T, baseURL, host string, auth *basicAuth, wantStatus int, wantBody string, timeout time.Duration) {
	t.Helper()
	h.poll(t, timeout, func() (bool, string) {
		resp, payload, err := h.doHTTPHostRequest(baseURL, host, auth)
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		if resp.StatusCode != wantStatus {
			return false, fmt.Sprintf("status=%d body=%s", resp.StatusCode, payload)
		}
		if wantBody != "" && !bytes.Contains(payload, []byte(wantBody)) {
			return false, string(payload)
		}
		return true, ""
	})
}

func (h *systemHarness) doHTTPHostRequest(baseURL, host string, auth *basicAuth) (*http.Response, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/", nil)
	if err != nil {
		return nil, nil, err
	}
	req.Host = host
	if auth != nil {
		req.SetBasicAuth(auth.username, auth.password)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	payload, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		resp.Body.Close()
		return nil, nil, readErr
	}
	resp.Body = io.NopCloser(bytes.NewReader(payload))
	return resp, payload, nil
}

func (h *systemHarness) expectTCPHTTPContains(t *testing.T, port int, host, expected string) {
	t.Helper()
	h.poll(t, 30*time.Second, func() (bool, string) {
		if err := h.requestTCPHTTP(port, host, expected, 5*time.Second); err != nil {
			return false, err.Error()
		}
		return true, ""
	})
}

func (h *systemHarness) requestTCPHTTP(port int, host, expected string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial TCP ingress port %d: %w", port, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("set TCP ingress deadline: %w", err)
	}
	req := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", host)
	if _, err := io.WriteString(conn, req); err != nil {
		return fmt.Errorf("write HTTP through TCP tunnel: %w", err)
	}
	payload, err := io.ReadAll(conn)
	if err != nil {
		return fmt.Errorf("read HTTP through TCP tunnel: %w", err)
	}
	if !bytes.Contains(payload, []byte(expected)) {
		return fmt.Errorf("TCP tunnel response missing %q:\n%s", expected, payload)
	}
	return nil
}

func (h *systemHarness) expectLargeTCPUpload(t *testing.T, port, size int, timeout time.Duration) {
	t.Helper()
	payload := deterministicPayload(size)
	h.poll(t, timeout, func() (bool, string) {
		got, err := h.requestTCPUploadAck(port, payload, timeout)
		if err != nil {
			return false, err.Error()
		}
		if string(got) != "NETSGO_LARGE_OK" {
			return false, fmt.Sprintf("large TCP upload ack mismatch: got %q", got)
		}
		return true, ""
	})
}

func deterministicPayload(size int) []byte {
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte((i * 7) % 251)
	}
	return payload
}

func (h *systemHarness) requestTCPUploadAck(port int, payload []byte, timeout time.Duration) ([]byte, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial TCP upload ingress port %d: %w", port, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, fmt.Errorf("set TCP upload deadline: %w", err)
	}

	written, err := io.Copy(conn, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("write large TCP upload payload after %d/%d bytes: %w", written, len(payload), err)
	}
	if written != int64(len(payload)) {
		return nil, fmt.Errorf("short large TCP upload write: wrote %d/%d bytes", written, len(payload))
	}

	got := make([]byte, len("NETSGO_LARGE_OK"))
	n, err := io.ReadFull(conn, got)
	if err != nil {
		return nil, fmt.Errorf("read large TCP upload ack after %d/%d bytes: %w", n, len(got), err)
	}
	return got, nil
}

func (h *systemHarness) expectUDPEcho(t *testing.T, port int, payload []byte, timeout time.Duration) {
	t.Helper()
	h.poll(t, timeout, func() (bool, string) {
		got, err := h.requestUDP(port, payload, 5*time.Second)
		if err != nil {
			return false, err.Error()
		}
		if !bytes.Equal(got, payload) {
			return false, fmt.Sprintf("got %q want %q", got, payload)
		}
		return true, ""
	})
}

func (h *systemHarness) requestUDP(port int, payload []byte, timeout time.Duration) ([]byte, error) {
	conn, err := net.DialTimeout("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial UDP ingress port %d: %w", port, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, fmt.Errorf("set UDP ingress deadline: %w", err)
	}
	if _, err := conn.Write(payload); err != nil {
		return nil, fmt.Errorf("write UDP tunnel payload: %w", err)
	}
	got := make([]byte, 2048)
	n, err := conn.Read(got)
	if err != nil {
		return nil, fmt.Errorf("read UDP tunnel response: %w", err)
	}
	return got[:n], nil
}

func (h *systemHarness) expectConcurrent(t *testing.T, name string, count int, fn func(int) error) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, count)
	start := make(chan struct{})
	for i := 0; i < count; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := fn(i); err != nil {
				errs <- fmt.Errorf("%s worker %d: %w", name, i, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		t.Fatalf("%s failed", name)
	}
}

func (h *systemHarness) expectBoundedLatency(t *testing.T, name string, count int, max time.Duration, fn func() error) {
	t.Helper()
	var slowest time.Duration
	for i := 0; i < count; i++ {
		started := time.Now()
		if err := fn(); err != nil {
			t.Fatalf("%s iteration %d failed: %v", name, i, err)
		}
		elapsed := time.Since(started)
		if elapsed > slowest {
			slowest = elapsed
		}
		if elapsed > max {
			t.Fatalf("%s iteration %d took %s, want <= %s", name, i, elapsed, max)
		}
	}
	t.Logf("%s slowest successful request: %s", name, slowest)
}

func (h *systemHarness) expectFastTunnelWhileSlowTunnelBusy(t *testing.T) {
	t.Helper()
	slowErrs := make(chan error, 4)
	for i := 0; i < cap(slowErrs); i++ {
		go func() {
			slowErrs <- h.holdTCPConnection(h.c2cTCPSlowPort, 2*time.Second)
		}()
	}
	time.Sleep(250 * time.Millisecond)
	started := time.Now()
	if err := h.requestTCPHTTP(h.c2cTCPPort, backendHost, backendResponse, 3*time.Second); err != nil {
		t.Fatalf("fast tunnel failed while slow tunnel was busy: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("fast tunnel was delayed by slow tunnel load: took %s", elapsed)
	}
	for i := 0; i < cap(slowErrs); i++ {
		if err := <-slowErrs; err != nil {
			t.Fatalf("slow tunnel hold failed: %v", err)
		}
	}
}

func (h *systemHarness) holdTCPConnection(port int, duration time.Duration) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial slow TCP ingress port %d: %w", port, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(duration + 3*time.Second)); err != nil {
		return fmt.Errorf("set slow TCP ingress deadline: %w", err)
	}
	if _, err := io.WriteString(conn, "hold slow tunnel open\n"); err != nil {
		return fmt.Errorf("write slow TCP tunnel payload: %w", err)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	<-timer.C
	return nil
}

func (h *systemHarness) expectSOCKS5HTTPContains(t *testing.T, proxyPort int, targetHost string, targetPort int, creds *socks5Credentials, expected string) {
	t.Helper()
	h.poll(t, 30*time.Second, func() (bool, string) {
		if err := h.requestSOCKS5HTTP(proxyPort, targetHost, targetPort, creds, expected, 5*time.Second); err != nil {
			return false, err.Error()
		}
		return true, ""
	})
}

func (h *systemHarness) requestSOCKS5HTTP(proxyPort int, targetHost string, targetPort int, creds *socks5Credentials, expected string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxyPort)), 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial SOCKS5 proxy port %d: %w", proxyPort, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("set SOCKS5 deadline: %w", err)
	}
	if rep, err := socks5HandshakeAndConnect(conn, targetHost, targetPort, creds); err != nil {
		return err
	} else if rep != socks5wire.RepSuccess {
		return fmt.Errorf("SOCKS5 CONNECT: want success, got REP %#x", rep)
	}
	req := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", targetHost)
	if _, err := io.WriteString(conn, req); err != nil {
		return fmt.Errorf("write HTTP through SOCKS5: %w", err)
	}
	payload, err := io.ReadAll(conn)
	if err != nil {
		return fmt.Errorf("read HTTP through SOCKS5: %w", err)
	}
	if !bytes.Contains(payload, []byte(expected)) {
		return fmt.Errorf("SOCKS5 HTTP response missing %q:\n%s", expected, payload)
	}
	return nil
}

func (h *systemHarness) socks5ConnectReply(t *testing.T, proxyPort int, targetHost string, targetPort int, creds *socks5Credentials) byte {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxyPort)), 5*time.Second)
	if err != nil {
		t.Fatalf("dial SOCKS5 proxy port %d: %v", proxyPort, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("set SOCKS5 deadline: %v", err)
	}
	rep, err := socks5HandshakeAndConnect(conn, targetHost, targetPort, creds)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func (h *systemHarness) expectSOCKS5NoAcceptableMethod(t *testing.T, proxyPort int) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxyPort)), 5*time.Second)
	if err != nil {
		t.Fatalf("dial SOCKS5 auth proxy port %d: %v", proxyPort, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set SOCKS5 auth deadline: %v", err)
	}
	if _, err := conn.Write([]byte{socks5wire.Version, 0x01, socks5wire.MethodNoAuth}); err != nil {
		t.Fatalf("write SOCKS5 no-auth method: %v", err)
	}
	var resp [2]byte
	if _, err := io.ReadFull(conn, resp[:]); err != nil {
		t.Fatalf("read SOCKS5 no-acceptable-method response: %v", err)
	}
	if resp != [2]byte{socks5wire.Version, socks5wire.MethodNoAcceptable} {
		t.Fatalf("SOCKS5 no-auth method should be rejected, got %#v", resp)
	}
}

func (h *systemHarness) expectSOCKS5AuthFailure(t *testing.T, proxyPort int, username, password string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxyPort)), 5*time.Second)
	if err != nil {
		t.Fatalf("dial SOCKS5 auth proxy port %d: %v", proxyPort, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set SOCKS5 auth deadline: %v", err)
	}
	if _, err := conn.Write([]byte{socks5wire.Version, 0x01, socks5wire.MethodUsernamePass}); err != nil {
		t.Fatalf("write SOCKS5 username/password method: %v", err)
	}
	var methodResp [2]byte
	if _, err := io.ReadFull(conn, methodResp[:]); err != nil {
		t.Fatalf("read SOCKS5 username/password method response: %v", err)
	}
	if methodResp != [2]byte{socks5wire.Version, socks5wire.MethodUsernamePass} {
		t.Fatalf("SOCKS5 username/password method response: got %#v", methodResp)
	}
	req := []byte{socks5wire.AuthVersion, byte(len(username))}
	req = append(req, []byte(username)...)
	req = append(req, byte(len(password)))
	req = append(req, []byte(password)...)
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write SOCKS5 wrong credentials: %v", err)
	}
	var authResp [2]byte
	if _, err := io.ReadFull(conn, authResp[:]); err != nil {
		t.Fatalf("read SOCKS5 auth failure response: %v", err)
	}
	if authResp[0] != socks5wire.AuthVersion || authResp[1] == 0x00 {
		t.Fatalf("SOCKS5 wrong credentials should fail, got %#v", authResp)
	}
}

func (h *systemHarness) expectSOCKS5SourceRejected(t *testing.T, proxyPort int) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxyPort)), 5*time.Second)
	if err != nil {
		t.Fatalf("dial SOCKS5 source-deny proxy port %d: %v", proxyPort, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set SOCKS5 source-deny deadline: %v", err)
	}
	if _, err := conn.Write([]byte{socks5wire.Version, 0x01, socks5wire.MethodNoAuth}); err != nil {
		t.Fatalf("write SOCKS5 source-deny methods: %v", err)
	}
	var resp [2]byte
	_, err = io.ReadFull(conn, resp[:])
	if err == nil {
		t.Fatalf("SOCKS5 source CIDR denial should close before negotiation, got response %#v", resp)
	}
}

type socks5Credentials struct {
	username string
	password string
}

func socks5HandshakeAndConnect(conn net.Conn, targetHost string, targetPort int, creds *socks5Credentials) (byte, error) {
	requestedMethod := socks5wire.MethodNoAuth
	if creds != nil {
		requestedMethod = socks5wire.MethodUsernamePass
	}
	if _, err := conn.Write([]byte{socks5wire.Version, 0x01, requestedMethod}); err != nil {
		return 0, fmt.Errorf("write SOCKS5 methods: %w", err)
	}
	var selected [2]byte
	if _, err := io.ReadFull(conn, selected[:]); err != nil {
		return 0, fmt.Errorf("read SOCKS5 method response: %w", err)
	}
	if selected[0] != socks5wire.Version {
		return 0, fmt.Errorf("SOCKS5 method response version: got %#v", selected)
	}
	if selected[1] == socks5wire.MethodNoAcceptable {
		return 0, fmt.Errorf("SOCKS5 method negotiation failed: no acceptable method")
	}
	if selected[1] != requestedMethod {
		return 0, fmt.Errorf("SOCKS5 method response: want %#x, got %#x", requestedMethod, selected[1])
	}
	if selected[1] == socks5wire.MethodUsernamePass {
		if err := writeSOCKS5UserPassAuth(conn, creds); err != nil {
			return 0, err
		}
	}
	req, err := buildSOCKS5ConnectRequest(targetHost, targetPort)
	if err != nil {
		return 0, err
	}
	if _, err := conn.Write(req); err != nil {
		return 0, fmt.Errorf("write SOCKS5 CONNECT: %w", err)
	}
	rep, err := readSOCKS5Reply(conn)
	if err != nil {
		return 0, err
	}
	return rep, nil
}

func writeSOCKS5UserPassAuth(conn net.Conn, creds *socks5Credentials) error {
	if creds == nil {
		return errors.New("SOCKS5 credentials are required for username/password auth")
	}
	username := []byte(creds.username)
	password := []byte(creds.password)
	if len(username) == 0 || len(username) > 255 || len(password) > 255 {
		return fmt.Errorf("invalid SOCKS5 username/password lengths: username=%d password=%d", len(username), len(password))
	}
	req := []byte{socks5wire.AuthVersion, byte(len(username))}
	req = append(req, username...)
	req = append(req, byte(len(password)))
	req = append(req, password...)
	if _, err := conn.Write(req); err != nil {
		return fmt.Errorf("write SOCKS5 username/password auth: %w", err)
	}
	var resp [2]byte
	if _, err := io.ReadFull(conn, resp[:]); err != nil {
		return fmt.Errorf("read SOCKS5 username/password auth response: %w", err)
	}
	if resp[0] != socks5wire.AuthVersion {
		return fmt.Errorf("SOCKS5 username/password auth version: got %#x", resp[0])
	}
	if resp[1] != 0x00 {
		return fmt.Errorf("SOCKS5 username/password auth failed with status %#x", resp[1])
	}
	return nil
}

func buildSOCKS5ConnectRequest(targetHost string, targetPort int) ([]byte, error) {
	if targetPort < 1 || targetPort > 65535 {
		return nil, fmt.Errorf("invalid SOCKS5 target port %d", targetPort)
	}
	if ip := net.ParseIP(targetHost); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			req := []byte{socks5wire.Version, socks5wire.CommandConnect, 0x00, socks5wire.AddrIPv4, ip4[0], ip4[1], ip4[2], ip4[3], 0, 0}
			binary.BigEndian.PutUint16(req[8:10], uint16(targetPort))
			return req, nil
		}
		ip16 := ip.To16()
		req := make([]byte, 4+16+2)
		req[0], req[1], req[2], req[3] = socks5wire.Version, socks5wire.CommandConnect, 0x00, socks5wire.AddrIPv6
		copy(req[4:20], ip16)
		binary.BigEndian.PutUint16(req[20:22], uint16(targetPort))
		return req, nil
	}
	if len(targetHost) == 0 || len(targetHost) > 255 {
		return nil, fmt.Errorf("invalid SOCKS5 target host %q", targetHost)
	}
	req := []byte{socks5wire.Version, socks5wire.CommandConnect, 0x00, socks5wire.AddrDomain, byte(len(targetHost))}
	req = append(req, targetHost...)
	req = append(req, 0, 0)
	binary.BigEndian.PutUint16(req[len(req)-2:], uint16(targetPort))
	return req, nil
}

func readSOCKS5Reply(conn net.Conn) (byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return 0, fmt.Errorf("read SOCKS5 reply header: %w", err)
	}
	if header[0] != socks5wire.Version || header[2] != 0x00 {
		return 0, fmt.Errorf("invalid SOCKS5 reply header: %#v", header)
	}
	switch header[3] {
	case socks5wire.AddrIPv4:
		var rest [6]byte
		if _, err := io.ReadFull(conn, rest[:]); err != nil {
			return 0, fmt.Errorf("read SOCKS5 IPv4 reply body: %w", err)
		}
	case socks5wire.AddrIPv6:
		var rest [18]byte
		if _, err := io.ReadFull(conn, rest[:]); err != nil {
			return 0, fmt.Errorf("read SOCKS5 IPv6 reply body: %w", err)
		}
	case socks5wire.AddrDomain:
		var length [1]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			return 0, fmt.Errorf("read SOCKS5 domain reply length: %w", err)
		}
		rest := make([]byte, int(length[0])+2)
		if _, err := io.ReadFull(conn, rest); err != nil {
			return 0, fmt.Errorf("read SOCKS5 domain reply body: %w", err)
		}
	default:
		return 0, fmt.Errorf("unsupported SOCKS5 reply address type %#x", header[3])
	}
	return header[1], nil
}

func (h *systemHarness) apiRequest(method, path, token string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, h.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Host = h.managementHost
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return http.DefaultClient.Do(req)
}

func (h *systemHarness) poll(t *testing.T, timeout time.Duration, fn func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		ok, msg := fn()
		if ok {
			return
		}
		if msg != "" {
			last = msg
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out after %s; last=%s", timeout, last)
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func getenvDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func mustAtoi(t *testing.T, value string) int {
	t.Helper()
	out, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("parse integer %q: %v", value, err)
	}
	return out
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return string(raw)
}

func TestSystemHarnessRequiresComposeFiles(t *testing.T) {
	old := os.Getenv("NETSGO_E2E_COMPOSE_FILES")
	t.Cleanup(func() { _ = os.Setenv("NETSGO_E2E_COMPOSE_FILES", old) })
	_ = os.Unsetenv("NETSGO_E2E_COMPOSE_FILES")
	if os.Getenv("NETSGO_E2E_COMPOSE_FILES") != "" {
		t.Fatal("test setup failed to clear NETSGO_E2E_COMPOSE_FILES")
	}
}
