package server

type realtimeSecondIndex struct {
	byClient map[string]map[trafficSeriesKey]map[int64]TrafficBucket
}

func newRealtimeSecondIndex() *realtimeSecondIndex {
	return &realtimeSecondIndex{byClient: make(map[string]map[trafficSeriesKey]map[int64]TrafficBucket)}
}

func (idx *realtimeSecondIndex) Add(bucket TrafficBucket) error {
	if idx == nil || bucket.ClientID == "" || bucket.TunnelName == "" || bucket.TunnelType == "" {
		return nil
	}
	if bucket.IngressBytes == 0 && bucket.EgressBytes == 0 {
		return nil
	}
	if idx.byClient == nil {
		idx.byClient = make(map[string]map[trafficSeriesKey]map[int64]TrafficBucket)
	}

	seriesByClient := idx.byClient[bucket.ClientID]
	if seriesByClient == nil {
		seriesByClient = make(map[trafficSeriesKey]map[int64]TrafficBucket)
		idx.byClient[bucket.ClientID] = seriesByClient
	}

	seriesKey := trafficSeriesKey{TunnelName: bucket.TunnelName, TunnelType: bucket.TunnelType}
	bucketsBySecond := seriesByClient[seriesKey]
	if bucketsBySecond == nil {
		bucketsBySecond = make(map[int64]TrafficBucket)
		seriesByClient[seriesKey] = bucketsBySecond
	}

	if existing, ok := bucketsBySecond[bucket.BucketStart]; ok {
		if err := addTrafficBucketValues(&existing, bucket); err != nil {
			return err
		}
		bucketsBySecond[bucket.BucketStart] = existing
		return nil
	}

	bucketsBySecond[bucket.BucketStart] = bucket
	return nil
}

func (idx *realtimeSecondIndex) Query(clientID, tunnelName string, fromUnix, toUnix int64) []TrafficBucket {
	if idx == nil || idx.byClient == nil {
		return nil
	}

	seriesByClient := idx.byClient[clientID]
	if len(seriesByClient) == 0 {
		return nil
	}

	buckets := []TrafficBucket{}
	for key, bucketsBySecond := range seriesByClient {
		if tunnelName != "" && key.TunnelName != tunnelName {
			continue
		}
		for second, bucket := range bucketsBySecond {
			if second >= fromUnix && second <= toUnix {
				buckets = append(buckets, bucket)
			}
		}
	}
	return buckets
}

func (idx *realtimeSecondIndex) PruneBefore(cutoff int64) {
	if idx == nil || idx.byClient == nil {
		return
	}

	for clientID, seriesByClient := range idx.byClient {
		for key, bucketsBySecond := range seriesByClient {
			for second := range bucketsBySecond {
				if second < cutoff {
					delete(bucketsBySecond, second)
				}
			}
			if len(bucketsBySecond) == 0 {
				delete(seriesByClient, key)
			}
		}
		if len(seriesByClient) == 0 {
			delete(idx.byClient, clientID)
		}
	}
}

func (idx *realtimeSecondIndex) EvictClient(clientID string) {
	if idx == nil || idx.byClient == nil {
		return
	}
	delete(idx.byClient, clientID)
}

func (idx *realtimeSecondIndex) EvictTunnel(clientID, tunnelName string) {
	if idx == nil || idx.byClient == nil {
		return
	}
	seriesByClient := idx.byClient[clientID]
	for key := range seriesByClient {
		if key.TunnelName == tunnelName {
			delete(seriesByClient, key)
		}
	}
	if len(seriesByClient) == 0 {
		delete(idx.byClient, clientID)
	}
}
