import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { AdminUser } from '@/types';

export const AUTH_STORAGE_KEY = 'netsgo-auth';

interface AuthState {
  user: Partial<AdminUser> | null;
  isAuthenticated: boolean;
  setAuth: (user: Partial<AdminUser>) => void;
  logout: () => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      user: null,
      isAuthenticated: false,
      setAuth: (user) => set({ user, isAuthenticated: true }),
      logout: () => {
        set({ user: null, isAuthenticated: false });
      },
    }),
    {
      name: AUTH_STORAGE_KEY,
    }
  )
);

/**
 * P5: 不再存储 token，仅持久化 user 信息和认证状态。
 * JWT token 通过 httpOnly cookie 传递，JavaScript 无法读取。
 */
export function getStoredAuthState(): { isAuthenticated: boolean; user: Partial<AdminUser> | null } {
  if (typeof window === 'undefined') {
    return { isAuthenticated: false, user: null };
  }

  try {
    const raw = window.localStorage.getItem(AUTH_STORAGE_KEY);
    if (!raw) {
      return { isAuthenticated: false, user: null };
    }

    const parsed = JSON.parse(raw) as {
      state?: {
        isAuthenticated?: boolean;
        user?: Partial<AdminUser> | null;
      };
    };

    return {
      isAuthenticated: parsed.state?.isAuthenticated ?? false,
      user: parsed.state?.user ?? null,
    };
  } catch {
    return { isAuthenticated: false, user: null };
  }
}
