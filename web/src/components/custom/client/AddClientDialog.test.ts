import { describe, expect, test } from 'bun:test';

import { resolveAddClientServiceAddress } from './client-service-address';

describe('resolveAddClientServiceAddress', () => {
  test('prefers the effective service address for env-locked configs', () => {
    expect(resolveAddClientServiceAddress({
      effectiveServerAddr: 'https://Locked.EXAMPLE.com:443/',
      adminServerAddr: 'http://localhost',
      keyServerAddr: 'https://key.example.com',
      statusServerAddr: 'https://status.example.com',
      browserOrigin: 'https://browser.example.com',
    })).toBe('https://locked.example.com');
  });

  test('skips legacy websocket-form values and falls back to an http service address', () => {
    expect(resolveAddClientServiceAddress({
      effectiveServerAddr: 'wss://legacy.example.com',
      adminServerAddr: '',
      keyServerAddr: 'https://key.example.com',
      statusServerAddr: 'https://status.example.com',
      browserOrigin: 'https://browser.example.com',
    })).toBe('https://key.example.com');
  });
});
