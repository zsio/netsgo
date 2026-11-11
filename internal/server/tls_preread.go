package server

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/cryptobyte"
)

const (
	tlsPrereadTimeout   = 5 * time.Second
	maxClientHelloBytes = 64 << 10
)

type prereadConn struct {
	net.Conn
	reader io.Reader
}

func (c *prereadConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// Read only enough records to inspect the ClientHello. Every consumed byte,
// including any data coalesced with the hello, is replayed to the chosen path.
func prereadTLS(conn net.Conn) (net.Conn, string, bool, error) {
	if err := conn.SetReadDeadline(time.Now().Add(tlsPrereadTimeout)); err != nil {
		return nil, "", false, err
	}
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	reader := bufio.NewReader(conn)
	first, err := reader.Peek(1)
	if err != nil {
		return nil, "", false, err
	}
	if first[0] != 22 {
		return &prereadConn{Conn: conn, reader: reader}, "", false, nil
	}
	var wire, handshake []byte
	for {
		var header [5]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return nil, "", true, err
		}
		length := int(binary.BigEndian.Uint16(header[3:]))
		if header[0] != 22 || header[1] != 3 || header[2] > 4 || length > 16384 || len(wire)+5+length > maxClientHelloBytes {
			return nil, "", true, fmt.Errorf("invalid or oversized TLS ClientHello record")
		}
		wire = append(wire, header[:]...)
		start := len(wire)
		wire = append(wire, make([]byte, length)...)
		if _, err := io.ReadFull(reader, wire[start:]); err != nil {
			return nil, "", true, err
		}
		handshake = append(handshake, wire[start:]...)
		if len(handshake) < 4 {
			continue
		}
		helloLength := 4 + (int(handshake[1]) << 16) + (int(handshake[2]) << 8) + int(handshake[3])
		if handshake[0] != 1 || helloLength > maxClientHelloBytes {
			return nil, "", true, fmt.Errorf("invalid or oversized TLS ClientHello")
		}
		if len(handshake) < helloLength {
			continue
		}
		name, err := clientHelloServerName(handshake[4:helloLength])
		if err != nil {
			return nil, "", true, err
		}
		return &prereadConn{Conn: conn, reader: io.MultiReader(bytes.NewReader(wire), reader)}, name, true, nil
	}
}

func clientHelloServerName(hello []byte) (string, error) {
	invalid := fmt.Errorf("malformed TLS ClientHello")
	data := cryptobyte.String(hello)
	var sessionID, ciphers, compression, extensions cryptobyte.String
	if !data.Skip(34) || !data.ReadUint8LengthPrefixed(&sessionID) || len(sessionID) > 32 ||
		!data.ReadUint16LengthPrefixed(&ciphers) || len(ciphers) == 0 || len(ciphers)%2 != 0 ||
		!data.ReadUint8LengthPrefixed(&compression) || len(compression) == 0 {
		return "", invalid
	}
	if data.Empty() { // A TLS 1.2 ClientHello may omit extensions.
		return "", nil
	}
	if !data.ReadUint16LengthPrefixed(&extensions) || !data.Empty() {
		return "", invalid
	}
	var serverName string
	seenSNI := false
	for !extensions.Empty() {
		var kind uint16
		var extension cryptobyte.String
		if !extensions.ReadUint16(&kind) || !extensions.ReadUint16LengthPrefixed(&extension) {
			return "", invalid
		}
		if kind != 0 {
			continue
		}
		var names cryptobyte.String
		if seenSNI || !extension.ReadUint16LengthPrefixed(&names) || names.Empty() || !extension.Empty() {
			return "", invalid
		}
		seenSNI = true
		for !names.Empty() {
			var nameType uint8
			var name cryptobyte.String
			if !names.ReadUint8(&nameType) || !names.ReadUint16LengthPrefixed(&name) || len(name) == 0 {
				return "", invalid
			}
			if nameType != 0 {
				continue
			}
			if serverName != "" || len(name) > 253 || strings.ContainsAny(string(name), ":/\\*?#@[] \t\r\n\x00") {
				return "", invalid
			}
			serverName = canonicalHTTPDomain(string(name))
		}
	}
	return serverName, nil
}
