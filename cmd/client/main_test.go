package main

import (
	"testing"
)

func TestParseProxyConfigs_ThreeParts(t *testing.T) {
	configs, err := parseProxyConfigs("tcp:3306:13306")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("期望 1 条配置，得到 %d", len(configs))
	}
	c := configs[0]
	if c.Type != "tcp" {
		t.Errorf("Type 期望 tcp，得到 %s", c.Type)
	}
	if c.LocalIP != "127.0.0.1" {
		t.Errorf("LocalIP 期望 127.0.0.1，得到 %s", c.LocalIP)
	}
	if c.LocalPort != 3306 {
		t.Errorf("LocalPort 期望 3306，得到 %d", c.LocalPort)
	}
	if c.RemotePort != 13306 {
		t.Errorf("RemotePort 期望 13306，得到 %d", c.RemotePort)
	}
	if c.Name == "" {
		t.Error("Name 不应为空")
	}
}

func TestParseProxyConfigs_FourParts(t *testing.T) {
	configs, err := parseProxyConfigs("tcp:192.168.1.1:3306:13306")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("期望 1 条配置，得到 %d", len(configs))
	}
	c := configs[0]
	if c.LocalIP != "192.168.1.1" {
		t.Errorf("LocalIP 期望 192.168.1.1，得到 %s", c.LocalIP)
	}
	if c.LocalPort != 3306 {
		t.Errorf("LocalPort 期望 3306，得到 %d", c.LocalPort)
	}
}

func TestParseProxyConfigs_Multiple(t *testing.T) {
	configs, err := parseProxyConfigs("tcp:3306:13306, tcp:8080:18080")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("期望 2 条配置，得到 %d", len(configs))
	}
	if configs[0].LocalPort != 3306 {
		t.Errorf("第 1 条 LocalPort 期望 3306，得到 %d", configs[0].LocalPort)
	}
	if configs[1].LocalPort != 8080 {
		t.Errorf("第 2 条 LocalPort 期望 8080，得到 %d", configs[1].LocalPort)
	}
}

func TestParseProxyConfigs_EmptyString(t *testing.T) {
	configs, err := parseProxyConfigs("")
	if err != nil {
		t.Fatalf("空字符串不应报错: %v", err)
	}
	if len(configs) != 0 {
		t.Errorf("空字符串应返回空切片，得到 %d 条", len(configs))
	}
}

func TestParseProxyConfigs_InvalidPort(t *testing.T) {
	_, err := parseProxyConfigs("tcp:abc:123")
	if err == nil {
		t.Error("非法端口应返回错误")
	}
}

func TestParseProxyConfigs_InvalidRemotePort(t *testing.T) {
	_, err := parseProxyConfigs("tcp:3306:xyz")
	if err == nil {
		t.Error("非法公网端口应返回错误")
	}
}

func TestParseProxyConfigs_InvalidFormat(t *testing.T) {
	_, err := parseProxyConfigs("tcp:123")
	if err == nil {
		t.Error("段数不足应返回错误")
	}
}

func TestParseProxyConfigs_FourParts_InvalidLocalPort(t *testing.T) {
	_, err := parseProxyConfigs("tcp:192.168.1.1:abc:13306")
	if err == nil {
		t.Error("4段格式下非法本地端口应返回错误")
	}
}

func TestParseProxyConfigs_FourParts_InvalidRemotePort(t *testing.T) {
	_, err := parseProxyConfigs("tcp:192.168.1.1:3306:xyz")
	if err == nil {
		t.Error("4段格式下非法公网端口应返回错误")
	}
}

func TestParseProxyConfigs_TooManyParts(t *testing.T) {
	_, err := parseProxyConfigs("tcp:1:2:3:4")
	if err == nil {
		t.Error("5段格式应返回错误")
	}
}

func TestParseProxyConfigs_TrailingComma(t *testing.T) {
	configs, err := parseProxyConfigs("tcp:3306:13306,")
	if err != nil {
		t.Fatalf("尾部逗号不应报错: %v", err)
	}
	if len(configs) != 1 {
		t.Errorf("期望 1 条配置，得到 %d", len(configs))
	}
}
