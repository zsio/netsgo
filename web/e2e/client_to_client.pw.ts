import { expect, test } from './fixtures';
import {
  createClientToClientTunnel,
  e2eConfig,
  expectHTTPContains,
  login,
  sendUDP,
  waitForClientPair,
  waitForTunnelState,
} from './helpers';

test('creates TCP and UDP client-to-client tunnels from the web UI @smoke', async ({ page }) => {
  await login(page);
  const { source, ingress } = await waitForClientPair(page);

  await createClientToClientTunnel(page, {
    sourceClientID: source.id,
    sourceClientName: source.info.hostname,
    ingressClientID: ingress.id,
    ingressClientName: ingress.info.hostname,
    name: 'playwright-c2c-tcp',
    protocol: 'TCP',
    targetHost: 'tcp-backend',
    targetPort: '18083',
    ingressBindIP: '0.0.0.0',
    ingressPort: '18090',
  });
  await waitForTunnelState(page, 'playwright-c2c-tcp', 'active');
  await expectHTTPContains(page, e2eConfig.tcpIngressHostPort, 'playwright tcp c2c response');

  await createClientToClientTunnel(page, {
    sourceClientID: source.id,
    sourceClientName: source.info.hostname,
    ingressClientID: ingress.id,
    ingressClientName: ingress.info.hostname,
    name: 'playwright-c2c-udp',
    protocol: 'UDP',
    targetHost: 'udp-backend',
    targetPort: '18084',
    ingressBindIP: '0.0.0.0',
    ingressPort: '18091',
  });
  await waitForTunnelState(page, 'playwright-c2c-udp', 'active');

  const udpResponse = await sendUDP('127.0.0.1', e2eConfig.udpIngressHostPort, 'hello');
  expect(udpResponse).toContain('playwright-udp-c2c-response');
});
