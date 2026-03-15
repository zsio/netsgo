package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"netsgo/pkg/protocol"
)

func newTestTunnelStore(t *testing.T) *TunnelStore {
	t.Helper()

	store, err := NewTunnelStore(filepath.Join(t.TempDir(), "tunnels.json"))
	if err != nil {
		t.Fatalf("NewTunnelStore 失败: %v", err)
	}
	return store
}

func mustAddStableTunnel(t *testing.T, store *TunnelStore, tunnel StoredTunnel) {
	t.Helper()

	tunnel.Binding = TunnelBindingClientID
	if tunnel.ClientID == "" {
		t.Fatal("测试隧道必须提供 ClientID")
	}
	if err := store.AddTunnel(tunnel); err != nil {
		t.Fatalf("AddTunnel 失败: %v", err)
	}
}

func seedLegacyTunnels(t *testing.T, store *TunnelStore, tunnels ...StoredTunnel) {
	t.Helper()

	store.mu.Lock()
	store.tunnels = append([]StoredTunnel(nil), tunnels...)
	store.mu.Unlock()

	if err := store.save(); err != nil {
		t.Fatalf("保存 legacy tunnels 失败: %v", err)
	}
}

func TestTunnelStore_NewEmpty(t *testing.T) {
	store := newTestTunnelStore(t)
	if len(store.GetAllTunnels()) != 0 {
		t.Errorf("新建的 store 应该为空，得到 %d 条记录", len(store.GetAllTunnels()))
	}
}

func TestTunnelStore_LoadExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tunnels.json")

	store1, err := NewTunnelStore(path)
	if err != nil {
		t.Fatalf("NewTunnelStore 失败: %v", err)
	}
	mustAddStableTunnel(t, store1, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			Name: "t1", Type: "tcp", LocalIP: "127.0.0.1", LocalPort: 80, RemotePort: 8080,
		},
		Status:   protocol.ProxyStatusActive,
		ClientID:  "client-1",
		Hostname: "host-1",
	})

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
	if tunnels[0].Binding != TunnelBindingClientID {
		t.Errorf("Binding 期望 %s，得到 %s", TunnelBindingClientID, tunnels[0].Binding)
	}
}

func TestTunnelStore_CorruptedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tunnels.json")

	if err := os.WriteFile(path, []byte(`{{{invalid json`), 0o644); err != nil {
		t.Fatalf("写入损坏文件失败: %v", err)
	}

	store, err := NewTunnelStore(path)
	if err != nil {
		t.Fatalf("损坏文件不应导致 NewTunnelStore 失败: %v", err)
	}
	if len(store.GetAllTunnels()) != 0 {
		t.Error("损坏文件应降级为空列表")
	}
}

func TestTunnelStore_AddTunnel_Success(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "web", RemotePort: 8080},
		ClientID:         "client-1",
		Hostname:        "myhost",
		Status:          protocol.ProxyStatusActive,
	})

	tunnels := store.GetAllTunnels()
	if len(tunnels) != 1 {
		t.Fatalf("期望 1 条，得到 %d", len(tunnels))
	}
	if tunnels[0].ClientID != "client-1" {
		t.Errorf("ClientID 期望 client-1，得到 %s", tunnels[0].ClientID)
	}
	if tunnels[0].Binding != TunnelBindingClientID {
		t.Errorf("Binding 期望 %s，得到 %s", TunnelBindingClientID, tunnels[0].Binding)
	}
}

func TestTunnelStore_AddTunnel_DuplicateRejected(t *testing.T) {
	store := newTestTunnelStore(t)

	tunnel := StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "dup"},
		ClientID:         "client-1",
		Hostname:        "host-1",
		Binding:         TunnelBindingClientID,
	}
	mustAddStableTunnel(t, store, tunnel)

	if err := store.AddTunnel(tunnel); err == nil {
		t.Error("相同 client_id+name 的重复添加应被拒绝")
	}
}

func TestTunnelStore_AddTunnel_DiffClientSameNameAllowed(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "web"},
		ClientID:         "client-A",
		Hostname:        "host-A",
	})
	if err := store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "web"},
		ClientID:         "client-B",
		Hostname:        "host-B",
		Binding:         TunnelBindingClientID,
	}); err != nil {
		t.Errorf("不同 client_id 同 name 应允许: %v", err)
	}
	if len(store.GetAllTunnels()) != 2 {
		t.Error("应有 2 条记录")
	}
}

func TestTunnelStore_RemoveTunnel_Success(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "rm-me"},
		ClientID:         "client-1",
		Hostname:        "host",
	})

	if err := store.RemoveTunnel("client-1", "rm-me"); err != nil {
		t.Fatalf("RemoveTunnel 失败: %v", err)
	}
	if len(store.GetAllTunnels()) != 0 {
		t.Error("删除后应为空")
	}
}

func TestTunnelStore_RemoveTunnel_NotFound(t *testing.T) {
	store := newTestTunnelStore(t)
	if err := store.RemoveTunnel("ghost", "not-exist"); err == nil {
		t.Error("删除不存在的隧道应返回错误")
	}
}

func TestTunnelStore_UpdateStatus(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t1"},
		ClientID:         "client-1",
		Hostname:        "host",
		Status:          protocol.ProxyStatusActive,
	})

	if err := store.UpdateStatus("client-1", "t1", protocol.ProxyStatusPaused); err != nil {
		t.Fatalf("UpdateStatus 失败: %v", err)
	}
	st, _ := store.GetTunnel("client-1", "t1")
	if st.Status != protocol.ProxyStatusPaused {
		t.Errorf("状态期望 paused，得到 %s", st.Status)
	}

	if err := store.UpdateStatus("client-1", "t1", protocol.ProxyStatusStopped); err != nil {
		t.Fatalf("UpdateStatus 失败: %v", err)
	}
	st2, _ := store.GetTunnel("client-1", "t1")
	if st2.Status != protocol.ProxyStatusStopped {
		t.Errorf("状态期望 stopped，得到 %s", st2.Status)
	}
}

func TestTunnelStore_UpdateStatus_NotFound(t *testing.T) {
	store := newTestTunnelStore(t)
	if err := store.UpdateStatus("ghost", "no-tunnel", "active"); err == nil {
		t.Error("更新不存在的隧道应返回错误")
	}
}

func TestTunnelStore_UpdateClientID(t *testing.T) {
	store := newTestTunnelStore(t)

	seedLegacyTunnels(t, store,
		StoredTunnel{
			ProxyNewRequest: protocol.ProxyNewRequest{Name: "t1"},
			Hostname:        "host",
			Status:          protocol.ProxyStatusActive,
			Binding:         TunnelBindingLegacyHostname,
		},
		StoredTunnel{
			ProxyNewRequest: protocol.ProxyNewRequest{Name: "t2"},
			Hostname:        "host",
			Status:          protocol.ProxyStatusPaused,
			Binding:         TunnelBindingLegacyHostname,
		},
	)

	store.UpdateClientID("host", "old-id", "new-id")

	tunnels := store.GetTunnelsByClientID("new-id")
	if len(tunnels) != 2 {
		t.Fatalf("期望迁移出 2 条隧道，得到 %d", len(tunnels))
	}
	for _, tunnel := range tunnels {
		if tunnel.Binding != TunnelBindingClientID {
			t.Errorf("隧道 %s 的 Binding 期望迁移为 %s，得到 %s", tunnel.Name, TunnelBindingClientID, tunnel.Binding)
		}
		if tunnel.Hostname != "host" {
			t.Errorf("隧道 %s 的 Hostname 期望保留 host，得到 %s", tunnel.Name, tunnel.Hostname)
		}
	}
}

func TestTunnelStore_UpdateClientID_NoMatch(t *testing.T) {
	store := newTestTunnelStore(t)
	store.UpdateClientID("no-host", "", "new-id")
	if len(store.GetTunnelsByClientID("new-id")) != 0 {
		t.Error("无匹配时不应迁移出任何隧道")
	}
}

func TestTunnelStore_GetTunnel(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "find-me", RemotePort: 9090},
		ClientID:         "client-1",
		Hostname:        "host",
	})

	st, found := store.GetTunnel("client-1", "find-me")
	if !found {
		t.Fatal("应找到隧道")
	}
	if st.RemotePort != 9090 {
		t.Errorf("RemotePort 期望 9090，得到 %d", st.RemotePort)
	}

	if _, found := store.GetTunnel("client-1", "not-exist"); found {
		t.Error("不存在的隧道不应被找到")
	}
}

func TestTunnelStore_GetTunnelsByHostname(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t1"},
		ClientID:         "client-1",
		Hostname:        "host-A",
	})
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t2"},
		ClientID:         "client-2",
		Hostname:        "host-A",
	})
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t3"},
		ClientID:         "client-3",
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
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "original"},
		ClientID:         "client-1",
		Hostname:        "host",
	})

	result := store.GetAllTunnels()
	result[0].Name = "mutated"

	original := store.GetAllTunnels()
	if original[0].Name != "original" {
		t.Error("GetAllTunnels 应返回副本，修改不应影响原始数据")
	}
}

func TestTunnelStore_ConcurrentAccess(t *testing.T) {
	store := newTestTunnelStore(t)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("tunnel-%d", idx)
			hostname := fmt.Sprintf("host-%d", idx%5)
			clientID := fmt.Sprintf("client-%d", idx)
			_ = store.AddTunnel(StoredTunnel{
				ProxyNewRequest: protocol.ProxyNewRequest{Name: name},
				ClientID:         clientID,
				Hostname:        hostname,
				Binding:         TunnelBindingClientID,
			})
			store.GetAllTunnels()
			store.GetTunnelsByHostname(hostname)
			store.GetTunnelsByClientID(clientID)
		}(i)
	}
	wg.Wait()
}
