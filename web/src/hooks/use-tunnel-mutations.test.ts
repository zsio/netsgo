import { describe, expect, test } from 'bun:test';

import { ApiError } from '@/lib/api';

import { buildLegacyClientTunnelPath, shouldUseLegacyTunnelEndpoint } from './use-tunnel-mutations';

describe('shouldUseLegacyTunnelEndpoint', () => {
  test('allows legacy fallback for old server-expose endpoints', () => {
    expect(shouldUseLegacyTunnelEndpoint(new ApiError(404, 'Not Found'), 'server_expose')).toBe(true);
    expect(shouldUseLegacyTunnelEndpoint(new ApiError(405, 'Method Not Allowed'))).toBe(true);
  });

  test('does not fallback for client-to-client mutations', () => {
    expect(shouldUseLegacyTunnelEndpoint(new ApiError(404, 'Not Found'), 'client_to_client')).toBe(false);
  });

  test('does not fallback for non endpoint-missing errors', () => {
    expect(shouldUseLegacyTunnelEndpoint(new ApiError(500, 'Internal Server Error'), 'server_expose')).toBe(false);
    expect(shouldUseLegacyTunnelEndpoint(new Error('network failed'), 'server_expose')).toBe(false);
  });

  test('encodes legacy client tunnel path segments', () => {
    expect(buildLegacyClientTunnelPath('client/with?#chars')).toBe('/api/clients/client%2Fwith%3F%23chars/tunnels');
    expect(buildLegacyClientTunnelPath('client/with?#chars', '/tun%2F1/resume')).toBe('/api/clients/client%2Fwith%3F%23chars/tunnels/tun%2F1/resume');
  });
});
