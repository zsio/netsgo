package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// ============================================================
// 消息构造测试 (4)
// ============================================================

func TestNewMessage_WithPayload(t *testing.T) {
	authReq := AuthRequest{
		Key: "test-key",
		Client: ClientInfo{
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
	if parsed.Key != authReq.Key {
		t.Errorf("Key 期望 %q，得到 %q", authReq.Key, parsed.Key)
	}
	if parsed.Client.Hostname != authReq.Client.Hostname {
		t.Errorf("Hostname 期望 %q，得到 %q", authReq.Client.Hostname, parsed.Client.Hostname)
	}
	if parsed.Client.IP != authReq.Client.IP {
		t.Errorf("IP 期望 %q，得到 %q", authReq.Client.IP, parsed.Client.IP)
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
		Key: "my-secret-key",
		Client: ClientInfo{
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

	if parsed.Key != original.Key {
		t.Errorf("Key 不匹配")
	}
	if parsed.Client.Hostname != original.Client.Hostname {
		t.Errorf("Hostname 不匹配")
	}
	if parsed.Client.OS != original.Client.OS {
		t.Errorf("OS 不匹配")
	}
	if parsed.Client.Arch != original.Client.Arch {
		t.Errorf("Arch 不匹配")
	}
	if parsed.Client.IP != original.Client.IP {
		t.Errorf("IP 不匹配")
	}
	if parsed.Client.Version != original.Client.Version {
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
		Key: "round-trip-key",
		Client: ClientInfo{
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

	if restored.Key != original.Key {
		t.Errorf("Key 不匹配")
	}
	if restored.Client != original.Client {
		t.Errorf("ClientInfo 不匹配: 期望 %+v, 得到 %+v", original.Client, restored.Client)
	}
}

func TestRoundTrip_ProxyConfig(t *testing.T) {
	original := ProxyConfig{
		Name:         "my-tunnel",
		Type:         ProxyTypeTCP,
		LocalIP:      "127.0.0.1",
		LocalPort:    3306,
		RemotePort:   13306,
		Domain:       "",
		ClientID:     "client_host_1",
		DesiredState: ProxyDesiredStateRunning,
		RuntimeState: ProxyRuntimeStateExposed,
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

	if !reflect.DeepEqual(restored, original) {
		t.Errorf("SystemStats 不匹配:\n期望: %+v\n得到: %+v", original, restored)
	}
}

// ============================================================
// 边界条件测试 (4)
// ============================================================

func TestZeroValueStructs(t *testing.T) {
	// 所有零值结构体都应该能正常序列化/反序列化
	structs := []any{
		ClientInfo{},
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
	clientInfo := ClientInfo{
		Hostname: "中文主机名",
		OS:       "linux",
		Arch:     "amd64",
		IP:       "10.0.0.1",
		Version:  "1.0.0",
	}
	msg, _ := NewMessage(MsgTypeAuth, AuthRequest{
		Key:    "emoji-key-🔑",
		Client: clientInfo,
	})

	// JSON 往返
	data, _ := json.Marshal(msg)
	var restored Message
	json.Unmarshal(data, &restored)

	var parsed AuthRequest
	restored.ParsePayload(&parsed)

	if parsed.Client.Hostname != "中文主机名" {
		t.Errorf("中文 Hostname 丢失: 得到 %q", parsed.Client.Hostname)
	}
	if parsed.Key != "emoji-key-🔑" {
		t.Errorf("Emoji Key 丢失: 得到 %q", parsed.Key)
	}
}

func TestOmitemptyBehavior(t *testing.T) {
	// AuthResponse 中可选字段都有 omitempty
	resp := AuthResponse{Success: true}
	data, _ := json.Marshal(resp)
	jsonStr := string(data)

	// omitempty 的空字段不应出现在 JSON 中
	if strings.Contains(jsonStr, `"message"`) {
		t.Errorf("空 Message 字段不应出现在 JSON 中: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"client_id"`) {
		t.Errorf("空 ClientID 字段不应出现在 JSON 中: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"code"`) {
		t.Errorf("空 Code 字段不应出现在 JSON 中: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"retryable"`) {
		t.Errorf("空 Retryable 字段不应出现在 JSON 中: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"clear_token"`) {
		t.Errorf("空 ClearToken 字段不应出现在 JSON 中: %s", jsonStr)
	}

	// 有值时应出现
	resp2 := AuthResponse{Success: false, Message: "ok", ClientID: "a1", Code: "invalid_token", Retryable: true, ClearToken: true}
	data2, _ := json.Marshal(resp2)
	jsonStr2 := string(data2)

	if !strings.Contains(jsonStr2, `"message"`) {
		t.Errorf("非空 Message 应出现在 JSON 中: %s", jsonStr2)
	}
	if !strings.Contains(jsonStr2, `"client_id"`) {
		t.Errorf("非空 ClientID 应出现在 JSON 中: %s", jsonStr2)
	}
	if !strings.Contains(jsonStr2, `"code"`) {
		t.Errorf("非空 Code 应出现在 JSON 中: %s", jsonStr2)
	}
	if !strings.Contains(jsonStr2, `"retryable"`) {
		t.Errorf("Retryable=true 应出现在 JSON 中: %s", jsonStr2)
	}
	if !strings.Contains(jsonStr2, `"clear_token"`) {
		t.Errorf("ClearToken=true 应出现在 JSON 中: %s", jsonStr2)
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
			"ClientInfo",
			ClientInfo{Hostname: "h", OS: "o", Arch: "a", IP: "i", Version: "v"},
			[]string{"hostname", "os", "arch", "ip", "version"},
		},
		{
			"SystemStats",
			SystemStats{CPUUsage: 1, MemTotal: 1, MemUsed: 1, MemUsage: 1, DiskTotal: 1, DiskUsed: 1, DiskUsage: 1, DiskPartitions: []DiskPartition{{Path: "/", Used: 1, Total: 1}}, NetSent: 1, NetRecv: 1, Uptime: 1, NumCPU: 1},
			[]string{"cpu_usage", "mem_total", "mem_used", "mem_usage", "disk_total", "disk_used", "disk_usage", "disk_partitions", "net_sent", "net_recv", "uptime", "num_cpu"},
		},
		{
			"ProxyConfig",
			ProxyConfig{Name: "n", Type: "t", LocalIP: "l", LocalPort: 1, RemotePort: 1, Domain: "d", ClientID: "a", DesiredState: ProxyDesiredStateRunning, RuntimeState: ProxyRuntimeStateExposed},
			[]string{"name", "type", "local_ip", "local_port", "remote_port", "domain", "client_id", "desired_state", "runtime_state"},
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
		"MsgTypeAuth":              MsgTypeAuth,
		"MsgTypeAuthResp":          MsgTypeAuthResp,
		"MsgTypePing":              MsgTypePing,
		"MsgTypePong":              MsgTypePong,
		"MsgTypeProbeReport":       MsgTypeProbeReport,
		"MsgTypeProxyNew":          MsgTypeProxyNew,
		"MsgTypeProxyNewResp":      MsgTypeProxyNewResp,
		"MsgTypeProxyCreate":       MsgTypeProxyCreate,
		"MsgTypeProxyCreateResp":   MsgTypeProxyCreateResp,
		"MsgTypeProxyProvision":    MsgTypeProxyProvision,
		"MsgTypeProxyProvisionAck": MsgTypeProxyProvisionAck,
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

	// 双状态常量
	if ProxyDesiredStateRunning != "running" {
		t.Errorf("ProxyDesiredStateRunning 期望 'running'，得到 %q", ProxyDesiredStateRunning)
	}
	if ProxyRuntimeStateExposed != "exposed" {
		t.Errorf("ProxyRuntimeStateExposed 期望 'exposed'，得到 %q", ProxyRuntimeStateExposed)
	}
	if ProxyRuntimeStateError != "error" {
		t.Errorf("ProxyRuntimeStateError 期望 'error'，得到 %q", ProxyRuntimeStateError)
	}
}
