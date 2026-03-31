package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"netsgo/pkg/fileutil"
)

const (
	trafficFlushInterval   = 10 * time.Second
	trafficMinuteRetention = 24 * time.Hour
	trafficHourRetention   = 30 * 24 * time.Hour
	trafficMaxRange        = 30 * 24 * time.Hour
	trafficSchemaVersion   = 1
)

type TrafficResolution string

const (
	TrafficResolutionMinute TrafficResolution = "minute"
	TrafficResolutionHour   TrafficResolution = "hour"
)

type TrafficDelta struct {
	ClientID     string
	TunnelName   string
	TunnelType   string
	MinuteStart  int64
	IngressBytes uint64
	EgressBytes  uint64
}

type TrafficBucket struct {
	ClientID     string            `json:"client_id"`
	TunnelName   string            `json:"tunnel_name"`
	TunnelType   string            `json:"tunnel_type"`
	Resolution   TrafficResolution `json:"resolution"`
	BucketStart  int64             `json:"bucket_start"`
	IngressBytes uint64            `json:"ingress_bytes"`
	EgressBytes  uint64            `json:"egress_bytes"`
}

type TrafficPoint struct {
	BucketStart  time.Time `json:"bucket_start"`
	IngressBytes uint64    `json:"ingress_bytes"`
	EgressBytes  uint64    `json:"egress_bytes"`
	TotalBytes   uint64    `json:"total_bytes"`
}

type TunnelTrafficSeries struct {
	TunnelName string         `json:"tunnel_name"`
	TunnelType string         `json:"tunnel_type"`
	Points     []TrafficPoint `json:"points"`
}

type TrafficQueryResult struct {
	Resolution TrafficResolution     `json:"resolution"`
	Items      []TunnelTrafficSeries `json:"items"`
}

type trafficStoreSnapshot struct {
	SchemaVersion int             `json:"schema_version"`
	MinuteBuckets []TrafficBucket `json:"minute_buckets"`
	HourBuckets   []TrafficBucket `json:"hour_buckets"`
}

type TrafficStore struct {
	path          string
	mu            sync.RWMutex
	minuteBuckets map[string]TrafficBucket
	hourBuckets   map[string]TrafficBucket
	dirty         bool

	failSaveErr   error
	failSaveCount int
}

func NewTrafficStore(path string) (*TrafficStore, error) {
	store := &TrafficStore{
		path:          path,
		minuteBuckets: make(map[string]TrafficBucket),
		hourBuckets:   make(map[string]TrafficBucket),
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建流量存储目录失败: %w", err)
	}

	if _, err := os.Stat(path); err == nil {
		if err := store.load(); err != nil {
			return nil, fmt.Errorf("加载流量存储失败: %w", err)
		}
	}

	return store, nil
}

func (s *TrafficStore) ApplyDeltas(deltas []TrafficDelta) {
	if len(deltas) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, delta := range deltas {
		if delta.ClientID == "" || delta.TunnelName == "" || delta.TunnelType == "" {
			continue
		}
		if delta.IngressBytes == 0 && delta.EgressBytes == 0 {
			continue
		}

		bucket := TrafficBucket{
			ClientID:     delta.ClientID,
			TunnelName:   delta.TunnelName,
			TunnelType:   delta.TunnelType,
			Resolution:   TrafficResolutionMinute,
			BucketStart:  delta.MinuteStart,
			IngressBytes: delta.IngressBytes,
			EgressBytes:  delta.EgressBytes,
		}

		key := trafficBucketKey(bucket)
		if existing, ok := s.minuteBuckets[key]; ok {
			existing.IngressBytes += bucket.IngressBytes
			existing.EgressBytes += bucket.EgressBytes
			s.minuteBuckets[key] = existing
		} else {
			s.minuteBuckets[key] = bucket
		}
		s.dirty = true
	}
}

func (s *TrafficStore) Compact(now time.Time) {
	now = now.UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.rollupCompleteHoursLocked(now) {
		s.dirty = true
	}
	if s.pruneLocked(now) {
		s.dirty = true
	}
}

func (s *TrafficStore) Query(clientID, tunnelName string, from, to time.Time) TrafficQueryResult {
	return s.QueryWithResolution(clientID, tunnelName, from, to, autoTrafficResolution(from, to))
}

func (s *TrafficStore) QueryWithResolution(clientID, tunnelName string, from, to time.Time, resolution TrafficResolution) TrafficQueryResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.queryLocked(clientID, tunnelName, from, to, resolution)
}

func (s *TrafficStore) queryLocked(clientID, tunnelName string, from, to time.Time, resolution TrafficResolution) TrafficQueryResult {
	buckets := make([]TrafficBucket, 0)
	if resolution == TrafficResolutionMinute {
		for _, bucket := range s.minuteBuckets {
			if !bucketMatches(bucket, clientID, tunnelName) {
				continue
			}
			if bucket.BucketStart < minuteFloorUTC(from).Unix() || bucket.BucketStart > minuteFloorUTC(to).Unix() {
				continue
			}
			buckets = append(buckets, bucket)
		}
	} else {
		combined := make(map[string]TrafficBucket)
		currentHourStart := hourFloorUTC(time.Now()).Unix()
		for key, bucket := range s.hourBuckets {
			if !bucketMatches(bucket, clientID, tunnelName) {
				continue
			}
			if bucket.BucketStart < hourFloorUTC(from).Unix() || bucket.BucketStart > hourFloorUTC(to).Unix() {
				continue
			}
			combined[key] = bucket
		}

		for _, bucket := range s.minuteBuckets {
			if !bucketMatches(bucket, clientID, tunnelName) {
				continue
			}
			if bucket.BucketStart < minuteFloorUTC(from).Unix() || bucket.BucketStart > minuteFloorUTC(to).Unix() {
				continue
			}

			hourBucket := TrafficBucket{
				ClientID:    bucket.ClientID,
				TunnelName:  bucket.TunnelName,
				TunnelType:  bucket.TunnelType,
				Resolution:  TrafficResolutionHour,
				BucketStart: hourFloorUTC(time.Unix(bucket.BucketStart, 0).UTC()).Unix(),
			}
			key := trafficBucketKey(hourBucket)
			if hourBucket.BucketStart < currentHourStart {
				if _, ok := combined[key]; ok {
					continue
				}
			}
			if existing, ok := combined[key]; ok {
				existing.IngressBytes += bucket.IngressBytes
				existing.EgressBytes += bucket.EgressBytes
				combined[key] = existing
			} else {
				hourBucket.IngressBytes = bucket.IngressBytes
				hourBucket.EgressBytes = bucket.EgressBytes
				combined[key] = hourBucket
			}
		}

		for _, bucket := range combined {
			buckets = append(buckets, bucket)
		}
	}

	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].TunnelName != buckets[j].TunnelName {
			return buckets[i].TunnelName < buckets[j].TunnelName
		}
		return buckets[i].BucketStart < buckets[j].BucketStart
	})

	seriesMap := make(map[string]*TunnelTrafficSeries)
	for _, bucket := range buckets {
		series, ok := seriesMap[bucket.TunnelName]
		if !ok {
			series = &TunnelTrafficSeries{
				TunnelName: bucket.TunnelName,
				TunnelType: bucket.TunnelType,
				Points:     make([]TrafficPoint, 0),
			}
			seriesMap[bucket.TunnelName] = series
		}
		series.Points = append(series.Points, TrafficPoint{
			BucketStart:  time.Unix(bucket.BucketStart, 0).UTC(),
			IngressBytes: bucket.IngressBytes,
			EgressBytes:  bucket.EgressBytes,
			TotalBytes:   bucket.IngressBytes + bucket.EgressBytes,
		})
	}

	items := make([]TunnelTrafficSeries, 0, len(seriesMap))
	for _, series := range seriesMap {
		items = append(items, *series)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].TunnelName < items[j].TunnelName })

	return TrafficQueryResult{
		Resolution: resolution,
		Items:      items,
	}
}

func (s *TrafficStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.dirty {
		return nil
	}
	if s.failSaveErr != nil && s.failSaveCount > 0 {
		err := s.failSaveErr
		s.failSaveCount--
		if s.failSaveCount == 0 {
			s.failSaveErr = nil
		}
		return err
	}

	snapshot := trafficStoreSnapshot{
		SchemaVersion: trafficSchemaVersion,
		MinuteBuckets: collectSortedBuckets(s.minuteBuckets),
		HourBuckets:   collectSortedBuckets(s.hourBuckets),
	}

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	if err := fileutil.AtomicWriteFile(s.path, data, 0o600); err != nil {
		return err
	}

	s.dirty = false
	return nil
}

func (s *TrafficStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	var snapshot trafficStoreSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}

	s.minuteBuckets = make(map[string]TrafficBucket, len(snapshot.MinuteBuckets))
	for _, bucket := range snapshot.MinuteBuckets {
		if bucket.Resolution == "" {
			bucket.Resolution = TrafficResolutionMinute
		}
		s.minuteBuckets[trafficBucketKey(bucket)] = bucket
	}

	s.hourBuckets = make(map[string]TrafficBucket, len(snapshot.HourBuckets))
	for _, bucket := range snapshot.HourBuckets {
		if bucket.Resolution == "" {
			bucket.Resolution = TrafficResolutionHour
		}
		s.hourBuckets[trafficBucketKey(bucket)] = bucket
	}

	s.dirty = false
	return nil
}

func (s *TrafficStore) rollupCompleteHoursLocked(now time.Time) bool {
	currentHourStart := hourFloorUTC(now).Unix()
	aggregated := make(map[string]TrafficBucket)

	for _, bucket := range s.minuteBuckets {
		if bucket.BucketStart >= currentHourStart {
			continue
		}
		rolled := TrafficBucket{
			ClientID:    bucket.ClientID,
			TunnelName:  bucket.TunnelName,
			TunnelType:  bucket.TunnelType,
			Resolution:  TrafficResolutionHour,
			BucketStart: hourFloorUTC(time.Unix(bucket.BucketStart, 0).UTC()).Unix(),
		}
		key := trafficBucketKey(rolled)
		if existing, ok := aggregated[key]; ok {
			existing.IngressBytes += bucket.IngressBytes
			existing.EgressBytes += bucket.EgressBytes
			aggregated[key] = existing
		} else {
			rolled.IngressBytes = bucket.IngressBytes
			rolled.EgressBytes = bucket.EgressBytes
			aggregated[key] = rolled
		}
	}

	changed := false
	for key, bucket := range aggregated {
		if existing, ok := s.hourBuckets[key]; !ok || existing != bucket {
			s.hourBuckets[key] = bucket
			changed = true
		}
	}

	return changed
}

func (s *TrafficStore) pruneLocked(now time.Time) bool {
	minuteCutoff := minuteFloorUTC(now.Add(-trafficMinuteRetention)).Unix()
	hourCutoff := hourFloorUTC(now.Add(-trafficHourRetention)).Unix()
	changed := false

	for key, bucket := range s.minuteBuckets {
		if bucket.BucketStart < minuteCutoff {
			delete(s.minuteBuckets, key)
			changed = true
		}
	}
	for key, bucket := range s.hourBuckets {
		if bucket.BucketStart < hourCutoff {
			delete(s.hourBuckets, key)
			changed = true
		}
	}

	return changed
}

func collectSortedBuckets(source map[string]TrafficBucket) []TrafficBucket {
	buckets := make([]TrafficBucket, 0, len(source))
	for _, bucket := range source {
		buckets = append(buckets, bucket)
	}
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].ClientID != buckets[j].ClientID {
			return buckets[i].ClientID < buckets[j].ClientID
		}
		if buckets[i].TunnelName != buckets[j].TunnelName {
			return buckets[i].TunnelName < buckets[j].TunnelName
		}
		if buckets[i].Resolution != buckets[j].Resolution {
			return buckets[i].Resolution < buckets[j].Resolution
		}
		return buckets[i].BucketStart < buckets[j].BucketStart
	})
	return buckets
}

func trafficBucketKey(bucket TrafficBucket) string {
	return strings.Join([]string{
		bucket.ClientID,
		bucket.TunnelName,
		bucket.TunnelType,
		string(bucket.Resolution),
		strconv.FormatInt(bucket.BucketStart, 10),
	}, "\x00")
}

func bucketMatches(bucket TrafficBucket, clientID, tunnelName string) bool {
	if bucket.ClientID != clientID {
		return false
	}
	if tunnelName != "" && bucket.TunnelName != tunnelName {
		return false
	}
	return true
}

func minuteFloorUTC(t time.Time) time.Time {
	return t.UTC().Truncate(time.Minute)
}

func hourFloorUTC(t time.Time) time.Time {
	return t.UTC().Truncate(time.Hour)
}

func autoTrafficResolution(from, to time.Time) TrafficResolution {
	if to.Sub(from) <= trafficMinuteRetention {
		return TrafficResolutionMinute
	}
	return TrafficResolutionHour
}

func (s *TrafficStore) RecordBytes(clientID, tunnelName, tunnelType string, ingressBytes, egressBytes uint64) {
	minuteStart := minuteFloorUTC(time.Now()).Unix()
	s.ApplyDeltas([]TrafficDelta{{
		ClientID:     clientID,
		TunnelName:   tunnelName,
		TunnelType:   tunnelType,
		MinuteStart:  minuteStart,
		IngressBytes: ingressBytes,
		EgressBytes:  egressBytes,
	}})
}

func (s *TrafficStore) EvictTunnel(clientID, tunnelName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	for key, bucket := range s.minuteBuckets {
		if bucket.ClientID == clientID && bucket.TunnelName == tunnelName {
			delete(s.minuteBuckets, key)
			changed = true
		}
	}
	for key, bucket := range s.hourBuckets {
		if bucket.ClientID == clientID && bucket.TunnelName == tunnelName {
			delete(s.hourBuckets, key)
			changed = true
		}
	}
	if changed {
		s.dirty = true
	}
}

func (s *TrafficStore) EvictClient(clientID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	for key, bucket := range s.minuteBuckets {
		if bucket.ClientID == clientID {
			delete(s.minuteBuckets, key)
			changed = true
		}
	}
	for key, bucket := range s.hourBuckets {
		if bucket.ClientID == clientID {
			delete(s.hourBuckets, key)
			changed = true
		}
	}
	if changed {
		s.dirty = true
	}
}
