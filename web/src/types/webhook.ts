import { createLocalId } from '@/lib/utils';

export type WebhookMethod = 'GET' | 'POST';

export type WebhookEventKey =
  | 'client.online'
  | 'client.offline'
  | 'tunnel.stopped'
  | 'tunnel.resumed'
  | 'tunnel.runtime_changed'
  | 'tunnel.runtime_error'
  | 'tunnel.runtime_recovered'
  | 'p2p.checking'
  | 'p2p.connected'
  | 'p2p.failed'
  | 'p2p.fallback'
  | 'p2p.session_closed';

export type WebhookTargetKind = 'client' | 'tunnel';
export type WebhookEventFamily = WebhookTargetKind | 'p2p';
export type WebhookTemplateSurface = 'url' | 'header' | 'body';
export type WebhookTemplateValue = string | number | boolean | null | Record<string, unknown> | unknown[];

export interface WebhookCatalogEvent {
  key: WebhookEventKey;
  target_kind: WebhookTargetKind;
  family: WebhookEventFamily;
}

export interface WebhookVariable {
  key: string;
  group: 'delivery' | 'event' | 'client' | 'tunnel' | 'webhook';
  value_type: 'text' | 'number' | 'boolean' | 'json';
  surfaces: WebhookTemplateSurface[];
  available_for_events: 'all' | WebhookEventKey[];
  optional?: boolean;
}

export interface WebhookCatalog {
  events: WebhookCatalogEvent[];
  variables: WebhookVariable[];
  fixtures: Record<WebhookEventKey, Record<string, WebhookTemplateValue>>;
  default_body: string;
  locales: string[];
}

export interface WebhookTargetOption {
  id: string;
  name: string;
  detail: string;
  unavailable?: boolean;
}

export interface WebhookHeader {
  id: string;
  key: string;
  value: string;
}

export interface ActivityWebhookConfig {
  id: string;
  revision: number;
  name: string;
  enabled: boolean;
  targetKind: WebhookTargetKind;
  targetMode: 'all' | 'selected';
  targetIds: string[];
  method: WebhookMethod;
  url: string;
  headers: WebhookHeader[];
  body: string;
  events: WebhookEventKey[];
  minIntervalSeconds: number;
  calls24h: number;
  lastStatus: 'success' | 'failed' | 'retrying' | 'idle';
  consecutiveFailures: number;
  lastCalledAt: string | null;
  createdAt: string;
  updatedAt: string;
}

export interface ActivityWebhookAPI {
  id: string;
  revision: number;
  name: string;
  enabled: boolean;
  target_kind: WebhookTargetKind;
  target_mode: 'all' | 'selected';
  target_ids: string[];
  method: WebhookMethod;
  url: string;
  headers: WebhookHeader[];
  body: string;
  events: WebhookEventKey[];
  min_interval_seconds: number;
  calls_24h: number;
  last_status: ActivityWebhookConfig['lastStatus'];
  consecutive_failures: number;
  last_called_at?: string | null;
  created_at: string;
  updated_at: string;
}

export interface WebhookConfigInputAPI {
  id: string;
  expected_revision?: number;
  name: string;
  enabled: boolean;
  target_kind: WebhookTargetKind;
  target_mode: 'all' | 'selected';
  target_ids: string[];
  method: WebhookMethod;
  url: string;
  headers: WebhookHeader[];
  body: string;
  events: WebhookEventKey[];
  min_interval_seconds: number;
}

export function activityWebhookFromAPI(value: ActivityWebhookAPI): ActivityWebhookConfig {
  return {
    id: value.id,
    revision: value.revision,
    name: value.name,
    enabled: value.enabled,
    targetKind: value.target_kind,
    targetMode: value.target_mode,
    targetIds: value.target_ids ?? [],
    method: value.method,
    url: value.url,
    headers: value.headers ?? [],
    body: value.body,
    events: value.events ?? [],
    minIntervalSeconds: value.min_interval_seconds,
    calls24h: value.calls_24h,
    lastStatus: value.last_status,
    consecutiveFailures: value.consecutive_failures,
    lastCalledAt: value.last_called_at ?? null,
    createdAt: value.created_at,
    updatedAt: value.updated_at,
  };
}

export function activityWebhookToAPI(value: ActivityWebhookConfig): WebhookConfigInputAPI {
  return {
    id: value.id,
    expected_revision: value.revision || undefined,
    name: value.name,
    enabled: value.enabled,
    target_kind: value.targetKind,
    target_mode: value.targetMode,
    target_ids: value.targetIds,
    method: value.method,
    url: value.url,
    headers: value.headers,
    body: value.body,
    events: value.events,
    min_interval_seconds: value.minIntervalSeconds,
  };
}

export function createEmptyWebhook(catalog: WebhookCatalog): ActivityWebhookConfig {
  const now = new Date().toISOString();
  return {
    id: `wh_${createLocalId('webhook')}`,
    revision: 0,
    name: '',
    enabled: false,
    targetKind: 'client',
    targetMode: 'selected',
    targetIds: [],
    method: 'POST',
    url: '',
    headers: [{ id: `header_${createLocalId('header')}`, key: 'Content-Type', value: 'application/json' }],
    body: catalog.default_body,
    events: [],
    minIntervalSeconds: 0,
    calls24h: 0,
    lastStatus: 'idle',
    consecutiveFailures: 0,
    lastCalledAt: null,
    createdAt: now,
    updatedAt: now,
  };
}

export type WebhookInvocationStatus = 'queued' | 'retrying' | 'success' | 'failed' | 'canceled';
export type WebhookInvocationOrigin = 'event' | 'test' | 'replay';

export interface WebhookInvocationAttempt {
  number: number;
  occurredAt: string;
  status: 'success' | 'failed' | 'pending';
  statusCode: number | null;
  durationMs: number | null;
  error?: string;
}

export interface WebhookInvocation {
  id: string;
  webhookId: string;
  webhookName: string;
  eventId: string;
  event: WebhookEventKey;
  occurredAt: string;
  status: WebhookInvocationStatus;
  origin: WebhookInvocationOrigin;
  statusCode: number | null;
  durationMs: number | null;
  attempts: WebhookInvocationAttempt[];
  requestMethod: WebhookMethod;
  requestUrl: string;
  requestHeaders: Record<string, string>;
  requestBody: string | null;
  responseHeaders: Record<string, string>;
  responseBody: string;
  error?: string;
  nextAttemptAt?: string;
  createdAt: string;
}

export interface WebhookInvocationAPI {
  id: string;
  webhook_id: string;
  webhook_name: string;
  event_id: string;
  event: WebhookEventKey;
  occurred_at: string;
  status: WebhookInvocationStatus;
  origin: WebhookInvocationOrigin;
  status_code?: number | null;
  duration_ms?: number | null;
  attempts: Array<{
    number: number;
    occurred_at: string;
    status: 'success' | 'failed' | 'pending';
    status_code?: number | null;
    duration_ms?: number | null;
    error?: string;
  }>;
  request_method: WebhookMethod;
  request_url: string;
  request_headers: Record<string, string>;
  request_body?: string | null;
  response_headers: Record<string, string>;
  response_body: string;
  error?: string;
  next_attempt_at?: string;
  created_at: string;
}

export function webhookInvocationFromAPI(value: WebhookInvocationAPI): WebhookInvocation {
  return {
    id: value.id,
    webhookId: value.webhook_id,
    webhookName: value.webhook_name,
    eventId: value.event_id,
    event: value.event,
    occurredAt: value.occurred_at,
    status: value.status,
    origin: value.origin,
    statusCode: value.status_code ?? null,
    durationMs: value.duration_ms ?? null,
    attempts: (value.attempts ?? []).map((attempt) => ({
      number: attempt.number,
      occurredAt: attempt.occurred_at,
      status: attempt.status,
      statusCode: attempt.status_code ?? null,
      durationMs: attempt.duration_ms ?? null,
      error: attempt.error,
    })),
    requestMethod: value.request_method,
    requestUrl: value.request_url,
    requestHeaders: value.request_headers ?? {},
    requestBody: value.request_body ?? null,
    responseHeaders: value.response_headers ?? {},
    responseBody: value.response_body ?? '',
    error: value.error,
    nextAttemptAt: value.next_attempt_at,
    createdAt: value.created_at,
  };
}

export interface WebhookDeliveryPageAPI {
  items: WebhookInvocationAPI[];
  next_cursor?: string;
  has_more: boolean;
}

export interface WebhookPreviewAPI {
  event: WebhookEventKey;
  values: Record<string, WebhookTemplateValue>;
  method: WebhookMethod;
  url: string;
  headers: Record<string, string>;
  body: string | null;
}
