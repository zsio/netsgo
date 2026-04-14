package client

import (
	"encoding/json"
	"runtime"
	"testing"
	"time"
)

// ============================================================
// Basic probe functionality (3)
// ============================================================

func TestProbe_NotNil(t *testing.T) {
	stats, err := CollectSystemStats(time.Time{})
	if err != nil {
		t.Fatalf("CollectSystemStats failed: %v", err)
	}
	if stats == nil {
		t.Fatal("returned stats should not be nil")
	}
}

func TestProbe_CPURange(t *testing.T) {
	stats, err := CollectSystemStats(time.Time{})
	if err != nil {
		t.Fatalf("CollectSystemStats failed: %v", err)
	}
	if stats.CPUUsage < 0 || stats.CPUUsage > 100 {
		t.Errorf("CPU usage should be in the range 0-100, got %f", stats.CPUUsage)
	}
}

func TestProbe_NumCPU(t *testing.T) {
	stats, err := CollectSystemStats(time.Time{})
	if err != nil {
		t.Fatalf("CollectSystemStats failed: %v", err)
	}
	expected := runtime.NumCPU()
	if stats.NumCPU != expected {
		t.Errorf("NumCPU: want %d, got %d", expected, stats.NumCPU)
	}
}

// ============================================================
// Field validation (4)
// ============================================================

func TestProbe_MemValid(t *testing.T) {
	stats, err := CollectSystemStats(time.Time{})
	if err != nil {
		t.Fatalf("CollectSystemStats failed: %v", err)
	}
	if stats.MemTotal == 0 {
		t.Error("MemTotal should not be 0")
	}
	if stats.MemUsed > stats.MemTotal {
		t.Errorf("MemUsed (%d) should not be greater than MemTotal (%d)", stats.MemUsed, stats.MemTotal)
	}
	if stats.MemUsage < 0 || stats.MemUsage > 100 {
		t.Errorf("MemUsage should be in the range 0-100, got %f", stats.MemUsage)
	}
}

func TestProbe_DiskValid(t *testing.T) {
	stats, err := CollectSystemStats(time.Time{})
	if err != nil {
		t.Fatalf("CollectSystemStats failed: %v", err)
	}
	if stats.DiskTotal == 0 {
		t.Error("DiskTotal should not be 0")
	}
	if stats.DiskUsed > stats.DiskTotal {
		t.Errorf("DiskUsed (%d) should not be greater than DiskTotal (%d)", stats.DiskUsed, stats.DiskTotal)
	}
	if stats.DiskUsage < 0 || stats.DiskUsage > 100 {
		t.Errorf("DiskUsage should be in the range 0-100, got %f", stats.DiskUsage)
	}
}

func TestProbe_UptimePositive(t *testing.T) {
	stats, err := CollectSystemStats(time.Time{})
	if err != nil {
		t.Fatalf("CollectSystemStats failed: %v", err)
	}
	if stats.Uptime == 0 {
		t.Error("Uptime should not be 0 (the machine should have been running for at least a few seconds)")
	}
}

func TestProbe_NetCounters(t *testing.T) {
	stats, err := CollectSystemStats(time.Time{})
	if err != nil {
		t.Fatalf("CollectSystemStats failed: %v", err)
	}
	// Network counters should be >= 0 (even on a freshly started machine they should not be negative)
	// Running the test itself generates network traffic, so at least one should be > 0
	if stats.NetSent == 0 && stats.NetRecv == 0 {
		t.Log("warning: both NetSent and NetRecv are 0, possibly due to an isolated environment (logged, not treated as a failure)")
	}
}

// ============================================================
// Stability & serialization (2)
// ============================================================

func TestProbe_MultipleCollections(t *testing.T) {
	for i := 0; i < 3; i++ {
		stats, err := CollectSystemStats(time.Time{})
		if err != nil {
			t.Fatalf("collection #%d failed: %v", i, err)
		}
		if stats == nil {
			t.Fatalf("collection #%d returned nil", i)
		}
		if stats.NumCPU <= 0 {
			t.Errorf("collection #%d: NumCPU should be positive, got %d", i, stats.NumCPU)
		}
	}
}

func TestProbe_JSONRoundTrip(t *testing.T) {
	stats, err := CollectSystemStats(time.Time{})
	if err != nil {
		t.Fatalf("CollectSystemStats failed: %v", err)
	}

	// Serialize
	data, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Deserialize
	var restored struct {
		CPUUsage  float64 `json:"cpu_usage"`
		MemTotal  uint64  `json:"mem_total"`
		MemUsed   uint64  `json:"mem_used"`
		MemUsage  float64 `json:"mem_usage"`
		DiskTotal uint64  `json:"disk_total"`
		DiskUsed  uint64  `json:"disk_used"`
		DiskUsage float64 `json:"disk_usage"`
		NetSent   uint64  `json:"net_sent"`
		NetRecv   uint64  `json:"net_recv"`
		Uptime    uint64  `json:"uptime"`
		NumCPU    int     `json:"num_cpu"`
	}
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify that all fields survive the JSON round trip
	if restored.NumCPU != stats.NumCPU {
		t.Errorf("NumCPU mismatch: %d vs %d", restored.NumCPU, stats.NumCPU)
	}
	if restored.MemTotal != stats.MemTotal {
		t.Errorf("MemTotal mismatch: %d vs %d", restored.MemTotal, stats.MemTotal)
	}
	if restored.CPUUsage != stats.CPUUsage {
		t.Errorf("CPUUsage mismatch: %f vs %f", restored.CPUUsage, stats.CPUUsage)
	}
	if restored.Uptime != stats.Uptime {
		t.Errorf("Uptime mismatch: %d vs %d", restored.Uptime, stats.Uptime)
	}

	// Verify that the JSON contains all 11 fields
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal probe JSON failed: %v", err)
	}
	expectedKeys := []string{
		"cpu_usage", "mem_total", "mem_used", "mem_usage",
		"disk_total", "disk_used", "disk_usage",
		"net_sent", "net_recv", "uptime", "num_cpu",
	}
	for _, key := range expectedKeys {
		if _, ok := m[key]; !ok {
			t.Errorf("JSON is missing field %q", key)
		}
	}
}
