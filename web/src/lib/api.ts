/**
 * 统一 API 请求器
 * 所有业务代码通过此模块发起 HTTP 请求，不直接使用 fetch
 *
 * P5: 认证凭证通过 httpOnly cookie 自动传递（credentials: 'same-origin'），
 * 不再需要手动管理 Authorization header。API 编程调用者仍可通过 header 传递 token。
 */

import { useAuthStore } from '@/stores/auth-store';
import { i18n } from '@/i18n';
import type {
  ProxyConfig,
  TunnelClientRole,
  ActivityPage,
  ActivityQuery,
  TunnelCreateRequest,
  TunnelMigrateRequest,
  TunnelMutationResponse,
  TunnelUpdateRequest,
} from '@/types';

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
      useAuthStore.getState().logout();
      if (typeof window !== 'undefined' && !window.location.hash.startsWith('#/login')) {
        window.location.hash = '#/login';
      }
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

export const tunnelApi = {
  listByClientRole(clientId: string, role: TunnelClientRole = 'owner') {
    const params = new URLSearchParams({ role });
    return api.get<ProxyConfig[]>(`/api/clients/${encodePath(clientId)}/tunnels?${params.toString()}`);
  },

  create(body: TunnelCreateRequest) {
    return api.post<TunnelMutationResponse>('/api/tunnels', body);
  },

  update(tunnelId: string, body: TunnelUpdateRequest) {
    return api.put<TunnelMutationResponse>(`/api/tunnels/${encodePath(tunnelId)}`, body);
  },

  migrate(tunnelId: string, body: TunnelMigrateRequest) {
    return api.post<TunnelMutationResponse>(`/api/tunnels/${encodePath(tunnelId)}/migrate`, body);
  },

  resume(tunnelId: string) {
    return api.put<TunnelMutationResponse>(`/api/tunnels/${encodePath(tunnelId)}/resume`);
  },

  stop(tunnelId: string) {
    return api.put<TunnelMutationResponse>(`/api/tunnels/${encodePath(tunnelId)}/stop`);
  },

  delete(tunnelId: string) {
    return api.delete<TunnelMutationResponse>(`/api/tunnels/${encodePath(tunnelId)}`);
  },
};


export function buildActivityURL(query: ActivityQuery = {}) {
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
  return `/api/activity?${params.toString()}`;
}

export const activityApi = {
  list(query: ActivityQuery = {}) {
    return api.get<ActivityPage>(buildActivityURL(query));
  },
  recovery(after: number, limit = 200) {
    return api.get<ActivityPage>(buildActivityURL({
      scope: 'global', after, limit,
      severities: ['debug', 'info', 'warning', 'error'],
    }));
  },
};
export { ApiError };
