package server

import (
	"log"
	"sort"
	"sync"
	"time"

	"netsgo/pkg/protocol"
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
	tunnelID    string
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

func (a *trafficAccumulator) Add(now time.Time, tunnelID, clientID, tunnelName, tunnelType string, ingressBytes, egressBytes uint64) error {
	if a == nil || clientID == "" || tunnelName == "" || tunnelType == "" {
		return nil
	}
	if ingressBytes == 0 && egressBytes == 0 {
		return nil
	}

	now = now.UTC()
	delta := TrafficDelta{
		TunnelID:     tunnelID,
		ClientID:     clientID,
		TunnelName:   tunnelName,
		TunnelType:   tunnelType,
		SecondStart:  secondFloorUTC(now).Unix(),
		MinuteStart:  minuteFloorUTC(now).Unix(),
		IngressBytes: ingressBytes,
		EgressBytes:  egressBytes,
	}
	key := trafficAccumulatorKey{
		tunnelID:    delta.TunnelID,
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
		if deltas[i].TunnelID != deltas[j].TunnelID {
			return deltas[i].TunnelID < deltas[j].TunnelID
		}
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
	hash := trafficAccumulatorHashString(2166136261, key.tunnelID)
	hash = trafficAccumulatorHashString(hash, key.clientID)
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

func (s *Server) recordTunnelTraffic(clientID string, config protocol.ProxyConfig, ingressBytes, egressBytes uint64) {
	s.recordTunnelTrafficAt(time.Now(), clientID, config, ingressBytes, egressBytes)
}

func (s *Server) recordTunnelTrafficAt(now time.Time, clientID string, config protocol.ProxyConfig, ingressBytes, egressBytes uint64) {
	s.recordTrafficObservationAt(now, config.ID, clientID, config.Name, config.Type, ingressBytes, egressBytes)
}

func (s *Server) recordTrafficAt(now time.Time, clientID, tunnelName, tunnelType string, ingressBytes, egressBytes uint64) {
	s.recordTrafficObservationAt(now, "", clientID, tunnelName, tunnelType, ingressBytes, egressBytes)
}

func (s *Server) recordTrafficObservationAt(now time.Time, tunnelID, clientID, tunnelName, tunnelType string, ingressBytes, egressBytes uint64) {
	if s == nil || s.trafficStore == nil {
		return
	}
	if ingressBytes == 0 && egressBytes == 0 {
		return
	}
	if tunnelID == "" {
		tunnelID = s.resolveTrafficTunnelID(clientID, tunnelName, tunnelType)
	}

	acc := s.trafficAccumulator
	if acc == nil {
		s.trafficStore.recordTunnelBytesAt(now, tunnelID, clientID, tunnelName, tunnelType, ingressBytes, egressBytes)
		return
	}

	if err := acc.Add(now, tunnelID, clientID, tunnelName, tunnelType, ingressBytes, egressBytes); err == nil {
		return
	}

	// Overflow is practically unreachable for normal chunk sizes. Flush the current
	// batch and retry so the hot path can still preserve the observation.
	s.flushTrafficObservations()
	if err := acc.Add(now, tunnelID, clientID, tunnelName, tunnelType, ingressBytes, egressBytes); err != nil {
		log.Printf("⚠️ Failed to aggregate traffic bytes for client %s tunnel %s: %v", clientID, tunnelName, err)
		s.trafficStore.recordTunnelBytesAt(now, tunnelID, clientID, tunnelName, tunnelType, ingressBytes, egressBytes)
	}
}

func (s *Server) resolveTrafficTunnelID(clientID, tunnelName, tunnelType string) string {
	if s == nil || clientID == "" || tunnelName == "" {
		return ""
	}
	if value, ok := s.clients.Load(clientID); ok {
		if client, ok := value.(*ClientConn); ok {
			client.proxyMu.RLock()
			tunnel, ok := client.proxies[tunnelName]
			if ok && tunnel != nil && tunnel.Config.ID != "" && (tunnelType == "" || tunnel.Config.Type == tunnelType) {
				tunnelID := tunnel.Config.ID
				client.proxyMu.RUnlock()
				return tunnelID
			}
			client.proxyMu.RUnlock()
		}
	}
	if s.store != nil {
		if stored, ok := s.store.GetTunnel(clientID, tunnelName); ok && stored.ID != "" && (tunnelType == "" || stored.Type == tunnelType) {
			return stored.ID
		}
	}
	return ""
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
