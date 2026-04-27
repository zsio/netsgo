package server

import (
	"database/sql"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	trafficFlushInterval   = 10 * time.Second
	trafficMinuteRetention = 24 * time.Hour
	trafficHourRetention   = 7 * 24 * time.Hour
	trafficMaxRange        = 7 * 24 * time.Hour
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

type trafficSeriesKey struct {
	TunnelName string
	TunnelType string
}

type TrafficQueryResult struct {
	Resolution TrafficResolution     `json:"resolution"`
	Items      []TunnelTrafficSeries `json:"items"`
}

type TrafficStore struct {
	path      string
	db        *sql.DB
	closeDB   bool
	mu        sync.RWMutex
	closeOnce sync.Once
	closeErr  error

	pendingMinute map[string]TrafficBucket

	failSaveErr   error
	failSaveCount int
}

func NewTrafficStore(path string) (*TrafficStore, error) {
	db, err := openServerDB(path)
	if err != nil {
		return nil, err
	}
	return newTrafficStoreWithDB(path, db, true), nil
}

func newTrafficStoreWithDB(path string, db *sql.DB, closeDB bool) *TrafficStore {
	return &TrafficStore{
		path:          path,
		db:            db,
		closeDB:       closeDB,
		pendingMinute: make(map[string]TrafficBucket),
	}
}

func (s *TrafficStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if !s.closeDB {
		return nil
	}
	s.closeOnce.Do(func() {
		s.closeErr = s.db.Close()
	})
	return s.closeErr
}

func (s *TrafficStore) maybeFailSave() error {
	if s.failSaveErr != nil && s.failSaveCount > 0 {
		err := s.failSaveErr
		s.failSaveCount--
		if s.failSaveCount == 0 {
			s.failSaveErr = nil
		}
		return err
	}
	return nil
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
		if existing, ok := s.pendingMinute[key]; ok {
			existing.IngressBytes += bucket.IngressBytes
			existing.EgressBytes += bucket.EgressBytes
			s.pendingMinute[key] = existing
		} else {
			s.pendingMinute[key] = bucket
		}
	}
}

func (s *TrafficStore) Compact(now time.Time) error {
	now = now.UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.flushLocked(); err != nil {
		return err
	}
	if err := s.rollupCompleteHoursLocked(now); err != nil {
		return err
	}
	if err := s.pruneLocked(now); err != nil {
		return err
	}
	return nil
}

func (s *TrafficStore) Query(clientID, tunnelName string, from, to time.Time) (TrafficQueryResult, error) {
	return s.QueryWithResolution(clientID, tunnelName, from, to, autoTrafficResolution(from, to))
}

func (s *TrafficStore) QueryWithResolution(clientID, tunnelName string, from, to time.Time, resolution TrafficResolution) (TrafficQueryResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.queryLocked(clientID, tunnelName, from, to, resolution)
}

func (s *TrafficStore) queryLocked(clientID, tunnelName string, from, to time.Time, resolution TrafficResolution) (TrafficQueryResult, error) {
	buckets := []TrafficBucket{}
	if resolution == TrafficResolutionMinute {
		combined := make(map[string]TrafficBucket)
		persisted, err := s.queryBucketsLocked(clientID, tunnelName, TrafficResolutionMinute, minuteFloorUTC(from).Unix(), minuteFloorUTC(to).Unix())
		if err != nil {
			return TrafficQueryResult{}, err
		}
		for _, bucket := range persisted {
			addTrafficBucket(combined, bucket)
		}
		for _, bucket := range s.pendingMinute {
			if bucketMatchesRange(bucket, clientID, tunnelName, minuteFloorUTC(from).Unix(), minuteFloorUTC(to).Unix()) {
				addTrafficBucket(combined, bucket)
			}
		}
		for _, bucket := range combined {
			buckets = append(buckets, bucket)
		}
	} else {
		combined := make(map[string]TrafficBucket)
		currentHourStart := hourFloorUTC(time.Now()).Unix()
		persistedHours, err := s.queryBucketsLocked(clientID, tunnelName, TrafficResolutionHour, hourFloorUTC(from).Unix(), hourFloorUTC(to).Unix())
		if err != nil {
			return TrafficQueryResult{}, err
		}
		for _, bucket := range persistedHours {
			combined[trafficBucketKey(bucket)] = bucket
		}

		minuteBuckets, err := s.queryBucketsLocked(clientID, tunnelName, TrafficResolutionMinute, minuteFloorUTC(from).Unix(), minuteFloorUTC(to).Unix())
		if err != nil {
			return TrafficQueryResult{}, err
		}
		for _, bucket := range minuteBuckets {
			foldMinuteBucketIntoHour(combined, bucket, currentHourStart, true)
		}

		for _, bucket := range s.pendingMinute {
			if bucketMatchesRange(bucket, clientID, tunnelName, minuteFloorUTC(from).Unix(), minuteFloorUTC(to).Unix()) {
				foldMinuteBucketIntoHour(combined, bucket, currentHourStart, false)
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
		if buckets[i].TunnelType != buckets[j].TunnelType {
			return buckets[i].TunnelType < buckets[j].TunnelType
		}
		return buckets[i].BucketStart < buckets[j].BucketStart
	})

	seriesMap := make(map[trafficSeriesKey]*TunnelTrafficSeries)
	for _, bucket := range buckets {
		key := trafficSeriesKey{
			TunnelName: bucket.TunnelName,
			TunnelType: bucket.TunnelType,
		}
		series, ok := seriesMap[key]
		if !ok {
			series = &TunnelTrafficSeries{
				TunnelName: bucket.TunnelName,
				TunnelType: bucket.TunnelType,
				Points:     []TrafficPoint{},
			}
			seriesMap[key] = series
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
	sort.Slice(items, func(i, j int) bool {
		if items[i].TunnelName != items[j].TunnelName {
			return items[i].TunnelName < items[j].TunnelName
		}
		return items[i].TunnelType < items[j].TunnelType
	})

	return TrafficQueryResult{
		Resolution: resolution,
		Items:      items,
	}, nil
}

func (s *TrafficStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.flushLocked()
}

func (s *TrafficStore) flushLocked() error {
	if len(s.pendingMinute) == 0 {
		return nil
	}
	if err := s.maybeFailSave(); err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	currentHourStart := hourFloorUTC(time.Now()).Unix()
	for _, bucket := range collectSortedBuckets(s.pendingMinute) {
		if err := upsertTrafficBucketAdd(tx, bucket); err != nil {
			return err
		}
		if err := addTrafficToExistingHourBucket(tx, bucket, currentHourStart); err != nil {
			return err
		}
	}
	if err := commitTx(tx, &committed); err != nil {
		return err
	}

	s.pendingMinute = make(map[string]TrafficBucket)
	return nil
}

func (s *TrafficStore) queryBucketsLocked(clientID, tunnelName string, resolution TrafficResolution, fromUnix, toUnix int64) ([]TrafficBucket, error) {
	query := `SELECT client_id, tunnel_name, tunnel_type, resolution, bucket_start, ingress_bytes, egress_bytes
FROM traffic_buckets
WHERE client_id = ? AND resolution = ? AND bucket_start >= ? AND bucket_start <= ?`
	args := []any{clientID, string(resolution), fromUnix, toUnix}
	if tunnelName != "" {
		query += ` AND tunnel_name = ?`
		args = append(args, tunnelName)
	}
	query += ` ORDER BY tunnel_name, tunnel_type, bucket_start`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return scanTrafficBucketRows(rows)
}

func scanTrafficBucketRows(rows *sql.Rows) ([]TrafficBucket, error) {
	defer func() { _ = rows.Close() }()

	buckets := []TrafficBucket{}
	for rows.Next() {
		bucket, err := scanTrafficBucket(rows)
		if err != nil {
			return nil, err
		}
		buckets = append(buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return buckets, nil
}

func scanTrafficBucket(row dbScanner) (TrafficBucket, error) {
	var bucket TrafficBucket
	var ingressBytes, egressBytes int64
	err := row.Scan(
		&bucket.ClientID,
		&bucket.TunnelName,
		&bucket.TunnelType,
		&bucket.Resolution,
		&bucket.BucketStart,
		&ingressBytes,
		&egressBytes,
	)
	if err != nil {
		return TrafficBucket{}, err
	}
	bucket.IngressBytes, err = sqliteUint64("traffic_buckets.ingress_bytes", ingressBytes)
	if err != nil {
		return TrafficBucket{}, err
	}
	bucket.EgressBytes, err = sqliteUint64("traffic_buckets.egress_bytes", egressBytes)
	if err != nil {
		return TrafficBucket{}, err
	}
	return bucket, nil
}

func (s *TrafficStore) rollupCompleteHoursLocked(now time.Time) error {
	currentHourStart := hourFloorUTC(now).Unix()
	rows, err := s.db.Query(`SELECT client_id, tunnel_name, tunnel_type, (bucket_start / 3600) * 3600 AS hour_start, SUM(ingress_bytes), SUM(egress_bytes)
FROM traffic_buckets
WHERE resolution = ? AND bucket_start < ?
GROUP BY client_id, tunnel_name, tunnel_type, hour_start`, string(TrafficResolutionMinute), currentHourStart)
	if err != nil {
		return err
	}

	rolled := []TrafficBucket{}
	for rows.Next() {
		var bucket TrafficBucket
		var ingressBytes, egressBytes int64
		if err := rows.Scan(&bucket.ClientID, &bucket.TunnelName, &bucket.TunnelType, &bucket.BucketStart, &ingressBytes, &egressBytes); err != nil {
			_ = rows.Close()
			return err
		}
		bucket.Resolution = TrafficResolutionHour
		bucket.IngressBytes, err = sqliteUint64("traffic_buckets.ingress_bytes", ingressBytes)
		if err != nil {
			_ = rows.Close()
			return err
		}
		bucket.EgressBytes, err = sqliteUint64("traffic_buckets.egress_bytes", egressBytes)
		if err != nil {
			_ = rows.Close()
			return err
		}
		rolled = append(rolled, bucket)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	for _, bucket := range rolled {
		if err := upsertTrafficBucketReplace(tx, bucket); err != nil {
			return err
		}
	}
	return commitTx(tx, &committed)
}

func (s *TrafficStore) pruneLocked(now time.Time) error {
	minuteCutoff := minuteFloorUTC(now.Add(-trafficMinuteRetention)).Unix()
	hourCutoff := hourFloorUTC(now.Add(-trafficHourRetention)).Unix()

	if _, err := s.db.Exec(`DELETE FROM traffic_buckets WHERE resolution = ? AND bucket_start < ?`, string(TrafficResolutionMinute), minuteCutoff); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM traffic_buckets WHERE resolution = ? AND bucket_start < ?`, string(TrafficResolutionHour), hourCutoff); err != nil {
		return err
	}
	return nil
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
		if buckets[i].TunnelType != buckets[j].TunnelType {
			return buckets[i].TunnelType < buckets[j].TunnelType
		}
		if buckets[i].Resolution != buckets[j].Resolution {
			return buckets[i].Resolution < buckets[j].Resolution
		}
		return buckets[i].BucketStart < buckets[j].BucketStart
	})
	return buckets
}

func addTrafficBucket(combined map[string]TrafficBucket, bucket TrafficBucket) {
	key := trafficBucketKey(bucket)
	if existing, ok := combined[key]; ok {
		existing.IngressBytes += bucket.IngressBytes
		existing.EgressBytes += bucket.EgressBytes
		combined[key] = existing
		return
	}
	combined[key] = bucket
}

func upsertTrafficBucketAdd(tx *sql.Tx, bucket TrafficBucket) error {
	ingressBytes, err := sqliteInt64("traffic_buckets.ingress_bytes", bucket.IngressBytes)
	if err != nil {
		return err
	}
	egressBytes, err := sqliteInt64("traffic_buckets.egress_bytes", bucket.EgressBytes)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO traffic_buckets (client_id, tunnel_name, tunnel_type, resolution, bucket_start, ingress_bytes, egress_bytes)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(client_id, tunnel_name, tunnel_type, resolution, bucket_start)
DO UPDATE SET
	ingress_bytes = ingress_bytes + excluded.ingress_bytes,
	egress_bytes = egress_bytes + excluded.egress_bytes`,
		bucket.ClientID,
		bucket.TunnelName,
		bucket.TunnelType,
		string(bucket.Resolution),
		bucket.BucketStart,
		ingressBytes,
		egressBytes,
	)
	return err
}

func upsertTrafficBucketReplace(tx *sql.Tx, bucket TrafficBucket) error {
	ingressBytes, err := sqliteInt64("traffic_buckets.ingress_bytes", bucket.IngressBytes)
	if err != nil {
		return err
	}
	egressBytes, err := sqliteInt64("traffic_buckets.egress_bytes", bucket.EgressBytes)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO traffic_buckets (client_id, tunnel_name, tunnel_type, resolution, bucket_start, ingress_bytes, egress_bytes)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(client_id, tunnel_name, tunnel_type, resolution, bucket_start)
DO UPDATE SET
	ingress_bytes = excluded.ingress_bytes,
	egress_bytes = excluded.egress_bytes`,
		bucket.ClientID,
		bucket.TunnelName,
		bucket.TunnelType,
		string(bucket.Resolution),
		bucket.BucketStart,
		ingressBytes,
		egressBytes,
	)
	return err
}

func addTrafficToExistingHourBucket(tx *sql.Tx, bucket TrafficBucket, currentHourStart int64) error {
	hourStart := hourFloorUTC(time.Unix(bucket.BucketStart, 0).UTC()).Unix()
	if hourStart >= currentHourStart {
		return nil
	}

	ingressBytes, err := sqliteInt64("traffic_buckets.ingress_bytes", bucket.IngressBytes)
	if err != nil {
		return err
	}
	egressBytes, err := sqliteInt64("traffic_buckets.egress_bytes", bucket.EgressBytes)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`UPDATE traffic_buckets
SET ingress_bytes = ingress_bytes + ?, egress_bytes = egress_bytes + ?
WHERE client_id = ? AND tunnel_name = ? AND tunnel_type = ? AND resolution = ? AND bucket_start = ?`,
		ingressBytes,
		egressBytes,
		bucket.ClientID,
		bucket.TunnelName,
		bucket.TunnelType,
		string(TrafficResolutionHour),
		hourStart,
	)
	return err
}

func foldMinuteBucketIntoHour(combined map[string]TrafficBucket, bucket TrafficBucket, currentHourStart int64, skipAlreadyRolled bool) {
	hourBucket := TrafficBucket{
		ClientID:    bucket.ClientID,
		TunnelName:  bucket.TunnelName,
		TunnelType:  bucket.TunnelType,
		Resolution:  TrafficResolutionHour,
		BucketStart: hourFloorUTC(time.Unix(bucket.BucketStart, 0).UTC()).Unix(),
	}
	key := trafficBucketKey(hourBucket)
	if skipAlreadyRolled && hourBucket.BucketStart < currentHourStart {
		if _, ok := combined[key]; ok {
			return
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

func bucketMatchesRange(bucket TrafficBucket, clientID, tunnelName string, fromUnix, toUnix int64) bool {
	if !bucketMatches(bucket, clientID, tunnelName) {
		return false
	}
	return bucket.BucketStart >= fromUnix && bucket.BucketStart <= toUnix
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

func (s *TrafficStore) EvictTunnel(clientID, tunnelName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, bucket := range s.pendingMinute {
		if bucket.ClientID == clientID && bucket.TunnelName == tunnelName {
			delete(s.pendingMinute, key)
		}
	}
	_, err := s.db.Exec(`DELETE FROM traffic_buckets WHERE client_id = ? AND tunnel_name = ?`, clientID, tunnelName)
	return err
}

func (s *TrafficStore) EvictClient(clientID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, bucket := range s.pendingMinute {
		if bucket.ClientID == clientID {
			delete(s.pendingMinute, key)
		}
	}
	_, err := s.db.Exec(`DELETE FROM traffic_buckets WHERE client_id = ?`, clientID)
	return err
}
