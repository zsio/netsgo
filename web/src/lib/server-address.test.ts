import { describe, expect, test } from 'bun:test';

import {
  getServerAddrInfo,
  getServerAddrValidationError,
  normalizeServerAddr,
} from './server-address';

describe('server-address', () => {
  test.each([
    ['https://example.com', 'https://example.com'],
    ['https://example.com:8443', 'https://example.com:8443'],
    ['http://localhost', 'http://localhost'],
    ['https://127.0.0.1', 'https://127.0.0.1'],
    ['http://192.168.1.10:8080', 'http://192.168.1.10:8080'],
    ['https://[::1]', 'https://[::1]'],
    ['https://[::1]:8443', 'https://[::1]:8443'],
    ['https://example.com/', 'https://example.com'],
  ])('允许合法地址 %s', (input, expected) => {
    expect(getServerAddrValidationError(input)).toBeNull();
    expect(normalizeServerAddr(input)).toBe(expected);
  });

  test.each([
    'example.com',
    '127.0.0.1:8080',
    'localhost',
    'ftp://example.com',
    'ws://example.com',
    'https://example.com/path',
    'https://example.com?x=1',
    'https://user:pass@example.com',
    'https://@example.com',
    'http://test',
  ])('拒绝非法地址 %s', (input) => {
    expect(getServerAddrValidationError(input)).not.toBeNull();
    expect(normalizeServerAddr(input)).toBeNull();
  });

  test('信息提取与后端规则对齐', () => {
    expect(getServerAddrInfo('https://example.com')?.hostKind).toBe('domain');
    expect(getServerAddrInfo('http://localhost')?.hostKind).toBe('localhost');
    expect(getServerAddrInfo('https://127.0.0.1')?.hostKind).toBe('ip');
    expect(getServerAddrInfo('ws://example.com')).toBeNull();
  });
});
