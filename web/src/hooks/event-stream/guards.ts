import { isResourceBootstrap } from '@/lib/resource-bootstrap';
import type {
  ActivityItem,
  Client,
  ClientOfflineEvent,
  ClientOnlineEvent,
  ClientTrafficResponse,
  ConsoleSnapshot,
  ConsoleSummary,
  ProxyConfig,
  StatsUpdateEvent,
  TrafficRealtimeEvent,
  TunnelChangedEvent,
} from '@/types';

type JsonObject = Record<string, unknown>;

export interface UserListChangedEvent {
  user_id: string;
}

export interface WebhookChangedEvent {
  webhook_id: string;
}

export interface WebhookDeliveryChangedEvent extends WebhookChangedEvent {
  delivery_id: string;
  status: string;
}

export interface ActivityReadyPayload {
  activity_cursor: number;
}

const consoleSummaryFields = [
  'total_clients',
  'online_clients',
  'offline_clients',
  'total_tunnels',
  'active_tunnels',
  'inactive_tunnels',
  'pending_tunnels',
  'offline_tunnels',
  'stopped_tunnels',
  'error_tunnels',
] as const satisfies readonly (keyof ConsoleSummary)[];

function isRecord(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0;
}

export function isUserListChangedEvent(value: unknown): value is UserListChangedEvent {
  return isRecord(value) && isNonEmptyString(value.user_id);
}

export function isWebhookChangedEvent(value: unknown): value is WebhookChangedEvent {
  return isRecord(value) && isNonEmptyString(value.webhook_id);
}

export function isWebhookDeliveryChangedEvent(value: unknown): value is WebhookDeliveryChangedEvent {
  return isRecord(value)
    && isNonEmptyString(value.webhook_id)
    && isNonEmptyString(value.delivery_id)
    && isNonEmptyString(value.status);
}

function isConsoleSummary(value: unknown): value is ConsoleSummary {
  return isRecord(value) && consoleSummaryFields.every((field) => typeof value[field] === 'number');
}

function isClient(value: unknown): value is Client {
  return (
    isRecord(value) &&
    isNonEmptyString(value.id) &&
    typeof value.ingress_bps === 'number' &&
    typeof value.egress_bps === 'number' &&
    isRecord(value.info) &&
    (value.stats === undefined || value.stats === null || isRecord(value.stats)) &&
    (value.proxies === undefined || Array.isArray(value.proxies)) &&
    typeof value.online === 'boolean'
  );
}

export function isConsoleSnapshot(value: unknown): value is ConsoleSnapshot {
  return (
    isRecord(value) &&
    Array.isArray(value.clients) && value.clients.every(isClient) &&
    isConsoleSummary(value.summary) &&
    isResourceBootstrap(value.bootstrap) &&
    typeof value.generated_at === 'string' &&
    typeof value.fresh_until === 'string'
  );
}

export function isStatsUpdateEvent(value: unknown): value is StatsUpdateEvent {
  return (
    isRecord(value) &&
    isNonEmptyString(value.client_id) &&
    isRecord(value.stats)
  );
}

function isTrafficResolution(value: unknown): value is ClientTrafficResponse['resolution'] {
  return value === 'second' || value === 'minute' || value === 'hour';
}

function isTrafficPoint(value: unknown): boolean {
  return (
    isRecord(value) &&
    typeof value.bucket_start === 'string' &&
    typeof value.ingress_bytes === 'number' &&
    typeof value.egress_bytes === 'number' &&
    typeof value.total_bytes === 'number'
  );
}

function isTunnelTrafficSeries(value: unknown): boolean {
  return isRecord(value) && Array.isArray(value.points) && value.points.every(isTrafficPoint);
}

function isTrafficRealtimeClient(value: unknown): value is TrafficRealtimeEvent['clients'][number] {
  return (
    isRecord(value) &&
    isNonEmptyString(value.client_id) &&
    isTrafficResolution(value.resolution) &&
    Array.isArray(value.items) &&
    value.items.every(isTunnelTrafficSeries)
  );
}

export function isTrafficRealtimeEvent(value: unknown): value is TrafficRealtimeEvent {
  return (
    isRecord(value) &&
    (value.generated_at === undefined || typeof value.generated_at === 'string') &&
    Array.isArray(value.clients) &&
    value.clients.every(isTrafficRealtimeClient)
  );
}

export function isClientOnlineEvent(value: unknown): value is ClientOnlineEvent {
  return isRecord(value) && isNonEmptyString(value.client_id) && isRecord(value.info);
}

export function isClientOfflineEvent(value: unknown): value is ClientOfflineEvent {
  return isRecord(value) && isNonEmptyString(value.client_id);
}

export function isProxyConfig(value: unknown): value is ProxyConfig {
  return (
    isRecord(value) &&
    isNonEmptyString(value.id) &&
    isNonEmptyString(value.name) &&
    isNonEmptyString(value.type) &&
    typeof value.local_ip === 'string' &&
    typeof value.local_port === 'number' &&
    typeof value.remote_port === 'number' &&
    typeof value.domain === 'string' &&
    isNonEmptyString(value.client_id) &&
    typeof value.ingress_bps === 'number' &&
    typeof value.egress_bps === 'number' &&
    typeof value.created_at === 'string' &&
    isNonEmptyString(value.desired_state) &&
    isNonEmptyString(value.runtime_state) &&
    (value.capabilities === undefined || isRecord(value.capabilities))
  );
}

export function isActivityReadyPayload(value: unknown): value is ActivityReadyPayload {
  return isRecord(value) && Number.isSafeInteger(value.activity_cursor) && Number(value.activity_cursor) >= 0;
}

export function isActivityItem(value: unknown): value is ActivityItem {
  return isRecord(value)
    && Number.isSafeInteger(value.id) && Number(value.id) > 0
    && typeof value.occurred_at === 'string'
    && typeof value.recorded_at === 'string'
    && ['debug', 'info', 'warning', 'error'].includes(String(value.severity))
    && ['client', 'tunnel', 'p2p', 'admin', 'security'].includes(String(value.category))
    && typeof value.action === 'string'
    && typeof value.source === 'string'
    && isRecord(value.actor)
    && Number.isSafeInteger(value.payload_version)
    && isRecord(value.payload)
    && Array.isArray(value.clients)
    && Array.isArray(value.tunnels);
}

export function isTunnelChangedEvent(value: unknown): value is TunnelChangedEvent {
  return (
    isRecord(value) &&
    isNonEmptyString(value.client_id) &&
    (value.action === undefined || typeof value.action === 'string') &&
    isProxyConfig(value.tunnel)
  );
}

export function parseEventPayload<T>(data: string, guard: (value: unknown) => value is T): T | null {
  try {
    const parsed: unknown = JSON.parse(data);
    return guard(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

export function parseSSE(buffer: string, onEvent: (eventType: string, data: string) => void): string {
  let remaining = buffer;

  while (true) {
    const delimiterIndex = remaining.indexOf('\n\n');
    if (delimiterIndex === -1) {
      break;
    }

    const rawEvent = remaining.slice(0, delimiterIndex);
    remaining = remaining.slice(delimiterIndex + 2);

    let eventType = 'message';
    const dataLines: string[] = [];

    for (const line of rawEvent.split('\n')) {
      if (line.startsWith('event:')) {
        eventType = line.slice(6).trim();
      }
      if (line.startsWith('data:')) {
        dataLines.push(line.slice(5).trim());
      }
    }

    if (dataLines.length > 0) {
      onEvent(eventType, dataLines.join('\n'));
    }
  }

  return remaining;
}
