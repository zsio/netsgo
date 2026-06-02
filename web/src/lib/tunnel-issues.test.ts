import { afterEach, describe, expect, test } from 'bun:test';

import { i18n } from '@/i18n';
import { formatTunnelIssueMessage, formatTunnelIssueTooltipLine } from './tunnel-issues';

describe('tunnel-issues', () => {
  afterEach(() => {
    void i18n.changeLanguage('en-US');
  });

  test('formats capability issues by scope in English', () => {
    expect(formatTunnelIssueMessage({
      code: 'capability_not_supported',
      scope: 'target_client',
      message: 'backend fallback',
    })).toBe('Service source client does not support this target service type.');

    expect(formatTunnelIssueMessage({
      code: 'capability_not_supported',
      scope: 'ingress_client',
      message: 'backend fallback',
    })).toBe('Ingress client does not support this ingress type.');
  });

  test('formats protocol issue codes in Chinese', async () => {
    await i18n.changeLanguage('zh-CN');

    expect(formatTunnelIssueMessage({
      code: 'ingress_port_in_use',
      scope: 'server',
      message: 'backend fallback',
    })).toBe('入口端口已被占用。');
  });

  test('falls back to backend message for unknown codes', () => {
    expect(formatTunnelIssueTooltipLine({
      code: 'unknown_code',
      scope: 'server',
      severity: 'warning',
      message: 'backend fallback',
    })).toBe('warning: backend fallback');
  });
});
