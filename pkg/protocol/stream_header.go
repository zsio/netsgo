package protocol

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

const (
	DataStreamHeaderKindTunnelStream = "tunnel_stream"
	DataStreamHeaderMagic            = "NGDS"
	DataStreamHeaderVersion          = byte(1)
	DataStreamHeaderMaxLen           = 16 * 1024
	DataStreamHeaderMaxStringLen     = 1024
	DataStreamHeaderMaxTokenLen      = 4096

	DataStreamRoleServer  = "server"
	DataStreamRoleIngress = "ingress"
	DataStreamRoleTarget  = "target"

	DataStreamDirectionIngressToTarget = "ingress_to_target"
)

// DataStreamHeader is the versioned stream-routing header for tunnel data
// streams. It replaces name-based yamux stream routing.
type DataStreamHeader struct {
	Kind             string `json:"kind"`
	TunnelID         string `json:"tunnel_id"`
	Revision         int64  `json:"revision"`
	StreamID         string `json:"stream_id"`
	OpenClientID     string `json:"open_client_id"`
	SourceRole       string `json:"source_role"`
	TargetRole       string `json:"target_role"`
	Direction        string `json:"direction"`
	Transport        string `json:"transport"`
	OpenToken        string `json:"open_token,omitempty"`
	ServerAuthorized bool   `json:"server_authorized,omitempty"`
}

func NewDataStreamID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate data stream id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func EncodeDataStreamHeader(w io.Writer, header DataStreamHeader) error {
	if err := ValidateDataStreamHeader(header); err != nil {
		return err
	}
	payload, err := json.Marshal(header)
	if err != nil {
		return err
	}
	if len(payload) == 0 || len(payload) > DataStreamHeaderMaxLen {
		return fmt.Errorf("data stream header length %d exceeds limit", len(payload))
	}

	var prefix [9]byte
	copy(prefix[:4], DataStreamHeaderMagic)
	prefix[4] = DataStreamHeaderVersion
	binary.BigEndian.PutUint32(prefix[5:9], uint32(len(payload)))
	if _, err := w.Write(prefix[:]); err != nil {
		return fmt.Errorf("write data stream header prefix: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("write data stream header payload: %w", err)
	}
	return nil
}

func DecodeDataStreamHeader(r io.Reader) (DataStreamHeader, error) {
	var prefix [9]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return DataStreamHeader{}, fmt.Errorf("read data stream header prefix: %w", err)
	}
	if string(prefix[:4]) != DataStreamHeaderMagic {
		return DataStreamHeader{}, fmt.Errorf("invalid data stream header magic")
	}
	if prefix[4] != DataStreamHeaderVersion {
		return DataStreamHeader{}, fmt.Errorf("unsupported data stream header version %d", prefix[4])
	}
	length := binary.BigEndian.Uint32(prefix[5:9])
	if length == 0 || length > DataStreamHeaderMaxLen {
		return DataStreamHeader{}, fmt.Errorf("invalid data stream header length %d", length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return DataStreamHeader{}, fmt.Errorf("read data stream header payload: %w", err)
	}
	if !utf8.Valid(payload) {
		return DataStreamHeader{}, fmt.Errorf("data stream header is not utf-8")
	}

	var header DataStreamHeader
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&header); err != nil {
		return DataStreamHeader{}, fmt.Errorf("decode data stream header json: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return DataStreamHeader{}, fmt.Errorf("data stream header must be one json object")
	}
	if err := ValidateDataStreamHeader(header); err != nil {
		return DataStreamHeader{}, err
	}
	return header, nil
}

// WriteDataStreamHeaderToBytes serializes a data stream header to the on-wire
// format, useful in tests.
func WriteDataStreamHeaderToBytes(header DataStreamHeader) ([]byte, error) {
	var buf bytes.Buffer
	if err := EncodeDataStreamHeader(&buf, header); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ValidateDataStreamHeader verifies only the structural invariants of the
// header. ServerAuthorized is a caller-provided trust bit; data-plane callers
// must still authorize the stream against the current session and tunnel state.
func ValidateDataStreamHeader(header DataStreamHeader) error {
	if header.Kind != DataStreamHeaderKindTunnelStream {
		return fmt.Errorf("unsupported data stream header kind %q", header.Kind)
	}
	if header.TunnelID == "" {
		return fmt.Errorf("data stream header tunnel_id is required")
	}
	if header.Revision <= 0 {
		return fmt.Errorf("data stream header revision must be positive")
	}
	if header.StreamID == "" {
		return fmt.Errorf("data stream header stream_id is required")
	}
	if header.OpenClientID == "" {
		return fmt.Errorf("data stream header open_client_id is required")
	}
	if header.Direction == "" {
		return fmt.Errorf("data stream header direction is required")
	}
	if header.Transport == "" {
		return fmt.Errorf("data stream header transport is required")
	}
	if header.Transport != ActualTransportServerRelay && header.Transport != ActualTransportPeerDirect && header.Transport != ActualTransportTURNRelay {
		return fmt.Errorf("unsupported data stream transport %q", header.Transport)
	}
	if header.Direction != DataStreamDirectionIngressToTarget {
		return fmt.Errorf("unsupported data stream direction %q", header.Direction)
	}
	if !header.ServerAuthorized && header.OpenToken == "" {
		return fmt.Errorf("data stream header open_token is required")
	}
	strings := []struct {
		name  string
		value string
		limit int
	}{
		{"tunnel_id", header.TunnelID, DataStreamHeaderMaxStringLen},
		{"stream_id", header.StreamID, DataStreamHeaderMaxStringLen},
		{"open_client_id", header.OpenClientID, DataStreamHeaderMaxStringLen},
		{"source_role", header.SourceRole, DataStreamHeaderMaxStringLen},
		{"target_role", header.TargetRole, DataStreamHeaderMaxStringLen},
		{"direction", header.Direction, DataStreamHeaderMaxStringLen},
		{"transport", header.Transport, DataStreamHeaderMaxStringLen},
		{"open_token", header.OpenToken, DataStreamHeaderMaxTokenLen},
	}
	for _, field := range strings {
		if len(field.value) > field.limit {
			return fmt.Errorf("data stream header %s is too long", field.name)
		}
	}
	if header.SourceRole == "" {
		return fmt.Errorf("data stream header source_role is required")
	}
	if !isKnownDataStreamRole(header.SourceRole) {
		return fmt.Errorf("unsupported data stream source_role %q", header.SourceRole)
	}
	if header.TargetRole == "" {
		return fmt.Errorf("data stream header target_role is required")
	}
	if !isKnownDataStreamRole(header.TargetRole) {
		return fmt.Errorf("unsupported data stream target_role %q", header.TargetRole)
	}
	if header.Direction == DataStreamDirectionIngressToTarget {
		if header.SourceRole != DataStreamRoleServer && header.SourceRole != DataStreamRoleIngress {
			return fmt.Errorf("data stream header source_role %q is invalid for direction %q", header.SourceRole, header.Direction)
		}
		if header.TargetRole != DataStreamRoleTarget {
			return fmt.Errorf("data stream header target_role %q is invalid for direction %q", header.TargetRole, header.Direction)
		}
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
