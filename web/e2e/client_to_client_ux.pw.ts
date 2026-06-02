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

  await expect(dialog.getByLabel('Local IP')).toBeVisible();
  await expect(dialog.getByLabel('Public port')).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'HTTP' })).toBeVisible();

  await dialog.getByRole('button', { name: 'Client to Client' }).click();
  await expect(dialog.getByLabel('Service source client')).toHaveValue(source.id);
  await expect(dialog.getByLabel('Ingress client')).toHaveValue(ingress.id);
  await expect(dialog.getByLabel('Target service address')).toBeVisible();
  await expect(dialog.getByLabel('Target service port')).toBeVisible();
  await expect(dialog.getByLabel('Ingress bind address')).toBeVisible();
  await expect(dialog.getByLabel('Ingress bind port')).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'HTTP' })).toHaveCount(0);

  await dialog.getByLabel('Service source client').selectOption(ingress.id);
  await expect(dialog.getByLabel('Ingress client')).toHaveValue(source.id);

  await captureArtifact(dialog, testInfo, 'client-to-client-form-desktop');
  await page.setViewportSize({ width: 390, height: 800 });
  await expect(dialog.getByRole('button', { name: 'Create tunnel' })).toBeVisible();
  await captureArtifact(dialog, testInfo, 'client-to-client-form-mobile');
});

test('client-to-client form keeps invalid input local and surfaces field errors @ux @error', async ({ page }, testInfo) => {
  await login(page);
  const { source, ingress } = await waitForClientPair(page);
  const dialog = await openCreateTunnelDialog(page, source.id);
  const createButton = dialog.getByRole('button', { name: 'Create tunnel' });

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

  await dialog.getByLabel('Ingress limit').fill('-1');
  await expect(createButton).toBeDisabled();
  await dialog.getByLabel('Ingress limit').fill('');
  await dialog.getByLabel('Ingress bind address').fill('');
  await expect(createButton).toBeDisabled();

  await dialog.getByLabel('Ingress bind address').fill('not-an-ip');
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
  await dialog.getByRole('button', { name: 'Create tunnel' }).click();
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText(/conflicts|already in use|occupied/i).first()).toBeVisible();
  await captureArtifact(dialog, testInfo, 'client-to-client-port-conflict');
});
