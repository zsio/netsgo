import { describe, expect, test } from 'bun:test';

import { getTrafficSeriesKey, getTunnelSeriesKey } from './tunnel-traffic-keys';

describe('tunnel traffic keys', () => {
  test('uses stable tunnel ids when present', () => {
    expect(getTunnelSeriesKey({ id: 'tun-1', name: 'api', type: 'tcp' })).toBe('id:tun-1');
    expect(getTrafficSeriesKey({
      tunnel_id: 'tun-1',
      tunnel_name: 'api',
      tunnel_type: 'tcp',
      points: [],
    })).toBe('id:tun-1');
  });

  test('falls back to type and name only when ids are absent', () => {
    expect(getTunnelSeriesKey({ name: 'api', type: 'tcp' })).toBe('tcp:api');
    expect(getTrafficSeriesKey({
      tunnel_name: 'api',
      tunnel_type: 'tcp',
      points: [],
    })).toBe('tcp:api');
  });
});
