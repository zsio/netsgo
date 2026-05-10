package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestSignCreatesRawAndSSHSignatures(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skipf("ssh-keygen not available: %v", err)
	}
	pub, _, keyPEM := testEd25519KeyPEM(t)
	tmpDir := t.TempDir()
	checksumsPath := filepath.Join(tmpDir, "checksums.txt")
	if err := os.WriteFile(checksumsPath, []byte("abc  netsgo_0.1.0_linux_amd64.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := sign(checksumsPath, keyPEM); err != nil {
		t.Fatalf("sign returned error: %v", err)
	}

	rawSig, err := os.ReadFile(checksumsPath + ".sig")
	if err != nil {
		t.Fatalf("read raw signature: %v", err)
	}
	data, _ := os.ReadFile(checksumsPath)
	if !ed25519.Verify(pub, data, rawSig) {
		t.Fatal("raw signature did not verify")
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh public key: %v", err)
	}
	allowedPath := filepath.Join(tmpDir, "allowed_signers")
	allowed := append([]byte("netsgo-release "), ssh.MarshalAuthorizedKey(sshPub)...)
	if err := os.WriteFile(allowedPath, allowed, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ssh-keygen", "-Y", "verify", "-f", allowedPath, "-I", "netsgo-release", "-n", "file", "-s", checksumsPath+".sshsig")
	cmd.Stdin = mustOpen(t, checksumsPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh signature did not verify: %v: %s", err, out)
	}
}

func TestSignRejectsInvalidKey(t *testing.T) {
	tmpDir := t.TempDir()
	checksumsPath := filepath.Join(tmpDir, "checksums.txt")
	if err := os.WriteFile(checksumsPath, []byte("abc  file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sign(checksumsPath, []byte("not pem")); err == nil {
		t.Fatal("expected invalid key error")
	}
}

func TestExtractEmbeddedKeysAndVerifyEmbedded(t *testing.T) {
	_, _, keyPEM := testEd25519KeyPEM(t)
	privateKey, err := readPrivateKeyFromBytes(keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	pub := privateKey.Public().(ed25519.PublicKey)
	publicPEM, err := publicKeyPEM(pub)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := allowedSigners(pub)
	if err != nil {
		t.Fatal(err)
	}
	script := []byte(`NETSGO_RELEASE_PUBLIC_KEY_PEM='` + string(bytes.TrimSpace(publicPEM)) + `'
NETSGO_RELEASE_ALLOWED_SIGNERS='` + string(bytes.TrimSpace(allowed)) + `'
`)
	gotPEM, gotAllowed, err := extractEmbeddedKeys(script)
	if err != nil {
		t.Fatalf("extractEmbeddedKeys returned error: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(gotPEM), bytes.TrimSpace(publicPEM)) {
		t.Fatal("embedded PEM mismatch")
	}
	if string(bytes.TrimSpace(gotAllowed)) != string(bytes.TrimSpace(allowed)) {
		t.Fatal("embedded allowed signers mismatch")
	}
}

func TestEmbeddedKeyBlockCanBeExtracted(t *testing.T) {
	_, _, keyPEM := testEd25519KeyPEM(t)
	privateKey, err := readPrivateKeyFromBytes(keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	pub := privateKey.Public().(ed25519.PublicKey)
	publicPEM, err := publicKeyPEM(pub)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := allowedSigners(pub)
	if err != nil {
		t.Fatal(err)
	}

	gotPEM, gotAllowed, err := extractEmbeddedKeys([]byte(embeddedKeyBlock(publicPEM, allowed)))
	if err != nil {
		t.Fatalf("extractEmbeddedKeys returned error: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(gotPEM), bytes.TrimSpace(publicPEM)) {
		t.Fatal("embedded PEM mismatch")
	}
	if string(bytes.TrimSpace(gotAllowed)) != string(bytes.TrimSpace(allowed)) {
		t.Fatal("embedded allowed signers mismatch")
	}
}

func TestShellSingleQuote(t *testing.T) {
	got := shellSingleQuote("abc'def")
	want := `'abc'"'"'def'`
	if got != want {
		t.Fatalf("shellSingleQuote mismatch: got %q want %q", got, want)
	}
}

func testEd25519KeyPEM(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func readPrivateKeyFromBytes(raw []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return key.(ed25519.PrivateKey), nil
}

func mustOpen(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}
