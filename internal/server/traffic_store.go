package server

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	trafficFlushInterval        = 10 * time.Second
	trafficRealtimePointCount   = 60
	trafficRealtimePushInterval = time.Second
	trafficMinuteRetention      = 24 * time.Hour
	trafficHourRetention        = 7 * 24 * time.Hour
	trafficMaxRange             = 7 * 24 * time.Hour
)

type TrafficResolution string

const (
	TrafficResolutionSecond TrafficResolution = "second"
	TrafficResolutionMinute TrafficResolution = "minute"
	TrafficResolutionHour   TrafficResolution = "hour"
)

type TrafficDelta struct {
	TunnelID        string
	ClientID        string
	OwnerClientID   string
	IngressClientID string
	TargetClientID  string
	Topology        string
	Transport       string
	TunnelName      string
	TunnelType      string
	SecondStart     int64
	MinuteStart     int64
	IngressBytes    uint64
	EgressBytes     uint64
}

type TrafficBucket struct {
	TunnelID        string            `json:"tunnel_id,omitempty"`
	ClientID        string            `json:"client_id"`
	OwnerClientID   string            `json:"owner_client_id,omitempty"`
	IngressClientID string            `json:"ingress_client_id,omitempty"`
	TargetClientID  string            `json:"target_client_id,omitempty"`
	Topology        string            `json:"topology,omitempty"`
	Transport       string            `json:"transport,omitempty"`
	TunnelName      string            `json:"tunnel_name"`
	TunnelType      string            `json:"tunnel_type"`
	MetadataMissing bool              `json:"metadata_missing,omitempty"`
	Resolution      TrafficResolution `json:"resolution"`
	BucketStart     int64             `json:"bucket_start"`
	IngressBytes    uint64            `json:"ingress_bytes"`
	EgressBytes     uint64            `json:"egress_bytes"`
}

type TrafficPoint struct {
	BucketStart  time.Time `json:"bucket_start"`
	IngressBytes uint64    `json:"ingress_bytes"`
	EgressBytes  uint64    `json:"egress_bytes"`
	TotalBytes   uint64    `json:"total_bytes"`
}

type TunnelTrafficSeries struct {
	TunnelID        string         `json:"tunnel_id,omitempty"`
	TunnelName      string         `json:"tunnel_name,omitempty"`
	TunnelType      string         `json:"tunnel_type,omitempty"`
	MetadataMissing bool           `json:"metadata_missing,omitempty"`
	Points          []TrafficPoint `json:"points"`
}

type trafficSeriesKey struct {
	TunnelID   string
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

	realtimeSecond *realtimeSecondIndex
	pendingMinute  map[string]TrafficBucket
	pendingErr     error

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

// newTrafficStoreWithDB creates a traffic store over an existing DB handle.
// When closeDB is false the caller retains DB ownership.
func newTrafficStoreWithDB(path string, db *sql.DB, closeDB bool) *TrafficStore {
	return &TrafficStore{
		path:           path,
		db:             db,
		closeDB:        closeDB,
		realtimeSecond: newRealtimeSecondIndex(),
		pendingMinute:  make(map[string]TrafficBucket),
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

	var newestSecond int64
	for _, delta := range deltas {
		if delta.ClientID == "" || delta.TunnelName == "" || delta.TunnelType == "" {
			continue
		}
		if delta.IngressBytes == 0 && delta.EgressBytes == 0 {
			continue
		}

		minuteBucket := TrafficBucket{
			TunnelID:        delta.TunnelID,
			ClientID:        delta.ClientID,
			OwnerClientID:   delta.OwnerClientID,
			IngressClientID: delta.IngressClientID,
			TargetClientID:  delta.TargetClientID,
			Topology:        delta.Topology,
			Transport:       delta.Transport,
			TunnelName:      delta.TunnelName,
			TunnelType:      delta.TunnelType,
			Resolution:      TrafficResolutionMinute,
			BucketStart:     delta.MinuteStart,
			IngressBytes:    delta.IngressBytes,
			EgressBytes:     delta.EgressBytes,
		}

		key := trafficBucketKey(minuteBucket)
		if existing, ok := s.pendingMinute[key]; ok {
			if err := addTrafficBucketValues(&existing, minuteBucket); err != nil {
				s.pendingErr = err
				continue
			}
			s.pendingMinute[key] = existing
		} else {
			s.pendingMinute[key] = minuteBucket
		}

		if delta.SecondStart == 0 {
			continue
		}

		secondBucket := TrafficBucket{
			TunnelID:        delta.TunnelID,
			ClientID:        delta.ClientID,
			OwnerClientID:   delta.OwnerClientID,
			IngressClientID: delta.IngressClientID,
			TargetClientID:  delta.TargetClientID,
			Topology:        delta.Topology,
			Transport:       delta.Transport,
			TunnelName:      delta.TunnelName,
			TunnelType:      delta.TunnelType,
			Resolution:      TrafficResolutionSecond,
			BucketStart:     delta.SecondStart,
			IngressBytes:    delta.IngressBytes,
			EgressBytes:     delta.EgressBytes,
		}
		if err := s.realtimeSecond.Add(secondBucket); err != nil {
			s.pendingErr = err
			continue
		}
		if delta.SecondStart > newestSecond {
			newestSecond = delta.SecondStart
		}
	}
	if newestSecond != 0 {
		s.pruneRealtimeLocked(time.Unix(newestSecond, 0).UTC())
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
	if s.pendingErr != nil {
		return TrafficQueryResult{}, s.pendingErr
	}

	buckets := []TrafficBucket{}
	switch resolution {
	case TrafficResolutionSecond:
		combined := make(map[string]TrafficBucket)
		fromUnix := secondFloorUTC(from).Unix()
		toUnix := secondFloorUTC(to).Unix()
		for _, bucket := range s.realtimeSecond.Query(clientID, tunnelName, fromUnix, toUnix) {
			if err := addTrafficBucket(combined, bucket); err != nil {
				return TrafficQueryResult{}, err
			}
		}
		for _, bucket := range combined {
			buckets = append(buckets, bucket)
		}
	case TrafficResolutionMinute:
		combined := make(map[string]TrafficBucket)
		persisted, err := s.queryBucketsLocked(clientID, tunnelName, TrafficResolutionMinute, minuteFloorUTC(from).Unix(), minuteFloorUTC(to).Unix())
		if err != nil {
			return TrafficQueryResult{}, err
		}
		for _, bucket := range persisted {
			if err := addTrafficBucket(combined, bucket); err != nil {
				return TrafficQueryResult{}, err
			}
		}
		for _, bucket := range s.pendingMinute {
			if bucketMatchesRange(bucket, clientID, tunnelName, minuteFloorUTC(from).Unix(), minuteFloorUTC(to).Unix()) {
				if err := addTrafficBucket(combined, bucket); err != nil {
					return TrafficQueryResult{}, err
				}
			}
		}
		for _, bucket := range combined {
			buckets = append(buckets, bucket)
		}
	case TrafficResolutionHour:
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
			if err := foldMinuteBucketIntoHour(combined, bucket, currentHourStart, true); err != nil {
				return TrafficQueryResult{}, err
			}
		}

		for _, bucket := range s.pendingMinute {
			if bucketMatchesRange(bucket, clientID, tunnelName, minuteFloorUTC(from).Unix(), minuteFloorUTC(to).Unix()) {
				if err := foldMinuteBucketIntoHour(combined, bucket, currentHourStart, false); err != nil {
					return TrafficQueryResult{}, err
				}
			}
		}

		for _, bucket := range combined {
			buckets = append(buckets, bucket)
		}
	default:
		return TrafficQueryResult{}, fmt.Errorf("invalid traffic resolution %q", resolution)
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
		key := trafficSeriesKeyFromBucket(bucket)
		series, ok := seriesMap[key]
		if !ok {
			series = &TunnelTrafficSeries{
				TunnelID:        bucket.TunnelID,
				TunnelName:      bucket.TunnelName,
				TunnelType:      bucket.TunnelType,
				MetadataMissing: trafficBucketMetadataMissing(bucket),
				Points:          []TrafficPoint{},
			}
			seriesMap[key] = series
		}
		totalBytes, err := checkedTrafficAdd("traffic point total_bytes", bucket.IngressBytes, bucket.EgressBytes)
		if err != nil {
			return TrafficQueryResult{}, err
		}
		series.Points = append(series.Points, TrafficPoint{
			BucketStart:  time.Unix(bucket.BucketStart, 0).UTC(),
			IngressBytes: bucket.IngressBytes,
			EgressBytes:  bucket.EgressBytes,
			TotalBytes:   totalBytes,
		})
	}

	items := make([]TunnelTrafficSeries, 0, len(seriesMap))
	for _, series := range seriesMap {
		items = append(items, *series)
	}
	sortTrafficSeries(items)

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
		s.pendingErr = nil
		return nil
	}
	if s.pendingErr != nil {
		return s.pendingErr
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
	s.pendingErr = nil
	return nil
}

func (s *TrafficStore) queryBucketsLocked(clientID, tunnelName string, resolution TrafficResolution, fromUnix, toUnix int64) ([]TrafficBucket, error) {
	query := `SELECT b.tunnel_id, b.owner_client_id, b.ingress_client_id, b.target_client_id, b.topology, b.transport, b.client_id, b.tunnel_name, b.tunnel_type, b.resolution, b.bucket_start, b.ingress_bytes, b.egress_bytes, CASE WHEN t.id IS NULL THEN 1 ELSE 0 END
FROM traffic_buckets b
LEFT JOIN tunnels t ON t.id = b.tunnel_id
WHERE b.client_id = ? AND b.resolution = ? AND b.bucket_start >= ? AND b.bucket_start <= ?`
	args := []any{clientID, string(resolution), fromUnix, toUnix}
	if tunnelName != "" {
		query += ` AND (b.tunnel_name = ? OR b.tunnel_id = ?)`
		args = append(args, tunnelName, tunnelName)
	}
	query += ` ORDER BY b.tunnel_name, b.tunnel_type, b.bucket_start`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return scanTrafficBucketRowsWithMetadata(rows)
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

func scanTrafficBucketRowsWithMetadata(rows *sql.Rows) ([]TrafficBucket, error) {
	defer func() { _ = rows.Close() }()

	buckets := []TrafficBucket{}
	for rows.Next() {
		bucket, err := scanTrafficBucketWithMetadata(rows)
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

func scanTrafficBucketWithMetadata(row dbScanner) (TrafficBucket, error) {
	var metadataMissing int
	bucket, err := scanTrafficBucketFields(row, &metadataMissing)
	if err != nil {
		return TrafficBucket{}, err
	}
	bucket.MetadataMissing = metadataMissing != 0
	return bucket, nil
}

func scanTrafficBucket(row dbScanner) (TrafficBucket, error) {
	return scanTrafficBucketFields(row)
}

func scanTrafficBucketFields(row dbScanner, extra ...any) (TrafficBucket, error) {
	var bucket TrafficBucket
	var ingressBytes, egressBytes int64
	dest := []any{
		&bucket.TunnelID,
		&bucket.OwnerClientID,
		&bucket.IngressClientID,
		&bucket.TargetClientID,
		&bucket.Topology,
		&bucket.Transport,
		&bucket.ClientID,
		&bucket.TunnelName,
		&bucket.TunnelType,
		&bucket.Resolution,
		&bucket.BucketStart,
		&ingressBytes,
		&egressBytes,
	}
	dest = append(dest, extra...)
	err := row.Scan(dest...)
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
	rows, err := s.db.Query(`SELECT tunnel_id, owner_client_id, ingress_client_id, target_client_id, topology, transport, client_id, tunnel_name, tunnel_type, (bucket_start / 3600) * 3600 AS hour_start, ingress_bytes, egress_bytes
FROM traffic_buckets
WHERE resolution = ? AND bucket_start < ?
ORDER BY client_id, tunnel_name, tunnel_type, hour_start`, string(TrafficResolutionMinute), currentHourStart)
	if err != nil {
		return err
	}

	rolled := make(map[string]TrafficBucket)
	for rows.Next() {
		var bucket TrafficBucket
		var ingressBytes, egressBytes int64
		if err := rows.Scan(&bucket.TunnelID, &bucket.OwnerClientID, &bucket.IngressClientID, &bucket.TargetClientID, &bucket.Topology, &bucket.Transport, &bucket.ClientID, &bucket.TunnelName, &bucket.TunnelType, &bucket.BucketStart, &ingressBytes, &egressBytes); err != nil {
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
		if err := addTrafficBucket(rolled, bucket); err != nil {
			_ = rows.Close()
			return err
		}
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

	for _, bucket := range collectSortedBuckets(rolled) {
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
	s.pruneRealtimeLocked(now)
	return nil
}

func (s *TrafficStore) pruneRealtimeLocked(now time.Time) {
	cutoff := secondFloorUTC(now).Add(-time.Duration(trafficRealtimePointCount-1) * time.Second).Unix()
	s.realtimeSecond.PruneBefore(cutoff)
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

func checkedTrafficAdd(field string, a, b uint64) (uint64, error) {
	if a > ^uint64(0)-b {
		return 0, fmt.Errorf("%s overflow adding %d + %d", field, a, b)
	}
	return a + b, nil
}

func addTrafficBucketValues(existing *TrafficBucket, bucket TrafficBucket) error {
	var err error
	existing.IngressBytes, err = checkedTrafficAdd("traffic_buckets.ingress_bytes", existing.IngressBytes, bucket.IngressBytes)
	if err != nil {
		return err
	}
	existing.EgressBytes, err = checkedTrafficAdd("traffic_buckets.egress_bytes", existing.EgressBytes, bucket.EgressBytes)
	if err != nil {
		return err
	}
	return nil
}

func addTrafficBucket(combined map[string]TrafficBucket, bucket TrafficBucket) error {
	key := trafficBucketKey(bucket)
	if existing, ok := combined[key]; ok {
		if err := addTrafficBucketValues(&existing, bucket); err != nil {
			return err
		}
		combined[key] = existing
		return nil
	}
	combined[key] = bucket
	return nil
}

func upsertTrafficBucketAdd(tx *sql.Tx, bucket TrafficBucket) error {
	if err := hydrateTrafficBucketTx(tx, &bucket); err != nil {
		return err
	}
	existing, found, err := findTrafficBucketTx(tx, bucket)
	if err != nil {
		return err
	}
	if found {
		if err := addTrafficBucketValues(&existing, bucket); err != nil {
			return err
		}
		return upsertTrafficBucketReplace(tx, existing)
	}

	ingressBytes, err := sqliteInt64("traffic_buckets.ingress_bytes", bucket.IngressBytes)
	if err != nil {
		return err
	}
	egressBytes, err := sqliteInt64("traffic_buckets.egress_bytes", bucket.EgressBytes)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO traffic_buckets (tunnel_id, owner_client_id, ingress_client_id, target_client_id, topology, transport, client_id, tunnel_name, tunnel_type, resolution, bucket_start, ingress_bytes, egress_bytes)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		bucket.TunnelID,
		bucket.OwnerClientID,
		bucket.IngressClientID,
		bucket.TargetClientID,
		bucket.Topology,
		bucket.Transport,
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

func findTrafficBucketTx(tx *sql.Tx, bucket TrafficBucket) (TrafficBucket, bool, error) {
	if err := hydrateTrafficBucketTx(tx, &bucket); err != nil {
		return TrafficBucket{}, false, err
	}
	row := tx.QueryRow(`SELECT tunnel_id, owner_client_id, ingress_client_id, target_client_id, topology, transport, client_id, tunnel_name, tunnel_type, resolution, bucket_start, ingress_bytes, egress_bytes
FROM traffic_buckets
WHERE tunnel_id = ? AND transport = ? AND resolution = ? AND bucket_start = ?`,
		bucket.TunnelID,
		bucket.Transport,
		string(bucket.Resolution),
		bucket.BucketStart,
	)
	existing, err := scanTrafficBucket(row)
	if err == sql.ErrNoRows {
		return TrafficBucket{}, false, nil
	}
	if err != nil {
		return TrafficBucket{}, false, err
	}
	return existing, true, nil
}

func upsertTrafficBucketReplace(tx *sql.Tx, bucket TrafficBucket) error {
	if err := hydrateTrafficBucketTx(tx, &bucket); err != nil {
		return err
	}
	ingressBytes, err := sqliteInt64("traffic_buckets.ingress_bytes", bucket.IngressBytes)
	if err != nil {
		return err
	}
	egressBytes, err := sqliteInt64("traffic_buckets.egress_bytes", bucket.EgressBytes)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO traffic_buckets (tunnel_id, owner_client_id, ingress_client_id, target_client_id, topology, transport, client_id, tunnel_name, tunnel_type, resolution, bucket_start, ingress_bytes, egress_bytes)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tunnel_id, transport, resolution, bucket_start)
DO UPDATE SET
	owner_client_id = excluded.owner_client_id,
	ingress_client_id = excluded.ingress_client_id,
	target_client_id = excluded.target_client_id,
	topology = excluded.topology,
	client_id = excluded.client_id,
	tunnel_name = excluded.tunnel_name,
	tunnel_type = excluded.tunnel_type,
	ingress_bytes = excluded.ingress_bytes,
	egress_bytes = excluded.egress_bytes`,
		bucket.TunnelID,
		bucket.OwnerClientID,
		bucket.IngressClientID,
		bucket.TargetClientID,
		bucket.Topology,
		bucket.Transport,
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
	if err := hydrateTrafficBucketTx(tx, &bucket); err != nil {
		return err
	}
	hourStart := hourFloorUTC(time.Unix(bucket.BucketStart, 0).UTC()).Unix()
	if hourStart >= currentHourStart {
		return nil
	}

	hourBucket := TrafficBucket{
		TunnelID:        bucket.TunnelID,
		ClientID:        bucket.ClientID,
		OwnerClientID:   bucket.OwnerClientID,
		IngressClientID: bucket.IngressClientID,
		TargetClientID:  bucket.TargetClientID,
		Topology:        bucket.Topology,
		Transport:       bucket.Transport,
		TunnelName:      bucket.TunnelName,
		TunnelType:      bucket.TunnelType,
		Resolution:      TrafficResolutionHour,
		BucketStart:     hourStart,
	}
	existing, found, err := findTrafficBucketTx(tx, hourBucket)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if err := addTrafficBucketValues(&existing, bucket); err != nil {
		return err
	}
	return upsertTrafficBucketReplace(tx, existing)
}

func foldMinuteBucketIntoHour(combined map[string]TrafficBucket, bucket TrafficBucket, currentHourStart int64, skipAlreadyRolled bool) error {
	hourBucket := TrafficBucket{
		TunnelID:        bucket.TunnelID,
		ClientID:        bucket.ClientID,
		OwnerClientID:   bucket.OwnerClientID,
		IngressClientID: bucket.IngressClientID,
		TargetClientID:  bucket.TargetClientID,
		Topology:        bucket.Topology,
		Transport:       bucket.Transport,
		TunnelName:      bucket.TunnelName,
		TunnelType:      bucket.TunnelType,
		Resolution:      TrafficResolutionHour,
		BucketStart:     hourFloorUTC(time.Unix(bucket.BucketStart, 0).UTC()).Unix(),
	}
	key := trafficBucketKey(hourBucket)
	if skipAlreadyRolled && hourBucket.BucketStart < currentHourStart {
		if _, ok := combined[key]; ok {
			return nil
		}
	}
	if existing, ok := combined[key]; ok {
		if err := addTrafficBucketValues(&existing, bucket); err != nil {
			return err
		}
		combined[key] = existing
	} else {
		hourBucket.IngressBytes = bucket.IngressBytes
		hourBucket.EgressBytes = bucket.EgressBytes
		combined[key] = hourBucket
	}
	return nil
}

func trafficBucketKey(bucket TrafficBucket) string {
	seriesKey := trafficSeriesKeyFromBucket(bucket)
	return strings.Join([]string{
		bucket.ClientID,
		seriesKey.TunnelID,
		seriesKey.TunnelName,
		seriesKey.TunnelType,
		string(bucket.Resolution),
		strconv.FormatInt(bucket.BucketStart, 10),
	}, "\x00")
}

func trafficSeriesKeyFromBucket(bucket TrafficBucket) trafficSeriesKey {
	return trafficSeriesKey{
		TunnelID:   bucket.TunnelID,
		TunnelName: bucket.TunnelName,
		TunnelType: bucket.TunnelType,
	}
}

func trafficBucketMetadataMissing(bucket TrafficBucket) bool {
	return bucket.MetadataMissing || (bucket.TunnelID != "" && (bucket.TunnelName == "" || bucket.TunnelType == ""))
}

func sortTrafficSeries(items []TunnelTrafficSeries) {
	sort.Slice(items, func(i, j int) bool {
		leftName := items[i].TunnelName
		rightName := items[j].TunnelName
		if leftName != rightName {
			return leftName < rightName
		}
		if items[i].TunnelType != items[j].TunnelType {
			return items[i].TunnelType < items[j].TunnelType
		}
		return items[i].TunnelID < items[j].TunnelID
	})
}

func bucketMatches(bucket TrafficBucket, clientID, tunnelName string) bool {
	if bucket.ClientID != clientID {
		return false
	}
	if tunnelName != "" && bucket.TunnelName != tunnelName && bucket.TunnelID != tunnelName {
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

func secondFloorUTC(t time.Time) time.Time {
	return t.UTC().Truncate(time.Second)
}

func minuteFloorUTC(t time.Time) time.Time {
	return t.UTC().Truncate(time.Minute)
}

func hourFloorUTC(t time.Time) time.Time {
	return t.UTC().Truncate(time.Hour)
}

func autoTrafficResolution(from, to time.Time) TrafficResolution {
	if secondFloorUTC(to).Sub(secondFloorUTC(from)) <= time.Duration(trafficRealtimePointCount-1)*time.Second {
		return TrafficResolutionSecond
	}
	if to.Sub(from) <= trafficMinuteRetention {
		return TrafficResolutionMinute
	}
	return TrafficResolutionHour
}

func (s *TrafficStore) recordBytesAt(now time.Time, clientID, tunnelName, tunnelType string, ingressBytes, egressBytes uint64) {
	now = now.UTC()
	s.ApplyDeltas([]TrafficDelta{{
		ClientID:     clientID,
		TunnelName:   tunnelName,
		TunnelType:   tunnelType,
		SecondStart:  secondFloorUTC(now).Unix(),
		MinuteStart:  minuteFloorUTC(now).Unix(),
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
	s.realtimeSecond.EvictTunnel(clientID, tunnelName)
	if len(s.pendingMinute) == 0 {
		s.pendingErr = nil
	}
	// Hard-delete tunnel semantics keep persisted history by tunnel_id. Only
	// unflushed/realtime observations are evicted so deleted tunnels stop appearing
	// as live series while durable traffic buckets remain queryable.
	return nil
}

func (s *TrafficStore) RenameTunnel(clientID, oldName, newName string) error {
	if oldName == newName {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	renamedPending, err := renamedPendingMinuteBuckets(s.pendingMinute, clientID, oldName, newName)
	if err != nil {
		return err
	}
	renamedRealtime, realtimeChanged, err := s.realtimeSecond.renamedTunnelBuckets(clientID, oldName, newName)
	if err != nil {
		return err
	}
	if err := s.renamePersistedTrafficBucketsLocked(clientID, oldName, newName); err != nil {
		return err
	}

	s.pendingMinute = renamedPending
	if realtimeChanged {
		s.realtimeSecond.byClient[clientID] = renamedRealtime
	}
	return nil
}

func renamedPendingMinuteBuckets(pending map[string]TrafficBucket, clientID, oldName, newName string) (map[string]TrafficBucket, error) {
	if len(pending) == 0 {
		return pending, nil
	}

	hasOldName := false
	for _, bucket := range pending {
		if bucket.ClientID == clientID && bucket.TunnelName == oldName {
			hasOldName = true
			break
		}
	}
	if !hasOldName {
		return pending, nil
	}

	renamed := make(map[string]TrafficBucket, len(pending))
	for _, bucket := range pending {
		if bucket.ClientID == clientID && bucket.TunnelName == oldName {
			bucket.TunnelName = newName
		}
		key := trafficBucketKey(bucket)
		if existing, ok := renamed[key]; ok {
			if err := addTrafficBucketValues(&existing, bucket); err != nil {
				return nil, err
			}
			renamed[key] = existing
			continue
		}
		renamed[key] = bucket
	}
	return renamed, nil
}

func (s *TrafficStore) renamePersistedTrafficBucketsLocked(clientID, oldName, newName string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)

	rows, err := tx.Query(`SELECT tunnel_id, owner_client_id, ingress_client_id, target_client_id, topology, transport, client_id, tunnel_name, tunnel_type, resolution, bucket_start, ingress_bytes, egress_bytes
FROM traffic_buckets
WHERE client_id = ? AND tunnel_name = ?
ORDER BY tunnel_type, resolution, bucket_start`, clientID, oldName)
	if err != nil {
		return err
	}
	buckets, err := scanTrafficBucketRows(rows)
	if err != nil {
		return err
	}

	for _, bucket := range buckets {
		if _, err := tx.Exec(`UPDATE traffic_buckets SET tunnel_name = ? WHERE tunnel_id = ? AND transport = ? AND resolution = ? AND bucket_start = ?`, newName, bucket.TunnelID, bucket.Transport, string(bucket.Resolution), bucket.BucketStart); err != nil {
			return err
		}
	}
	return commitTx(tx, &committed)
}

func (s *TrafficStore) EvictClient(clientID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, bucket := range s.pendingMinute {
		if bucket.ClientID == clientID {
			delete(s.pendingMinute, key)
		}
	}
	s.realtimeSecond.EvictClient(clientID)
	if len(s.pendingMinute) == 0 {
		s.pendingErr = nil
	}
	_, err := s.db.Exec(`DELETE FROM traffic_buckets WHERE client_id = ?`, clientID)
	return err
}

func hydrateTrafficBucketTx(tx *sql.Tx, bucket *TrafficBucket) error {
	if bucket == nil {
		return nil
	}
	if bucket.Transport == "" {
		bucket.Transport = TunnelActualTransportServerRelay
	}
	if bucket.TunnelID == "" && bucket.ClientID != "" && bucket.TunnelName != "" {
		var actualTransport string
		err := tx.QueryRow(`SELECT id, owner_client_id, ingress_client_id, target_client_id, topology, actual_transport FROM tunnels WHERE client_id = ? AND name = ?`, bucket.ClientID, bucket.TunnelName).Scan(
			&bucket.TunnelID,
			&bucket.OwnerClientID,
			&bucket.IngressClientID,
			&bucket.TargetClientID,
			&bucket.Topology,
			&actualTransport,
		)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if actualTransport != "" && actualTransport != TunnelActualTransportUnknown {
			bucket.Transport = actualTransport
		}
	}
	applyTrafficBucketStorageDefaults(bucket)
	return nil
}

func applyTrafficBucketStorageDefaults(bucket *TrafficBucket) {
	if bucket.OwnerClientID == "" {
		bucket.OwnerClientID = bucket.ClientID
	}
	if bucket.TargetClientID == "" {
		bucket.TargetClientID = bucket.ClientID
	}
	if bucket.Topology == "" {
		bucket.Topology = TunnelTopologyServerExpose
	}
	if bucket.Transport == "" || bucket.Transport == TunnelActualTransportUnknown {
		bucket.Transport = TunnelActualTransportServerRelay
	}
	if bucket.TunnelID == "" {
		bucket.TunnelID = fallbackTrafficTunnelID(bucket.ClientID, bucket.TunnelName, bucket.TunnelType)
	}
}

func fallbackTrafficTunnelID(clientID, tunnelName, tunnelType string) string {
	return strings.Join([]string{"legacy", clientID, tunnelName, tunnelType}, "\x00")
}
