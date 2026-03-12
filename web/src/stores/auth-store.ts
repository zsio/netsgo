import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { AdminUser } from '@/types';

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
      logout: () => set({ token: null, user: null }),
    }),
    {
      name: 'netsgo-auth', // 存入 localStorage 的 key
    }
  )
);
