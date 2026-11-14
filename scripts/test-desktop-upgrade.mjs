#!/usr/bin/env bun
// Exercise the real macOS installer in an isolated directory, without launching the UI.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { access, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { parseTag, sha256File } from './desktop-release.mjs';

const [previousZip, nextPackage, nextTag] = process.argv.slice(2);
if (process.platform !== 'darwin' || !previousZip || !nextPackage || !nextTag) {
  throw new Error('On macOS: bun scripts/test-desktop-upgrade.mjs <previous-Portable.zip> <next-full.nupkg> <vNEXT>');
}
parseTag(nextTag);
const temp = await mkdtemp(path.join(os.tmpdir(), 'netsgo-upgrade-test-'));
function run(command, args, allowFailure = false) {
  const result = spawnSync(command, args, { encoding: 'utf8', timeout: 60000,
    env: { ...process.env, NETSGO_DESKTOP_DATA_DIR: path.join(temp, 'user-data') } });
  if (result.error) throw result.error;
  if (result.status !== 0 && !allowFailure) throw new Error(`${command}: ${result.stdout}\n${result.stderr}`);
  return result;
}
try {
  run('ditto', ['-x', '-k', path.resolve(previousZip), temp]);
  const apps = (await readdir(temp)).filter(name => name === 'NetsGo.app');
  assert.equal(apps.length, 1);
  const app = path.join(temp, apps[0]);
  const macOS = path.join(app, 'Contents/MacOS');
  const manifest = path.join(app, 'Contents/Resources/sq.version');
  const previous = await readFile(manifest, 'utf8');
  const oldVersion = previous.match(/<version>([^<]+)<\/version>/)?.[1];
  assert.ok(oldVersion && oldVersion !== nextTag.slice(1));
  const executables = ['netsgo-desktop', 'netsgo'].map(name => path.join(macOS, name));
  const before = await Promise.all(executables.map(sha256File));
  const config = path.join(temp, 'user-data/profiles.json');
  await mkdir(path.dirname(config));
  await writeFile(config, 'user configuration must survive');
  const obsolete = path.join(macOS, 'obsolete-upgrade-test-file');
  await writeFile(obsolete, 'must disappear with old bundle');
  const corrupt = path.join(temp, 'corrupt-full.nupkg');
  await writeFile(corrupt, 'not a package');
  const installerArgs = packagePath => ['apply', '--rootDir', app, '--packageDir', path.join(temp, 'packages'),
    '--package', packagePath, '--norestart', '--silent', '--log', path.join(temp, 'update.log')];
  const failed = run(path.join(macOS, 'UpdateMac'), installerArgs(corrupt), true);
  assert.notEqual(failed.status, 0, 'invalid package must fail');
  assert.equal(await readFile(manifest, 'utf8'), previous);
  assert.deepEqual(await Promise.all(executables.map(sha256File)), before, 'failure must preserve both executables');

  run(path.join(macOS, 'UpdateMac'), installerArgs(path.resolve(nextPackage)));
  run('codesign', ['--verify', '--deep', '--strict', app]);
  assert.ok((await readFile(manifest, 'utf8')).includes(`<version>${nextTag.slice(1)}</version>`));
  for (const executable of executables) assert.ok(run(executable, ['--version']).stdout.includes(nextTag));
  await assert.rejects(access(obsolete));
  assert.equal(await readFile(config, 'utf8'), 'user configuration must survive');
  console.log(`Passed: v${oldVersion} → ${nextTag}; desktop + sidecar replaced, failed update preserved old app, user data retained.`);
  await rm(temp, { recursive: true, force: true });
} catch (error) {
  console.error(`Upgrade diagnostics retained at ${temp}`);
  throw error;
}
