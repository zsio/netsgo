package server

import (
	"fmt"
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

func (s *Server) nextClientGeneration() uint64 {
	return s.sessions.nextClientGeneration()
}

func (s *Server) startPendingDataTimer(client *ClientConn) {
	timeout := s.sessions.pendingDataTimeout
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

func (s *Server) waitForCurrentDataReady(client *ClientConn, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if !s.isCurrentGeneration(client.ID, client.generation) {
			return fmt.Errorf("logical session has been invalidated")
		}
		if client.getState() == clientStateClosing {
			return fmt.Errorf("logical session is closing")
		}

		client.dataMu.RLock()
		session := client.dataSession
		dataReady := session != nil && !session.IsClosed()
		client.dataMu.RUnlock()
		if dataReady {
			return nil
		}

		if timeout <= 0 || time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for data channel to become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
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
	s.clientTunnelMutationMu.Lock()
	defer s.clientTunnelMutationMu.Unlock()
	client.lifecycleMu.Lock()
	defer client.lifecycleMu.Unlock()

	value, ok := s.clients.Load(client.ID)
	if !ok || value != client {
		return false
	}
	client.stateMu.Lock()
	if client.state != clientStatePendingData {
		client.stateMu.Unlock()
		return false
	}
	if client.pendingTimer != nil {
		client.pendingTimer.Stop()
		client.pendingTimer = nil
	}
	client.state = clientStateLive
	client.stateMu.Unlock()

	activityID := s.appendClientLifecycle(client, "online", clientDisconnectCause{ReasonCode: "normal_closure", Expected: true})
	s.publishActivityID(activityID)
	return true
}

func (s *Server) invalidateLogicalSessionIfCurrent(clientID string, generation uint64, reason string) bool {
	s.clientTunnelMutationMu.Lock()
	defer s.clientTunnelMutationMu.Unlock()
	return s.invalidateLogicalSessionIfCurrentLocked(clientID, generation, normalizeClientDisconnectCause(reason))
}
func (s *Server) invalidateLogicalSessionIfCurrentWithCause(clientID string, generation uint64, cause clientDisconnectCause) bool {
	s.clientTunnelMutationMu.Lock()
	defer s.clientTunnelMutationMu.Unlock()
	return s.invalidateLogicalSessionIfCurrentLocked(clientID, generation, cause)
}

func (s *Server) invalidateLogicalSessionIfCurrentLocked(clientID string, generation uint64, cause clientDisconnectCause) bool {
	value, ok := s.clients.Load(clientID)
	if !ok {
		return false
	}
	client := value.(*ClientConn)
	if client.generation != generation {
		return false
	}
	client.lifecycleMu.Lock()
	defer client.lifecycleMu.Unlock()
	value, ok = s.clients.Load(clientID)
	if !ok || value != client || client.generation != generation {
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

	s.cancelTunnelProvisionAckWaiters(clientID, generation)
	if s.p2p != nil {
		s.sendP2PLifecycleResults(s.p2p.closeClient(clientID, generation, cause.ReasonCode))
	}

	controlConn := client.detachControlConn()
	if controlConn != nil {
		_ = controlConn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseGoingAway, cause.ReasonCode),
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

	s.CloseExposedProxyRuntime(client)
	s.releaseUnifiedRuntimeForClient(clientID)

	if wasLive {
		activityID := s.appendClientLifecycle(client, "offline", cause)
		s.publishActivityID(activityID)
		info := client.GetInfo()
		log.Printf("🔌 Client disconnected: %s [ID: %s, reason=%s]", info.Hostname, client.ID, cause.ReasonCode)
		s.events.PublishJSON("client_offline", map[string]any{
			"client_id": client.ID,
		})
	}
	s.clients.CompareAndDelete(clientID, client)

	return true
}
