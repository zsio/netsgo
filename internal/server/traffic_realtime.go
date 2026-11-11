package server

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"time"
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
			eventsByOwner := s.collectRealtimeTrafficEvents(now)
			ownerUserIDs := make([]string, 0, len(eventsByOwner))
			for ownerUserID := range eventsByOwner {
				ownerUserIDs = append(ownerUserIDs, ownerUserID)
			}
			sort.Strings(ownerUserIDs)
			for _, ownerUserID := range ownerUserIDs {
				event := eventsByOwner[ownerUserID]
				if len(event.Clients) == 0 {
					continue
				}
				s.events.PublishScopedJSON("traffic_realtime", ownerUserID, event)
			}
		}
	}
}

// collectRealtimeTrafficEvents produces one browser payload per owner scope.
// The owner remains EventBus transport metadata and is never serialized in the
// traffic payload itself.
func (s *Server) collectRealtimeTrafficEvents(now time.Time) map[string]realtimeTrafficEvent {
	s.flushTrafficObservations()

	from, to := realtimeTrafficWindow(now)
	eventsByOwner := make(map[string]realtimeTrafficEvent)
	knownByOwner := make(map[string]map[string][]trafficSeriesKey)

	s.RangeClients(func(clientID string, client *ClientConn) bool {
		if !client.isLive() || client.OwnerUserID == "" {
			return true
		}

		knownByClient, loaded := knownByOwner[client.OwnerUserID]
		if !loaded {
			knownByClient = s.knownTrafficTunnelsByClientForUser(client.OwnerUserID)
			knownByOwner[client.OwnerUserID] = knownByClient
		}
		knownTunnels := knownByClient[clientID]
		if len(knownTunnels) == 0 {
			return true
		}

		result, err := s.buildRealtimeTrafficResultForUser(client.OwnerUserID, clientID, "", from, to, knownTunnels)
		if err != nil {
			log.Printf("⚠️ Failed to collect realtime traffic for user %s client %s: %v", client.OwnerUserID, clientID, err)
			return true
		}

		event := eventsByOwner[client.OwnerUserID]
		if event.GeneratedAt.IsZero() {
			event = realtimeTrafficEvent{GeneratedAt: now.UTC(), Clients: []realtimeTrafficClient{}}
		}
		event.Clients = append(event.Clients, realtimeTrafficClient{
			ClientID:   clientID,
			Resolution: result.Resolution,
			Items:      result.Items,
		})
		eventsByOwner[client.OwnerUserID] = event
		return true
	})

	for ownerUserID, event := range eventsByOwner {
		sort.Slice(event.Clients, func(i, j int) bool {
			return event.Clients[i].ClientID < event.Clients[j].ClientID
		})
		eventsByOwner[ownerUserID] = event
	}
	return eventsByOwner
}

func (s *Server) collectRealtimeTrafficForUser(ownerUserID string, now time.Time) (realtimeTrafficEvent, error) {
	event := realtimeTrafficEvent{
		GeneratedAt: now.UTC(),
		Clients:     []realtimeTrafficClient{},
	}
	if ownerUserID == "" || s == nil || s.trafficStore == nil {
		return event, nil
	}

	s.flushTrafficObservations()

	from, to := realtimeTrafficWindow(now)
	var knownByClient map[string][]trafficSeriesKey
	knownLoaded := false
	var firstErr error

	s.RangeClients(func(clientID string, client *ClientConn) bool {
		if !client.isLive() || client.OwnerUserID != ownerUserID {
			return true
		}
		if !knownLoaded {
			knownByClient = s.knownTrafficTunnelsByClientForUser(ownerUserID)
			knownLoaded = true
		}
		knownTunnels := knownByClient[clientID]
		if len(knownTunnels) == 0 {
			return true
		}

		result, err := s.buildRealtimeTrafficResultForUser(ownerUserID, clientID, "", from, to, knownTunnels)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return true
		}
		event.Clients = append(event.Clients, realtimeTrafficClient{
			ClientID:   clientID,
			Resolution: result.Resolution,
			Items:      result.Items,
		})
		return true
	})
	if firstErr != nil {
		return realtimeTrafficEvent{}, firstErr
	}

	sort.Slice(event.Clients, func(i, j int) bool {
		return event.Clients[i].ClientID < event.Clients[j].ClientID
	})
	return event, nil
}

func (s *Server) buildRealtimeTrafficResultForUser(ownerUserID, clientID, tunnelName string, from, to time.Time, knownTunnels []trafficSeriesKey) (TrafficQueryResult, error) {
	result, err := s.trafficStore.QueryWithResolutionForUser(ownerUserID, clientID, tunnelName, from, to, TrafficResolutionSecond)
	if err != nil {
		return TrafficQueryResult{}, err
	}
	return fillRealtimeTrafficResult(result, knownTunnels, from, to)
}

func (s *Server) knownTrafficTunnelsByClientForUser(ownerUserID string) map[string][]trafficSeriesKey {
	if ownerUserID == "" || s == nil || s.store == nil {
		return nil
	}
	stored, err := s.store.GetTunnelsByUserID(ownerUserID)
	if err != nil {
		log.Printf("⚠️ failed to load tunnels for realtime traffic user %s: %v", ownerUserID, err)
		return nil
	}
	knownByClient := make(map[string]map[trafficSeriesKey]struct{})
	for _, tunnel := range stored {
		if tunnel.ClientID == "" {
			continue
		}
		key := trafficSeriesKey{TunnelID: tunnel.ID, TunnelName: tunnel.Name, TunnelType: tunnel.Type}
		if key.TunnelName == "" || key.TunnelType == "" {
			continue
		}
		set := knownByClient[tunnel.ClientID]
		if set == nil {
			set = make(map[trafficSeriesKey]struct{})
			knownByClient[tunnel.ClientID] = set
		}
		set[key] = struct{}{}
	}
	result := make(map[string][]trafficSeriesKey, len(knownByClient))
	for clientID, set := range knownByClient {
		keys := make([]trafficSeriesKey, 0, len(set))
		for key := range set {
			keys = append(keys, key)
		}
		sortTrafficSeriesKeys(keys)
		result[clientID] = keys
	}
	return result
}

func (s *Server) knownTrafficTunnelsForUser(ownerUserID, clientID, tunnelName string) []trafficSeriesKey {
	if ownerUserID == "" || s == nil || s.store == nil {
		return nil
	}
	known := make(map[trafficSeriesKey]struct{})
	stored, err := s.store.GetTunnelsByUserID(ownerUserID)
	if err != nil {
		log.Printf("⚠️ failed to load tunnels for realtime traffic user %s client %s: %v", ownerUserID, clientID, err)
		return nil
	}
	for _, tunnel := range stored {
		if tunnel.ClientID != clientID {
			continue
		}
		key := trafficSeriesKey{TunnelID: tunnel.ID, TunnelName: tunnel.Name, TunnelType: tunnel.Type}
		if key.TunnelName == "" || key.TunnelType == "" {
			continue
		}
		if tunnelName != "" && key.TunnelName != tunnelName && key.TunnelID != tunnelName {
			continue
		}
		known[key] = struct{}{}
	}
	keys := make([]trafficSeriesKey, 0, len(known))
	for key := range known {
		keys = append(keys, key)
	}
	sortTrafficSeriesKeys(keys)
	return keys
}

func filterTrafficResultByKnownTunnels(result TrafficQueryResult, knownTunnels []trafficSeriesKey) TrafficQueryResult {
	allowedSeries := make(map[trafficSeriesKey]struct{}, len(knownTunnels))
	for _, key := range knownTunnels {
		if key.TunnelName == "" || key.TunnelType == "" {
			continue
		}
		allowedSeries[key] = struct{}{}
	}

	items := make([]TunnelTrafficSeries, 0, len(result.Items))
	for _, item := range result.Items {
		key := trafficSeriesKey{TunnelID: item.TunnelID, TunnelName: item.TunnelName, TunnelType: item.TunnelType}
		if _, ok := allowedSeries[key]; !ok {
			continue
		}
		items = append(items, item)
	}
	result.Items = items
	return result
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
	allowedSeries := make(map[trafficSeriesKey]struct{}, len(knownTunnels))
	seriesSet := make(map[trafficSeriesKey]struct{})
	pointsBySeries := make(map[trafficSeriesKey]map[int64]TrafficPoint)

	for _, key := range knownTunnels {
		if key.TunnelName == "" || key.TunnelType == "" {
			continue
		}
		allowedSeries[key] = struct{}{}
		seriesSet[key] = struct{}{}
	}

	for _, item := range result.Items {
		key := trafficSeriesKey{TunnelID: item.TunnelID, TunnelName: item.TunnelName, TunnelType: item.TunnelType}
		if key.TunnelName == "" || key.TunnelType == "" {
			continue
		}
		if _, ok := allowedSeries[key]; !ok {
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
