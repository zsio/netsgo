//go:build e2e

package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestP2PDoubleNATSystemE2E(t *testing.T) {
	composeFile := os.Getenv("NETSGO_DOUBLE_NAT_COMPOSE_FILE")
	if composeFile == "" {
		t.Skip("NETSGO_DOUBLE_NAT_COMPOSE_FILE is required")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("docker CLI not found: %v", err)
	}
	adminPass := os.Getenv("NETSGO_ADMIN_PASS")
	if adminPass == "" {
		t.Fatal("NETSGO_ADMIN_PASS is required")
	}
	serverPort := getenvDefault("DOUBLE_NAT_SERVER_PORT", "19281")
	ingressPort, err := strconv.Atoi(getenvDefault("DOUBLE_NAT_INGRESS_PORT", "19298"))
	if err != nil {
		t.Fatal(err)
	}
	h := &systemHarness{
		projectName:        getenvDefault("NETSGO_E2E_COMPOSE_PROJECT", "netsgo-p2p-double-nat"),
		composeFiles:       []string{composeFile},
		baseURL:            "http://127.0.0.1:" + serverPort,
		managementHost:     "172.31.0.10:8080",
		adminUser:          defaultAdminUser,
		adminPass:          adminPass,
		targetHostname:     "p2p-double-nat-target",
		ingressHostname:    "p2p-double-nat-ingress",
		c2cTCPPort:         ingressPort,
		c2cTransportPolicy: "direct_preferred",
		expectP2P:          true,
	}
	h.composeEnv = append(os.Environ(),
		"NETSGO_ADMIN_PASS="+adminPass,
		"NETSGO_E2E_IMAGE="+getenvDefault("NETSGO_E2E_IMAGE", "netsgo-e2e:prebuilt"),
		"DOUBLE_NAT_SERVER_PORT="+serverPort,
		"DOUBLE_NAT_INGRESS_PORT="+strconv.Itoa(ingressPort),
	)
	t.Cleanup(func() {
		if t.Failed() {
			h.dumpCompose(t, "ps")
			h.dumpCompose(t, "logs", "--no-color", "--tail", "300")
		}
		h.compose(t, h.composeEnv, "down", "-v", "--remove-orphans")
	})
	h.compose(t, h.composeEnv, "down", "-v", "--remove-orphans")
	h.compose(t, h.composeEnv, "up", "-d", "--no-build", "server")
	h.adminToken = h.waitForAdminToken(t, 90*time.Second)
	clientKey := h.createAPIKey(t)
	clientEnv := append(append([]string(nil), h.composeEnv...), "NETSGO_CLIENT_KEY="+clientKey)
	h.compose(t, clientEnv, "up", "-d", "--no-build", "target-router", "ingress-router")
	h.targetClientID, h.ingressClientID = h.waitForClientPair(t, 90*time.Second)
	assertDoubleNATUDPHairpin(t, h)

	tunnel := h.createTCPClientToClientTunnel(t, "double-nat-direct", 19098, "127.0.0.1", backendPort)
	h.waitTunnelState(t, tunnel.ID, "active", 90*time.Second)
	h.waitTunnelP2PConnected(t, tunnel.ID, 120*time.Second)
	assertDoubleNATEcho(t, ingressPort, []byte("double-nat-peer-direct"))

	blockPeerUDP(t, h, true)
	waitDoubleNATFallback(t, h, tunnel.ID, 120*time.Second)
	assertDoubleNATEcho(t, ingressPort, []byte("double-nat-server-relay-fallback"))

	blockPeerUDP(t, h, false)
	h.waitTunnelP2PConnected(t, tunnel.ID, 150*time.Second)
	assertDoubleNATEcho(t, ingressPort, []byte("double-nat-direct-after-automatic-retry"))
}

func assertDoubleNATUDPHairpin(t *testing.T, h *systemHarness) {
	t.Helper()
	command := "ip netns exec netsgo-client socat UDP-LISTEN:19999,fork,reuseaddr SYSTEM:cat >/dev/null 2>&1 & " +
		"pid=$!; sleep 0.2; " +
		"got=$(ip netns exec netsgo-client sh -c 'printf hairpin | socat -T2 - UDP:172.31.0.2:19999'); " +
		"kill $pid 2>/dev/null || true; [ \"$got\" = hairpin ]"
	h.compose(t, h.composeEnv, "exec", "-T", "target-router", "sh", "-c", command)
}

func blockPeerUDP(t *testing.T, h *systemHarness, block bool) {
	t.Helper()
	for _, router := range []string{"target-router", "ingress-router"} {
		command := "nft delete table ip netsgo_test_block"
		if block {
			command = "nft add table ip netsgo_test_block; " +
				"nft 'add chain ip netsgo_test_block prerouting { type filter hook prerouting priority -310; policy accept; }'; " +
				"nft add rule ip netsgo_test_block prerouting ip protocol udp drop"
		}
		h.compose(t, h.composeEnv, "exec", "-T", router, "sh", "-c", command)
	}
}

func waitDoubleNATFallback(t *testing.T, h *systemHarness, tunnelID string, timeout time.Duration) {
	t.Helper()
	var last tunnelResponse
	h.poll(t, timeout, func() (bool, string) {
		resp, err := h.apiRequest(http.MethodGet, "/api/tunnels/"+tunnelID, h.adminToken, nil)
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return false, fmt.Sprintf("status=%d body=%s", resp.StatusCode, body)
		}
		if err := json.NewDecoder(resp.Body).Decode(&last); err != nil {
			return false, err.Error()
		}
		if last.ActualTransport == "server_relay" && (last.P2P.State == "fallback" || last.P2P.State == "failed") {
			return true, ""
		}
		return false, fmt.Sprintf("p2p=%q actual=%q", last.P2P.State, last.ActualTransport)
	})
}

func assertDoubleNATEcho(t *testing.T, ingressPort int, payload []byte) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ingressPort)), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if n, err := conn.Write(payload); err != nil || n != len(payload) {
		t.Fatalf("write: n=%d want=%d err=%v", n, len(payload), err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("echo mismatch: got=%q want=%q", got, payload)
	}
	_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var extra [1]byte
	if n, err := conn.Read(extra[:]); err == nil || n != 0 {
		t.Fatalf("duplicate payload after echo: n=%d data=%q err=%v", n, extra[:n], err)
	} else if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("connection closed instead of staying idle: %v", err)
	}
}
