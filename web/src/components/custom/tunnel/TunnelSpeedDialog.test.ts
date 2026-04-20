import { describe, expect, test } from 'bun:test';
import {
  Children,
  isValidElement,
  type ReactElement,
  type ReactNode,
} from 'react';

import { TrafficRateChart } from '@/components/custom/chart/TrafficRateChart';

import { TunnelSpeedDialog } from './TunnelSpeedDialog';

function findElement(node: ReactNode, matcher: (element: ReactElement) => boolean): ReactElement | null {
  if (!isValidElement(node)) {
    return null;
  }

  if (matcher(node)) {
    return node;
  }

  for (const child of Children.toArray(node.props.children)) {
    const match = findElement(child, matcher);
    if (match) {
      return match;
    }
  }

  return null;
}

describe('TunnelSpeedDialog', () => {
  test('renders the traffic rate chart for the selected tunnel', () => {
    const element = TunnelSpeedDialog({
      tunnel: {
        name: 'api',
        type: 'tcp',
        local_ip: '127.0.0.1',
        local_port: 3000,
        remote_port: 18080,
        domain: '',
        client_id: 'client-1',
        ingress_bps: 0,
        egress_bps: 0,
        desired_state: 'running',
        runtime_state: 'exposed',
        capabilities: {
          can_resume: false,
          can_stop: true,
          can_edit: true,
          can_delete: true,
        },
      },
      clientId: 'client-1',
      open: true,
      onOpenChange: () => {},
    });

    const rateChart = findElement(element, (candidate) => candidate.type === TrafficRateChart);

    expect(rateChart).not.toBeNull();
    expect(rateChart?.props.clientId).toBe('client-1');
    expect(rateChart?.props.tunnelFilter).toHaveLength(1);
    expect(rateChart?.props.tunnelFilter?.[0]).toMatchObject({ name: 'api', type: 'tcp' });
  });
});
