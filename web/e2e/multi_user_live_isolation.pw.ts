import { expect, test, type Page } from './fixtures';
import {
  createClientToClientTunnel,
  e2eURL,
  expectHTTPContains,
  expectHTTPUnavailable,
  fetchClients,
  fetchTunnels,
  fillClientToClientTunnel,
  login,
  loginAs,
  uniqueTunnelName,
  waitForTunnelState,
} from './helpers';

type ClientSummary = {
  id: string;
  online: boolean;
  info: { hostname: string };
};

type TunnelSummary = {
  id: string;
  name: string;
  runtime_state: string;
};

type ManagedUser = { id: string; username: string; status: string };

type TrafficResponse = {
  items: Array<{
    tunnel_id?: string;
    tunnel_name?: string;
    points: Array<{ total_bytes: number }>;
  }>;
};

const userPassword = process.env.NETSGO_MULTI_USER_PASSWORD ?? 'PlaywrightUser123!';
const userAName = process.env.NETSGO_MULTI_USER_A ?? 'playwright-user-a';
const userBName = process.env.NETSGO_MULTI_USER_B ?? 'playwright-user-b';
const userAIngressPort = Number.parseInt(process.env.PLAYWRIGHT_USER_A_TCP_INGRESS_PORT ?? '19194', 10);
const userBIngressPort = Number.parseInt(process.env.PLAYWRIGHT_USER_B_TCP_INGRESS_PORT ?? '19195', 10);

async function managedUser(page: Page, username: string): Promise<ManagedUser> {
  const response = await page.request.get(e2eURL(`/api/admin/users?query=${encodeURIComponent(username)}`));
  expect(response.ok(), await response.text()).toBeTruthy();
  const body = await response.json() as { items: ManagedUser[] };
  const user = body.items.find((item) => item.username === username);
  expect(user, `missing managed user ${username}`).toBeTruthy();
  return user!;
}

async function waitForManagedUser(page: Page, username: string) {
  await expect.poll(async () => {
    const response = await page.request.get(e2eURL(`/api/admin/users?query=${encodeURIComponent(username)}`));
    if (!response.ok()) return false;
    const body = await response.json() as { items: ManagedUser[] };
    return body.items.some((item) => item.username === username);
  }, { timeout: 90_000 }).toBe(true);
  return managedUser(page, username);
}

async function scopedClients(page: Page, userID: string): Promise<ClientSummary[]> {
  const response = await page.request.get(e2eURL(`/api/admin/users/${encodeURIComponent(userID)}/clients`));
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}

async function scopedTunnels(page: Page, userID: string): Promise<TunnelSummary[]> {
  const response = await page.request.get(e2eURL(`/api/admin/users/${encodeURIComponent(userID)}/tunnels`));
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}

async function waitForScopedClients(page: Page, userID: string, hostnames: string[]) {
  await expect.poll(async () => {
    const clients = await scopedClients(page, userID);
    return clients.filter((client) => client.online && hostnames.includes(client.info.hostname))
      .map((client) => client.info.hostname).sort();
  }, { timeout: 90_000 }).toEqual([...hostnames].sort());
  return scopedClients(page, userID);
}

async function waitForScopedTunnel(page: Page, userID: string, name: string, state: string) {
  let matched: TunnelSummary | undefined;
  await expect.poll(async () => {
    const tunnels = await scopedTunnels(page, userID);
    matched = tunnels.find((tunnel) => tunnel.name === name);
    return matched?.runtime_state ?? 'missing';
  }, { timeout: 90_000 }).toBe(state);
  return matched!;
}

async function waitForScopedTunnelMissing(page: Page, userID: string, name: string) {
  await expect.poll(async () => {
    const tunnels = await scopedTunnels(page, userID);
    return tunnels.some((tunnel) => tunnel.name === name);
  }, { timeout: 30_000 }).toBe(false);
}

function trafficQuery(path: string) {
  const now = Math.floor(Date.now() / 1000);
  const params = new URLSearchParams({
    from: String(now - 120),
    to: String(now + 60),
    resolution: 'minute',
  });
  return `${path}?${params.toString()}`;
}

async function trafficForClients(page: Page, paths: string[]) {
  const items: TrafficResponse['items'] = [];
  for (const path of paths) {
    const response = await page.request.get(e2eURL(trafficQuery(path)));
    if (!response.ok()) {
      throw new Error(`traffic query failed: ${response.status()} ${await response.text()}`);
    }
    const body = await response.json() as TrafficResponse;
    items.push(...body.items);
  }
  return items;
}

function hasRecordedTraffic(items: TrafficResponse['items'], tunnel: TunnelSummary) {
  return items.some((item) =>
    (item.tunnel_id === tunnel.id || item.tunnel_name === tunnel.name)
    && item.points.some((point) => point.total_bytes > 0));
}

async function waitForRecordedTraffic(page: Page, paths: string[], tunnel: TunnelSummary) {
  await expect.poll(async () => hasRecordedTraffic(await trafficForClients(page, paths), tunnel), {
    timeout: 60_000,
  }).toBe(true);
}

async function openScopedTunnelDialog(page: Page, userID: string, clientID: string) {
  await page.goto(e2eURL(`/#/dashboard/users/${encodeURIComponent(userID)}/clients/${encodeURIComponent(clientID)}`));
  await expect(page.getByText('Child tunnels')).toBeVisible();
  await page.getByRole('button', { name: 'Add tunnel' }).click();
  const dialog = page.getByRole('dialog', { name: 'Create tunnel' });
  await expect(dialog).toBeVisible();
  return dialog;
}

async function createScopedTunnel(page: Page, userID: string, source: ClientSummary, ingress: ClientSummary, name: string, ingressPort: number) {
  const dialog = await openScopedTunnelDialog(page, userID, source.id);
  await fillClientToClientTunnel(dialog, {
    sourceClientID: source.id,
    sourceClientName: source.info.hostname,
    ingressClientID: ingress.id,
    ingressClientName: ingress.info.hostname,
    name,
    protocol: 'TCP',
    targetHost: 'tcp-backend',
    targetPort: '18083',
    ingressBindIP: '0.0.0.0',
    ingressPort: String(ingressPort === userAIngressPort ? 18094 : 18095),
  });
  await dialog.getByRole('button', { name: 'Create tunnel' }).click();
  await expect(dialog).toBeHidden({ timeout: 30_000 });
}

test('two user workspaces keep live devices, tunnels, traffic, and disable state isolated @multi-user @live', async ({ page, browser }) => {
  await login(page);
  const userA = await waitForManagedUser(page, userAName);
  const userB = await waitForManagedUser(page, userBName);
  const clientsA = await waitForScopedClients(page, userA.id, ['playwright-user-a-source', 'playwright-user-a-ingress']);
  const clientsB = await waitForScopedClients(page, userB.id, ['playwright-user-b-source', 'playwright-user-b-ingress']);
  expect(clientsA).toHaveLength(2);
  expect(clientsB).toHaveLength(2);
  expect(new Set(clientsA.map((client) => client.id))).not.toEqual(new Set(clientsB.map((client) => client.id)));

  const sourceA = clientsA.find((client) => client.info.hostname === 'playwright-user-a-source')!;
  const ingressA = clientsA.find((client) => client.info.hostname === 'playwright-user-a-ingress')!;
  const sourceB = clientsB.find((client) => client.info.hostname === 'playwright-user-b-source')!;
  const ingressB = clientsB.find((client) => client.info.hostname === 'playwright-user-b-ingress')!;
  const tunnelAName = uniqueTunnelName('playwright-user-a-tunnel');
  const tunnelBName = uniqueTunnelName('playwright-user-b-tunnel');
  const userAContext = await browser.newContext({ locale: 'en-US' });
  const userAPage = await userAContext.newPage();

  try {
    await loginAs(userAPage, userAName, userPassword);
    await expect(userAPage.getByRole('link', { name: 'Users', exact: true })).toHaveCount(0);
    await expect(userAPage.getByText('playwright-user-a-source', { exact: true }).first()).toBeVisible();
    await expect(userAPage.getByText('playwright-user-b-source', { exact: true })).toHaveCount(0);

    const ownClientIDs = (await fetchClients(userAPage)).map((client) => client.id);
    expect(ownClientIDs.sort()).toEqual(clientsA.map((client) => client.id).sort());
    expect(await userAPage.request.get(e2eURL('/api/admin/users')).then((response) => response.status())).toBe(403);

    await createClientToClientTunnel(userAPage, {
      sourceClientID: sourceA.id,
      sourceClientName: sourceA.info.hostname,
      ingressClientID: ingressA.id,
      ingressClientName: ingressA.info.hostname,
      name: tunnelAName,
      protocol: 'TCP',
      targetHost: 'tcp-backend',
      targetPort: '18083',
      ingressBindIP: '0.0.0.0',
      ingressPort: '18094',
    });
    await waitForTunnelState(userAPage, tunnelAName, 'active');
    const tunnelA = await waitForScopedTunnel(page, userA.id, tunnelAName, 'active');
    await expectHTTPContains(page, userAIngressPort, 'playwright tcp c2c response');
    const userATrafficPaths = clientsA.map((client) => `/api/clients/${encodeURIComponent(client.id)}/traffic`);
    await waitForRecordedTraffic(userAPage, userATrafficPaths, tunnelA);

    await createScopedTunnel(page, userB.id, sourceB, ingressB, tunnelBName, userBIngressPort);
    const tunnelB = await waitForScopedTunnel(page, userB.id, tunnelBName, 'active');
    await expectHTTPContains(page, userBIngressPort, 'playwright tcp c2c response');
    const userBTrafficPaths = clientsB.map((client) =>
      `/api/admin/users/${encodeURIComponent(userB.id)}/clients/${encodeURIComponent(client.id)}/traffic`);
    await waitForRecordedTraffic(page, userBTrafficPaths, tunnelB);

    expect((await scopedTunnels(page, userA.id)).map((tunnel) => tunnel.name)).toContain(tunnelAName);
    expect((await scopedTunnels(page, userB.id)).map((tunnel) => tunnel.name)).toContain(tunnelBName);
    const ownTunnelNames = (await fetchTunnels(userAPage)).map((tunnel) => tunnel.name);
    expect(ownTunnelNames).toContain(tunnelAName);
    expect(ownTunnelNames).not.toContain(tunnelBName);

    const userATraffic = await trafficForClients(userAPage, userATrafficPaths);
    expect(hasRecordedTraffic(userATraffic, tunnelA)).toBe(true);
    expect(userATraffic.some((item) => item.tunnel_id === tunnelB.id || item.tunnel_name === tunnelB.name)).toBe(false);
    const userBTraffic = await trafficForClients(page, userBTrafficPaths);
    expect(hasRecordedTraffic(userBTraffic, tunnelB)).toBe(true);
    expect(userBTraffic.some((item) => item.tunnel_id === tunnelA.id || item.tunnel_name === tunnelA.name)).toBe(false);
    const crossOwnerTraffic = await userAPage.request.get(e2eURL(trafficQuery(`/api/clients/${encodeURIComponent(sourceB.id)}/traffic`)));
    expect(crossOwnerTraffic.status()).toBe(404);

    const foreignCreate = await page.request.post(e2eURL(`/api/admin/users/${encodeURIComponent(userA.id)}/tunnels`), {
      headers: { 'content-type': 'application/json' },
      data: JSON.stringify({
        name: uniqueTunnelName('foreign-client-reject'),
        topology: 'client_to_client',
        ingress: {
          location: 'client', client_id: ingressA.id, type: 'tcp_listen',
          config: { bind_ip: '0.0.0.0', port: 18094, allowed_source_cidrs: ['0.0.0.0/0', '::/0'] },
        },
        target: {
          location: 'client', client_id: sourceB.id, type: 'tcp_service',
          config: { host: 'tcp-backend', port: 18083 },
        },
      }),
    });
    expect(foreignCreate.status()).toBe(400);
    expect((await foreignCreate.json()).code).toBe('unknown_client');

    await page.goto(e2eURL('/#/dashboard/users'));
    await page.getByRole('row').filter({ hasText: userAName }).getByRole('button', { name: 'Actions' }).click();
    await page.getByRole('menuitem', { name: 'Disable user' }).click();
    await page.getByRole('dialog', { name: 'Disable user' }).getByRole('button', { name: 'Confirm' }).click();
    await expect.poll(async () => (await managedUser(page, userAName)).status).toBe('disabled');
    await expect.poll(async () => (await userAPage.request.get(e2eURL('/api/auth/me'))).status()).toBe(401);
    await userAPage.goto(e2eURL('/#/dashboard'));
    await expect(userAPage).toHaveURL(/#\/login$/);
    await expectHTTPUnavailable(page, userAIngressPort);
    await expectHTTPContains(page, userBIngressPort, 'playwright tcp c2c response');

    await page.getByRole('row').filter({ hasText: userAName }).getByRole('button', { name: 'Actions' }).click();
    await page.getByRole('menuitem', { name: 'Enable user' }).click();
    await page.getByRole('dialog', { name: 'Enable user' }).getByRole('button', { name: 'Confirm' }).click();
    await expect.poll(async () => (await managedUser(page, userAName)).status).toBe('active');
    await waitForScopedClients(page, userA.id, ['playwright-user-a-source', 'playwright-user-a-ingress']);
    await waitForScopedTunnel(page, userA.id, tunnelAName, 'active');
    await expectHTTPContains(page, userAIngressPort, 'playwright tcp c2c response');
  } finally {
    try {
      for (const [userID, name] of [[userA.id, tunnelAName], [userB.id, tunnelBName]] as const) {
        const tunnel = (await scopedTunnels(page, userID)).find((item) => item.name === name);
        if (!tunnel) continue;
        const deleted = await page.request.delete(e2eURL(`/api/admin/users/${encodeURIComponent(userID)}/tunnels/${encodeURIComponent(tunnel.id)}`));
        expect([200, 204, 404]).toContain(deleted.status());
        await waitForScopedTunnelMissing(page, userID, name).catch(() => undefined);
      }
      await expectHTTPUnavailable(page, userAIngressPort).catch(() => undefined);
      await expectHTTPUnavailable(page, userBIngressPort).catch(() => undefined);
    } finally {
      await userAContext.close();
    }
  }
});
