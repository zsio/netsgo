package server

import (
	"net"
	"testing"
	"time"

	"github.com/pion/stun/v3"
)

func TestSTUNBindingReturnsObservedAddress(t *testing.T) {
	serverConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverConn.Close() }()
	s := New(0)
	go s.serveSTUN(serverConn)
	client, err := net.Dial("udp", serverConn.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	request := stun.MustBuild(stun.TransactionID, stun.BindingRequest, stun.Fingerprint)
	if _, err := client.Write(request.Raw); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, maxSTUNPacketSize)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	response := &stun.Message{Raw: buf[:n]}
	if err := response.Decode(); err != nil {
		t.Fatal(err)
	}
	var mapped stun.XORMappedAddress
	if err := mapped.GetFrom(response); err != nil {
		t.Fatal(err)
	}
	if mapped.IP == nil || mapped.Port == 0 {
		t.Fatalf("invalid mapped address: %v", mapped)
	}
}
