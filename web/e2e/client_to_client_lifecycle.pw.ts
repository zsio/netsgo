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
  await expect(row).toContainText('Ingress');
  await expect(row).toContainText('Target');
  await expect(row).toContainText('Server relay');
  await expect(row).toContainText('Ingress binds to a wildcard address');
  await captureArtifact(row, testInfo, 'client-to-client-active-row');

  await clickTunnelAction(page, name, 'Stop');
  await waitForTunnelState(page, name, 'idle');
  row = tunnelRow(page, name);
  await expect(row).toContainText('Stopped');
  await expectHTTPUnavailable(page, e2eConfig.lifecycleTCPHostPort);

  await clickTunnelAction(page, name, 'Edit');
  const editDialog = page.getByRole('dialog', { name: 'Edit tunnel' });
  await expect(editDialog).toBeVisible();
  await expect(editDialog.getByLabel('Target service port')).toHaveValue('18083');
  await expect(editDialog.getByLabel('Ingress bind port')).toHaveValue('18092');
  await editDialog.getByLabel('Ingress bind port').fill('18093');
  await captureArtifact(editDialog, testInfo, 'client-to-client-edit-dialog');
  await editDialog.getByRole('button', { name: 'Save changes' }).click();
  await expect(editDialog).toBeHidden({ timeout: 30_000 });
  await waitForTunnelState(page, name, 'idle');
  row = tunnelRow(page, name);
  await expect(row).toContainText('18093');

  await clickTunnelAction(page, name, 'Start');
  await waitForTunnelState(page, name, 'active');
  await expectHTTPUnavailable(page, e2eConfig.lifecycleTCPHostPort);
  await expectHTTPContains(page, e2eConfig.editedTCPHostPort, 'playwright tcp c2c response');

  await clickTunnelAction(page, name, 'Stop');
  await waitForTunnelState(page, name, 'idle');
  await clickTunnelAction(page, name, 'Delete');
  const confirmDialog = page.getByRole('dialog', { name: 'Delete tunnel' });
  await expect(confirmDialog).toContainText(`Permanently delete tunnel "${name}"? This cannot be undone.`);
  await confirmDialog.getByRole('button', { name: 'Delete' }).click();
  await waitForTunnelMissing(page, name);
  await expectHTTPUnavailable(page, e2eConfig.editedTCPHostPort);
});
