package netutil

import (
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// GetOutboundIP 获取本机出站 IP 地址
// 通过 UDP dial 一个公网地址（不实际发送数据），获取本地使用的网络接口 IP
func GetOutboundIP() string {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

// FetchPublicIPs 并发获取公网 IPv4 和 IPv6 地址
func FetchPublicIPs() (ipv4, ipv6 string) {
	type result struct {
		ip  string
		ver int // 4 or 6
	}
	ch := make(chan result, 2)

	go func() { ch <- result{fetchIPFromURL("https://4.ipw.cn"), 4} }()
	go func() { ch <- result{fetchIPFromURL("https://6.ipw.cn"), 6} }()

	for i := 0; i < 2; i++ {
		r := <-ch
		if r.ver == 4 {
			ipv4 = r.ip
		} else {
			ipv6 = r.ip
		}
	}
	return
}

// fetchIPFromURL 从指定 URL 获取 IP 地址（纯文本响应）
func fetchIPFromURL(url string) string {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}
