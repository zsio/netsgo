#!/usr/bin/env bun
import { spawnSync } from 'node:child_process';
import { mkdir, mkdtemp, cp, readdir, readFile, rename, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { root, appId, platforms, parseTag, signingMode, vpkVersion } from './desktop-release.mjs';

const [target, tag] = process.argv.slice(2);
const platform = platforms[target];
if (!platform) throw new Error('Usage: bun scripts/package-client-desktop.mjs <osx-arm64|osx-x64|win-x64> <vVERSION>');
const { channel } = parseTag(tag);
const signing = signingMode(process.env.NETSGO_DESKTOP_SIGNING);
const nativeOS = process.platform === 'darwin' ? 'darwin' : process.platform === 'win32' ? 'windows' : process.platform;
if (platform.goos !== nativeOS) throw new Error('Build each desktop package on its target operating system');

function run(command, args, env = {}, cwd = root, capture = false) {
  const childEnv = { ...process.env, ...env };
  for (const [name, value] of Object.entries(childEnv)) if (value === null) delete childEnv[name];
  const result = spawnSync(command, args, { cwd, env: childEnv, stdio: capture ? 'pipe' : 'inherit', encoding: 'utf8' });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed (${result.status}): ${capture ? result.stderr : 'see output above'}`);
  return result.stdout?.trim();
}

const vpk = process.env.VPK || 'vpk';
if (!run(vpk, ['--help'], {}, root, true).includes(vpkVersion)) throw new Error(`Install vpk ${vpkVersion} (must match the Rust SDK)`);
if (signing === 'required') {
  const required = platform.goos === 'darwin'
    ? ['VPK_SIGN_APP_IDENTITY', 'VPK_SIGN_INSTALL_IDENTITY', 'VPK_NOTARY_PROFILE']
    : ['VPK_SIGN_PARAMS'];
  for (const name of required) if (!process.env[name]) throw new Error(`${name} is required when desktop signing is required`);
}

const desktop = path.join(root, 'client-desktop');
const output = path.resolve(process.env.DESKTOP_DIST_DIR || path.join(desktop, 'dist'), target);
// A nonempty output could silently introduce old release packages into the new feed.
await mkdir(output, { recursive: true });
if ((await readdir(output)).length) throw new Error(`Output must be empty: ${output}`);
const staging = await mkdtemp(path.join(os.tmpdir(), 'netsgo-desktop-'));
try {
  const ext = platform.goos === 'windows' ? '.exe' : '';
  const commit = run('git', ['rev-parse', 'HEAD'], {}, root, true);
  const date = new Date().toISOString();
  run('go', ['build', '-tags', 'dev', '-trimpath', '-ldflags',
    `-s -w -X netsgo/pkg/version.Current=${tag} -X netsgo/pkg/version.Commit=${commit} -X netsgo/pkg/version.Date=${date}`,
    '-o', path.join(staging, `netsgo${ext}`), './cmd/netsgo'],
  { CGO_ENABLED: '0', GOOS: platform.goos, GOARCH: platform.goarch });
  run('cargo', ['build', '--release', '--locked', '--target', platform.triple, '-p', 'netsgo-gpui', '-p', 'netsgo-desktop-service', '--bins'],
    { NETSGO_DESKTOP_VERSION: tag }, desktop);
  const executable = `netsgo-desktop${ext}`;
  await cp(path.join(desktop, 'target', platform.triple, 'release', executable), path.join(staging, executable));
  const service = `netsgo-desktop-service${ext}`;
  await cp(path.join(desktop, 'target', platform.triple, 'release', service), path.join(staging, service));
  if (platform.goos === 'windows') {
    await cp(path.join(staging, service), path.join(staging, 'netsgo-uninstall.exe'));
  } else {
    const helper = path.join(staging, 'NetsGo Uninstaller.app', 'Contents');
    await mkdir(path.join(helper, 'MacOS'), { recursive: true });
    await mkdir(path.join(helper, 'Resources'));
    await cp(path.join(staging, service), path.join(helper, 'MacOS', 'netsgo-uninstall'));
    await cp(path.join(root, 'desktop/src-tauri/icons/icon.icns'), path.join(helper, 'Resources/icon.icns'));
    await writeFile(path.join(helper, 'Info.plist'), `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>uy.zs.netsgo.desktop.uninstaller</string><key>CFBundleName</key><string>NetsGo Uninstaller</string><key>CFBundleDisplayName</key><string>卸载 NetsGo</string><key>CFBundleExecutable</key><string>netsgo-uninstall</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleIconFile</key><string>icon.icns</string><key>CFBundleVersion</key><string>${tag.slice(1).split('-')[0]}</string><key>LSUIElement</key><true/></dict></plist>`);
  }

  const args = ['pack', '--packId', appId, '--packVersion', tag.slice(1), '--packTitle', 'NetsGo',
    '--packAuthors', 'NetsGo', '--mainExe', executable, '--runtime', target,
    '--channel', `${channel}-${target}`, '--packDir', staging, '--outputDir', output,
    '--delta', 'None', '--icon', path.join(root, 'desktop/src-tauri/icons', platform.goos === 'darwin' ? 'icon.icns' : 'icon.ico'),
    '--releaseNotes', path.join(desktop, 'RELEASE_NOTES.md'), '--skip-updates', '--yes'];
  if (platform.goos === 'darwin') {
    args.push('--bundleId', 'uy.zs.netsgo.desktop', '--signEntitlements', path.join(desktop, 'macos.entitlements'));
  }
  const signingEnv = {};
  if (signing === 'unsigned') {
    // No paid identity: seal the macOS bundle with an ad-hoc signature, and skip the .pkg installer.
    // Clear inherited signing options so this mode never requires a certificate or notary account.
    for (const name of Object.keys(process.env)) {
      if (name.startsWith('VPK_SIGN_') || ['VPK_NOTARY_PROFILE', 'VPK_KEYCHAIN', 'VPK_AZURE_TRUSTED_SIGN_FILE'].includes(name)) signingEnv[name] = null;
    }
    if (platform.goos === 'darwin') args.push('--signAppIdentity', '-', '--noInst');
  }
  run(vpk, args, signingEnv);

  if (platform.goos === 'darwin') {
    for (const name of await readdir(output)) {
      if (!name.endsWith('-Portable.zip')) continue;
      const portable = await mkdtemp(path.join(os.tmpdir(), 'netsgo-portable-'));
      try {
        const archive = path.join(output, name);
        run('ditto', ['-x', '-k', archive, portable]);
        await cp(path.join(portable, 'NetsGo.app/Contents/MacOS/NetsGo Uninstaller.app'), path.join(portable, 'NetsGo Uninstaller.app'), { recursive: true });
        await rm(archive);
        run('ditto', ['-c', '-k', '--sequesterRsrc', portable, archive]);
      } finally { await rm(portable, { recursive: true, force: true }); }
    }
  }

  // Put architecture into every public filename, including nupkg names produced by older packagers.
  const feedPath = path.join(output, `releases.${channel}-${target}.json`);
  const feed = JSON.parse(await readFile(feedPath, 'utf8'));
  for (const asset of feed.Assets) {
    const name = `${appId}-${tag.slice(1)}-${target}-${asset.Type.toLowerCase()}.nupkg`;
    if (asset.FileName !== name) await rename(path.join(output, asset.FileName), path.join(output, name));
    asset.FileName = name;
  }
  await writeFile(feedPath, `${JSON.stringify(feed, null, 2)}\n`);
  for (const name of await readdir(output)) {
    if (/\.(exe|pkg|zip)$/.test(name)) {
      await rename(path.join(output, name), path.join(output, `NetsGo_${tag.slice(1)}_${target}_${name}`));
    } else if (name !== path.basename(feedPath) && !name.endsWith('.nupkg')) {
      await rm(path.join(output, name), { recursive: true });
    }
  }
  await cp(path.join(desktop, 'INSTALL.md'), path.join(output, `NetsGo_${tag.slice(1)}_${target}_README.txt`));
  console.log(`Desktop release ready: ${output}`);
} finally {
  await rm(staging, { recursive: true, force: true });
}
