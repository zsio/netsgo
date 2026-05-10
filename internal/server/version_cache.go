package server

import (
	"sync"
	"time"

	"netsgo/pkg/updater"
)

type releaseIndexCache struct {
	mu             sync.Mutex
	cond           *sync.Cond
	index          *updater.ReleaseIndex
	fetchedAt      time.Time
	refreshing     bool
	lastForceStart time.Time
	lastRefresh    releaseIndexSnapshot
	ttl            time.Duration
	forceCooldown  time.Duration
	fetch          func() (*updater.ReleaseIndex, error)
}

func newReleaseIndexCache(fetch func() (*updater.ReleaseIndex, error)) *releaseIndexCache {
	c := &releaseIndexCache{
		ttl:           12 * time.Hour,
		forceCooldown: 10 * time.Second,
		fetch:         fetch,
	}
	c.cond = sync.NewCond(&c.mu)
	return c
}

type releaseIndexSnapshot struct {
	index         *updater.ReleaseIndex
	cacheSource   string
	refreshFailed bool
}

func (c *releaseIndexCache) Get(force bool, now time.Time) releaseIndexSnapshot {
	c.mu.Lock()
	if c.index != nil && !force && now.Sub(c.fetchedAt) < c.ttl {
		idx := c.index
		c.mu.Unlock()
		return releaseIndexSnapshot{index: idx, cacheSource: "cache"}
	}
	if c.refreshing {
		for c.refreshing {
			c.cond.Wait()
		}
		if c.lastRefresh.cacheSource != "" {
			snap := c.lastRefresh
			c.mu.Unlock()
			return snap
		}
		c.mu.Unlock()
		return releaseIndexSnapshot{cacheSource: "none", refreshFailed: true}
	}
	if force && !c.lastForceStart.IsZero() && now.Sub(c.lastForceStart) < c.forceCooldown {
		if c.lastRefresh.refreshFailed || c.index == nil {
			snap := c.lastRefresh
			c.mu.Unlock()
			return snap
		}
		idx := c.index
		c.mu.Unlock()
		return releaseIndexSnapshot{index: idx, cacheSource: "cache"}
	}
	c.refreshing = true
	if force {
		c.lastForceStart = now
	}
	c.mu.Unlock()

	idx, err := c.fetch()

	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshing = false
	defer c.cond.Broadcast()
	if err == nil {
		c.index = idx
		c.fetchedAt = now
		snap := releaseIndexSnapshot{index: idx, cacheSource: "fresh"}
		c.lastRefresh = snap
		return snap
	}
	if c.index != nil {
		snap := releaseIndexSnapshot{index: c.index, cacheSource: "stale_cache", refreshFailed: true}
		c.lastRefresh = snap
		return snap
	}
	snap := releaseIndexSnapshot{cacheSource: "none", refreshFailed: true}
	c.lastRefresh = snap
	return snap
}
