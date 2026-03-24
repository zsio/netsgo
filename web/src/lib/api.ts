/**
 * 统一 API 请求器
 * 所有业务代码通过此模块发起 HTTP 请求，不直接使用 fetch
 *
 * P5: 认证凭证通过 httpOnly cookie 自动传递（credentials: 'same-origin'），
 * 不再需要手动管理 Authorization header。API 编程调用者仍可通过 header 传递 token。
 */

import { useAuthStore } from '@/stores/auth-store';

class ApiError extends Error {
  status: number;
  statusText: string;
  body?: unknown;

  constructor(
    status: number,
    statusText: string,
    message?: string,
    body?: unknown,
  ) {
    super(message || `API Error: ${status} ${statusText}`);
    this.name = "ApiError";
    this.status = status;
    this.statusText = statusText;
    this.body = body;
  }
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
    if (res.status === 401) {
      useAuthStore.getState().logout();
      if (typeof window !== 'undefined' && !window.location.hash.startsWith('#/login')) {
        window.location.hash = '#/login';
      }
    }
    const bodyText = await res.text().catch(() => "");
    let errorBody: unknown;
    let errorMessage = bodyText || undefined;
    try {
      if (bodyText) {
        const json = JSON.parse(bodyText);
        errorBody = json;
        if (json && typeof json.error === 'string') {
          errorMessage = json.error;
        } else if (json && typeof json.message === 'string') {
          errorMessage = json.message;
        }
      }
    } catch {
      // not JSON, fallback to raw string
    }

    throw new ApiError(res.status, res.statusText, errorMessage, errorBody);
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

  delete<T>(url: string): Promise<T> {
    return request<T>(url, { method: "DELETE" });
  },
};

export { ApiError };
