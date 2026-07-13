package server

import (
	"net"

	"github.com/pion/stun/v3"
)

const maxSTUNPacketSize = 1500

func (s *Server) serveSTUN(conn net.PacketConn) {
	buf := make([]byte, maxSTUNPacketSize)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		request := &stun.Message{Raw: append([]byte(nil), buf[:n]...)}
		if err := request.Decode(); err != nil || request.Type != stun.BindingRequest {
			continue
		}
		udpAddr, ok := addr.(*net.UDPAddr)
		if !ok {
			continue
		}
		response, err := stun.Build(stun.BindingSuccess, stun.NewTransactionIDSetter(request.TransactionID), &stun.XORMappedAddress{IP: udpAddr.IP, Port: udpAddr.Port}, stun.Fingerprint)
		if err != nil {
			continue
		}
		_, _ = conn.WriteTo(response.Raw, addr)
	}
}
