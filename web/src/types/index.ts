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
  update_capability?: UpdateCapability;
  public_ipv4?: string;
  public_ipv6?: string;
}

export interface UpdateCapability {
  install_method: "service" | "docker" | "binary";
}

export type VersionInstallMethod = "service" | "docker" | "binary";
export interface VersionCheckCommands {
  command: string;
}

export type VersionTargetKind = "server" | "client";
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
  updated_at?: string;
  fresh_until?: string;
}

/** 对齐 /api/clients 响应中的 clientView (server.go handleAPIClients) */
export interface Client {
  id: string;
  display_name?: string;
  ingress_bps: number;
  egress_bps: number;
  info: ClientInfo;
  stats: SystemStats | null;
  proxies?: ProxyConfig[];
  online: boolean;
  last_seen?: string;
  last_ip?: string;
}

// --- Tunnel / Proxy ---

export type ProxyType = "tcp" | "udp" | "http";
export type TunnelFormType = ProxyType | "socks5";
export type TunnelTopology = "server_expose" | "client_to_client";
export type EndpointLocation = "server" | "client";
export type IngressEndpointType = "tcp_listen" | "udp_listen" | "http_host" | "socks5_listen";
export type TargetEndpointType = "tcp_service" | "udp_service" | "socks5_connect_handler";
export type EndpointType = IngressEndpointType | TargetEndpointType;
export type TransportPolicy = "server_relay_only" | "direct_preferred" | "direct_only";
export type ActualTransport = "unknown" | "server_relay" | "peer_direct" | "turn_relay";
export type P2PStateValue = "idle" | "gathering" | "checking" | "connected" | "failed" | "fallback" | "closed";
export type ParticipantRole = "ingress" | "target";
export type ParticipantState =
  | "provision_pending"
  | "ready"
  | "offline"
  | "idle"
  | "error";
export type TunnelClientRole = "owner" | "ingress" | "target" | "related";
export type ProxyDesiredState = "running" | "stopped";
export type ProxyRuntimeState = "pending" | "exposed" | "active" | "offline" | "idle" | "error";

export interface BandwidthSettings {
  ingress_bps: number;
  egress_bps: number;
  total_bps?: number;
}

export interface TcpListenConfig {
  bind_ip: string;
  port: number;
  allowed_source_cidrs?: string[];
}

export type UdpListenConfig = TcpListenConfig;

export interface HttpHostConfig {
  domain: string;
  allowed_source_cidrs?: string[];
  auth?: HttpAuthConfig;
}

export interface HttpAuthConfig {
  type: "none" | "basic";
  username?: string;
  password?: string;
  password_hash?: string;
}

export interface TcpServiceConfig {
  ip?: string;
  host?: string;
  port: number;
}

export type UdpServiceConfig = TcpServiceConfig;

export interface Socks5AuthConfig {
  type: "none" | "username_password";
  username?: string;
  password?: string;
  password_hash?: string;
}

export interface Socks5ListenConfig {
  bind_ip: string;
  port: number;
  allowed_source_cidrs: string[];
  auth: Socks5AuthConfig;
}

export interface Socks5ConnectHandlerConfig {
  allowed_target_cidrs: string[];
  allowed_target_hosts: string[];
  allowed_target_ports: number[];
  dial_timeout_seconds: number;
}

export type TunnelIngress =
  | {
    location: "server" | "client";
    client_id?: string;
    type: "tcp_listen";
    config: TcpListenConfig;
  }
  | {
    location: "server" | "client";
    client_id?: string;
    type: "udp_listen";
    config: UdpListenConfig;
  }
  | {
    location: "server";
    client_id?: string;
    type: "http_host";
    config: HttpHostConfig;
  }
  | {
    location: "server" | "client";
    client_id?: string;
    type: "socks5_listen";
    config: Socks5ListenConfig;
  };

export type TunnelTarget =
  | {
    location: "client";
    client_id: string;
    type: "tcp_service";
    config: TcpServiceConfig;
  }
  | {
    location: "client";
    client_id: string;
    type: "udp_service";
    config: UdpServiceConfig;
  }
  | {
    location: "client";
    client_id: string;
    type: "socks5_connect_handler";
    config: Socks5ConnectHandlerConfig;
  };

export interface P2PState {
  state: P2PStateValue;
  error?: string;
  session_id?: string;
}

export interface ParticipantRuntime {
  client_id: string;
  role: ParticipantRole;
  state: ParticipantState;
  revision: number;
  error?: string;
}

export interface TunnelParticipants {
  ingress?: ParticipantRuntime;
  target?: ParticipantRuntime;
}

export interface TransportRuntime {
  policy: TransportPolicy;
  actual: ActualTransport;
  p2p_state?: P2PStateValue;
  p2p_error?: string;
  fallback_since?: string;
  last_direct_ok?: string;
  last_direct_error?: string;
}

/** TunnelSpec = Ingress + Target + Transport. Current endpoint scope excludes future-only target types. */

export type TunnelIssueScope = "ingress_client" | "target_client" | "server" | "transport" | "config";
export type TunnelIssueSeverity = "info" | "warning" | "error";

export interface TunnelIssue {
  code: string;
  scope: TunnelIssueScope | string;
  client_id?: string;
  severity: TunnelIssueSeverity | string;
  message: string;
  retryable: boolean;
  observed_at: string;
  details?: Record<string, unknown>;
}

export interface TunnelSpec {
  id: string;
  name: string;
  revision: number;
  topology: TunnelTopology;
  owner_client_id: string;
  ingress: TunnelIngress;
  target: TunnelTarget;
  transport_policy: TransportPolicy;
  actual_transport: ActualTransport;
  p2p: P2PState;
  desired_state: ProxyDesiredState;
  runtime_state: ProxyRuntimeState;
  error?: string;
  issues?: TunnelIssue[];
  participants?: TunnelParticipants;
  transport?: TransportRuntime;
  bandwidth_settings: BandwidthSettings;
  created_by_user_id?: string;
  created_at: string;
  updated_at?: string;
  metadata_missing?: boolean;
}

export interface TunnelCapabilities {
  can_resume: boolean;
  can_stop: boolean;
  can_edit: boolean;
  can_delete: boolean;
  can_migrate: boolean;
}

/** 对齐 protocol.ProxyConfig */
export interface ProxyConfig {
  id: string;
  name: string;
  revision?: number;
  topology?: TunnelTopology;
  owner_client_id?: string;
  ingress?: TunnelIngress;
  target?: TunnelTarget;
  transport_policy?: TransportPolicy;
  actual_transport?: ActualTransport;
  p2p?: P2PState;
  participants?: TunnelParticipants;
  transport?: TransportRuntime;
  bandwidth_settings?: BandwidthSettings;
  metadata_missing?: boolean;
  type: ProxyType;
  local_ip: string;
  local_port: number;
  remote_port: number;
  domain: string;
  client_id: string;
  ingress_bps: number;
  egress_bps: number;
  total_bps?: number;
  created_at: string;
  desired_state: ProxyDesiredState;
  runtime_state: ProxyRuntimeState;
  error?: string;
  issues?: TunnelIssue[];
  capabilities: TunnelCapabilities;
}

/** 创建隧道请求体 */
export interface CreateTunnelInput {
  clientId: string;
  name: string;
  topology?: TunnelTopology;
  ingress_client_id?: string;
  bind_ip?: string;
  type: TunnelFormType;
  local_ip: string;
  local_port: number;
  remote_port?: number;
  domain?: string;
  allowed_source_cidrs?: string[];
  ingress_bps?: number;
  egress_bps?: number;
  total_bps?: number;
  transport_policy?: TransportPolicy;
  socks5?: {
    auth_type: Socks5AuthConfig["type"];
    username?: string;
    password?: string;
    allowed_target_cidrs?: string[];
    allowed_target_hosts?: string[];
    allowed_target_ports?: number[];
    dial_timeout_seconds?: number;
  };
  http_auth?: {
    enabled: boolean;
    username?: string;
    password?: string;
  };
  confirm_no_auth_risk?: boolean;
}

export interface UpdateTunnelInput {
  clientId: string;
  tunnelId: string;
  expected_revision?: number;
  name: string;
  topology?: TunnelTopology;
  ingress_client_id?: string;
  bind_ip?: string;
  type: TunnelFormType;
  local_ip: string;
  local_port: number;
  remote_port?: number;
  domain?: string;
  allowed_source_cidrs?: string[];
  ingress_bps?: number;
  egress_bps?: number;
  total_bps?: number;
  transport_policy?: TransportPolicy;
  socks5?: CreateTunnelInput["socks5"];
  http_auth?: CreateTunnelInput["http_auth"];
  confirm_no_auth_risk?: boolean;
}

export type TrafficResolution = 'second' | 'minute' | 'hour';
export type ClientTrafficRange = '60s' | '1h' | '24h' | '7d';

export interface TrafficPoint {
  bucket_start: string;
  ingress_bytes: number;
  egress_bytes: number;
  total_bytes: number;
}

export interface TunnelTrafficSeries {
  tunnel_id?: string;
  tunnel_name?: string;
  tunnel_type?: ProxyType;
  metadata_missing?: boolean;
  points: TrafficPoint[];
}

export interface ClientTrafficResponse {
  resolution: TrafficResolution;
  items: TunnelTrafficSeries[];
}

export interface TrafficRealtimeClient extends ClientTrafficResponse {
  client_id: string;
}

export interface TrafficRealtimeEvent {
  generated_at: string;
  clients: TrafficRealtimeClient[];
}

export interface ClientBandwidthSettingsResponse {
  success: boolean;
  bandwidth_settings: {
    ingress_bps: number;
    egress_bps: number;
  };
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
  action?: string;
  tunnel: ProxyConfig;
}

export interface TunnelCreateRequest {
  name: string;
  topology: TunnelTopology;
  ingress: TunnelIngress;
  target: TunnelTarget;
  transport_policy: TransportPolicy;
  bandwidth_settings: BandwidthSettings;
  confirm_no_auth_risk?: boolean;
}

export interface TunnelUpdateRequest {
  expected_revision?: number;
  spec: TunnelCreateRequest;
}

export interface TunnelMigrateRequest {
  expected_revision: number;
  target_client_id: string;
}

export interface MigrateTunnelInput extends TunnelMigrateRequest {
  tunnelId: string;
}

export interface TunnelMutationResponse {
  success: boolean;
  message?: string;
  tunnel?: TunnelSpec;
  tunnel_id?: string;
}

export interface ConsoleSummary {
  total_clients: number;
  online_clients: number;
  offline_clients: number;
  total_tunnels: number;
  active_tunnels: number;
  inactive_tunnels: number;
  pending_tunnels: number;
  offline_tunnels: number;
  stopped_tunnels: number;
  error_tunnels: number;
}

export interface ConsoleSnapshot {
  clients?: Client[];
  summary?: ConsoleSummary;
  server_status?: ServerStatus;
  generated_at?: string;
  fresh_until?: string;
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
  summary?: ConsoleSummary;
  version: string;
  update_capability?: UpdateCapability;
  listen_port: number;
  uptime: number;         // seconds (process uptime)
  system_uptime: number;  // seconds (OS boot uptime)
  os_install_time?: number; // unix timestamp (seconds)
  tunnel_active: number;
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
  generated_at: string;
  fresh_until: string;
}

export interface VersionCheckResult {
  target: VersionTargetKind;
  target_id: string;
  current_version: string;
  latest_version: string;
  update_available: boolean;
  checked_at: string;
  install_method: VersionInstallMethod;
  recommended_channel: "stable" | "beta" | "";
  recommended_action: "none" | "run_script" | "github_release" | "docker_docs";
  commands: VersionCheckCommands | null;
  release_url: string;
  check_failed: boolean;
  refresh_failed: boolean;
  cache_source: "fresh" | "cache" | "stale_cache" | "none";
  reason: string;
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
  mfa_required?: false;
}

export interface MFALoginResponse {
  mfa_required: true;
  mfa_token: string;
  user: {
    id: string;
    username: string;
    role: string;
  };
}

export type AuthLoginResponse = LoginResponse | MFALoginResponse;

export interface PasskeySummary {
  id: string;
  name: string;
  rp_id: string;
  origin: string;
  created_at: string;
  last_used_at?: string;
}

export interface AdminSecurity {
  user: AdminUser;
  totp_enabled: boolean;
  recovery_codes_remaining: number;
  passkeys: PasskeySummary[];
  webauthn: {
    rp_id: string;
    origin: string;
  };
}

export interface RateLimitEntry {
  ip: string;
  request_count: number;
  max_requests: number;
  limited: boolean;
  reason?: string;
  retry_after_seconds: number;
  locked_until?: string;
  last_activity: string;
  window_seconds: number;
}

export interface ClientAuthRateLimitSettings {
  enabled: boolean;
  requests_per_minute: number;
}

export interface ClientAuthRateLimitsResponse extends ClientAuthRateLimitSettings {
  entries: RateLimitEntry[];
  generated_at: string;
}

export interface ResetRateLimitResponse {
  success: boolean;
  deleted: boolean;
  ip: string;
}

export interface TOTPBeginResponse {
  setup_token: string;
  secret: string;
  otpauth_url: string;
  qr_data_url: string;
}

export interface RecoveryCodesResponse {
  success: boolean;
  requires_relogin: boolean;
  recovery_codes: string[];
}

export interface PasskeyChallengeResponse {
  challenge_id: string;
  public_key: unknown;
}

export interface ReloginResponse {
  success: boolean;
  requires_relogin: boolean;
}

// --- Setup (初始化) ---

export interface PortRange {
  start: number;
  end: number;
}

export type ActivitySeverity = 'debug' | 'info' | 'warning' | 'error';

export interface ActivityRetentionRule {
  days: number;
  min_count: number;
}

export interface ActivityRetentionPolicy {
  debug: ActivityRetentionRule;
  info: ActivityRetentionRule;
  warning: ActivityRetentionRule;
  error: ActivityRetentionRule;
}

export interface ServerConfig {
  server_addr: string;
  allowed_ports: PortRange[];
  activity_retention: ActivityRetentionPolicy;
}

export type ActivityCategory = 'client' | 'tunnel' | 'p2p' | 'admin' | 'security';
export type ActivityScope = 'global' | 'client' | 'tunnel';

export interface ActivityActor {
  type: 'admin' | 'client' | 'system' | 'security' | 'unknown';
  id?: string;
  name?: string;
  ip_hash?: string;
  ip_prefix?: string;
}

export interface ActivityClientSubject {
  client_id: string;
  relation: 'owner' | 'ingress' | 'target' | 'peer' | 'subject' | 'related';
  display_name?: string;
  hostname?: string;
  truncated?: boolean;
}

export interface ActivityTunnelSubject {
  tunnel_id: string;
  relation: 'subject' | 'related' | 'shared_session';
  name?: string;
  tunnel_type?: string;
  topology?: string;
  truncated?: boolean;
}

export interface ActivitySummaryArgs {
  client_name?: string;
  tunnel_name?: string;
  resource_name?: string;
  before?: string;
  after?: string;
  value?: number;
  count?: number;
  transport?: string;
  topology?: string;
}

export interface ActivityPayloadV1 {
  summary_key?: string;
  summary_args?: ActivitySummaryArgs;
  reason_code?: string;
  before?: string;
  after?: string;
  revision?: number;
  generation?: number;
  sequence?: number;
  session_id?: string;
}

export interface ActivityItem {
  id: number;
  occurred_at: string;
  recorded_at: string;
  severity: ActivitySeverity;
  category: ActivityCategory;
  action: string;
  source: string;
  actor: ActivityActor;
  payload_version: number;
  payload: ActivityPayloadV1;
  clients: ActivityClientSubject[];
  tunnels: ActivityTunnelSubject[];
}

export interface ActivityPage {
  items: ActivityItem[];
  next_cursor?: number;
  has_more: boolean;
  direction: 'before' | 'after';
}

export interface ActivityQuery {
  scope?: ActivityScope;
  scopeId?: string;
  before?: number;
  after?: number;
  limit?: number;
  severities?: ActivitySeverity[];
  categories?: ActivityCategory[];
  from?: string;
  to?: string;
}

export interface AffectedTunnel {
  client_id: string;
  hostname: string;
  display_name?: string;
  tunnel_name: string;
  remote_port: number;
  desired_state: ProxyDesiredState;
  runtime_state: ProxyRuntimeState;
  error?: string;
}

export interface AdminConfig extends ServerConfig {
  effective_server_addr: string;
  server_addr_locked: boolean;
}

export interface AdminConfigUpdateResponse {
  success?: boolean;
  error?: string;
  server_addr_locked?: boolean;
  affected_tunnels: AffectedTunnel[];
  conflicting_http_tunnels: string[];
}

export interface TunnelMutationErrorResponse {
  success?: boolean;
  error?: string;
  message?: string;
  error_code?: string;
  code?: string;
  field?: string;
}
