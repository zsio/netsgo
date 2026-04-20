import { describe, expect, test } from 'bun:test';

import { buildClientTrafficQueryKey, buildClientTrafficUrl } from './use-client-traffic';

describe('buildClientTrafficQueryKey', () => {
  test('includes an empty tunnel slot for client-level traffic', () => {
    expect([...buildClientTrafficQueryKey('client-1', '24h')]).toEqual([
      'client-traffic',
      'client-1',
      '24h',
      '',
    ]);
  });

  test('separates single-tunnel traffic cache entries', () => {
    expect([...buildClientTrafficQueryKey('client-1', '24h', { tunnel: 'api' })]).toEqual([
      'client-traffic',
      'client-1',
      '24h',
      'api',
    ]);
  });
});

describe('buildClientTrafficUrl', () => {
  test('omits tunnel query parameter for client-level traffic', () => {
    const url = buildClientTrafficUrl('client-1', '24h', {}, 1_800_000);

    expect(url).toBe('/api/clients/client-1/traffic?from=1713600&to=1800000&resolution=minute');
    expect(url).not.toContain('tunnel=');
  });

  test('adds an encoded tunnel query parameter for single-tunnel traffic', () => {
    const url = buildClientTrafficUrl('client-1', '24h', { tunnel: 'api edge/1' }, 1_800_000);
    const parsed = new URL(url, 'https://netsgo.test');

    expect(parsed.pathname).toBe('/api/clients/client-1/traffic');
    expect(parsed.searchParams.get('from')).toBe('1713600');
    expect(parsed.searchParams.get('to')).toBe('1800000');
    expect(parsed.searchParams.get('resolution')).toBe('minute');
    expect(parsed.searchParams.get('tunnel')).toBe('api edge/1');
  });
});
