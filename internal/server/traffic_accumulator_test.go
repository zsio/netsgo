package server

import (
	"testing"
	"time"
)

func TestTrafficAccumulatorAggregatesBySecondAndTunnel(t *testing.T) {
	acc := newTrafficAccumulator()
	base := time.Unix(1_700_000_000, 123_000_000).UTC()

	if err := acc.Add(base, "c1", "web", "http", 100, 10); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if err := acc.Add(base.Add(500*time.Millisecond), "c1", "web", "http", 50, 5); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if err := acc.Add(base.Add(time.Second), "c1", "web", "http", 7, 3); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if err := acc.Add(base, "c1", "ssh", "tcp", 20, 2); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	if got := acc.Len(); got != 3 {
		t.Fatalf("pending buckets: want 3, got %d", got)
	}

	deltas := acc.Drain()
	if len(deltas) != 3 {
		t.Fatalf("drained deltas: want 3, got %+v", deltas)
	}
	if got := acc.Len(); got != 0 {
		t.Fatalf("pending buckets after drain: want 0, got %d", got)
	}

	first := deltas[0]
	if first.ClientID != "c1" || first.TunnelName != "ssh" || first.TunnelType != "tcp" {
		t.Fatalf("first sorted delta should be ssh/tcp, got %+v", first)
	}
	webFirst := deltas[1]
	if webFirst.TunnelName != "web" || webFirst.SecondStart != secondFloorUTC(base).Unix() {
		t.Fatalf("first web delta mismatch: %+v", webFirst)
	}
	if webFirst.IngressBytes != 150 || webFirst.EgressBytes != 15 {
		t.Fatalf("web same-second aggregation mismatch: %+v", webFirst)
	}
	webSecond := deltas[2]
	if webSecond.TunnelName != "web" || webSecond.SecondStart != secondFloorUTC(base.Add(time.Second)).Unix() {
		t.Fatalf("second web delta mismatch: %+v", webSecond)
	}
	if webSecond.IngressBytes != 7 || webSecond.EgressBytes != 3 {
		t.Fatalf("web next-second aggregation mismatch: %+v", webSecond)
	}
}

func TestServerFlushTrafficObservationsAppliesAccumulator(t *testing.T) {
	s := New(0)
	ts, cleanup := newTestTrafficStore(t)
	defer cleanup()
	s.trafficStore = ts

	base := secondFloorUTC(time.Now().UTC())
	s.recordTrafficAt(base, "c1", "web", "http", 100, 10)
	s.recordTrafficAt(base, "c1", "web", "http", 50, 5)

	before := mustQueryWithResolution(t, ts, "c1", "web", base.Add(-time.Second), base.Add(time.Second), TrafficResolutionSecond)
	if len(before.Items) != 0 {
		t.Fatalf("traffic store should not see accumulator data before flush, got %+v", before.Items)
	}

	s.flushTrafficObservations()

	after := mustQueryWithResolution(t, ts, "c1", "web", base.Add(-time.Second), base.Add(time.Second), TrafficResolutionSecond)
	web := mustSingleSeries(t, after, "web")
	if len(web.Points) != 1 {
		t.Fatalf("web points: want 1, got %+v", web.Points)
	}
	if web.Points[0].IngressBytes != 150 || web.Points[0].EgressBytes != 15 || web.Points[0].TotalBytes != 165 {
		t.Fatalf("flushed point mismatch: %+v", web.Points[0])
	}
}
