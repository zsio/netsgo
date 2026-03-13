import { useEffect, useRef, useState } from 'react';
import type { Agent } from '@/types';

interface NetSpeed {
  upload: number;
  download: number;
}

export function useNetSpeed(agent: Agent): NetSpeed {
  const previousRef = useRef<{
    sent: number;
    recv: number;
    time: number;
  } | null>(null);
  const [speed, setSpeed] = useState<NetSpeed>({ upload: 0, download: 0 });

  useEffect(() => {
    if (!agent.stats) {
      return;
    }

    const now = performance.now();
    const previous = previousRef.current;

    if (previous && previous.sent <= agent.stats.net_sent && previous.recv <= agent.stats.net_recv) {
      const elapsed = (now - previous.time) / 1000;
      if (elapsed > 0.5) {
        setSpeed({
          upload: (agent.stats.net_sent - previous.sent) / elapsed,
          download: (agent.stats.net_recv - previous.recv) / elapsed,
        });
      }
    }

    previousRef.current = {
      sent: agent.stats.net_sent,
      recv: agent.stats.net_recv,
      time: now,
    };
  }, [agent.stats]);

  return speed;
}
