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

// TLS 模式常量
const (
	TLSModeCustom = "custom" // 用户提供证书
	TLSModeAuto   = "auto"   // 自动生成自签名证书
	TLSModeOff    = "off"    // 不使用 TLS（由反向代理负责）
)

// TLSConfig TLS 配置
type TLSConfig struct {
	Mode           string   `json:"mode"`            // custom / auto / off
	CertFile       string   `json:"cert_file"`       // custom 模式: 证书文件路径
	KeyFile        string   `json:"key_file"`        // custom 模式: 私钥文件路径
	TrustedProxies []string `json:"trusted_proxies"` // off 模式: 受信任代理 CIDR 列表
	AutoDir        string   `json:"auto_dir"`        // auto 模式: 证书自动存储目录（空则使用默认）
}

// Validate 验证 TLS 配置合法性
func (c *TLSConfig) Validate() error {
	switch c.Mode {
	case TLSModeCustom:
		if c.CertFile == "" || c.KeyFile == "" {
			return fmt.Errorf("tls.mode=custom 需要指定 cert_file 和 key_file")
		}
		if _, err := os.Stat(c.CertFile); err != nil {
			return fmt.Errorf("证书文件不可访问: %s: %w", c.CertFile, err)
		}
		if _, err := os.Stat(c.KeyFile); err != nil {
			return fmt.Errorf("私钥文件不可访问: %s: %w", c.KeyFile, err)
		}
	case TLSModeAuto:
		// auto 模式无需额外参数
	case TLSModeOff:
		// off 模式验证 trusted_proxies CIDR 格式
		for _, cidr := range c.TrustedProxies {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				// 尝试按单 IP 解析
				if ip := net.ParseIP(cidr); ip == nil {
					return fmt.Errorf("trusted_proxies 格式无效: %s (需要 CIDR 如 127.0.0.1/32 或 IP 地址)", cidr)
				}
			}
		}
	case "":
		return fmt.Errorf("tls.mode 不能为空，可选值: custom / auto / off")
	default:
		return fmt.Errorf("不支持的 tls.mode: %s，可选值: custom / auto / off", c.Mode)
	}
	return nil
}

// IsEnabled 返回是否启用了 TLS（custom 或 auto）
func (c *TLSConfig) IsEnabled() bool {
	return c.Mode == TLSModeCustom || c.Mode == TLSModeAuto
}

// loadOrBuildTLSConfig 根据 TLSConfig 构建 *tls.Config
// 如果 mode=custom，加载用户提供的证书；
// 如果 mode=auto，从磁盘加载或自动生成自签证书。
func (c *TLSConfig) loadOrBuildTLSConfig(dataDir string) (*tls.Config, string, error) {
	switch c.Mode {
	case TLSModeCustom:
		return c.loadCustomTLS()
	case TLSModeAuto:
		return c.loadOrGenerateAutoTLS(dataDir)
	default:
		return nil, "", fmt.Errorf("TLS 未启用 (mode=%s)", c.Mode)
	}
}

// loadCustomTLS 加载用户提供的证书
func (c *TLSConfig) loadCustomTLS() (*tls.Config, string, error) {
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, "", fmt.Errorf("加载证书失败: %w", err)
	}

	fingerprint := certFingerprint(cert.Certificate[0])

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	log.Printf("🔒 TLS 模式: custom")
	log.Printf("🔒 证书: %s", c.CertFile)
	log.Printf("🔒 私钥: %s", c.KeyFile)
	log.Printf("🔒 证书指纹 (SHA-256): %s", fingerprint)

	return tlsConfig, fingerprint, nil
}

// loadOrGenerateAutoTLS 从磁盘加载或自动生成自签证书
func (c *TLSConfig) loadOrGenerateAutoTLS(dataDir string) (*tls.Config, string, error) {
	autoDir := c.AutoDir
	if autoDir == "" {
		autoDir = filepath.Join(dataDir, "tls")
	}

	certPath := filepath.Join(autoDir, "server.crt")
	keyPath := filepath.Join(autoDir, "server.key")

	// 尝试从磁盘加载已有证书
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			cert, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err == nil {
				// 检查证书是否过期
				x509Cert, parseErr := x509.ParseCertificate(cert.Certificate[0])
				if parseErr == nil && time.Now().Before(x509Cert.NotAfter) {
					fingerprint := certFingerprint(cert.Certificate[0])
					tlsConfig := &tls.Config{
						Certificates: []tls.Certificate{cert},
						MinVersion:   tls.VersionTLS12,
					}
					log.Printf("🔒 TLS 模式: auto (已加载持久化证书)")
					log.Printf("🔒 证书路径: %s", certPath)
					log.Printf("🔒 证书有效期至: %s", x509Cert.NotAfter.Format("2006-01-02"))
					log.Printf("🔒 证书指纹 (SHA-256): %s", fingerprint)
					return tlsConfig, fingerprint, nil
				}
				log.Printf("⚠️ 已有证书已过期或无法解析，将重新生成")
			} else {
				log.Printf("⚠️ 已有证书加载失败: %v，将重新生成", err)
			}
		}
	}

	// 生成新的自签名证书
	log.Printf("🔒 TLS 模式: auto (生成自签名证书)")
	cert, certPEM, keyPEM, err := generateSelfSignedCert()
	if err != nil {
		return nil, "", fmt.Errorf("生成自签名证书失败: %w", err)
	}

	// 持久化到磁盘
	if err := os.MkdirAll(autoDir, 0700); err != nil {
		return nil, "", fmt.Errorf("创建证书目录失败: %w", err)
	}
	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		return nil, "", fmt.Errorf("保存证书文件失败: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return nil, "", fmt.Errorf("保存私钥文件失败: %w", err)
	}

	fingerprint := certFingerprint(cert.Certificate[0])

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	log.Printf("🔒 证书已持久化: %s", certPath)
	log.Printf("🔒 证书指纹 (SHA-256): %s", fingerprint)

	return tlsConfig, fingerprint, nil
}

// generateSelfSignedCert 生成 ECDSA P-256 自签名证书
// 包含当前主机名和所有可检测到的 IP 作为 SAN
func generateSelfSignedCert() (tls.Certificate, []byte, []byte, error) {
	// 生成密钥对
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("生成密钥失败: %w", err)
	}

	// 构建证书模板
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("生成序列号失败: %w", err)
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
		NotAfter:              time.Now().Add(365 * 24 * time.Hour), // 1 年有效期
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	// 添加 SAN: 主机名
	template.DNSNames = []string{hostname, "localhost"}

	// 添加 SAN: 回环地址
	template.IPAddresses = []net.IP{
		net.ParseIP("127.0.0.1"),
		net.ParseIP("::1"),
	}

	// 添加 SAN: 所有可检测到的本机 IP
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				template.IPAddresses = append(template.IPAddresses, ipnet.IP)
			}
		}
	}

	// 签发证书
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("签发证书失败: %w", err)
	}

	// PEM 编码
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	privDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("编码私钥失败: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privDER})

	// 构建 tls.Certificate
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("组装证书失败: %w", err)
	}

	log.Printf("🔒 已生成自签名证书: CN=%s, 有效期至 %s",
		hostname, template.NotAfter.Format("2006-01-02"))
	log.Printf("🔒 SAN DNS: %v", template.DNSNames)
	log.Printf("🔒 SAN IP: %v", template.IPAddresses)

	return cert, certPEM, keyPEM, nil
}

// certFingerprint 计算 DER 编码证书的 SHA-256 指纹
func certFingerprint(certDER []byte) string {
	hash := sha256.Sum256(certDER)
	return formatFingerprint(hex.EncodeToString(hash[:]))
}

// formatFingerprint 将 hex 字符串格式化为 AA:BB:CC 形式
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

// isTrustedProxy 检查给定 IP 是否在受信任代理列表中
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
			// 尝试按单 IP 匹配
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
