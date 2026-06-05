import { describe, expect, test } from 'bun:test';

import { resolveAddClientServiceAddress } from './client-service-address';
import {
  clientCNBDockerImageForVersion,
  clientDockerImageForVersion,
  clientInstallChannelArgForVersion,
  clientReleaseChannelForVersion,
  hasKnownClientInstallVersion,
} from './client-install-commands';

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

describe('client install command release helpers', () => {
  test('requires a known server version before generating install output', () => {
    expect(hasKnownClientInstallVersion(undefined)).toBe(false);
    expect(hasKnownClientInstallVersion('')).toBe(false);
    expect(hasKnownClientInstallVersion('  ')).toBe(false);
    expect(hasKnownClientInstallVersion('v0.2.0-beta.3')).toBe(true);
  });

  test('defaults to stable without adding an install channel argument', () => {
    expect(clientReleaseChannelForVersion('v0.2.0')).toBe('stable');
    expect(clientReleaseChannelForVersion('dev-snapshot')).toBe('stable');
    expect(clientInstallChannelArgForVersion('v0.2.0')).toBe('');
    expect(clientDockerImageForVersion('v0.2.0')).toBe('zsio/netsgo:latest');
    expect(clientCNBDockerImageForVersion('v0.2.0')).toBe('docker.cnb.cool/zsio/netsgo:latest');
  });

  test('uses beta channel and a matching Docker image tag for beta server versions', () => {
    expect(clientReleaseChannelForVersion('v0.2.0-beta.3')).toBe('beta');
    expect(clientInstallChannelArgForVersion('v0.2.0-beta.3')).toBe('--channel beta');
    expect(clientDockerImageForVersion('v0.2.0-beta.3')).toBe('zsio/netsgo:0.2.0-beta.3');
    expect(clientCNBDockerImageForVersion('v0.2.0-beta.3')).toBe('docker.cnb.cool/zsio/netsgo:0.2.0-beta.3');
  });
});
