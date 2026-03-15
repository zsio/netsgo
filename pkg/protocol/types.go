package protocol

// ClientInfo 描述一个 Client 的基本信息，在认证时发送给 Server
type ClientInfo struct {
	Hostname string `json:"hostname"` // 主机名
	OS       string `json:"os"`       // 操作系统 (windows/linux/darwin)
	Arch     string `json:"arch"`     // CPU 架构 (amd64/arm64)
	IP       string `json:"ip"`       // Client 本地 IP 地址
	Version  string `json:"version"`  // Client 版本号
}

// DiskPartition 描述单个磁盘分区的使用情况
type DiskPartition struct {
	Path  string `json:"path"`
	Used  uint64 `json:"used"`
	Total uint64 `json:"total"`
}

// SystemStats 描述一台机器的实时系统状态，由 Client 探针定时采集并上报
type SystemStats struct {
	CPUUsage       float64         `json:"cpu_usage"`       // CPU 使用率 (0-100)
	MemTotal       uint64          `json:"mem_total"`       // 总内存 (bytes)
	MemUsed        uint64          `json:"mem_used"`        // 已用内存 (bytes)
	MemUsage       float64         `json:"mem_usage"`       // 内存使用率 (0-100)
	DiskTotal      uint64          `json:"disk_total"`      // 磁盘总容量 (bytes) — 所有分区聚合
	DiskUsed       uint64          `json:"disk_used"`       // 磁盘已用 (bytes) — 所有分区聚合
	DiskUsage      float64         `json:"disk_usage"`      // 磁盘使用率 (0-100) — 聚合百分比
	DiskPartitions []DiskPartition `json:"disk_partitions"` // 各分区明细
	NetSent        uint64          `json:"net_sent"`        // 网络发送字节数（累计）
	NetRecv        uint64          `json:"net_recv"`        // 网络接收字节数（累计）
	NetSentSpeed   float64         `json:"net_sent_speed"`  // 发送速率 (bytes/s)，服务端计算
	NetRecvSpeed   float64         `json:"net_recv_speed"`  // 接收速率 (bytes/s)，服务端计算
	Uptime         uint64          `json:"uptime"`          // 系统运行时间 (秒)
	NumCPU         int             `json:"num_cpu"`         // CPU 核心数
}

// ProxyConfig 代理隧道的完整配置
type ProxyConfig struct {
	Name       string `json:"name"`            // 隧道名称（唯一标识）
	Type       string `json:"type"`            // 隧道类型: tcp, udp, http
	LocalIP    string `json:"local_ip"`        // 内网目标服务 IP
	LocalPort  int    `json:"local_port"`      // 内网目标服务端口
	RemotePort int    `json:"remote_port"`     // 公网暴露端口
	Domain     string `json:"domain"`          // HTTP 类型时的域名
	ClientID   string `json:"client_id"`       // 所属 Client ID
	Status     string `json:"status"`          // 状态: active, paused, stopped, error
	Error      string `json:"error,omitempty"` // 错误状态时的具体原因
}

// ToProxyNewRequest 将 ProxyConfig 转换为 ProxyNewRequest（用于发送给 Client）
func (c ProxyConfig) ToProxyNewRequest() ProxyNewRequest {
	return ProxyNewRequest{
		Name:       c.Name,
		Type:       c.Type,
		LocalIP:    c.LocalIP,
		LocalPort:  c.LocalPort,
		RemotePort: c.RemotePort,
		Domain:     c.Domain,
	}
}

// 代理隧道类型常量
const (
	ProxyTypeTCP  = "tcp"
	ProxyTypeUDP  = "udp"
	ProxyTypeHTTP = "http"
)

// 代理隧道状态常量
const (
	ProxyStatusActive  = "active"
	ProxyStatusPaused  = "paused"
	ProxyStatusStopped = "stopped"
	ProxyStatusError   = "error"
)

// 数据通道握手魔数 — Client 连接 Server 数据通道时，首字节发送此值以区分 HTTP 流量
const DataChannelMagic byte = 0x4E // 'N' for NetsGo

// 数据通道握手状态码
const (
	DataHandshakeOK       byte = 0x00
	DataHandshakeFail     byte = 0x01
	DataHandshakeAuthFail byte = 0x02
)

// StreamHeader 每个 yamux stream 开头发送的头部
// Server 打开 stream 后写入此头部，告诉 Client 这个 stream 属于哪条代理隧道
type StreamHeader struct {
	ProxyName string `json:"proxy_name"`
}
