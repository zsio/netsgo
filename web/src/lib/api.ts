/**
 * 统一 API 请求器
 * 所有业务代码通过此模块发起 HTTP 请求，不直接使用 fetch
 */

class ApiError extends Error {
  status: number;
  statusText: string;

  constructor(
    status: number,
    statusText: string,
    message?: string,
  ) {
    super(message || `API Error: ${status} ${statusText}`);
    this.name = "ApiError";
    this.status = status;
    this.statusText = statusText;
  }
}

async function request<T>(
  url: string,
  options?: RequestInit,
): Promise<T> {
  let token = '';
  try {
    const authStoreStr = localStorage.getItem('netsgo-auth');
    if (authStoreStr) {
      const state = JSON.parse(authStoreStr).state;
      if (state && state.token) {
        token = state.token;
      }
    }
  } catch (e) {
    console.error('Failed to parse auth store from local storage', e);
  }

  const headers = new Headers({
    "Content-Type": "application/json",
    ...options?.headers,
  });

  if (token && !headers.has('Authorization')) {
    headers.set('Authorization', `Bearer ${token}`);
  }

  const res = await fetch(url, {
    ...options,
    headers,
  });

  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new ApiError(res.status, res.statusText, body || undefined);
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
