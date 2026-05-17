package protocol

import "encoding/json"

// 消息类型常量 — 控制通道上传输的所有消息类型
const (
	MsgTypeAuth        = "auth"         // Client → Server: 认证请求
	MsgTypeAuthResp    = "auth_resp"    // Server → Client: 认证响应
	MsgTypePing        = "ping"         // Client → Server: 心跳
	MsgTypePong        = "pong"         // Server → Client: 心跳回复
	MsgTypeProbeReport = "probe_report" // Client → Server: 探针数据上报
)

// Tunnel control messages. The wire protocol uses the unified tunnel_*
// names; the MsgTypeProxy* identifiers remain source-compatible aliases while
// the codebase is being cut over.
const (
	MsgTypeTunnelCreate         = "tunnel_create"
	MsgTypeTunnelCreateResp     = "tunnel_create_resp"
	MsgTypeTunnelProvision      = "tunnel_provision"
	MsgTypeTunnelProvisionAck   = "tunnel_provision_ack"
	MsgTypeTunnelUnprovision    = "tunnel_unprovision"
	MsgTypeTunnelRuntimeReport  = "tunnel_runtime_report"
	MsgTypeTunnelStreamClose    = "tunnel_stream_close"
	MsgTypeP2PSessionPrepare    = "p2p_session_prepare"
	MsgTypeP2PSessionReady      = "p2p_session_ready"
	MsgTypeP2PCandidate         = "p2p_candidate"
	MsgTypeP2PConnectivityCheck = "p2p_connectivity_check"
	MsgTypeP2PSelected          = "p2p_selected"
	MsgTypeP2PFailed            = "p2p_failed"
	MsgTypeP2PClosed            = "p2p_closed"
	MsgTypeP2PStatsReport       = "p2p_stats_report"
	MsgTypeTrafficReport        = "traffic_report"

	MsgTypeProxyCreate       = MsgTypeTunnelCreate
	MsgTypeProxyCreateResp   = MsgTypeTunnelCreateResp
	MsgTypeProxyProvision    = MsgTypeTunnelProvision
	MsgTypeProxyProvisionAck = MsgTypeTunnelProvisionAck
	MsgTypeProxyClose        = MsgTypeTunnelUnprovision
)

const (
	AuthCodeOK                  = "ok"
	AuthCodeInvalidToken        = "invalid_token"
	AuthCodeRevokedToken        = "revoked_token"
	AuthCodeInvalidKey          = "invalid_key"
	AuthCodeConcurrentSession   = "concurrent_session"
	AuthCodeRateLimited         = "rate_limited"
	AuthCodeServerUninitialized = "server_uninitialized"
)

// Message 是控制通道上传输的统一消息结构
// 所有控制消息都通过这个结构体封装，Type 标识消息类型，Payload 携带具体数据
type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NewMessage 创建一个新消息，自动将 payload 序列化为 JSON
func NewMessage(msgType string, payload any) (*Message, error) {
	var raw json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		raw = data
	}
	return &Message{Type: msgType, Payload: raw}, nil
}

// ParsePayload 将消息的 Payload 反序列化到目标结构体
func (m *Message) ParsePayload(target any) error {
	return json.Unmarshal(m.Payload, target)
}

// --- 各类消息的 Payload 结构体 ---

// AuthRequest Client 连接时发送的认证请求
type AuthRequest struct {
	Key       string     `json:"key"`             // 认证密钥（用于兑换 Token）
	Token     string     `json:"token,omitempty"` // 客户端连接密钥（优先使用）
	InstallID string     `json:"install_id"`      // Client 稳定安装 ID
	Client    ClientInfo `json:"client"`          // Client 基本信息
}

// AuthResponse Server 返回的认证结果
type AuthResponse struct {
	Success    bool   `json:"success"`
	Message    string `json:"message,omitempty"`
	ClientID   string `json:"client_id,omitempty"` // Server 分配的唯一 ID
	Token      string `json:"token,omitempty"`     // 服务端下发的新 Token（仅兑换时）
	DataToken  string `json:"data_token,omitempty"`
	Code       string `json:"code,omitempty"`
	Retryable  bool   `json:"retryable,omitempty"`
	ClearToken bool   `json:"clear_token,omitempty"`
}

// TunnelCreateRequest requests creation of a unified tunnel.
type TunnelCreateRequest = TunnelSpec

// TunnelProvisionRequest is sent by the server to an ingress or target client.
type TunnelProvisionRequest struct {
	TunnelID string     `json:"tunnel_id"`
	Revision int64      `json:"revision"`
	Role     string     `json:"role"`
	Spec     TunnelSpec `json:"spec"`
}

// TunnelCreateResponse is returned for unified tunnel creation.
type TunnelCreateResponse struct {
	TunnelID string     `json:"tunnel_id,omitempty"`
	Success  bool       `json:"success"`
	Message  string     `json:"message,omitempty"`
	Spec     TunnelSpec `json:"spec,omitempty"`
}

// TunnelProvisionAck acknowledges a tunnel provisioning request.
type TunnelProvisionAck struct {
	TunnelID string `json:"tunnel_id"`
	Revision int64  `json:"revision"`
	Role     string `json:"role"`
	Accepted bool   `json:"accepted"`
	Message  string `json:"message,omitempty"`
}

// TunnelUnprovisionRequest asks a participant to stop a tunnel revision.
type TunnelUnprovisionRequest struct {
	TunnelID string `json:"tunnel_id"`
	Revision int64  `json:"revision"`
	Role     string `json:"role,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// TunnelRuntimeReport reports participant/runtime state changes.
type TunnelRuntimeReport struct {
	TunnelID    string             `json:"tunnel_id"`
	Revision    int64              `json:"revision"`
	Role        string             `json:"role,omitempty"`
	Participant ParticipantRuntime `json:"participant,omitempty"`
	Transport   TransportRuntime   `json:"transport,omitempty"`
	Message     string             `json:"message,omitempty"`
}

// ProxyNewRequest 请求创建一条新的代理隧道
type ProxyNewRequest struct {
	ID                string `json:"id,omitempty"` // 隧道稳定 ID（管理面唯一标识）
	Name              string `json:"name"`         // 隧道名称
	Type              string `json:"type"`         // tcp / udp / http
	LocalIP           string `json:"local_ip"`     // 内网目标 IP
	LocalPort         int    `json:"local_port"`   // 内网目标端口
	RemotePort        int    `json:"remote_port"`  // 公网暴露端口（TCP/UDP 类型时使用）
	Domain            string `json:"domain"`       // 域名（HTTP 类型时使用）
	TransportPolicy   string `json:"transport_policy,omitempty"`
	ActualTransport   string `json:"actual_transport,omitempty"`
	ProvisionRevision uint64 `json:"provision_revision,omitempty"` // Server 生成的 provisioning 修订号；ACK 必须原样返回
	BandwidthSettings        // payload bytes/sec; 0 = unlimited
}

// ProxyCreateRequest 表示 client 主动请求 server 创建 tunnel 的消息体。
type ProxyCreateRequest = ProxyNewRequest

// ProxyProvisionRequest 表示 server 下发给 client 的 provisioning 配置消息体。
type ProxyProvisionRequest = ProxyNewRequest

// ProxyCreateResponse 表示 client 主动创建 tunnel 时 server 返回的结果。
type ProxyCreateResponse struct {
	ID                string `json:"id,omitempty"`
	Name              string `json:"name,omitempty"`
	Success           bool   `json:"success"`
	Message           string `json:"message,omitempty"`
	RemotePort        int    `json:"remote_port,omitempty"` // 实际分配的公网端口
	TransportPolicy   string `json:"transport_policy,omitempty"`
	ActualTransport   string `json:"actual_transport,omitempty"`
	ProvisionRevision uint64 `json:"provision_revision,omitempty"`
}

// ProxyProvisionAck 表示 client 接收 provisioning 配置后的 ACK。
type ProxyProvisionAck struct {
	Name              string `json:"name,omitempty"`
	ProvisionRevision uint64 `json:"provision_revision,omitempty"`
	Accepted          bool   `json:"accepted"`
	Message           string `json:"message,omitempty"`
}

// ProxyCloseRequest 关闭某条代理隧道
type ProxyCloseRequest struct {
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
}
