package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"

	"netsgo/internal/releasesign"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: netsgo-release-sign sign <checksums.txt> | keygen | public | verify-embedded <scripts/common-update.sh>")
		os.Exit(2)
	}
	command := os.Args[1]
	if command != "sign" && command != "keygen" && command != "public" && command != "verify-embedded" && len(os.Args) == 2 {
		command = "sign"
		os.Args = []string{os.Args[0], "sign", os.Args[1]}
	}

	var err error
	switch command {
	case "sign":
		err = runSign(os.Args[2:])
	case "keygen":
		err = runKeygen(os.Args[2:])
	case "public":
		err = runPublic(os.Args[2:])
	case "verify-embedded":
		err = runVerifyEmbedded(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command: %s", command)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runSign(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: netsgo-release-sign sign <checksums.txt>")
	}
	keyPEM := os.Getenv("NETSGO_RELEASE_SIGNING_KEY_PEM")
	if keyPEM == "" {
		return fmt.Errorf("NETSGO_RELEASE_SIGNING_KEY_PEM is required")
	}
	return sign(args[0], []byte(keyPEM))
}

func runKeygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	privateOut := fs.String("private-out", "", "write PKCS8 PEM private key to path instead of stdout")
	publicOut := fs.String("public-out", "", "write PEM public key to path")
	allowedOut := fs.String("allowed-signers-out", "", "write OpenSSH allowed signers line to path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	privatePEM, err := privateKeyPEM(priv)
	if err != nil {
		return err
	}
	publicPEM, err := publicKeyPEM(pub)
	if err != nil {
		return err
	}
	allowed, err := allowedSigners(pub)
	if err != nil {
		return err
	}
	if err := writeOrStdout(*privateOut, privatePEM); err != nil {
		return err
	}
	if *publicOut != "" {
		if err := os.WriteFile(*publicOut, publicPEM, 0o644); err != nil {
			return fmt.Errorf("write public key: %w", err)
		}
	}
	if *allowedOut != "" {
		if err := os.WriteFile(*allowedOut, allowed, 0o644); err != nil {
			return fmt.Errorf("write allowed signers: %w", err)
		}
	}
	return nil
}

func runPublic(args []string) error {
	fs := flag.NewFlagSet("public", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	privateKeyPath := fs.String("private-key", "", "read PKCS8 PEM private key from path instead of NETSGO_RELEASE_SIGNING_KEY_PEM")
	shell := fs.Bool("shell", false, "print shell assignments for scripts/common-update.sh")
	if err := fs.Parse(args); err != nil {
		return err
	}
	privateKey, err := readPrivateKey(*privateKeyPath)
	if err != nil {
		return err
	}
	pub, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("derive Ed25519 public key")
	}
	publicPEM, err := publicKeyPEM(pub)
	if err != nil {
		return err
	}
	allowed, err := allowedSigners(pub)
	if err != nil {
		return err
	}
	if *shell {
		fmt.Print(embeddedKeyBlock(publicPEM, allowed))
		return nil
	}
	fmt.Print(string(publicPEM))
	fmt.Print(string(allowed))
	return nil
}

func runVerifyEmbedded(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: netsgo-release-sign verify-embedded <scripts/common-update.sh>")
	}
	privateKey, err := readPrivateKey("")
	if err != nil {
		return err
	}
	pub, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("derive Ed25519 public key")
	}
	wantPEM, err := publicKeyPEM(pub)
	if err != nil {
		return err
	}
	wantAllowed, err := allowedSigners(pub)
	if err != nil {
		return err
	}
	script, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("read script: %w", err)
	}
	gotPEM, gotAllowed, err := extractEmbeddedKeys(script)
	if err != nil {
		return err
	}
	if !bytes.Equal(bytes.TrimSpace(gotPEM), bytes.TrimSpace(wantPEM)) {
		return fmt.Errorf("embedded PEM public key does not match NETSGO_RELEASE_SIGNING_KEY_PEM")
	}
	if strings.TrimSpace(string(gotAllowed)) != strings.TrimSpace(string(wantAllowed)) {
		return fmt.Errorf("embedded OpenSSH allowed signers key does not match NETSGO_RELEASE_SIGNING_KEY_PEM")
	}
	return nil
}

func sign(checksumsPath string, keyPEM []byte) error {
	privateKey, err := releasesign.PrivateKeyFromPEM(keyPEM)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(checksumsPath)
	if err != nil {
		return fmt.Errorf("read checksums: %w", err)
	}
	if err := writeSSHSignature(checksumsPath, privateKey); err != nil {
		return err
	}
	if err := os.WriteFile(checksumsPath+".sig", releasesign.RawSignature(privateKey, data), 0o644); err != nil {
		return fmt.Errorf("write raw signature: %w", err)
	}
	return nil
}

func writeSSHSignature(checksumsPath string, privateKey ed25519.PrivateKey) error {
	tmpDir, err := os.MkdirTemp("", "netsgo-release-sign-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	keyPath := filepath.Join(tmpDir, "release_ed25519")
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return fmt.Errorf("create ssh signer: %w", err)
	}
	privatePEM, err := ssh.MarshalPrivateKey(privateKey, "")
	if err != nil {
		return fmt.Errorf("marshal ssh private key: %w", err)
	}
	if err := os.WriteFile(keyPath, pemEncode(privatePEM), 0o600); err != nil {
		return fmt.Errorf("write ssh private key: %w", err)
	}
	if err := os.WriteFile(keyPath+".pub", ssh.MarshalAuthorizedKey(signer.PublicKey()), 0o644); err != nil {
		return fmt.Errorf("write ssh public key: %w", err)
	}
	cmd := exec.Command("ssh-keygen", "-Y", "sign", "-f", keyPath, "-n", "file", checksumsPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create sshsig: %w: %s", err, string(out))
	}
	if err := os.Rename(checksumsPath+".sig", checksumsPath+".sshsig"); err != nil {
		return fmt.Errorf("write sshsig: %w", err)
	}
	return nil
}

func pemEncode(block *pem.Block) []byte {
	return pem.EncodeToMemory(block)
}

func readPrivateKey(path string) (ed25519.PrivateKey, error) {
	var raw []byte
	var err error
	if path != "" {
		raw, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read private key: %w", err)
		}
	} else {
		value := os.Getenv("NETSGO_RELEASE_SIGNING_KEY_PEM")
		if value == "" {
			return nil, fmt.Errorf("NETSGO_RELEASE_SIGNING_KEY_PEM is required")
		}
		raw = []byte(value)
	}
	return releasesign.PrivateKeyFromPEM(raw)
}

func privateKeyPEM(privateKey ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func publicKeyPEM(publicKey ed25519.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, fmt.Errorf("marshal public key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

func allowedSigners(publicKey ed25519.PublicKey) ([]byte, error) {
	sshPub, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		return nil, fmt.Errorf("marshal ssh public key: %w", err)
	}
	return append([]byte("netsgo-release "), ssh.MarshalAuthorizedKey(sshPub)...), nil
}

func writeOrStdout(path string, data []byte) error {
	if path == "" {
		_, err := os.Stdout.Write(data)
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}
	return nil
}

func extractEmbeddedKeys(script []byte) ([]byte, []byte, error) {
	text := string(script)
	pemValue, ok := extractSingleQuotedAssignment(text, "NETSGO_RELEASE_PUBLIC_KEY_PEM")
	if !ok {
		return nil, nil, fmt.Errorf("embedded PEM public key is missing")
	}
	allowedValue, ok := extractSingleQuotedAssignment(text, "NETSGO_RELEASE_ALLOWED_SIGNERS")
	if !ok {
		return nil, nil, fmt.Errorf("embedded allowed signers key is missing")
	}
	return []byte(pemValue), []byte(allowedValue), nil
}

func embeddedKeyBlock(publicPEM, allowed []byte) string {
	var b strings.Builder
	b.WriteString("# BEGIN NETSGO RELEASE PUBLIC KEYS\n")
	b.WriteString("if [ -z \"${NETSGO_RELEASE_PUBLIC_KEY_PEM:-}\" ]; then\n")
	b.WriteString("  NETSGO_RELEASE_PUBLIC_KEY_PEM=")
	b.WriteString(shellSingleQuote(strings.TrimSpace(string(publicPEM))))
	b.WriteString("\n")
	b.WriteString("fi\n")
	b.WriteString("if [ -z \"${NETSGO_RELEASE_ALLOWED_SIGNERS:-}\" ]; then\n")
	b.WriteString("  NETSGO_RELEASE_ALLOWED_SIGNERS=")
	b.WriteString(shellSingleQuote(strings.TrimSpace(string(allowed))))
	b.WriteString("\n")
	b.WriteString("fi\n")
	b.WriteString("# END NETSGO RELEASE PUBLIC KEYS\n")
	return b.String()
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func extractSingleQuotedAssignment(text, name string) (string, bool) {
	for _, marker := range []string{"\n  " + name + "=", "\n" + name + "=", name + "="} {
		start := strings.Index(text, marker)
		if start == -1 {
			continue
		}
		start += len(marker)
		if start >= len(text) || text[start] != '\'' {
			continue
		}
		start++
		end := strings.IndexByte(text[start:], '\'')
		if end == -1 {
			return "", false
		}
		return text[start : start+end], true
	}
	return "", false
}
