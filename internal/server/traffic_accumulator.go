package server

import (
	"log"
	"sort"
	"sync"
	"time"
)

const trafficAccumulatorShardCount = 32

type trafficAccumulator struct {
	shards [trafficAccumulatorShardCount]trafficAccumulatorShard
}

type trafficAccumulatorShard struct {
	mu      sync.Mutex
	pending map[trafficAccumulatorKey]TrafficDelta
}

type trafficAccumulatorKey struct {
	clientID    string
	tunnelName  string
	tunnelType  string
	secondStart int64
	minuteStart int64
}

func newTrafficAccumulator() *trafficAccumulator {
	acc := &trafficAccumulator{}
	for i := range acc.shards {
		acc.shards[i].pending = make(map[trafficAccumulatorKey]TrafficDelta)
	}
	return acc
}

func (a *trafficAccumulator) Add(now time.Time, clientID, tunnelName, tunnelType string, ingressBytes, egressBytes uint64) error {
	if a == nil || clientID == "" || tunnelName == "" || tunnelType == "" {
		return nil
	}
	if ingressBytes == 0 && egressBytes == 0 {
		return nil
	}

	now = now.UTC()
	delta := TrafficDelta{
		ClientID:     clientID,
		TunnelName:   tunnelName,
		TunnelType:   tunnelType,
		SecondStart:  secondFloorUTC(now).Unix(),
		MinuteStart:  minuteFloorUTC(now).Unix(),
		IngressBytes: ingressBytes,
		EgressBytes:  egressBytes,
	}
	key := trafficAccumulatorKey{
		clientID:    delta.ClientID,
		tunnelName:  delta.TunnelName,
		tunnelType:  delta.TunnelType,
		secondStart: delta.SecondStart,
		minuteStart: delta.MinuteStart,
	}

	shard := &a.shards[trafficAccumulatorShardIndex(key)]
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if shard.pending == nil {
		shard.pending = make(map[trafficAccumulatorKey]TrafficDelta)
	}

	if existing, ok := shard.pending[key]; ok {
		mergedIngress, err := checkedTrafficAdd("traffic accumulator ingress_bytes", existing.IngressBytes, delta.IngressBytes)
		if err != nil {
			return err
		}
		mergedEgress, err := checkedTrafficAdd("traffic accumulator egress_bytes", existing.EgressBytes, delta.EgressBytes)
		if err != nil {
			return err
		}
		existing.IngressBytes = mergedIngress
		existing.EgressBytes = mergedEgress
		shard.pending[key] = existing
		return nil
	}

	shard.pending[key] = delta
	return nil
}

func (a *trafficAccumulator) Drain() []TrafficDelta {
	if a == nil {
		return nil
	}

	deltas := []TrafficDelta{}
	for i := range a.shards {
		shard := &a.shards[i]
		shard.mu.Lock()
		for _, delta := range shard.pending {
			deltas = append(deltas, delta)
		}
		shard.pending = make(map[trafficAccumulatorKey]TrafficDelta)
		shard.mu.Unlock()
	}

	sort.Slice(deltas, func(i, j int) bool {
		if deltas[i].ClientID != deltas[j].ClientID {
			return deltas[i].ClientID < deltas[j].ClientID
		}
		if deltas[i].TunnelName != deltas[j].TunnelName {
			return deltas[i].TunnelName < deltas[j].TunnelName
		}
		if deltas[i].TunnelType != deltas[j].TunnelType {
			return deltas[i].TunnelType < deltas[j].TunnelType
		}
		if deltas[i].SecondStart != deltas[j].SecondStart {
			return deltas[i].SecondStart < deltas[j].SecondStart
		}
		return deltas[i].MinuteStart < deltas[j].MinuteStart
	})
	return deltas
}

func (a *trafficAccumulator) Len() int {
	if a == nil {
		return 0
	}

	count := 0
	for i := range a.shards {
		shard := &a.shards[i]
		shard.mu.Lock()
		count += len(shard.pending)
		shard.mu.Unlock()
	}
	return count
}

func trafficAccumulatorShardIndex(key trafficAccumulatorKey) int {
	hash := trafficAccumulatorHashString(2166136261, key.clientID)
	hash = trafficAccumulatorHashString(hash, key.tunnelName)
	hash = trafficAccumulatorHashString(hash, key.tunnelType)
	return int(hash % trafficAccumulatorShardCount)
}

func trafficAccumulatorHashString(hash uint32, value string) uint32 {
	for i := 0; i < len(value); i++ {
		hash ^= uint32(value[i])
		hash *= 16777619
	}
	return hash
}

func (s *Server) recordTraffic(clientID, tunnelName, tunnelType string, ingressBytes, egressBytes uint64) {
	s.recordTrafficAt(time.Now(), clientID, tunnelName, tunnelType, ingressBytes, egressBytes)
}

func (s *Server) recordTrafficAt(now time.Time, clientID, tunnelName, tunnelType string, ingressBytes, egressBytes uint64) {
	if s == nil || s.trafficStore == nil {
		return
	}
	if ingressBytes == 0 && egressBytes == 0 {
		return
	}

	acc := s.trafficAccumulator
	if acc == nil {
		s.trafficStore.recordBytesAt(now, clientID, tunnelName, tunnelType, ingressBytes, egressBytes)
		return
	}

	if err := acc.Add(now, clientID, tunnelName, tunnelType, ingressBytes, egressBytes); err == nil {
		return
	}

	// Overflow is practically unreachable for normal chunk sizes. Flush the current
	// batch and retry so the hot path can still preserve the observation.
	s.flushTrafficObservations()
	if err := acc.Add(now, clientID, tunnelName, tunnelType, ingressBytes, egressBytes); err != nil {
		log.Printf("⚠️ Failed to aggregate traffic bytes for client %s tunnel %s: %v", clientID, tunnelName, err)
		s.trafficStore.recordBytesAt(now, clientID, tunnelName, tunnelType, ingressBytes, egressBytes)
	}
}

func (s *Server) flushTrafficObservations() {
	if s == nil || s.trafficStore == nil || s.trafficAccumulator == nil {
		return
	}
	deltas := s.trafficAccumulator.Drain()
	if len(deltas) == 0 {
		return
	}
	s.trafficStore.ApplyDeltas(deltas)
}
