package mux

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

const (
	DataStreamHeaderMagic           = "NGSH"
	DataStreamHeaderVersion   byte  = 1
	MaxDataStreamHeaderLength       = 16 * 1024
	MaxDataStreamHeaderStringLength = 1024
	MaxDataStreamOpenTokenLength    = 4096

	DataStreamKindTunnelStream = "tunnel_stream"

	DataStreamRoleServer  = "server"
	DataStreamRoleIngress = "ingress"
	DataStreamRoleTarget  = "target"

	DataStreamDirectionIngressToTarget = "ingress_to_target"

	DataStreamTransportServerRelay = "server_relay"
	DataStreamTransportPeerDirect  = "peer_direct"
	DataStreamTransportTURNRelay   = "turn_relay"
)

// DataStreamHeader is the versioned header written at the start of every
// logical yamux data stream. It replaces the legacy name-length stream header
// with a stable tunnel/revision/transport identity.
type DataStreamHeader struct {
	Kind             string `json:"kind"`
	TunnelID         string `json:"tunnel_id"`
	Revision         int64  `json:"revision"`
	StreamID         string `json:"stream_id"`
	OpenClientID     string `json:"open_client_id"`
	SourceRole       string `json:"source_role,omitempty"`
	TargetRole       string `json:"target_role,omitempty"`
	Direction        string `json:"direction"`
	Transport        string `json:"transport"`
	OpenToken        string `json:"open_token,omitempty"`
	ServerAuthorized bool   `json:"server_authorized,omitempty"`
}

// WriteDataStreamHeader writes:
// magic(4) + version(1) + header_len(4 big endian) + JSON header.
func WriteDataStreamHeader(w io.Writer, header DataStreamHeader) error {
	if err := ValidateDataStreamHeader(header); err != nil {
		return err
	}

	payload, err := json.Marshal(header)
	if err != nil {
		return fmt.Errorf("marshal data stream header: %w", err)
	}
	if len(payload) > MaxDataStreamHeaderLength {
		return fmt.Errorf("data stream header too large: %d > %d", len(payload), MaxDataStreamHeaderLength)
	}

	var prefix [9]byte
	copy(prefix[:4], DataStreamHeaderMagic)
	prefix[4] = DataStreamHeaderVersion
	binary.BigEndian.PutUint32(prefix[5:], uint32(len(payload)))

	if _, err := w.Write(prefix[:]); err != nil {
		return fmt.Errorf("write data stream header prefix: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("write data stream header payload: %w", err)
	}
	return nil
}

// ReadDataStreamHeader reads and validates a DataStreamHeader. Callers that
// operate on network streams should set a short read deadline around this call.
func ReadDataStreamHeader(r io.Reader) (DataStreamHeader, error) {
	var prefix [9]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return DataStreamHeader{}, fmt.Errorf("read data stream header prefix: %w", err)
	}
	if string(prefix[:4]) != DataStreamHeaderMagic {
		return DataStreamHeader{}, fmt.Errorf("invalid data stream header magic")
	}
	if prefix[4] != DataStreamHeaderVersion {
		return DataStreamHeader{}, fmt.Errorf("unsupported data stream header version: %d", prefix[4])
	}

	headerLen := binary.BigEndian.Uint32(prefix[5:])
	if headerLen == 0 || headerLen > MaxDataStreamHeaderLength {
		return DataStreamHeader{}, fmt.Errorf("invalid data stream header length: %d", headerLen)
	}

	payload := make([]byte, headerLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return DataStreamHeader{}, fmt.Errorf("read data stream header payload: %w", err)
	}
	if !utf8.Valid(payload) {
		return DataStreamHeader{}, fmt.Errorf("data stream header is not valid UTF-8")
	}

	var header DataStreamHeader
	if err := json.Unmarshal(payload, &header); err != nil {
		return DataStreamHeader{}, fmt.Errorf("decode data stream header JSON: %w", err)
	}
	if err := ValidateDataStreamHeader(header); err != nil {
		return DataStreamHeader{}, err
	}
	return header, nil
}

// ValidateDataStreamHeader validates enum and required-field invariants that
// are independent of server-side authorization state.
func ValidateDataStreamHeader(header DataStreamHeader) error {
	if header.Kind != DataStreamKindTunnelStream {
		return fmt.Errorf("invalid data stream kind: %q", header.Kind)
	}
	if header.TunnelID == "" {
		return fmt.Errorf("data stream header missing tunnel_id")
	}
	if header.Revision <= 0 {
		return fmt.Errorf("data stream header revision must be positive")
	}
	if header.StreamID == "" {
		return fmt.Errorf("data stream header missing stream_id")
	}
	if header.OpenClientID == "" {
		return fmt.Errorf("data stream header missing open_client_id")
	}
	if header.Direction != DataStreamDirectionIngressToTarget {
		return fmt.Errorf("invalid data stream direction: %q", header.Direction)
	}
	switch header.Transport {
	case DataStreamTransportServerRelay, DataStreamTransportPeerDirect, DataStreamTransportTURNRelay:
	default:
		return fmt.Errorf("invalid data stream transport: %q", header.Transport)
	}
	if header.OpenToken == "" && !header.ServerAuthorized {
		return fmt.Errorf("data stream header missing open_token")
	}
	if len(header.OpenToken) > MaxDataStreamOpenTokenLength {
		return fmt.Errorf("data stream open_token too large: %d > %d", len(header.OpenToken), MaxDataStreamOpenTokenLength)
	}

	for name, value := range map[string]string{
		"kind":           header.Kind,
		"tunnel_id":      header.TunnelID,
		"stream_id":      header.StreamID,
		"open_client_id": header.OpenClientID,
		"source_role":    header.SourceRole,
		"target_role":    header.TargetRole,
		"direction":      header.Direction,
		"transport":      header.Transport,
	} {
		if len(value) > MaxDataStreamHeaderStringLength {
			return fmt.Errorf("data stream header %s too large: %d > %d", name, len(value), MaxDataStreamHeaderStringLength)
		}
	}

	if header.SourceRole != "" && !isKnownDataStreamRole(header.SourceRole) {
		return fmt.Errorf("invalid data stream source_role: %q", header.SourceRole)
	}
	if header.TargetRole != "" && !isKnownDataStreamRole(header.TargetRole) {
		return fmt.Errorf("invalid data stream target_role: %q", header.TargetRole)
	}
	return nil
}

func isKnownDataStreamRole(role string) bool {
	switch role {
	case DataStreamRoleServer, DataStreamRoleIngress, DataStreamRoleTarget:
		return true
	default:
		return false
	}
}
