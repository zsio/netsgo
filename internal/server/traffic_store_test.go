package server

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func newTestTrafficStore(t *testing.T) (*TrafficStore, func()) {
	t.Helper()

	ts, err := NewTrafficStore(filepath.Join(t.TempDir(), serverDBFileName))
	if err != nil {
		t.Fatalf("Failed to create TrafficStore: %v", err)
	}
	return ts, func() { _ = ts.Close() }
}

func mustSingleSeries(t *testing.T, result TrafficQueryResult, tunnelName string) TunnelTrafficSeries {
	t.Helper()
	if len(result.Items) != 1 {
		t.Fatalf("Expected 1 series, got %d", len(result.Items))
	}
	if result.Items[0].TunnelName != tunnelName {
		t.Fatalf("Expected tunnel=%s, got %s", tunnelName, result.Items[0].TunnelName)
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
	t.Fatalf("Tunnel=%s not found", tunnelName)
	return TunnelTrafficSeries{}
}

func findSeriesWithType(t *testing.T, result TrafficQueryResult, tunnelName, tunnelType string) TunnelTrafficSeries {
	t.Helper()
	for _, item := range result.Items {
		if item.TunnelName == tunnelName && item.TunnelType == tunnelType {
			return item
		}
	}
	t.Fatalf("Tunnel=%s type=%s not found", tunnelName, tunnelType)
	return TunnelTrafficSeries{}
}

func mustInsertTrafficBucket(t *testing.T, store *TrafficStore, bucket TrafficBucket) {
	t.Helper()

	if _, err := store.db.Exec(`INSERT INTO traffic_buckets (client_id, tunnel_name, tunnel_type, resolution, bucket_start, ingress_bytes, egress_bytes)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		bucket.ClientID,
		bucket.TunnelName,
		bucket.TunnelType,
		string(bucket.Resolution),
		bucket.BucketStart,
		int64(bucket.IngressBytes),
		int64(bucket.EgressBytes),
	); err != nil {
		t.Fatalf("failed to insert traffic bucket: %v", err)
	}
}

func TestTrafficStore_UsesSQLiteAndNoJsonFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, serverDBFileName)
	ts, err := NewTrafficStore(path)
	if err != nil {
		t.Fatalf("NewTrafficStore failed: %v", err)
	}
	t.Cleanup(func() { _ = ts.Close() })

	now := time.Now().UTC()
	ts.ApplyDeltas([]TrafficDelta{{
		ClientID: "c1", TunnelName: "web", TunnelType: "http", MinuteStart: minuteFloorUTC(now).Unix(), IngressBytes: 100, EgressBytes: 50,
	}})
	if err := ts.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "traffic.json")); !os.IsNotExist(err) {
		t.Fatalf("traffic.json should not exist, stat error = %v", err)
	}

	reloaded, err := NewTrafficStore(path)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	t.Cleanup(func() { _ = reloaded.Close() })
	got := reloaded.QueryWithResolution("c1", "web", now.Add(-time.Minute), now.Add(time.Minute), TrafficResolutionMinute)
	series := mustSingleSeries(t, got, "web")
	if series.Points[0].IngressBytes != 100 || series.Points[0].EgressBytes != 50 {
		t.Fatalf("traffic did not round-trip through SQLite: %+v", series.Points[0])
	}
}

func TestTrafficStore_CloseIsIdempotent(t *testing.T) {
	ts, cleanup := newTestTrafficStore(t)
	defer cleanup()

	if err := ts.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := ts.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
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
		t.Fatalf("Expected minute resolution, got %s", got.Resolution)
	}
	if len(got.Items) != 2 {
		t.Fatalf("Expected 2 tunnels, got %d", len(got.Items))
	}

	tun1 := findSeries(t, got, "tun1")
	if len(tun1.Points) != 1 {
		t.Fatalf("tun1 expected 1 point, got %d", len(tun1.Points))
	}
	if tun1.Points[0].IngressBytes != 150 {
		t.Errorf("tun1 ingress expected 150, got %d", tun1.Points[0].IngressBytes)
	}
	if tun1.Points[0].EgressBytes != 275 {
		t.Errorf("tun1 egress expected 275, got %d", tun1.Points[0].EgressBytes)
	}
	if tun1.Points[0].TotalBytes != 425 {
		t.Errorf("tun1 total expected 425, got %d", tun1.Points[0].TotalBytes)
	}

	tun2 := findSeries(t, got, "tun2")
	if len(tun2.Points) != 1 || tun2.Points[0].IngressBytes != 10 {
		t.Errorf("tun2 ingress expected 10, got %+v", tun2.Points)
	}

	gotC2 := ts.QueryWithResolution("c2", "", from, to, TrafficResolutionMinute)
	c2Tun1 := mustSingleSeries(t, gotC2, "tun1")
	if c2Tun1.Points[0].IngressBytes != 999 {
		t.Errorf("c2 tun1 ingress expected 999, got %d", c2Tun1.Points[0].IngressBytes)
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
		t.Errorf("tun1 ingress expected 100, got %d", series.Points[0].IngressBytes)
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
		t.Fatalf("Expected 2 series with same name different types, got %d", len(got.Items))
	}

	tcpSeries := findSeriesWithType(t, got, "shared", "tcp")
	if tcpSeries.Points[0].IngressBytes != 100 || tcpSeries.Points[0].EgressBytes != 10 {
		t.Fatalf("tcp series aggregation error: %+v", tcpSeries.Points[0])
	}

	httpSeries := findSeriesWithType(t, got, "shared", "http")
	if httpSeries.Points[0].IngressBytes != 200 || httpSeries.Points[0].EgressBytes != 20 {
		t.Fatalf("http series aggregation error: %+v", httpSeries.Points[0])
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
	if err := ts.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	ts.Compact(now)

	got := ts.QueryWithResolution("c1", "tun1", baseHour, now, TrafficResolutionHour)
	series := mustSingleSeries(t, got, "tun1")
	if len(series.Points) != 1 {
		t.Fatalf("hour query expected 1 point, got %d", len(series.Points))
	}
	if !series.Points[0].BucketStart.Equal(baseHour) {
		t.Fatalf("hour bucket start time error, expected %s, got %s", baseHour, series.Points[0].BucketStart)
	}
	if series.Points[0].IngressBytes != 60 {
		t.Errorf("hour ingress expected 60, got %d", series.Points[0].IngressBytes)
	}
	if series.Points[0].EgressBytes != 30 {
		t.Errorf("hour egress expected 30, got %d", series.Points[0].EgressBytes)
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

	mustInsertTrafficBucket(t, ts, TrafficBucket{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", Resolution: TrafficResolutionHour, BucketStart: oldHour.Unix(), IngressBytes: 3, EgressBytes: 3})
	mustInsertTrafficBucket(t, ts, TrafficBucket{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", Resolution: TrafficResolutionHour, BucketStart: recentHour.Unix(), IngressBytes: 4, EgressBytes: 4})

	ts.Compact(now)

	minuteResult := ts.QueryWithResolution("c1", "tun1", now.Add(-26*time.Hour), now, TrafficResolutionMinute)
	series := mustSingleSeries(t, minuteResult, "tun1")
	if len(series.Points) != 1 {
		t.Fatalf("Expected 1 point after minute bucket eviction, got %d", len(series.Points))
	}
	if series.Points[0].IngressBytes != 2 {
		t.Errorf("Retained minute bucket ingress expected 2, got %d", series.Points[0].IngressBytes)
	}

	hourResult := ts.QueryWithResolution("c1", "tun1", now.Add(-(trafficHourRetention + 2*time.Hour)), now, TrafficResolutionHour)
	hourSeries := mustSingleSeries(t, hourResult, "tun1")
	if len(hourSeries.Points) == 0 {
		t.Fatal("hour bucket query should retain recent data")
	}
	for _, point := range hourSeries.Points {
		if point.BucketStart.Equal(oldHour) {
			t.Fatal("hour buckets beyond retention period should be evicted")
		}
	}
}

func TestTrafficStore_FlushAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), serverDBFileName)
	ts, err := NewTrafficStore(path)
	if err != nil {
		t.Fatalf("Failed to create TrafficStore: %v", err)
	}
	t.Cleanup(func() { _ = ts.Close() })

	now := time.Now().UTC()
	ts.ApplyDeltas([]TrafficDelta{{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: minuteFloorUTC(now).Unix(), IngressBytes: 500, EgressBytes: 300}})

	if err := ts.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	var count int
	if err := ts.db.QueryRow(`SELECT COUNT(*) FROM traffic_buckets`).Scan(&count); err != nil {
		t.Fatalf("failed to query traffic buckets: %v", err)
	}
	if count != 1 {
		t.Fatalf("traffic_buckets count = %d, want 1", count)
	}

	ts2, err := NewTrafficStore(path)
	if err != nil {
		t.Fatalf("Failed to reload TrafficStore: %v", err)
	}
	t.Cleanup(func() { _ = ts2.Close() })

	got := ts2.QueryWithResolution("c1", "tun1", now.Add(-time.Minute), now.Add(time.Minute), TrafficResolutionMinute)
	series := mustSingleSeries(t, got, "tun1")
	if series.Points[0].IngressBytes != 500 {
		t.Errorf("ingress expected 500 after reload, got %d", series.Points[0].IngressBytes)
	}
	if series.Points[0].EgressBytes != 300 {
		t.Errorf("egress expected 300 after reload, got %d", series.Points[0].EgressBytes)
	}
}

func TestTrafficStore_FlushAccumulatesPersistedBuckets(t *testing.T) {
	ts, cleanup := newTestTrafficStore(t)
	defer cleanup()

	now := time.Now().UTC()
	minuteStart := minuteFloorUTC(now)
	ts.ApplyDeltas([]TrafficDelta{{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: minuteStart.Unix(), IngressBytes: 10, EgressBytes: 20}})
	if err := ts.Flush(); err != nil {
		t.Fatalf("first Flush failed: %v", err)
	}
	ts.ApplyDeltas([]TrafficDelta{{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: minuteStart.Unix(), IngressBytes: 30, EgressBytes: 40}})
	if err := ts.Flush(); err != nil {
		t.Fatalf("second Flush failed: %v", err)
	}

	got := ts.QueryWithResolution("c1", "tun1", now.Add(-time.Minute), now.Add(time.Minute), TrafficResolutionMinute)
	series := mustSingleSeries(t, got, "tun1")
	if series.Points[0].IngressBytes != 40 || series.Points[0].EgressBytes != 60 {
		t.Fatalf("flushed buckets should accumulate, got %+v", series.Points[0])
	}
}

func TestTrafficStore_QueryMergesPersistedAndPendingMinuteBuckets(t *testing.T) {
	ts, cleanup := newTestTrafficStore(t)
	defer cleanup()

	now := time.Now().UTC()
	minuteStart := minuteFloorUTC(now)
	ts.ApplyDeltas([]TrafficDelta{{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: minuteStart.Unix(), IngressBytes: 10, EgressBytes: 20}})
	if err := ts.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	ts.ApplyDeltas([]TrafficDelta{{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: minuteStart.Unix(), IngressBytes: 30, EgressBytes: 40}})

	got := ts.QueryWithResolution("c1", "tun1", now.Add(-time.Minute), now.Add(time.Minute), TrafficResolutionMinute)
	series := mustSingleSeries(t, got, "tun1")
	if len(series.Points) != 1 {
		t.Fatalf("expected merged minute point, got %d points", len(series.Points))
	}
	if series.Points[0].IngressBytes != 40 || series.Points[0].EgressBytes != 60 {
		t.Fatalf("minute query should merge persisted and pending buckets, got %+v", series.Points[0])
	}
}

func TestTrafficStore_HourQueryIncludesLateDeltasAfterRollup(t *testing.T) {
	ts, cleanup := newTestTrafficStore(t)
	defer cleanup()

	now := time.Now().UTC()
	completedHour := hourFloorUTC(now.Add(-2 * time.Hour))
	ts.ApplyDeltas([]TrafficDelta{{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: completedHour.Add(5 * time.Minute).Unix(), IngressBytes: 10, EgressBytes: 20}})
	if err := ts.Flush(); err != nil {
		t.Fatalf("initial Flush failed: %v", err)
	}
	ts.Compact(now)

	ts.ApplyDeltas([]TrafficDelta{{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: completedHour.Add(6 * time.Minute).Unix(), IngressBytes: 30, EgressBytes: 40}})
	pendingResult := ts.QueryWithResolution("c1", "tun1", completedHour, now, TrafficResolutionHour)
	pendingSeries := mustSingleSeries(t, pendingResult, "tun1")
	if pendingSeries.Points[0].IngressBytes != 40 || pendingSeries.Points[0].EgressBytes != 60 {
		t.Fatalf("hour query should include pending late delta, got %+v", pendingSeries.Points[0])
	}

	if err := ts.Flush(); err != nil {
		t.Fatalf("late Flush failed: %v", err)
	}
	flushedResult := ts.QueryWithResolution("c1", "tun1", completedHour, now, TrafficResolutionHour)
	flushedSeries := mustSingleSeries(t, flushedResult, "tun1")
	if flushedSeries.Points[0].IngressBytes != 40 || flushedSeries.Points[0].EgressBytes != 60 {
		t.Fatalf("hour query should include flushed late delta before next compact, got %+v", flushedSeries.Points[0])
	}
}

func TestTrafficStore_CompactKeepsPendingOnFlushFailure(t *testing.T) {
	ts, cleanup := newTestTrafficStore(t)
	defer cleanup()

	now := time.Now().UTC()
	oldMinute := minuteFloorUTC(now.Add(-25 * time.Hour))
	ts.ApplyDeltas([]TrafficDelta{{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: oldMinute.Unix(), IngressBytes: 10, EgressBytes: 20}})
	ts.failSaveErr = errors.New("flush failed")
	ts.failSaveCount = 1

	if err := ts.Compact(now); err == nil {
		t.Fatal("Compact should return the flush failure")
	}
	pendingResult := ts.QueryWithResolution("c1", "tun1", now.Add(-26*time.Hour), now, TrafficResolutionMinute)
	pendingSeries := mustSingleSeries(t, pendingResult, "tun1")
	if pendingSeries.Points[0].IngressBytes != 10 || pendingSeries.Points[0].EgressBytes != 20 {
		t.Fatalf("pending traffic should survive failed compact, got %+v", pendingSeries.Points[0])
	}

	if err := ts.Compact(now); err != nil {
		t.Fatalf("retry Compact failed: %v", err)
	}
	minuteResult := ts.QueryWithResolution("c1", "tun1", now.Add(-26*time.Hour), now, TrafficResolutionMinute)
	if len(minuteResult.Items) != 0 {
		t.Fatalf("old minute bucket should be pruned after successful rollup, got %+v", minuteResult.Items)
	}
	hourResult := ts.QueryWithResolution("c1", "tun1", now.Add(-26*time.Hour), now, TrafficResolutionHour)
	hourSeries := mustSingleSeries(t, hourResult, "tun1")
	if hourSeries.Points[0].IngressBytes != 10 || hourSeries.Points[0].EgressBytes != 20 {
		t.Fatalf("old pending traffic should roll up after retry, got %+v", hourSeries.Points[0])
	}
}

func TestTrafficStore_CompactSkipsPruneWhenRollupFails(t *testing.T) {
	ts, cleanup := newTestTrafficStore(t)
	defer cleanup()

	now := time.Now().UTC()
	oldMinute := minuteFloorUTC(now.Add(-25 * time.Hour))
	ts.ApplyDeltas([]TrafficDelta{{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: oldMinute.Unix(), IngressBytes: 10, EgressBytes: 20}})
	if err := ts.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	if _, err := ts.db.Exec(`CREATE TRIGGER fail_hour_rollup
BEFORE INSERT ON traffic_buckets
WHEN NEW.resolution = 'hour'
BEGIN
	SELECT RAISE(FAIL, 'rollup blocked');
END;`); err != nil {
		t.Fatalf("create trigger failed: %v", err)
	}

	if err := ts.Compact(now); err == nil {
		t.Fatal("Compact should return the rollup failure")
	}
	if _, err := ts.db.Exec(`DROP TRIGGER fail_hour_rollup`); err != nil {
		t.Fatalf("drop trigger failed: %v", err)
	}
	minuteResult := ts.QueryWithResolution("c1", "tun1", now.Add(-26*time.Hour), now, TrafficResolutionMinute)
	minuteSeries := mustSingleSeries(t, minuteResult, "tun1")
	if minuteSeries.Points[0].IngressBytes != 10 || minuteSeries.Points[0].EgressBytes != 20 {
		t.Fatalf("minute bucket should survive failed rollup, got %+v", minuteSeries.Points[0])
	}

	if err := ts.Compact(now); err != nil {
		t.Fatalf("retry Compact failed: %v", err)
	}
	hourResult := ts.QueryWithResolution("c1", "tun1", now.Add(-26*time.Hour), now, TrafficResolutionHour)
	hourSeries := mustSingleSeries(t, hourResult, "tun1")
	if hourSeries.Points[0].IngressBytes != 10 || hourSeries.Points[0].EgressBytes != 20 {
		t.Fatalf("minute bucket should roll up after retry, got %+v", hourSeries.Points[0])
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
		t.Fatalf("c1 should only have tun2 after EvictTunnel, got %+v", got.Items)
	}

	ts.EvictClient("c1")
	got2 := ts.QueryWithResolution("c1", "", now.Add(-time.Minute), now.Add(time.Minute), TrafficResolutionMinute)
	if len(got2.Items) != 0 {
		t.Errorf("c1 data should be empty after EvictClient, got %d series", len(got2.Items))
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
		t.Fatalf("24h range should use minute, got %s", minuteRange.Resolution)
	}

	hourRange := ts.Query("c1", "tun1", now.Add(-(24*time.Hour + time.Second)), now)
	if hourRange.Resolution != TrafficResolutionHour {
		t.Fatalf("Range exceeding 24h should use hour, got %s", hourRange.Resolution)
	}
}

func TestTrafficStore_HourQueryIncludesCurrentHourFromMinuteBuckets(t *testing.T) {
	ts, cleanup := newTestTrafficStore(t)
	defer cleanup()

	now := time.Now().UTC()
	currentHour := hourFloorUTC(now)
	currentMinute := minuteFloorUTC(now)
	completedHour := currentHour.Add(-time.Hour)

	ts.ApplyDeltas([]TrafficDelta{
		{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: completedHour.Add(5 * time.Minute).Unix(), IngressBytes: 10, EgressBytes: 5},
		{ClientID: "c1", TunnelName: "tun1", TunnelType: "tcp", MinuteStart: currentMinute.Unix(), IngressBytes: 20, EgressBytes: 7},
	})
	if err := ts.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	ts.Compact(now)

	result := ts.QueryWithResolution("c1", "tun1", completedHour, now.Add(time.Minute), TrafficResolutionHour)
	series := mustSingleSeries(t, result, "tun1")
	if len(series.Points) != 2 {
		t.Fatalf("Should return both completed hour and current hour folded data, got %d points", len(series.Points))
	}
	if !series.Points[0].BucketStart.Equal(completedHour) || series.Points[0].IngressBytes != 10 {
		t.Fatalf("Completed hour aggregation error: %+v", series.Points[0])
	}
	if !series.Points[1].BucketStart.Equal(currentHour) || series.Points[1].IngressBytes != 20 {
		t.Fatalf("Current hour fold error: %+v", series.Points[1])
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
	if err := ts.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	ts.Compact(now)

	result := ts.QueryWithResolution("c1", "tun1", completedHour, now, TrafficResolutionHour)
	series := mustSingleSeries(t, result, "tun1")
	if len(series.Points) != 1 {
		t.Fatalf("Rolled-up hours should not duplicate due to minute/hour coexistence, got %d points", len(series.Points))
	}
	if series.Points[0].IngressBytes != 24 || series.Points[0].EgressBytes != 7 {
		t.Fatalf("hour deduplication aggregation error: %+v", series.Points[0])
	}
}

func TestTrafficAPI_Query(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	ts, trafficCleanup := newTestTrafficStore(t)
	defer trafficCleanup()
	s.trafficStore = ts

	clientID := "test-client-001"
	now := time.Now().UTC()
	ts.ApplyDeltas([]TrafficDelta{{ClientID: clientID, TunnelName: "web", TunnelType: "http", MinuteStart: minuteFloorUTC(now).Unix(), IngressBytes: 1024, EgressBytes: 512}})

	from := now.Add(-time.Minute).Unix()
	to := now.Add(time.Minute).Unix()

	path := "/api/clients/" + clientID + "/traffic?from=" + itoa(from) + "&to=" + itoa(to)
	w := doMuxRequest(t, handler, http.MethodGet, path, token, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d, body: %s", w.Code, w.Body.String())
	}

	var resp TrafficQueryResult
	if err := mustDecodeJSON(t, w.Body, &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp.Resolution != TrafficResolutionMinute {
		t.Errorf("Expected resolution=minute, got %q", resp.Resolution)
	}

	web := mustSingleSeries(t, resp, "web")
	if len(web.Points) == 0 {
		t.Fatal("Expected web data points")
	}
	if web.Points[0].IngressBytes != 1024 {
		t.Errorf("web ingress expected 1024, got %d", web.Points[0].IngressBytes)
	}
	if web.Points[0].EgressBytes != 512 {
		t.Errorf("web egress expected 512, got %d", web.Points[0].EgressBytes)
	}
}

func TestTrafficAPI_Unauthorized(t *testing.T) {
	_, handler, _, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	w := doMuxRequest(t, handler, http.MethodGet, "/api/clients/c1/traffic", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Unauthorized should return 401, got %d", w.Code)
	}
}

func TestTrafficAPI_DefaultTimeRange(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	ts, trafficCleanup := newTestTrafficStore(t)
	defer trafficCleanup()
	s.trafficStore = ts

	w := doMuxRequest(t, handler, http.MethodGet, "/api/clients/c1/traffic", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	var resp TrafficQueryResult
	if err := mustDecodeJSON(t, w.Body, &resp); err != nil {
		t.Fatalf("Failed to parse default time range response: %v", err)
	}
	if resp.Resolution != TrafficResolutionMinute {
		t.Fatalf("Default 24h time range should be minute, got %s", resp.Resolution)
	}
}

func TestTrafficAPI_InvalidResolution(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	ts, trafficCleanup := newTestTrafficStore(t)
	defer trafficCleanup()
	s.trafficStore = ts

	w := doMuxRequest(t, handler, http.MethodGet, "/api/clients/c1/traffic?resolution=bad", token, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Invalid resolution expected 400, got %d", w.Code)
	}
}

func TestTrafficAPI_InvalidTimeRange(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	ts, trafficCleanup := newTestTrafficStore(t)
	defer trafficCleanup()
	s.trafficStore = ts

	now := time.Now().UTC()
	from := now.Unix()
	to := now.Add(-time.Minute).Unix()
	w := doMuxRequest(t, handler, http.MethodGet, "/api/clients/c1/traffic?from="+itoa(from)+"&to="+itoa(to), token, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("from > to should return 400, got %d", w.Code)
	}
}

func TestTrafficAPI_TimeRangeTooLarge(t *testing.T) {
	s, handler, token, cleanup := setupTestServerWithStores(t, true)
	defer cleanup()

	ts, trafficCleanup := newTestTrafficStore(t)
	defer trafficCleanup()
	s.trafficStore = ts

	now := time.Now().UTC()
	from := now.Add(-(trafficMaxRange + time.Hour)).Unix()
	to := now.Unix()
	w := doMuxRequest(t, handler, http.MethodGet, "/api/clients/c1/traffic?from="+itoa(from)+"&to="+itoa(to), token, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("Range exceeding 7 days should return 400, got %d", w.Code)
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
