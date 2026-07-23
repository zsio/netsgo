package server

import (
	"errors"
	"testing"

	"github.com/gorilla/websocket"
)

func TestControlLoopReturnsStructuredDisconnectCause(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want clientDisconnectCause
	}{
		{name: "normal", err: &websocket.CloseError{Code: websocket.CloseNormalClosure, Text: "client shutdown"}, want: clientDisconnectCause{CloseCode: websocket.CloseNormalClosure, ReasonCode: "normal_closure", Expected: true}},
		{name: "going away", err: &websocket.CloseError{Code: websocket.CloseGoingAway, Text: "raw client text must not persist"}, want: clientDisconnectCause{CloseCode: websocket.CloseGoingAway, ReasonCode: "transport_error"}},
		{name: "transport", err: errors.New("sensitive transport details"), want: clientDisconnectCause{ReasonCode: "transport_error"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clientDisconnectCauseFromError(tt.err)
			if got != tt.want {
				t.Fatalf("cause = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestControlLoopNilConnectionReturnsTransportError(t *testing.T) {
	s := New(0)
	if got := s.controlLoop(&ClientConn{}); got != (clientDisconnectCause{ReasonCode: "transport_error"}) {
		t.Fatalf("controlLoop(nil conn) = %+v", got)
	}
}
