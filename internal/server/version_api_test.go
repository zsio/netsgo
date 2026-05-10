package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"netsgo/internal/svcmgr"
	"netsgo/pkg/protocol"
	"netsgo/pkg/updater"
	buildversion "netsgo/pkg/version"
)

func TestAPI_VersionCheckUsesServerVersionAndReturnsStructuredResult(t *testing.T) {
	origVersion := buildversion.Current
	t.Cleanup(func() { buildversion.Current = origVersion })
	buildversion.Current = "v0.1.0-beta.5"

	s := New(8080)
	s.releaseIndexCache = newReleaseIndexCache(func() (*updater.ReleaseIndex, error) {
		return apiTestIndex("v0.1.0", "v0.1.0-beta.6"), nil
	})

	req := httptest.NewRequest(http.MethodGet, "/api/version/check", nil)
	w := httptest.NewRecorder()
	s.handleAPIVersionCheck(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code: want 200, got %d", w.Code)
	}

	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if result["target"] != "server" || result["target_id"] != "server" {
		t.Fatalf("target mismatch: %v", result)
	}
	if result["current_version"] != "v0.1.0-beta.5" {
		t.Fatalf("current_version mismatch: %v", result["current_version"])
	}
	if result["latest_version"] != "v0.1.0" {
		t.Fatalf("latest_version mismatch: %v", result["latest_version"])
	}
	if result["update_available"] != true {
		t.Fatalf("update_available mismatch: %v", result["update_available"])
	}
	if result["recommended_channel"] != "stable" {
		t.Fatalf("recommended_channel mismatch: %v", result["recommended_channel"])
	}
}

func TestAPI_VersionCheckFailureStillReturnsHTTP200(t *testing.T) {
	s := New(8080)
	s.releaseIndexCache = newReleaseIndexCache(func() (*updater.ReleaseIndex, error) {
		return nil, errors.New("network unavailable")
	})

	req := httptest.NewRequest(http.MethodGet, "/api/version/check?force=true", nil)
	w := httptest.NewRecorder()
	s.handleAPIVersionCheck(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code: want 200, got %d", w.Code)
	}
	var result struct {
		CheckFailed   bool   `json:"check_failed"`
		RefreshFailed bool   `json:"refresh_failed"`
		CacheSource   string `json:"cache_source"`
		Reason        string `json:"reason"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if !result.CheckFailed || !result.RefreshFailed || result.CacheSource != "none" || result.Reason != updater.ReasonReleaseIndexUnavailable {
		t.Fatalf("unexpected failure response: %+v", result)
	}
}

func TestVersionCheckCommandsOnlyForService(t *testing.T) {
	s := New(8080)
	s.releaseIndexCache = newReleaseIndexCache(func() (*updater.ReleaseIndex, error) {
		return apiTestIndex("v0.2.0", ""), nil
	})

	result := s.computeVersionCheck(false, "server", "server", "v0.1.0", updater.InstallMethodService)
	if result.Commands == nil || !strings.Contains(result.Commands.Domestic, "--source cnb --channel stable -y") {
		t.Fatalf("expected service upgrade commands, got %+v", result.Commands)
	}

	result = s.computeVersionCheck(false, "server", "server", "v0.1.0", updater.InstallMethodBinary)
	if result.Commands != nil || result.RecommendedAction != updater.RecommendedActionGitHubRelease {
		t.Fatalf("binary update should not include commands: %+v", result)
	}
}

func TestReleaseIndexCacheUsesStaleCacheOnRefreshFailure(t *testing.T) {
	calls := 0
	cache := newReleaseIndexCache(func() (*updater.ReleaseIndex, error) {
		calls++
		if calls == 1 {
			return apiTestIndex("v0.2.0", ""), nil
		}
		return nil, errors.New("network unavailable")
	})
	cache.ttl = time.Second

	first := cache.Get(false, time.Unix(10, 0))
	if first.cacheSource != "fresh" || first.refreshFailed {
		t.Fatalf("unexpected first cache snapshot: %+v", first)
	}

	second := cache.Get(true, time.Unix(12, 0))
	if second.cacheSource != "stale_cache" || !second.refreshFailed || second.index == nil {
		t.Fatalf("expected stale cache on refresh failure, got %+v", second)
	}
}

func TestReleaseIndexCacheUsesFreshCacheWithinTTL(t *testing.T) {
	calls := 0
	cache := newReleaseIndexCache(func() (*updater.ReleaseIndex, error) {
		calls++
		return apiTestIndex("v0.2.0", ""), nil
	})
	cache.ttl = time.Hour

	first := cache.Get(false, time.Unix(10, 0))
	second := cache.Get(false, time.Unix(20, 0))

	if first.cacheSource != "fresh" || second.cacheSource != "cache" {
		t.Fatalf("unexpected cache sources: first=%+v second=%+v", first, second)
	}
	if calls != 1 {
		t.Fatalf("expected one fetch within TTL, got %d", calls)
	}
}

func TestReleaseIndexCacheForceCooldownReturnsCache(t *testing.T) {
	calls := 0
	cache := newReleaseIndexCache(func() (*updater.ReleaseIndex, error) {
		calls++
		return apiTestIndex("v0.2.0", ""), nil
	})
	cache.forceCooldown = 10 * time.Second

	first := cache.Get(true, time.Unix(10, 0))
	second := cache.Get(true, time.Unix(15, 0))

	if first.cacheSource != "fresh" || second.cacheSource != "cache" {
		t.Fatalf("unexpected force cooldown sources: first=%+v second=%+v", first, second)
	}
	if calls != 1 {
		t.Fatalf("expected force cooldown to suppress refetch, got %d calls", calls)
	}
}

func TestReleaseIndexCacheForceCooldownReturnsFailureWithoutCache(t *testing.T) {
	calls := 0
	cache := newReleaseIndexCache(func() (*updater.ReleaseIndex, error) {
		calls++
		return nil, errors.New("network unavailable")
	})
	cache.forceCooldown = 10 * time.Second

	first := cache.Get(true, time.Unix(10, 0))
	second := cache.Get(true, time.Unix(15, 0))

	if first.cacheSource != "none" || !first.refreshFailed {
		t.Fatalf("unexpected first force failure: %+v", first)
	}
	if second.cacheSource != "none" || !second.refreshFailed {
		t.Fatalf("expected force cooldown to reuse failure snapshot, got %+v", second)
	}
	if calls != 1 {
		t.Fatalf("expected failed force cooldown to suppress refetch, got %d calls", calls)
	}
}

func TestReleaseIndexCacheCoalescesConcurrentRefresh(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	calls := 0
	cache := newReleaseIndexCache(func() (*updater.ReleaseIndex, error) {
		calls++
		close(started)
		<-release
		return apiTestIndex("v0.2.0", ""), nil
	})

	var wg sync.WaitGroup
	results := make(chan releaseIndexSnapshot, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- cache.Get(false, time.Unix(10, 0))
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(results)

	var snapshots []releaseIndexSnapshot
	for snap := range results {
		snapshots = append(snapshots, snap)
	}
	if calls != 1 {
		t.Fatalf("expected concurrent refresh to be coalesced, got %d calls", calls)
	}
	if len(snapshots) != 2 {
		t.Fatalf("expected two snapshots, got %d", len(snapshots))
	}
	for _, snap := range snapshots {
		if snap.index == nil {
			t.Fatalf("expected cached index for every waiter, got %+v", snap)
		}
		if snap.cacheSource != "fresh" && snap.cacheSource != "cache" {
			t.Fatalf("expected fresh/cache source for coalesced waiters, got %+v", snapshots)
		}
	}
}

func TestReleaseIndexCacheCoalescedRefreshFailurePreservesRefreshFailed(t *testing.T) {
	cache := newReleaseIndexCache(func() (*updater.ReleaseIndex, error) {
		return apiTestIndex("v0.2.0", ""), nil
	})
	cache.ttl = time.Second
	first := cache.Get(false, time.Unix(10, 0))
	if first.cacheSource != "fresh" || first.refreshFailed {
		t.Fatalf("unexpected first cache snapshot: %+v", first)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	calls := 0
	cache.fetch = func() (*updater.ReleaseIndex, error) {
		calls++
		close(started)
		<-release
		return nil, errors.New("network unavailable")
	}

	var wg sync.WaitGroup
	results := make(chan releaseIndexSnapshot, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- cache.Get(true, time.Unix(12, 0))
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(results)

	if calls != 1 {
		t.Fatalf("expected concurrent refresh failure to be coalesced, got %d calls", calls)
	}
	for snap := range results {
		if snap.cacheSource != "stale_cache" || !snap.refreshFailed || snap.index == nil {
			t.Fatalf("expected every waiter to see stale refresh failure, got %+v", snap)
		}
	}
}

func TestAPI_ClientVersionCheckOfflineDoesNotFetchReleaseIndex(t *testing.T) {
	s := New(8080)
	s.clients.Store("client-1", &ClientConn{
		ID: "client-1",
		Info: protocol.ClientInfo{
			Version: "v0.1.0",
		},
		state: clientStatePendingData,
	})
	s.releaseIndexCache = newReleaseIndexCache(func() (*updater.ReleaseIndex, error) {
		t.Fatal("offline client version check must not fetch release index")
		return nil, nil
	})

	req := httptest.NewRequest(http.MethodGet, "/api/clients/client-1/version/check?force=true", nil)
	req.SetPathValue("id", "client-1")
	w := httptest.NewRecorder()
	s.handleAPIClientVersionCheck(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status code: want 200, got %d", w.Code)
	}
	var result versionCheckResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if !result.CheckFailed || result.Reason != updater.ReasonClientOffline || result.CurrentVersion != "v0.1.0" {
		t.Fatalf("unexpected offline client response: %+v", result)
	}
}

func TestInstallMethodFromClientInfoDefaultsToBinary(t *testing.T) {
	got := installMethodFromClientInfo(protocol.ClientInfo{})
	if got != updater.InstallMethodBinary {
		t.Fatalf("expected missing client capability to default to binary, got %q", got)
	}
}

func TestServerUpdateCapabilityUsesCachedValue(t *testing.T) {
	detectCalls := 0
	s := New(8080)
	s.updateCapabilityCache = newUpdateCapabilityCache(func(svcmgr.Role) string {
		detectCalls++
		return updater.InstallMethodService
	})

	got := s.serverUpdateCapability(time.Unix(100, 0))
	if got == nil || got.InstallMethod != updater.InstallMethodService {
		t.Fatalf("expected detected service install method, got %+v", got)
	}

	got.InstallMethod = updater.InstallMethodDocker
	again := s.serverUpdateCapability(time.Unix(101, 0))
	if again == nil || again.InstallMethod != updater.InstallMethodService {
		t.Fatalf("cached capability should be returned as a defensive copy, got %+v", again)
	}
	if detectCalls != 1 {
		t.Fatalf("expected cached call to avoid repeating detection, got %d calls", detectCalls)
	}
}

func apiTestIndex(stable, beta string) *updater.ReleaseIndex {
	channels := map[string]updater.ReleaseChannel{}
	if stable != "" {
		channels["stable"] = updater.ReleaseChannel{Latest: stable}
	}
	if beta != "" {
		channels["beta"] = updater.ReleaseChannel{Latest: beta}
	}
	return &updater.ReleaseIndex{Schema: 1, Project: "netsgo", Channels: channels}
}
