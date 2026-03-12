package server

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"netsgo/pkg/protocol"
)

// ============================================================
// TunnelStore 单元测试
// ============================================================

// --- 创建 & 加载 ---

func TestTunnelStore_NewEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tunnels.json")

	store, err := NewTunnelStore(path)
	if err != nil {
		t.Fatalf("NewTunnelStore 失败: %v", err)
	}
	if store == nil {
		t.Fatal("store 不应为 nil")
	}

	tunnels := store.GetAllTunnels()
	if len(tunnels) != 0 {
		t.Errorf("新建的 store 应该为空，得到 %d 条记录", len(tunnels))
	}
}

func TestTunnelStore_LoadExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tunnels.json")

	// 先写入一条记录
	store1, _ := NewTunnelStore(path)
	store1.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			Name: "t1", Type: "tcp", LocalIP: "127.0.0.1", LocalPort: 80, RemotePort: 8080,
		},
		Status:   protocol.ProxyStatusActive,
		AgentID:  "agent-1",
		Hostname: "host-1",
	})

	// 重新加载
	store2, err := NewTunnelStore(path)
	if err != nil {
		t.Fatalf("重新加载失败: %v", err)
	}
	tunnels := store2.GetAllTunnels()
	if len(tunnels) != 1 {
		t.Fatalf("期望加载 1 条记录，得到 %d", len(tunnels))
	}
	if tunnels[0].Name != "t1" {
		t.Errorf("加载的隧道名期望 t1，得到 %s", tunnels[0].Name)
	}
}

func TestTunnelStore_CorruptedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tunnels.json")

	// 写入损坏的 JSON
	os.WriteFile(path, []byte(`{{{invalid json`), 0644)

	store, err := NewTunnelStore(path)
	if err != nil {
		t.Fatalf("损坏文件不应导致 NewTunnelStore 失败: %v", err)
	}
	// 应该降级为空列表
	if len(store.GetAllTunnels()) != 0 {
		t.Error("损坏文件应降级为空列表")
	}
}

// --- AddTunnel ---

func TestTunnelStore_AddTunnel_Success(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewTunnelStore(filepath.Join(dir, "tunnels.json"))

	err := store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "web", RemotePort: 8080},
		Hostname:        "myhost",
		Status:          protocol.ProxyStatusActive,
	})
	if err != nil {
		t.Fatalf("AddTunnel 失败: %v", err)
	}

	tunnels := store.GetAllTunnels()
	if len(tunnels) != 1 {
		t.Fatalf("期望 1 条，得到 %d", len(tunnels))
	}

	// 验证文件确实被写入
	data, _ := os.ReadFile(filepath.Join(dir, "tunnels.json"))
	if len(data) == 0 {
		t.Error("文件应已被写入")
	}
}

func TestTunnelStore_AddTunnel_DuplicateRejected(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewTunnelStore(filepath.Join(dir, "tunnels.json"))

	tunnel := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "dup"},
		Hostname:        "host-1",
	}
	store.AddTunnel(tunnel)

	err := store.AddTunnel(tunnel)
	if err == nil {
		t.Error("相同 hostname+name 的重复添加应被拒绝")
	}
}

func TestTunnelStore_AddTunnel_DiffHostnameSameNameAllowed(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewTunnelStore(filepath.Join(dir, "tunnels.json"))

	store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "web"},
		Hostname:        "host-A",
	})
	err := store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "web"},
		Hostname:        "host-B",
	})
	if err != nil {
		t.Errorf("不同 hostname 同 name 应允许: %v", err)
	}
	if len(store.GetAllTunnels()) != 2 {
		t.Error("应有 2 条记录")
	}
}

// --- RemoveTunnel ---

func TestTunnelStore_RemoveTunnel_Success(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewTunnelStore(filepath.Join(dir, "tunnels.json"))

	store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "rm-me"},
		Hostname:        "host",
	})

	err := store.RemoveTunnel("host", "rm-me")
	if err != nil {
		t.Fatalf("RemoveTunnel 失败: %v", err)
	}
	if len(store.GetAllTunnels()) != 0 {
		t.Error("删除后应为空")
	}
}

func TestTunnelStore_RemoveTunnel_NotFound(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewTunnelStore(filepath.Join(dir, "tunnels.json"))

	err := store.RemoveTunnel("ghost", "not-exist")
	if err == nil {
		t.Error("删除不存在的隧道应返回错误")
	}
}

// --- UpdateStatus ---

func TestTunnelStore_UpdateStatus(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewTunnelStore(filepath.Join(dir, "tunnels.json"))

	store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t1"},
		Hostname:        "host",
		Status:          protocol.ProxyStatusActive,
	})

	// active → paused
	if err := store.UpdateStatus("host", "t1", protocol.ProxyStatusPaused); err != nil {
		t.Fatalf("UpdateStatus 失败: %v", err)
	}
	st, _ := store.GetTunnel("host", "t1")
	if st.Status != protocol.ProxyStatusPaused {
		t.Errorf("状态期望 paused，得到 %s", st.Status)
	}

	// paused → stopped
	store.UpdateStatus("host", "t1", protocol.ProxyStatusStopped)
	st2, _ := store.GetTunnel("host", "t1")
	if st2.Status != protocol.ProxyStatusStopped {
		t.Errorf("状态期望 stopped，得到 %s", st2.Status)
	}
}

func TestTunnelStore_UpdateStatus_NotFound(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewTunnelStore(filepath.Join(dir, "tunnels.json"))

	err := store.UpdateStatus("ghost", "no-tunnel", "active")
	if err == nil {
		t.Error("更新不存在的隧道应返回错误")
	}
}

// --- UpdateAgentID ---

func TestTunnelStore_UpdateAgentID(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewTunnelStore(filepath.Join(dir, "tunnels.json"))

	store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t1"},
		Hostname:        "host",
		AgentID:         "old-id",
	})
	store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t2"},
		Hostname:        "host",
		AgentID:         "old-id",
	})

	store.UpdateAgentID("host", "old-id", "new-id")

	tunnels := store.GetTunnelsByHostname("host")
	for _, t2 := range tunnels {
		if t2.AgentID != "new-id" {
			t.Errorf("隧道 %s 的 AgentID 期望 new-id，得到 %s", t2.Name, t2.AgentID)
		}
	}
}

func TestTunnelStore_UpdateAgentID_NoMatch(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewTunnelStore(filepath.Join(dir, "tunnels.json"))

	// 无匹配时不应 panic
	store.UpdateAgentID("no-host", "", "new-id")
}

// --- 查询 ---

func TestTunnelStore_GetTunnel(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewTunnelStore(filepath.Join(dir, "tunnels.json"))

	store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "find-me", RemotePort: 9090},
		Hostname:        "host",
	})

	st, found := store.GetTunnel("host", "find-me")
	if !found {
		t.Fatal("应找到隧道")
	}
	if st.RemotePort != 9090 {
		t.Errorf("RemotePort 期望 9090，得到 %d", st.RemotePort)
	}

	_, found2 := store.GetTunnel("host", "not-exist")
	if found2 {
		t.Error("不存在的隧道不应被找到")
	}
}

func TestTunnelStore_GetTunnelsByHostname(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewTunnelStore(filepath.Join(dir, "tunnels.json"))

	store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t1"},
		Hostname:        "host-A",
	})
	store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t2"},
		Hostname:        "host-A",
	})
	store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t3"},
		Hostname:        "host-B",
	})

	result := store.GetTunnelsByHostname("host-A")
	if len(result) != 2 {
		t.Errorf("期望 2 条，得到 %d", len(result))
	}

	empty := store.GetTunnelsByHostname("no-host")
	if len(empty) != 0 {
		t.Errorf("不存在的 host 应返回空，得到 %d", len(empty))
	}
}

func TestTunnelStore_GetAllTunnels_ReturnsCopy(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewTunnelStore(filepath.Join(dir, "tunnels.json"))

	store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "original"},
		Hostname:        "host",
	})

	// 修改返回值不应影响原始
	result := store.GetAllTunnels()
	result[0].Name = "mutated"

	original := store.GetAllTunnels()
	if original[0].Name != "original" {
		t.Error("GetAllTunnels 应返回副本，修改不应影响原始数据")
	}
}

// --- 并发安全 ---

func TestTunnelStore_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewTunnelStore(filepath.Join(dir, "tunnels.json"))

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := "tunnel-" + string(rune('A'+idx%26))
			hostname := "host-" + string(rune('0'+idx%10))
			store.AddTunnel(StoredTunnel{
				ProxyNewRequest: protocol.ProxyNewRequest{Name: name},
				Hostname:        hostname,
			})
			store.GetAllTunnels()
			store.GetTunnelsByHostname(hostname)
		}(i)
	}
	wg.Wait()
	// 不 panic 即通过
}
