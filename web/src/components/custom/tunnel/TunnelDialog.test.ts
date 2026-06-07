import { describe, expect, test } from 'bun:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import { TooltipProvider } from '@/components/ui/tooltip';

import { ClientToClientTopologyButton } from './TunnelDialog';

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
