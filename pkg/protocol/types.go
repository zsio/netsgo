package protocol

// AgentInfo 描述一个 Agent 的基本信息，在认证时发送给 Server
type AgentInfo struct {
	Hostname string `json:"hostname"` // 主机名
	OS       string `json:"os"`       // 操作系统 (windows/linux/darwin)
	Arch     string `json:"arch"`     // CPU 架构 (amd64/arm64)
	IP       string `json:"ip"`       // Agent 本地 IP 地址
	Version  string `json:"version"`  // Agent 版本号
}

// SystemStats 描述一台机器的实时系统状态，由 Agent 探针定时采集并上报
type SystemStats struct {
	CPUUsage    float64 `json:"cpu_usage"`    // CPU 使用率 (0-100)
	MemTotal    uint64  `json:"mem_total"`    // 总内存 (bytes)
	MemUsed     uint64  `json:"mem_used"`     // 已用内存 (bytes)
	MemUsage    float64 `json:"mem_usage"`    // 内存使用率 (0-100)
	DiskTotal   uint64  `json:"disk_total"`   // 磁盘总容量 (bytes)
	DiskUsed    uint64  `json:"disk_used"`    // 磁盘已用 (bytes)
	DiskUsage   float64 `json:"disk_usage"`   // 磁盘使用率 (0-100)
	NetSent     uint64  `json:"net_sent"`     // 网络发送字节数（累计）
	NetRecv     uint64  `json:"net_recv"`     // 网络接收字节数（累计）
	Uptime      uint64  `json:"uptime"`       // 系统运行时间 (秒)
	NumCPU      int     `json:"num_cpu"`      // CPU 核心数
}

// ProxyConfig 代理隧道的完整配置
type ProxyConfig struct {
	Name       string `json:"name"`        // 隧道名称（唯一标识）
	Type       string `json:"type"`        // 隧道类型: tcp, udp, http
	LocalIP    string `json:"local_ip"`    // 内网目标服务 IP
	LocalPort  int    `json:"local_port"`  // 内网目标服务端口
	RemotePort int    `json:"remote_port"` // 公网暴露端口
	Domain     string `json:"domain"`      // HTTP 类型时的域名
	AgentID    string `json:"agent_id"`    // 所属 Agent ID
	Status     string `json:"status"`      // 状态: active, stopped, error
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
	ProxyStatusStopped = "stopped"
	ProxyStatusError   = "error"
)

// 数据通道握手魔数 — Agent 连接 Server 数据通道时，首字节发送此值以区分 HTTP 流量
const DataChannelMagic byte = 0x4E // 'N' for NetsGo

// 数据通道握手状态码
const (
	DataHandshakeOK   byte = 0x00
	DataHandshakeFail byte = 0x01
)

// StreamHeader 每个 yamux stream 开头发送的头部
// Server 打开 stream 后写入此头部，告诉 Agent 这个 stream 属于哪条代理隧道
type StreamHeader struct {
	ProxyName string `json:"proxy_name"`
}
