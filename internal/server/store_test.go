package server

import (
	"errors"
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
	if tunnel.DesiredState == "" {
		tunnel.DesiredState = protocol.ProxyDesiredStateRunning
	}
	if tunnel.RuntimeState == "" {
		tunnel.RuntimeState = protocol.ProxyRuntimeStateExposed
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
		DesiredState: protocol.ProxyDesiredStateRunning,
		RuntimeState: protocol.ProxyRuntimeStateExposed,
		ClientID:     "client-1",
		Hostname:     "host-1",
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

func TestTunnelStore_LoadExistingStatesKeepsDesiredAndRuntimeState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tunnels.json")

	store1, err := NewTunnelStore(path)
	if err != nil {
		t.Fatalf("NewTunnelStore 失败: %v", err)
	}
	mustAddStableTunnel(t, store1, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			Name: "legacy-active", Type: protocol.ProxyTypeTCP, LocalIP: "127.0.0.1", LocalPort: 80, RemotePort: 8080,
		},
		DesiredState: protocol.ProxyDesiredStateRunning,
		RuntimeState: protocol.ProxyRuntimeStateExposed,
		ClientID:     "client-1",
		Hostname:     "host-1",
	})
	mustAddStableTunnel(t, store1, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{
			Name: "legacy-error", Type: protocol.ProxyTypeUDP, LocalIP: "127.0.0.1", LocalPort: 53, RemotePort: 8053,
		},
		DesiredState: protocol.ProxyDesiredStateRunning,
		RuntimeState: protocol.ProxyRuntimeStateError,
		Error:        "restore failed",
		ClientID:     "client-1",
		Hostname:     "host-1",
	})

	store2, err := NewTunnelStore(path)
	if err != nil {
		t.Fatalf("重新加载失败: %v", err)
	}

	active, ok := store2.GetTunnel("client-1", "legacy-active")
	if !ok {
		t.Fatal("应找到 legacy-active")
	}
	if active.DesiredState != protocol.ProxyDesiredStateRunning {
		t.Fatalf("legacy active desired_state 期望 running，得到 %s", active.DesiredState)
	}
	if active.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("legacy active runtime_state 期望 exposed，得到 %s", active.RuntimeState)
	}

	errored, ok := store2.GetTunnel("client-1", "legacy-error")
	if !ok {
		t.Fatal("应找到 legacy-error")
	}
	if errored.DesiredState != protocol.ProxyDesiredStateRunning {
		t.Fatalf("legacy error desired_state 期望 running，得到 %s", errored.DesiredState)
	}
	if errored.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("legacy error runtime_state 期望 error，得到 %s", errored.RuntimeState)
	}
	if errored.Error != "restore failed" {
		t.Fatalf("legacy error 错误原因期望保留 restore failed，得到 %q", errored.Error)
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
		ClientID:        "client-1",
		Hostname:        "myhost",
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
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
		ClientID:        "client-1",
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
		ClientID:        "client-A",
		Hostname:        "host-A",
	})
	if err := store.AddTunnel(StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "web"},
		ClientID:        "client-B",
		Hostname:        "host-B",
		Binding:         TunnelBindingClientID,
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
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
		ClientID:        "client-1",
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

func TestTunnelStore_UpdateStates(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t1"},
		ClientID:        "client-1",
		Hostname:        "host",
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
	})

	if err := store.UpdateStates("client-1", "t1", protocol.ProxyDesiredStatePaused, protocol.ProxyRuntimeStateIdle, ""); err != nil {
		t.Fatalf("UpdateStates 失败: %v", err)
	}
	st, _ := store.GetTunnel("client-1", "t1")
	if st.DesiredState != protocol.ProxyDesiredStatePaused || st.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Errorf("状态期望 paused/idle，得到 %s/%s", st.DesiredState, st.RuntimeState)
	}

	if err := store.UpdateStates("client-1", "t1", protocol.ProxyDesiredStateStopped, protocol.ProxyRuntimeStateIdle, ""); err != nil {
		t.Fatalf("UpdateStates 失败: %v", err)
	}
	st2, _ := store.GetTunnel("client-1", "t1")
	if st2.DesiredState != protocol.ProxyDesiredStateStopped || st2.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Errorf("状态期望 stopped/idle，得到 %s/%s", st2.DesiredState, st2.RuntimeState)
	}
}

func TestTunnelStore_UpdateStates_NotFound(t *testing.T) {
	store := newTestTunnelStore(t)
	if err := store.UpdateStates("ghost", "no-tunnel", protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateExposed, ""); err == nil {
		t.Error("更新不存在的隧道应返回错误")
	}
}

func TestTunnelStore_UpdateState_ErrorRoundTrip(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t-error"},
		ClientID:        "client-1",
		Hostname:        "host",
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
	})

	if err := store.UpdateStates("client-1", "t-error", protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, "restore failed"); err != nil {
		t.Fatalf("UpdateStates 设置 error 失败: %v", err)
	}
	st, _ := store.GetTunnel("client-1", "t-error")
	if st.DesiredState != protocol.ProxyDesiredStateRunning || st.RuntimeState != protocol.ProxyRuntimeStateError {
		t.Fatalf("状态期望 running/error，得到 %s/%s", st.DesiredState, st.RuntimeState)
	}
	if st.Error != "restore failed" {
		t.Fatalf("错误原因期望 %q，得到 %q", "restore failed", st.Error)
	}

	if err := store.UpdateStates("client-1", "t-error", protocol.ProxyDesiredStatePaused, protocol.ProxyRuntimeStateIdle, ""); err != nil {
		t.Fatalf("UpdateStates 清理 error 失败: %v", err)
	}
	st, _ = store.GetTunnel("client-1", "t-error")
	if st.DesiredState != protocol.ProxyDesiredStatePaused || st.RuntimeState != protocol.ProxyRuntimeStateIdle {
		t.Fatalf("状态期望 paused/idle，得到 %s/%s", st.DesiredState, st.RuntimeState)
	}
	if st.Error != "" {
		t.Fatalf("paused 状态下错误原因应清空，得到 %q", st.Error)
	}
}

func TestTunnelStore_UpdateState_RollbackOnSaveFailure(t *testing.T) {
	store := newTestTunnelStore(t)

	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t-rollback"},
		ClientID:        "client-1",
		Hostname:        "host",
		DesiredState:    protocol.ProxyDesiredStateRunning,
		RuntimeState:    protocol.ProxyRuntimeStateExposed,
	})

	store.mu.Lock()
	store.failSaveErr = errors.New("injected save failure")
	store.failSaveCount = 1
	store.mu.Unlock()

	if err := store.UpdateStates("client-1", "t-rollback", protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, "boom"); err == nil {
		t.Fatal("预期 UpdateStates 在注入 save 失败时返回错误")
	}

	st, _ := store.GetTunnel("client-1", "t-rollback")
	if st.DesiredState != protocol.ProxyDesiredStateRunning || st.RuntimeState != protocol.ProxyRuntimeStateExposed {
		t.Fatalf("save 失败时状态应回滚为 running/exposed，得到 %s/%s", st.DesiredState, st.RuntimeState)
	}
	if st.Error != "" {
		t.Fatalf("save 失败时错误字段应保持为空，得到 %q", st.Error)
	}

	if err := store.UpdateStates("client-1", "t-rollback", protocol.ProxyDesiredStateRunning, protocol.ProxyRuntimeStateError, "boom"); err != nil {
		t.Fatalf("注入失败耗尽后 UpdateStates 应成功，得到: %v", err)
	}
	st, _ = store.GetTunnel("client-1", "t-rollback")
	if st.DesiredState != protocol.ProxyDesiredStateRunning || st.RuntimeState != protocol.ProxyRuntimeStateError || st.Error != "boom" {
		t.Fatalf("最终状态应为 running/error + boom，得到 state=%s/%s error=%q", st.DesiredState, st.RuntimeState, st.Error)
	}
}

func TestTunnelStore_UpdateClientID(t *testing.T) {
	store := newTestTunnelStore(t)

	seedLegacyTunnels(t, store,
		StoredTunnel{
			ProxyNewRequest: protocol.ProxyNewRequest{Name: "t1"},
			Hostname:        "host",
			DesiredState:    protocol.ProxyDesiredStateRunning,
			RuntimeState:    protocol.ProxyRuntimeStateExposed,
			Binding:         TunnelBindingLegacyHostname,
		},
		StoredTunnel{
			ProxyNewRequest: protocol.ProxyNewRequest{Name: "t2"},
			Hostname:        "host",
			DesiredState:    protocol.ProxyDesiredStatePaused,
			RuntimeState:    protocol.ProxyRuntimeStateIdle,
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
		ClientID:        "client-1",
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
		ClientID:        "client-1",
		Hostname:        "host-A",
	})
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t2"},
		ClientID:        "client-2",
		Hostname:        "host-A",
	})
	mustAddStableTunnel(t, store, StoredTunnel{
		ProxyNewRequest: protocol.ProxyNewRequest{Name: "t3"},
		ClientID:        "client-3",
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
		ClientID:        "client-1",
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
				ClientID:        clientID,
				Hostname:        hostname,
				Binding:         TunnelBindingClientID,
				DesiredState:    protocol.ProxyDesiredStateRunning,
				RuntimeState:    protocol.ProxyRuntimeStateExposed,
			})
			store.GetAllTunnels()
			store.GetTunnelsByHostname(hostname)
			store.GetTunnelsByClientID(clientID)
		}(i)
	}
	wg.Wait()
}
