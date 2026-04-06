package server

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================
// TLS 配置验证测试
// ============================================================

func TestTLSConfig_Validate_Custom_Valid(t *testing.T) {
	// 先生成一对临时证书用于测试
	tmpDir := t.TempDir()
	cert, certPEM, keyPEM, err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("生成测试证书失败: %v", err)
	}
	_ = cert

	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	os.WriteFile(certPath, certPEM, 0600)
	os.WriteFile(keyPath, keyPEM, 0600)

	cfg := &TLSConfig{
		Mode:     TLSModeCustom,
		CertFile: certPath,
		KeyFile:  keyPath,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("合法 custom 配置验证失败: %v", err)
	}
}

func TestTLSConfig_Validate_Custom_MissingFiles(t *testing.T) {
	cfg := &TLSConfig{
		Mode:     TLSModeCustom,
		CertFile: "",
		KeyFile:  "",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("缺少 cert_file/key_file 应返回错误")
	}
	if !strings.Contains(err.Error(), "cert_file") {
		t.Errorf("错误信息应提到 cert_file: %v", err)
	}
}

func TestTLSConfig_Validate_Custom_FileNotExist(t *testing.T) {
	cfg := &TLSConfig{
		Mode:     TLSModeCustom,
		CertFile: "/nonexistent/cert.pem",
		KeyFile:  "/nonexistent/key.pem",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("文件不存在应返回错误")
	}
}

func TestTLSConfig_Validate_Auto(t *testing.T) {
	cfg := &TLSConfig{Mode: TLSModeAuto}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("auto 模式验证不应失败: %v", err)
	}
}

func TestTLSConfig_Validate_Off_ValidCIDR(t *testing.T) {
	cfg := &TLSConfig{
		Mode:           TLSModeOff,
		TrustedProxies: []string{"127.0.0.1/32", "10.0.0.0/8"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("合法 CIDR 验证失败: %v", err)
	}
}

func TestTLSConfig_Validate_Off_InvalidCIDR(t *testing.T) {
	cfg := &TLSConfig{
		Mode:           TLSModeOff,
		TrustedProxies: []string{"not-a-cidr"},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("非法 CIDR 应返回错误")
	}
}

func TestTLSConfig_Validate_Off_SingleIP(t *testing.T) {
	cfg := &TLSConfig{
		Mode:           TLSModeOff,
		TrustedProxies: []string{"192.168.1.1"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("单 IP 格式应被接受: %v", err)
	}
}

func TestTLSConfig_Validate_EmptyMode(t *testing.T) {
	cfg := &TLSConfig{Mode: ""}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("空 mode 应返回错误")
	}
}

func TestTLSConfig_Validate_UnknownMode(t *testing.T) {
	cfg := &TLSConfig{Mode: "mtls"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("未知 mode 应返回错误")
	}
}

func TestTLSConfig_IsEnabled(t *testing.T) {
	tests := []struct {
		mode    string
		enabled bool
	}{
		{"custom", true},
		{"auto", true},
		{"off", false},
	}
	for _, tt := range tests {
		cfg := &TLSConfig{Mode: tt.mode}
		if cfg.IsEnabled() != tt.enabled {
			t.Errorf("mode=%s: IsEnabled() 期望 %v", tt.mode, tt.enabled)
		}
	}
}

// ============================================================
// 自签名证书生成测试
// ============================================================

func TestGenerateSelfSignedCert_Basic(t *testing.T) {
	cert, certPEM, keyPEM, err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("生成自签名证书失败: %v", err)
	}

	if len(certPEM) == 0 {
		t.Error("证书 PEM 不应为空")
	}
	if len(keyPEM) == 0 {
		t.Error("私钥 PEM 不应为空")
	}
	if len(cert.Certificate) == 0 {
		t.Error("证书 DER 不应为空")
	}

	// 解析证书检查字段
	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("解析证书失败: %v", err)
	}

	// 应包含 localhost 作为 SAN
	foundLocalhost := false
	for _, dns := range x509Cert.DNSNames {
		if dns == "localhost" {
			foundLocalhost = true
		}
	}
	if !foundLocalhost {
		t.Error("证书 SAN 应包含 localhost")
	}

	// 应包含 127.0.0.1 作为 IP SAN
	found127 := false
	for _, ip := range x509Cert.IPAddresses {
		if ip.Equal(net.ParseIP("127.0.0.1")) {
			found127 = true
		}
	}
	if !found127 {
		t.Error("证书 SAN 应包含 127.0.0.1")
	}

	// 应有 Server Auth EKU
	foundServerAuth := false
	for _, eku := range x509Cert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageServerAuth {
			foundServerAuth = true
		}
	}
	if !foundServerAuth {
		t.Error("证书应包含 ServerAuth EKU")
	}
}

// ============================================================
// 证书持久化测试
// ============================================================

func TestAutoTLS_PersistAndReload(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &TLSConfig{
		Mode:    TLSModeAuto,
		AutoDir: filepath.Join(tmpDir, "tls"),
	}

	// 第一次生成
	tlsCfg1, fp1, err := cfg.loadOrBuildTLSConfig(tmpDir)
	if err != nil {
		t.Fatalf("首次生成自签证书失败: %v", err)
	}
	if tlsCfg1 == nil {
		t.Fatal("tlsConfig 不应为 nil")
	}
	if fp1 == "" {
		t.Fatal("指纹不应为空")
	}

	// 验证文件已持久化
	certPath := filepath.Join(tmpDir, "tls", "server.crt")
	keyPath := filepath.Join(tmpDir, "tls", "server.key")

	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("证书文件应已持久化: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("私钥文件应已持久化: %v", err)
	}

	// 第二次加载应使用缓存
	_, fp2, err := cfg.loadOrBuildTLSConfig(tmpDir)
	if err != nil {
		t.Fatalf("重新加载证书失败: %v", err)
	}

	// 指纹应一致（证书未重新生成）
	if fp1 != fp2 {
		t.Errorf("重启后指纹应保持稳定: %s != %s", fp1, fp2)
	}
}

func TestAutoTLS_DefaultDir_UsesServerSubdir(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &TLSConfig{Mode: TLSModeAuto}
	if _, _, err := cfg.loadOrBuildTLSConfig(tmpDir); err != nil {
		t.Fatalf("loadOrBuildTLSConfig() error = %v", err)
	}

	certPath := filepath.Join(tmpDir, "server", "tls", "server.crt")
	keyPath := filepath.Join(tmpDir, "server", "tls", "server.key")

	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("expected cert at %q: %v", certPath, err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("expected key at %q: %v", keyPath, err)
	}
}

// ============================================================
// 证书指纹格式测试
// ============================================================

func TestCertFingerprint_Format(t *testing.T) {
	cert, _, _, err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("生成证书失败: %v", err)
	}

	fp := certFingerprint(cert.Certificate[0])
	if fp == "" {
		t.Fatal("指纹不应为空")
	}

	// 指纹格式应为 AA:BB:CC:... (SHA-256 = 64 hex chars + 31 colons = 95 chars)
	if len(fp) != 95 {
		t.Errorf("SHA-256 指纹长度应为 95，得到 %d: %s", len(fp), fp)
	}

	parts := strings.Split(fp, ":")
	if len(parts) != 32 {
		t.Errorf("指纹应有 32 个组，得到 %d", len(parts))
	}
}

// ============================================================
// Trusted Proxy 测试
// ============================================================

func TestIsTrustedProxy(t *testing.T) {
	cfg := &TLSConfig{
		Mode:           TLSModeOff,
		TrustedProxies: []string{"127.0.0.1/32", "10.0.0.0/8", "192.168.1.100"},
	}

	tests := []struct {
		ip      string
		trusted bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"192.168.1.100", true},
		{"192.168.1.101", false},
		{"8.8.8.8", false},
		{"", false},
		{"invalid", false},
	}

	for _, tt := range tests {
		result := cfg.isTrustedProxy(tt.ip)
		if result != tt.trusted {
			t.Errorf("isTrustedProxy(%q) = %v, 期望 %v", tt.ip, result, tt.trusted)
		}
	}
}

func TestIsTrustedProxy_NotOffMode(t *testing.T) {
	cfg := &TLSConfig{
		Mode:           TLSModeAuto,
		TrustedProxies: []string{"127.0.0.1/32"},
	}
	if cfg.isTrustedProxy("127.0.0.1") {
		t.Error("非 off 模式不应匹配 trusted proxy")
	}
}

// ============================================================
// Custom TLS 加载测试
// ============================================================

func TestCustomTLS_Load(t *testing.T) {
	tmpDir := t.TempDir()
	_, certPEM, keyPEM, err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("生成测试证书失败: %v", err)
	}

	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	os.WriteFile(certPath, certPEM, 0600)
	os.WriteFile(keyPath, keyPEM, 0600)

	cfg := &TLSConfig{
		Mode:     TLSModeCustom,
		CertFile: certPath,
		KeyFile:  keyPath,
	}

	tlsCfg, fp, err := cfg.loadCustomTLS()
	if err != nil {
		t.Fatalf("加载 custom TLS 失败: %v", err)
	}
	if tlsCfg == nil {
		t.Fatal("tlsConfig 不应为 nil")
	}
	if fp == "" {
		t.Fatal("指纹不应为空")
	}
	if tlsCfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion 应为 TLS 1.2")
	}
}
