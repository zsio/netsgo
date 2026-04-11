package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// SSEEvent represents a single SSE event.
type SSEEvent struct {
	Type string // "ready" | "snapshot" | "stats_update" | "client_online" | "client_offline" | "tunnel_changed"
	Data string // JSON string
}

// EventBus manages SSE subscriber registration and broadcasting.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[chan SSEEvent]struct{}
}

// NewEventBus creates a new event bus.
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[chan SSEEvent]struct{}),
	}
}

// Subscribe registers a new subscriber and returns its event channel.
func (eb *EventBus) Subscribe() chan SSEEvent {
	ch := make(chan SSEEvent, 64) // Buffer 64 events to avoid blocking on slow consumers.
	eb.mu.Lock()
	eb.subscribers[ch] = struct{}{}
	eb.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber and closes its channel.
// If the channel was already closed and removed by Close(), this is a no-op.
func (eb *EventBus) Unsubscribe(ch chan SSEEvent) {
	eb.mu.Lock()
	_, exists := eb.subscribers[ch]
	if exists {
		delete(eb.subscribers, ch)
	}
	eb.mu.Unlock()
	if exists {
		close(ch)
	}
}

// Close shuts down the event bus and disconnects all subscribers. (P15)
func (eb *EventBus) Close() {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	for ch := range eb.subscribers {
		close(ch)
		delete(eb.subscribers, ch)
	}
}

// Publish broadcasts an event to all subscribers.
// It is non-blocking and drops the event if a subscriber channel is full.
func (eb *EventBus) Publish(event SSEEvent) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for ch := range eb.subscribers {
		select {
		case ch <- event:
		default:
			// Drop the event to avoid blocking if the channel is full.
			log.Printf("⚠️ SSE subscriber channel is full, dropping event: %s", event.Type)
		}
	}
}

// PublishJSON marshals data to JSON and broadcasts it.
func (eb *EventBus) PublishJSON(eventType string, data any) {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		log.Printf("⚠️ Failed to marshal SSE event: %v", err)
		return
	}
	eb.Publish(SSEEvent{Type: eventType, Data: string(jsonBytes)})
}

func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, data any) error {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, jsonBytes); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// handleSSE handles SSE connections — GET /api/events.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}
	release := s.beginLongLivedHandler()
	defer release()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	log.Printf("📡 SSE client connected: %s", r.RemoteAddr)

	if err := writeSSEEvent(w, flusher, "ready", map[string]any{}); err != nil {
		log.Printf("⚠️ Failed to write initial SSE handshake: %v", err)
		return
	}
	if err := writeSSEEvent(w, flusher, "snapshot", s.collectSnapshot()); err != nil {
		log.Printf("⚠️ Failed to write initial SSE snapshot: %v", err)
		return
	}

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	snapshotTicker := time.NewTicker(10 * time.Second)
	defer snapshotTicker.Stop()

	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, event.Data); err != nil {
				log.Printf("⚠️ Failed to write SSE event: %v", err)
				return
			}
			flusher.Flush()
		case <-snapshotTicker.C:
			if err := writeSSEEvent(w, flusher, "snapshot", s.collectSnapshot()); err != nil {
				log.Printf("⚠️ Failed to write SSE snapshot: %v", err)
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				log.Printf("⚠️ Failed to write SSE heartbeat: %v", err)
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			log.Printf("📡 SSE client disconnected: %s", r.RemoteAddr)
			return
		}
	}
}
