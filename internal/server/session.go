package server

import (
	"log"
	"time"

	"github.com/gorilla/websocket"
)

type clientState string

const (
	clientStatePendingData clientState = "PendingData"
	clientStateLive        clientState = "Live"
	clientStateClosing     clientState = "Closing"
)

func (c *ClientConn) getState() clientState {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.state
}

func (c *ClientConn) isLive() bool {
	return c.getState() == clientStateLive
}

func (c *ClientConn) stopPendingTimer() {
	c.stateMu.Lock()
	timer := c.pendingTimer
	c.pendingTimer = nil
	c.stateMu.Unlock()
	if timer != nil {
		timer.Stop()
	}
}

func (s *Server) nextClientGeneration() uint64 {
	return s.nextGeneration.Add(1)
}

func (s *Server) startPendingDataTimer(client *ClientConn) {
	timeout := s.pendingDataTimeout
	if timeout <= 0 {
		return
	}

	timer := time.AfterFunc(timeout, func() {
		s.invalidateLogicalSessionIfCurrent(client.ID, client.generation, "pending_data_timeout")
	})

	client.stateMu.Lock()
	if client.state == clientStatePendingData && client.pendingTimer == nil {
		client.pendingTimer = timer
		client.stateMu.Unlock()
		return
	}
	client.stateMu.Unlock()
	timer.Stop()
}

func (s *Server) isCurrentGeneration(clientID string, generation uint64) bool {
	value, ok := s.clients.Load(clientID)
	if !ok {
		return false
	}
	client := value.(*ClientConn)
	return client.generation == generation
}

func (s *Server) isCurrentLive(clientID string, generation uint64) bool {
	value, ok := s.clients.Load(clientID)
	if !ok {
		return false
	}
	client := value.(*ClientConn)
	if client.generation != generation {
		return false
	}
	return client.isLive()
}

func (s *Server) loadLiveClient(clientID string) (*ClientConn, bool) {
	value, ok := s.clients.Load(clientID)
	if !ok {
		return nil, false
	}
	client := value.(*ClientConn)
	if !client.isLive() {
		return nil, false
	}
	return client, true
}

func (s *Server) promotePendingToLiveIfCurrent(client *ClientConn) bool {
	value, ok := s.clients.Load(client.ID)
	if !ok || value != client {
		return false
	}

	client.stateMu.Lock()
	defer client.stateMu.Unlock()

	if client.state != clientStatePendingData {
		return false
	}

	if client.pendingTimer != nil {
		client.pendingTimer.Stop()
		client.pendingTimer = nil
	}
	client.state = clientStateLive
	return true
}

func (s *Server) invalidateLogicalSessionIfCurrent(clientID string, generation uint64, reason string) bool {
	value, ok := s.clients.Load(clientID)
	if !ok {
		return false
	}
	client := value.(*ClientConn)
	if client.generation != generation {
		return false
	}

	client.stateMu.Lock()
	if client.state == clientStateClosing {
		client.stateMu.Unlock()
		return false
	}
	wasLive := client.state == clientStateLive
	if client.pendingTimer != nil {
		client.pendingTimer.Stop()
		client.pendingTimer = nil
	}
	client.state = clientStateClosing
	client.stateMu.Unlock()

	s.clients.CompareAndDelete(clientID, client)
	s.cancelTunnelReadyWaiters(clientID, generation)

	client.mu.Lock()
	controlConn := client.conn
	client.conn = nil
	client.mu.Unlock()
	if controlConn != nil {
		_ = controlConn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseGoingAway, reason),
			time.Now().Add(time.Second),
		)
		_ = controlConn.Close()
	}

	client.dataMu.Lock()
	dataSession := client.dataSession
	client.dataSession = nil
	client.dataMu.Unlock()
	if dataSession != nil && !dataSession.IsClosed() {
		_ = dataSession.Close()
	}

	s.PauseAllProxies(client)

	if wasLive {
		log.Printf("🔌 Client 已断开: %s [ID: %s, reason=%s]", client.Info.Hostname, client.ID, reason)
		s.events.PublishJSON("client_offline", map[string]any{
			"client_id": client.ID,
		})
	}

	return true
}
