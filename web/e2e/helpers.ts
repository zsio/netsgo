import { expect, type Locator, type Page, type TestInfo } from '@playwright/test';
import dgram from 'node:dgram';
import http from 'node:http';

export type ClientSummary = {
  id: string;
  online: boolean;
  info: {
    hostname: string;
  };
};

export type TunnelSummary = {
  id: string;
  name: string;
  desired_state: string;
  runtime_state: string;
  topology: string;
};

export type ClientPair = {
  source: ClientSummary;
  ingress: ClientSummary;
};

export type ClientToClientTunnelInput = {
  sourceClientID: string;
  sourceClientName?: string;
  ingressClientID: string;
  ingressClientName?: string;
  name: string;
  protocol: 'TCP' | 'UDP';
  targetHost: string;
  targetPort: string;
  ingressBindIP: string;
  ingressPort: string;
};

const e2eLocaleStorageKey = 'netsgo.locale';
const pagesWithLocaleInit = new WeakSet<Page>();

function requiredEnv(name: string) {
  const value = process.env[name];
  if (!value) {
    throw new Error(`${name} is required for Playwright E2E`);
  }
  return value;
}

export const e2eConfig = {
  baseURL: process.env.NETSGO_E2E_BASE_URL ?? 'http://127.0.0.1:19180',
  adminUser: process.env.NETSGO_ADMIN_USER ?? 'admin',
  adminPass: requiredEnv('NETSGO_ADMIN_PASS'),
  sourceHostname: process.env.NETSGO_SOURCE_CLIENT_HOSTNAME ?? 'playwright-source-client',
  ingressHostname: process.env.NETSGO_INGRESS_CLIENT_HOSTNAME ?? 'playwright-ingress-client',
  tcpIngressHostPort: Number.parseInt(process.env.PLAYWRIGHT_TCP_INGRESS_PORT ?? '19190', 10),
  udpIngressHostPort: Number.parseInt(process.env.PLAYWRIGHT_UDP_INGRESS_PORT ?? '19191', 10),
  lifecycleTCPHostPort: Number.parseInt(process.env.PLAYWRIGHT_TCP_LIFECYCLE_INGRESS_PORT ?? '19192', 10),
  editedTCPHostPort: Number.parseInt(process.env.PLAYWRIGHT_TCP_EDIT_INGRESS_PORT ?? '19193', 10),
};

export function uniqueTunnelName(prefix: string) {
  return `${prefix}-${Date.now()}-${Math.floor(Math.random() * 10_000)}`;
}

export function e2eURL(path: string) {
  return new URL(path, e2eConfig.baseURL).toString();
}

async function expectDashboardShell(page: Page) {
  await expect(page).toHaveURL(/#\/dashboard(?:$|[/?])/);
  await expect(page.getByText('Recent Activity', { exact: true })).toBeVisible();
  await expect(page.getByText('Traffic Telemetry', { exact: true })).toBeVisible();
}

export async function login(page: Page) {
  await loginAs(page, e2eConfig.adminUser, e2eConfig.adminPass);
  await expect(page.getByText('Memory', { exact: true })).toBeVisible();
  await expect(page.getByText('Disk', { exact: true })).toBeVisible();
}

export async function loginAs(page: Page, username: string, password: string) {
  await gotoWhenReady(page, '/#/login');
  await page.getByPlaceholder('Enter user account').fill(username);
  await page.getByPlaceholder('Enter password').fill(password);
  await page.getByRole('button', { name: 'Log in', exact: true }).click();
  await expectDashboardShell(page);
}

export async function gotoWhenReady(page: Page, path: string) {
  await ensureEnglishLocale(page);
  const deadline = Date.now() + 90_000;
  let lastError: unknown;
  while (Date.now() < deadline) {
    try {
      await page.goto(e2eURL(path), { waitUntil: 'domcontentloaded', timeout: 5_000 });
      return;
    } catch (err) {
      lastError = err;
      await page.waitForTimeout(1_000);
    }
  }
  throw lastError instanceof Error ? lastError : new Error(`timed out waiting for ${path}`);
}

async function ensureEnglishLocale(page: Page) {
  if (pagesWithLocaleInit.has(page)) {
    return;
  }
  await page.addInitScript(({ key, value }) => {
    window.localStorage.setItem(key, value);
  }, { key: e2eLocaleStorageKey, value: 'en-US' });
  pagesWithLocaleInit.add(page);
}

export async function fetchClients(page: Page): Promise<ClientSummary[]> {
  const response = await page.request.get(e2eURL('/api/clients'));
  if (!response.ok()) {
    throw new Error(`fetch clients failed: ${response.status()} ${await response.text()}`);
  }
  return response.json();
}

export async function fetchTunnels(page: Page): Promise<TunnelSummary[]> {
  const response = await page.request.get(e2eURL('/api/tunnels'));
  if (!response.ok()) {
    throw new Error(`fetch tunnels failed: ${response.status()} ${await response.text()}`);
  }
  return response.json();
}

export async function waitForLiveClients(page: Page, hostnames: string[]) {
  await expect.poll(async () => {
    const clients = await fetchClients(page);
    return clients
      .filter((client) => client.online && hostnames.includes(client.info.hostname))
      .map((client) => client.info.hostname)
      .sort();
  }, { timeout: 90_000 }).toEqual([...hostnames].sort());

  return fetchClients(page);
}

export async function waitForClientPair(page: Page): Promise<ClientPair> {
  const clients = await waitForLiveClients(page, [e2eConfig.sourceHostname, e2eConfig.ingressHostname]);
  const source = clients.find((client) => client.info.hostname === e2eConfig.sourceHostname);
  const ingress = clients.find((client) => client.info.hostname === e2eConfig.ingressHostname);
  expect(source, `missing live source client ${e2eConfig.sourceHostname}`).toBeTruthy();
  expect(ingress, `missing live ingress client ${e2eConfig.ingressHostname}`).toBeTruthy();
  return { source: source!, ingress: ingress! };
}

export async function openCreateTunnelDialog(page: Page, clientID: string) {
  const principalResponse = await page.request.get(e2eURL('/api/auth/me'));
  if (!principalResponse.ok()) {
    throw new Error(`fetch current user failed: ${principalResponse.status()} ${await principalResponse.text()}`);
  }
  const principal = await principalResponse.json() as { id: string; is_admin: boolean };
  const clientPath = principal.is_admin
    ? `/#/dashboard/users/${encodeURIComponent(principal.id)}/clients/${encodeURIComponent(clientID)}`
    : `/#/dashboard/clients/${encodeURIComponent(clientID)}`;
  await page.goto(e2eURL(clientPath));
  await expect(page.getByText('Child tunnels')).toBeVisible();
  await page.getByRole('button', { name: 'Add tunnel' }).click();
  const dialog = page.getByRole('dialog', { name: 'Create tunnel' });
  await expect(dialog).toBeVisible();
  return dialog;
}

export async function fillClientToClientTunnel(dialog: Locator, config: ClientToClientTunnelInput) {
  await dialog.getByLabel('Tunnel name').fill(config.name);
  await dialog.getByRole('button', { name: 'Client to Client' }).click();
  await selectRadixOption(dialog, 'Service source client', config.sourceClientName ?? config.sourceClientID);
  await selectRadixOption(dialog, 'Ingress client', config.ingressClientName ?? config.ingressClientID);
  await dialog.getByRole('button', { name: config.protocol }).click();
  await dialog.getByLabel('Target service address').fill(config.targetHost);
  await dialog.getByLabel('Target service port').fill(config.targetPort);
  await dialog.getByLabel('Ingress bind address').fill(config.ingressBindIP);
  await dialog.getByLabel('Ingress bind port').fill(config.ingressPort);
}

export async function createClientToClientTunnel(page: Page, config: ClientToClientTunnelInput) {
  const dialog = await openCreateTunnelDialog(page, config.sourceClientID);
  await fillClientToClientTunnel(dialog, config);
  await dialog.getByRole('button', { name: 'Create tunnel' }).click();
  await expect(dialog).toBeHidden({ timeout: 30_000 });
}

export async function selectRadixOption(scope: Locator, label: string, optionName: string) {
  await scope.getByLabel(label).click();
  await scope.page().getByRole('option', { name: optionName, exact: true }).click();
}

export async function waitForTunnelState(page: Page, name: string, state: string) {
  let matched: TunnelSummary | undefined;
  await expect.poll(async () => {
    const tunnels = await fetchTunnels(page);
    matched = tunnels.find((item) => item.name === name);
    return matched ? `${matched.topology}:${matched.runtime_state}` : 'missing';
  }, { timeout: 90_000 }).toBe(`client_to_client:${state}`);
  return matched!;
}

export async function waitForTunnelMissing(page: Page, name: string) {
  await expect.poll(async () => {
    const tunnels = await fetchTunnels(page);
    return tunnels.some((item) => item.name === name) ? 'present' : 'missing';
  }, { timeout: 30_000 }).toBe('missing');
}

export async function deleteTunnelByName(page: Page, name: string) {
  const tunnel = (await fetchTunnels(page)).find((item) => item.name === name);
  if (!tunnel) {
    return;
  }

  const response = await page.request.delete(e2eURL(`/api/tunnels/${encodeURIComponent(tunnel.id)}`));
  if (!response.ok() && response.status() !== 404) {
    throw new Error(`delete tunnel ${name} failed: ${response.status()} ${await response.text()}`);
  }
  await waitForTunnelMissing(page, name);
}

export function tunnelRow(page: Page, name: string) {
  return page.getByRole('row').filter({ hasText: name }).first();
}

export async function clickTunnelAction(page: Page, name: string, action: 'Start' | 'Stop' | 'Edit' | 'Delete') {
  const row = tunnelRow(page, name);
  await expect(row).toBeVisible();
  await row.getByRole('button', { name: action }).click();
}

export async function expectHTTPContains(_page: Page, port: number, expected: string) {
  await expect.poll(async () => {
    try {
      const response = await requestHTTPText(port);
      return response.statusCode >= 200 && response.statusCode < 300 ? response.body : `HTTP ${response.statusCode}`;
    } catch (err) {
      return `ERROR ${(err as Error).message}`;
    }
  }, { timeout: 20_000 }).toContain(expected);
}

export async function expectHTTPUnavailable(_page: Page, port: number) {
  await expect.poll(async () => {
    try {
      const response = await requestHTTPText(port, 1_000);
      return response.statusCode >= 200 && response.statusCode < 300 ? 'reachable' : `HTTP ${response.statusCode}`;
    } catch {
      return 'unreachable';
    }
  }, { timeout: 20_000 }).toBe('unreachable');
}

function requestHTTPText(port: number, timeout = 2_000) {
  return new Promise<{ statusCode: number; body: string }>((resolve, reject) => {
    const req = http.request({
      hostname: '127.0.0.1',
      port,
      path: '/',
      method: 'GET',
      agent: false,
      headers: { Connection: 'close' },
      timeout,
    }, (res) => {
      const chunks: Buffer[] = [];
      res.on('data', (chunk: Buffer) => chunks.push(chunk));
      res.on('end', () => {
        resolve({
          statusCode: res.statusCode ?? 0,
          body: Buffer.concat(chunks).toString('utf8'),
        });
      });
    });
    req.on('timeout', () => req.destroy(new Error(`HTTP request timed out for 127.0.0.1:${port}`)));
    req.on('error', reject);
    req.end();
  });
}

export async function sendUDP(host: string, port: number, message: string) {
  const socket = dgram.createSocket('udp4');
  try {
    return await new Promise<string>((resolve, reject) => {
      const timeoutMs = 10_000;
      const resendIntervalMs = 250;
      const deadline = Date.now() + timeoutMs;
      const payload = Buffer.from(message);
      let resendTimer: ReturnType<typeof setTimeout> | undefined;
      let settled = false;

      const cleanup = () => {
        if (resendTimer) {
          clearTimeout(resendTimer);
        }
        socket.removeListener('message', onMessage);
        socket.removeListener('error', onError);
      };
      const finishResolve = (value: string) => {
        if (settled) {
          return;
        }
        settled = true;
        cleanup();
        resolve(value);
      };
      const finishReject = (err: Error) => {
        if (settled) {
          return;
        }
        settled = true;
        cleanup();
        reject(err);
      };
      function onMessage(response: Buffer) {
        finishResolve(response.toString('utf8'));
      }
      function onError(err: Error) {
        finishReject(err);
      }
      const send = () => {
        if (settled) {
          return;
        }
        const remainingMs = deadline - Date.now();
        if (remainingMs <= 0) {
          finishReject(new Error(`timed out waiting for UDP response from ${host}:${port}`));
          return;
        }
        socket.send(payload, port, host, (err) => {
          if (err) {
            finishReject(err);
          }
        });
        resendTimer = setTimeout(send, Math.min(resendIntervalMs, remainingMs));
      };

      socket.on('message', onMessage);
      socket.on('error', onError);
      send();
    });
  } finally {
    socket.close();
  }
}

async function udpPortResponds(host: string, port: number) {
  const socket = dgram.createSocket('udp4');
  return await new Promise<boolean>((resolve) => {
    let settled = false;
    const finish = (responded: boolean) => {
      if (settled) {
        return;
      }
      settled = true;
      clearTimeout(timeout);
      socket.close();
      resolve(responded);
    };
    const timeout = setTimeout(() => finish(false), 500);
    socket.once('message', () => finish(true));
    socket.once('error', () => finish(false));
    socket.send(Buffer.from(`netsgo-cleanup-${Date.now()}`), port, host, (err) => {
      if (err) {
        finish(false);
      }
    });
  });
}

export async function expectUDPUnavailable(host: string, port: number) {
  let consecutiveUnavailable = 0;
  await expect.poll(async () => {
    if (await udpPortResponds(host, port)) {
      consecutiveUnavailable = 0;
      return 'reachable';
    }
    consecutiveUnavailable += 1;
    return consecutiveUnavailable >= 3 ? 'unreachable' : 'reachable';
  }, { timeout: 30_000 }).toBe('unreachable');
}

export async function captureArtifact(locator: Locator, testInfo: TestInfo, name: string) {
  await locator.screenshot({ path: testInfo.outputPath(`${name}.png`) });
}
