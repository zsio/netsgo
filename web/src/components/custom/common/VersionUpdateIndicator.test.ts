import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import { versionCheckQueryKey, type VersionCheckTarget } from '@/hooks/use-version-check';
import type { VersionCheckResult } from '@/types';

import { VersionUpdateContent, VersionUpdateIndicator } from './VersionUpdateIndicator';
import { manualVersionCheckToast } from './version-update-toast';

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

  test('service update renders both trusted upgrade commands', () => {
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
        domestic: 'curl -fsSL https://netsgo.zs.uy/upgrade.sh | sh -s -- --source cnb --channel stable -y',
        global: 'curl -fsSL https://raw.githubusercontent.com/zsio/netsgo/main/scripts/upgrade.sh | sh -s -- --source github --channel stable -y',
      },
      }),
    }));

    expect(markup).toContain('Run the following command on the Server machine to upgrade');
    expect(markup).toContain('scripts/upgrade.sh');
    expect(markup).toContain('--source cnb --channel stable -y');
    expect(markup).toContain('--source github --channel stable -y');
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
