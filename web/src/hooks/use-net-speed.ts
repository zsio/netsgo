import { useRef } from 'react';
import type { Agent } from '@/types';

interface NetSpeed {
  upload: number;   // bytes/s
  download: number; // bytes/s
}

/**
 * 计算网络 I/O 实时速率
 * 基于前后两次 stats 的 net_sent / net_recv 差值
 */
export function useNetSpeed(agent: Agent): NetSpeed {
  const prevRef = useRef<{
    sent: number;
    recv: number;
    time: number;
  } | null>(null);

  const speedRef = useRef<NetSpeed>({ upload: 0, download: 0 });

  if (agent.stats) {
    const now = Date.now();
    const prev = prevRef.current;

    if (prev && prev.sent <= agent.stats.net_sent) {
      const elapsed = (now - prev.time) / 1000; // seconds
      if (elapsed > 0.5) { // 至少 0.5s 间隔才有意义
        speedRef.current = {
          upload: (agent.stats.net_sent - prev.sent) / elapsed,
          download: (agent.stats.net_recv - prev.recv) / elapsed,
        };
        prevRef.current = {
          sent: agent.stats.net_sent,
          recv: agent.stats.net_recv,
          time: now,
        };
      }
    } else {
      // 首次或数据重置
      prevRef.current = {
        sent: agent.stats.net_sent,
        recv: agent.stats.net_recv,
        time: now,
      };
    }
  }

  return speedRef.current;
}
