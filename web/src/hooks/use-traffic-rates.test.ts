import { describe, expect, test } from 'bun:test';

import type { ClientTrafficResponse, ProxyConfig } from '@/types';

import { buildAggregatedTrafficRates, hasTrafficSamples } from './use-traffic-rates';

function isoAt(minuteOffset: number) {
  return new Date(Date.UTC(2026, 3, 19, 10, minuteOffset, 0)).toISOString();
}

const tunnelFilter: Pick<ProxyConfig, 'name' | 'type'>[] = [
  { name: 'api', type: 'tcp' },
];

describe('buildAggregatedTrafficRates', () => {
  test('fills a full 60-point 1h series with zero-rate gaps', () => {
    const data: ClientTrafficResponse = {
      resolution: 'minute',
      items: [
        {
          tunnel_name: 'api',
          tunnel_type: 'tcp',
          points: [
            {
              bucket_start: isoAt(57),
              ingress_bytes: 120,
              egress_bytes: 60,
              total_bytes: 180,
            },
            {
              bucket_start: isoAt(59),
              ingress_bytes: 240,
              egress_bytes: 180,
              total_bytes: 420,
            },
          ],
        },
      ],
    };

    const points = buildAggregatedTrafficRates(data, '1h', undefined, Date.UTC(2026, 3, 19, 11, 0, 0));

    expect(points).toHaveLength(60);
    expect(points[57]).toEqual({
      timestamp: Date.UTC(2026, 3, 19, 10, 57, 0),
      inRate: 2,
      outRate: 1,
    });
    expect(points[58]).toEqual({
      timestamp: Date.UTC(2026, 3, 19, 10, 58, 0),
      inRate: 0,
      outRate: 0,
    });
    expect(points[59]).toEqual({
      timestamp: Date.UTC(2026, 3, 19, 10, 59, 0),
      inRate: 4,
      outRate: 3,
    });
  });

  test('aggregates multiple tunnels into client-level rates', () => {
    const data: ClientTrafficResponse = {
      resolution: 'minute',
      items: [
        {
          tunnel_name: 'api',
          tunnel_type: 'tcp',
          points: [
            {
              bucket_start: isoAt(59),
              ingress_bytes: 120,
              egress_bytes: 60,
              total_bytes: 180,
            },
          ],
        },
        {
          tunnel_name: 'web',
          tunnel_type: 'http',
          points: [
            {
              bucket_start: isoAt(59),
              ingress_bytes: 180,
              egress_bytes: 120,
              total_bytes: 300,
            },
          ],
        },
      ],
    };

    const points = buildAggregatedTrafficRates(data, '1h', undefined, Date.UTC(2026, 3, 19, 11, 0, 0));

    expect(points.at(-1)).toEqual({
      timestamp: Date.UTC(2026, 3, 19, 10, 59, 0),
      inRate: 5,
      outRate: 3,
    });
  });

  test('filters to a single tunnel when a tunnel list is provided', () => {
    const data: ClientTrafficResponse = {
      resolution: 'minute',
      items: [
        {
          tunnel_name: 'api',
          tunnel_type: 'tcp',
          points: [
            {
              bucket_start: isoAt(59),
              ingress_bytes: 300,
              egress_bytes: 120,
              total_bytes: 420,
            },
          ],
        },
        {
          tunnel_name: 'web',
          tunnel_type: 'http',
          points: [
            {
              bucket_start: isoAt(59),
              ingress_bytes: 600,
              egress_bytes: 240,
              total_bytes: 840,
            },
          ],
        },
      ],
    };

    const points = buildAggregatedTrafficRates(data, '1h', tunnelFilter, Date.UTC(2026, 3, 19, 11, 0, 0));

    expect(points.at(-1)).toEqual({
      timestamp: Date.UTC(2026, 3, 19, 10, 59, 0),
      inRate: 5,
      outRate: 2,
    });
  });

  test('keeps historical tunnel data when no tunnel filter is provided', () => {
    const data: ClientTrafficResponse = {
      resolution: 'minute',
      items: [
        {
          tunnel_name: 'deleted-tunnel',
          tunnel_type: 'tcp',
          points: [
            {
              bucket_start: isoAt(59),
              ingress_bytes: 600,
              egress_bytes: 300,
              total_bytes: 900,
            },
          ],
        },
      ],
    };

    const points = buildAggregatedTrafficRates(data, '1h', undefined, Date.UTC(2026, 3, 19, 11, 0, 0));

    expect(points.at(-1)).toEqual({
      timestamp: Date.UTC(2026, 3, 19, 10, 59, 0),
      inRate: 10,
      outRate: 5,
    });
  });

  test('reports no samples for a successful response with zero points', () => {
    const data: ClientTrafficResponse = {
      resolution: 'minute',
      items: [
        {
          tunnel_name: 'api',
          tunnel_type: 'tcp',
          points: [],
        },
      ],
    };

    expect(hasTrafficSamples(data)).toBe(false);
    expect(hasTrafficSamples(data, tunnelFilter)).toBe(false);
  });
});
