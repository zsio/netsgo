// NetsGo TypeScript Types
// Aligned with pkg/protocol/types.go

// --- Agent ---

/** 对齐 protocol.AgentInfo */
export interface AgentInfo {
  hostname: string;
  os: "windows" | "linux" | "darwin";
  arch: "amd64" | "arm64";
  ip: string;
  version: string;
}

/** 对齐 protocol.SystemStats */
export interface SystemStats {
  cpu_usage: number;      // 0-100
  mem_total: number;      // bytes
  mem_used: number;       // bytes
  mem_usage: number;      // 0-100
  disk_total: number;     // bytes
  disk_used: number;      // bytes
  disk_usage: number;     // 0-100
  net_sent: number;       // bytes (cumulative)
  net_recv: number;       // bytes (cumulative)
  uptime: number;         // seconds
  num_cpu: number;
}

/** 对齐 /api/agents 响应中的 agentView (server.go handleAPIAgents) */
export interface Agent {
  id: string;
  info: AgentInfo;
  stats: SystemStats | null;
  proxies?: ProxyConfig[];
}

// --- Tunnel / Proxy ---

export type ProxyType = "tcp" | "udp" | "http";
export type ProxyStatus = "active" | "paused" | "stopped" | "error";

/** 对齐 protocol.ProxyConfig */
export interface ProxyConfig {
  name: string;
  type: ProxyType;
  local_ip: string;
  local_port: number;
  remote_port: number;
  domain: string;
  agent_id: string;
  status: ProxyStatus;
}

/** 创建隧道请求体 */
export interface CreateTunnelInput {
  agentId: string;
  name: string;
  type: ProxyType;
  local_ip: string;
  local_port: number;
  remote_port?: number;
  domain?: string;
}

// --- SSE Events ---

export interface StatsUpdateEvent {
  agent_id: string;
  stats: SystemStats;
}

export interface AgentOnlineEvent {
  agent_id: string;
  info: AgentInfo;
}

export interface AgentOfflineEvent {
  agent_id: string;
}

export interface TunnelChangedEvent {
  agent_id: string;
  tunnel: ProxyConfig;
}

// --- API ---

export interface ServerStatus {
  status: string;
  agent_count: number;
  version: string;
  listen_port: number;
  uptime: number;         // seconds
  store_path: string;
  tunnel_active: number;
  tunnel_paused: number;
  tunnel_stopped: number;
}

// --- Admin System ---

export interface APIKey {
  id: string;
  name: string;
  permissions: string[];
  created_at: string;
  expires_at?: string;
  is_active: boolean;
}

export interface AdminUser {
  id: string;
  username: string;
  role: string;
  created_at: string;
  last_login?: string;
}

export interface TunnelPolicy {
  min_port: number;
  max_port: number;
  blocked_ports: number[];
  agent_whitelist: string[];
}

export interface SystemLogEntry {
  id: string;
  timestamp: string;
  level: string;
  message: string;
  source: string;
}

export interface EventRecord {
  id: string;
  timestamp: string;
  type: string;
  data: string;
}

export interface LoginResponse {
  token: string;
  user: {
    id: string;
    username: string;
    role: string;
  };
}
