package client

import (
	"encoding/json"
	"runtime"
	"testing"
	"time"
)

// ============================================================
// 探针基本功能 (3)
// ============================================================

func TestProbe_NotNil(t *testing.T) {
	stats, err := CollectSystemStats(time.Time{})
	if err != nil {
		t.Fatalf("CollectSystemStats 失败: %v", err)
	}
	if stats == nil {
		t.Fatal("返回的 stats 不应为 nil")
	}
}

func TestProbe_CPURange(t *testing.T) {
	stats, err := CollectSystemStats(time.Time{})
	if err != nil {
		t.Fatalf("CollectSystemStats 失败: %v", err)
	}
	if stats.CPUUsage < 0 || stats.CPUUsage > 100 {
		t.Errorf("CPU 使用率应在 0-100，得到 %f", stats.CPUUsage)
	}
}

func TestProbe_NumCPU(t *testing.T) {
	stats, err := CollectSystemStats(time.Time{})
	if err != nil {
		t.Fatalf("CollectSystemStats 失败: %v", err)
	}
	expected := runtime.NumCPU()
	if stats.NumCPU != expected {
		t.Errorf("NumCPU 期望 %d，得到 %d", expected, stats.NumCPU)
	}
}

// ============================================================
// 各字段验证 (4)
// ============================================================

func TestProbe_MemValid(t *testing.T) {
	stats, err := CollectSystemStats(time.Time{})
	if err != nil {
		t.Fatalf("CollectSystemStats 失败: %v", err)
	}
	if stats.MemTotal == 0 {
		t.Error("MemTotal 不应为 0")
	}
	if stats.MemUsed > stats.MemTotal {
		t.Errorf("MemUsed (%d) 不应大于 MemTotal (%d)", stats.MemUsed, stats.MemTotal)
	}
	if stats.MemUsage < 0 || stats.MemUsage > 100 {
		t.Errorf("MemUsage 应在 0-100，得到 %f", stats.MemUsage)
	}
}

func TestProbe_DiskValid(t *testing.T) {
	stats, err := CollectSystemStats(time.Time{})
	if err != nil {
		t.Fatalf("CollectSystemStats 失败: %v", err)
	}
	if stats.DiskTotal == 0 {
		t.Error("DiskTotal 不应为 0")
	}
	if stats.DiskUsed > stats.DiskTotal {
		t.Errorf("DiskUsed (%d) 不应大于 DiskTotal (%d)", stats.DiskUsed, stats.DiskTotal)
	}
	if stats.DiskUsage < 0 || stats.DiskUsage > 100 {
		t.Errorf("DiskUsage 应在 0-100，得到 %f", stats.DiskUsage)
	}
}

func TestProbe_UptimePositive(t *testing.T) {
	stats, err := CollectSystemStats(time.Time{})
	if err != nil {
		t.Fatalf("CollectSystemStats 失败: %v", err)
	}
	if stats.Uptime == 0 {
		t.Error("Uptime 不应为 0（机器至少运行了几秒）")
	}
}

func TestProbe_NetCounters(t *testing.T) {
	stats, err := CollectSystemStats(time.Time{})
	if err != nil {
		t.Fatalf("CollectSystemStats 失败: %v", err)
	}
	// 网络计数器应 ≥ 0（刚启动的机器也不会是负数）
	// 运行测试本身就会产生网络流量，所以至少有一个 > 0
	if stats.NetSent == 0 && stats.NetRecv == 0 {
		t.Log("⚠️ NetSent 和 NetRecv 都为 0，可能是隔离环境（不报错但记录）")
	}
}

// ============================================================
// 稳定性 & 序列化 (2)
// ============================================================

func TestProbe_MultipleCollections(t *testing.T) {
	for i := 0; i < 3; i++ {
		stats, err := CollectSystemStats(time.Time{})
		if err != nil {
			t.Fatalf("第 %d 次采集失败: %v", i, err)
		}
		if stats == nil {
			t.Fatalf("第 %d 次采集返回 nil", i)
		}
		if stats.NumCPU <= 0 {
			t.Errorf("第 %d 次: NumCPU 应为正数，得到 %d", i, stats.NumCPU)
		}
	}
}

func TestProbe_JSONRoundTrip(t *testing.T) {
	stats, err := CollectSystemStats(time.Time{})
	if err != nil {
		t.Fatalf("CollectSystemStats 失败: %v", err)
	}

	// 序列化
	data, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}

	// 反序列化
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
		t.Fatalf("Unmarshal 失败: %v", err)
	}

	// 验证所有字段在 JSON 往返后不丢失
	if restored.NumCPU != stats.NumCPU {
		t.Errorf("NumCPU 不匹配: %d vs %d", restored.NumCPU, stats.NumCPU)
	}
	if restored.MemTotal != stats.MemTotal {
		t.Errorf("MemTotal 不匹配: %d vs %d", restored.MemTotal, stats.MemTotal)
	}
	if restored.CPUUsage != stats.CPUUsage {
		t.Errorf("CPUUsage 不匹配: %f vs %f", restored.CPUUsage, stats.CPUUsage)
	}
	if restored.Uptime != stats.Uptime {
		t.Errorf("Uptime 不匹配: %d vs %d", restored.Uptime, stats.Uptime)
	}

	// 验证 JSON 中包含所有 11 个字段
	var m map[string]any
	json.Unmarshal(data, &m)
	expectedKeys := []string{
		"cpu_usage", "mem_total", "mem_used", "mem_usage",
		"disk_total", "disk_used", "disk_usage",
		"net_sent", "net_recv", "uptime", "num_cpu",
	}
	for _, key := range expectedKeys {
		if _, ok := m[key]; !ok {
			t.Errorf("JSON 缺少字段 %q", key)
		}
	}
}
