package socks5wire

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"

	"netsgo/internal/credential"
	"netsgo/pkg/protocol"
)

const (
	Version = byte(0x05)

	MethodNoAuth       = byte(0x00)
	MethodUsernamePass = byte(0x02)
	MethodNoAcceptable = byte(0xff)

	AuthVersion = byte(0x01)

	CommandConnect      = byte(0x01)
	CommandBind         = byte(0x02)
	CommandUDPAssociate = byte(0x03)

	AddrIPv4   = byte(0x01)
	AddrDomain = byte(0x03)
	AddrIPv6   = byte(0x04)

	RepSuccess            = byte(0x00)
	RepGeneralFailure     = byte(0x01)
	RepNotAllowed         = byte(0x02)
	RepNetworkUnreachable = byte(0x03)
	RepHostUnreachable    = byte(0x04)
	RepConnectionRefused  = byte(0x05)
	RepCommandUnsupported = byte(0x07)
	RepAddrUnsupported    = byte(0x08)

	maxDialResultLen = 16 * 1024
)

type ConnectRequest struct {
	Host         string
	Port         int
	AddrType     string
	OriginalHost string
}

func WriteDialResult(w io.Writer, result protocol.SOCKS5DialResult) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if len(raw) > maxDialResultLen {
		return fmt.Errorf("SOCKS5 dial result too large")
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(raw)))
	if _, err := w.Write(prefix[:]); err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

func ReadDialResult(r io.Reader) (protocol.SOCKS5DialResult, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return protocol.SOCKS5DialResult{}, err
	}
	length := binary.BigEndian.Uint32(prefix[:])
	if length == 0 || length > maxDialResultLen {
		return protocol.SOCKS5DialResult{}, fmt.Errorf("invalid SOCKS5 dial result length %d", length)
	}
	raw := make([]byte, length)
	if _, err := io.ReadFull(r, raw); err != nil {
		return protocol.SOCKS5DialResult{}, err
	}
	var result protocol.SOCKS5DialResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return protocol.SOCKS5DialResult{}, err
	}
	if result.Status == "" {
		return protocol.SOCKS5DialResult{}, fmt.Errorf("SOCKS5 dial result missing status")
	}
	return result, nil
}

func ServeHandshake(conn net.Conn, cfg protocol.SOCKS5ListenConfig) (ConnectRequest, bool) {
	method, ok := NegotiateMethod(conn, cfg.Auth.Type)
	if !ok {
		return ConnectRequest{}, false
	}
	if method == MethodUsernamePass && !Authenticate(conn, cfg.Auth) {
		return ConnectRequest{}, false
	}
	req, rep, ok := ReadConnectRequest(conn)
	if !ok {
		_ = WriteReply(conn, rep, "", 0)
		return ConnectRequest{}, false
	}
	return req, true
}

func NegotiateMethod(conn net.Conn, authType string) (byte, bool) {
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil || header[0] != Version || header[1] == 0 {
		return 0, false
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return 0, false
	}
	selected := MethodNoAcceptable
	if authType == protocol.SOCKS5AuthTypeUsernamePassword {
		if slices.Contains(methods, MethodUsernamePass) {
			selected = MethodUsernamePass
		}
	} else if slices.Contains(methods, MethodNoAuth) {
		selected = MethodNoAuth
	}
	_, _ = conn.Write([]byte{Version, selected})
	return selected, selected != MethodNoAcceptable
}

func Authenticate(conn net.Conn, auth protocol.SOCKS5AuthConfig) bool {
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil || header[0] != AuthVersion {
		return false
	}
	username := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, username); err != nil {
		return false
	}
	var passLen [1]byte
	if _, err := io.ReadFull(conn, passLen[:]); err != nil {
		return false
	}
	password := make([]byte, int(passLen[0]))
	if _, err := io.ReadFull(conn, password); err != nil {
		return false
	}
	ok := string(username) == auth.Username && credential.VerifyPassword(auth.PasswordHash, string(password))
	status := byte(0x01)
	if ok {
		status = 0x00
	}
	_, _ = conn.Write([]byte{AuthVersion, status})
	return ok
}

func ReadConnectRequest(conn net.Conn) (ConnectRequest, byte, bool) {
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil || header[0] != Version {
		return ConnectRequest{}, RepGeneralFailure, false
	}
	switch header[1] {
	case CommandConnect:
	case CommandBind, CommandUDPAssociate:
		return ConnectRequest{}, RepCommandUnsupported, false
	default:
		return ConnectRequest{}, RepCommandUnsupported, false
	}
	host, addrType, rep, ok := ReadAddress(conn, header[3])
	if !ok {
		return ConnectRequest{}, rep, false
	}
	var portRaw [2]byte
	if _, err := io.ReadFull(conn, portRaw[:]); err != nil {
		return ConnectRequest{}, RepGeneralFailure, false
	}
	port := int(binary.BigEndian.Uint16(portRaw[:]))
	if port < 1 {
		return ConnectRequest{}, RepGeneralFailure, false
	}
	return ConnectRequest{Host: host, Port: port, AddrType: addrType, OriginalHost: host}, 0, true
}

func ReadAddress(r io.Reader, atyp byte) (string, string, byte, bool) {
	switch atyp {
	case AddrIPv4:
		raw := make([]byte, 4)
		if _, err := io.ReadFull(r, raw); err != nil {
			return "", "", RepGeneralFailure, false
		}
		return net.IP(raw).String(), protocol.SOCKS5AddrTypeIPv4, 0, true
	case AddrIPv6:
		raw := make([]byte, 16)
		if _, err := io.ReadFull(r, raw); err != nil {
			return "", "", RepGeneralFailure, false
		}
		return net.IP(raw).String(), protocol.SOCKS5AddrTypeIPv6, 0, true
	case AddrDomain:
		var length [1]byte
		if _, err := io.ReadFull(r, length[:]); err != nil || length[0] == 0 {
			return "", "", RepGeneralFailure, false
		}
		raw := make([]byte, int(length[0]))
		if _, err := io.ReadFull(r, raw); err != nil {
			return "", "", RepGeneralFailure, false
		}
		return strings.ToLower(string(raw)), protocol.SOCKS5AddrTypeDomain, 0, true
	default:
		return "", "", RepAddrUnsupported, false
	}
}

func WriteReply(w io.Writer, rep byte, boundAddr string, boundPort int) error {
	ip := net.ParseIP(boundAddr)
	if ip4 := ip.To4(); ip4 != nil {
		reply := []byte{Version, rep, 0x00, AddrIPv4, ip4[0], ip4[1], ip4[2], ip4[3], 0, 0}
		binary.BigEndian.PutUint16(reply[8:10], uint16(boundPort))
		_, err := w.Write(reply)
		return err
	}
	if ip16 := ip.To16(); ip16 != nil {
		reply := make([]byte, 4+16+2)
		reply[0], reply[1], reply[2], reply[3] = Version, rep, 0x00, AddrIPv6
		copy(reply[4:20], ip16)
		binary.BigEndian.PutUint16(reply[20:22], uint16(boundPort))
		_, err := w.Write(reply)
		return err
	}
	reply := []byte{Version, rep, 0x00, AddrIPv4, 0, 0, 0, 0, 0, 0}
	_, err := w.Write(reply)
	return err
}

func ReplyForDialStatus(status string) byte {
	switch status {
	case protocol.SOCKS5DialStatusSuccess:
		return RepSuccess
	case protocol.SOCKS5DialStatusTargetDenied:
		return RepNotAllowed
	case protocol.SOCKS5DialStatusNetworkUnreachable:
		return RepNetworkUnreachable
	case protocol.SOCKS5DialStatusHostUnreachable:
		return RepHostUnreachable
	case protocol.SOCKS5DialStatusConnectionRefused:
		return RepConnectionRefused
	case protocol.SOCKS5DialStatusDialTimeout:
		return RepHostUnreachable
	default:
		return RepGeneralFailure
	}
}
