#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { mkdtemp, readFile, writeFile, mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

const root = process.cwd();
const tmp = await mkdtemp(path.join(tmpdir(), 'netsgo-index-test-'));
const out = path.join(tmp, 'out');

async function build(tag, assetName, cnbUploadedAssets = undefined) {
  const dist = path.join(tmp, `dist-${tag}`);
  await mkdir(dist, { recursive: true });
  const asset = Buffer.from(`fake archive ${tag}`);
  const sum = createHash('sha256').update(asset).digest('hex');
  await writeFile(path.join(dist, assetName), asset);
  await writeFile(path.join(dist, 'checksums.txt'), `${sum}  ${assetName}\n`);
  await writeFile(path.join(dist, 'checksums.txt.sig'), 'sig');
  await writeFile(path.join(dist, 'checksums.txt.sshsig'), 'sshsig');

  const env = {
    ...process.env,
    GITHUB_REF_NAME: tag,
    GITHUB_REPOSITORY: 'zsio/netsgo',
    DIST_DIR: dist,
    OUT_DIR: out,
    CNB_REPO_SLUG: 'zsio/netsgo',
  };
  if (cnbUploadedAssets !== undefined) {
    env.CNB_UPLOADED_ASSETS = cnbUploadedAssets;
  }
  const result = spawnSync(process.execPath, [path.join(root, 'scripts/release-index.mjs'), 'build'], {
    cwd: root,
    env,
    encoding: 'utf8',
  });
  if (result.status !== 0) {
    process.stderr.write(result.stderr);
    process.exit(result.status || 1);
  }
}

await build('v0.1.0', 'netsgo_0.1.0_linux_amd64.tar.gz', 'checksums.txt,checksums.txt.sig,checksums.txt.sshsig,netsgo_0.1.0_linux_amd64.tar.gz');
await build('v0.1.1-beta.1', 'netsgo_0.1.1-beta.1_linux_armv7.tar.gz', 'checksums.txt,checksums.txt.sig');

const latest = JSON.parse(await readFile(path.join(out, 'updates/index-v1/latest.json'), 'utf8'));
const detailRaw = await readFile(path.join(out, 'updates/index-v1/releases/v0.1.0.json'), 'utf8');
const detail = JSON.parse(detailRaw);
const betaDetailRaw = await readFile(path.join(out, 'updates/index-v1/releases/v0.1.1-beta.1.json'), 'utf8');
const betaDetail = JSON.parse(betaDetailRaw);

assert(latest.channels.stable.latest === 'v0.1.0', 'stable channel latest mismatch');
assert(latest.channels.beta.latest === 'v0.1.1-beta.1', 'beta channel latest mismatch');
assert(!('normalized_version' in detail), 'release detail must not expose normalized_version');
assert(detail.signature_assets.ed25519.name === 'checksums.txt.sig', 'missing raw signature asset');
assert(detail.signature_assets.sshsig.name === 'checksums.txt.sshsig', 'missing sshsig asset');
assert(detail.assets[0].name === 'netsgo_0.1.0_linux_amd64.tar.gz', 'archive asset missing');
assert(betaDetail.assets[0].arch === 'armv7', 'armv7 asset arch mismatch');
assert(betaDetail.assets[0].urls.every((entry) => entry.provider !== 'cnb'), 'failed CNB archive upload should not produce CNB URL');
assert(betaDetail.checksum_asset.urls.some((entry) => entry.provider === 'cnb'), 'successful CNB checksum upload should produce CNB URL');

console.log('ok');
