package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// ============================================================
// 消息构造测试 (4)
// ============================================================

func TestNewMessage_WithPayload(t *testing.T) {
	authReq := AuthRequest{
		Token: "test-token",
		Agent: AgentInfo{
			Hostname: "test-host",
			OS:       "linux",
			Arch:     "amd64",
			IP:       "192.168.1.100",
			Version:  "0.1.0",
		},
	}

	msg, err := NewMessage(MsgTypeAuth, authReq)
	if err != nil {
		t.Fatalf("NewMessage 失败: %v", err)
	}
	if msg.Type != MsgTypeAuth {
		t.Errorf("Type 期望 %q，得到 %q", MsgTypeAuth, msg.Type)
	}
	if msg.Payload == nil {
		t.Fatal("Payload 不应为 nil")
	}

	// 验证 Payload 能正确解析回来
	var parsed AuthRequest
	if err := msg.ParsePayload(&parsed); err != nil {
		t.Fatalf("ParsePayload 失败: %v", err)
	}
	if parsed.Token != authReq.Token {
		t.Errorf("Token 期望 %q，得到 %q", authReq.Token, parsed.Token)
	}
	if parsed.Agent.Hostname != authReq.Agent.Hostname {
		t.Errorf("Hostname 期望 %q，得到 %q", authReq.Agent.Hostname, parsed.Agent.Hostname)
	}
	if parsed.Agent.IP != authReq.Agent.IP {
		t.Errorf("IP 期望 %q，得到 %q", authReq.Agent.IP, parsed.Agent.IP)
	}
}

func TestNewMessage_NilPayload(t *testing.T) {
	msg, err := NewMessage(MsgTypePing, nil)
	if err != nil {
		t.Fatalf("NewMessage 失败: %v", err)
	}
	if msg.Type != MsgTypePing {
		t.Errorf("Type 期望 %q，得到 %q", MsgTypePing, msg.Type)
	}
	if msg.Payload != nil {
		t.Errorf("Payload 应为 nil，得到 %s", string(msg.Payload))
	}
}

func TestNewMessage_InvalidPayload(t *testing.T) {
	ch := make(chan int)
	_, err := NewMessage("test", ch)
	if err == nil {
		t.Fatal("传入无法序列化的 payload 应该返回 error")
	}
}

func TestNewMessage_EmptyType(t *testing.T) {
	msg, err := NewMessage("", nil)
	if err != nil {
		t.Fatalf("NewMessage 失败: %v", err)
	}
	if msg.Type != "" {
		t.Errorf("Type 期望空字符串，得到 %q", msg.Type)
	}
}

// ============================================================
// Payload 解析测试 (4)
// ============================================================

func TestParsePayload_AuthRequest(t *testing.T) {
	original := AuthRequest{
		Token: "my-secret-token",
		Agent: AgentInfo{
			Hostname: "production-server",
			OS:       "linux",
			Arch:     "arm64",
			IP:       "10.0.0.5",
			Version:  "1.2.3",
		},
	}

	msg, _ := NewMessage(MsgTypeAuth, original)
	var parsed AuthRequest
	if err := msg.ParsePayload(&parsed); err != nil {
		t.Fatalf("ParsePayload 失败: %v", err)
	}

	if parsed.Token != original.Token {
		t.Errorf("Token 不匹配")
	}
	if parsed.Agent.Hostname != original.Agent.Hostname {
		t.Errorf("Hostname 不匹配")
	}
	if parsed.Agent.OS != original.Agent.OS {
		t.Errorf("OS 不匹配")
	}
	if parsed.Agent.Arch != original.Agent.Arch {
		t.Errorf("Arch 不匹配")
	}
	if parsed.Agent.IP != original.Agent.IP {
		t.Errorf("IP 不匹配")
	}
	if parsed.Agent.Version != original.Agent.Version {
		t.Errorf("Version 不匹配")
	}
}

func TestParsePayload_SystemStats(t *testing.T) {
	original := SystemStats{
		CPUUsage:  75.5,
		MemTotal:  32 * 1024 * 1024 * 1024,
		MemUsed:   24 * 1024 * 1024 * 1024,
		MemUsage:  75.0,
		DiskTotal: 1024 * 1024 * 1024 * 1024,
		DiskUsed:  512 * 1024 * 1024 * 1024,
		DiskUsage: 50.0,
		NetSent:   999_999_999,
		NetRecv:   888_888_888,
		Uptime:    86400 * 30,
		NumCPU:    16,
	}

	msg, _ := NewMessage(MsgTypeProbeReport, original)
	var parsed SystemStats
	if err := msg.ParsePayload(&parsed); err != nil {
		t.Fatalf("ParsePayload 失败: %v", err)
	}

	if parsed.CPUUsage != original.CPUUsage {
		t.Errorf("CPUUsage: 期望 %f，得到 %f", original.CPUUsage, parsed.CPUUsage)
	}
	if parsed.MemTotal != original.MemTotal {
		t.Errorf("MemTotal: 期望 %d，得到 %d", original.MemTotal, parsed.MemTotal)
	}
	if parsed.MemUsed != original.MemUsed {
		t.Errorf("MemUsed: 期望 %d，得到 %d", original.MemUsed, parsed.MemUsed)
	}
	if parsed.DiskTotal != original.DiskTotal {
		t.Errorf("DiskTotal: 期望 %d，得到 %d", original.DiskTotal, parsed.DiskTotal)
	}
	if parsed.NetSent != original.NetSent {
		t.Errorf("NetSent: 期望 %d，得到 %d", original.NetSent, parsed.NetSent)
	}
	if parsed.NetRecv != original.NetRecv {
		t.Errorf("NetRecv: 期望 %d，得到 %d", original.NetRecv, parsed.NetRecv)
	}
	if parsed.Uptime != original.Uptime {
		t.Errorf("Uptime: 期望 %d，得到 %d", original.Uptime, parsed.Uptime)
	}
	if parsed.NumCPU != original.NumCPU {
		t.Errorf("NumCPU: 期望 %d，得到 %d", original.NumCPU, parsed.NumCPU)
	}
}

func TestParsePayload_NilPayload(t *testing.T) {
	msg := &Message{Type: MsgTypePing, Payload: nil}
	var target AuthRequest
	err := msg.ParsePayload(&target)
	if err == nil {
		t.Fatal("nil Payload 调用 ParsePayload 应返回 error")
	}
}

func TestParsePayload_MalformedJSON(t *testing.T) {
	msg := &Message{
		Type:    MsgTypeAuth,
		Payload: json.RawMessage(`{broken json!!!`),
	}
	var target AuthRequest
	err := msg.ParsePayload(&target)
	if err == nil {
		t.Fatal("损坏的 JSON Payload 应返回 error")
	}
}

// ============================================================
// JSON 完整往返测试 — 每种结构体 (5)
// ============================================================

func TestRoundTrip_Message(t *testing.T) {
	original, _ := NewMessage(MsgTypePong, nil)

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}

	var restored Message
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal 失败: %v", err)
	}

	if restored.Type != original.Type {
		t.Errorf("Type: 期望 %q，得到 %q", original.Type, restored.Type)
	}
}

func TestRoundTrip_AuthRequest(t *testing.T) {
	original := AuthRequest{
		Token: "round-trip-token",
		Agent: AgentInfo{
			Hostname: "nested-host",
			OS:       "darwin",
			Arch:     "arm64",
			IP:       "172.16.0.1",
			Version:  "2.0.0",
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}

	var restored AuthRequest
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal 失败: %v", err)
	}

	if restored.Token != original.Token {
		t.Errorf("Token 不匹配")
	}
	if restored.Agent != original.Agent {
		t.Errorf("AgentInfo 不匹配: 期望 %+v, 得到 %+v", original.Agent, restored.Agent)
	}
}

func TestRoundTrip_ProxyConfig(t *testing.T) {
	original := ProxyConfig{
		Name:       "my-tunnel",
		Type:       ProxyTypeTCP,
		LocalIP:    "127.0.0.1",
		LocalPort:  3306,
		RemotePort: 13306,
		Domain:     "",
		AgentID:    "agent_host_1",
		Status:     ProxyStatusActive,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}

	var restored ProxyConfig
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal 失败: %v", err)
	}

	if restored != original {
		t.Errorf("ProxyConfig 不匹配: 期望 %+v, 得到 %+v", original, restored)
	}
}

func TestRoundTrip_ProxyNewRequest(t *testing.T) {
	testCases := []ProxyNewRequest{
		{Name: "tcp-tunnel", Type: ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 80, RemotePort: 8080},
		{Name: "udp-tunnel", Type: ProxyTypeUDP, LocalIP: "127.0.0.1", LocalPort: 53, RemotePort: 10053},
		{Name: "http-tunnel", Type: ProxyTypeHTTP, LocalIP: "127.0.0.1", LocalPort: 3000, Domain: "app.example.com"},
	}

	for _, original := range testCases {
		t.Run(original.Type, func(t *testing.T) {
			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("Marshal 失败: %v", err)
			}
			var restored ProxyNewRequest
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatalf("Unmarshal 失败: %v", err)
			}
			if restored != original {
				t.Errorf("不匹配: 期望 %+v, 得到 %+v", original, restored)
			}
		})
	}
}

func TestRoundTrip_SystemStats(t *testing.T) {
	// 使用大数值测试精度
	original := SystemStats{
		CPUUsage:  99.99,
		MemTotal:  64 * 1024 * 1024 * 1024, // 64GB
		MemUsed:   48 * 1024 * 1024 * 1024,
		MemUsage:  75.0,
		DiskTotal: 2 * 1024 * 1024 * 1024 * 1024, // 2TB
		DiskUsed:  1 * 1024 * 1024 * 1024 * 1024,
		DiskUsage: 50.0,
		NetSent:   1_000_000_000_000, // 1TB
		NetRecv:   2_000_000_000_000,
		Uptime:    365 * 24 * 3600, // 1 year
		NumCPU:    128,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}

	var restored SystemStats
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal 失败: %v", err)
	}

	if restored != original {
		t.Errorf("SystemStats 不匹配:\n期望: %+v\n得到: %+v", original, restored)
	}
}

// ============================================================
// 边界条件测试 (4)
// ============================================================

func TestZeroValueStructs(t *testing.T) {
	// 所有零值结构体都应该能正常序列化/反序列化
	structs := []any{
		AgentInfo{},
		SystemStats{},
		ProxyConfig{},
		AuthRequest{},
		AuthResponse{},
		ProxyNewRequest{},
		ProxyNewResponse{},
	}

	for _, s := range structs {
		data, err := json.Marshal(s)
		if err != nil {
			t.Errorf("零值结构体 %T Marshal 失败: %v", s, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("零值结构体 %T 序列化为空", s)
		}
	}
}

func TestUnicodeFields(t *testing.T) {
	agent := AgentInfo{
		Hostname: "中文主机名",
		OS:       "linux",
		Arch:     "amd64",
		IP:       "10.0.0.1",
		Version:  "1.0.0",
	}
	msg, _ := NewMessage(MsgTypeAuth, AuthRequest{
		Token: "emoji-token-🔑",
		Agent: agent,
	})

	// JSON 往返
	data, _ := json.Marshal(msg)
	var restored Message
	json.Unmarshal(data, &restored)

	var parsed AuthRequest
	restored.ParsePayload(&parsed)

	if parsed.Agent.Hostname != "中文主机名" {
		t.Errorf("中文 Hostname 丢失: 得到 %q", parsed.Agent.Hostname)
	}
	if parsed.Token != "emoji-token-🔑" {
		t.Errorf("Emoji Token 丢失: 得到 %q", parsed.Token)
	}
}

func TestOmitemptyBehavior(t *testing.T) {
	// AuthResponse 中 Message 和 AgentID 有 omitempty
	resp := AuthResponse{Success: true}
	data, _ := json.Marshal(resp)
	jsonStr := string(data)

	// omitempty 的空字段不应出现在 JSON 中
	if strings.Contains(jsonStr, `"message"`) {
		t.Errorf("空 Message 字段不应出现在 JSON 中: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"agent_id"`) {
		t.Errorf("空 AgentID 字段不应出现在 JSON 中: %s", jsonStr)
	}

	// 有值时应出现
	resp2 := AuthResponse{Success: true, Message: "ok", AgentID: "a1"}
	data2, _ := json.Marshal(resp2)
	jsonStr2 := string(data2)

	if !strings.Contains(jsonStr2, `"message"`) {
		t.Errorf("非空 Message 应出现在 JSON 中: %s", jsonStr2)
	}
	if !strings.Contains(jsonStr2, `"agent_id"`) {
		t.Errorf("非空 AgentID 应出现在 JSON 中: %s", jsonStr2)
	}
}

func TestLargePayload(t *testing.T) {
	// 构造一个 ~1MB 的 Payload
	largeStr := strings.Repeat("x", 1024*1024) // 1MB
	payload := map[string]string{"data": largeStr}

	msg, err := NewMessage("large_test", payload)
	if err != nil {
		t.Fatalf("大 Payload 构造失败: %v", err)
	}

	// 验证能反序列化
	var parsed map[string]string
	if err := msg.ParsePayload(&parsed); err != nil {
		t.Fatalf("大 Payload 解析失败: %v", err)
	}

	if len(parsed["data"]) != 1024*1024 {
		t.Errorf("大 Payload 长度不匹配: 期望 %d，得到 %d", 1024*1024, len(parsed["data"]))
	}
}

// ============================================================
// JSON Tag 验证 (2)
// ============================================================

func TestAllStructs_JSONTags(t *testing.T) {
	tests := []struct {
		name         string
		value        any
		expectedKeys []string
	}{
		{
			"AgentInfo",
			AgentInfo{Hostname: "h", OS: "o", Arch: "a", IP: "i", Version: "v"},
			[]string{"hostname", "os", "arch", "ip", "version"},
		},
		{
			"SystemStats",
			SystemStats{CPUUsage: 1, MemTotal: 1, MemUsed: 1, MemUsage: 1, DiskTotal: 1, DiskUsed: 1, DiskUsage: 1, NetSent: 1, NetRecv: 1, Uptime: 1, NumCPU: 1},
			[]string{"cpu_usage", "mem_total", "mem_used", "mem_usage", "disk_total", "disk_used", "disk_usage", "net_sent", "net_recv", "uptime", "num_cpu"},
		},
		{
			"ProxyConfig",
			ProxyConfig{Name: "n", Type: "t", LocalIP: "l", LocalPort: 1, RemotePort: 1, Domain: "d", AgentID: "a", Status: "s"},
			[]string{"name", "type", "local_ip", "local_port", "remote_port", "domain", "agent_id", "status"},
		},
		{
			"ProxyNewRequest",
			ProxyNewRequest{Name: "n", Type: "t", LocalIP: "l", LocalPort: 1, RemotePort: 1, Domain: "d"},
			[]string{"name", "type", "local_ip", "local_port", "remote_port", "domain"},
		},
		{
			"Message",
			Message{Type: "t", Payload: json.RawMessage(`{}`)},
			[]string{"type", "payload"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, _ := json.Marshal(tt.value)
			var m map[string]any
			json.Unmarshal(data, &m)

			for _, key := range tt.expectedKeys {
				if _, ok := m[key]; !ok {
					t.Errorf("%s JSON 缺少字段 %q, JSON: %s", tt.name, key, string(data))
				}
			}
		})
	}
}

func TestConstants(t *testing.T) {
	// 消息类型
	msgTypes := map[string]string{
		"MsgTypeAuth":         MsgTypeAuth,
		"MsgTypeAuthResp":     MsgTypeAuthResp,
		"MsgTypePing":         MsgTypePing,
		"MsgTypePong":         MsgTypePong,
		"MsgTypeProbeReport":  MsgTypeProbeReport,
		"MsgTypeProxyNew":     MsgTypeProxyNew,
		"MsgTypeProxyNewResp": MsgTypeProxyNewResp,
	}
	for name, val := range msgTypes {
		if val == "" {
			t.Errorf("%s 不应为空字符串", name)
		}
	}

	// 代理类型
	if ProxyTypeTCP != "tcp" {
		t.Errorf("ProxyTypeTCP 期望 'tcp'，得到 %q", ProxyTypeTCP)
	}
	if ProxyTypeUDP != "udp" {
		t.Errorf("ProxyTypeUDP 期望 'udp'，得到 %q", ProxyTypeUDP)
	}
	if ProxyTypeHTTP != "http" {
		t.Errorf("ProxyTypeHTTP 期望 'http'，得到 %q", ProxyTypeHTTP)
	}

	// 代理状态
	if ProxyStatusActive != "active" {
		t.Errorf("ProxyStatusActive 期望 'active'，得到 %q", ProxyStatusActive)
	}
	if ProxyStatusStopped != "stopped" {
		t.Errorf("ProxyStatusStopped 期望 'stopped'，得到 %q", ProxyStatusStopped)
	}
	if ProxyStatusError != "error" {
		t.Errorf("ProxyStatusError 期望 'error'，得到 %q", ProxyStatusError)
	}
}
