import { expect, test } from '@playwright/test';
import {
  captureArtifact,
  clickTunnelAction,
  createClientToClientTunnel,
  e2eConfig,
  expectHTTPContains,
  expectHTTPUnavailable,
  login,
  tunnelRow,
  uniqueTunnelName,
  waitForClientPair,
  waitForTunnelMissing,
  waitForTunnelState,
} from './helpers';

test('client-to-client TCP tunnel can stop, edit, resume, and delete from the UI @ux @lifecycle', async ({ page }, testInfo) => {
  await login(page);
  const { source, ingress } = await waitForClientPair(page);
  const name = uniqueTunnelName('playwright-c2c-life');

  await createClientToClientTunnel(page, {
    sourceClientID: source.id,
    ingressClientID: ingress.id,
    name,
    protocol: 'TCP',
    targetHost: 'tcp-backend',
    targetPort: '18083',
    ingressBindIP: '0.0.0.0',
    ingressPort: '18092',
  });
  await waitForTunnelState(page, name, 'active');
  await expectHTTPContains(page, e2eConfig.lifecycleTCPHostPort, 'playwright tcp c2c response');

  let row = tunnelRow(page, name);
  await expect(row).toContainText('Client ↔ Client');
  await expect(row).toContainText('入口');
  await expect(row).toContainText('目标');
  await expect(row).toContainText('Server 中继');
  await expect(row).toContainText('入口绑定到通配地址');
  await captureArtifact(row, testInfo, 'client-to-client-active-row');

  await clickTunnelAction(page, name, '停止');
  await waitForTunnelState(page, name, 'idle');
  row = tunnelRow(page, name);
  await expect(row).toContainText('已停止');
  await expectHTTPUnavailable(page, e2eConfig.lifecycleTCPHostPort);

  await clickTunnelAction(page, name, '编辑');
  const editDialog = page.getByRole('dialog', { name: '编辑隧道' });
  await expect(editDialog).toBeVisible();
  await expect(editDialog.getByLabel('目标服务端口')).toHaveValue('18083');
  await expect(editDialog.getByLabel('入口监听端口')).toHaveValue('18092');
  await editDialog.getByLabel('入口监听端口').fill('18093');
  await captureArtifact(editDialog, testInfo, 'client-to-client-edit-dialog');
  await editDialog.getByRole('button', { name: '保存修改' }).click();
  await expect(editDialog).toBeHidden({ timeout: 30_000 });
  await waitForTunnelState(page, name, 'idle');
  row = tunnelRow(page, name);
  await expect(row).toContainText('18093');

  await clickTunnelAction(page, name, '启动');
  await waitForTunnelState(page, name, 'active');
  await expectHTTPUnavailable(page, e2eConfig.lifecycleTCPHostPort);
  await expectHTTPContains(page, e2eConfig.editedTCPHostPort, 'playwright tcp c2c response');

  await clickTunnelAction(page, name, '停止');
  await waitForTunnelState(page, name, 'idle');
  await clickTunnelAction(page, name, '删除');
  const confirmDialog = page.getByRole('dialog', { name: '删除隧道' });
  await expect(confirmDialog).toContainText(`确认永久删除隧道「${name}」？删除后无法恢复。`);
  await confirmDialog.getByRole('button', { name: '删除' }).click();
  await waitForTunnelMissing(page, name);
  await expectHTTPUnavailable(page, e2eConfig.editedTCPHostPort);
});
