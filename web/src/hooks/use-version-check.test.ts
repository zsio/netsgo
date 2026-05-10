import { describe, expect, test } from 'bun:test';

import { versionCheckQueryKey } from './use-version-check';

describe('versionCheckQueryKey', () => {
  test('includes target, version, install method, os and arch', () => {
    expect([...versionCheckQueryKey({
      kind: 'client',
      id: 'client-1',
      version: 'v0.1.0',
      installMethod: 'service',
      os: 'linux',
      arch: 'amd64',
    })]).toEqual([
      'version-check',
      'client',
      'client-1',
      'v0.1.0',
      'service',
      'linux',
      'amd64',
    ]);
  });

  test('falls back to binary install method for missing capability', () => {
    expect([...versionCheckQueryKey({
      kind: 'server',
      version: 'v0.1.0',
    })]).toEqual([
      'version-check',
      'server',
      'server',
      'v0.1.0',
      'binary',
      '',
      '',
    ]);
  });
});
