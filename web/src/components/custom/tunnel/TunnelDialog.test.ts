import { describe, expect, test } from 'vitest';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import { TooltipProvider } from '@/components/ui/tooltip';
import { preserveLoopbackSourceCIDRsOnFirstRestriction } from '@/lib/source-cidrs';

import { ClientToClientTopologyButton } from './TunnelDialog';
import { getInitialTunnelFormState } from './tunnel-dialog-form';

const legacyInlineWarning = ['客户端互访', '需要', '至少', '两个客户端'].join('');
function renderClientToClientButton(disabled: boolean) {
  return renderToStaticMarkup(
    createElement(
      TooltipProvider,
      null,
      createElement(ClientToClientTopologyButton, {
        selected: false,
        disabled,
        label: '客户端互访',
        tooltip: '添加第二个客户端后可创建客户端互访隧道',
        onSelect: () => undefined,
      }),
    ),
  );
}

describe('ClientToClientTopologyButton', () => {
  test('不可用时禁用按钮且不显示表单错误文案', () => {
    const markup = renderClientToClientButton(true);

    expect(markup).toContain('客户端互访');
    expect(markup).toContain('disabled=""');
    expect(markup).toContain('cursor-not-allowed');
    expect(markup).not.toContain(legacyInlineWarning);
    expect(markup).not.toContain('text-destructive">客户端互访');
  });

  test('可用时按钮保持可点击状态', () => {
    const markup = renderClientToClientButton(false);

    expect(markup).toContain('客户端互访');
    expect(markup).not.toContain('disabled=""');
    expect(markup).not.toContain('cursor-not-allowed');
  });
});

describe('getInitialTunnelFormState', () => {
  test.each(['*.x.com', '*.a.b.com', '*.*.x.com', '*.*.*.x.com'])(
    '编辑时从入口配置保留泛域名 %s',
    (domain) => {
      const form = getInitialTunnelFormState({
        mode: 'edit',
        tunnel: {
          id: 'wildcard', name: 'wildcard', clientId: 'client-1', client_id: 'client-1',
          type: 'http', local_ip: '127.0.0.1', local_port: 3000, remote_port: 0,
          domain: 'stale.example.com', ingress_bps: 0, egress_bps: 0,
          created_at: '2026-09-05T00:00:00Z', desired_state: 'running', runtime_state: 'offline',
          capabilities: { can_resume: false, can_stop: true, can_edit: true, can_delete: true, can_migrate: true },
          ingress: {
            location: 'server', type: 'http_host',
            config: { domain, allowed_source_cidrs: ['0.0.0.0/0', '::/0'] },
          },
        },
      });
      expect(form.domain).toBe(domain);
      expect(form.type).toBe('http');
    },
  );

  test('创建隧道时入口监听地址默认使用通配地址', () => {
    const form = getInitialTunnelFormState({
      mode: 'create',
      clientId: 'client-1',
      clients: [
        {
          id: 'client-1',
          ingress_bps: 0,
          egress_bps: 0,
          info: {
            hostname: 'source',
            os: 'linux',
            arch: 'amd64',
            ip: '10.0.0.1',
            version: 'dev',
          },
          stats: null,
          online: true,
        },
        {
          id: 'client-2',
          ingress_bps: 0,
          egress_bps: 0,
          info: {
            hostname: 'ingress',
            os: 'linux',
            arch: 'amd64',
            ip: '10.0.0.2',
            version: 'dev',
          },
          stats: null,
          online: true,
        },
      ],
    });

    expect(form.bindIp).toBe('0.0.0.0');
  });
});

describe('preserveLoopbackSourceCIDRsOnFirstRestriction', () => {
  test('首次从默认 allow-all 收窄时自动追加 loopback CIDR', () => {
    expect(
      preserveLoopbackSourceCIDRsOnFirstRestriction(
        '0.0.0.0/0, ::/0',
        '203.0.113.0/24',
      ),
    ).toBe('203.0.113.0/24, 127.0.0.0/8, ::1/128');
  });

  test('用户后续删除 loopback CIDR 时不再强行补回', () => {
    expect(
      preserveLoopbackSourceCIDRsOnFirstRestriction(
        '203.0.113.0/24, 127.0.0.0/8, ::1/128',
        '203.0.113.0/24',
      ),
    ).toBe('203.0.113.0/24');
  });

  test('清空输入保持由提交逻辑回退为 allow-all', () => {
    expect(
      preserveLoopbackSourceCIDRsOnFirstRestriction(
        '0.0.0.0/0, ::/0',
        '',
      ),
    ).toBe('');
  });
});
