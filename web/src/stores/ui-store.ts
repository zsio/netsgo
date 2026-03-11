import { create } from 'zustand';

interface UIState {
  /** 当前选中的 Agent ID */
  selectedAgentId: string | null;
  setSelectedAgentId: (id: string | null) => void;

  /** 侧边栏分组折叠状态 */
  expandedGroups: Record<string, boolean>;
  toggleGroup: (group: string) => void;
}

export const useUIStore = create<UIState>((set) => ({
  selectedAgentId: null,
  setSelectedAgentId: (id) => set({ selectedAgentId: id }),

  expandedGroups: {
    active: true,
    offline: true,
  },
  toggleGroup: (group) =>
    set((state) => ({
      expandedGroups: {
        ...state.expandedGroups,
        [group]: !state.expandedGroups[group],
      },
    })),
}));
