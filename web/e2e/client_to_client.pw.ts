import { expect, test, type Page } from '@playwright/test';
import dgram from 'node:dgram';

type ClientSummary = {
  id: string;
  online: boolean;
  info: {
    hostname: string;
  };
};

type TunnelSummary = {
  id: string;
  name: string;
  runtime_state: string;
  topology: string;
};

const adminUser = process.env.NETSGO_ADMIN_USER ?? 'admin';
const adminPass = process.env.NETSGO_ADMIN_PASS ?? 'password123';
const sourceHostname = process.env.NETSGO_SOURCE_CLIENT_HOSTNAME ?? 'playwright-source-client';
const ingressHostname = process.env.NETSGO_INGRESS_CLIENT_HOSTNAME ?? 'playwright-ingress-client';
const tcpIngressPort = Number.parseInt(process.env.PLAYWRIGHT_TCP_INGRESS_PORT ?? '19190', 10);
const udpIngressPort = Number.parseInt(process.env.PLAYWRIGHT_UDP_INGRESS_PORT ?? '19191', 10);

test('creates TCP and UDP client-to-client tunnels from the web UI', async ({ page }) => {
  await login(page);
  const clients = await waitForLiveClients(page, [sourceHostname, ingressHostname]);
  const source = clients.find((client) => client.info.hostname === sourceHostname);
  const ingress = clients.find((client) => client.info.hostname === ingressHostname);
  expect(source, `missing live source client ${sourceHostname}`).toBeTruthy();
  expect(ingress, `missing live ingress client ${ingressHostname}`).toBeTruthy();

  await createClientToClientTunnel(page, {
    sourceClientID: source!.id,
    ingressClientID: ingress!.id,
    name: 'playwright-c2c-tcp',
    protocol: 'TCP',
    targetHost: 'tcp-backend',
    targetPort: '18083',
    ingressBindIP: '0.0.0.0',
    ingressPort: '18090',
  });
  await waitForTunnelState(page, 'playwright-c2c-tcp', 'active');

  const tcpResponse = await page.request.get(`http://127.0.0.1:${tcpIngressPort}/`);
  expect(tcpResponse.ok()).toBeTruthy();
  expect(await tcpResponse.text()).toContain('playwright tcp c2c response');

  await createClientToClientTunnel(page, {
    sourceClientID: source!.id,
    ingressClientID: ingress!.id,
    name: 'playwright-c2c-udp',
    protocol: 'UDP',
    targetHost: 'udp-backend',
    targetPort: '18084',
    ingressBindIP: '0.0.0.0',
    ingressPort: '18091',
  });
  await waitForTunnelState(page, 'playwright-c2c-udp', 'active');

  const udpResponse = await sendUDP('127.0.0.1', udpIngressPort, 'hello');
  expect(udpResponse).toContain('playwright-udp-c2c-response');
});

async function login(page: Page) {
  await gotoWhenReady(page, '/#/login');
  await page.getByPlaceholder('请输入用户名').fill(adminUser);
  await page.getByPlaceholder('请输入密码').fill(adminPass);
  await page.getByRole('button', { name: /登\s*录/ }).click();
  await expect(page.getByText('在线端点 (Clients)')).toBeVisible();
}

async function gotoWhenReady(page: Page, path: string) {
  const deadline = Date.now() + 90_000;
  let lastError: unknown;
  while (Date.now() < deadline) {
    try {
      await page.goto(path, { waitUntil: 'domcontentloaded', timeout: 5_000 });
      return;
    } catch (err) {
      lastError = err;
      await page.waitForTimeout(1_000);
    }
  }
  throw lastError instanceof Error ? lastError : new Error(`timed out waiting for ${path}`);
}

async function fetchClients(page: Page): Promise<ClientSummary[]> {
  const response = await page.request.get('/api/clients');
  expect(response.ok()).toBeTruthy();
  return response.json();
}

async function waitForLiveClients(page: Page, hostnames: string[]) {
  await expect.poll(async () => {
    const clients = await fetchClients(page);
    return clients
      .filter((client) => client.online && hostnames.includes(client.info.hostname))
      .map((client) => client.info.hostname)
      .sort();
  }, { timeout: 90_000 }).toEqual([...hostnames].sort());

  return fetchClients(page);
}

async function createClientToClientTunnel(
  page: Page,
  config: {
    sourceClientID: string;
    ingressClientID: string;
    name: string;
    protocol: 'TCP' | 'UDP';
    targetHost: string;
    targetPort: string;
    ingressBindIP: string;
    ingressPort: string;
  },
) {
  await page.goto(`/#/dashboard/clients/${config.sourceClientID}`);
  await expect(page.getByText('下属隧道')).toBeVisible();
  await page.getByRole('button', { name: '添加隧道' }).click();

  const dialog = page.getByRole('dialog', { name: '创建代理隧道' });
  await expect(dialog).toBeVisible();
  await dialog.getByLabel('隧道名称').fill(config.name);
  await dialog.getByRole('button', { name: '客户端互访' }).click();
  await dialog.getByLabel('服务来源客户端').selectOption(config.sourceClientID);
  await dialog.getByLabel('访问入口客户端').selectOption(config.ingressClientID);
  await dialog.getByRole('button', { name: config.protocol }).click();
  await dialog.getByLabel('目标服务地址').fill(config.targetHost);
  await dialog.getByLabel('目标服务端口').fill(config.targetPort);
  await dialog.getByLabel('入口监听地址').fill(config.ingressBindIP);
  await dialog.getByLabel('入口监听端口').fill(config.ingressPort);
  await dialog.getByRole('button', { name: '创建隧道' }).click();
  await expect(dialog).toBeHidden({ timeout: 30_000 });
}

async function waitForTunnelState(page: Page, name: string, state: string) {
  await expect.poll(async () => {
    const response = await page.request.get('/api/tunnels');
    expect(response.ok()).toBeTruthy();
    const tunnels = await response.json() as TunnelSummary[];
    const tunnel = tunnels.find((item) => item.name === name);
    return tunnel ? `${tunnel.topology}:${tunnel.runtime_state}` : 'missing';
  }, { timeout: 90_000 }).toBe(`client_to_client:${state}`);
}

async function sendUDP(host: string, port: number, message: string) {
  const socket = dgram.createSocket('udp4');
  try {
    return await new Promise<string>((resolve, reject) => {
      const timeout = setTimeout(() => {
        reject(new Error(`timed out waiting for UDP response from ${host}:${port}`));
      }, 10_000);
      socket.once('message', (payload) => {
        clearTimeout(timeout);
        resolve(payload.toString('utf8'));
      });
      socket.once('error', (err) => {
        clearTimeout(timeout);
        reject(err);
      });
      socket.send(Buffer.from(message), port, host);
    });
  } finally {
    socket.close();
  }
}
