#!/usr/bin/env bun
// Intentionally restricted to disposable Actions runners: this installs a real OS service.
import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { closeSync, openSync } from 'node:fs';
import { copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { root } from './desktop-release.mjs';

assert.equal(process.env.NETSGO_STARTUP_SYSTEM_TEST, '1');
assert.equal(process.env.GITHUB_ACTIONS, 'true', 'System-service tests may only run on disposable Actions runners');
const windows = process.platform === 'win32';
assert.ok(windows || process.platform === 'darwin');
const ext = windows ? '.exe' : '';
function run(command, args, allowFailure = false) {
  const env = { ...process.env };
  if (command === 'powershell.exe') {
    for (const key of Object.keys(env)) if (key.toLowerCase() === 'psmodulepath') delete env[key];
  }
  const result = spawnSync(command, args, { encoding: 'utf8', timeout: 120000, env });
  if (result.error) throw result.error;
  if (result.status !== 0 && !allowFailure) throw new Error(`${command}: ${result.stdout}\n${result.stderr}`);
  return result;
}
const machine = windows
  ? path.join(run('powershell.exe', ['-NoProfile', '-Command', "[Environment]::GetFolderPath('CommonApplicationData')"]).stdout.trim(), 'NetsGoDesktop')
  : '/Library/Application Support/NetsGoDesktop';
try { await readFile(path.join(machine, 'service.json')); throw new Error('Refusing to replace an existing service'); }
catch (error) { if (error.code !== 'ENOENT') throw error; }
const serviceCheck = () => windows ? run('sc.exe', ['query', 'NetsGoDesktop'], true) : run('sudo', ['-n', '/bin/launchctl', 'print', 'system/uy.zs.netsgo.desktop.service'], true);
assert.notEqual(serviceCheck().status, 0, 'A real service already exists');
const owner = windows ? {
  id: run('whoami.exe', ['/user', '/fo', 'csv', '/nh']).stdout.trim().split(',').at(-1).replaceAll('"', ''),
  name: process.env.USERNAME, gid: 0,
} : { id: run('/usr/bin/id', ['-u']).stdout.trim(), name: run('/usr/bin/id', ['-un']).stdout.trim(), gid: Number(run('/usr/bin/id', ['-g']).stdout.trim()) };
const temp = await mkdtemp(path.join(os.tmpdir(), 'netsgo-service-system-test-'));
const source = path.join(temp, 'source'), user = path.join(temp, 'netsgo-desktop');
await mkdir(source); await mkdir(path.join(user, 'profiles/fixture/client'), { recursive: true });
const agent = path.join(source, `netsgo-desktop-service${ext}`);
await copyFile(path.join(root, 'client-desktop/target/release', `netsgo-desktop-service${ext}`), agent);
await copyFile(path.join(root, 'bin', `netsgo${ext}`), path.join(source, `netsgo${ext}`));
const version = run(agent, ['--version']).stdout.trim();
assert.match(version, /^v\d+\.\d+\.\d+/);
const sqliteBytes = Buffer.from('fixture identity bytes; never transmit or log real credentials');
await writeFile(path.join(user, 'desktop-config.json'), JSON.stringify({ profiles: [{ id: 'fixture', name: 'Before', server: 'http://127.0.0.1:1', auto_connect: false }] }));
await writeFile(path.join(user, 'profiles/fixture/client/netsgo.db'), sqliteBytes);
let resumeLive = false;
async function manage(operation, sourceBin = source, expectFailure = false) {
  const request = path.join(temp, 'request.json');
  const resultFile = path.join(temp, 'result.json');
  await rm(resultFile, { force: true });
  await writeFile(request, JSON.stringify({ operation, owner, user_root: user, source_bin: sourceBin, version, resume: resumeLive ? ['live'] : [] }), { mode: 0o600 });
  if (windows) run(agent, ['--manage-request', request]);
  else run('sudo', ['-n', agent, '--manage-request', request]);
  const result = JSON.parse(await readFile(resultFile, 'utf8'));
  if (expectFailure) assert.ok(result.error, 'the invalid service must fail and roll back');
  else assert.equal(result.error, null);
}
async function call(command, invalidToken = false) {
  const endpoint = JSON.parse(await readFile(path.join(machine, 'data/agent.endpoint.json'), 'utf8'));
  return new Promise((resolve, reject) => {
    const socket = net.createConnection({ host: '127.0.0.1', port: endpoint.port });
    let body = '';
    socket.setTimeout(5000, () => { socket.destroy(); reject(new Error('Service RPC timed out')); });
    socket.on('connect', () => socket.write(`${JSON.stringify({ schema: 1, token: invalidToken ? 'unauthorized' : endpoint.token, command })}\n`));
    socket.on('error', reject);
    socket.on('data', bytes => {
      body += bytes.toString();
      if (body.includes('\n')) { socket.end(); resolve(JSON.parse(body.split('\n')[0])); }
    });
    socket.on('end', () => { if (!body.includes('\n')) reject(new Error('Unauthorized or unavailable')); });
  });
}
async function eventually(check, label, timeout = 35000) {
  const deadline = Date.now() + timeout;
  let lastError;
  do {
    try { const value = await check(); if (value) return value; }
    catch (error) { lastError = error; }
    await new Promise(resolve => setTimeout(resolve, 300));
  } while (Date.now() < deadline);
  throw new Error(`${label} did not become ready${lastError ? `: ${lastError.message}` : ''}`);
}
const port = await new Promise((resolve, reject) => {
  const listener = net.createServer(); listener.on('error', reject);
  listener.listen(0, '127.0.0.1', () => { const port = listener.address().port; listener.close(() => resolve(port)); });
});
const serverUrl = `http://127.0.0.1:${port}`;
const serverLog = openSync(path.join(temp, 'server.log'), 'w');
const server = spawn(path.join(source, `netsgo${ext}`), ['server', '--port', String(port), '--data-dir', path.join(temp, 'server'),
  '--init-admin-username', 'startup-fixture', '--init-admin-password', 'NetsGoStartupFixture123!', '--init-server-addr', serverUrl],
{ stdio: ['ignore', serverLog, serverLog], windowsHide: true });
closeSync(serverLog);
server.on('error', error => console.error(`Fixture server failed: ${error.message}`));
async function api(route, body, token) {
  const response = await fetch(`${serverUrl}${route}`, { method: 'POST',
    headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    body: JSON.stringify(body), signal: AbortSignal.timeout(3000) });
  assert.ok(response.ok, `Fixture API ${route}: ${response.status}`);
  return response.json();
}
async function liveConnected() {
  const state = (await call({ op: 'state' })).state;
  const live = state.sessions.find(session => session.profile.id === 'live');
  return live?.snapshot.connected && live.pid ? live : null;
}
let installed = false;
try {
  const login = await eventually(() => api('/api/auth/login', { username: 'startup-fixture', password: 'NetsGoStartupFixture123!' }), 'Fixture server');
  const key = await api('/api/admin/keys', { name: 'startup-test', permissions: ['connect'] }, login.token);
  assert.ok(key.raw_key, 'Fixture client key must be present');
  await manage('Install'); installed = true;
  assert.equal(serviceCheck().status, 0);
  assert.equal((await call({ op: 'state' })).version, version);
  await assert.rejects(call({ op: 'state' }, true));
  await call({ op: 'rename', args: { id: 'fixture', name: 'Background survives detached UI' } });
  const before = JSON.parse(await readFile(path.join(machine, 'data/agent.endpoint.json'), 'utf8')).pid;
  assert.equal((await call({ op: 'state' })).state.sessions[0].profile.name, 'Background survives detached UI');
  if (windows) {
    const query = `(Get-Acl -LiteralPath '${path.join(machine, 'data').replaceAll("'", "''")}').Access | ForEach-Object { $_.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value }`;
    const sids = run('powershell.exe', ['-NoProfile', '-Command', query]).stdout;
    assert.ok(sids.includes(owner.id));
    assert.ok(!sids.includes('S-1-5-32-545') && !sids.includes('S-1-1-0'), 'credentials must not be readable by all users');
  } else {
    const { stat } = await import('node:fs/promises');
    assert.equal((await stat(path.join(machine, 'data/agent.endpoint.json'))).mode & 0o777, 0o600);
  }
  console.log('Verified system-managed host, private local control, and detached UI state');
  const connected = await call({ op: 'connect', args: { profile_id: 'live', name: 'Real Go sidecar', server: serverUrl,
    key: key.raw_key, mode: 'key', tls_skip_verify: false, tls_fingerprint: '' } });
  assert.equal(connected.error, null);
  const initialLive = await eventually(liveConnected, 'Background Go connection');
  const clientId = initialLive.snapshot.client_id;
  assert.ok(clientId, 'A real server-issued client identity must be available');
  resumeLive = true;
  console.log('Verified a real authenticated Go control/data connection without a desktop process');
  await manage('Upgrade');
  const after = JSON.parse(await readFile(path.join(machine, 'data/agent.endpoint.json'), 'utf8')).pid;
  assert.notEqual(after, before);
  assert.equal((await call({ op: 'state' })).state.sessions[0].profile.name, 'Background survives detached UI');
  assert.equal((await eventually(liveConnected, 'Connection after service update')).snapshot.client_id, clientId);
  console.log('Verified service binary replacement and configuration preservation');
  const invalid = path.join(temp, 'invalid-binaries'); await mkdir(invalid);
  await writeFile(path.join(invalid, `netsgo-desktop-service${ext}`), 'not an executable');
  await copyFile(path.join(source, `netsgo${ext}`), path.join(invalid, `netsgo${ext}`));
  await manage('Upgrade', invalid, true);
  assert.equal((await call({ op: 'state' })).version, version);
  assert.equal((await eventually(liveConnected, 'Connection after rollback')).snapshot.client_id, clientId);
  console.log('Verified failed-update rollback to the working system service');
  // Restart through the OS without a one-shot resume file: this is the boot auto-connect path.
  const beforeRestart = JSON.parse(await readFile(path.join(machine, 'data/agent.endpoint.json'), 'utf8')).pid;
  if (windows) {
    run('sc.exe', ['stop', 'NetsGoDesktop']);
    await eventually(() => run('powershell.exe', ['-NoProfile', '-Command', "(Get-Service NetsGoDesktop).Status.ToString()"]).stdout.trim() === 'Stopped', 'Stopped service');
    run('sc.exe', ['start', 'NetsGoDesktop']);
  } else {
    run('sudo', ['-n', '/bin/launchctl', 'kill', 'SIGTERM', 'system/uy.zs.netsgo.desktop.service']);
  }
  await eventually(async () => JSON.parse(await readFile(path.join(machine, 'data/agent.endpoint.json'), 'utf8')).pid !== beforeRestart && await liveConnected(), 'Boot auto-connect');
  assert.equal((await liveConnected()).snapshot.client_id, clientId);
  console.log('Verified saved-token auto-connect after OS-managed restart, with no desktop process');
  await manage('Uninstall'); installed = false;
  assert.equal(JSON.parse(await readFile(path.join(machine, 'service.json'), 'utf8')).enabled, false);
  assert.equal(JSON.parse(await readFile(path.join(user, 'desktop-config.json'), 'utf8')).profiles[0].name, 'Background survives detached UI');
  assert.deepEqual(await readFile(path.join(user, 'profiles/fixture/client/netsgo.db')), sqliteBytes);
  assert.ok((await readFile(path.join(user, 'profiles/live/client/netsgo.db'))).length > 0);
  console.log('Verified disabling and migration back to the desktop data directory');
} finally {
  if (installed) { try { await manage('Uninstall'); } catch (error) { console.error(`Cleanup failed: ${error.message}`); } }
  server.kill();
  // The runner is disposable; retain files for job diagnostics rather than touching other services.
  console.log(`System-service test diagnostics: ${temp}`);
}
