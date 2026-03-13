import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { AdminUser } from '@/types';

export const AUTH_STORAGE_KEY = 'netsgo-auth';

interface AuthState {
  token: string | null;
  user: Partial<AdminUser> | null;
  setAuth: (token: string, user: Partial<AdminUser>) => void;
  logout: () => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      token: null,
      user: null,
      setAuth: (token, user) => set({ token, user }),
      logout: () => {
        set({ token: null, user: null });
        clearStoredAuth();
      },
    }),
    {
      name: AUTH_STORAGE_KEY,
    }
  )
);

export function getStoredAuthState(): { token: string | null; user: Partial<AdminUser> | null } {
  if (typeof window === 'undefined') {
    return { token: null, user: null };
  }

  try {
    const raw = window.localStorage.getItem(AUTH_STORAGE_KEY);
    if (!raw) {
      return { token: null, user: null };
    }

    const parsed = JSON.parse(raw) as {
      state?: {
        token?: string | null;
        user?: Partial<AdminUser> | null;
      };
    };

    return {
      token: parsed.state?.token ?? null,
      user: parsed.state?.user ?? null,
    };
  } catch {
    return { token: null, user: null };
  }
}

export function clearStoredAuth() {
  if (typeof window === 'undefined') {
    return;
  }
  window.localStorage.removeItem(AUTH_STORAGE_KEY);
}
