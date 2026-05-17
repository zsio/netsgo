package protocol

import (
	"encoding/json"
	"time"
)

// Unified tunnel enum values. The tunnel architecture is modeled as
// TunnelSpec = Ingress + Target + Transport. Future endpoint examples such as
// unix_socket/static_file/serial_device are intentionally not enum values here.
const (
	TunnelSpecVersion = 1

	TunnelTopologyServerExpose   = "server_expose"
	TunnelTopologyClientToClient = "client_to_client"

	EndpointLocationServer = "server"
	EndpointLocationClient = "client"

	IngressTypeTCPListen = "tcp_listen"
	IngressTypeUDPListen = "udp_listen"
	IngressTypeHTTPHost  = "http_host"

	TargetTypeTCPService = "tcp_service"
	TargetTypeUDPService = "udp_service"

	TransportPolicyServerRelayOnly = "server_relay_only"
	TransportPolicyDirectPreferred = "direct_preferred"
	TransportPolicyDirectOnly      = "direct_only"

	ActualTransportUnknown     = "unknown"
	ActualTransportServerRelay = "server_relay"
	ActualTransportPeerDirect  = "peer_direct"
	ActualTransportTURNRelay   = "turn_relay"

	TunnelDesiredStateRunning = "running"
	TunnelDesiredStateStopped = "stopped"

	TunnelRuntimeStatePending = "pending"
	TunnelRuntimeStateActive  = "active"
	TunnelRuntimeStateOffline = "offline"
	TunnelRuntimeStateIdle    = "idle"
	TunnelRuntimeStateError   = "error"

	ParticipantStateUnknown           = "unknown"
	ParticipantStateOffline           = "offline"
	ParticipantStateProvisionPending  = "provision_pending"
	ParticipantStateProvisionRejected = "provision_rejected"
	ParticipantStateReady             = "ready"
	ParticipantStateListening         = "listening"
	ParticipantStateListenFailed      = "listen_failed"
	ParticipantStateTargetReady       = "target_ready"
	ParticipantStateTargetFailed      = "target_failed"

	P2PStateIdle      = "idle"
	P2PStateGathering = "gathering"
	P2PStateChecking  = "checking"
	P2PStateConnected = "connected"
	P2PStateFailed    = "failed"
	P2PStateFallback  = "fallback"
	P2PStateClosed    = "closed"

	P2PImplWebRTCICE = "webrtc_ice"
)

// EndpointSpec describes where traffic enters or exits a tunnel.
type EndpointSpec struct {
	Location string          `json:"location"`
	ClientID string          `json:"client_id,omitempty"`
	Type     string          `json:"type"`
	Config   json.RawMessage `json:"config"`
}

// P2PState describes the currently selected peer-direct session state.
type P2PState struct {
	State     string `json:"state"`
	Error     string `json:"error,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// ParticipantRuntime records the runtime state for one tunnel participant.
type ParticipantRuntime struct {
	ClientID string `json:"client_id"`
	Role     string `json:"role"`
	State    string `json:"state"`
	Revision int64  `json:"revision"`
	Error    string `json:"error,omitempty"`
}

// TunnelParticipants groups ingress and target participant runtime state.
type TunnelParticipants struct {
	Ingress ParticipantRuntime `json:"ingress"`
	Target  ParticipantRuntime `json:"target"`
}

// TransportRuntime describes the effective transport path for a tunnel.
type TransportRuntime struct {
	Policy          string    `json:"policy"`
	Actual          string    `json:"actual"`
	P2PState        string    `json:"p2p_state,omitempty"`
	P2PError        string    `json:"p2p_error,omitempty"`
	FallbackSince   time.Time `json:"fallback_since,omitempty"`
	LastDirectOK    time.Time `json:"last_direct_ok,omitempty"`
	LastDirectError string    `json:"last_direct_error,omitempty"`
}

// TunnelSpec is the canonical tunnel payload used by API, protocol, storage,
// runtime and events.
type TunnelSpec struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision int64  `json:"revision"`

	Topology      string `json:"topology"`
	OwnerClientID string `json:"owner_client_id"`

	Ingress EndpointSpec `json:"ingress"`
	Target  EndpointSpec `json:"target"`

	TransportPolicy string `json:"transport_policy"`
	ActualTransport string `json:"actual_transport"`

	P2P P2PState `json:"p2p"`

	DesiredState string `json:"desired_state"`
	RuntimeState string `json:"runtime_state"`
	Error        string `json:"error,omitempty"`

	Participants TunnelParticipants `json:"participants,omitempty"`
	Transport    TransportRuntime   `json:"transport,omitempty"`

	BandwidthSettings BandwidthSettings `json:"bandwidth_settings"`

	CreatedByUserID string              `json:"created_by_user_id,omitempty"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
	Capabilities    *TunnelCapabilities `json:"capabilities,omitempty"`
}

// ClientTunnelRole selects the relationship used for client-scoped tunnel lists.
const (
	ClientTunnelRoleOwner   = "owner"
	ClientTunnelRoleIngress = "ingress"
	ClientTunnelRoleTarget  = "target"
	ClientTunnelRoleRelated = "related"
)

// P2PCapabilities describes a client's peer-direct implementation.
type P2PCapabilities struct {
	Supported    bool   `json:"supported"`
	Impl         string `json:"impl,omitempty"`
	SupportsIPv6 bool   `json:"supports_ipv6,omitempty"`
	SupportsTURN bool   `json:"supports_turn,omitempty"`
}

// ClientCapabilities is reported during auth and persisted as the latest known
// tunnel capability set for offline validation.
type ClientCapabilities struct {
	ProtocolVersion     int             `json:"protocol_version"`
	StreamHeaderVersion int             `json:"stream_header_version"`
	TunnelSpecVersion   int             `json:"tunnel_spec_version"`
	IngressTypes        []string        `json:"ingress_types"`
	TargetTypes         []string        `json:"target_types"`
	TransportPolicies   []string        `json:"transport_policies"`
	P2P                 P2PCapabilities `json:"p2p"`
}

// DefaultClientCapabilities returns the capabilities implemented by this client
// binary without advertising future-only endpoint types.
func DefaultClientCapabilities() ClientCapabilities {
	return ClientCapabilities{
		ProtocolVersion:     1,
		StreamHeaderVersion: 1,
		TunnelSpecVersion:   TunnelSpecVersion,
		IngressTypes:        []string{IngressTypeTCPListen, IngressTypeUDPListen},
		TargetTypes:         []string{TargetTypeTCPService, TargetTypeUDPService},
		TransportPolicies:   []string{TransportPolicyServerRelayOnly},
		P2P:                 P2PCapabilities{Supported: false},
	}
}

// BandwidthSettings carries directional payload-byte-per-second limits.
// A zero value means unlimited.
type BandwidthSettings struct {
	IngressBPS int64 `json:"ingress_bps"`
	EgressBPS  int64 `json:"egress_bps"`
}

// UpdateCapability describes what kind of local update guidance can be shown.
type UpdateCapability struct {
	InstallMethod string `json:"install_method"`
}

// ClientInfo 描述一个 Client 的基本信息，在认证时发送给 Server
type ClientInfo struct {
	Hostname         string              `json:"hostname"`                    // 主机名
	OS               string              `json:"os"`                          // 操作系统 (windows/linux/darwin)
	Arch             string              `json:"arch"`                        // CPU 架构 (amd64/arm64)
	IP               string              `json:"ip"`                          // Client 本地 IP 地址
	Version          string              `json:"version"`                     // Client 版本号
	UpdateCapability *UpdateCapability   `json:"update_capability,omitempty"` // 更新能力
	Capabilities     *ClientCapabilities `json:"capabilities,omitempty"`      // TunnelSpec 能力
	PublicIPv4       string              `json:"public_ipv4,omitempty"`       // 公网 IPv4
	PublicIPv6       string              `json:"public_ipv6,omitempty"`       // 公网 IPv6
}

// DiskPartition 描述单个磁盘分区的使用情况
type DiskPartition struct {
	Path  string `json:"path"`
	Used  uint64 `json:"used"`
	Total uint64 `json:"total"`
}

// SystemStats 描述一台机器的实时系统状态，由 Client 探针定时采集并上报
type SystemStats struct {
	CPUUsage       float64         `json:"cpu_usage"`                 // CPU 使用率 (0-100)
	MemTotal       uint64          `json:"mem_total"`                 // 总内存 (bytes)
	MemUsed        uint64          `json:"mem_used"`                  // 已用内存 (bytes)
	MemUsage       float64         `json:"mem_usage"`                 // 内存使用率 (0-100)
	DiskTotal      uint64          `json:"disk_total"`                // 磁盘总容量 (bytes) — 所有分区聚合
	DiskUsed       uint64          `json:"disk_used"`                 // 磁盘已用 (bytes) — 所有分区聚合
	DiskUsage      float64         `json:"disk_usage"`                // 磁盘使用率 (0-100) — 聚合百分比
	DiskPartitions []DiskPartition `json:"disk_partitions"`           // 各分区明细
	NetSent        uint64          `json:"net_sent"`                  // 网络发送字节数（累计）
	NetRecv        uint64          `json:"net_recv"`                  // 网络接收字节数（累计）
	NetSentSpeed   float64         `json:"net_sent_speed"`            // 发送速率 (bytes/s)，服务端计算
	NetRecvSpeed   float64         `json:"net_recv_speed"`            // 接收速率 (bytes/s)，服务端计算
	Uptime         uint64          `json:"uptime"`                    // 系统运行时间 (秒)
	ProcessUptime  uint64          `json:"process_uptime"`            // 程序运行时间 (秒)
	OSInstallTime  uint64          `json:"os_install_time,omitempty"` // 系统安装时间 (Unix 时间戳，秒)
	NumCPU         int             `json:"num_cpu"`                   // CPU 核心数
	AppMemUsed     uint64          `json:"app_mem_used"`              // 程序堆内存 (bytes)
	AppMemSys      uint64          `json:"app_mem_sys"`               // 程序进程占用 (bytes)
	PublicIPv4     string          `json:"public_ipv4,omitempty"`     // 公网 IPv4（探针附带）
	PublicIPv6     string          `json:"public_ipv6,omitempty"`     // 公网 IPv6（探针附带）
	UpdatedAt      time.Time       `json:"updated_at,omitempty"`      // 服务端最近一次接收/整理该状态的时间
	FreshUntil     time.Time       `json:"fresh_until,omitempty"`     // 页面可将该状态视为“新鲜”的截止时间
}

// TunnelCapabilities 描述一条隧道当前允许执行的操作，由服务端计算并注入，仅用于前端展示
type TunnelCapabilities struct {
	CanResume bool `json:"can_resume"` // 是否可以启动
	CanStop   bool `json:"can_stop"`   // 是否可以停止
	CanEdit   bool `json:"can_edit"`   // 是否可以编辑
	CanDelete bool `json:"can_delete"` // 是否可以删除
}

// ProxyConfig 代理隧道的完整配置
type ProxyConfig struct {
	ID                string              `json:"id"`          // 隧道稳定 ID（管理面唯一标识）
	Name              string              `json:"name"`        // 隧道名称（唯一标识）
	Type              string              `json:"type"`        // 隧道类型: tcp, udp, http
	LocalIP           string              `json:"local_ip"`    // 内网目标服务 IP
	LocalPort         int                 `json:"local_port"`  // 内网目标服务端口
	RemotePort        int                 `json:"remote_port"` // 公网暴露端口
	Domain            string              `json:"domain"`      // HTTP 类型时的域名
	ClientID          string              `json:"client_id"`   // 所属 Client ID
	BandwidthSettings                     // 聚合带宽限制（payload bytes/sec，0 = unlimited）
	CreatedAt         time.Time           `json:"created_at"`             // 创建时间
	DesiredState      string              `json:"desired_state"`          // 用户目标状态: running, stopped
	RuntimeState      string              `json:"runtime_state"`          // 实际运行状态: pending, exposed, offline, idle, error
	Error             string              `json:"error,omitempty"`        // 错误状态时的具体原因
	Capabilities      *TunnelCapabilities `json:"capabilities,omitempty"` // 可执行操作（服务端计算，仅供前端使用）
}

// ToProxyNewRequest 将 ProxyConfig 转换为 ProxyNewRequest（用于发送给 Client）
func (c ProxyConfig) ToProxyNewRequest() ProxyNewRequest {
	return ProxyNewRequest{
		ID:                c.ID,
		Name:              c.Name,
		Type:              c.Type,
		LocalIP:           c.LocalIP,
		LocalPort:         c.LocalPort,
		RemotePort:        c.RemotePort,
		Domain:            c.Domain,
		BandwidthSettings: c.BandwidthSettings,
	}
}

// 代理隧道类型常量
const (
	ProxyTypeTCP  = "tcp"
	ProxyTypeUDP  = "udp"
	ProxyTypeHTTP = "http"
)

// Tunnel mutation field constants used by the admin HTTP API.
const (
	TunnelMutationFieldName       = "name"
	TunnelMutationFieldDomain     = "domain"
	TunnelMutationFieldRemotePort = "remote_port"
	TunnelMutationFieldIngressBPS = "ingress_bps"
	TunnelMutationFieldEgressBPS  = "egress_bps"
)

// Tunnel mutation error code constants used by the admin HTTP API.
const (
	TunnelMutationErrorCodeDomainInvalid      = "domain_invalid"
	TunnelMutationErrorCodeServerAddrConflict = "server_addr_conflict"
	TunnelMutationErrorCodeHTTPTunnelConflict = "http_tunnel_conflict"
	TunnelMutationErrorCodeTunnelBusy         = "tunnel_busy"
)

// WebSocket 子协议常量
const (
	WSSubProtocolControl = "netsgo-control.v1"
	WSSubProtocolData    = "netsgo-data.v1"
)

// 兼容旧测试/错误文案使用的 legacy 状态标签常量。
const (
	ProxyStatusPending = "pending"
	ProxyStatusActive  = "active"
	ProxyStatusStopped = "stopped"
	ProxyStatusError   = "error"
)

// 隧道目标状态常量
const (
	ProxyDesiredStateRunning = "running"
	ProxyDesiredStateStopped = "stopped"
)

// 隧道运行状态常量
const (
	ProxyRuntimeStatePending = "pending"
	ProxyRuntimeStateExposed = "exposed"
	ProxyRuntimeStateOffline = "offline"
	ProxyRuntimeStateIdle    = "idle"
	ProxyRuntimeStateError   = "error"
)

// StreamHeader 每个 yamux stream 开头发送的头部
// Server 打开 stream 后写入此头部，告诉 Client 这个 stream 属于哪条代理隧道
type StreamHeader struct {
	ProxyName string `json:"proxy_name"`
}
