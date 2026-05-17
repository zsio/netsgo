package server

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"netsgo/pkg/protocol"
)

type realtimeTrafficClient struct {
	ClientID   string                `json:"client_id"`
	Resolution TrafficResolution     `json:"resolution"`
	Items      []TunnelTrafficSeries `json:"items"`
}

type realtimeTrafficEvent struct {
	GeneratedAt time.Time               `json:"generated_at"`
	Clients     []realtimeTrafficClient `json:"clients"`
}

func realtimeTrafficWindow(now time.Time) (time.Time, time.Time) {
	to := secondFloorUTC(now).Add(-time.Second)
	from := to.Add(-time.Duration(trafficRealtimePointCount-1) * time.Second)
	return from, to
}

func validateRealtimeTrafficTimeRange(from, to time.Time) error {
	from = secondFloorUTC(from)
	to = secondFloorUTC(to)
	if from.After(to) {
		return errors.New("from must be before to")
	}
	pointCount := int(to.Sub(from)/time.Second) + 1
	if pointCount > trafficRealtimePointCount {
		return fmt.Errorf("second resolution range must contain at most %d points", trafficRealtimePointCount)
	}
	return nil
}

func (s *Server) trafficRealtimeLoop() {
	ticker := time.NewTicker(trafficRealtimePushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case now := <-ticker.C:
			if s.trafficStore == nil || s.events == nil || !s.events.HasSubscribers() {
				continue
			}
			event := s.collectRealtimeTrafficEvent(now)
			if len(event.Clients) == 0 {
				continue
			}
			s.events.PublishJSON("traffic_realtime", event)
		}
	}
}

func (s *Server) collectRealtimeTrafficEvent(now time.Time) realtimeTrafficEvent {
	s.flushTrafficObservations()

	from, to := realtimeTrafficWindow(now)
	event := realtimeTrafficEvent{
		GeneratedAt: now.UTC(),
		Clients:     []realtimeTrafficClient{},
	}

	s.RangeClients(func(clientID string, client *ClientConn) bool {
		if !client.isLive() {
			return true
		}

		knownTunnels := trafficSeriesKeysFromProxyConfigs(client.ProxyConfigsSnapshot(), "")
		if len(knownTunnels) == 0 {
			return true
		}

		result, err := s.buildRealtimeTrafficResult(clientID, "", from, to, knownTunnels)
		if err != nil {
			log.Printf("⚠️ Failed to collect realtime traffic for client %s: %v", clientID, err)
			return true
		}

		event.Clients = append(event.Clients, realtimeTrafficClient{
			ClientID:   clientID,
			Resolution: result.Resolution,
			Items:      result.Items,
		})
		return true
	})

	sort.Slice(event.Clients, func(i, j int) bool {
		return event.Clients[i].ClientID < event.Clients[j].ClientID
	})
	return event
}

func (s *Server) buildRealtimeTrafficResult(clientID, tunnelName string, from, to time.Time, knownTunnels []trafficSeriesKey) (TrafficQueryResult, error) {
	result, err := s.trafficStore.QueryWithResolution(clientID, tunnelName, from, to, TrafficResolutionSecond)
	if err != nil {
		return TrafficQueryResult{}, err
	}
	return fillRealtimeTrafficResult(result, knownTunnels, from, to)
}

func (s *Server) knownTrafficTunnels(clientID, tunnelName string) []trafficSeriesKey {
	known := make(map[trafficSeriesKey]struct{})
	add := func(key trafficSeriesKey) {
		if key.TunnelName == "" || key.TunnelType == "" {
			return
		}
		if tunnelName != "" && key.TunnelName != tunnelName {
			return
		}
		known[key] = struct{}{}
	}

	if value, ok := s.clients.Load(clientID); ok {
		if client, ok := value.(*ClientConn); ok && client.isLive() {
			for _, key := range trafficSeriesKeysFromProxyConfigs(client.ProxyConfigsSnapshot(), tunnelName) {
				add(key)
			}
		}
	}

	if s.store != nil {
		stored, err := s.store.GetTunnelsByClientID(clientID)
		if err != nil {
			log.Printf("⚠️ failed to load tunnels for realtime traffic client %s: %v", clientID, err)
		} else {
			for _, tunnel := range stored {
				add(trafficSeriesKey{TunnelID: tunnel.ID, TunnelName: tunnel.Name, TunnelType: tunnel.Type})
			}
		}
	}

	keys := make([]trafficSeriesKey, 0, len(known))
	for key := range known {
		keys = append(keys, key)
	}
	sortTrafficSeriesKeys(keys)
	return keys
}

func trafficSeriesKeysFromProxyConfigs(configs []protocol.ProxyConfig, tunnelName string) []trafficSeriesKey {
	keys := make([]trafficSeriesKey, 0, len(configs))
	for _, config := range configs {
		if config.Name == "" || config.Type == "" {
			continue
		}
		if tunnelName != "" && config.Name != tunnelName {
			continue
		}
		keys = append(keys, trafficSeriesKey{TunnelID: config.ID, TunnelName: config.Name, TunnelType: config.Type})
	}
	sortTrafficSeriesKeys(keys)
	return keys
}

func fillRealtimeTrafficResult(result TrafficQueryResult, knownTunnels []trafficSeriesKey, from, to time.Time) (TrafficQueryResult, error) {
	from = secondFloorUTC(from)
	to = secondFloorUTC(to)
	if from.After(to) {
		return TrafficQueryResult{}, errors.New("from must be before to")
	}

	pointCount := int(to.Sub(from)/time.Second) + 1
	if pointCount > trafficRealtimePointCount {
		return TrafficQueryResult{}, fmt.Errorf("second resolution range must contain at most %d points", trafficRealtimePointCount)
	}

	fromUnix := from.Unix()
	toUnix := to.Unix()
	seriesSet := make(map[trafficSeriesKey]struct{})
	pointsBySeries := make(map[trafficSeriesKey]map[int64]TrafficPoint)

	for _, key := range knownTunnels {
		if key.TunnelName == "" || key.TunnelType == "" {
			continue
		}
		seriesSet[key] = struct{}{}
	}

	for _, item := range result.Items {
		key := trafficSeriesKey{TunnelID: item.TunnelID, TunnelName: item.TunnelName, TunnelType: item.TunnelType}
		if key.TunnelName == "" || key.TunnelType == "" {
			continue
		}
		seriesSet[key] = struct{}{}
		if pointsBySeries[key] == nil {
			pointsBySeries[key] = make(map[int64]TrafficPoint)
		}
		for _, point := range item.Points {
			timestamp := secondFloorUTC(point.BucketStart).Unix()
			if timestamp < fromUnix || timestamp > toUnix {
				continue
			}
			point.BucketStart = time.Unix(timestamp, 0).UTC()
			pointsBySeries[key][timestamp] = point
		}
	}

	keys := make([]trafficSeriesKey, 0, len(seriesSet))
	for key := range seriesSet {
		keys = append(keys, key)
	}
	sortTrafficSeriesKeys(keys)

	items := make([]TunnelTrafficSeries, 0, len(keys))
	for _, key := range keys {
		points := make([]TrafficPoint, 0, pointCount)
		pointMap := pointsBySeries[key]
		for i := 0; i < pointCount; i++ {
			timestamp := fromUnix + int64(i)
			if point, ok := pointMap[timestamp]; ok {
				points = append(points, point)
				continue
			}
			points = append(points, TrafficPoint{BucketStart: time.Unix(timestamp, 0).UTC()})
		}
		items = append(items, TunnelTrafficSeries{
			TunnelID:   key.TunnelID,
			TunnelName: key.TunnelName,
			TunnelType: key.TunnelType,
			Points:     points,
		})
	}

	return TrafficQueryResult{Resolution: TrafficResolutionSecond, Items: items}, nil
}

func sortTrafficSeriesKeys(keys []trafficSeriesKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].TunnelName != keys[j].TunnelName {
			return keys[i].TunnelName < keys[j].TunnelName
		}
		if keys[i].TunnelType != keys[j].TunnelType {
			return keys[i].TunnelType < keys[j].TunnelType
		}
		return keys[i].TunnelID < keys[j].TunnelID
	})
}
