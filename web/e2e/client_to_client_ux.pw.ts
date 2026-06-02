import { expect, test } from '@playwright/test';
import {
  captureArtifact,
  createClientToClientTunnel,
  fillClientToClientTunnel,
  login,
  openCreateTunnelDialog,
  uniqueTunnelName,
  waitForClientPair,
  waitForTunnelState,
} from './helpers';

test('client-to-client form exposes the right UX and responsive layout @ux', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await login(page);
  const { source, ingress } = await waitForClientPair(page);
  const dialog = await openCreateTunnelDialog(page, source.id);

  await expect(dialog.getByLabel('本地 IP')).toBeVisible();
  await expect(dialog.getByLabel('公网端口')).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'HTTP' })).toBeVisible();

  await dialog.getByRole('button', { name: '客户端互访' }).click();
  await expect(dialog.getByLabel('服务来源客户端')).toHaveValue(source.id);
  await expect(dialog.getByLabel('访问入口客户端')).toHaveValue(ingress.id);
  await expect(dialog.getByLabel('目标服务地址')).toBeVisible();
  await expect(dialog.getByLabel('目标服务端口')).toBeVisible();
  await expect(dialog.getByLabel('入口监听地址')).toBeVisible();
  await expect(dialog.getByLabel('入口监听端口')).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'HTTP' })).toHaveCount(0);
  await expect(dialog.getByText('客户端互访当前开放 TCP / UDP，传输固定为 Server 中继。')).toBeVisible();

  await dialog.getByLabel('服务来源客户端').selectOption(ingress.id);
  await expect(dialog.getByLabel('访问入口客户端')).toHaveValue(source.id);

  await captureArtifact(dialog, testInfo, 'client-to-client-form-desktop');
  await page.setViewportSize({ width: 390, height: 800 });
  await expect(dialog.getByRole('button', { name: '创建隧道' })).toBeVisible();
  await captureArtifact(dialog, testInfo, 'client-to-client-form-mobile');
});

test('client-to-client form keeps invalid input local and surfaces field errors @ux @error', async ({ page }, testInfo) => {
  await login(page);
  const { source, ingress } = await waitForClientPair(page);
  const dialog = await openCreateTunnelDialog(page, source.id);
  const createButton = dialog.getByRole('button', { name: '创建隧道' });

  await expect(createButton).toBeDisabled();
  await fillClientToClientTunnel(dialog, {
    sourceClientID: source.id,
    ingressClientID: ingress.id,
    name: uniqueTunnelName('playwright-c2c-invalid'),
    protocol: 'TCP',
    targetHost: 'tcp-backend',
    targetPort: '18083',
    ingressBindIP: '0.0.0.0',
    ingressPort: '18095',
  });
  await expect(createButton).toBeEnabled();

  await dialog.getByLabel('入站限速').fill('-1');
  await expect(createButton).toBeDisabled();
  await dialog.getByLabel('入站限速').fill('');
  await dialog.getByLabel('入口监听地址').fill('');
  await expect(createButton).toBeDisabled();

  await dialog.getByLabel('入口监听地址').fill('not-an-ip');
  await expect(createButton).toBeEnabled();
  await createButton.click();
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText(/bind_ip must be a valid IPv4 address|IPv4/).first()).toBeVisible();
  await captureArtifact(dialog, testInfo, 'client-to-client-invalid-bind-ip');
});

test('client-to-client port conflicts stay in the dialog with actionable feedback @ux @error', async ({ page }, testInfo) => {
  await login(page);
  const { source, ingress } = await waitForClientPair(page);
  const baseName = uniqueTunnelName('playwright-c2c-conflict-base');

  await createClientToClientTunnel(page, {
    sourceClientID: source.id,
    ingressClientID: ingress.id,
    name: baseName,
    protocol: 'TCP',
    targetHost: 'tcp-backend',
    targetPort: '18083',
    ingressBindIP: '0.0.0.0',
    ingressPort: '18094',
  });
  await waitForTunnelState(page, baseName, 'active');

  const dialog = await openCreateTunnelDialog(page, source.id);
  await fillClientToClientTunnel(dialog, {
    sourceClientID: source.id,
    ingressClientID: ingress.id,
    name: uniqueTunnelName('playwright-c2c-conflict-dup'),
    protocol: 'TCP',
    targetHost: 'tcp-backend',
    targetPort: '18083',
    ingressBindIP: '0.0.0.0',
    ingressPort: '18094',
  });
  await dialog.getByRole('button', { name: '创建隧道' }).click();
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText(/conflicts|冲突|占用/).first()).toBeVisible();
  await captureArtifact(dialog, testInfo, 'client-to-client-port-conflict');
});
