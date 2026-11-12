import { describe, expect, test } from 'vitest';

import type { ClientTrafficResponse, TrafficRealtimeEvent } from '@/types';
import {
  buildGlobalHistoricalTrafficSummary,
  buildGlobalRealtimeTrafficSummary,
  computeSymmetricTrafficTicks,
  computeTrafficAxisTicks,
} from './dashboard-traffic-summary';

describe('buildGlobalRealtimeTrafficSummary', () => {
  test('aggregates 60s buckets across online clients and deduplicates shared C2C series', () => {
    const baseSec = Date.parse('2026-11-12T05:20:00Z');
    const event: TrafficRealtimeEvent = {
      resolution: 'second',
      clients: [
        {
          client_id: 'cli-a',
          resolution: 'second',
          items: [
            {
              tunnel_id: 'tun-web',
              tunnel_name: 'web-portal',
              tunnel_type: 'http',
              points: [
                {
                  bucket_start: new Date(baseSec - 1000).toISOString(),
                  ingress_bytes: 2048,
                  egress_bytes: 1024,
                  total_bytes: 3072,
                },
                {
                  bucket_start: new Date(baseSec).toISOString(),
                  ingress_bytes: 4096,
                  egress_bytes: 2048,
                  total_bytes: 6144,
                },
              ],
            },
            {
              tunnel_id: 'tun-c2c',
              tunnel_name: 'pg-mesh',
              tunnel_type: 'tcp',
              points: [
                {
                  bucket_start: new Date(baseSec).toISOString(),
                  ingress_bytes: 1000,
                  egress_bytes: 500,
                  total_bytes: 1500,
                },
              ],
            },
          ],
        },
        {
          client_id: 'cli-b',
          resolution: 'second',
          items: [
            {
              tunnel_id: 'tun-c2c',
              tunnel_name: 'pg-mesh',
              tunnel_type: 'tcp',
              points: [
                {
                  bucket_start: new Date(baseSec).toISOString(),
                  ingress_bytes: 1000,
                  egress_bytes: 500,
                  total_bytes: 1500,
                },
              ],
            },
          ],
        },
        {
          client_id: 'cli-offline',
          resolution: 'second',
          items: [
            {
              tunnel_id: 'tun-stale',
              tunnel_name: 'stale-svc',
              tunnel_type: 'tcp',
              points: [
                {
                  bucket_start: new Date(baseSec).toISOString(),
                  ingress_bytes: 999999,
                  egress_bytes: 999999,
                  total_bytes: 1999998,
                },
              ],
            },
          ],
        },
      ],
    };

    const summary = buildGlobalRealtimeTrafficSummary(
      event,
      new Set(['cli-a', 'cli-b']),
      baseSec + 200,
    );

    expect(summary.series).toHaveLength(60);
    // Shared tun-c2c counted once (4096 + 1000 = 5096; 2048 + 500 = 2548), offline client ignored
    expect(summary.currentInRate).toBe(5096);
    expect(summary.currentOutRate).toBe(2548);
    expect(summary.series[59]?.negInRate).toBe(-5096);
    expect(summary.series[59]?.negOutRate).toBe(-2548);
    expect(summary.peakInRate).toBe(5096);
    expect(summary.peakOutRate).toBe(2548);
    expect(summary.totalInBytes).toBe(7144);
    expect(summary.totalOutBytes).toBe(3572);
    expect(summary.topTunnelName).toBe('web-portal');
  });

  test('returns a calm 60-point zero baseline when no traffic samples exist', () => {
    const nowMs = Date.parse('2026-11-12T05:25:10Z');
    const summary = buildGlobalRealtimeTrafficSummary(undefined, new Set(), nowMs);

    expect(summary.series).toHaveLength(60);
    expect(summary.currentInRate).toBe(0);
    expect(summary.currentOutRate).toBe(0);
    expect(summary.series[59]?.negOutRate).toBe(0);
    expect(summary.topTunnelName).toBeNull();
  });

  test('aggregates historical 1h buckets and deduplicates shared C2C raw points', () => {
    const nowMs = Date.parse('2026-11-12T06:00:00Z');
    const sampleIso = new Date(nowMs - 60_000).toISOString();
    const responses: ClientTrafficResponse[] = [
      {
        resolution: 'minute',
        items: [
          {
            tunnel_id: 'tun-c2c',
            tunnel_name: 'mesh-db',
            tunnel_type: 'tcp',
            points: [
              {
                bucket_start: sampleIso,
                ingress_bytes: 6000,
                egress_bytes: 3000,
                total_bytes: 9000,
              },
            ],
          },
        ],
      },
      {
        resolution: 'minute',
        items: [
          {
            tunnel_id: 'tun-c2c',
            tunnel_name: 'mesh-db',
            tunnel_type: 'tcp',
            points: [
              {
                bucket_start: sampleIso,
                ingress_bytes: 6000,
                egress_bytes: 3000,
                total_bytes: 9000,
              },
            ],
          },
        ],
      },
    ];

    const summary = buildGlobalHistoricalTrafficSummary(responses, '1h', nowMs);
    expect(summary.series).toHaveLength(60);
    expect(summary.peakInRate).toBe(100); // 6000 bytes / 60s
    expect(summary.peakOutRate).toBe(50); // 3000 bytes / 60s
    expect(summary.totalInBytes).toBe(6000);
    expect(summary.totalOutBytes).toBe(3000);
    expect(summary.topTunnelName).toBe('mesh-db');
  });

  test('computes evenly spaced binary-aligned Y-axis ticks for positive and mirrored axes', () => {
    expect(computeTrafficAxisTicks(0)).toEqual([0, 1024, 2048, 3072]);
    expect(computeTrafficAxisTicks(5096)).toEqual([0, 2048, 4096, 6144]);
    expect(computeSymmetricTrafficTicks(0)).toEqual([-2048, -1024, 0, 1024, 2048]);
    expect(computeSymmetricTrafficTicks(5096)).toEqual([-10240, -5120, 0, 5120, 10240]);
  });
});
