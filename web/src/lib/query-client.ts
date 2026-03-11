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
