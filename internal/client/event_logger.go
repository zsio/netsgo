package client

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"
)

const (
	LogFormatText = "text"
	LogFormatJSON = "json"
)

type EventLogger struct {
	format string
	out    io.Writer
	mu     sync.Mutex
}

type ClientEvent struct {
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Event   string         `json:"event"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}

func NewEventLogger(format string, out io.Writer) *EventLogger {
	if format != LogFormatJSON {
		format = LogFormatText
	}
	return &EventLogger{format: format, out: out}
}

func (l *EventLogger) Format() string {
	if l == nil || l.format == "" {
		return LogFormatText
	}
	return l.format
}

func (l *EventLogger) Event(level, event, message string, fields map[string]any) {
	if l == nil || l.Format() == LogFormatText {
		if message != "" {
			if err, ok := fields["error"]; ok && err != "" {
				message = fmt.Sprintf("%s: %v", message, err)
			}
			log.Print(message)
		}
		return
	}
	if l.out == nil {
		return
	}

	if level == "" {
		level = "info"
	}
	payload := ClientEvent{
		Time:    time.Now().UTC(),
		Level:   level,
		Event:   event,
		Message: message,
		Fields:  fields,
	}
	line, err := json.Marshal(payload)
	if err != nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintln(l.out, string(line))
}

func (l *EventLogger) Info(event, message string, fields map[string]any) {
	l.Event("info", event, message, fields)
}

func (l *EventLogger) Warn(event, message string, fields map[string]any) {
	l.Event("warn", event, message, fields)
}

func (l *EventLogger) Error(event, message string, fields map[string]any) {
	l.Event("error", event, message, fields)
}
