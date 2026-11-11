/**
 * 统一 API 请求器
 * 所有业务代码通过此模块发起 HTTP 请求，不直接使用 fetch
 *
 * P5: 认证凭证通过 httpOnly cookie 自动传递（credentials: 'same-origin'），
 * 不再需要手动管理 Authorization header。API 编程调用者仍可通过 header 传递 token。
 */

import { i18n } from '@/i18n';
import { clearClientSessionAndRedirect } from '@/lib/session';
import type { ResourceScope } from '@/lib/resource-scope';
import type {
  APIKey,
  Client,
  ProxyConfig,
  TunnelClientRole,
  ActivityPage,
  ActivityQuery,
  TunnelCreateRequest,
  TunnelMigrateRequest,
  TunnelMutationResponse,
  TunnelUpdateRequest,
  ManagedUser,
  Principal,
  UserDeletionImpact,
  UserListResponse,
} from '@/types';
import {
  activityWebhookFromAPI,
  activityWebhookToAPI,
  webhookInvocationFromAPI,
  type ActivityWebhookAPI,
  type ActivityWebhookConfig,
  type WebhookCatalog,
  type WebhookDeliveryPageAPI,
  type WebhookEventKey,
  type WebhookInvocation,
  type WebhookInvocationAPI,
  type WebhookInvocationStatus,
  type WebhookPreviewAPI,
} from '@/types/webhook';

class ApiError extends Error {
  status: number;
  statusText: string;
  code?: string;
  field?: string;
  body?: unknown;

  constructor(
    status: number,
    statusText: string,
    message?: string,
    body?: unknown,
    code?: string,
    field?: string,
  ) {
    super(localizeApiErrorMessage(code, message) || `API Error: ${status} ${statusText}`);
    this.name = "ApiError";
    this.status = status;
    this.statusText = statusText;
    this.code = code;
    this.field = field;
    this.body = body;
  }
}

interface ApiErrorBody {
  error?: string;
  message?: string;
  code?: string;
  error_code?: string;
  field?: string;
}

const AUTH_SESSION_ERROR_CODES = new Set([
  'missing_credentials',
  'invalid_or_expired_token',
  'session_expired_or_revoked',
  'session_environment_mismatch',
  'session_not_found',
  'admin_user_not_found',
  'user_not_found',
  'user_disabled',
  'admin_role_changed',
]);

function localizeApiErrorMessage(code?: string, fallback?: string) {
  if (!code) return fallback;
  const translated = i18n.t(`errors.${code}`, { defaultValue: '' });
  return translated || fallback;
}

function normalizeErrorBody(value: unknown): ApiErrorBody | undefined {
  if (!value || typeof value !== 'object') return undefined;
  return value as ApiErrorBody;
}

export function shouldLogoutOnAPIError(status: number, code?: string) {
  if (status !== 401) return false;
  return !code || AUTH_SESSION_ERROR_CODES.has(code);
}

async function request<T>(
  url: string,
  options?: RequestInit,
): Promise<T> {
  const headers = new Headers({
    "Content-Type": "application/json",
    ...options?.headers,
  });

  const res = await fetch(url, {
    ...options,
    headers,
    credentials: 'same-origin',
  });

  if (!res.ok) {
    const bodyText = await res.text().catch(() => "");
    let errorBody: unknown;
    let errorMessage = bodyText || undefined;
    let errorCode: string | undefined;
    let errorField: string | undefined;
    try {
      if (bodyText) {
        const json = JSON.parse(bodyText);
        errorBody = json;
        const normalized = normalizeErrorBody(json);
        if (normalized) {
          errorCode = normalized.code || normalized.error_code;
          errorField = normalized.field;
          if (typeof normalized.message === 'string') {
            errorMessage = normalized.message;
          } else if (typeof normalized.error === 'string') {
            errorMessage = normalized.error;
          }
        }
      }
    } catch {
      // not JSON, fallback to raw string
    }

    if (shouldLogoutOnAPIError(res.status, errorCode)) {
      clearClientSessionAndRedirect();
    }

    throw new ApiError(res.status, res.statusText, errorMessage, errorBody, errorCode, errorField);
  }

  // 204 No Content
  if (res.status === 204) return undefined as T;

  return res.json() as Promise<T>;
}

export const api = {
  get<T>(url: string): Promise<T> {
    return request<T>(url);
  },

  post<T>(url: string, body?: unknown): Promise<T> {
    return request<T>(url, {
      method: "POST",
      body: body ? JSON.stringify(body) : undefined,
    });
  },

  put<T>(url: string, body?: unknown): Promise<T> {
    return request<T>(url, {
      method: "PUT",
      body: body ? JSON.stringify(body) : undefined,
    });
  },

  delete<T>(url: string, body?: unknown): Promise<T> {
    return request<T>(url, {
      method: "DELETE",
      body: body ? JSON.stringify(body) : undefined,
    });
  },
};

function encodePath(value: string) {
  return encodeURIComponent(value);
}

function adminUserBase(userId: string) {
  return `/api/admin/users/${encodePath(userId)}`;
}

export function scopedConsoleSnapshotPath(scope: ResourceScope) {
  return scope.kind === 'self'
    ? '/api/console/snapshot'
    : `${adminUserBase(scope.userId)}/console/snapshot`;
}

export function scopedRealtimeTrafficPath(scope: ResourceScope) {
  return scope.kind === 'self'
    ? '/api/traffic/realtime'
    : `${adminUserBase(scope.userId)}/traffic/realtime`;
}

export type EventStreamScope = ResourceScope | { kind: 'admin-global' };

export function scopedEventStreamPath(scope: EventStreamScope) {
  if (scope.kind === 'admin-global') return '/api/admin/events';
  return scope.kind === 'self'
    ? '/api/events'
    : `${adminUserBase(scope.userId)}/events`;
}

export function scopedClientsPath(scope: ResourceScope) {
  return scope.kind === 'self'
    ? '/api/clients'
    : `${adminUserBase(scope.userId)}/clients`;
}

export function scopedClientPath(scope: ResourceScope, clientId: string, suffix = '') {
  return `${scopedClientsPath(scope)}/${encodePath(clientId)}${suffix}`;
}

function scopedTunnelsPath(scope: ResourceScope) {
  return scope.kind === 'self'
    ? '/api/tunnels'
    : `${adminUserBase(scope.userId)}/tunnels`;
}

export const scopedClientApi = {
  list(scope: ResourceScope) {
    return api.get<Client[]>(scopedClientsPath(scope));
  },

  delete(scope: ResourceScope, clientId: string) {
    return api.delete<void>(scopedClientPath(scope, clientId));
  },

  updateDisplayName(scope: ResourceScope, clientId: string, displayName: string) {
    return api.put<void>(scopedClientPath(scope, clientId, '/display-name'), {
      display_name: displayName,
    });
  },

  updateBandwidth(scope: ResourceScope, clientId: string, body: { ingress_bps: number; egress_bps: number }) {
    return api.put<void>(scopedClientPath(scope, clientId, '/bandwidth-settings'), body);
  },

  listTunnels(scope: ResourceScope, clientId: string, role: TunnelClientRole = 'owner') {
    const params = new URLSearchParams({ role });
    return api.get<ProxyConfig[]>(`${scopedClientPath(scope, clientId, '/tunnels')}?${params.toString()}`);
  },

  versionCheck(scope: ResourceScope, clientId: string, force = false) {
    return api.get(`${scopedClientPath(scope, clientId, '/version/check')}${force ? '?force=true' : ''}`);
  },
};

function scopedKeysPath(scope: ResourceScope) {
  return scope.kind === 'self'
    ? '/api/keys'
    : `${adminUserBase(scope.userId)}/keys`;
}

export const scopedKeyApi = {
  list(scope: ResourceScope) {
    return api.get<APIKey[]>(scopedKeysPath(scope));
  },

  create(scope: ResourceScope, body: { name: string; permissions?: string[]; max_uses?: number; expires_in?: string }) {
    return api.post<{ key: APIKey; raw_key: string; server_addr: string }>(scopedKeysPath(scope), body);
  },

  enable(scope: ResourceScope, keyId: string) {
    return api.put<void>(`${scopedKeysPath(scope)}/${encodePath(keyId)}/enable`);
  },

  disable(scope: ResourceScope, keyId: string) {
    return api.put<void>(`${scopedKeysPath(scope)}/${encodePath(keyId)}/disable`);
  },

  delete(scope: ResourceScope, keyId: string) {
    return api.delete<void>(`${scopedKeysPath(scope)}/${encodePath(keyId)}`);
  },
};

export const tunnelApi = {
  listByClientRole(scope: ResourceScope, clientId: string, role: TunnelClientRole = 'owner') {
    return scopedClientApi.listTunnels(scope, clientId, role);
  },

  create(scope: ResourceScope, body: TunnelCreateRequest) {
    return api.post<TunnelMutationResponse>(scopedTunnelsPath(scope), body);
  },

  update(scope: ResourceScope, tunnelId: string, body: TunnelUpdateRequest) {
    return api.put<TunnelMutationResponse>(`${scopedTunnelsPath(scope)}/${encodePath(tunnelId)}`, body);
  },

  migrate(scope: ResourceScope, tunnelId: string, body: TunnelMigrateRequest) {
    return api.post<TunnelMutationResponse>(`${scopedTunnelsPath(scope)}/${encodePath(tunnelId)}/migrate`, body);
  },

  resume(scope: ResourceScope, tunnelId: string) {
    return api.put<TunnelMutationResponse>(`${scopedTunnelsPath(scope)}/${encodePath(tunnelId)}/resume`);
  },

  stop(scope: ResourceScope, tunnelId: string) {
    return api.put<TunnelMutationResponse>(`${scopedTunnelsPath(scope)}/${encodePath(tunnelId)}/stop`);
  },

  delete(scope: ResourceScope, tunnelId: string) {
    return api.delete<TunnelMutationResponse>(`${scopedTunnelsPath(scope)}/${encodePath(tunnelId)}`);
  },
};

export interface UserListQuery {
  limit?: number;
  cursor?: string;
  query?: string;
  status?: 'active' | 'disabled';
  isAdmin?: boolean;
}

const userDeletionImpactCountFields = [
  'api_keys',
  'clients',
  'tunnels',
  'traffic_buckets',
  'activity_events',
] as const satisfies readonly (keyof UserDeletionImpact)[];
const rfc3339TimestampPattern = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/;

export function parseUserDeletionImpact(value: unknown, expectedUserId?: string): UserDeletionImpact {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(i18n.t('users.deletionImpactInvalid'));
  }
  const candidate = value as Record<string, unknown>;
  const countsAreValid = userDeletionImpactCountFields.every((field) => (
    typeof candidate[field] === 'number'
    && Number.isSafeInteger(candidate[field])
    && Number(candidate[field]) >= 0
  ));
  const userId = candidate.user_id;
  const generatedAt = candidate.generated_at;
  if (
    !countsAreValid
    || typeof userId !== 'string'
    || userId.trim().length === 0
    || (expectedUserId !== undefined && userId !== expectedUserId)
    || typeof generatedAt !== 'string'
    || !rfc3339TimestampPattern.test(generatedAt)
    || !Number.isFinite(Date.parse(generatedAt))
  ) {
    throw new Error(i18n.t('users.deletionImpactInvalid'));
  }
  return candidate as unknown as UserDeletionImpact;
}

export const usersApi = {
  list(query: UserListQuery = {}) {
    const params = new URLSearchParams();
    if (query.limit) params.set('limit', String(query.limit));
    if (query.cursor) params.set('cursor', query.cursor);
    if (query.query) params.set('query', query.query);
    if (query.status) params.set('status', query.status);
    if (query.isAdmin !== undefined) params.set('is_admin', String(query.isAdmin));
    const suffix = params.size > 0 ? `?${params.toString()}` : '';
    return api.get<UserListResponse>(`/api/admin/users${suffix}`);
  },

  get(userId: string) {
    return api.get<ManagedUser>(`${adminUserBase(userId)}`);
  },

  async deletionImpact(userId: string) {
    const response = await api.get<unknown>(`${adminUserBase(userId)}/deletion-impact`);
    return parseUserDeletionImpact(response, userId);
  },

  create(body: { username: string; password: string }) {
    return api.post<ManagedUser>('/api/admin/users', body);
  },

  updateUsername(userId: string, username: string) {
    return api.put<ManagedUser>(`${adminUserBase(userId)}/username`, { username });
  },

  updatePassword(userId: string, password: string) {
    return api.put<void>(`${adminUserBase(userId)}/password`, { password });
  },

  setAdmin(userId: string, isAdmin: boolean) {
    return api.put<ManagedUser>(`${adminUserBase(userId)}/admin`, { is_admin: isAdmin });
  },

  disable(userId: string) {
    return api.post<ManagedUser>(`${adminUserBase(userId)}/disable`);
  },

  enable(userId: string) {
    return api.post<ManagedUser>(`${adminUserBase(userId)}/enable`);
  },

  delete(userId: string) {
    return api.delete<void>(`${adminUserBase(userId)}`);
  },

  revokeSessions(userId: string) {
    return api.post<void>(`${adminUserBase(userId)}/sessions/revoke`);
  },
};

export const authApi = {
  me() {
    return api.get<Principal>('/api/auth/me');
  },
};

export type ActivityReadScope = ResourceScope | {
  kind: 'admin-global';
  userId?: string;
};

function activityBasePath(scope: ActivityReadScope) {
  if (scope.kind === 'self') return '/api/activity';
  if (scope.kind === 'admin-user') return `${adminUserBase(scope.userId)}/activity`;
  return '/api/admin/activity';
}

export function buildActivityURL(query: ActivityQuery = {}, readScope: ActivityReadScope = { kind: 'self' }) {
  const params = new URLSearchParams();
  const scope = query.scope ?? 'global';
  params.set('scope', scope);
  if (scope === 'client' && query.scopeId) params.set('client_id', query.scopeId);
  if (scope === 'tunnel' && query.scopeId) params.set('tunnel_id', query.scopeId);
  if (query.before) params.set('before', String(query.before));
  if (query.after) params.set('after', String(query.after));
  if (query.limit) params.set('limit', String(query.limit));
  for (const severity of query.severities ?? []) params.append('severity', severity);
  for (const category of query.categories ?? []) params.append('category', category);
  if (query.from) params.set('from', query.from);
  if (query.to) params.set('to', query.to);
  if (readScope.kind === 'admin-global' && readScope.userId) params.set('user_id', readScope.userId);
  return `${activityBasePath(readScope)}?${params.toString()}`;
}

export const activityApi = {
  list(readScope: ActivityReadScope, query: ActivityQuery = {}) {
    return api.get<ActivityPage>(buildActivityURL(query, readScope));
  },
  recovery(readScope: ActivityReadScope, after: number, limit = 200) {
    return api.get<ActivityPage>(buildActivityURL({
      scope: 'global', after, limit,
      severities: ['debug', 'info', 'warning', 'error'],
    }, readScope));
  },
};

export const webhookApi = {
  catalog() {
    return api.get<WebhookCatalog>('/api/webhooks/catalog');
  },

  async list() {
    const items = await api.get<ActivityWebhookAPI[]>('/api/webhooks');
    return items.map(activityWebhookFromAPI);
  },

  async create(config: ActivityWebhookConfig) {
    const item = await api.post<ActivityWebhookAPI>('/api/webhooks', activityWebhookToAPI(config));
    return activityWebhookFromAPI(item);
  },

  async update(config: ActivityWebhookConfig) {
    const item = await api.put<ActivityWebhookAPI>(`/api/webhooks/${encodePath(config.id)}`, activityWebhookToAPI(config));
    return activityWebhookFromAPI(item);
  },

  async setEnabled(webhookId: string, enabled: boolean) {
    const item = await api.put<ActivityWebhookAPI>(`/api/webhooks/${encodePath(webhookId)}/enabled`, { enabled });
    return activityWebhookFromAPI(item);
  },

  delete(webhookId: string) {
    return api.delete<void>(`/api/webhooks/${encodePath(webhookId)}`);
  },

  preview(config: ActivityWebhookConfig, event: WebhookEventKey) {
    return api.post<WebhookPreviewAPI>('/api/webhooks/preview', { config: activityWebhookToAPI(config), event });
  },

  async test(config: ActivityWebhookConfig, event: WebhookEventKey) {
    const delivery = await api.post<WebhookInvocationAPI>('/api/webhooks/test', { config: activityWebhookToAPI(config), event });
    return webhookInvocationFromAPI(delivery);
  },

  async deliveries(webhookId: string, status?: WebhookInvocationStatus, cursor?: string) {
    const params = new URLSearchParams({ limit: '100' });
    if (status) params.set('status', status);
    if (cursor) params.set('cursor', cursor);
    const page = await api.get<WebhookDeliveryPageAPI>(`/api/webhooks/${encodePath(webhookId)}/deliveries?${params.toString()}`);
    return { ...page, items: page.items.map(webhookInvocationFromAPI) };
  },

  async delivery(deliveryId: string) {
    const delivery = await api.get<WebhookInvocationAPI>(`/api/webhook-deliveries/${encodePath(deliveryId)}`);
    return webhookInvocationFromAPI(delivery);
  },

  async replay(deliveryId: string): Promise<WebhookInvocation> {
    const delivery = await api.post<WebhookInvocationAPI>(`/api/webhook-deliveries/${encodePath(deliveryId)}/replay`);
    return webhookInvocationFromAPI(delivery);
  },
};
export { ApiError };
