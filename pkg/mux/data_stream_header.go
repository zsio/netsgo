package mux

import (
	"io"

	"netsgo/pkg/protocol"
)

const (
	DataStreamHeaderMagic           = protocol.DataStreamHeaderMagic
	DataStreamHeaderVersion         = protocol.DataStreamHeaderVersion
	MaxDataStreamHeaderLength       = protocol.DataStreamHeaderMaxLen
	MaxDataStreamHeaderStringLength = protocol.DataStreamHeaderMaxStringLen
	MaxDataStreamOpenTokenLength    = protocol.DataStreamHeaderMaxTokenLen

	DataStreamKindTunnelStream = protocol.DataStreamHeaderKindTunnelStream

	DataStreamRoleServer  = protocol.DataStreamRoleServer
	DataStreamRoleIngress = protocol.DataStreamRoleIngress
	DataStreamRoleTarget  = protocol.DataStreamRoleTarget

	DataStreamDirectionIngressToTarget = protocol.DataStreamDirectionIngressToTarget

	DataStreamTransportServerRelay = protocol.ActualTransportServerRelay
	DataStreamTransportPeerDirect  = protocol.ActualTransportPeerDirect
	DataStreamTransportTURNRelay   = protocol.ActualTransportTURNRelay
)

// DataStreamHeader is the versioned header written at the start of every
// logical yamux data stream. It replaces the legacy name-length stream header
// with a stable tunnel/revision/transport identity.
type DataStreamHeader = protocol.DataStreamHeader

// WriteDataStreamHeader writes:
// magic(4) + version(1) + header_len(4 big endian) + JSON header.
func WriteDataStreamHeader(w io.Writer, header DataStreamHeader) error {
	return protocol.EncodeDataStreamHeader(w, header)
}

// ReadDataStreamHeader reads and validates a DataStreamHeader. Callers that
// operate on network streams should set a short read deadline around this call.
func ReadDataStreamHeader(r io.Reader) (DataStreamHeader, error) {
	return protocol.DecodeDataStreamHeader(r)
}

// ValidateDataStreamHeader validates enum and required-field invariants that
// are independent of server-side authorization state.
func ValidateDataStreamHeader(header DataStreamHeader) error {
	return protocol.ValidateDataStreamHeader(header)
}
