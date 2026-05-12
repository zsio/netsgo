package server

import (
	"sync"
	"time"

	"netsgo/internal/svcmgr"
	"netsgo/pkg/protocol"
)

const serverUpdateCapabilityTTL = 10 * time.Minute

type installMethodDetector func(svcmgr.Role) string

type updateCapabilityCache struct {
	mu       sync.RWMutex
	value    *protocol.UpdateCapability
	cachedAt time.Time
	detect   installMethodDetector
}

func newUpdateCapabilityCache(detect installMethodDetector) *updateCapabilityCache {
	if detect == nil {
		detect = func(svcmgr.Role) string { return "" }
	}
	return &updateCapabilityCache{detect: detect}
}

func (c *updateCapabilityCache) Get(now time.Time) *protocol.UpdateCapability {
	if now.IsZero() {
		now = time.Now()
	}

	c.mu.RLock()
	if cached := c.freshLocked(now); cached != nil {
		c.mu.RUnlock()
		return cached
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if cached := c.freshLocked(now); cached != nil {
		return cached
	}
	return c.refreshLocked(now)
}

func (c *updateCapabilityCache) Refresh(now time.Time) *protocol.UpdateCapability {
	if now.IsZero() {
		now = time.Now()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refreshLocked(now)
}

func (c *updateCapabilityCache) refreshLocked(now time.Time) *protocol.UpdateCapability {
	capability := &protocol.UpdateCapability{InstallMethod: normalizeInstallMethod(c.detect(svcmgr.RoleServer))}
	c.value = capability
	c.cachedAt = now

	copy := *capability
	return &copy
}

func (c *updateCapabilityCache) freshLocked(now time.Time) *protocol.UpdateCapability {
	if c.value == nil || now.Sub(c.cachedAt) >= serverUpdateCapabilityTTL {
		return nil
	}
	copy := *c.value
	return &copy
}
