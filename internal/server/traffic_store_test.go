package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func newTestTrafficStore(t *testing.T) (*TrafficStore, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "traffic_test_*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	ts, err := NewTrafficStore(filepath.Join(dir, "traffic.json"))
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("创建 TrafficStore 失败: %v", err)
	}
	return ts, func() { os.RemoveAll(dir) }
}

func mustSingleSeries(t *testing.T, result TrafficQueryResult, tunnelName string) TunnelTrafficSeries {
	t.Helper()
	if len(result.Items) != 1 {
		t.Fatalf("期望 1 条 series，得到 %d", len(result.Items))
	}
	if result.Items[0].TunnelName != tunnelName {
		t.Fatalf("期望 tunnel=%s，得到 %s", tunnelName, result.Items[0].TunnelName)
	}
	return result.Items[0]
}

func findSeries(t *testing.T, result TrafficQueryResult, tunnelName string) TunnelTrafficSeries {
	t.Helper()
	for _, item := range result.Items {
		if item.TunnelName == tunnelName {
			return item
		}
	}
	t.Fatalf("未找到 tunnel=%s", tunnelName)
	return TunnelTrafficSeries{}
}

func findSeriesWithType(t *testing.T, result TrafficQueryResult, tunnelName, tunnelType string) TunnelTrafficSeries {
	t.Helper()
	for _, item := range result.Items {
		if item.TunnelName == tunnelName && item.TunnelType == tunnelType {
			return item
		}
	}
	t.Fatalf("未找到 tunnel=%s type=%s", tunnelName, tunnelType)
	return TunnelTrafficSeries{}
}

func TestTrafficStore_RecordAndQuery(t *testing.T) {
	ts, cleanup := newTestTrafficStore(t)
	defer cleanup()

	now := time.Now().UTC()
	minuteStart := minuteFloorUTC(now)
	ts.ApplyDeltas([]TrafficDelta{
		{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: minuteStart.Unix(), IngressBytes: 100, EgressBytes: 200},
		{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: minuteStart.Unix(), IngressBytes: 50, EgressBytes: 75},
		{ClientID: "c1", TunnelName: "tun2", TunnelType: "udp", MinuteStart: minuteStart.Unix(), IngressBytes: 10, EgressBytes: 20},
		{ClientID: "c2", TunnelName: "tun1", TunnelType: "http", MinuteStart: minuteStart.Unix(), IngressBytes: 999, EgressBytes: 0},
	})

	from := now.Add(-time.Minute)
	to := now.Add(time.Minute)

	got := ts.QueryWithResolution("c1", "", from, to, TrafficResolutionMinute)
	if got.Resolution != TrafficResolutionMinute {
		t.Fatalf("期望 minute resolution，得到 %s", got.Resolution)
	}
	if len(got.Items) != 2 {
		t.Fatalf("期望 2 条隧道，得到 %d", len(got.Items))
	}

	tun1 := findSeries(t, got, "tun1")
	if len(tun1.Points) != 1 {
		t.Fatalf("tun1 期望 1 个点，得到 %d", len(tun1.Points))
	}
	if tun1.Points[0].IngressBytes != 150 {
		t.Errorf("tun1 ingress 期望 150，得到 %d", tun1.Points[0].IngressBytes)
	}
	if tun1.Points[0].EgressBytes != 275 {
		t.Errorf("tun1 egress 期望 275，得到 %d", tun1.Points[0].EgressBytes)
	}
	if tun1.Points[0].TotalBytes != 425 {
		t.Errorf("tun1 total 期望 425，得到 %d", tun1.Points[0].TotalBytes)
	}

	tun2 := findSeries(t, got, "tun2")
	if len(tun2.Points) != 1 || tun2.Points[0].IngressBytes != 10 {
		t.Errorf("tun2 ingress 期望 10，得到 %+v", tun2.Points)
	}

	gotC2 := ts.QueryWithResolution("c2", "", from, to, TrafficResolutionMinute)
	c2Tun1 := mustSingleSeries(t, gotC2, "tun1")
	if c2Tun1.Points[0].IngressBytes != 999 {
		t.Errorf("c2 tun1 ingress 期望 999，得到 %d", c2Tun1.Points[0].IngressBytes)
	}
}

func TestTrafficStore_TunnelFilter(t *testing.T) {
	ts, cleanup := newTestTrafficStore(t)
	defer cleanup()

	now := time.Now().UTC()
	minuteStart := minuteFloorUTC(now)
	ts.ApplyDeltas([]TrafficDelta{
		{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: minuteStart.Unix(), IngressBytes: 100},
		{ClientID: "c1", TunnelName: "tun2", TunnelType: "udp", MinuteStart: minuteStart.Unix(), IngressBytes: 200},
	})

	got := ts.QueryWithResolution("c1", "tun1", now.Add(-time.Minute), now.Add(time.Minute), TrafficResolutionMinute)
	series := mustSingleSeries(t, got, "tun1")
	if series.Points[0].IngressBytes != 100 {
		t.Errorf("tun1 ingress 期望 100，得到 %d", series.Points[0].IngressBytes)
	}
}

func TestTrafficStore_QuerySeparatesSameNameDifferentTypes(t *testing.T) {
	ts, cleanup := newTestTrafficStore(t)
	defer cleanup()

	now := time.Now().UTC()
	minuteStart := minuteFloorUTC(now)
	ts.ApplyDeltas([]TrafficDelta{
		{ClientID: "c1", TunnelName: "shared", TunnelType: "tcp", MinuteStart: minuteStart.Unix(), IngressBytes: 100, EgressBytes: 10},
		{ClientID: "c1", TunnelName: "shared", TunnelType: "http", MinuteStart: minuteStart.Unix(), IngressBytes: 200, EgressBytes: 20},
	})

	got := ts.QueryWithResolution("c1", "shared", now.Add(-time.Minute), now.Add(time.Minute), TrafficResolutionMinute)
	if len(got.Items) != 2 {
		t.Fatalf("期望 2 条同名不同类型 series，得到 %d", len(got.Items))
	}

	tcpSeries := findSeriesWithType(t, got, "shared", "tcp")
	if tcpSeries.Points[0].IngressBytes != 100 || tcpSeries.Points[0].EgressBytes != 10 {
		t.Fatalf("tcp series 聚合错误: %+v", tcpSeries.Points[0])
	}

	httpSeries := findSeriesWithType(t, got, "shared", "http")
	if httpSeries.Points[0].IngressBytes != 200 || httpSeries.Points[0].EgressBytes != 20 {
		t.Fatalf("http series 聚合错误: %+v", httpSeries.Points[0])
	}
}

func TestTrafficStore_RollupAndHourQuery(t *testing.T) {
	ts, cleanup := newTestTrafficStore(t)
	defer cleanup()

	now := time.Now().UTC()
	baseHour := hourFloorUTC(now.Add(-2 * time.Hour))
	ts.ApplyDeltas([]TrafficDelta{
		{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: baseHour.Add(2 * time.Minute).Unix(), IngressBytes: 10, EgressBytes: 5},
		{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: baseHour.Add(3 * time.Minute).Unix(), IngressBytes: 20, EgressBytes: 10},
		{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: baseHour.Add(4 * time.Minute).Unix(), IngressBytes: 30, EgressBytes: 15},
	})

	ts.Compact(now)

	got := ts.QueryWithResolution("c1", "tun1", baseHour, now, TrafficResolutionHour)
	series := mustSingleSeries(t, got, "tun1")
	if len(series.Points) != 1 {
		t.Fatalf("hour 查询期望 1 个点，得到 %d", len(series.Points))
	}
	if !series.Points[0].BucketStart.Equal(baseHour) {
		t.Fatalf("hour bucket 起点错误，期望 %s，得到 %s", baseHour, series.Points[0].BucketStart)
	}
	if series.Points[0].IngressBytes != 60 {
		t.Errorf("hour ingress 期望 60，得到 %d", series.Points[0].IngressBytes)
	}
	if series.Points[0].EgressBytes != 30 {
		t.Errorf("hour egress 期望 30，得到 %d", series.Points[0].EgressBytes)
	}
}

func TestTrafficStore_Eviction(t *testing.T) {
	ts, cleanup := newTestTrafficStore(t)
	defer cleanup()

	now := time.Now().UTC()
	oldMinute := minuteFloorUTC(now.Add(-25 * time.Hour))
	recentMinute := minuteFloorUTC(now.Add(-30 * time.Minute))
	oldHour := hourFloorUTC(now.Add(-(trafficHourRetention + time.Hour)))
	recentHour := hourFloorUTC(now.Add(-2 * time.Hour))

	ts.ApplyDeltas([]TrafficDelta{
		{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: oldMinute.Unix(), IngressBytes: 1, EgressBytes: 1},
		{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: recentMinute.Unix(), IngressBytes: 2, EgressBytes: 2},
	})

	ts.mu.Lock()
	ts.hourBuckets[trafficBucketKey(TrafficBucket{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", Resolution: TrafficResolutionHour, BucketStart: oldHour.Unix()})] = TrafficBucket{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", Resolution: TrafficResolutionHour, BucketStart: oldHour.Unix(), IngressBytes: 3, EgressBytes: 3}
	ts.hourBuckets[trafficBucketKey(TrafficBucket{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", Resolution: TrafficResolutionHour, BucketStart: recentHour.Unix()})] = TrafficBucket{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", Resolution: TrafficResolutionHour, BucketStart: recentHour.Unix(), IngressBytes: 4, EgressBytes: 4}
	ts.mu.Unlock()

	ts.Compact(now)

	minuteResult := ts.QueryWithResolution("c1", "tun1", now.Add(-26*time.Hour), now, TrafficResolutionMinute)
	series := mustSingleSeries(t, minuteResult, "tun1")
	if len(series.Points) != 1 {
		t.Fatalf("分钟桶驱逐后期望仅剩 1 个点，得到 %d", len(series.Points))
	}
	if series.Points[0].IngressBytes != 2 {
		t.Errorf("保留的分钟桶 ingress 期望 2，得到 %d", series.Points[0].IngressBytes)
	}

	hourResult := ts.QueryWithResolution("c1", "tun1", now.Add(-(trafficHourRetention + 2*time.Hour)), now, TrafficResolutionHour)
	hourSeries := mustSingleSeries(t, hourResult, "tun1")
	if len(hourSeries.Points) == 0 {
		t.Fatal("小时桶查询应至少保留近期数据")
	}
	for _, point := range hourSeries.Points {
		if point.BucketStart.Equal(oldHour) {
			t.Fatal("超出保留期的小时桶应已被驱逐")
		}
	}
}

func TestTrafficStore_FlushAndReload(t *testing.T) {
	dir, err := os.MkdirTemp("", "traffic_reload_*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "traffic.json")
	ts, err := NewTrafficStore(path)
	if err != nil {
		t.Fatalf("创建 TrafficStore 失败: %v", err)
	}

	now := time.Now().UTC()
	ts.ApplyDeltas([]TrafficDelta{{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: minuteFloorUTC(now).Unix(), IngressBytes: 500, EgressBytes: 300}})

	if err := ts.Flush(); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Flush 后文件不存在: %v", err)
	}

	ts2, err := NewTrafficStore(path)
	if err != nil {
		t.Fatalf("重新加载 TrafficStore 失败: %v", err)
	}

	got := ts2.QueryWithResolution("c1", "tun1", now.Add(-time.Minute), now.Add(time.Minute), TrafficResolutionMinute)
	series := mustSingleSeries(t, got, "tun1")
	if series.Points[0].IngressBytes != 500 {
		t.Errorf("重新加载后 ingress 期望 500，得到 %d", series.Points[0].IngressBytes)
	}
	if series.Points[0].EgressBytes != 300 {
		t.Errorf("重新加载后 egress 期望 300，得到 %d", series.Points[0].EgressBytes)
	}
}

func TestTrafficStore_EvictTunnelAndClient(t *testing.T) {
	ts, cleanup := newTestTrafficStore(t)
	defer cleanup()

	now := time.Now().UTC()
	minuteStart := minuteFloorUTC(now).Unix()
	ts.ApplyDeltas([]TrafficDelta{
		{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: minuteStart, IngressBytes: 100},
		{ClientID: "c1", TunnelName: "tun2", TunnelType: "udp", MinuteStart: minuteStart, IngressBytes: 200},
		{ClientID: "c2", TunnelName: "tun1", TunnelType: "http", MinuteStart: minuteStart, IngressBytes: 300},
	})

	ts.EvictTunnel("c1", "tun1")

	got := ts.QueryWithResolution("c1", "", now.Add(-time.Minute), now.Add(time.Minute), TrafficResolutionMinute)
	if len(got.Items) != 1 || got.Items[0].TunnelName != "tun2" {
		t.Fatalf("EvictTunnel 后 c1 应仅剩 tun2，得到 %+v", got.Items)
	}

	ts.EvictClient("c1")
	got2 := ts.QueryWithResolution("c1", "", now.Add(-time.Minute), now.Add(time.Minute), TrafficResolutionMinute)
	if len(got2.Items) != 0 {
		t.Errorf("EvictClient 后 c1 的数据应为空，得到 %d 条", len(got2.Items))
	}

	got3 := ts.QueryWithResolution("c2", "", now.Add(-time.Minute), now.Add(time.Minute), TrafficResolutionMinute)
	mustSingleSeries(t, got3, "tun1")
}

func TestTrafficStore_AutoResolutionBoundary(t *testing.T) {
	ts, cleanup := newTestTrafficStore(t)
	defer cleanup()

	now := time.Now().UTC()
	minuteStart := minuteFloorUTC(now).Unix()
	ts.ApplyDeltas([]TrafficDelta{{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: minuteStart, IngressBytes: 1}})

	minuteRange := ts.Query("c1", "tun1", now.Add(-24*time.Hour), now)
	if minuteRange.Resolution != TrafficResolutionMinute {
		t.Fatalf("刚好 24h 范围应使用 minute，得到 %s", minuteRange.Resolution)
	}

	hourRange := ts.Query("c1", "tun1", now.Add(-(24*time.Hour + time.Second)), now)
	if hourRange.Resolution != TrafficResolutionHour {
		t.Fatalf("超过 24h 范围应使用 hour，得到 %s", hourRange.Resolution)
	}
}

func TestTrafficStore_HourQueryIncludesCurrentHourFromMinuteBuckets(t *testing.T) {
	ts, cleanup := newTestTrafficStore(t)
	defer cleanup()

	now := time.Now().UTC()
	currentHour := hourFloorUTC(now)
	completedHour := currentHour.Add(-time.Hour)

	ts.ApplyDeltas([]TrafficDelta{
		{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: completedHour.Add(5 * time.Minute).Unix(), IngressBytes: 10, EgressBytes: 5},
		{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: currentHour.Add(2 * time.Minute).Unix(), IngressBytes: 20, EgressBytes: 7},
	})
	ts.Compact(now)

	result := ts.QueryWithResolution("c1", "tun1", completedHour, now.Add(time.Minute), TrafficResolutionHour)
	series := mustSingleSeries(t, result, "tun1")
	if len(series.Points) != 2 {
		t.Fatalf("应同时返回已完成小时和当前小时折叠数据，得到 %d 个点", len(series.Points))
	}
	if !series.Points[0].BucketStart.Equal(completedHour) || series.Points[0].IngressBytes != 10 {
		t.Fatalf("已完成小时聚合错误: %+v", series.Points[0])
	}
	if !series.Points[1].BucketStart.Equal(currentHour) || series.Points[1].IngressBytes != 20 {
		t.Fatalf("当前小时折叠错误: %+v", series.Points[1])
	}
}

func TestTrafficStore_HourQueryDoesNotDoubleCountRolledUpHours(t *testing.T) {
	ts, cleanup := newTestTrafficStore(t)
	defer cleanup()

	now := time.Now().UTC()
	completedHour := hourFloorUTC(now.Add(-2 * time.Hour))

	ts.ApplyDeltas([]TrafficDelta{
		{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: completedHour.Add(1 * time.Minute).Unix(), IngressBytes: 11, EgressBytes: 3},
		{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: completedHour.Add(2 * time.Minute).Unix(), IngressBytes: 13, EgressBytes: 4},
	})
	ts.Compact(now)

	result := ts.QueryWithResolution("c1", "tun1", completedHour, now, TrafficResolutionHour)
	series := mustSingleSeries(t, result, "tun1")
	if len(series.Points) != 1 {
		t.Fatalf("已 rollup 小时不应因 minute/hour 共存而重复，得到 %d 个点", len(series.Points))
	}
	if series.Points[0].IngressBytes != 24 || series.Points[0].EgressBytes != 7 {
		t.Fatalf("hour 去重聚合错误: %+v", series.Points[0])
	}
}

func TestTrafficAPI_Query(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	trafficDir, err := os.MkdirTemp("", "traffic_api_*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(trafficDir)

	ts, err := NewTrafficStore(filepath.Join(trafficDir, "traffic.json"))
	if err != nil {
		t.Fatalf("创建 TrafficStore 失败: %v", err)
	}
	s.trafficStore = ts

	clientID := "test-client-001"
	now := time.Now().UTC()
	ts.ApplyDeltas([]TrafficDelta{{ClientID: clientID, TunnelName: "web", TunnelType: "http", MinuteStart: minuteFloorUTC(now).Unix(), IngressBytes: 1024, EgressBytes: 512}})

	from := now.Add(-time.Minute).Unix()
	to := now.Add(time.Minute).Unix()

	path := "/api/clients/" + clientID + "/traffic?from=" + itoa(from) + "&to=" + itoa(to)
	w := doMuxRequest(t, handler, http.MethodGet, path, token, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，得到 %d，body: %s", w.Code, w.Body.String())
	}

	var resp TrafficQueryResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	if resp.Resolution != TrafficResolutionMinute {
		t.Errorf("期望 resolution=minute，得到 %q", resp.Resolution)
	}

	web := mustSingleSeries(t, resp, "web")
	if len(web.Points) == 0 {
		t.Fatal("期望有 web 数据点")
	}
	if web.Points[0].IngressBytes != 1024 {
		t.Errorf("web ingress 期望 1024，得到 %d", web.Points[0].IngressBytes)
	}
	if web.Points[0].EgressBytes != 512 {
		t.Errorf("web egress 期望 512，得到 %d", web.Points[0].EgressBytes)
	}
}

func TestTrafficAPI_Unauthorized(t *testing.T) {
	_, handler, _, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	w := doMuxRequest(t, handler, http.MethodGet, "/api/clients/c1/traffic", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("未授权应返回 401，得到 %d", w.Code)
	}
}

func TestTrafficAPI_DefaultTimeRange(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	trafficDir, err := os.MkdirTemp("", "traffic_default_*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(trafficDir)

	ts, err := NewTrafficStore(filepath.Join(trafficDir, "traffic.json"))
	if err != nil {
		t.Fatalf("创建 TrafficStore 失败: %v", err)
	}
	s.trafficStore = ts

	w := doMuxRequest(t, handler, http.MethodGet, "/api/clients/c1/traffic", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("期望 200，得到 %d", w.Code)
	}

	var resp TrafficQueryResult
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("解析默认时间范围响应失败: %v", err)
	}
	if resp.Resolution != TrafficResolutionMinute {
		t.Fatalf("默认 24h 时间范围应为 minute，得到 %s", resp.Resolution)
	}
}

func TestTrafficAPI_InvalidResolution(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	trafficDir, err := os.MkdirTemp("", "traffic_res_*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(trafficDir)

	ts, err := NewTrafficStore(filepath.Join(trafficDir, "traffic.json"))
	if err != nil {
		t.Fatalf("创建 TrafficStore 失败: %v", err)
	}
	s.trafficStore = ts

	w := doMuxRequest(t, handler, http.MethodGet, "/api/clients/c1/traffic?resolution=bad", token, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("非法 resolution 期望 400，得到 %d", w.Code)
	}
}

func TestTrafficAPI_InvalidTimeRange(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	trafficDir, err := os.MkdirTemp("", "traffic_range_*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(trafficDir)

	ts, err := NewTrafficStore(filepath.Join(trafficDir, "traffic.json"))
	if err != nil {
		t.Fatalf("创建 TrafficStore 失败: %v", err)
	}
	s.trafficStore = ts

	now := time.Now().UTC()
	from := now.Unix()
	to := now.Add(-time.Minute).Unix()
	w := doMuxRequest(t, handler, http.MethodGet, "/api/clients/c1/traffic?from="+itoa(from)+"&to="+itoa(to), token, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("from > to 应返回 400，得到 %d", w.Code)
	}
}

func TestTrafficAPI_TimeRangeTooLarge(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	trafficDir, err := os.MkdirTemp("", "traffic_range_large_*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(trafficDir)

	ts, err := NewTrafficStore(filepath.Join(trafficDir, "traffic.json"))
	if err != nil {
		t.Fatalf("创建 TrafficStore 失败: %v", err)
	}
	s.trafficStore = ts

	now := time.Now().UTC()
	from := now.Add(-(trafficMaxRange + time.Hour)).Unix()
	to := now.Unix()
	w := doMuxRequest(t, handler, http.MethodGet, "/api/clients/c1/traffic?from="+itoa(from)+"&to="+itoa(to), token, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("超过 7 天范围应返回 400，得到 %d", w.Code)
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
