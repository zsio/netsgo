package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// SSEEvent 代表一个 SSE 事件
type SSEEvent struct {
	Type string // "ready" | "snapshot" | "stats_update" | "agent_online" | "agent_offline" | "tunnel_changed"
	Data string // JSON 字符串
}

// EventBus 管理 SSE 订阅者的注册和广播
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[chan SSEEvent]struct{}
}

// NewEventBus 创建一个新的事件总线
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[chan SSEEvent]struct{}),
	}
}

// Subscribe 注册一个新的订阅者，返回事件通道
func (eb *EventBus) Subscribe() chan SSEEvent {
	ch := make(chan SSEEvent, 64) // 缓冲 64 条事件，防止慢消费者阻塞
	eb.mu.Lock()
	eb.subscribers[ch] = struct{}{}
	eb.mu.Unlock()
	return ch
}

// Unsubscribe 移除订阅者并关闭通道
// 如果通道已被 Close() 关闭并移除，则为 no-op
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

// Close 关闭事件总线，断开所有订阅者 (P15)
func (eb *EventBus) Close() {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	for ch := range eb.subscribers {
		close(ch)
		delete(eb.subscribers, ch)
	}
}

// Publish 向所有订阅者广播事件（非阻塞，满则丢弃）
func (eb *EventBus) Publish(event SSEEvent) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for ch := range eb.subscribers {
		select {
		case ch <- event:
		default:
			// 通道已满，丢弃事件防止阻塞
			log.Printf("⚠️ SSE 订阅者通道已满，丢弃事件: %s", event.Type)
		}
	}
}

// PublishJSON 序列化 data 为 JSON 并广播
func (eb *EventBus) PublishJSON(eventType string, data any) {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		log.Printf("⚠️ SSE 事件序列化失败: %v", err)
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

// handleSSE 处理 SSE 连接 — GET /api/events
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := s.events.Subscribe()
	defer s.events.Unsubscribe(ch)

	log.Printf("📡 SSE 客户端已连接: %s", r.RemoteAddr)

	if err := writeSSEEvent(w, flusher, "ready", map[string]any{}); err != nil {
		log.Printf("⚠️ SSE 初始握手写入失败: %v", err)
		return
	}
	if err := writeSSEEvent(w, flusher, "snapshot", s.collectSnapshot()); err != nil {
		log.Printf("⚠️ SSE 初始快照写入失败: %v", err)
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
				log.Printf("⚠️ SSE 事件写入失败: %v", err)
				return
			}
			flusher.Flush()
		case <-snapshotTicker.C:
			if err := writeSSEEvent(w, flusher, "snapshot", s.collectSnapshot()); err != nil {
				log.Printf("⚠️ SSE 快照写入失败: %v", err)
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				log.Printf("⚠️ SSE 心跳写入失败: %v", err)
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			log.Printf("📡 SSE 客户端已断开: %s", r.RemoteAddr)
			return
		}
	}
}
