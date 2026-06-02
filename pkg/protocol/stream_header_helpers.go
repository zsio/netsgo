package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

const (
	StreamHeaderMagic        = "NGSH"
	StreamHeaderVersion byte = 1

	StreamHeaderMaxLen          = 16 * 1024
	StreamHeaderMaxStringLen    = 1024
	StreamHeaderMaxTransportLen = 64
)

func WriteStreamHeader(w io.Writer, header StreamHeader) error {
	if err := validateStreamHeader(header); err != nil {
		return err
	}
	payload, err := json.Marshal(header)
	if err != nil {
		return fmt.Errorf("marshal stream header: %w", err)
	}
	if len(payload) == 0 || len(payload) > StreamHeaderMaxLen {
		return fmt.Errorf("stream header length %d exceeds limit", len(payload))
	}

	var prefix [9]byte
	copy(prefix[:4], StreamHeaderMagic)
	prefix[4] = StreamHeaderVersion
	binary.BigEndian.PutUint32(prefix[5:9], uint32(len(payload)))
	if _, err := w.Write(prefix[:]); err != nil {
		return fmt.Errorf("write stream header prefix: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("write stream header payload: %w", err)
	}
	return nil
}

func WriteStreamHeaderToBytes(header StreamHeader) ([]byte, error) {
	var buf bytes.Buffer
	if err := WriteStreamHeader(&buf, header); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func ReadStreamHeader(r io.Reader) (StreamHeader, error) {
	var prefix [9]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return StreamHeader{}, fmt.Errorf("read stream header prefix: %w", err)
	}
	if string(prefix[:4]) != StreamHeaderMagic {
		return StreamHeader{}, fmt.Errorf("invalid stream header magic")
	}
	if prefix[4] != StreamHeaderVersion {
		return StreamHeader{}, fmt.Errorf("unsupported stream header version %d", prefix[4])
	}

	length := binary.BigEndian.Uint32(prefix[5:9])
	if length == 0 || length > StreamHeaderMaxLen {
		return StreamHeader{}, fmt.Errorf("invalid stream header length %d", length)
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return StreamHeader{}, fmt.Errorf("read stream header payload: %w", err)
	}
	if !utf8.Valid(payload) {
		return StreamHeader{}, fmt.Errorf("stream header is not utf-8")
	}

	var header StreamHeader
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&header); err != nil {
		return StreamHeader{}, fmt.Errorf("decode stream header json: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return StreamHeader{}, fmt.Errorf("stream header must be one json object")
	}
	if err := validateStreamHeader(header); err != nil {
		return StreamHeader{}, err
	}
	return header, nil
}

func validateStreamHeader(header StreamHeader) error {
	if header.ProxyName == "" {
		return fmt.Errorf("stream header proxy_name is required")
	}
	if len(header.ProxyName) > StreamHeaderMaxStringLen {
		return fmt.Errorf("stream header proxy_name too large: %d > %d", len(header.ProxyName), StreamHeaderMaxStringLen)
	}
	if len(header.TransportPolicy) > StreamHeaderMaxTransportLen {
		return fmt.Errorf("stream header transport_policy too large: %d > %d", len(header.TransportPolicy), StreamHeaderMaxTransportLen)
	}
	if len(header.ActualTransport) > StreamHeaderMaxTransportLen {
		return fmt.Errorf("stream header actual_transport too large: %d > %d", len(header.ActualTransport), StreamHeaderMaxTransportLen)
	}
	return nil
}
