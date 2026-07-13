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
	shards                  [trafficAccumulatorShardCount]trafficAccumulatorShard
	minimumRevisionMu       sync.RWMutex
	minimumRevisionByTunnel map[string]int64
}

type trafficAccumulatorShard struct {
	mu      sync.Mutex
	pending map[trafficAccumulatorKey]TrafficDelta
}

type trafficAccumulatorKey struct {
	tunnelID    string
	revision    int64
	clientID    string
	tunnelName  string
	tunnelType  string
	transport   string
	secondStart int64
	minuteStart int64
}

func newTrafficAccumulator() *trafficAccumulator {
	acc := &trafficAccumulator{minimumRevisionByTunnel: make(map[string]int64)}
	for i := range acc.shards {
		acc.shards[i].pending = make(map[trafficAccumulatorKey]TrafficDelta)
	}
	return acc
}

func (a *trafficAccumulator) Add(now time.Time, tunnelID, clientID, tunnelName, tunnelType string, ingressBytes, egressBytes uint64) error {
	return a.AddDelta(now, TrafficDelta{
		TunnelID:     tunnelID,
		ClientID:     clientID,
		TunnelName:   tunnelName,
		TunnelType:   tunnelType,
		IngressBytes: ingressBytes,
		EgressBytes:  egressBytes,
	})
}

func (a *trafficAccumulator) AddDelta(now time.Time, delta TrafficDelta) error {
	if a == nil || delta.ClientID == "" || delta.TunnelName == "" || delta.TunnelType == "" {
		return nil
	}
	if delta.IngressBytes == 0 && delta.EgressBytes == 0 {
		return nil
	}
	a.minimumRevisionMu.RLock()
	defer a.minimumRevisionMu.RUnlock()
	if delta.TunnelID != "" && delta.Revision < a.minimumRevisionByTunnel[delta.TunnelID] {
		return nil
	}

	now = now.UTC()
	delta.SecondStart = secondFloorUTC(now).Unix()
	delta.MinuteStart = minuteFloorUTC(now).Unix()
	key := trafficAccumulatorKey{
		tunnelID:    delta.TunnelID,
		revision:    delta.Revision,
		clientID:    delta.ClientID,
		tunnelName:  delta.TunnelName,
		tunnelType:  delta.TunnelType,
		transport:   delta.Transport,
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
		mergeTrafficDeltaMetadata(&existing, delta)
		shard.pending[key] = existing
		return nil
	}

	shard.pending[key] = delta
	return nil
}

func mergeTrafficDeltaMetadata(existing *TrafficDelta, delta TrafficDelta) {
	if existing.OwnerClientID == "" {
		existing.OwnerClientID = delta.OwnerClientID
	}
	if existing.IngressClientID == "" {
		existing.IngressClientID = delta.IngressClientID
	}
	if existing.TargetClientID == "" {
		existing.TargetClientID = delta.TargetClientID
	}
	if existing.Topology == "" {
		existing.Topology = delta.Topology
	}
	if existing.Transport == "" || existing.Transport == TunnelActualTransportUnknown {
		existing.Transport = delta.Transport
	}
}

// ResetTunnel removes observations already queued for an older tunnel
// specification and prevents late producers from queueing that revision again.
func (a *trafficAccumulator) ResetTunnel(tunnelID string, minimumRevision int64) {
	if a == nil || tunnelID == "" || minimumRevision <= 0 {
		return
	}

	a.minimumRevisionMu.Lock()
	if a.minimumRevisionByTunnel == nil {
		a.minimumRevisionByTunnel = make(map[string]int64)
	}
	if a.minimumRevisionByTunnel[tunnelID] >= minimumRevision {
		a.minimumRevisionMu.Unlock()
		return
	}
	a.minimumRevisionByTunnel[tunnelID] = minimumRevision
	for i := range a.shards {
		shard := &a.shards[i]
		shard.mu.Lock()
		for key := range shard.pending {
			if key.tunnelID == tunnelID && key.revision < minimumRevision {
				delete(shard.pending, key)
			}
		}
		shard.mu.Unlock()
	}
	a.minimumRevisionMu.Unlock()
}

func (a *trafficAccumulator) Drain() []TrafficDelta {
	if a == nil {
		return nil
	}

	deltas := []TrafficDelta{}
	for i := range a.shards {
		shard := &a.shards[i]
		shard.mu.Lock()
		pending := shard.pending
		shard.pending = make(map[trafficAccumulatorKey]TrafficDelta)
		shard.mu.Unlock()
		for _, delta := range pending {
			deltas = append(deltas, delta)
		}
	}

	sort.Slice(deltas, func(i, j int) bool {
		if deltas[i].TunnelID != deltas[j].TunnelID {
			return deltas[i].TunnelID < deltas[j].TunnelID
		}
		if deltas[i].Revision != deltas[j].Revision {
			return deltas[i].Revision < deltas[j].Revision
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
	hash = trafficAccumulatorHashString(hash, key.transport)
	return int(hash % trafficAccumulatorShardCount)
}

func trafficAccumulatorHashString(hash uint32, value string) uint32 {
	for i := 0; i < len(value); i++ {
		hash ^= uint32(value[i])
		hash *= 16777619
	}
	return hash
}

func (s *Server) recordTunnelTraffic(clientID string, config protocol.ProxyConfig, ingressBytes, egressBytes uint64) {
	s.recordTunnelTrafficAt(time.Now(), clientID, config, ingressBytes, egressBytes)
}

func (s *Server) recordTunnelTrafficAt(now time.Time, clientID string, config protocol.ProxyConfig, ingressBytes, egressBytes uint64) {
	s.recordTrafficDeltaAt(now, trafficDeltaFromProxyConfig(clientID, config, ingressBytes, egressBytes))
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
	s.trafficStore.attachAccumulator(s.trafficAccumulator)
	s.recordTrafficDeltaAt(now, TrafficDelta{
		TunnelID:     tunnelID,
		ClientID:     clientID,
		TunnelName:   tunnelName,
		TunnelType:   tunnelType,
		IngressBytes: ingressBytes,
		EgressBytes:  egressBytes,
	})
}

func (s *Server) recordStoredTunnelTrafficAt(now time.Time, stored StoredTunnel, ingressBytes, egressBytes uint64) {
	delta := trafficDeltaFromStoredTunnel(stored, ingressBytes, egressBytes)
	// This helper is called only by the Server relay data path. Stored
	// ActualTransport describes the selector for new connections and may already
	// be peer_direct while an older relay stream is still draining.
	delta.Transport = protocol.ActualTransportServerRelay
	s.recordTrafficDeltaAt(now, delta)
}

func (s *Server) recordTrafficDeltaAt(now time.Time, delta TrafficDelta) {
	if s == nil || s.trafficStore == nil {
		return
	}
	if delta.IngressBytes == 0 && delta.EgressBytes == 0 {
		return
	}
	if delta.TunnelID == "" || delta.Revision <= 0 {
		tunnelID, revision := s.resolveTrafficTunnelIdentity(delta.ClientID, delta.TunnelName, delta.TunnelType)
		if delta.TunnelID == "" {
			delta.TunnelID = tunnelID
		}
		if delta.Revision <= 0 && tunnelID == delta.TunnelID {
			delta.Revision = revision
		}
	}
	s.trafficStore.attachAccumulator(s.trafficAccumulator)

	acc := s.trafficAccumulator
	if acc == nil {
		now = now.UTC()
		delta.SecondStart = secondFloorUTC(now).Unix()
		delta.MinuteStart = minuteFloorUTC(now).Unix()
		s.trafficStore.ApplyDeltas([]TrafficDelta{delta})
		return
	}

	if err := acc.AddDelta(now, delta); err == nil {
		return
	}

	// Overflow is practically unreachable for normal chunk sizes. Flush the current
	// batch and retry so the hot path can still preserve the observation.
	s.flushTrafficObservations()
	if err := acc.AddDelta(now, delta); err != nil {
		log.Printf("⚠️ Failed to aggregate traffic bytes for client %s tunnel %s: %v", delta.ClientID, delta.TunnelName, err)
		now = now.UTC()
		delta.SecondStart = secondFloorUTC(now).Unix()
		delta.MinuteStart = minuteFloorUTC(now).Unix()
		s.trafficStore.ApplyDeltas([]TrafficDelta{delta})
	}
}

func trafficDeltaFromProxyConfig(clientID string, config protocol.ProxyConfig, ingressBytes, egressBytes uint64) TrafficDelta {
	ownerClientID := config.OwnerClientID
	if ownerClientID == "" {
		ownerClientID = clientID
	}
	ingressClientID := ""
	if config.Ingress != nil {
		ingressClientID = config.Ingress.ClientID
	}
	targetClientID := clientID
	if config.Target != nil && config.Target.ClientID != "" {
		targetClientID = config.Target.ClientID
	}
	topology := config.Topology
	if topology == "" {
		topology = TunnelTopologyServerExpose
	}
	return TrafficDelta{
		TunnelID:        config.ID,
		Revision:        config.Revision,
		ClientID:        clientID,
		OwnerClientID:   ownerClientID,
		IngressClientID: ingressClientID,
		TargetClientID:  targetClientID,
		Topology:        topology,
		Transport:       relayTrafficTransport(config.ActualTransport),
		TunnelName:      config.Name,
		TunnelType:      config.Type,
		IngressBytes:    ingressBytes,
		EgressBytes:     egressBytes,
	}
}

func trafficDeltaFromStoredTunnel(stored StoredTunnel, ingressBytes, egressBytes uint64) TrafficDelta {
	ownerClientID := stored.OwnerClientID
	if ownerClientID == "" {
		ownerClientID = stored.ClientID
	}
	if ownerClientID == "" {
		ownerClientID = stored.Target.ClientID
	}
	targetClientID := stored.Target.ClientID
	if targetClientID == "" {
		targetClientID = ownerClientID
	}
	topology := stored.Topology
	if topology == "" {
		topology = TunnelTopologyServerExpose
	}
	return TrafficDelta{
		TunnelID:        stored.ID,
		Revision:        stored.Revision,
		ClientID:        ownerClientID,
		OwnerClientID:   ownerClientID,
		IngressClientID: stored.Ingress.ClientID,
		TargetClientID:  targetClientID,
		Topology:        topology,
		Transport:       relayTrafficTransport(stored.ActualTransport),
		TunnelName:      stored.Name,
		TunnelType:      stored.Type,
		IngressBytes:    ingressBytes,
		EgressBytes:     egressBytes,
	}
}

func relayTrafficTransport(actual string) string {
	if actual == "" || actual == TunnelActualTransportUnknown {
		return TunnelActualTransportServerRelay
	}
	return actual
}

func (s *Server) resolveTrafficTunnelIdentity(clientID, tunnelName, tunnelType string) (string, int64) {
	if s == nil || clientID == "" || tunnelName == "" {
		return "", 0
	}
	if value, ok := s.clients.Load(clientID); ok {
		if client, ok := value.(*ClientConn); ok {
			client.proxyMu.RLock()
			tunnel, ok := client.proxies[tunnelName]
			if ok && tunnel != nil && tunnel.Config.ID != "" && (tunnelType == "" || tunnel.Config.Type == tunnelType) {
				tunnelID := tunnel.Config.ID
				revision := tunnel.Config.Revision
				client.proxyMu.RUnlock()
				return tunnelID, revision
			}
			client.proxyMu.RUnlock()
		}
	}
	if s.store != nil {
		if stored, ok := s.store.GetTunnel(clientID, tunnelName); ok && stored.ID != "" && (tunnelType == "" || stored.Type == tunnelType) {
			return stored.ID, stored.Revision
		}
	}
	return "", 0
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
