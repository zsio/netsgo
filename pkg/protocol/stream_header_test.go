package protocol

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func validTestDataStreamHeader() DataStreamHeader {
	return DataStreamHeader{
		Kind:             DataStreamHeaderKindTunnelStream,
		TunnelID:         "tun-1",
		Revision:         1,
		StreamID:         "stream-1",
		OpenClientID:     "client-1",
		SourceRole:       DataStreamRoleServer,
		TargetRole:       DataStreamRoleTarget,
		Direction:        DataStreamDirectionIngressToTarget,
		Transport:        ActualTransportServerRelay,
		ServerAuthorized: true,
	}
}

func TestDataStreamHeaderRoundTrip(t *testing.T) {
	want := validTestDataStreamHeader()
	encoded, err := WriteDataStreamHeaderToBytes(want)
	if err != nil {
		t.Fatalf("encode data stream header: %v", err)
	}

	got, err := DecodeDataStreamHeader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("decode data stream header: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch:\nwant %+v\n got %+v", want, got)
	}
}

func TestDecodeDataStreamHeaderRejectsMalformedFrames(t *testing.T) {
	valid := validTestDataStreamHeader()
	validBytes, err := WriteDataStreamHeaderToBytes(valid)
	if err != nil {
		t.Fatalf("encode valid header: %v", err)
	}

	tests := []struct {
		name string
		data []byte
	}{
		{name: "bad magic", data: append([]byte("BAD!"), validBytes[4:]...)},
		{name: "bad version", data: func() []byte { b := append([]byte(nil), validBytes...); b[4] = 99; return b }()},
		{name: "zero length", data: func() []byte {
			b := append([]byte(nil), validBytes...)
			binary.BigEndian.PutUint32(b[5:9], 0)
			return b[:9]
		}()},
		{name: "oversized length", data: func() []byte {
			b := append([]byte(nil), validBytes[:9]...)
			binary.BigEndian.PutUint32(b[5:9], DataStreamHeaderMaxLen+1)
			return b
		}()},
		{name: "short payload", data: validBytes[:len(validBytes)-1]},
		{name: "not utf8", data: dataStreamFrameForTest([]byte{0xff})},
		{name: "not json", data: dataStreamFrameForTest([]byte("not-json"))},
		{name: "unknown field", data: dataStreamFrameForTest([]byte(`{"kind":"tunnel_stream","tunnel_id":"tun-1","revision":1,"stream_id":"stream-1","open_client_id":"client-1","direction":"ingress_to_target","transport":"server_relay","server_authorized":true,"extra":true}`))},
		{name: "missing open token when not server authorized", data: dataStreamFrameForTest([]byte(`{"kind":"tunnel_stream","tunnel_id":"tun-1","revision":1,"stream_id":"stream-1","open_client_id":"client-1","direction":"ingress_to_target","transport":"server_relay"}`))},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeDataStreamHeader(bytes.NewReader(tc.data)); err == nil {
				t.Fatal("expected malformed data stream header to be rejected")
			}
		})
	}
}

func TestValidateDataStreamHeaderRejectsInvalidFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*DataStreamHeader)
	}{
		{name: "missing tunnel", mutate: func(h *DataStreamHeader) { h.TunnelID = "" }},
		{name: "non positive revision", mutate: func(h *DataStreamHeader) { h.Revision = 0 }},
		{name: "unknown transport", mutate: func(h *DataStreamHeader) { h.Transport = "server_relayish" }},
		{name: "unknown direction", mutate: func(h *DataStreamHeader) { h.Direction = "backwards" }},
		{name: "unknown role", mutate: func(h *DataStreamHeader) { h.SourceRole = "attacker" }},
		{name: "long token", mutate: func(h *DataStreamHeader) { h.OpenToken = strings.Repeat("x", DataStreamHeaderMaxTokenLen+1) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			header := validTestDataStreamHeader()
			tc.mutate(&header)
			if err := ValidateDataStreamHeader(header); err == nil {
				t.Fatal("expected invalid data stream header to be rejected")
			}
		})
	}
}

func dataStreamFrameForTest(payload []byte) []byte {
	frame := make([]byte, 9+len(payload))
	copy(frame[:4], DataStreamHeaderMagic)
	frame[4] = DataStreamHeaderVersion
	binary.BigEndian.PutUint32(frame[5:9], uint32(len(payload)))
	copy(frame[9:], payload)
	return frame
}
