package netutil

import (
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

var publicIPProbeURLs = map[int][]string{
	4: {
		"https://netsgo.zs.uy/ip",
		"https://api.ipify.org",
		"https://ipv4.icanhazip.com",
		"https://4.ipw.cn",
	},
	6: {
		"https://netsgo.zs.uy/ip",
		"https://api6.ipify.org",
		"https://ipv6.icanhazip.com",
		"https://6.ipw.cn",
	},
}

// GetOutboundIP returns the best local LAN-style IPv4 address for display.
// Route-based detection is only a fallback because VPN/tunnel interfaces can
// otherwise be mistaken for the machine's normal intranet address.
func GetOutboundIP() string {
	if ip := preferredInterfaceIPv4(); ip != "" {
		return ip
	}

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

	go func() { ch <- result{fetchIPFromURLs(publicIPProbeURLs[4], 4), 4} }()
	go func() { ch <- result{fetchIPFromURLs(publicIPProbeURLs[6], 6), 6} }()

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

func preferredInterfaceIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	var fallback string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 ||
			iface.Flags&net.FlagLoopback != 0 ||
			iface.Flags&net.FlagPointToPoint != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip := ipFromAddr(addr).To4()
			if ip == nil || !ip.IsGlobalUnicast() || ip.IsLinkLocalUnicast() {
				continue
			}
			if ip.IsPrivate() {
				return ip.String()
			}
			if fallback == "" {
				fallback = ip.String()
			}
		}
	}

	return fallback
}

func ipFromAddr(addr net.Addr) net.IP {
	switch v := addr.(type) {
	case *net.IPNet:
		return v.IP
	case *net.IPAddr:
		return v.IP
	default:
		return nil
	}
}

func fetchIPFromURLs(urls []string, version int) string {
	client := &http.Client{Timeout: 2 * time.Second}
	for _, url := range urls {
		if ip := fetchIPFromURL(client, url, version); ip != "" {
			return ip
		}
	}
	return ""
}

// fetchIPFromURL 从指定 URL 获取 IP 地址（纯文本响应）
func fetchIPFromURL(client *http.Client, url string, version int) string {
	resp, err := client.Get(url)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return ""
	}
	return parseIPResponse(strings.TrimSpace(string(body)), version)
}

func parseIPResponse(raw string, version int) string {
	ip := net.ParseIP(raw)
	if ip == nil {
		return ""
	}

	if version == 4 {
		ip4 := ip.To4()
		if ip4 == nil {
			return ""
		}
		return ip4.String()
	}

	if version == 6 && ip.To4() == nil {
		return ip.String()
	}

	return ""
}
