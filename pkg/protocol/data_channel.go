package protocol

import (
	"encoding/binary"
	"fmt"
)

const (
	DataHandshakeOK       byte = 0x00
	DataHandshakeFail     byte = 0x01
	DataHandshakeAuthFail byte = 0x02

	DataHandshakeMaxClientIDLen = 1024
	DataHandshakeMaxTokenLen    = 256
	MaxDataHandshakePayloadLen  = 2 + DataHandshakeMaxClientIDLen + 2 + DataHandshakeMaxTokenLen
)

func EncodeDataHandshake(clientID, dataToken string) []byte {
	clientIDBytes := []byte(clientID)
	tokenBytes := []byte(dataToken)

	buf := make([]byte, 2+len(clientIDBytes)+2+len(tokenBytes))
	binary.BigEndian.PutUint16(buf[:2], uint16(len(clientIDBytes)))
	copy(buf[2:], clientIDBytes)

	offset := 2 + len(clientIDBytes)
	binary.BigEndian.PutUint16(buf[offset:offset+2], uint16(len(tokenBytes)))
	copy(buf[offset+2:], tokenBytes)

	return buf
}

func DecodeDataHandshake(payload []byte) (clientID, dataToken string, err error) {
	if len(payload) < 4 {
		return "", "", fmt.Errorf("payload too short")
	}
	if len(payload) > MaxDataHandshakePayloadLen {
		return "", "", fmt.Errorf("payload too large")
	}

	clientIDLen := int(binary.BigEndian.Uint16(payload[:2]))
	if clientIDLen == 0 || clientIDLen > DataHandshakeMaxClientIDLen {
		return "", "", fmt.Errorf("invalid client id length")
	}

	offset := 2 + clientIDLen
	if len(payload) < offset+2 {
		return "", "", fmt.Errorf("payload truncated before token length")
	}

	clientID = string(payload[2:offset])

	tokenLen := int(binary.BigEndian.Uint16(payload[offset : offset+2]))
	if tokenLen == 0 || tokenLen > DataHandshakeMaxTokenLen {
		return "", "", fmt.Errorf("invalid token length")
	}
	if len(payload) != offset+2+tokenLen {
		return "", "", fmt.Errorf("payload length mismatch")
	}

	dataToken = string(payload[offset+2:])
	return clientID, dataToken, nil
}
