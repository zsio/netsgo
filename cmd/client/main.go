package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	"netsgo/internal/client"
	"netsgo/pkg/protocol"
)

func main() {
	serverAddr := flag.String("server", "ws://localhost:8080", "Server 的 WebSocket 地址")
	token := flag.String("token", "", "认证令牌")
	proxy := flag.String("proxy", "", "代理配置，如 tcp:3306:13306 表示将本地 3306 映射到公网 13306，可用逗号分隔多条")
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	c := client.New(*serverAddr, *token)

	// 解析代理配置
	if *proxy != "" {
		configs, err := parseProxyConfigs(*proxy)
		if err != nil {
			log.Fatalf("❌ 代理配置格式错误: %v", err)
		}
		c.ProxyConfigs = configs
	}

	if err := c.Start(); err != nil {
		log.Fatalf("❌ 客户端启动失败: %v", err)
	}
}

// parseProxyConfigs 解析代理配置字符串
// 格式: "tcp:本地端口:公网端口" 或 "tcp:本地IP:本地端口:公网端口"
// 多条配置用逗号分隔
func parseProxyConfigs(input string) ([]protocol.ProxyNewRequest, error) {
	var configs []protocol.ProxyNewRequest

	for _, item := range strings.Split(input, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		parts := strings.Split(item, ":")
		var req protocol.ProxyNewRequest

		switch len(parts) {
		case 3:
			// tcp:localPort:remotePort
			req.Type = parts[0]
			req.LocalIP = "127.0.0.1"
			var localPort, remotePort int
			if _, err := fmt.Sscanf(parts[1], "%d", &localPort); err != nil {
				return nil, fmt.Errorf("无效的本地端口 %q: %w", parts[1], err)
			}
			if _, err := fmt.Sscanf(parts[2], "%d", &remotePort); err != nil {
				return nil, fmt.Errorf("无效的公网端口 %q: %w", parts[2], err)
			}
			req.LocalPort = localPort
			req.RemotePort = remotePort
			req.Name = fmt.Sprintf("%s_%d_%d", req.Type, localPort, remotePort)
		case 4:
			// tcp:localIP:localPort:remotePort
			req.Type = parts[0]
			req.LocalIP = parts[1]
			var localPort, remotePort int
			if _, err := fmt.Sscanf(parts[2], "%d", &localPort); err != nil {
				return nil, fmt.Errorf("无效的本地端口 %q: %w", parts[2], err)
			}
			if _, err := fmt.Sscanf(parts[3], "%d", &remotePort); err != nil {
				return nil, fmt.Errorf("无效的公网端口 %q: %w", parts[3], err)
			}
			req.LocalPort = localPort
			req.RemotePort = remotePort
			req.Name = fmt.Sprintf("%s_%d_%d", req.Type, localPort, remotePort)
		default:
			return nil, fmt.Errorf("无效的配置格式 %q, 应为 type:localPort:remotePort", item)
		}

		configs = append(configs, req)
	}

	return configs, nil
}
