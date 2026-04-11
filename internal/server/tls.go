package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TLS mode constants
const (
	TLSModeCustom = "custom" // User-provided certificate
	TLSModeAuto   = "auto"   // Auto-generate a self-signed certificate
	TLSModeOff    = "off"    // No TLS (handled by a reverse proxy)
)

// TLSConfig holds the TLS configuration.
type TLSConfig struct {
	Mode           string   `json:"mode"`            // custom / auto / off
	CertFile       string   `json:"cert_file"`       // custom mode: certificate file path
	KeyFile        string   `json:"key_file"`        // custom mode: private key file path
	TrustedProxies []string `json:"trusted_proxies"` // off mode: list of trusted proxy CIDRs
	AutoDir        string   `json:"auto_dir"`        // auto mode: certificate storage directory (empty = default)
}

// Validate checks the TLS configuration for validity.
func (c *TLSConfig) Validate() error {
	switch c.Mode {
	case TLSModeCustom:
		if c.CertFile == "" || c.KeyFile == "" {
			return fmt.Errorf("tls.mode=custom requires cert_file and key_file")
		}
		if _, err := os.Stat(c.CertFile); err != nil {
			return fmt.Errorf("certificate file is not accessible: %s: %w", c.CertFile, err)
		}
		if _, err := os.Stat(c.KeyFile); err != nil {
			return fmt.Errorf("private key file is not accessible: %s: %w", c.KeyFile, err)
		}
	case TLSModeAuto:
		// No additional parameters required for auto mode.
	case TLSModeOff:
		// Validate trusted_proxies CIDR format for off mode.
		for _, cidr := range c.TrustedProxies {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				// Try parsing as a plain IP address.
				if ip := net.ParseIP(cidr); ip == nil {
					return fmt.Errorf("trusted_proxies has invalid format: %s (expected CIDR like 127.0.0.1/32 or an IP address)", cidr)
				}
			}
		}
	case "":
		return fmt.Errorf("tls.mode cannot be empty; valid values: custom / auto / off")
	default:
		return fmt.Errorf("unsupported tls.mode: %s; valid values: custom / auto / off", c.Mode)
	}
	return nil
}

// IsEnabled reports whether TLS is enabled (custom or auto).
func (c *TLSConfig) IsEnabled() bool {
	return c.Mode == TLSModeCustom || c.Mode == TLSModeAuto
}

// loadOrBuildTLSConfig builds a *tls.Config from the TLSConfig.
// If mode=custom, loads the user-provided certificate.
// If mode=auto, loads from disk or auto-generates a self-signed certificate.
func (c *TLSConfig) loadOrBuildTLSConfig(dataDir string) (*tls.Config, string, error) {
	switch c.Mode {
	case TLSModeCustom:
		return c.loadCustomTLS()
	case TLSModeAuto:
		return c.loadOrGenerateAutoTLS(dataDir)
	default:
		return nil, "", fmt.Errorf("TLS is not enabled (mode=%s)", c.Mode)
	}
}

// loadCustomTLS loads the user-provided certificate.
func (c *TLSConfig) loadCustomTLS() (*tls.Config, string, error) {
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load certificate: %w", err)
	}

	fingerprint := certFingerprint(cert.Certificate[0])

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	log.Printf("🔒 TLS mode: custom")
	log.Printf("🔒 Certificate: %s", c.CertFile)
	log.Printf("🔒 Private key: %s", c.KeyFile)
	log.Printf("🔒 Certificate fingerprint (SHA-256): %s", fingerprint)

	return tlsConfig, fingerprint, nil
}

// loadOrGenerateAutoTLS loads from disk or auto-generates a self-signed certificate.
func (c *TLSConfig) loadOrGenerateAutoTLS(dataDir string) (*tls.Config, string, error) {
	autoDir := c.AutoDir
	if autoDir == "" {
		autoDir = filepath.Join(dataDir, "server", "tls")
	}

	certPath := filepath.Join(autoDir, "server.crt")
	keyPath := filepath.Join(autoDir, "server.key")

	// Try to load an existing certificate from disk.
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			cert, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err == nil {
				// Check whether the certificate has expired.
				x509Cert, parseErr := x509.ParseCertificate(cert.Certificate[0])
				if parseErr == nil && time.Now().Before(x509Cert.NotAfter) {
					fingerprint := certFingerprint(cert.Certificate[0])
					tlsConfig := &tls.Config{
						Certificates: []tls.Certificate{cert},
						MinVersion:   tls.VersionTLS12,
					}
					log.Printf("🔒 TLS mode: auto (loaded persisted certificate)")
					log.Printf("🔒 Certificate path: %s", certPath)
					log.Printf("🔒 Certificate valid until: %s", x509Cert.NotAfter.Format("2006-01-02"))
					log.Printf("🔒 Certificate fingerprint (SHA-256): %s", fingerprint)
					return tlsConfig, fingerprint, nil
				}
				log.Printf("⚠️ Existing certificate has expired or could not be parsed; regenerating")
			} else {
				log.Printf("⚠️ Failed to load existing certificate: %v; regenerating", err)
			}
		}
	}

	// Generate a new self-signed certificate.
	log.Printf("🔒 TLS mode: auto (generating self-signed certificate)")
	cert, certPEM, keyPEM, err := generateSelfSignedCert()
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate self-signed certificate: %w", err)
	}

	// Persist to disk.
	if err := os.MkdirAll(autoDir, 0700); err != nil {
		return nil, "", fmt.Errorf("failed to create certificate directory: %w", err)
	}
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		return nil, "", fmt.Errorf("failed to save certificate file: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return nil, "", fmt.Errorf("failed to save private key file: %w", err)
	}

	fingerprint := certFingerprint(cert.Certificate[0])

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	log.Printf("🔒 Certificate persisted: %s", certPath)
	log.Printf("🔒 Certificate fingerprint (SHA-256): %s", fingerprint)

	return tlsConfig, fingerprint, nil
}

// generateSelfSignedCert generates an ECDSA P-256 self-signed certificate
// with the current hostname and all detectable IPs as SANs.
func generateSelfSignedCert() (tls.Certificate, []byte, []byte, error) {
	// Generate a key pair.
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("failed to generate key: %w", err)
	}

	// Build the certificate template.
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "netsgo"
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   hostname,
			Organization: []string{"NetsGo Auto-Generated"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour), // valid for 1 year
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	// Add SAN: hostname
	template.DNSNames = []string{hostname, "localhost"}

	// Add SAN: loopback addresses
	template.IPAddresses = []net.IP{
		net.ParseIP("127.0.0.1"),
		net.ParseIP("::1"),
	}

	// Add SAN: all detectable local IPs
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				template.IPAddresses = append(template.IPAddresses, ipnet.IP)
			}
		}
	}

	// Sign the certificate.
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("failed to issue certificate: %w", err)
	}

	// PEM encode.
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	privDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("failed to encode private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER})

	// Assemble the tls.Certificate.
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("failed to assemble certificate: %w", err)
	}

	log.Printf("🔒 Self-signed certificate generated: CN=%s, valid until %s",
		hostname, template.NotAfter.Format("2006-01-02"))
	log.Printf("🔒 SAN DNS: %v", template.DNSNames)
	log.Printf("🔒 SAN IP: %v", template.IPAddresses)

	return cert, certPEM, keyPEM, nil
}

// certFingerprint computes the SHA-256 fingerprint of a DER-encoded certificate.
func certFingerprint(certDER []byte) string {
	hash := sha256.Sum256(certDER)
	return formatFingerprint(hex.EncodeToString(hash[:]))
}

// formatFingerprint formats a hex string as AA:BB:CC notation.
func formatFingerprint(hexStr string) string {
	hexStr = strings.ToUpper(hexStr)
	parts := make([]string, 0, len(hexStr)/2)
	for i := 0; i < len(hexStr); i += 2 {
		end := i + 2
		if end > len(hexStr) {
			end = len(hexStr)
		}
		parts = append(parts, hexStr[i:end])
	}
	return strings.Join(parts, ":")
}

// isTrustedProxy reports whether the given IP is in the trusted proxy list.
func (c *TLSConfig) isTrustedProxy(ip string) bool {
	if c.Mode != TLSModeOff || len(c.TrustedProxies) == 0 {
		return false
	}

	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}

	for _, cidr := range c.TrustedProxies {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			// Try matching as a plain IP.
			if net.ParseIP(cidr) != nil && cidr == ip {
				return true
			}
			continue
		}
		if network.Contains(parsedIP) {
			return true
		}
	}
	return false
}
