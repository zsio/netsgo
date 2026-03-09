package protocol

import "encoding/json"

// 消息类型常量 — 控制通道上传输的所有消息类型
const (
	MsgTypeAuth         = "auth"           // Agent → Server: 认证请求
	MsgTypeAuthResp     = "auth_resp"      // Server → Agent: 认证响应
	MsgTypePing         = "ping"           // Agent → Server: 心跳
	MsgTypePong         = "pong"           // Server → Agent: 心跳回复
	MsgTypeProbeReport  = "probe_report"   // Agent → Server: 探针数据上报
	MsgTypeProxyNew     = "proxy_new"      // Agent/Server: 请求创建代理隧道
	MsgTypeProxyNewResp = "proxy_new_resp" // Server → Agent: 创建代理响应
	MsgTypeProxyClose   = "proxy_close"    // 双向: 关闭某条代理隧道
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

// AuthRequest Agent 连接时发送的认证请求
type AuthRequest struct {
	Token    string    `json:"token"`     // 认证令牌
	Agent    AgentInfo `json:"agent"`     // Agent 基本信息
}

// AuthResponse Server 返回的认证结果
type AuthResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	AgentID string `json:"agent_id,omitempty"` // Server 分配的唯一 ID
}

// ProxyNewRequest 请求创建一条新的代理隧道
type ProxyNewRequest struct {
	Name       string `json:"name"`        // 隧道名称
	Type       string `json:"type"`        // tcp / udp / http
	LocalIP    string `json:"local_ip"`    // 内网目标 IP
	LocalPort  int    `json:"local_port"`  // 内网目标端口
	RemotePort int    `json:"remote_port"` // 公网暴露端口（TCP/UDP 类型时使用）
	Domain     string `json:"domain"`      // 域名（HTTP 类型时使用）
}

// ProxyNewResponse 代理隧道创建结果
type ProxyNewResponse struct {
	Success    bool   `json:"success"`
	Message    string `json:"message,omitempty"`
	RemotePort int    `json:"remote_port,omitempty"` // 实际分配的公网端口
}

// ProxyCloseRequest 关闭某条代理隧道
type ProxyCloseRequest struct {
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
}
