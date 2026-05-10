package releasesign

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestPrivateKeyFromPEMAndRawSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	parsed, err := PrivateKeyFromPEM(pemBytes)
	if err != nil {
		t.Fatalf("PrivateKeyFromPEM returned error: %v", err)
	}
	data := []byte("checksums")
	sig := RawSignature(parsed, data)
	if !ed25519.Verify(pub, data, sig) {
		t.Fatal("raw signature did not verify")
	}
	if ed25519.Verify(pub, []byte("changed"), sig) {
		t.Fatal("signature verified after data changed")
	}
}

func TestPrivateKeyFromPEMRejectsWrongKey(t *testing.T) {
	_, err := PrivateKeyFromPEM([]byte("not pem"))
	if err == nil {
		t.Fatal("expected invalid PEM error")
	}
}
