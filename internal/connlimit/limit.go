// Package connlimit bounds forwarding work without queueing excess connections.
package connlimit

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
)

const Default = 10000

var ErrReached = errors.New("forwarding connection limit reached")

// Limiter is process-owned, so reconnects and tunnel revisions share its budget.
// Its zero value is ready for use. A non-positive limit selects Default.
type Limiter struct {
	active   atomic.Int64
	rejected atomic.Uint64
}

func (l *Limiter) Acquire(limit int) (func(), bool) {
	if limit <= 0 {
		limit = Default
	}
	for {
		n := l.active.Load()
		if n >= int64(limit) {
			l.rejected.Add(1)
			return nil, false
		}
		if l.active.CompareAndSwap(n, n+1) {
			var once sync.Once
			return func() { once.Do(func() { l.active.Add(-1) }) }, true
		}
	}
}

func (l *Limiter) Active() int64 { return l.active.Load() }

// Report only powers of two so a connection flood cannot flood the logs too.
func (l *Limiter) ShouldReportRejection() bool {
	n := l.rejected.Load()
	return n != 0 && n&(n-1) == 0
}

type countedConn struct {
	net.Conn
	release func()
	owner   *Limiter
}

func (c *countedConn) Close() error {
	err := c.Conn.Close()
	c.release()
	return err
}

// Admit reserves before a caller starts a handler or opens a target stream.
func (l *Limiter) Admit(conn net.Conn, limit int) (net.Conn, bool) {
	if counted, ok := conn.(*countedConn); ok && counted.owner == l {
		return conn, true
	}
	release, ok := l.Acquire(limit)
	if !ok {
		_ = conn.Close()
		return nil, false
	}
	return &countedConn{Conn: conn, release: release, owner: l}, true
}

func Wrap(conn net.Conn, release func()) net.Conn {
	return &countedConn{Conn: conn, release: release}
}
