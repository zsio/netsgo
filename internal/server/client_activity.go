package server

import (
	"fmt"
	"log"
	"strings"
	"time"
)

type clientDisconnectCause struct {
	CloseCode  int
	ReasonCode string
	Expected   bool
}

func normalizeClientDisconnectCause(reason string) clientDisconnectCause {
	switch reason {
	case "server_shutdown":
		return clientDisconnectCause{ReasonCode: "server_shutdown", Expected: true}
	case "normal_closure":
		return clientDisconnectCause{ReasonCode: "normal_closure", Expected: true}
	case "pending_data_timeout":
		return clientDisconnectCause{ReasonCode: "timeout"}
	case "data_session_closed":
		return clientDisconnectCause{ReasonCode: "data_channel_closed"}
	case "auth_response_failed", "data_session_start_failed", "control_loop_exit":
		return clientDisconnectCause{ReasonCode: "transport_error"}
	case "replaced":
		return clientDisconnectCause{ReasonCode: "replaced", Expected: true}
	default:
		return clientDisconnectCause{ReasonCode: "unknown"}
	}
}

func (s *Server) clientLifecycleSpec(client *ClientConn, action string, cause clientDisconnectCause) ActivityEventSpec {
	info := client.GetInfo()
	managed := !strings.HasPrefix(client.ID, "unmanaged-")
	name := info.Hostname
	if name == "" {
		name = client.ID
	}
	payload := newActivityClientLifecyclePayload(action, cause.ReasonCode, client.generation, managed, ActivitySummaryArgs{ClientName: name})
	severity := ActivitySeverityInfo
	if action == "offline" && !cause.Expected {
		severity = ActivitySeverityWarning
	}
	return ActivityEventSpec{
		OccurredAt: time.Now().UTC(),
		Severity:   severity,
		Category:   ActivityCategoryClient,
		Action:     action,
		Source:     "server",
		Actor:      ActivityActor{Type: "client", ID: client.ID, Name: name},
		DedupeKey:  fmt.Sprintf("%s:%s:%d:%s", s.activityBootID, client.ID, client.generation, action),
		Payload:    payload,
		Clients: []ActivityClientSubject{{
			ClientID: client.ID, Relation: "subject", Hostname: info.Hostname,
		}},
	}
}

func (s *Server) appendClientLifecycle(client *ClientConn, action string, cause clientDisconnectCause) int64 {
	s.ensureSharedStoreReferences()
	if s.activityStore == nil {
		return 0
	}
	id, err := s.activityStore.Append(s.clientLifecycleSpec(client, action, cause))
	if err != nil {
		log.Printf("⚠️ Failed to persist client %s activity [%s generation=%d]: %v", action, client.ID, client.generation, err)
		return 0
	}
	return id
}
