import { describe, expect, test } from 'bun:test';

import { buildActivityURL } from './api';

describe('activity API', () => {
  test('encodes repeated filters and scoped cursor queries', () => {
    const url = buildActivityURL({
      scope: 'client', scopeId: 'client/a', after: 41, limit: 17,
      severities: ['debug', 'error'], categories: ['p2p', 'tunnel'],
      from: '2026-07-01T00:00:00Z', to: '2026-07-02T00:00:00Z',
    });
    const parsed = new URL(url, 'http://netsgo.local');
    expect(parsed.pathname).toBe('/api/activity');
    expect(parsed.searchParams.get('client_id')).toBe('client/a');
    expect(parsed.searchParams.getAll('severity')).toEqual(['debug', 'error']);
    expect(parsed.searchParams.getAll('category')).toEqual(['p2p', 'tunnel']);
    expect(parsed.searchParams.get('after')).toBe('41');
  });

  test('uses global scope when omitted', () => {
    const parsed = new URL(buildActivityURL(), 'http://netsgo.local');
    expect(parsed.searchParams.get('scope')).toBe('global');
  });
});
