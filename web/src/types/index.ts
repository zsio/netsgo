// NetsGo TypeScript Types
// Aligned with pkg/protocol/types.go

// --- Client ---

/** 对齐 protocol.ClientInfo */
export interface ClientInfo {
  hostname: string;
  os: "windows" | "linux" | "darwin";
  arch: "amd64" | "arm64";
  ip: string;
  version: string;
  public_ipv4?: string;
  public_ipv6?: string;
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
  disk_partitions: DiskPartition[];
  net_sent: number;       // bytes (cumulative)
  net_recv: number;       // bytes (cumulative)
  net_sent_speed: number; // bytes/s (server-computed)
  net_recv_speed: number; // bytes/s (server-computed)
  uptime: number;         // seconds (system boot uptime)
  process_uptime: number;  // seconds (NetsGo process uptime)
  os_install_time?: number; // unix timestamp (seconds)
  num_cpu: number;
  app_mem_used: number;   // bytes (Go heap alloc)
  app_mem_sys: number;    // bytes (Go process sys)
}

/** 对齐 /api/clients 响应中的 clientView (server.go handleAPIClients) */
export interface Client {
  id: string;
  display_name?: string;
  info: ClientInfo;
  stats: SystemStats | null;
  proxies?: ProxyConfig[];
  online: boolean;
  last_seen?: string;
  last_ip?: string;
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
  client_id: string;
  status: ProxyStatus;
  error?: string;
}

/** 创建隧道请求体 */
export interface CreateTunnelInput {
  clientId: string;
  name: string;
  type: ProxyType;
  local_ip: string;
  local_port: number;
  remote_port?: number;
  domain?: string;
}

// --- SSE Events ---

export interface StatsUpdateEvent {
  client_id: string;
  stats: SystemStats;
}

export interface ClientOnlineEvent {
  client_id: string;
  info: ClientInfo;
}

export interface ClientOfflineEvent {
  client_id: string;
}

export interface TunnelChangedEvent {
  client_id: string;
  tunnel: ProxyConfig;
}

// --- API ---

export interface DiskPartition {
  path: string;
  used: number;
  total: number;
}

export interface ServerStatus {
  status: string;
  client_count: number;
  version: string;
  listen_port: number;
  uptime: number;         // seconds (process uptime)
  system_uptime: number;  // seconds (OS boot uptime)
  os_install_time?: number; // unix timestamp (seconds)
  store_path: string;
  tunnel_active: number;
  tunnel_paused: number;
  tunnel_stopped: number;
  server_addr: string;
  allowed_ports: PortRange[];
  os_arch: string;
  go_version: string;
  hostname: string;
  ip_address: string;
  cpu_usage: number;
  cpu_cores: number;
  mem_used: number;
  mem_total: number;
  app_mem_used: number;
  app_mem_sys: number;
  disk_used: number;
  disk_total: number;
  disk_partitions: DiskPartition[];
  goroutine_count: number;
  public_ipv4?: string;
  public_ipv6?: string;
}

// --- Admin System ---

export interface APIKey {
  id: string;
  name: string;
  permissions: string[];
  created_at: string;
  expires_at?: string;
  is_active: boolean;
  max_uses: number;
  use_count: number;
}

export interface AdminUser {
  id: string;
  username: string;
  role: string;
  created_at: string;
  last_login?: string;
}




export interface LoginResponse {
  token: string;
  user: {
    id: string;
    username: string;
    role: string;
  };
}

// --- Setup (初始化) ---

export interface PortRange {
  start: number;
  end: number;
}

export interface ServerConfig {
  server_addr: string;
  allowed_ports: PortRange[];
}

export interface SetupStatus {
  initialized: boolean;
  setup_token_required: boolean;
}

export interface SetupRequest {
  admin: {
    username: string;
    password: string;
  };
  server_addr: string;
  allowed_ports: PortRange[];
  setup_token?: string;
}

export interface SetupResponse {
  success: boolean;
  message: string;
}
