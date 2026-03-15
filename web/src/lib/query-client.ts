import { QueryClient } from "@tanstack/react-query";

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 2,
      retryDelay: (attempt) => Math.min(1000 * 2 ** attempt, 10000),
      staleTime: 5000,
      // 嵌入式面板：API 就是 self，网络中断时仍然尝试请求
      networkMode: "always",
    },
    mutations: {
      networkMode: "always",
    },
  },
});

// 清理历史遗留的 localStorage 缓存键
try {
  localStorage.removeItem("netsgo:query-cache");
  localStorage.removeItem("netsgo:net-speed");
  localStorage.removeItem("netsgo:net-speed-baseline");
  localStorage.removeItem("netsgo:clients-cache:v1");
} catch {
  /* ignore */
}
