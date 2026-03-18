package protocol

import (
	"strings"
	"testing"
)

func TestEncodeDecodeDataHandshake(t *testing.T) {
	clientID := "client-123"
	dataToken := "token-abc"

	payload := EncodeDataHandshake(clientID, dataToken)
	gotClientID, gotToken, err := DecodeDataHandshake(payload)
	if err != nil {
		t.Fatalf("DecodeDataHandshake 失败: %v", err)
	}
	if gotClientID != clientID {
		t.Fatalf("clientID 不匹配: 期望 %q, 得到 %q", clientID, gotClientID)
	}
	if gotToken != dataToken {
		t.Fatalf("dataToken 不匹配: 期望 %q, 得到 %q", dataToken, gotToken)
	}
}

func TestDecodeDataHandshake_RejectsZeroClientID(t *testing.T) {
	payload := EncodeDataHandshake("", "token")
	if _, _, err := DecodeDataHandshake(payload); err == nil {
		t.Fatal("空 clientID 应被拒绝")
	}
}

func TestDecodeDataHandshake_RejectsTooLongClientID(t *testing.T) {
	payload := EncodeDataHandshake(strings.Repeat("a", DataHandshakeMaxClientIDLen+1), "token")
	if _, _, err := DecodeDataHandshake(payload); err == nil {
		t.Fatal("超长 clientID 应被拒绝")
	}
}

func TestDecodeDataHandshake_RejectsZeroToken(t *testing.T) {
	payload := EncodeDataHandshake("client", "")
	if _, _, err := DecodeDataHandshake(payload); err == nil {
		t.Fatal("空 dataToken 应被拒绝")
	}
}

func TestDecodeDataHandshake_RejectsTooLongToken(t *testing.T) {
	payload := EncodeDataHandshake("client", strings.Repeat("t", DataHandshakeMaxTokenLen+1))
	if _, _, err := DecodeDataHandshake(payload); err == nil {
		t.Fatal("超长 dataToken 应被拒绝")
	}
}

func TestDecodeDataHandshake_RejectsOversizedPayload(t *testing.T) {
	payload := make([]byte, MaxDataHandshakePayloadLen+1)
	if _, _, err := DecodeDataHandshake(payload); err == nil {
		t.Fatal("超长 payload 应被拒绝")
	}
}

func TestDecodeDataHandshake_RejectsTrailingBytes(t *testing.T) {
	payload := append(EncodeDataHandshake("client", "token"), 0x01)
	if _, _, err := DecodeDataHandshake(payload); err == nil {
		t.Fatal("带多余尾字节的 payload 应被拒绝")
	}
}
