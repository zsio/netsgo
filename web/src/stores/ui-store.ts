import { create } from 'zustand';

interface UIState {
  /** 侧边栏搜索关键字 */
  sidebarSearch: string;
  setSidebarSearch: (q: string) => void;
}

export const useUIStore = create<UIState>((set) => ({
  sidebarSearch: '',
  setSidebarSearch: (q) => set({ sidebarSearch: q }),
}));
